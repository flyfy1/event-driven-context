package hubconnectors

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestGoogleOAuthAuthorizationURL(t *testing.T) {
	g := GoogleOAuth{ClientID: "test-client", ClientSecret: "secret", RedirectURL: "https://hub.example/oauth/callback"}
	verifier := strings.Repeat("v", 43)
	state := strings.Repeat("s", 32)
	u, err := g.AuthorizationURL(state, verifier, []string{"google-drive", "gmail", "gmail"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(u)
	q := parsed.Query()
	hash := sha256.Sum256([]byte(verifier))
	if parsed.Host != "accounts.google.com" || q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(hash[:]) || q.Get("code_challenge_method") != "S256" || q.Get("state") != state || q.Get("access_type") != "offline" {
		t.Fatal("invalid authorization URL")
	}
	if q.Get("scope") != "https://www.googleapis.com/auth/drive.readonly https://www.googleapis.com/auth/gmail.readonly" {
		t.Fatalf("unexpected scopes %s", q.Get("scope"))
	}
	if strings.Contains(u, "secret") || q.Get("code_verifier") != "" {
		t.Fatal("authorization URL leaked secrets")
	}
	if _, err = g.AuthorizationURL("short", verifier, []string{"gmail"}); err == nil {
		t.Fatal("short state accepted")
	}
	if _, err = g.AuthorizationURL(state, verifier, []string{"whatsapp-business"}); err == nil {
		t.Fatal("unsupported provider accepted")
	}
	g.RedirectURL = "http://public.example/callback"
	if _, err = g.AuthorizationURL(state, verifier, []string{"gmail"}); err == nil {
		t.Fatal("insecure public redirect accepted")
	}
}

func TestGoogleOAuthExchangeRefreshAndFailure(t *testing.T) {
	for _, mode := range []string{"exchange", "refresh", "error", "redirect", "invalid_token"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.Method != "POST" || r.URL.Path != "/token" || r.Form.Get("client_secret") != "test-secret" {
					t.Error("invalid token request")
				}
				if mode == "refresh" {
					if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "stored-refresh" {
						t.Error("invalid refresh request")
					}
				} else if r.Form.Get("code_verifier") != strings.Repeat("v", 43) || r.Form.Get("code") != "owner-code" {
					t.Error("missing PKCE code exchange")
				}
				switch mode {
				case "error":
					w.WriteHeader(400)
					_, _ = io.WriteString(w, `{"error":"private-secret"}`)
				case "redirect":
					w.Header().Set("Location", "https://attacker.example/token")
					w.WriteHeader(302)
				case "invalid_token":
					_, _ = io.WriteString(w, `{"access_token":"token","token_type":"Basic","expires_in":3600}`)
				default:
					_, _ = io.WriteString(w, `{"access_token":"token","token_type":"Bearer","expires_in":3600,"scope":"https://www.googleapis.com/auth/gmail.readonly"}`)
				}
			}, func(r *http.Request) {
				if r.URL.Host != "oauth2.googleapis.com" || r.URL.Scheme != "https" {
					t.Error("invalid OAuth origin")
				}
			})
			g := GoogleOAuth{ClientID: "client", ClientSecret: "test-secret", RedirectURL: "http://127.0.0.1:8888/callback", HTTPClient: client}
			var got GoogleToken
			var err error
			if mode == "refresh" {
				got, err = g.Refresh(context.Background(), "stored-refresh")
			} else {
				got, err = g.ExchangeCode(context.Background(), "owner-code", strings.Repeat("v", 43))
			}
			if mode == "exchange" || mode == "refresh" {
				if err != nil || got.AccessToken != "token" || got.ExpiresIn != 3600 {
					t.Fatalf("exchange failure: %v", err)
				}
				if mode == "refresh" && got.RefreshToken != "" {
					t.Error("invented refresh token")
				}
			} else {
				if err == nil {
					t.Fatal("expected failure")
				}
				if strings.Contains(err.Error(), "private-secret") || strings.Contains(err.Error(), "test-secret") {
					t.Fatal("secret in error")
				}
			}
			if calls != 1 {
				t.Errorf("unexpected retry or redirect: %d calls", calls)
			}
		})
	}
}
