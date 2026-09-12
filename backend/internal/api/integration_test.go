package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"event-driven-context/internal/core"
	"event-driven-context/internal/v2"
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
	in := core.Credentials{Username: name, Email: name + "@example.invalid", Password: "integration-password-123"}
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

func apiWAV(totalBytes int) []byte {
	if totalBytes < 45 {
		totalBytes = 45
	}
	data := make([]byte, totalBytes)
	copy(data[:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(totalBytes-8))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], 8000)
	binary.LittleEndian.PutUint32(data[28:32], 16000)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], uint32(totalBytes-44))
	return data
}

func mediaMultipart(t *testing.T, fields map[string]string, filename, mediaType string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := new(bytes.Buffer)
	w := multipart.NewWriter(body)
	for name, value := range fields {
		if err := w.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": filename}))
	header.Set("Content-Type", mediaType)
	part, err := w.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return body, w.FormDataContentType()
}

func postMedia(t *testing.T, c *Client, fields map[string]string, filename, mediaType string, data []byte) (*http.Response, []byte) {
	t.Helper()
	body, contentType := mediaMultipart(t, fields, filename, mediaType, data)
	r, err := http.NewRequest(http.MethodPost, c.BaseURL+"/v1/media-events", body)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+c.Token)
	r.Header.Set("Content-Type", contentType)
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return res, out
}

type delayedReader struct {
	once  sync.Once
	delay time.Duration
	inner io.Reader
}

func (r *delayedReader) Read(p []byte) (int, error) {
	r.once.Do(func() { time.Sleep(r.delay) })
	return r.inner.Read(p)
}

func TestMediaUploadExtendsServerWriteDeadline(t *testing.T) {
	s, err := core.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	h := httptest.NewUnstartedServer(Handler(s, nil))
	h.Config.WriteTimeout = 200 * time.Millisecond
	h.Start()
	t.Cleanup(h.Close)
	credentials := core.Credentials{Username: "slow-media", Email: "slow-media@example.invalid", Password: "integration-password-123"}
	user, err := s.Register(context.Background(), credentials)
	if err != nil {
		t.Fatal(err)
	}
	login, err := s.Login(context.Background(), credentials)
	if err != nil {
		t.Fatal(err)
	}
	alice, err := NewClient(h.URL, login.Token)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(core.WithUser(context.Background(), user.ID), core.ProjectInput{Name: "slow upload"})
	if err != nil {
		t.Fatal(err)
	}
	body, contentType := mediaMultipart(t, map[string]string{"project_id": p.ID}, "voice.wav", "audio/wav", apiWAV(128))
	request, err := http.NewRequest(http.MethodPost, h.URL+"/v1/media-events", &delayedReader{delay: 500 * time.Millisecond, inner: body})
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+alice.Token)
	request.Header.Set("Content-Type", contentType)
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("slow media response missed extended write deadline: %v", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("slow media HTTP %d: %s (%v)", response.StatusCode, responseBody, err)
	}
}

