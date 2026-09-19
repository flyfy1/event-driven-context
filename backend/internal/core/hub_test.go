package core

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func hubFixture(t *testing.T) (*Store, context.Context, context.Context, HubAgentRegistration, []byte) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "identity.db"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	owner, err := s.Register(context.Background(), Credentials{Username: "hub-owner", Email: "hub-owner@example.invalid", Password: "test-password-long-enough"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Register(context.Background(), Credentials{Username: "hub-other", Email: "hub-other@example.invalid", Password: "test-password-long-enough"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.RegisterHubAgent(context.Background(), owner.Username, "Coding Agent")
	if err != nil {
		t.Fatal(err)
	}
	return s, WithUser(context.Background(), owner.ID), WithUser(context.Background(), other.ID), a, bytes.Repeat([]byte{37}, 32)
}
func TestHubAuthorizationIsolationRevocationAndPersistence(t *testing.T) {
	s, owner, other, registration, key := hubFixture(t)
	if _, _, err := s.Authenticate(owner, registration.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("agent token authenticated as owner", err)
	}
	if _, err := s.HubAgent(owner, registration.Token, true); err == nil {
		t.Fatal("pending agent can discover accounts")
	}
	if err := s.DecideHubAgent(other, registration.ID, "approve", registration.VerificationCode); err == nil {
		t.Fatal("other owner approved agent")
	}
	if err := s.DecideHubAgent(owner, registration.ID, "approve", "WRONG"); err == nil {
		t.Fatal("wrong verification code approved")
	}
	if err := s.DecideHubAgent(owner, registration.ID, "approve", registration.VerificationCode); err != nil {
		t.Fatal(err)
	}
	a, err := s.HubAgent(owner, registration.Token, true)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.AddHubConnection(owner, HubConnection{ProviderID: "gmail", AccountID: "work@example.invalid", DisplayName: "Work"}, "private-provider-secret", key)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := s.AddHubConnection(owner, HubConnection{ProviderID: "gmail", AccountID: "personal@example.invalid", DisplayName: "Personal"}, "other-provider-secret", key)
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := s.AddHubConnection(other, HubConnection{ProviderID: "gmail", AccountID: "outsider@example.invalid", DisplayName: "Other"}, "secret", key)
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.HubConnections(owner, UserID(owner))
	if err != nil || len(list) != 2 {
		t.Fatal("multiaccount or owner isolation", list, err)
	}
	if _, err = s.RequestHubAccess(owner, a, outsider.ID, "messages.get", "Cross owner", 3600); err == nil {
		t.Fatal("cross owner request succeeded")
	}
	r, err := s.RequestHubAccess(owner, a, c.ID, "messages.get", "Read one project thread", 3600)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := s.RequestHubAccess(owner, a, c.ID, "messages.get", "Read one project thread", 3600)
	if err != nil || repeat.ID != r.ID {
		t.Fatal("duplicate request", repeat, err)
	}
	if err = s.CheckHubAccess(owner, a, c.ID, "messages.get"); err == nil {
		t.Fatal("pending grant executed")
	}
	if err = s.DecideHubRequest(other, r.ID, "approve"); err == nil {
		t.Fatal("other owner approved request")
	}
	if err = s.DecideHubRequest(owner, r.ID, "approve"); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckHubAccess(owner, a, c.ID, "messages.get"); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckHubAccess(owner, a, c2.ID, "messages.get"); err == nil {
		t.Fatal("grant leaked to another account")
	}
	if err = s.CheckHubAccess(owner, a, c.ID, "messages.list"); err == nil {
		t.Fatal("grant leaked to another operation")
	}
	secret, err := s.HubCredential(owner, c, key)
	if err != nil || secret != "private-provider-secret" {
		t.Fatal("credential roundtrip failed")
	}
	var stored []byte
	if err = s.db.QueryRow("SELECT credential FROM hub_connections WHERE id=?", c.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte(secret)) {
		t.Fatal("stored plaintext credential")
	}
	if _, err = s.HubCredential(owner, c, bytes.Repeat([]byte{99}, 32)); err == nil {
		t.Fatal("wrong key decrypted")
	}
	if err = s.DecideHubRequest(owner, r.ID, "revoke"); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckHubAccess(owner, a, c.ID, "messages.get"); err == nil {
		t.Fatal("revoked grant still usable")
	}
	if err = s.DecideHubRequest(owner, r.ID, "approve"); err == nil {
		t.Fatal("revoked request replayed")
	}
	r, err = s.RequestHubAccess(owner, a, c.ID, "messages.get", "New authorization", 3600)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DecideHubRequest(owner, r.ID, "approve"); err != nil {
		t.Fatal(err)
	}
	if err = s.DisconnectHubConnection(other, c.ID); err == nil {
		t.Fatal("other owner disconnected source")
	}
	if err = s.DisconnectHubConnection(owner, c.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckHubAccess(owner, a, c.ID, "messages.get"); err == nil {
		t.Fatal("disconnected source accessible")
	}
	if _, err = s.HubCredential(owner, c, key); err == nil {
		t.Fatal("disconnected credential retained")
	}
	if err = s.DecideHubAgent(owner, a.ID, "revoke", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.HubAgent(owner, registration.Token, true); err == nil {
		t.Fatal("revoked identity remains approved")
	}
}
func TestHubExpiryAndSecretlessAccounts(t *testing.T) {
	s, owner, _, registration, key := hubFixture(t)
	c, err := s.AddHubConnection(owner, HubConnection{ProviderID: "whatsapp-business", AccountID: "business-1", DisplayName: "Business"}, "", nil)
	if err != nil || c.Status != "needs_auth" {
		t.Fatal(c, err)
	}
	if _, err = s.AddHubConnection(owner, HubConnection{ProviderID: "gmail", AccountID: "work", DisplayName: "Work"}, "secret", nil); err == nil {
		t.Fatal("credential stored with no key")
	}
	if _, err = s.db.Exec("UPDATE hub_agents SET expires_at=? WHERE id=?", time.Now().Add(-time.Second).Unix(), registration.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DecideHubAgent(owner, registration.ID, "approve", registration.VerificationCode); err == nil {
		t.Fatal("expired pairing approved")
	}
	a2, err := s.RegisterHubAgent(owner, "hub-owner", "Fresh Agent")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DecideHubAgent(owner, a2.ID, "approve", a2.VerificationCode); err != nil {
		t.Fatal(err)
	}
	a, err := s.HubAgent(owner, a2.Token, true)
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.AddHubConnection(owner, HubConnection{ProviderID: "gmail", AccountID: "work", DisplayName: "Work"}, "secret", key)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.RequestHubAccess(owner, a, c.ID, "messages.list", "Read", 60)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DecideHubRequest(owner, r.ID, "approve"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE hub_requests SET expires_at=? WHERE id=?", time.Now().Add(-time.Second).Unix(), r.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckHubAccess(owner, a, c.ID, "messages.list"); err == nil {
		t.Fatal("expired grant accessible")
	}
	requests, err := s.HubRequests(owner, UserID(owner), a.ID)
	if err != nil || requests[0].Status != "expired" {
		t.Fatal(requests, err)
	}
}

func TestHubReconnectionRevokesGrantsAndStaleAgentCannotRequest(t *testing.T) {
	s, owner, _, registration, key := hubFixture(t)
	if err := s.DecideHubAgent(owner, registration.ID, "approve", registration.VerificationCode); err != nil {
		t.Fatal(err)
	}
	a, err := s.HubAgent(owner, registration.Token, true)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.AddHubConnection(owner, HubConnection{ProviderID: "gmail", AccountID: "work", DisplayName: "Work"}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := s.AddHubConnection(owner, c, "secret", key)
	if err != nil || configured.ID != c.ID {
		t.Fatal("could not configure account", err)
	}
	r, err := s.RequestHubAccess(owner, a, c.ID, "messages.list", "Read", 3600)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DecideHubRequest(owner, r.ID, "approve"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddHubConnection(owner, c, "replacement", key); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckHubAccess(owner, a, c.ID, "messages.list"); err == nil {
		t.Fatal("reconnect inherited grant")
	}
	if err = s.DecideHubAgent(owner, a.ID, "revoke", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RequestHubAccess(owner, a, c.ID, "messages.list", "Stale snapshot", 3600); err == nil {
		t.Fatal("revoked agent created request")
	}
}
func TestHubExpiredRegistrationsDoNotExhaustLifetimeQuota(t *testing.T) {
	s, owner, _, _, _ := hubFixture(t)
	_, err := s.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<1000)
 INSERT INTO hub_agents(id,owner_id,name,token_hash,verification_code,status,created_at,expires_at)
 SELECT 'expired_'||x,?,'old','hash_'||x,'unused','pending',0,1 FROM n`, UserID(owner))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RegisterHubAgent(owner, "hub-owner", "New legitimate agent"); err != nil {
		t.Fatal(err)
	}
}
