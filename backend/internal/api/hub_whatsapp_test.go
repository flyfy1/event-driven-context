package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"event-driven-context/internal/core"
)

const whatsappFixtureSecret = `{"app_secret":"synthetic-app-secret-for-fixtures","verify_token":"synthetic-verification-token","waba_id":"1001","phone_number_id":"2001"}`
const whatsappFixtureBody = `{"object":"whatsapp_business_account","entry":[{"id":"1001","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"2001"},"messages":[{"id":"wamid.fixture","text":{"body":"Synthetic fixture"}}],"statuses":[{"id":"wamid.sent","status":"delivered","timestamp":"123"}]}}]}]}`

func TestHubWhatsAppWebhookAndAuthorizedReads(t *testing.T) {
	f, _, owner := registryFixture(t)
	// Owner setup validates the config. Anonymous and agent identities cannot set it.
	add := func(credential string, token string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]string{"provider_id": "whatsapp-business", "account_id": "2001", "display_name": "Fixture business", "credential": credential})
		return f.request(t, "POST", "/v1/hub/connections", token, "application/json", bytes.NewReader(raw))
	}
	if w := add(`{"app_secret":"too-short"}`, f.token); w.Code != 400 {
		t.Fatal("invalid config", w.Code, w.Body.String())
	}
	if w := add(whatsappFixtureSecret, ""); w.Code != 401 {
		t.Fatal("anonymous setup", w.Code)
	}
	w := add(whatsappFixtureSecret, f.token)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var c core.HubConnection
	if json.Unmarshal(w.Body.Bytes(), &c) != nil || c.ID == "" {
		t.Fatal(w.Body.String())
	}
	path := "/v1/hub/webhooks/whatsapp/" + c.ID
	challenge := f.request(t, "GET", path+"?hub.mode=subscribe&hub.verify_token=synthetic-verification-token&hub.challenge=123", "", "", nil)
	if challenge.Code != 200 || challenge.Body.String() != "123" {
		t.Fatal("challenge", challenge.Code, challenge.Body.String())
	}
	for _, query := range []string{"hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=123", "hub.mode=subscribe&hub.verify_token=synthetic-verification-token&hub.challenge=1&hub.challenge=2"} {
		if w := f.request(t, "GET", path+"?"+query, "", "", nil); w.Code != 403 {
			t.Fatal("ambiguous challenge accepted")
		}
	}
	post := func(body, secret string, duplicateHeader bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(body))
		r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		if duplicateHeader {
			r.Header.Add("X-Hub-Signature-256", r.Header.Get("X-Hub-Signature-256"))
		}
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}
	if w := post(whatsappFixtureBody, "wrong", false); w.Code != 403 {
		t.Fatal("invalid signature", w.Code)
	}
	if w := post(whatsappFixtureBody, "synthetic-app-secret-for-fixtures", true); w.Code != 403 {
		t.Fatal("duplicate header")
	}
	if w := post(strings.Replace(whatsappFixtureBody, `"2001"`, `"2002"`, 1), "synthetic-app-secret-for-fixtures", false); w.Code != 403 {
		t.Fatal("wrong phone", w.Code)
	}
	for _, body := range []string{whatsappFixtureBody, whatsappFixtureBody, " \n" + whatsappFixtureBody} {
		if w := post(body, "synthetic-app-secret-for-fixtures", false); w.Code != 200 {
			t.Fatal("delivery", w.Code, w.Body.String())
		}
	}
	_, registry := registryRequest(t, f.handler, "/v1/hub/integrations", f.token)
	if row := registryProvider(t, registry, "whatsapp-business"); !row.Visible || row.ConnectedAccountCount != 1 {
		t.Fatal("configured source absent")
	}
	registration, err := f.store.RegisterHubAgent(context.Background(), f.alice.Username, "Fixture reader")
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
	read := func(op string, args map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"connection_id": c.ID, "operation": op, "args": args})
		return f.request(t, "POST", "/v1/hub/execute", registration.Token, "application/json", bytes.NewReader(raw))
	}
	if w := read("events.list", nil); w.Code != 403 {
		t.Fatal("unapproved read", w.Code)
	}
	approve := func(op string) core.HubRequest {
		req, err := f.store.RequestHubAccess(context.Background(), agent, c.ID, op, "Fixture read", 3600, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.store.DecideHubRequest(owner, req.ID, "approve"); err != nil {
			t.Fatal(err)
		}
		return req
	}
	grant := approve("events.list")
	w = read("events.list", map[string]any{"limit": 1})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var page struct{ Data core.HubWhatsAppPage }
	if json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Data.Events) != 1 || !page.Data.HasMore {
		t.Fatal("first page", w.Body.String())
	}
	first := page.Data.Events[0]
	w = read("events.list", map[string]any{"after_sequence": page.Data.NextSequence, "limit": 100})
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Data.Events) != 1 || page.Data.HasMore {
		t.Fatal("dedup/pagination", w.Body.String())
	}
	if w := read("receipts.get", map[string]any{"receipt_id": first.ReceiptID}); w.Code != 403 {
		t.Fatal("grant broadened")
	}
	approve("receipts.get")
	w = read("receipts.get", map[string]any{"receipt_id": first.ReceiptID})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"original_base64"`) {
		t.Fatal("original not readable", w.Body.String())
	}
	if err = f.store.DecideHubRequest(owner, grant.ID, "revoke"); err != nil {
		t.Fatal(err)
	}
	if w := read("events.list", nil); w.Code != 403 {
		t.Fatal("revoked grant")
	}
	if err = f.store.DisconnectHubConnection(owner, c.ID); err != nil {
		t.Fatal(err)
	}
	if w := post(whatsappFixtureBody, "synthetic-app-secret-for-fixtures", false); w.Code != 403 {
		t.Fatal("disconnected webhook", w.Code)
	}
	if w := read("receipts.get", map[string]any{"receipt_id": first.ReceiptID}); w.Code == 200 {
		t.Fatal("disconnected read")
	}
}