func TestMediaUploadIdempotencyAndAuthenticatedRawContent(t *testing.T) {
	_, h := server(t)
	alice := account(t, h.URL, "alice")
	outsider := account(t, h.URL, "outsider")
	p, err := alice.CreateProject(context.Background(), core.ProjectInput{Name: "media"})
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{
		"project_id":      p.ID,
		"metadata":        `{"capture":"ios","language":"zh"}`,
		"occurred_at":     "2026-09-12T08:00:00+08:00",
		"idempotency_key": "capture-1",
	}
	original := apiWAV(512)
	res, body := postMedia(t, alice, fields, `语音 "one".wav`, "audio/x-wav", original)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("upload HTTP %d: %s", res.StatusCode, body)
	}
	var event core.Event
	if err = json.Unmarshal(body, &event); err != nil || event.Content.File == nil || event.Content.File.MediaType != "audio/wav" || event.Content.File.SizeBytes != len(original) {
		t.Fatalf("upload response: %+v %v", event, err)
	}
	res, body = postMedia(t, alice, fields, `语音 "one".wav`, "audio/x-wav", original)
	var retry core.Event
	if res.StatusCode != http.StatusOK || json.Unmarshal(body, &retry) != nil || retry.ID != event.ID {
		t.Fatalf("retry response HTTP %d: %s", res.StatusCode, body)
	}
	changed := bytes.Clone(original)
	changed[len(changed)-1] = 1
	res, body = postMedia(t, alice, fields, `语音 "one".wav`, "audio/x-wav", changed)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("changed retry HTTP %d: %s", res.StatusCode, body)
	}

	raw, err := http.NewRequest(http.MethodGet, h.URL+"/v1/files/"+event.Content.File.ID+"/content", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw.Header.Set("Authorization", "Bearer "+alice.Token)
	rawResponse, err := http.DefaultClient.Do(raw)
	if err != nil {
		t.Fatal(err)
	}
	rawBody, err := io.ReadAll(rawResponse.Body)
	rawResponse.Body.Close()
	if err != nil || rawResponse.StatusCode != http.StatusOK || !bytes.Equal(rawBody, original) {
		t.Fatalf("raw roundtrip HTTP %d bytes=%d: %v", rawResponse.StatusCode, len(rawBody), err)
	}
	if rawResponse.Header.Get("Content-Type") != "audio/wav" || rawResponse.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(rawResponse.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("unsafe raw headers: %#v", rawResponse.Header)
	}
	raw.Header.Set("Authorization", "Bearer "+outsider.Token)
	denied, err := http.DefaultClient.Do(raw)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, denied.Body)
	denied.Body.Close()
	if denied.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-project raw read HTTP %d", denied.StatusCode)
	}

	res, body = postMedia(t, alice, map[string]string{"project_id": p.ID}, "attack.mp3", "audio/mpeg", []byte("<!doctype html><script>alert(1)</script>"))
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("HTML media HTTP %d: %s", res.StatusCode, body)
	}
	events, err := alice.QueryEvents(context.Background(), core.QueryInput{ProjectID: p.ID})
	if err != nil || len(events.Events) != 1 {
		t.Fatalf("failed upload left visible event: %+v %v", events, err)
	}
}

