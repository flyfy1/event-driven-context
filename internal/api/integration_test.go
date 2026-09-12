package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"event-driven-context/internal/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func server(t *testing.T) (*core.Store, *httptest.Server) {
	t.Helper()
	s, err := core.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	h := httptest.NewServer(Handler(s, nil))
	t.Cleanup(h.Close)
	return s, h
}
func account(t *testing.T, base, name string) *Client {
	t.Helper()
	c, err := NewClient(base, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	in := core.Credentials{Username: name, Password: "integration-password-123"}
	if _, err = c.Register(ctx, in); err != nil {
		t.Fatal(err)
	}
	login, err := c.Login(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	c.Token = login.Token
	return c
}

type tokenTransport struct{ token string }

func (tr tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	q := r.Clone(r.Context())
	q.Header.Set("Authorization", "Bearer "+tr.token)
	return http.DefaultTransport.RoundTrip(q)
}
func connect(t *testing.T, c *Client) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := mcp.NewClient(&mcp.Implementation{Name: "integration", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: c.BaseURL + "/mcp", HTTPClient: &http.Client{Transport: tokenTransport{c.Token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func call[T any](t *testing.T, s *mcp.ClientSession, name string, args any) T {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		b, _ := json.Marshal(res.Content)
		t.Fatalf("%s tool error: %s", name, b)
	}
	// The SDK client decodes structuredContent into float64s. The protocol's
	// required text fallback lets us inspect the exact JSON sent on the wire.
	b := []byte(res.Content[0].(*mcp.TextContent).Text)
	var out T
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatalf("%s output: %v", name, err)
	}
	return out
}
func object(s string) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		panic(err)
	}
	return m
}

func TestStandardMCPHTTPSharedWorkflow(t *testing.T) {
	_, h := server(t)
	alice := account(t, h.URL, "alice")
	bob := account(t, h.URL, "bob")
	outsider := account(t, h.URL, "outsider")
	a := connect(t, alice)
	b := connect(t, bob)
	x := connect(t, outsider)
	ctx := context.Background()
	tools, err := a.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 9 {
		t.Fatalf("tools/list: %+v %v", tools, err)
	}
	for _, tool := range tools.Tools {
		if tool.Annotations == nil {
			t.Fatal("missing tool annotations")
		}
		requiredScope := core.ScopeWrite
		if tool.Annotations.ReadOnlyHint {
			requiredScope = core.ScopeRead
		}
		if !strings.Contains(tool.Description, "OAuth scope: "+requiredScope) {
			t.Fatalf("tool %s description missing accurate scope", tool.Name)
		}
		if tool.Name == "query_events" && !tool.Annotations.ReadOnlyHint {
			t.Fatal("query not read-only")
		}
	}
	p := call[core.Project](t, a, "create_project", core.ProjectInput{Name: "共同 context"})
	member := call[core.User](t, a, "add_project_member", core.MemberInput{ProjectID: p.ID, Username: "bob"})
	text := "这是 Bob 追加的需求"
	in := core.RecordInput{ProjectID: p.ID, Content: core.ContentInput{Kind: "text", Text: &text}, Metadata: object(`{"source":"cli","iteration":1,"verified":true,"pending":null,"nested":{"owner":"team"},"tags":["go","mcp"],"large":9007199254740993}`), OccurredAt: "2026-09-08T14:00:00Z", IdempotencyKey: "same-request"}
	e := call[core.Event](t, b, "record_event", in)
	if e.ActorUserID != member.ID || e.ActorUsername != "bob" || *e.Content.Text != text {
		t.Fatalf("incorrect event: %+v", e)
	}
	stored, err := bob.GetEvent(ctx, core.EventRef{EventID: e.ID})
	if err != nil || string(stored.Metadata["large"]) != "9007199254740993" || string(e.Metadata["large"]) != "9007199254740993" {
		t.Fatal("MCP silently rounded metadata number", err)
	}
	again := call[core.Event](t, b, "record_event", in)
	if again.ID != e.ID {
		t.Fatal("MCP duplicate retry")
	}
	q := call[core.Events](t, a, "query_events", core.QueryInput{ProjectID: p.ID, Metadata: object(`{"iteration":1,"verified":true,"large":9007199254740993}`)})
	if len(q.Events) != 1 || q.Events[0].ID != e.ID {
		t.Fatal("MCP filters failed")
	}
	fields := call[core.MetadataResult](t, a, "list_metadata", core.MetadataInput{ProjectID: p.ID})
	if len(fields.Fields) != 7 {
		t.Fatalf("metadata fields: %+v", fields)
	}
	key := "source"
	values := call[core.MetadataResult](t, a, "list_metadata", core.MetadataInput{ProjectID: p.ID, Key: &key})
	if len(values.Values) != 1 || string(values.Values[0].Value) != `"cli"` {
		t.Fatal("metadata values failed")
	}
	data := base64.StdEncoding.EncodeToString([]byte("文件内容\r\nUTF-8 原始字节\n"))
	file := call[core.Event](t, b, "record_event", core.RecordInput{ProjectID: p.ID, Content: core.ContentInput{Kind: "file", File: &core.FileInput{Filename: "note.txt", MediaType: "text/plain", DataBase64: data}}})
	got := call[core.FileResult](t, a, "get_file", core.FileRef{FileID: file.Content.File.ID})
	if got.DataBase64 != data {
		t.Fatal("MCP file corrupted")
	}
	for _, tc := range []struct {
		name string
		args any
	}{{"get_event", core.EventRef{EventID: e.ID}}, {"query_events", core.QueryInput{ProjectID: p.ID}}, {"list_metadata", core.MetadataInput{ProjectID: p.ID}}, {"get_file", core.FileRef{FileID: file.Content.File.ID}}, {"record_event", in}} {
		res, err := x.CallTool(ctx, &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
		if err != nil || !res.IsError {
			t.Fatalf("unauthorized %s accepted: %v", tc.name, err)
		}
	}
	res, err := b.CallTool(ctx, &mcp.CallToolParams{Name: "record_event", Arguments: map[string]any{"project_id": p.ID, "actor_user_id": "forged", "content": map[string]string{"kind": "text", "text": "spoof"}}})
	if err == nil && !res.IsError {
		t.Fatal("MCP accepted forged author")
	}
	if err = bob.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	res, err = b.CallTool(ctx, &mcp.CallToolParams{Name: "list_projects", Arguments: core.Empty{}})
	if err == nil && !res.IsError {
		t.Fatal("MCP accepted revoked token")
	}
}

func TestHTTPValidationAuthAndLegacyProtocolNegotiation(t *testing.T) {
	_, h := server(t)
	c := account(t, h.URL, "alice")
	p, err := c.CreateProject(context.Background(), core.ProjectInput{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		method, path, body, token, origin string
		want                              int
	}{
		{"POST", "/v1/events", `{}`, "", "", 401},
		{"POST", "/v1/events", `   `, c.Token, "", 400},
		{"POST", "/v1/events", `null`, c.Token, "", 400},
		{"POST", "/v1/events", `{} {}`, c.Token, "", 400},
		{"POST", "/v1/events", fmt.Sprintf(`{"project_id":%q,"actor_user_id":"forged","content":{"kind":"text","text":"x"}}`, p.ID), c.Token, "", 400},
		{"POST", "/v1/events", strings.Repeat(" ", core.MaxRequestBytes+1), c.Token, "", 413},
		{"GET", "/v1/projects", "", c.Token, "https://evil.example", 403},
		{"POST", "/mcp", `{}`, c.Token, "https://evil.example", 403},
		{"POST", "/mcp", `{}`, "", "", 401},
		{"DELETE", "/v1/events/anything", "", c.Token, "", 405},
		{"PATCH", "/v1/events/anything", `{}`, c.Token, "", 405},
	}
	for i, tc := range cases {
		r, _ := http.NewRequest(tc.method, h.URL+tc.path, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != tc.want {
			t.Errorf("case %d got %d want %d", i, res.StatusCode, tc.want)
		}
	}
	for _, version := range []string{"2025-03-26", "2025-06-18", "2025-11-25"} {
		body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":%q,"capabilities":{},"clientInfo":{"name":"legacy-test","version":"1"}}}`, version)
		r, _ := http.NewRequest("POST", h.URL+"/mcp", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("Authorization", "Bearer "+c.Token)
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Result struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"result"`
		}
		err = json.NewDecoder(res.Body).Decode(&result)
		res.Body.Close()
		if err != nil || res.StatusCode != 200 || result.Result.ProtocolVersion != version {
			t.Fatalf("negotiation %s: HTTP %d %+v %v", version, res.StatusCode, result, err)
		}
	}
}

func TestAllowedBrowserOriginGetsCORSAndPreflight(t *testing.T) {
	s, err := core.Open(filepath.Join(t.TempDir(), "cors.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := httptest.NewServer(Handler(s, []string{"https://context.integ.life"}))
	defer h.Close()
	r, err := http.NewRequest(http.MethodOptions, h.URL+"/v1/projects", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Origin", "https://context.integ.life")
	r.Header.Set("Access-Control-Request-Method", "POST")
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent || res.Header.Get("Access-Control-Allow-Origin") != "https://context.integ.life" || !strings.Contains(res.Header.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("invalid CORS preflight: %d %#v", res.StatusCode, res.Header)
	}
	r, err = http.NewRequest(http.MethodGet, h.URL+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Origin", "https://context.integ.life")
	res, err = http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Header.Get("Access-Control-Allow-Origin") != "https://context.integ.life" {
		t.Fatalf("missing CORS response: %d %#v", res.StatusCode, res.Header)
	}
}

func TestRealCLIAndStdioMCP(t *testing.T) {
	_, h := server(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "edc")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/edc")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %s %v", out, err)
	}
	// Isolate from the developer's login environment and persistent CLI configuration.
	var env []string
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "EDC_") {
			env = append(env, v)
		}
	}
	cli := func(profile, stdin string, args ...string) []byte {
		t.Helper()
		argv := append([]string{"--server", h.URL, "--config", filepath.Join(dir, profile+".json")}, args...)
		cmd := exec.Command(bin, argv...)
		cmd.Env = env
		cmd.Stdin = strings.NewReader(stdin)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("CLI %v failed: %v %s", args, err, stderr.String())
		}
		if strings.Contains(string(out), "integration-password") || strings.Contains(string(out), `"token"`) {
			t.Fatal("CLI leaked credentials")
		}
		return out
	}
	for _, name := range []string{"alice", "bob"} {
		cli(name, "integration-password-123\n", "register", "--username", name, "--password-stdin")
		cli(name, "integration-password-123\n", "login", "--username", name, "--password-stdin")
		info, err := os.Stat(filepath.Join(dir, name+".json"))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("credential file not private", err)
		}
	}
	var p core.Project
	if err := json.Unmarshal(cli("alice", "", "project", "create", "--name", "CLI 团队项目"), &p); err != nil {
		t.Fatal(err)
	}
	cli("alice", "", "project", "add-member", "--project", p.ID, "--username", "bob")
	textPath := filepath.Join(dir, "note.txt")
	data := []byte("CLI 文件原始内容\r\n")
	if err := os.WriteFile(textPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	var e core.Event
	if err := json.Unmarshal(cli("bob", "", "record", "--project", p.ID, "--file", textPath, "--type", "text/plain", "--metadata", `{"source":"cli","tags":["team"]}`, "--idempotency-key", "cli-file"), &e); err != nil {
		t.Fatal(err)
	}
	if e.ActorUsername != "bob" {
		t.Fatal("CLI author mismatch")
	}
	outPath := filepath.Join(dir, "download.txt")
	cli("alice", "", "file", "--id", e.Content.File.ID, "--output", outPath)
	download, err := os.ReadFile(outPath)
	if err != nil || !bytes.Equal(data, download) {
		t.Fatal("CLI file roundtrip failed", err)
	}
	var q core.Events
	if err = json.Unmarshal(cli("alice", "", "query", "--project", p.ID, "--metadata", `{"source":"cli"}`), &q); err != nil || len(q.Events) != 1 || q.Events[0].ID != e.ID {
		t.Fatal("CLI query failed", err)
	}
	cli("alice", "", "metadata", "--project", p.ID, "--key", "source")
	// A real child process speaks MCP on stdout and forwards calls to the same HTTP backend.
	cmd := exec.Command(bin, "--server", h.URL, "--config", filepath.Join(dir, "bob.json"), "mcp")
	cmd.Env = env
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "stdio-integration", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 9 {
		t.Fatal("stdio discovery failed", err)
	}
	text := "stdio 追加"
	recorded := call[core.Event](t, session, "record_event", core.RecordInput{ProjectID: p.ID, Content: core.ContentInput{Kind: "text", Text: &text}, Metadata: object(`{"source":"stdio"}`)})
	if recorded.ActorUsername != "bob" {
		t.Fatal("stdio identity mismatch")
	}
	got := call[core.Event](t, session, "get_event", core.EventRef{EventID: e.ID})
	if got.ID != e.ID {
		t.Fatal("stdio read failed")
	}
	cli("bob", "", "logout")
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_projects", Arguments: core.Empty{}})
	if err == nil && !res.IsError {
		t.Fatal("stdio ignored logout")
	}
}
