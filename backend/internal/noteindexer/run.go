package noteindexer

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	"event-driven-context/internal/notes"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Options struct {
	ProjectID, AgentPath string
	Timeout              time.Duration
}
type Result struct {
	Noop                      bool
	Reason                    string
	ProcessedEvents           int
	ThroughSequence, Revision int64
}

func Run(ctx context.Context, client Client, opts Options) (result Result, err error) {
	if client == nil || opts.ProjectID == "" {
		return result, fmt.Errorf("project and client required")
	}
	if opts.Timeout == 0 {
		opts.Timeout = 5 * time.Minute
	}
	if opts.Timeout < 0 || opts.Timeout > 10*time.Minute {
		return result, fmt.Errorf("organizer timeout must be between zero and10m")
	}
	run, err := client.BeginNotesOrganization(ctx, opts.ProjectID, PromptVersion)
	if err != nil {
		return result, err
	}
	result = Result{Noop: run.Noop, Reason: run.Reason, ProcessedEvents: len(run.Events), ThroughSequence: run.ThroughSequence, Revision: run.Snapshot.Revision}
	if run.Noop {
		return result, nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.CancelNotesOrganization(cleanup, opts.ProjectID, run.RunID)
	}()
	if opts.AgentPath == "" {
		return result, fmt.Errorf("--agent-command must point to Muse Code")
	}
	if run.RunID == "" || run.Snapshot.ProjectID != opts.ProjectID {
		return result, fmt.Errorf("invalid organizer snapshot")
	}
	d := &draft{client: client, project: opts.ProjectID, run: run, files: map[string]string{}, read: map[string]bool{}, accounted: map[string]string{}}
	for _, f := range run.Snapshot.Files {
		d.files[f.Path] = f.Content
	}
	seedDefaults(d.files)
	work, err := os.MkdirTemp("", "edc-notes-agent-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(work)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return result, err
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		listener.Close()
		return result, err
	}
	secret := hex.EncodeToString(random[:])
	mcpServer := d.server()
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	server := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, notes.MaxFileBytes+16384)
		stream.ServeHTTP(w, r)
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	url := "http://" + listener.Addr().String() + "/mcp"
	promptData, _ := json.Marshal(map[string]any{"project_id": opts.ProjectID, "timezone": run.Timezone, "after_sequence": run.AfterSequence, "through_sequence": run.ThroughSequence, "batch_events": len(run.Events), "task": "Read the organization policies and relevant evidence with the provided tools; update the draft and finish."})
	// Keep the fixed developer prompt and the project-scoped MCP boundary.
	// The credential is resolved from an environment variable, never argv.
	promptPath := work + "/prompt.json"
	if err = os.WriteFile(promptPath, promptData, 0600); err != nil {
		return result, err
	}
	serverConfig, _ := json.Marshal(map[string]any{
		"transport": "streamable_http", "url": url, "enabled": true, "mode": "required",
		"headers": map[string]string{"Authorization": "${EDC_NOTES_AUTHORIZATION}"},
	})
	developerPrompt, _ := json.Marshal(systemPrompt)
	args := []string{"exec", "--json", "--model", Model, "--contributor-mode", "off",
		"--reasoning-effort", Reasoning, "--workspace", work, "--prompt-file", promptPath,
		"--no-session-log", "--no-foreign-personal-context", "--disable-shell", "--disable-write",
		"--disable-web-tools", "--disable-reminders", "--disable-approval", "--max-model-steps", "64",
		"-c", "run.developer_prompt=" + string(developerPrompt),
		"-c", `run.toolset=["mcp__notes__*"]`, "-c", "mcp_servers.notes=" + string(serverConfig)}

	if err = execute(ctx, opts.Timeout, opts.AgentPath, args, string(promptData), work, secret); err != nil {
		return result, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.finished {
		return result, fmt.Errorf("Muse exited without successfully validating the draft with finish")
	}
	old := map[string]string{}
	for _, f := range run.Snapshot.Files {
		old[f.Path] = f.Content
	}
	input := notes.PublishInput{RunID: run.RunID, Files: []notes.WriteFile{}, Removed: []string{}, Accounted: []notes.AccountedEvent{}}
	for _, f := range d.all() {
		if text, ok := old[f.Path]; !ok || text != f.Content {
			input.Files = append(input.Files, f)
		}
		delete(old, f.Path)
	}
	for p := range old {
		input.Removed = append(input.Removed, p)
	}
	sort.Strings(input.Removed)
	for _, e := range run.Events {
		input.Accounted = append(input.Accounted, notes.AccountedEvent{ID: e.ID, Disposition: d.accounted[e.ID]})
	}
	out, err := client.PublishNotesOrganization(ctx, opts.ProjectID, input)
	if err != nil {
		return result, err
	}
	result.Revision = out.Revision
	result.ThroughSequence = out.ThroughSequence
	return result, nil
}

func execute(ctx context.Context, timeout time.Duration, command string, args []string, prompt, dir, secret string) error {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.Command(command, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	for _, key := range []string{"PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "TZ", "TMPDIR"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "EDC_NOTES_AUTHORIZATION=Bearer "+secret, "MUSE_LOGIN=0", "MUSE_NO_AUTO_UPDATE=1")
	var stderr, stdout bytes.Buffer
	cmd.Stderr = &cappedWriter{buffer: &stderr, left: 64 << 10}
	cmd.Stdout = &cappedWriter{buffer: &stdout, left: 4 << 20}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("Muse organizer failed: %w: %s", err, clip(strings.ReplaceAll(stderr.String(), secret, "[redacted]"), 2000))
		}
		return requireCompleted(stdout.String())
	case <-runCtx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
		return runCtx.Err()
	}
}

// A process exiting zero is insufficient: require Muse's terminal run status.
func requireCompleted(events string) error {
	terminal := ""
	for _, line := range strings.Split(events, "\n") {
		var event struct {
			PayloadType string `json:"payload_type"`
			Payload     struct {
				Terminal string `json:"terminal"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &event) == nil && strings.HasPrefix(event.PayloadType, "run.terminal.") {
			terminal = event.Payload.Terminal
		}
	}
	if terminal != "completed" {
		return fmt.Errorf("Muse did not emit a completed terminal event")
	}
	return nil
}

type cappedWriter struct {
	buffer *bytes.Buffer
	left   int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if len(p) > w.left {
		return 0, fmt.Errorf("Muse output limit exceeded")
	}
	w.left -= len(p)
	if w.buffer != nil {
		return w.buffer.Write(p)
	}
	return len(p), nil
}
