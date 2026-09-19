package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"
	"os"
	"strings"
	"time"

	"event-driven-context/internal/core"
	"event-driven-context/internal/hubconnectors"
)

type hubGoogleCredential struct {
	Kind      string                    `json:"kind"`
	Token     hubconnectors.GoogleToken `json:"token"`
	ExpiresAt int64                     `json:"expires_at"`
}

func hubGoogleConfig() hubconnectors.GoogleOAuth {
	return hubconnectors.GoogleOAuth{ClientID: os.Getenv("EDC_HUB_GOOGLE_CLIENT_ID"), ClientSecret: os.Getenv("EDC_HUB_GOOGLE_CLIENT_SECRET"), RedirectURL: os.Getenv("EDC_HUB_GOOGLE_REDIRECT_URL")}
}
func hubGoogleConfigured(g hubconnectors.GoogleOAuth, key []byte) bool {
	return g.ClientID != "" && g.ClientSecret != "" && g.RedirectURL != "" && len(key) == 32
}

func registerHubGoogleHandlers(mux *http.ServeMux, store *core.Store, approvalURLs ...string) {
	key, _ := base64.StdEncoding.DecodeString(os.Getenv("EDC_HUB_CREDENTIAL_KEY"))
	registerHubGoogleWithConfig(mux, store, hubGoogleConfig(), key, approvalURLs...)
}

