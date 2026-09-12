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
	"strconv"
	"strings"
	"syscall"
	"time"

	"event-driven-context/internal/notes"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Options struct {
	ProjectID, CodexPath string
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
	if opts.CodexPath == "" {
		return result, fmt.Errorf("--agent-command must point to Codex")
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
	// Developer instructions are fixed code. Organization policy is fetched as
	// tool data, so users cannot replace the operating boundary by editing notes.
	args := []string{"exec", "--ignore-user-config", "--ignore-rules", "--ephemeral", "--skip-git-repo-check", "--sandbox", "read-only", "--json", "-m", Model, "-c", `model_reasoning_effort="` + Reasoning + `"`, "-c", `approval_policy="never"`, "-c", `web_search="disabled"`, "-c", "developer_instructions=" + strconv.Quote(systemPrompt), "-c", "mcp_servers.notes.url=" + strconv.Quote(url), "-c", `mcp_servers.notes.bearer_token_env_var="EDC_NOTES_RUN_SECRET"`, "-c", `mcp_servers.notes.required=true`, "-c", `mcp_servers.notes.default_tools_approval_mode="approve"`}
	for _, feature := range []string{"shell_tool", "apps", "plugins", "multi_agent", "hooks", "memories", "browser_use", "computer_use", "image_generation", "view_image", "skill_search", "goals", "sleep_tool", "workspace_dependencies", "tool_suggest"} {
		args = append(args, "--disable", feature)
	}
	args = append(args, "--enable", "skip_host_skill_discovery", "-")
	if err = execute(ctx, opts.Timeout, opts.CodexPath, args, string(promptData), work, secret); err != nil {
		return result, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.finished {
		return result, fmt.Errorf("Codex exited without successfully validating the draft with finish")
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
	for _, key := range []string{"PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "TZ", "TMPDIR", "CODEX_HOME"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "EDC_NOTES_RUN_SECRET="+secret)
	var stderr bytes.Buffer
	cmd.Stderr = &cappedWriter{buffer: &stderr, left: 64 << 10}
	cmd.Stdout = &cappedWriter{left: 4 << 20}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("Codex organizer failed: %w: %s", err, clip(strings.ReplaceAll(stderr.String(), secret, "[redacted]"), 2000))
		}
		return nil
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

type cappedWriter struct {
	buffer *bytes.Buffer
	left   int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if len(p) > w.left {
		return 0, fmt.Errorf("Codex output limit exceeded")
	}
	w.left -= len(p)
	if w.buffer != nil {
		return w.buffer.Write(p)
	}
	return len(p), nil
}
