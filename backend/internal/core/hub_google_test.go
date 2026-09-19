package core

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestHubGoogleStateIsBoundEncryptedAndOneUse(t *testing.T) {
	s, owner, other, _, key := hubFixture(t)
	if _, err := s.db.Exec(HubGoogleSchema); err != nil {
		t.Fatal(err)
	}
	state, err := s.StartHubGoogle(owner, "gmail", "Work Gmail", key)
	if err != nil {
		t.Fatal(err)
	}
	if state.OwnerID != UserID(owner) || len(state.State) < 32 || len(state.Verifier) < 43 {
		t.Fatal("invalid state binding")
	}
	var sealed []byte
	var storedHash string
	if err = s.db.QueryRow("SELECT state_hash,verifier FROM hub_google_states").Scan(&storedHash, &sealed); err != nil {
		t.Fatal(err)
	}
	if storedHash == state.State || bytes.Contains(sealed, []byte(state.Verifier)) {
		t.Fatal("plaintext state or verifier persisted")
	}
	if _, err = s.ConsumeHubGoogle(context.Background(), strings.Repeat("x", 52), key); err == nil {
		t.Fatal("unknown state accepted")
	}
	got, err := s.ConsumeHubGoogle(other, state.State, key)
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerID != UserID(owner) || got.Verifier != state.Verifier || got.ProviderID != "gmail" {
		t.Fatal("callback changed owner binding")
	}
	if _, err = s.ConsumeHubGoogle(owner, state.State, key); err == nil {
		t.Fatal("state replay accepted")
	}
	expired, err := s.StartHubGoogle(owner, "google-drive", "Drive", key)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec("UPDATE hub_google_states SET expires_at=0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConsumeHubGoogle(owner, expired.State, key); err == nil {
		t.Fatal("expired state accepted")
	}
	if _, err = s.StartHubGoogle(context.Background(), "gmail", "Work", key); err == nil {
		t.Fatal("anonymous start accepted")
	}
}

func TestHubGoogleReconnectAndRefreshCannotReviveDisconnect(t *testing.T) {
	s, owner, other, _, key := hubFixture(t)
	c, err := s.SaveHubGoogleConnection(owner, HubConnection{ProviderID: "gmail", AccountID: "verified@example.invalid", DisplayName: "Work"}, "secret-one", key)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateHubCredential(owner, c, "secret-one", "secret-two", key); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateHubCredential(owner, c, "secret-one", "stale-refresh", key); err == nil {
		t.Fatal("stale refresh overwrote a newer credential")
	}
	if got, err := s.HubCredential(owner, c, key); err != nil || got != "secret-two" {
		t.Fatal("refresh failed", err)
	}
	if err = s.DisconnectHubConnection(owner, c.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateHubCredential(owner, c, "secret-two", "secret-race", key); err == nil {
		t.Fatal("refresh revived disconnect")
	}
	reconnected, err := s.SaveHubGoogleConnection(owner, HubConnection{ProviderID: "gmail", AccountID: "verified@example.invalid", DisplayName: "Reconnected"}, "secret-three", key)
	if err != nil {
		t.Fatal(err)
	}
	if reconnected.ID != c.ID {
		t.Fatal("reconnect changed connection identity")
	}
	second, err := s.SaveHubGoogleConnection(owner, HubConnection{ProviderID: "gmail", AccountID: "personal@example.invalid", DisplayName: "Personal"}, "secret-four", key)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == c.ID {
		t.Fatal("two accounts aliased")
	}
	otherConnection, err := s.SaveHubGoogleConnection(other, HubConnection{ProviderID: "gmail", AccountID: "verified@example.invalid", DisplayName: "Other owner"}, "other-secret", key)
	if err != nil {
		t.Fatal(err)
	}
	if otherConnection.ID == c.ID || otherConnection.OwnerID == c.OwnerID {
		t.Fatal("owners aliased")
	}
}