func registerHubGoogleWithConfig(mux *http.ServeMux, store *core.Store, g hubconnectors.GoogleOAuth, key []byte, approvalURLs ...string) {
	approvalURL := "/hub.html"
	if len(approvalURLs) > 0 {
		approvalURL = approvalURLs[0]
	}
	successPage := template.Must(template.New("google-connected").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><title>Google account connected</title><body><h1>Google account connected</h1><p>Your account is ready for separate Agent permission requests.</p><p><a href="{{.}}">Return to My authorizations</a></p></body></html>`))
	mux.HandleFunc("GET /v1/hub/google/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		respond(w, 200, map[string]bool{"configured": hubGoogleConfigured(g, key)})
	})
	mux.Handle("POST /v1/hub/google/start", authenticated(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !hubGoogleConfigured(g, key) {
			hubFail(w, &core.Error{Code: "service_unavailable", Message: "Google OAuth is not configured on this server"})
			return
		}
		var in struct {
			ProviderID  string `json:"provider_id"`
			DisplayName string `json:"display_name"`
		}
		if err := decode(r, &in); err != nil {
			hubFail(w, err)
			return
		}
		state, err := store.StartHubGoogle(r.Context(), in.ProviderID, in.DisplayName, key)
		if err != nil {
			hubFail(w, err)
			return
		}
		link, err := g.AuthorizationURL(state.State, state.Verifier, []string{state.ProviderID})
		if err != nil {
			_, _ = store.ConsumeHubGoogle(r.Context(), state.State, key)
			hubFail(w, &core.Error{Code: "service_unavailable", Message: "Google OAuth configuration is invalid"})
			return
		}
		http.SetCookie(w, hubGoogleCookie(state.State, strings.HasPrefix(g.RedirectURL, "https://"), 600))
		respond(w, 200, map[string]any{"authorization_url": link, "expires_in": 600, "provider_id": state.ProviderID, "scope_notice": "Google Drive or Gmail account-wide read access will be requested. Agent operation grants remain separate."})
	})))
	mux.HandleFunc("GET /v1/hub/google/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		if !hubGoogleConfigured(g, key) {
			http.Error(w, "Google OAuth is not configured on this server.", 503)
			return
		}
		rawState := r.URL.Query().Get("state")
		cookie, cookieErr := r.Cookie(hubGoogleCookieName(rawState))
		if cookieErr != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(rawState)) != 1 {
			http.Error(w, "This Google authorization must finish in the browser that started it.", 400)
			return
		}
		http.SetCookie(w, hubGoogleCookie(rawState, strings.HasPrefix(g.RedirectURL, "https://"), -1))
		state, err := store.ConsumeHubGoogle(r.Context(), rawState, key)
		if err != nil {
			http.Error(w, "This Google authorization is invalid or expired. Start again from My authorizations.", 400)
			return
		}
		if r.URL.Query().Get("error") != "" {
			http.Error(w, "Google authorization was not completed. You can try again from My authorizations.", 400)
			return
		}
		token, err := g.ExchangeCode(r.Context(), r.URL.Query().Get("code"), state.Verifier)
		if err != nil {
			http.Error(w, "Google authorization could not be completed. Start again from My authorizations.", 502)
			return
		}
		if !hubGoogleHasScope(token.Scope, state.ProviderID) {
			http.Error(w, "Google did not grant the required read scope. Start again and review the requested permission.", 403)
			return
		}
		accountID, err := hubconnectors.NewClient(g.HTTPClient).GoogleAccountID(r.Context(), state.ProviderID, token.AccessToken)
		if err != nil {
			http.Error(w, "Google account identity could not be verified. Start again from My authorizations.", 502)
			return
		}
		credential := hubGoogleCredential{Kind: "google_oauth", Token: token, ExpiresAt: time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).Unix()}
		body, err := json.Marshal(credential)
		if err != nil {
			http.Error(w, "Google authorization could not be saved.", 500)
			return
		}
		_, err = store.SaveHubGoogleConnection(core.WithUser(r.Context(), state.OwnerID), core.HubConnection{ProviderID: state.ProviderID, AccountID: accountID, DisplayName: state.DisplayName}, string(body), key)
		if err != nil {
			http.Error(w, "Google authorization could not be saved. Start again from My authorizations.", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = successPage.Execute(w, approvalURL)
	})
}

func hubGoogleCookieName(state string) string {
	hash := sha256.Sum256([]byte(state))
	return "edc_google_" + hex.EncodeToString(hash[:8])
}
func hubGoogleCookie(state string, secure bool, maxAge int) *http.Cookie {
	return &http.Cookie{Name: hubGoogleCookieName(state), Value: state, Path: "/v1/hub/google/callback", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
}

func hubGoogleHasScope(scopes, providerID string) bool {
	want := "https://www.googleapis.com/auth/gmail.readonly"
	if providerID == "google-drive" {
		want = "https://www.googleapis.com/auth/drive.readonly"
	}
	for _, scope := range strings.Fields(scopes) {
		if scope == want {
			return true
		}
	}
	return false
}

func hubProviderCredential(ctx context.Context, store *core.Store, c core.HubConnection, key []byte) (string, error) {
	return hubProviderCredentialWithConfig(ctx, store, c, key, hubGoogleConfig())
}

func hubProviderCredentialWithConfig(ctx context.Context, store *core.Store, c core.HubConnection, key []byte, g hubconnectors.GoogleOAuth) (string, error) {
	secret, err := store.HubCredential(ctx, c, key)
	if err != nil {
		return "", err
	}
	if c.ProviderID != "google-drive" && c.ProviderID != "gmail" {
		return secret, nil
	}
	// Manual access tokens remain supported; JSON represents managed credentials.
	if !strings.HasPrefix(strings.TrimSpace(secret), "{") {
		return secret, nil
	}
	var credential hubGoogleCredential
	if json.Unmarshal([]byte(secret), &credential) != nil || credential.Kind != "google_oauth" || credential.Token.AccessToken == "" {
		return "", &core.Error{Code: "service_unavailable", Message: "Google connection credential unavailable"}
	}
	if credential.ExpiresAt > time.Now().Add(time.Minute).Unix() {
		return credential.Token.AccessToken, nil
	}
	if credential.Token.RefreshToken == "" {
		return "", &core.Error{Code: "authorization_required", Message: "The owner must reconnect this Google account"}
	}
	refreshed, err := g.Refresh(ctx, credential.Token.RefreshToken)
	if err != nil {
		return "", &core.Error{Code: "authorization_required", Message: "Google token refresh failed; the owner may need to reconnect this account"}
	}
	if refreshed.Scope != "" && !hubGoogleHasScope(refreshed.Scope, c.ProviderID) {
		return "", &core.Error{Code: "authorization_required", Message: "Google read permission is no longer available"}
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = credential.Token.RefreshToken
	}
	if refreshed.Scope == "" {
		refreshed.Scope = credential.Token.Scope
	}
	credential.Token = refreshed
	credential.ExpiresAt = time.Now().Add(time.Duration(refreshed.ExpiresIn) * time.Second).Unix()
	body, err := json.Marshal(credential)
	if err != nil {
		return "", err
	}
	if err = store.UpdateHubCredential(ctx, c, secret, string(body), key); err != nil {
		return "", err
	}
	return refreshed.AccessToken, nil
}
