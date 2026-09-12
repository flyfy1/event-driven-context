package mcpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"event-driven-context/internal/core"
	"event-driven-context/internal/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type v2BearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t v2BearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header = r.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

func newV2MCPSession(t *testing.T) (context.Context, *mcp.ClientSession, *core.Store, *v2.Service, core.User, core.Project) {
	t.Helper()
	root := t.TempDir()
	store, err := core.Open(filepath.Join(root, "identity.db"), filepath.Join(root, "legacy"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	user, err := store.Register(context.Background(), core.Credentials{Username: "mcp-v2-user", Password: "password-123"})
	if err != nil {
		t.Fatal(err)
	}
	login, err := store.Login(context.Background(), core.Credentials{Username: user.Username, Password: "password-123"})
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(core.WithUser(context.Background(), user.ID), core.ProjectInput{Name: "MCP V2"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := v2.New(store, filepath.Join(root, "v2"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	server := httptest.NewServer(V2HTTP(store, service, ""))
	t.Cleanup(server.Close)
	client := &http.Client{Transport: v2BearerTransport{base: server.Client().Transport, token: login.Token}}
	ctx := context.Background()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "v2-test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: client}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return ctx, session, store, service, user, project
}

func callV2Tool(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

func decodeV2Tool[T any](t *testing.T, result *mcp.CallToolResult) T {
	t.Helper()
	if result.IsError {
		t.Fatalf("tool error: %#v", result.Content)
	}
	raw, ok := result.StructuredContent.(json.RawMessage)
	if !ok {
		b, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		raw = b
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return out
}

func TestV2MCPExactToolsTypedMetadataAndFileLimit(t *testing.T) {
	ctx, session, _, service, user, project := newV2MCPSession(t)
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		got = append(got, tool.Name)
	}
	sort.Strings(got)
	want := []string{"add_member", "create_project", "get_event", "get_file", "get_state", "list_members", "list_metadata", "list_projects", "list_state", "put_state", "query_events", "record_events", "upload_file"}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tools=%v", got)
	}

	first := v2.EventInput{ID: "11111111-1111-4111-8111-111111111111", Type: "note", Content: v2.EventContent{Kind: "text", Text: "one"}, Metadata: map[string]json.RawMessage{"number": json.RawMessage(`9007199254740993`), "nested": json.RawMessage(`{"b":2,"a":1}`)}, Source: map[string]json.RawMessage{"channel": json.RawMessage(`"api"`)}}
	created := decodeV2Tool[v2.RecordEventsResult](t, callV2Tool(t, ctx, session, "record_events", V2RecordEventsInput{ProjectID: project.ID, Events: []v2.EventInput{first}}))
	if len(created.Results) != 1 || created.Results[0].Status != "created" {
		t.Fatalf("created %#v", created)
	}
	retry := first
	retry.Metadata = map[string]json.RawMessage{"nested": json.RawMessage(` { "a":1, "b":2 } `), "number": json.RawMessage(` 9007199254740993 `)}
	duplicate := decodeV2Tool[v2.RecordEventsResult](t, callV2Tool(t, ctx, session, "record_events", V2RecordEventsInput{ProjectID: project.ID, Events: []v2.EventInput{retry}}))
	if duplicate.Results[0].Status != "duplicate" {
		t.Fatalf("duplicate %#v", duplicate)
	}
	second := first
	second.ID = "22222222-2222-4222-8222-222222222222"
	second.Metadata = map[string]json.RawMessage{"number": json.RawMessage(`9007199254740992`)}
	_ = decodeV2Tool[v2.RecordEventsResult](t, callV2Tool(t, ctx, session, "record_events", V2RecordEventsInput{ProjectID: project.ID, Events: []v2.EventInput{second}}))
	page := decodeV2Tool[v2.EventsPage](t, callV2Tool(t, ctx, session, "query_events", V2QueryEventsInput{ProjectID: project.ID, Metadata: map[string]json.RawMessage{"number": json.RawMessage(`9007199254740993`)}}))
	if len(page.Events) != 1 || page.Events[0].ID != first.ID || page.Events[0].Actor.ID != user.ID {
		t.Fatalf("typed query %#v", page)
	}

	data := []byte("small file")
	file := decodeV2Tool[v2.FileInfo](t, callV2Tool(t, ctx, session, "upload_file", V2UploadFileInput{ProjectID: project.ID, Filename: "small.txt", MediaType: "text/plain", DataBase64: base64.StdEncoding.EncodeToString(data)}))
	read := decodeV2Tool[V2FileResult](t, callV2Tool(t, ctx, session, "get_file", V2GetFileInput{ProjectID: project.ID, FileID: file.ID}))
	decoded, err := base64.StdEncoding.DecodeString(read.DataBase64)
	if err != nil || !bytes.Equal(decoded, data) || read.ContentOmitted {
		t.Fatalf("small read %#v err=%v", read, err)
	}

	tooLarge := bytes.Repeat([]byte("x"), MaxV2MCPFileBytes+1)
	result := callV2Tool(t, ctx, session, "upload_file", V2UploadFileInput{ProjectID: project.ID, Filename: "large.txt", MediaType: "text/plain", DataBase64: base64.StdEncoding.EncodeToString(tooLarge)})
	if !result.IsError || !strings.Contains(toolText(result), "too_large") {
		t.Fatalf("large upload %#v", result.Content)
	}
	direct, err := service.PutFile(core.WithUser(ctx, user.ID), project.ID, v2.FileUpload{Filename: "http-only.txt", MediaType: "text/plain", SizeBytes: int64(len(tooLarge)), Reader: bytes.NewReader(tooLarge)})
	if err != nil {
		t.Fatal(err)
	}
	read = decodeV2Tool[V2FileResult](t, callV2Tool(t, ctx, session, "get_file", V2GetFileInput{ProjectID: project.ID, FileID: direct.ID}))
	if !read.ContentOmitted || read.DataBase64 != "" {
		t.Fatalf("large get %#v", read)
	}
}

func TestV2MCPBase64AcceptsExactOneMiB(t *testing.T) {
	exact := bytes.Repeat([]byte("x"), MaxV2MCPFileBytes)
	decoded, err := decodeV2Base64(base64.StdEncoding.EncodeToString(exact))
	if err != nil || len(decoded) != MaxV2MCPFileBytes {
		t.Fatalf("exact limit rejected: size=%d err=%v", len(decoded), err)
	}
	tooLarge := append(exact, 'x')
	if _, err = decodeV2Base64(base64.StdEncoding.EncodeToString(tooLarge)); err == nil {
		t.Fatal("content over 1 MiB accepted")
	}
}

func TestV2MCPPausedPluginKeepsDocumentedErrorAndResumes(t *testing.T) {
	ctx, _, store, service, user, project := newV2MCPSession(t)
	installed, err := service.InstallPlugin(core.WithUser(ctx, user.ID), project.ID, v2.InstallPluginInput{Manifest: v2.Manifest{
		ID: "reader", Version: "0.1.0", Name: "Reader",
		Permissions: v2.Permissions{ReadEvents: []string{"note"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.SetPluginStatus(core.WithUser(ctx, user.ID), project.ID, "reader", "paused"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.AuthenticatePlugin(installed.Token); err == nil || err.Error() != "plugin is paused" {
		t.Fatalf("paused credential was not recognized by service: %v", err)
	}

	server := httptest.NewServer(V2HTTP(store, service, ""))
	defer server.Close()
	client := &http.Client{Transport: v2BearerTransport{base: server.Client().Transport, token: installed.Token}}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "paused-plugin-test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: client}, nil)
	if err != nil {
		t.Fatalf("paused plugin credential should initialize MCP: %v", err)
	}
	defer session.Close()

	result := callV2Tool(t, ctx, session, "query_events", V2QueryEventsInput{ProjectID: project.ID})
	if !result.IsError || !strings.Contains(toolText(result), `"code":"plugin_paused"`) {
		t.Fatalf("paused result %#v", result.Content)
	}
	if _, err = service.SetPluginStatus(core.WithUser(ctx, user.ID), project.ID, "reader", "active"); err != nil {
		t.Fatal(err)
	}
	page := decodeV2Tool[v2.EventsPage](t, callV2Tool(t, ctx, session, "query_events", V2QueryEventsInput{ProjectID: project.ID}))
	if len(page.Events) != 0 {
		t.Fatalf("resumed query %#v", page)
	}
	other, err := store.CreateProject(core.WithUser(ctx, user.ID), core.ProjectInput{Name: "Other project"})
	if err != nil {
		t.Fatal(err)
	}
	crossProject := callV2Tool(t, ctx, session, "query_events", V2QueryEventsInput{ProjectID: other.ID})
	if !crossProject.IsError || !strings.Contains(toolText(crossProject), `"code":"not_found"`) {
		t.Fatalf("plugin crossed project boundary %#v", crossProject.Content)
	}
}

func TestV2MCPOAuthReadAndWriteScopes(t *testing.T) {
	ctx, _, store, service, user, _ := newV2MCPSession(t)
	const issuer = "https://context-v2-mcp.example"
	server := httptest.NewServer(V2HTTP(store, service, issuer))
	defer server.Close()
	connect := func(token string) *mcp.ClientSession {
		t.Helper()
		client := &http.Client{Transport: v2BearerTransport{base: server.Client().Transport, token: token}}
		session, err := mcp.NewClient(&mcp.Implementation{Name: "oauth-scope-test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: client}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = session.Close() })
		return session
	}

	readSession := connect(mintV2MCPOAuthToken(t, store, user.ID, core.ScopeRead, issuer))
	projects := decodeV2Tool[core.Projects](t, callV2Tool(t, ctx, readSession, "list_projects", core.Empty{}))
	if len(projects.Projects) != 1 {
		t.Fatalf("read scope projects %#v", projects)
	}
	denied := callV2Tool(t, ctx, readSession, "create_project", core.ProjectInput{Name: "denied"})
	if !denied.IsError || !strings.Contains(toolText(denied), "context:write") {
		t.Fatalf("read token write result %#v", denied.Content)
	}

	writeSession := connect(mintV2MCPOAuthToken(t, store, user.ID, core.ScopeWrite, issuer))
	denied = callV2Tool(t, ctx, writeSession, "list_projects", core.Empty{})
	if !denied.IsError || !strings.Contains(toolText(denied), "context:read") {
		t.Fatalf("write token read result %#v", denied.Content)
	}
	created := decodeV2Tool[core.Project](t, callV2Tool(t, ctx, writeSession, "create_project", core.ProjectInput{Name: "allowed"}))
	if created.Name != "allowed" {
		t.Fatalf("write scope create %#v", created)
	}
}

func mintV2MCPOAuthToken(t *testing.T, store *core.Store, userID, scope, issuer string) string {
	t.Helper()
	ctx := context.Background()
	redirect := "http://127.0.0.1/callback"
	client, err := store.RegisterOAuthClient(ctx, "v2 MCP test", []string{redirect})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	requestID, csrf, code := "mcp-request-"+scope, "mcp-csrf-"+scope, "mcp-code-"+scope
	resource := issuer + "/mcp"
	if err = store.CreateOAuthRequest(ctx, requestID, csrf, core.OAuthRequest{ClientID: client.ClientID, RedirectURI: redirect, CodeChallenge: challenge, Scope: scope, Resource: resource}, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ApproveOAuthRequest(ctx, requestID, csrf, userID, code, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	token, _, err := store.ExchangeOAuthCode(ctx, code, client.ClientID, redirect, verifier, resource, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func toolText(result *mcp.CallToolResult) string {
	var out strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			out.WriteString(text.Text)
		}
	}
	return out.String()
}
