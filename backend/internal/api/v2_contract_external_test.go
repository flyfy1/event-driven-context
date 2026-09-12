package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"event-driven-context/internal/api"
	"event-driven-context/internal/core"
	"event-driven-context/internal/mcpserver"
	"event-driven-context/internal/v2"
	"event-driven-context/internal/v2client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	contractEventOne   = "11111111-1111-4111-8111-111111111111"
	contractEventTwo   = "22222222-2222-4222-8222-222222222222"
	contractEventThree = "33333333-3333-4333-8333-333333333333"
)

type contractBearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t contractBearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header = r.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

type contractFixture struct {
	server       *httptest.Server
	aliceHTTP    *v2client.Client
	bobHTTP      *v2client.Client
	aliceToken   string
	bobToken     string
	aliceProject core.Project
	bobProject   core.Project
}

func newContractFixture(t *testing.T) *contractFixture {
	t.Helper()
	root := t.TempDir()
	store, err := core.Open(filepath.Join(root, "identity.db"), filepath.Join(root, "legacy"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := v2.New(store, filepath.Join(root, "v2"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })

	alice, aliceToken := contractUser(t, store, "contract-alice")
	bob, bobToken := contractUser(t, store, "contract-bob")
	aliceProject, err := store.CreateProject(core.WithUser(context.Background(), alice.ID), core.ProjectInput{Name: "Contract Alice"})
	if err != nil {
		t.Fatal(err)
	}
	bobProject, err := store.CreateProject(core.WithUser(context.Background(), bob.ID), core.ProjectInput{Name: "Contract Bob"})
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(api.V2HandlerWithConfig(store, service, api.Config{}))
	t.Cleanup(server.Close)
	aliceHTTP, err := v2client.New(server.URL, aliceToken)
	if err != nil {
		t.Fatal(err)
	}
	bobHTTP, err := v2client.New(server.URL, bobToken)
	if err != nil {
		t.Fatal(err)
	}
	return &contractFixture{server: server, aliceHTTP: aliceHTTP, bobHTTP: bobHTTP, aliceToken: aliceToken, bobToken: bobToken, aliceProject: aliceProject, bobProject: bobProject}
}

func contractUser(t *testing.T, store *core.Store, username string) (core.User, string) {
	t.Helper()
	user, err := store.Register(context.Background(), core.Credentials{Username: username, Email: username + "@example.test", Password: "password-123"})
	if err != nil {
		t.Fatal(err)
	}
	login, err := store.Login(context.Background(), core.Credentials{Username: username, Password: "password-123"})
	if err != nil {
		t.Fatal(err)
	}
	return user, login.Token
}

func connectContractMCP(t *testing.T, serverURL, token string) *mcp.ClientSession {
	t.Helper()
	httpClient := &http.Client{Transport: contractBearerTransport{base: http.DefaultTransport, token: token}}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "v2-contract-test", Version: "1"}, nil).Connect(
		context.Background(),
		&mcp.StreamableClientTransport{Endpoint: serverURL + "/mcp", HTTPClient: httpClient},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func contractTool(t *testing.T, session *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s transport: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("%s tool: %s", name, contractToolText(result))
	}
	return result
}

func contractDecode[T any](t *testing.T, result *mcp.CallToolResult) T {
	t.Helper()
	raw, ok := result.StructuredContent.(json.RawMessage)
	if !ok {
		var err error
		raw, err = json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode MCP result %s: %v", raw, err)
	}
	return out
}

func contractToolText(result *mcp.CallToolResult) string {
	var out strings.Builder
	for _, content := range result.Content {
		if item, ok := content.(*mcp.TextContent); ok {
			out.WriteString(item.Text)
		}
	}
	return out.String()
}

func contractRawTool[T any](t *testing.T, serverURL, token, name string, args any) T {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      991,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, serverURL+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("raw MCP %s: HTTP %d %s", name, res.StatusCode, raw)
	}
	var envelope struct {
		Result struct {
			StructuredContent json.RawMessage `json:"structuredContent"`
		} `json:"result"`
	}
	if err = json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("raw MCP %s response %s: %v", name, raw, err)
	}
	var out T
	if err = json.Unmarshal(envelope.Result.StructuredContent, &out); err != nil {
		t.Fatalf("raw MCP %s structuredContent %s: %v", name, envelope.Result.StructuredContent, err)
	}
	return out
}

