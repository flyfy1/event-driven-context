package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"event-driven-context/internal/core"
)

func TestHubImportEndpointOwnerOnlySnapshotsAndInputBounds(t *testing.T) {
	f := newV2APIFixture(t)
	key := bytes.Repeat([]byte{61}, 32)
	mux := http.NewServeMux()
	registerHubImportWithKey(mux, f.store, key)
	post := func(token string, input core.HubImportInput) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(input)
		r := httptest.NewRequest("POST", "/v1/hub/imports", bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	in := core.HubImportInput{ProviderID: "whatsapp-import", AccountID: "personal", DisplayName: "Personal chat", Filename: "chat.txt", Format: "whatsapp-text", Content: "19/09/2026, 10:00 - Alice: Private content\n"}
	if w := post("", in); w.Code != 401 {
		t.Fatal("anonymous import", w.Code)
	}
	agent, err := f.store.RegisterHubAgent(context.Background(), f.alice.Username, "Import tester")
	if err != nil {
		t.Fatal(err)
	}
	if w := post(agent.Token, in); w.Code != 401 {
		t.Fatal("agent created owner snapshot", w.Code)
	}
	w := post(f.token, in)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "Private content") || strings.Contains(w.Body.String(), "hub_import") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("import response leaked original or reference")
	}
	var result core.HubImportResult
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Import.Snapshot || result.Import.Live || result.Import.RecordCount != 1 {
		t.Fatal("not honest snapshot metadata")
	}
	other, err := f.store.HubConnections(context.Background(), f.bob.ID)
	if err != nil || len(other) != 0 {
		t.Fatal("cross-owner snapshot", err)
	}
	in.Content = strings.Repeat("x", core.HubImportMaxBytes+1)
	if w = post(f.token, in); w.Code != 400 {
		t.Fatal("oversized snapshot accepted", w.Code)
	}
}
func TestHubImportEndpointFailsWithoutCredentialEncryption(t *testing.T) {
	f := newV2APIFixture(t)
	mux := http.NewServeMux()
	registerHubImportWithKey(mux, f.store, nil)
	r := httptest.NewRequest("POST", "/v1/hub/imports", strings.NewReader(`{"provider_id":"markdown-import","account_id":"notes","display_name":"Notes","filename":"note.md","format":"markdown","content":"# Original"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+f.token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal("missing encryption accepted", w.Code, w.Body.String())
	}
}

// Exercise the real V2 route registration and middleware using only an isolated
// database, synthetic identities and fixture text. No live account is approved.
func TestHubImportV2AgentReadRequiresApprovalAndReimportRevokes(t *testing.T) {
	t.Setenv("EDC_HUB_CREDENTIAL_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{62}, 32)))
	f := newV2APIFixture(t)
	post := func(path, token string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		w := f.request(t, "POST", path, token, "application/json", v2JSONBody(t, body))
		if w.Code != want {
			t.Fatalf("%s: status %d, expected %d; %s", path, w.Code, want, w.Body.String())
		}
		return w
	}
	input := core.HubImportInput{ProviderID: "whatsapp-import", AccountID: "synthetic-chat", DisplayName: "Fixture export", Filename: "fixture.txt", Format: "whatsapp-text", Content: "19/09/2026, 10:00 - Fixture Sender: First synthetic snapshot\nSecond line\n"}
	imported := decodeV2Response[core.HubImportResult](t, post("/v1/hub/imports", f.token, input, http.StatusCreated))
	if !imported.Import.Snapshot || imported.Import.Live {
		t.Fatal("import incorrectly advertised as a live account")
	}
	registration := decodeV2Response[core.HubAgentRegistration](t, post("/v1/hub/agents", "", map[string]string{"owner": f.alice.Username, "name": "Synthetic import reader"}, http.StatusCreated))
	post("/v1/hub/agents/"+registration.ID+"/decision", f.token, map[string]string{"decision": "approve", "verification_code": registration.VerificationCode}, http.StatusOK)
	requested := decodeV2Response[struct {
		Request core.HubRequest `json:"request"`
	}](t, post("/v1/hub/requests", registration.Token, map[string]any{"connection_id": imported.Connection.ID, "operation": "records.list", "reason": "Read only this synthetic fixture snapshot", "duration_seconds": 3600}, http.StatusCreated))
	execute := map[string]any{"connection_id": imported.Connection.ID, "operation": "records.list", "args": map[string]any{"offset": 0, "limit": 10}}
	denied := post("/v1/hub/execute", registration.Token, execute, http.StatusForbidden)
	deniedError := decodeV2Response[struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}](t, denied)
	if deniedError.Error.Code != "authorization_required" {
		t.Fatal("pending grant did not return actionable authorization error")
	}
	post("/v1/hub/requests/"+requested.Request.ID+"/decision", f.token, map[string]string{"decision": "approve"}, http.StatusOK)
	read := decodeV2Response[struct {
		ConnectionID string             `json:"connection_id"`
		Operation    string             `json:"operation"`
		Data         core.HubImportPage `json:"data"`
	}](t, post("/v1/hub/execute", registration.Token, execute, http.StatusOK))
	if read.ConnectionID != imported.Connection.ID || read.Operation != "records.list" || read.Data.Import.ID != imported.Import.ID || len(read.Data.Records) != 1 || read.Data.Records[0].Raw != input.Content || read.Data.NextOffset != nil {
		t.Fatal("HTTP execution lost snapshot identity, original bytes or pagination")
	}
	if !read.Data.Records[0].TimestampUncertain {
		t.Fatal("WhatsApp timestamp was presented as timezone-verified")
	}
	input.Content = "19/09/2026, 11:00 - Fixture Sender: Replacement synthetic snapshot\n"
	replaced := decodeV2Response[core.HubImportResult](t, post("/v1/hub/imports", f.token, input, http.StatusCreated))
	if replaced.Connection.ID != imported.Connection.ID || replaced.Import.ID == imported.Import.ID {
		t.Fatal("reimport must preserve account identity and create a new snapshot")
	}
	denied = post("/v1/hub/execute", registration.Token, execute, http.StatusForbidden)
	if strings.Contains(denied.Body.String(), "Replacement synthetic snapshot") || strings.Contains(denied.Body.String(), "First synthetic snapshot") {
		t.Fatal("reimport returned source content under a revoked grant")
	}
	requests := f.request(t, "GET", "/v1/hub/requests", registration.Token, "", nil)
	if requests.Code != http.StatusOK {
		t.Fatal("could not inspect post-reimport grant status", requests.Code)
	}
	list := decodeV2Response[struct {
		Requests []core.HubRequest `json:"requests"`
	}](t, requests)
	if len(list.Requests) != 1 || list.Requests[0].ID != requested.Request.ID || list.Requests[0].Status != "revoked" {
		t.Fatal("reimport did not revoke the original grant")
	}
}

func TestHubImportV2EscapedOneMiBPayloadUsesOnlyImportBodyAllowance(t *testing.T) {
	t.Setenv("EDC_HUB_CREDENTIAL_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{63}, 32)))
	f := newV2APIFixture(t)
	// encoding/json escapes '<' as six bytes, so this exact 1 MiB original
	// exceeds the normal 2 MiB JSON middleware allowance without exceeding
	// the import-specific bounded allowance.
	input := core.HubImportInput{ProviderID: "markdown-import", AccountID: "large-fixture", DisplayName: "Escaped fixture", Filename: "fixture.md", Format: "markdown", Content: strings.Repeat("<", core.HubImportMaxBytes)}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= core.MaxRequestBytes {
		t.Fatal("test payload does not exercise the outer middleware exception")
	}
	w := f.request(t, "POST", "/v1/hub/imports", f.token, "application/json", bytes.NewReader(raw))
	if w.Code != http.StatusCreated {
		t.Fatalf("exact 1 MiB escaped import rejected by V2 middleware: %d %s", w.Code, w.Body.String())
	}
	imported := decodeV2Response[core.HubImportResult](t, w)
	digest := sha256.Sum256([]byte(input.Content))
	if imported.Import.Bytes != core.HubImportMaxBytes || imported.Import.SHA256 != hex.EncodeToString(digest[:]) || imported.Import.RecordCount != 1 {
		t.Fatal("decoded original length, hash or record count changed")
	}
	ordinary := f.request(t, "POST", "/v1/hub/connections", f.token, "application/json", bytes.NewReader(raw))
	if ordinary.Code != http.StatusRequestEntityTooLarge {
		t.Fatal("import allowance leaked to ordinary JSON routes", ordinary.Code)
	}
	input.Content += "<"
	oversized := f.request(t, "POST", "/v1/hub/imports", f.token, "application/json", v2JSONBody(t, input))
	if oversized.Code != http.StatusBadRequest {
		t.Fatal("decoded content larger than 1 MiB was accepted", oversized.Code)
	}
}

// Cover the real HTTP execution switch for personal sources as well as core parsing.
func TestPersonalChatImportsV2ReadBoundaries(t *testing.T) {
	f, _, owner := registryFixture(t)
	registration, err := f.store.RegisterHubAgent(context.Background(), f.alice.Username, "Personal fixture reader")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.DecideHubAgent(owner, registration.ID, "approve", registration.VerificationCode); err != nil {
		t.Fatal(err)
	}
	agent, err := f.store.HubAgent(context.Background(), registration.Token, true)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	chat, _ := z.CreateHeader(&zip.FileHeader{Name: "_chat.txt", Method: zip.Store})
	chat.Write([]byte("19/09/2026, 10:00 - Fixture: Fixture WhatsApp text\n"))
	media, _ := z.CreateHeader(&zip.FileHeader{Name: "media.bin", Method: zip.Store})
	media.Write(bytes.Repeat([]byte{0}, 3<<20))
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	for _, in := range []core.HubImportInput{
		{ProviderID: "whatsapp-import", AccountID: "fixture-zip", DisplayName: "Personal ZIP", Filename: "chat.zip", Format: "whatsapp-zip", ContentBase64: base64.StdEncoding.EncodeToString(archive.Bytes())},
		{ProviderID: "telegram-import", AccountID: "fixture-personal", DisplayName: "Personal Telegram", Filename: "result.json", Format: "telegram-json", Content: `{"id":123,"name":"Fixture","messages":[{"id":1,"type":"message","date":"2026-10-07T12:00:00","from":"Fixture","text":"Fixture Telegram text"}]}`},
		{ProviderID: "wechat-import", AccountID: "fixture-personal", DisplayName: "Prepared WeChat", Filename: "chat.csv", Format: "wechat-csv", Content: "timestamp,sender,text\n2026-10-07,Fixture,Fixture WeChat text\n"},
	} {
		w := f.request(t, "POST", "/v1/hub/imports", f.token, "application/json", v2JSONBody(t, in))
		if w.Code != 201 {
			t.Fatal(in.ProviderID, w.Code, w.Body.String())
		}
		result := decodeV2Response[core.HubImportResult](t, w)
		execute := map[string]any{"connection_id": result.Connection.ID, "operation": "records.list", "args": map[string]any{"limit": 1}}
		w = f.request(t, "POST", "/v1/hub/execute", registration.Token, "application/json", v2JSONBody(t, execute))
		if w.Code != 403 {
			t.Fatal("unapproved personal read", w.Code)
		}
		req, err := f.store.RequestHubAccess(context.Background(), agent, result.Connection.ID, "records.list", "Fixture read", 3600, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.store.DecideHubRequest(owner, req.ID, "approve"); err != nil {
			t.Fatal(err)
		}
		w = f.request(t, "POST", "/v1/hub/execute", registration.Token, "application/json", v2JSONBody(t, execute))
		if w.Code != 200 {
			t.Fatal("personal read", w.Code, w.Body.String())
		}
		page := decodeV2Response[struct{ Data core.HubImportPage }](t, w)
		if page.Data.Import.Live || !page.Data.Import.Snapshot || len(page.Data.Records) != 1 {
			t.Fatal("dishonest personal connection", w.Body.String())
		}
		if err = f.store.DisconnectHubConnection(owner, result.Connection.ID); err != nil {
			t.Fatal(err)
		}
		w = f.request(t, "POST", "/v1/hub/execute", registration.Token, "application/json", v2JSONBody(t, execute))
		if w.Code == 200 {
			t.Fatal("disconnected personal read")
		}
	}
}
