package core

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestOAuthScopeNormalizationAndExpiredCode(t *testing.T) {
	if got, err := NormalizeOAuthScope(ScopeWrite + " " + ScopeRead); err != nil || got != ScopeRead+" "+ScopeWrite {
		t.Fatalf("normalized scope = %q, %v", got, err)
	}
	for _, invalid := range []string{"admin", ScopeRead + " " + ScopeRead} {
		if _, err := NormalizeOAuthScope(invalid); err == nil {
			t.Fatalf("accepted invalid scope %q", invalid)
		}
	}
	s, err := Open(t.TempDir() + "/oauth.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	user, err := s.Register(ctx, Credentials{Username: "oauth-user", Password: "integration-password-123"})
	if err != nil {
		t.Fatal(err)
	}
	client, err := s.RegisterOAuthClient(ctx, "Expired Code Client", []string{"https://client.example/callback"})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("v", 64)
	sum := sha256.Sum256([]byte(verifier))
	request := OAuthRequest{ClientID: client.ClientID, RedirectURI: client.RedirectURIs[0], State: "state", CodeChallenge: base64.RawURLEncoding.EncodeToString(sum[:]), Scope: ScopeRead, Resource: "https://context.example/mcp"}
	if err = s.CreateOAuthRequest(ctx, "request", "csrf", request, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApproveOAuthRequest(ctx, "request", "csrf", user.ID, "expired-code", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ExchangeOAuthCode(ctx, "expired-code", client.ClientID, client.RedirectURIs[0], verifier, request.Resource, time.Hour); err == nil {
		t.Fatal("expired authorization code accepted")
	}
}