func contractNote(id, text, number string) v2.EventInput {
	return v2.EventInput{
		ID:      id,
		Type:    "note",
		Content: v2.EventContent{Kind: "text", Text: text},
		Metadata: map[string]json.RawMessage{
			"number": json.RawMessage(number),
			"nested": json.RawMessage(`{"a":1,"b":2}`),
		},
		Source: map[string]json.RawMessage{"channel": json.RawMessage(`"api"`)},
	}
}

// TestV2PublicHTTPAndMCPShareOneContract exercises the production handler's
// JSON API and /mcp surface against one real identity store and one real V2
// file-backed service. Individual packages test their own validation in depth;
// this test protects the state and authorization contract between surfaces.
func TestV2PublicHTTPAndMCPShareOneContract(t *testing.T) {
	f := newContractFixture(t)
	ctx := context.Background()
	aliceMCP := connectContractMCP(t, f.server.URL, f.aliceToken)

	first := contractNote(contractEventOne, "large integer", `9007199254740993`)
	created, err := f.aliceHTTP.RecordEvents(ctx, f.aliceProject.ID, v2.RecordEventsInput{Events: []v2.EventInput{first}})
	if err != nil || len(created.Results) != 1 || created.Results[0].Status != "created" || created.Results[0].Sequence != 1 {
		t.Fatalf("HTTP initial write = %#v, %v", created, err)
	}

	duplicate := contractNote(contractEventOne, "large integer", ` 9007199254740993 `)
	duplicate.Metadata["nested"] = json.RawMessage(` { "b": 2, "a": 1 } `)
	conflict := contractNote(contractEventOne, "different content", `9007199254740993`)
	invalid := contractNote(contractEventTwo, "missing reference", `9007199254740993`)
	invalid.Refs = []v2.Ref{{Rel: "replies_to", ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}}
	valid := contractNote(contractEventThree, "second durable event", `9007199254740992`)
	batch := contractDecode[v2.RecordEventsResult](t, contractTool(t, aliceMCP, "record_events", mcpserver.V2RecordEventsInput{
		ProjectID: f.aliceProject.ID,
		Events:    []v2.EventInput{duplicate, conflict, invalid, valid},
	}))
	if len(batch.Results) != 4 {
		t.Fatalf("partial MCP batch = %#v", batch)
	}
	wantStatuses := []string{"duplicate", "conflict", "invalid", "created"}
	for i, want := range wantStatuses {
		if batch.Results[i].Status != want {
			t.Fatalf("partial MCP batch result %d = %#v, want status %q", i, batch.Results[i], want)
		}
	}
	if batch.Results[0].Sequence != 1 || batch.Results[3].Sequence != 2 || batch.Results[2].Error == nil || batch.Results[2].Error.Code != "invalid_ref" {
		t.Fatalf("partial MCP batch details = %#v", batch)
	}

	exactMCPResult := contractTool(t, aliceMCP, "query_events", mcpserver.V2QueryEventsInput{
		ProjectID: f.aliceProject.ID,
		Metadata:  map[string]json.RawMessage{"number": json.RawMessage(`9007199254740993`)},
	})
	exactMCP := contractDecode[v2.EventsPage](t, exactMCPResult)
	if len(exactMCP.Events) != 1 || exactMCP.Events[0].ID != contractEventOne {
		t.Fatalf("MCP exact integer query = %#v", exactMCP)
	}
	var textPage v2.EventsPage
	if err = json.Unmarshal([]byte(contractToolText(exactMCPResult)), &textPage); err != nil || len(textPage.Events) != 1 || string(textPage.Events[0].Metadata["number"]) != "9007199254740993" {
		t.Fatalf("MCP text content lost exact integer: %#v, %v", textPage, err)
	}
	rawPage := contractRawTool[v2.EventsPage](t, f.server.URL, f.aliceToken, "query_events", mcpserver.V2QueryEventsInput{
		ProjectID: f.aliceProject.ID,
		Metadata:  map[string]json.RawMessage{"number": json.RawMessage(`9007199254740993`)},
	})
	if len(rawPage.Events) != 1 || string(rawPage.Events[0].Metadata["number"]) != "9007199254740993" {
		t.Fatalf("raw MCP structuredContent lost exact integer: %#v", rawPage)
	}
	exactHTTP, err := f.aliceHTTP.QueryEvents(ctx, f.aliceProject.ID, v2.QueryEventsInput{Metadata: map[string]json.RawMessage{"number": json.RawMessage(`9007199254740993`)}})
	if err != nil || len(exactHTTP.Events) != 1 || exactHTTP.Events[0].ID != contractEventOne || string(exactHTTP.Events[0].Metadata["number"]) != "9007199254740993" || exactHTTP.LatestSequence != 2 {
		t.Fatalf("HTTP exact integer query = %#v, %v", exactHTTP, err)
	}

	manifest := v2.Manifest{
		ID:        "contract-brief",
		Version:   "0.1.0",
		Name:      "Contract brief",
		Skills:    []string{"skills/brief/SKILL.md"},
		State:     []v2.StateDeclaration{{Key: "current"}},
		Processor: json.RawMessage(`{"entry":{"type":"agent"}}`),
		Config:    json.RawMessage(`{"prompt":"brief"}`),
		Permissions: v2.Permissions{
			ReadEvents: []string{"note"},
			WriteState: []string{"current"},
		},
	}
	installed, err := f.aliceHTTP.InstallPlugin(ctx, f.aliceProject.ID, v2.InstallPluginInput{Manifest: manifest})
	if err != nil || installed.Token == "" {
		t.Fatalf("HTTP install plugin = %#v, %v", installed, err)
	}
	pluginMCP := connectContractMCP(t, f.server.URL, installed.Token)
	zero := int64(0)
	stateV1 := contractDecode[v2.State](t, contractTool(t, pluginMCP, "put_state", mcpserver.V2PutStateInput{
		ProjectID:       f.aliceProject.ID,
		Key:             "contract-brief/current",
		ExpectedVersion: &zero,
		Content:         v2.StateContent{Format: "markdown", Text: "version one"},
		BasedOnSequence: 2,
		Refs:            []string{contractEventOne},
	}))
	if stateV1.Version != 1 || stateV1.Lag != 0 || stateV1.Producer.PluginID != "contract-brief" {
		t.Fatalf("MCP state v1 = %#v", stateV1)
	}

	third := contractNote("44444444-4444-4444-8444-444444444444", "after state", `9007199254740994`)
	if _, err = f.aliceHTTP.RecordEvents(ctx, f.aliceProject.ID, v2.RecordEventsInput{Events: []v2.EventInput{third}}); err != nil {
		t.Fatal(err)
	}
	current, err := f.aliceHTTP.GetState(ctx, f.aliceProject.ID, "contract-brief/current", nil)
	if err != nil || current.Version != 1 || current.Lag != 1 || current.BasedOnSequence != 2 {
		t.Fatalf("HTTP current state after event = %#v, %v", current, err)
	}

	casResult, casErr := pluginMCP.CallTool(ctx, &mcp.CallToolParams{Name: "put_state", Arguments: mcpserver.V2PutStateInput{
		ProjectID:       f.aliceProject.ID,
		Key:             "contract-brief/current",
		ExpectedVersion: &zero,
		Content:         v2.StateContent{Format: "text", Text: "stale overwrite"},
		BasedOnSequence: 3,
	}})
	if casErr != nil || casResult == nil || !casResult.IsError || !strings.Contains(contractToolText(casResult), "state_version_mismatch") {
		t.Fatalf("MCP stale state CAS result=%#v err=%v", casResult, casErr)
	}
	one := int64(1)
	stateV2 := contractDecode[v2.State](t, contractTool(t, pluginMCP, "put_state", mcpserver.V2PutStateInput{
		ProjectID:       f.aliceProject.ID,
		Key:             "contract-brief/current",
		ExpectedVersion: &one,
		Content:         v2.StateContent{Format: "markdown", Text: "version two"},
		BasedOnSequence: 3,
		Refs:            []string{contractEventThree},
	}))
	if stateV2.Version != 2 || stateV2.Lag != 0 {
		t.Fatalf("MCP state v2 = %#v", stateV2)
	}
	historical, err := f.aliceHTTP.GetState(ctx, f.aliceProject.ID, "contract-brief/current", &one)
	if err != nil || historical.Version != 1 || historical.Content.Text != "version one" || historical.Lag != 1 {
		t.Fatalf("HTTP historical state = %#v, %v", historical, err)
	}

	paused, err := f.aliceHTTP.PatchPlugin(ctx, f.aliceProject.ID, "contract-brief", v2client.PatchPluginInput{Action: "pause"})
	if err != nil || paused.Status != "paused" {
		t.Fatalf("HTTP pause = %#v, %v", paused, err)
	}
	pausedResult, pausedErr := pluginMCP.CallTool(ctx, &mcp.CallToolParams{Name: "query_events", Arguments: mcpserver.V2QueryEventsInput{ProjectID: f.aliceProject.ID}})
	if pausedErr != nil || pausedResult == nil || !pausedResult.IsError || !strings.Contains(contractToolText(pausedResult), "plugin_paused") {
		t.Fatalf("paused plugin MCP result=%#v err=%v", pausedResult, pausedErr)
	}
	resumed, err := f.aliceHTTP.PatchPlugin(ctx, f.aliceProject.ID, "contract-brief", v2client.PatchPluginInput{Action: "resume"})
	if err != nil || resumed.Status != "active" {
		t.Fatalf("HTTP resume = %#v, %v", resumed, err)
	}
	resumedPage := contractDecode[v2.EventsPage](t, contractTool(t, pluginMCP, "query_events", mcpserver.V2QueryEventsInput{ProjectID: f.aliceProject.ID}))
	if resumedPage.LatestSequence != 3 || len(resumedPage.Events) != 3 {
		t.Fatalf("resumed plugin MCP query = %#v", resumedPage)
	}

	fileBytes := []byte("cross-interface file")
	file := contractDecode[v2.FileInfo](t, contractTool(t, aliceMCP, "upload_file", mcpserver.V2UploadFileInput{
		ProjectID:  f.aliceProject.ID,
		Filename:   "contract.txt",
		MediaType:  "text/plain",
		DataBase64: base64.StdEncoding.EncodeToString(fileBytes),
	}))
	var ownDownload bytes.Buffer
	if _, err = f.aliceHTTP.GetFile(ctx, f.aliceProject.ID, file.ID, &ownDownload); err != nil || !bytes.Equal(ownDownload.Bytes(), fileBytes) {
		t.Fatalf("HTTP download of MCP upload = %q, %v", ownDownload.Bytes(), err)
	}
	var crossProject bytes.Buffer
	_, err = f.bobHTTP.GetFile(ctx, f.bobProject.ID, file.ID, &crossProject)
	var apiErr *v2client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound || apiErr.Code != "not_found" || crossProject.Len() != 0 {
		t.Fatalf("cross-project HTTP download err=%#v bytes=%q", err, crossProject.Bytes())
	}
	if file.ProjectID != f.aliceProject.ID {
		t.Fatal(fmt.Sprintf("MCP file project = %q, want %q", file.ProjectID, f.aliceProject.ID))
	}
}
