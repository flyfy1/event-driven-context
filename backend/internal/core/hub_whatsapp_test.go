package core

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"event-driven-context/internal/hubwebhooks"
)

func TestHubWhatsAppAtomicEncryptedRetention(t *testing.T) {
	s, owner, other, _, key := hubFixture(t)
	credential := "fixture-private-configuration"
	c, err := s.AddHubConnection(owner, HubConnection{ProviderID: "whatsapp-business", AccountID: "2001", DisplayName: "Fixture"}, credential, key)
	if err != nil {
		t.Fatal(err)
	}
	v, err := hubwebhooks.NewWhatsAppVerifier(hubwebhooks.WhatsAppConfig{AppSecret: "synthetic-app-secret-for-fixtures", VerifyToken: "synthetic-verification-token", Binding: hubwebhooks.WhatsAppBinding{OwnerID: c.OwnerID, ConnectionID: c.ID, WABAID: "1001", PhoneNumberID: "2001"}})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"object":"whatsapp_business_account","entry":[{"id":"1001","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"2001"},"messages":[{"id":"wamid.fixture","text":{"body":"private-fixture-message"}}]}}]}]}`
	verify := func(body string) hubwebhooks.VerifiedWhatsAppEnvelope {
		mac := hmac.New(sha256.New, []byte("synthetic-app-secret-for-fixtures"))
		mac.Write([]byte(body))
		e, err := v.Verify("sha256="+hex.EncodeToString(mac.Sum(nil)), []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	e := verify(body)
	receipt, err := s.IngestHubWhatsApp(context.Background(), c, credential, key, e)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := s.IngestHubWhatsApp(context.Background(), c, credential, key, e)
	if err != nil || repeat != receipt {
		t.Fatal("receipt replay", err)
	}
	var encrypted []byte
	if err = s.db.QueryRow("SELECT encrypted_body FROM hub_webhook_receipts WHERE id=?", receipt).Scan(&encrypted); err != nil || bytes.Contains(encrypted, []byte("private-fixture-message")) {
		t.Fatal("plaintext receipt")
	}
	if err = s.db.QueryRow("SELECT encrypted_body FROM hub_webhook_events WHERE connection_id=?", c.ID).Scan(&encrypted); err != nil || bytes.Contains(encrypted, []byte("private-fixture-message")) {
		t.Fatal("plaintext event")
	}
	raw, err := s.ReadHubWhatsApp(owner, c, key, "events.list", nil)
	if err != nil || len(raw.(HubWhatsAppPage).Events) != 1 {
		t.Fatal("read", err)
	}
	// The same body cannot be ingested under another owner or credential generation.
	outsider, err := s.AddHubConnection(other, HubConnection{ProviderID: "whatsapp-business", AccountID: "2001", DisplayName: "Other"}, credential, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.IngestHubWhatsApp(context.Background(), outsider, credential, key, e); err == nil {
		t.Fatal("cross-owner ingestion")
	}
	if _, err = s.ReadHubWhatsApp(other, outsider, key, "receipts.get", map[string]any{"receipt_id": receipt}); err == nil {
		t.Fatal("cross-account original")
	}
	// One malformed message aborts the entire batch before any original is stored.
	invalid := `{"object":"whatsapp_business_account","entry":[{"id":"1001","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"2001"},"messages":[{"id":"wamid.second"},{"text":{"body":"missing-id"}}]}}]}]}`
	if _, err = s.IngestHubWhatsApp(context.Background(), c, credential, key, verify(invalid)); err == nil {
		t.Fatal("partial batch persisted")
	}
	var count int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM hub_webhook_receipts WHERE connection_id=?", c.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("partial receipt", count, err)
	}
	if _, err = s.AddHubConnection(owner, c, "rotated-config", key); err != nil {
		t.Fatal(err)
	}
	if _, err = s.IngestHubWhatsApp(context.Background(), c, credential, key, e); err == nil {
		t.Fatal("stale verifier accepted after rotation")
	}
	// Retained encrypted data survives reconfiguration; a wrong key cannot read it.
	if _, err = s.ReadHubWhatsApp(owner, c, bytes.Repeat([]byte{55}, 32), "events.list", nil); err == nil {
		t.Fatal("wrong key read")
	}
	result, err := s.ReadHubWhatsApp(owner, c, key, "receipts.get", map[string]any{"receipt_id": receipt})
	if err != nil || string(result.(map[string]any)["original"].(json.RawMessage)) != body {
		t.Fatal("retained original lost", err)
	}
}
