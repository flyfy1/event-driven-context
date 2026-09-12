package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"event-driven-context/internal/core"
	"event-driven-context/internal/v2"
)

const v2TestIssuer = "https://context-v2.example"

type v2APIFixture struct {
	store    *core.Store
	service  *v2.Service
	handler  http.Handler
	alice    core.User
	bob      core.User
	token    string
	bobToken string
	project  core.Project
}

func newV2APIFixture(t *testing.T) *v2APIFixture {
	t.Helper()
	root := t.TempDir()
	store, err := core.Open(filepath.Join(root, "identity.db"), filepath.Join(root, "legacy"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	alice, err := store.Register(context.Background(), core.Credentials{Username: "alice-v2", Email: "api-v2-alice@example.invalid", Password: "password-123"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := store.Register(context.Background(), core.Credentials{Username: "bob-v2", Email: "api-v2-bob@example.invalid", Password: "password-123"})
	if err != nil {
		t.Fatal(err)
	}
	login, err := store.Login(context.Background(), core.Credentials{Username: alice.Username, Password: "password-123"})
	if err != nil {
		t.Fatal(err)
	}
	bobLogin, err := store.Login(context.Background(), core.Credentials{Username: bob.Username, Password: "password-123"})
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(core.WithUser(context.Background(), alice.ID), core.ProjectInput{Name: "V2 private"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := v2.New(store, filepath.Join(root, "v2"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return &v2APIFixture{store: store, service: service, handler: V2HandlerWithConfig(store, service, Config{PublicBaseURL: v2TestIssuer, AllowedOrigins: []string{"https://app.example"}}), alice: alice, bob: bob, token: login.Token, bobToken: bobLogin.Token, project: project}
}

func (f *v2APIFixture) request(t *testing.T, method, path, token, contentType string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, body)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func v2JSONBody(t *testing.T, value any) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b)
}

func decodeV2Response[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %d %s: %v", w.Code, w.Body.String(), err)
	}
	return out
}

func TestV2HTTPProjectMemberEventQueryMetadataAndActorBoundary(t *testing.T) {
	f := newV2APIFixture(t)
	w := f.request(t, http.MethodGet, "/v1/projects", f.token, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("projects: %d %s", w.Code, w.Body.String())
	}
	projects := decodeV2Response[core.Projects](t, w)
	if len(projects.Projects) != 1 || projects.Projects[0].ID != f.project.ID {
		t.Fatalf("projects %#v", projects)
	}

	w = f.request(t, http.MethodPost, "/v1/projects/"+f.project.ID+"/members", f.token, "application/json", v2JSONBody(t, map[string]string{"username": f.bob.Username}))
	if w.Code != http.StatusOK {
		t.Fatalf("add member: %d %s", w.Code, w.Body.String())
	}
	w = f.request(t, http.MethodGet, "/v1/projects/"+f.project.ID+"/members", f.bobToken, "", nil)
	if members := decodeV2Response[core.Members](t, w); w.Code != http.StatusOK || len(members.Members) != 2 || members.Members[1].Role != "member" {
		t.Fatalf("members: %d %#v", w.Code, members)
	}
	w = f.request(t, http.MethodPatch, "/v1/projects/"+f.project.ID+"/members/"+f.bob.ID, f.bobToken, "application/json", v2JSONBody(t, map[string]string{"role": "owner"}))
	if w.Code != http.StatusForbidden {
		t.Fatalf("member promoted self: %d %s", w.Code, w.Body.String())
	}
	w = f.request(t, http.MethodPatch, "/v1/projects/"+f.project.ID+"/members/"+f.bob.ID, f.token, "application/json", v2JSONBody(t, map[string]string{"role": "owner"}))
	if promoted := decodeV2Response[core.ProjectMember](t, w); w.Code != http.StatusOK || promoted.ID != f.bob.ID || promoted.Role != "owner" {
		t.Fatalf("promote member: %d %#v", w.Code, promoted)
	}
	w = f.request(t, http.MethodPatch, "/v1/projects/"+f.project.ID, f.bobToken, "application/json", v2JSONBody(t, map[string]string{"timezone": "Asia/Singapore"}))
	if w.Code != http.StatusOK {
		t.Fatalf("second owner could not manage project: %d %s", w.Code, w.Body.String())
	}

	path := "/v1/projects/" + f.project.ID + "/events"
	spoof := `{"events":[{"id":"11111111-1111-4111-8111-111111111111","type":"note","content":{"kind":"text","text":"x"},"source":{"channel":"app"},"actor":{"type":"plugin","id":"fake"}}]}`
	w = f.request(t, http.MethodPost, path, f.token, "application/json", strings.NewReader(spoof))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "unknown field") {
		t.Fatalf("actor spoof: %d %s", w.Code, w.Body.String())
	}

	first := v2.EventInput{ID: "11111111-1111-4111-8111-111111111111", Type: "note", Content: v2.EventContent{Kind: "text", Text: "large integer"}, Metadata: map[string]json.RawMessage{"count": json.RawMessage(`9007199254740993`)}, Source: map[string]json.RawMessage{"channel": json.RawMessage(`"api"`)}}
	second := first
	second.ID = "22222222-2222-4222-8222-222222222222"
	second.Metadata = map[string]json.RawMessage{"count": json.RawMessage(`9007199254740992`)}
	w = f.request(t, http.MethodPost, path, f.token, "application/json", v2JSONBody(t, v2.RecordEventsInput{Events: []v2.EventInput{first, second}}))
	result := decodeV2Response[v2.RecordEventsResult](t, w)
	if w.Code != http.StatusOK || len(result.Results) != 2 || result.Results[0].Status != "created" {
		t.Fatalf("record: %d %#v", w.Code, result)
	}

	query := v2.QueryEventsInput{Metadata: map[string]json.RawMessage{"count": json.RawMessage(`9007199254740993`)}}
	w = f.request(t, http.MethodPost, path+"/query", f.token, "application/json", v2JSONBody(t, query))
	page := decodeV2Response[v2.EventsPage](t, w)
	if w.Code != http.StatusOK || len(page.Events) != 1 || page.Events[0].ID != first.ID || page.Events[0].Actor.ID != f.alice.ID || page.Events[0].Actor.Type != "user" {
		t.Fatalf("query: %d %#v", w.Code, page)
	}
	w = f.request(t, http.MethodGet, path+"/"+first.ID, f.bobToken, "", nil)
	if event := decodeV2Response[v2.Event](t, w); w.Code != http.StatusOK || event.ID != first.ID {
		t.Fatalf("get: %d %#v", w.Code, event)
	}
	w = f.request(t, http.MethodGet, "/v1/projects/"+f.project.ID+"/metadata?key=count", f.token, "", nil)
	if metadata := decodeV2Response[v2.MetadataResult](t, w); w.Code != http.StatusOK || len(metadata.Values) != 2 {
		t.Fatalf("metadata: %d %#v", w.Code, metadata)
	}
}

func TestV2HTTPFileMultipartLimitsHashAndDownloadHeaders(t *testing.T) {
	f := newV2APIFixture(t)
	upload := func(hash string) (*httptest.ResponseRecorder, v2.FileInfo) {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", `form-data; name="file"; filename="notes.txt"`)
		h.Set("Content-Type", "text/plain")
		part, err := mw.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write([]byte("file bytes"))
		if hash != "" {
			_ = mw.WriteField("sha256", hash)
		}
		if err = mw.Close(); err != nil {
			t.Fatal(err)
		}
		w := f.request(t, http.MethodPost, "/v1/projects/"+f.project.ID+"/files", f.token, mw.FormDataContentType(), &body)
		if w.Code != http.StatusCreated {
			return w, v2.FileInfo{}
		}
		return w, decodeV2Response[v2.FileInfo](t, w)
	}
	w, file := upload("")
	if w.Code != http.StatusCreated || file.SizeBytes != 10 {
		t.Fatalf("file only: %d %s", w.Code, w.Body.String())
	}
	w, _ = upload(strings.Repeat("0", 64))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "sha256") {
		t.Fatalf("wrong hash: %d %s", w.Code, w.Body.String())
	}
	w = f.request(t, http.MethodGet, "/v1/projects/"+f.project.ID+"/files/"+file.ID, f.token, "", nil)
	if w.Code != http.StatusOK || w.Body.String() != "file bytes" || w.Header().Get("X-EDC-SHA256") != file.SHA256 || w.Header().Get("X-EDC-File-ID") != file.ID || w.Header().Get("Content-Length") != "10" {
		t.Fatalf("download: %d headers=%v body=%q", w.Code, w.Header(), w.Body.String())
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/projects/"+f.project.ID+"/files/"+file.ID, nil)
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Origin", "https://app.example")
	w = httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	exposed := w.Header().Get("Access-Control-Expose-Headers")
	for _, name := range []string{"Content-Disposition", "X-EDC-File-ID", "X-EDC-SHA256", "X-Request-ID"} {
		if !strings.Contains(exposed, name) {
			t.Fatalf("download does not expose %s: %q", name, exposed)
		}
	}

	prefix := "--limit\r\nContent-Disposition: form-data; name=\"file\"; filename=\"large.txt\"\r\nContent-Type: text/plain\r\n\r\n"
	body := io.MultiReader(strings.NewReader(prefix), io.LimitReader(zeroReader{}, int64(v2.MaxFileBytes+v2MultipartOverhead+1)))
	w = f.request(t, http.MethodPost, "/v1/projects/"+f.project.ID+"/files", f.token, "multipart/form-data; boundary=limit", body)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize: %d %s", w.Code, w.Body.String())
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func TestV2HTTPPluginStatePauseManualRunAndUserOnlyManagement(t *testing.T) {
	f := newV2APIFixture(t)
	event := v2.EventInput{ID: "33333333-3333-4333-8333-333333333333", Type: "note", Content: v2.EventContent{Kind: "text", Text: "source"}, Source: map[string]json.RawMessage{"channel": json.RawMessage(`"app"`)}}
	path := "/v1/projects/" + f.project.ID
	w := f.request(t, http.MethodPost, path+"/events", f.token, "application/json", v2JSONBody(t, v2.RecordEventsInput{Events: []v2.EventInput{event}}))
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	manifest := v2.Manifest{ID: "project-brief", Version: "0.1.0", Name: "Project brief", Skills: []string{"skills/brief/SKILL.md"}, State: []v2.StateDeclaration{{Key: "current"}, {Key: "_requests"}}, Processor: json.RawMessage(`{"entry":{"type":"agent"}}`), Config: json.RawMessage(`{"prompt":"brief"}`), Permissions: v2.Permissions{ReadEvents: []string{"note", "derived"}, WriteEvents: []string{"derived"}, WriteState: []string{"current", "_requests"}}}
	w = f.request(t, http.MethodPost, path+"/plugins", f.token, "application/json", v2JSONBody(t, v2.InstallPluginInput{Manifest: manifest}))
	installed := decodeV2Response[v2.InstallPluginResult](t, w)
	if w.Code != http.StatusCreated || installed.Token == "" || installed.Installation.Manifest.ID != "project-brief" {
		t.Fatalf("install: %d %#v", w.Code, installed)
	}

	// Plugin credentials cannot cross into user-only member management. A host
	// may introspect only its own current installation and config revision.
	w = f.request(t, http.MethodGet, path+"/members", installed.Token, "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("plugin members: %d %s", w.Code, w.Body.String())
	}
	w = f.request(t, http.MethodGet, path+"/plugins", installed.Token, "", nil)
	self := decodeV2Response[struct {
		Plugins []v2.Installation `json:"plugins"`
	}](t, w)
	if w.Code != http.StatusOK || len(self.Plugins) != 1 || self.Plugins[0].ID != installed.Installation.ID || self.Plugins[0].ConfigRevision != installed.Installation.ConfigRevision {
		t.Fatalf("plugin self introspection: %d %#v", w.Code, self)
	}
	otherProject, err := f.store.CreateProject(core.WithUser(context.Background(), f.alice.ID), core.ProjectInput{Name: "Other V2 project"})
	if err != nil {
		t.Fatal(err)
	}
	w = f.request(t, http.MethodGet, "/v1/projects/"+otherProject.ID+"/plugins", installed.Token, "", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("plugin crossed project boundary: %d %s", w.Code, w.Body.String())
	}

	derived := v2.EventInput{ID: "44444444-4444-4444-8444-444444444444", Type: "derived", Content: v2.EventContent{Kind: "text", Text: "summary"}, Source: map[string]json.RawMessage{"channel": json.RawMessage(`"plugin"`)}, Refs: []v2.Ref{{Rel: "derived_from", ID: event.ID}}}
	w = f.request(t, http.MethodPost, path+"/events", installed.Token, "application/json", v2JSONBody(t, v2.RecordEventsInput{Events: []v2.EventInput{derived}}))
	if w.Code != http.StatusOK {
		t.Fatalf("plugin record: %d %s", w.Code, w.Body.String())
	}
	w = f.request(t, http.MethodGet, path+"/events/"+derived.ID, f.token, "", nil)
	if stored := decodeV2Response[v2.Event](t, w); stored.Actor.Type != "plugin" || stored.Actor.ID != "project-brief" || stored.Actor.OnBehalfOf != f.alice.ID {
		t.Fatalf("actor %#v", stored.Actor)
	}

	w = f.request(t, http.MethodPatch, path+"/plugins/project-brief", f.token, "application/json", v2JSONBody(t, map[string]any{"action": "pause"}))
	if paused := decodeV2Response[v2.Installation](t, w); w.Code != http.StatusOK || paused.Status != "paused" {
		t.Fatalf("pause: %d %#v", w.Code, paused)
	}
	w = f.request(t, http.MethodPost, path+"/events/query", installed.Token, "application/json", v2JSONBody(t, v2.QueryEventsInput{}))
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "plugin_paused") {
		t.Fatalf("paused token: %d %s", w.Code, w.Body.String())
	}
	w = f.request(t, http.MethodPatch, path+"/plugins/project-brief", f.token, "application/json", v2JSONBody(t, map[string]any{"action": "resume"}))
	if resumed := decodeV2Response[v2.Installation](t, w); w.Code != http.StatusOK || resumed.Status != "active" {
		t.Fatalf("resume: %d %#v", w.Code, resumed)
	}

	zero := int64(0)
	state := v2.PutStateInput{ExpectedVersion: &zero, Content: v2.StateContent{Format: "markdown", Text: "brief"}, BasedOnSequence: 2, Refs: []string{event.ID}}
	w = f.request(t, http.MethodPut, path+"/state/project-brief/current", installed.Token, "application/json", v2JSONBody(t, state))
	if w.Code != http.StatusOK {
		t.Fatalf("put state: %d %s", w.Code, w.Body.String())
	}
	w = f.request(t, http.MethodGet, path+"/state/project-brief/current", f.token, "", nil)
	if got := decodeV2Response[v2.State](t, w); w.Code != http.StatusOK || got.Key != "project-brief/current" || got.Version != 1 {
		t.Fatalf("get state: %d %#v", w.Code, got)
	}

	w = f.request(t, http.MethodPost, path+"/plugins/project-brief/runs", f.token, "application/json", v2JSONBody(t, v2.ManualRunInput{RequestID: "manual-1", SourceEventIDs: []string{event.ID}}))
	if run := decodeV2Response[v2.ManualRunRequest](t, w); w.Code != http.StatusAccepted || run.Status != "accepted" {
		t.Fatalf("manual: %d %#v", w.Code, run)
	}
	w = f.request(t, http.MethodGet, path+"/plugins", f.token, "", nil)
	var list struct {
		Plugins []v2.Installation `json:"plugins"`
	}
	list = decodeV2Response[struct {
		Plugins []v2.Installation `json:"plugins"`
	}](t, w)
	if w.Code != http.StatusOK || len(list.Plugins) != 1 {
		t.Fatalf("plugins wrapper: %d %#v", w.Code, list)
	}
}

func TestV2HTTPOAuthScopesApplyToProjectRoutes(t *testing.T) {
	f := newV2APIFixture(t)
	readToken := mintV2OAuthToken(t, f.store, f.alice.ID, core.ScopeRead)
	w := f.request(t, http.MethodGet, "/v1/projects", readToken, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("OAuth read: %d %s", w.Code, w.Body.String())
	}
	w = f.request(t, http.MethodPost, "/v1/projects", readToken, "application/json", v2JSONBody(t, core.ProjectInput{Name: "forbidden"}))
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "context:write") {
		t.Fatalf("OAuth write scope: %d %s", w.Code, w.Body.String())
	}
	writeToken := mintV2OAuthToken(t, f.store, f.alice.ID, core.ScopeWrite)
	w = f.request(t, http.MethodGet, "/v1/projects", writeToken, "", nil)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "context:read") {
		t.Fatalf("OAuth read scope: %d %s", w.Code, w.Body.String())
	}
	w = f.request(t, http.MethodPost, "/v1/projects", writeToken, "application/json", v2JSONBody(t, core.ProjectInput{Name: "allowed"}))
	if w.Code != http.StatusCreated {
		t.Fatalf("OAuth create: %d %s", w.Code, w.Body.String())
	}
}

func mintV2OAuthToken(t *testing.T, store *core.Store, userID, scope string) string {
	t.Helper()
	ctx := context.Background()
	redirect := "http://127.0.0.1/callback"
	client, err := store.RegisterOAuthClient(ctx, "v2 test", []string{redirect})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	requestID, csrf, code := "oauth-request-"+scope, "csrf-"+scope, "code-"+scope
	resource := v2TestIssuer + "/mcp"
	err = store.CreateOAuthRequest(ctx, requestID, csrf, core.OAuthRequest{ClientID: client.ClientID, RedirectURI: redirect, CodeChallenge: challenge, Scope: scope, Resource: resource}, time.Now().Add(time.Minute))
	if err != nil {
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