func TestMediaUploadFailureAndBase64Boundary(t *testing.T) {
	_, h := server(t)
	alice := account(t, h.URL, "alice")
	p, err := alice.CreateProject(context.Background(), core.ProjectInput{Name: "media-boundary"})
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{"project_id": p.ID, "idempotency_key": "large-media"}
	data := apiWAV(core.MaxContentBytes + 2)
	res, body := postMedia(t, alice, fields, "large.wav", "audio/wav", data)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("binary media above base64 limit HTTP %d: %s", res.StatusCode, body)
	}
	var event core.Event
	if err = json.Unmarshal(body, &event); err != nil {
		t.Fatal(err)
	}
	_, err = alice.GetFile(context.Background(), core.FileRef{FileID: event.Content.File.ID})
	var appErr *core.Error
	if !errors.As(err, &appErr) || appErr.Code != "too_large" {
		t.Fatalf("large media leaked through base64 API: %v", err)
	}

	broken, contentType := mediaMultipart(t, map[string]string{"project_id": p.ID, "idempotency_key": "truncated"}, "voice.wav", "audio/wav", apiWAV(128))
	brokenBytes := broken.Bytes()[:broken.Len()-12]
	r, err := http.NewRequest(http.MethodPost, h.URL+"/v1/media-events", bytes.NewReader(brokenBytes))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+alice.Token)
	r.Header.Set("Content-Type", contentType)
	brokenResponse, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, brokenResponse.Body)
	brokenResponse.Body.Close()
	if brokenResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("truncated multipart HTTP %d", brokenResponse.StatusCode)
	}
	events, err := alice.QueryEvents(context.Background(), core.QueryInput{ProjectID: p.ID})
	if err != nil || len(events.Events) != 1 {
		t.Fatalf("truncated multipart left ghost event: %+v %v", events, err)
	}
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
	if err != nil || len(tools.Tools) != 10 {
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
		if (tool.Name == "query_events" || tool.Name == "query_context") && !tool.Annotations.ReadOnlyHint {
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
	contextResult := call[core.ContextQueryResult](t, a, "query_context", core.ContextQueryInput{ProjectID: p.ID, Query: "Bob 需求", MaxOutputBytes: 24000, IncludeSuggestions: true})
	if len(contextResult.Evidence) != 1 || contextResult.Evidence[0].EventID != e.ID || contextResult.Evidence[0].State != "active_evidence" || contextResult.Coverage.ConflictDetection != "explicit_update_forks_only" {
		t.Fatalf("MCP context proxy: %+v", contextResult)
	}
	httpContext, err := alice.QueryContext(ctx, core.ContextQueryInput{ProjectID: p.ID, Query: "这是 Bob", MaxOutputBytes: 1024})
	if err != nil || len(httpContext.Evidence) != 1 || httpContext.Evidence[0].EventID != e.ID {
		t.Fatalf("HTTP context query: %+v %v", httpContext, err)
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
	}{{"get_event", core.EventRef{EventID: e.ID}}, {"query_events", core.QueryInput{ProjectID: p.ID}}, {"query_context", core.ContextQueryInput{ProjectID: p.ID, Query: "需求"}}, {"list_metadata", core.MetadataInput{ProjectID: p.ID}}, {"get_file", core.FileRef{FileID: file.Content.File.ID}}, {"record_event", in}} {
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
		{"POST", "/v1/context/query", fmt.Sprintf(`{"project_id":%q,"query":"test"}`, p.ID), "", "", 401},
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
	dataRoot := t.TempDir()
	store, err := core.Open(filepath.Join(dataRoot, "identity.db"), filepath.Join(dataRoot, "legacy"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := v2.New(store, filepath.Join(dataRoot, "v2"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	h := httptest.NewServer(V2Handler(store, service, nil))
	t.Cleanup(h.Close)

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
	type cliResult struct {
		stdout []byte
		stderr string
		err    error
	}
	cliRawIn := func(workdir, profile, stdin string, args ...string) cliResult {
		t.Helper()
		argv := append([]string{"--server", h.URL, "--config", filepath.Join(dir, profile+".json")}, args...)
		cmd := exec.Command(bin, argv...)
		cmd.Env = env
		cmd.Stdin = strings.NewReader(stdin)
		cmd.Dir = workdir
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, runErr := cmd.Output()
		if strings.Contains(string(out), "integration-password") || strings.Contains(stderr.String(), "integration-password") || strings.Contains(string(out), `"token":`) {
			t.Fatal("CLI leaked credentials")
		}
		return cliResult{stdout: out, stderr: stderr.String(), err: runErr}
	}
	cliRaw := func(profile, stdin string, args ...string) cliResult {
		return cliRawIn("", profile, stdin, args...)
	}
	cli := func(profile, stdin string, args ...string) []byte {
		t.Helper()
		result := cliRaw(profile, stdin, args...)
		if result.err != nil {
			t.Fatalf("CLI %v failed: %v %s", args, result.err, result.stderr)
		}
		return result.stdout
	}
	cliIn := func(workdir, profile, stdin string, args ...string) []byte {
		t.Helper()
		result := cliRawIn(workdir, profile, stdin, args...)
		if result.err != nil {
			t.Fatalf("CLI %v failed: %v %s", args, result.err, result.stderr)
		}
		return result.stdout
	}
	tokens := map[string]string{}
	for _, name := range []string{"alice", "bob"} {
		cli(name, "integration-password-123\n", "register", "--username", name, "--email", name+"@example.invalid", "--password-stdin")
		loginOut := cli(name, "integration-password-123\n", "login", "--username", name, "--password-stdin")
		configPath := filepath.Join(dir, name+".json")
		info, err := os.Stat(configPath)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("credential file not private", err)
		}
		var saved map[string]string
		configBytes, err := os.ReadFile(configPath)
		if err != nil || json.Unmarshal(configBytes, &saved) != nil || saved["token"] == "" {
			t.Fatal("credential file invalid", err)
		}
		tokens[name] = saved["token"]
		if bytes.Contains(loginOut, []byte(saved["token"])) {
			t.Fatal("login output leaked token")
		}
	}
	var p core.Project
	if err := json.Unmarshal(cli("alice", "", "project", "create", "--name", "CLI 团队项目"), &p); err != nil {
		t.Fatal(err)
	}
	cli("alice", "", "project", "add-member", "--project", p.ID, "--username", "bob")
	var members core.Members
	if err = json.Unmarshal(cli("bob", "", "project", "members", "--project", p.ID), &members); err != nil || len(members.Members) != 2 {
		t.Fatal("CLI members failed", err)
	}
	projectDir := filepath.Join(dir, "workspace")
	if err = os.Mkdir(projectDir, 0700); err != nil {
		t.Fatal(err)
	}
	var binding struct {
		ProjectID string `json:"project_id"`
	}
	if err = json.Unmarshal(cliIn(projectDir, "alice", "", "link", p.ID), &binding); err != nil || binding.ProjectID != p.ID {
		t.Fatalf("link failed: %#v %v", binding, err)
	}
	if err = json.Unmarshal(cliIn(projectDir, "alice", "", "link"), &binding); err != nil || binding.ProjectID != p.ID {
		t.Fatalf("show link failed: %#v %v", binding, err)
	}

	// JSON input keeps large integers exact and canonicalizes a valid client UUID.
	upperID := "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"
	upperEvent := fmt.Sprintf(`{"id":%q,"type":"note","content":{"kind":"text","text":"large integer"},"metadata":{"count":9007199254740993},"source":{"channel":"cli"}}`, upperID)
	var upperWrite v2.RecordEventsResult
	if err = json.Unmarshal(cli("bob", upperEvent, "push", "--project", p.ID, "--json"), &upperWrite); err != nil || len(upperWrite.Results) != 1 || upperWrite.Results[0].ID != strings.ToLower(upperID) || upperWrite.Results[0].Status != "created" {
		t.Fatalf("uppercase UUID write failed: %#v %v", upperWrite, err)
	}
	var duplicate v2.RecordEventsResult
	if err = json.Unmarshal(cli("bob", upperEvent, "push", "--project", p.ID, "--json"), &duplicate); err != nil || duplicate.Results[0].Status != "duplicate" || duplicate.Results[0].Sequence != upperWrite.Results[0].Sequence {
		t.Fatalf("uppercase UUID retry failed: %#v %v", duplicate, err)
	}
	var exact v2.Event
	if err = json.Unmarshal(cli("alice", "", "get", "--project", p.ID, strings.ToLower(upperID)), &exact); err != nil || string(exact.Metadata["count"]) != "9007199254740993" {
		t.Fatalf("large integer changed: %s %v", exact.Metadata["count"], err)
	}

	textPath := filepath.Join(dir, "note.txt")
	data := []byte("CLI 文件原始内容\r\n")
	if err := os.WriteFile(textPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	var fileWrite v2.RecordEventsResult
	if err := json.Unmarshal(cli("bob", "", "push", "--project", p.ID, "--type", "note", "--file", textPath, "--media-type", "text/plain", "--metadata", `{"kind":"artifact","tags":["team"]}`), &fileWrite); err != nil || len(fileWrite.Results) != 1 || fileWrite.Results[0].Status != "created" {
		t.Fatal(err)
	}
	var fileEvent v2.Event
	if err = json.Unmarshal(cli("alice", "", "get", "--project", p.ID, fileWrite.Results[0].ID), &fileEvent); err != nil || fileEvent.Actor.Username != "bob" || fileEvent.Content.FileID == "" {
		t.Fatal("CLI author mismatch")
	}
	outPath := filepath.Join(dir, "download.txt")
	cli("alice", "", "file", "get", "--project", p.ID, "-o", outPath, fileEvent.Content.FileID)
	download, err := os.ReadFile(outPath)
	if err != nil || !bytes.Equal(data, download) {
		t.Fatal("CLI file roundtrip failed", err)
	}
	blocked := cliRaw("alice", "", "file", "get", "--project", p.ID, "-o", outPath, fileEvent.Content.FileID)
	if blocked.err == nil || !strings.Contains(blocked.stderr, "output already exists") {
		t.Fatalf("existing target was not rejected: %v %s", blocked.err, blocked.stderr)
	}
	if after, readErr := os.ReadFile(outPath); readErr != nil || !bytes.Equal(after, data) {
		t.Fatal("existing target changed", readErr)
	}

	var q v2.EventsPage
	if err = json.Unmarshal(cli("alice", "", "query", "--project", p.ID, "--metadata", `{"count":9007199254740993}`), &q); err != nil || len(q.Events) != 1 || q.Events[0].ID != strings.ToLower(upperID) {
		t.Fatal("CLI query failed", err)
	}
	cli("alice", "", "metadata", "--project", p.ID, "--key", "source")

	preview := cliRawIn(projectDir, "alice", "", "setup", "claude-code")
	if preview.err != nil || !bytes.Contains(preview.stdout, []byte(`"applied": false`)) {
		t.Fatalf("setup preview: %v %s %s", preview.err, preview.stderr, preview.stdout)
	}
	if _, statErr := os.Stat(filepath.Join(projectDir, ".mcp.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("setup preview changed files: %v", statErr)
	}
	applied := cliRawIn(projectDir, "alice", "", "setup", "--apply", "--enable-shared-hooks", "claude-code")
	if applied.err != nil || !bytes.Contains(applied.stdout, []byte(`"applied": true`)) || !strings.Contains(applied.stderr, `"preview"`) {
		t.Fatalf("setup apply: %v %s %s", applied.err, applied.stderr, applied.stdout)
	}
	settings, err := os.ReadFile(filepath.Join(projectDir, ".claude", "settings.local.json"))
	if err != nil || !bytes.Contains(settings, []byte("UserPromptSubmit")) {
		t.Fatalf("setup hooks missing: %s %v", settings, err)
	}
	hookInput := fmt.Sprintf(`{"session_id":"session-one","prompt_id":"prompt-one","cwd":%q,"hook_event_name":"UserPromptSubmit","prompt":"hook text"}`, projectDir)
	hooked := cliRawIn(projectDir, "alice", hookInput, "hook", "claude-code")
	if hooked.err != nil {
		t.Fatalf("hook failed: %v %s", hooked.err, hooked.stderr)
	}
	var hookPage v2.EventsPage
	if err = json.Unmarshal(cliIn(projectDir, "alice", "", "query", "--source", `{"channel":"hook"}`), &hookPage); err != nil || len(hookPage.Events) != 1 || hookPage.Events[0].Content.Text != "hook text" {
		t.Fatalf("hook event roundtrip: %#v %v", hookPage, err)
	}
	var linkedStatus struct {
		HooksEnabled  bool `json:"hooks_enabled"`
		OutboxPending int  `json:"outbox_pending"`
	}
	if err = json.Unmarshal(cliIn(projectDir, "alice", "", "status"), &linkedStatus); err != nil || !linkedStatus.HooksEnabled || linkedStatus.OutboxPending != 0 {
		t.Fatalf("capture status: %#v %v", linkedStatus, err)
	}

	// Per-item failure prints the complete receipt and exits non-zero; the
	// successful item remains queryable because batches are deliberately partial.
	partialInput := `{"id":"BBBBBBBB-BBBB-4BBB-8BBB-BBBBBBBBBBBB","type":"note","content":{"kind":"text","text":"kept"},"source":{"channel":"cli"}}
{"id":"CCCCCCCC-CCCC-4CCC-8CCC-CCCCCCCCCCCC","type":"unknown","content":{"kind":"text","text":"rejected"},"source":{"channel":"cli"}}
`
	partialRun := cliRawIn(projectDir, "alice", partialInput, "push", "--jsonl")
	var partial v2.RecordEventsResult
	if partialRun.err == nil || json.Unmarshal(partialRun.stdout, &partial) != nil || len(partial.Results) != 2 || partial.Results[0].Status != "created" || partial.Results[1].Status != "invalid" {
		t.Fatalf("partial batch semantics: err=%v stderr=%s out=%s", partialRun.err, partialRun.stderr, partialRun.stdout)
	}
	var kept v2.Event
	if err = json.Unmarshal(cli("alice", "", "get", "--project", p.ID, strings.ToLower("BBBBBBBB-BBBB-4BBB-8BBB-BBBBBBBBBBBB")), &kept); err != nil || kept.Content.Text != "kept" {
		t.Fatal("partial batch lost successful item", err)
	}
	var queued struct {
		Items []json.RawMessage `json:"items"`
	}
	if err = json.Unmarshal(cliIn(projectDir, "alice", "", "outbox", "list"), &queued); err != nil || len(queued.Items) != 1 {
		t.Fatalf("partial failure was not retained: items=%d err=%v", len(queued.Items), err)
	}

	manifestPath := filepath.Join(dir, "manifest.json")
	manifest := v2.Manifest{ID: "project-brief", Version: "0.1.0", Name: "Project brief", State: []v2.StateDeclaration{{Key: "current"}}, Processor: json.RawMessage(`{"entry":{"type":"agent"}}`), Permissions: v2.Permissions{ReadEvents: []string{"note"}, WriteState: []string{"current"}}}
	manifestBytes, _ := json.Marshal(manifest)
	if err = os.WriteFile(manifestPath, manifestBytes, 0600); err != nil {
		t.Fatal(err)
	}
	pluginTokenPath := filepath.Join(dir, "project-brief.token")
	installOut := cli("alice", "", "plugin", "install", "--project", p.ID, "--manifest", manifestPath, "--token-file", pluginTokenPath)
	for _, token := range tokens {
		if bytes.Contains(installOut, []byte(token)) {
			t.Fatal("plugin command leaked a user token")
		}
	}
	if bytes.Contains(installOut, []byte(`"token":`)) || !bytes.Contains(installOut, []byte(`"token_returned": true`)) {
		t.Fatalf("plugin token disclosure contract: %s", installOut)
	}
	if tokenInfo, statErr := os.Stat(pluginTokenPath); statErr != nil || tokenInfo.Mode().Perm() != 0600 {
		t.Fatalf("plugin token file is not private: %v", statErr)
	}
	contentPath := filepath.Join(dir, "brief.md")
	if err = os.WriteFile(contentPath, []byte("# Current\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var state v2.State
	if err = json.Unmarshal(cli("alice", "", "state", "put", "--project", p.ID, "--content", contentPath, "--based-on", strconv.FormatInt(kept.Sequence, 10), "--expected-version", "0", "--as-plugin", "project-brief", "--ref", kept.ID, "project-brief/current"), &state); err != nil || state.Version != 1 || state.Key != "project-brief/current" {
		t.Fatalf("state put: %#v %v", state, err)
	}
	cli("alice", "", "state", "list", "--project", p.ID, "--prefix", "project-brief/")
	cli("alice", "", "state", "get", "--project", p.ID, "--version", "1", "project-brief/current")
	cli("alice", "", "plugin", "config", "--project", p.ID, "--plugin", "project-brief", "--expected-revision", "1", "--config", `{"prompt":"updated"}`)
	cli("alice", "", "plugin", "pause", "--project", p.ID, "--plugin", "project-brief")
	cli("alice", "", "plugin", "resume", "--project", p.ID, "--plugin", "project-brief")
	var runRequest v2.ManualRunRequest
	if err = json.Unmarshal(cli("alice", "", "plugin", "rerun", "--project", p.ID, "--plugin", "project-brief", "--request-id", "DDDDDDDD-DDDD-4DDD-8DDD-DDDDDDDDDDDD", "--source-event", kept.ID), &runRequest); err != nil || runRequest.Status != "accepted" {
		t.Fatalf("manual run must report accepted only: %#v %v", runRequest, err)
	}

	pullOut := cli("alice", "", "pull", "--project", p.ID, "--after", "0")
	pulled := 0
	for _, line := range bytes.Split(bytes.TrimSpace(pullOut), []byte{'\n'}) {
		var event v2.Event
		if err = json.Unmarshal(line, &event); err != nil {
			t.Fatalf("pull JSONL: %s %v", line, err)
		}
		pulled++
	}
	if pulled < 3 {
		t.Fatalf("pull returned %d events", pulled)
	}
	if result := cliRaw("alice", "", "pull", "--project", p.ID, "--after", "0", "--follow"); result.err == nil || !strings.Contains(result.stderr, "not implemented") {
		t.Fatalf("pull --follow pretended success: %v %s", result.err, result.stderr)
	}
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
	if err != nil || len(tools.Tools) != 13 {
		t.Fatal("stdio discovery failed", err)
	}
	got := call[v2.Event](t, session, "get_event", map[string]any{"project_id": p.ID, "event_id": fileEvent.ID})
	if got.ID != fileEvent.ID || got.Sequence != fileEvent.Sequence || got.Content.FileID != fileEvent.Content.FileID {
		t.Fatal("stdio read differs from CLI/HTTP event")
	}
	states := call[v2.StatesResult](t, session, "get_state", map[string]any{"project_id": p.ID, "keys": []string{"missing/state", "project-brief/current"}})
	if len(states.States) != 1 || states.States[0].Key != "project-brief/current" || states.LatestSequence != state.BasedOnSequence+state.Lag {
		t.Fatalf("stdio multi-state missing-key semantics: %#v", states)
	}
	invalidVersion, callErr := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_state", Arguments: map[string]any{"project_id": p.ID, "keys": []string{"project-brief/current"}, "version": 0}})
	if callErr != nil || !invalidVersion.IsError {
		t.Fatalf("stdio accepted invalid state version: %#v %v", invalidVersion, callErr)
	}
	stdioID := "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	stdioInput := v2.EventInput{ID: stdioID, Type: "note", Content: v2.EventContent{Kind: "text", Text: "stdio 追加"}, Source: object(`{"channel":"cli"}`)}
	recorded := call[v2.RecordEventsResult](t, session, "record_events", map[string]any{"project_id": p.ID, "events": []v2.EventInput{stdioInput}})
	if len(recorded.Results) != 1 || recorded.Results[0].Status != "created" {
		t.Fatalf("stdio record %#v", recorded)
	}
	var stdioEvent v2.Event
	if err = json.Unmarshal(cli("alice", "", "get", "--project", p.ID, stdioID), &stdioEvent); err != nil || stdioEvent.Actor.Username != "bob" || stdioEvent.Sequence != recorded.Results[0].Sequence {
		t.Fatal("stdio identity or CLI roundtrip mismatch", err)
	}
	conflict := stdioInput
	conflict.Content.Text = "different"
	partialMCP := call[v2.RecordEventsResult](t, session, "record_events", map[string]any{"project_id": p.ID, "events": []v2.EventInput{{ID: "ffffffff-ffff-4fff-8fff-ffffffffffff", Type: "note", Content: v2.EventContent{Kind: "text", Text: "created before conflict"}, Source: object(`{"channel":"cli"}`)}, conflict}})
	if len(partialMCP.Results) != 2 || partialMCP.Results[0].Status != "created" || partialMCP.Results[1].Status != "conflict" {
		t.Fatalf("stdio lost partial results: %#v", partialMCP)
	}
	missing, callErr := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_event", Arguments: map[string]any{"project_id": p.ID, "event_id": "99999999-9999-4999-8999-999999999999"}})
	if callErr != nil || !missing.IsError {
		t.Fatalf("stdio missing event error: %#v %v", missing, callErr)
	}
	missingJSON, _ := json.Marshal(missing.Content)
	if !bytes.Contains(missingJSON, []byte("not_found")) {
		t.Fatalf("stdio erased structured error: %s", missingJSON)
	}
	cli("bob", "", "logout")
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_projects", Arguments: core.Empty{}})
	if err == nil && !res.IsError {
		t.Fatal("stdio ignored logout")
	}
	cli("alice", "", "plugin", "remove", "--project", p.ID, "--plugin", "project-brief")
}
