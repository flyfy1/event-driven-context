package api

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"event-driven-context/internal/core"
)

const (
	oauthRequestTTL = 10 * time.Minute
	oauthCodeTTL    = 5 * time.Minute
)

var pkcePattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

var oauthPage = template.Must(template.New("oauth").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Event-driven Context OAuth</title><style>body{font:16px system-ui,sans-serif;background:#f5f5f2;color:#20221f;margin:0}.card{max-width:520px;margin:8vh auto;padding:32px;background:#fff;border:1px solid #d9ddd5;border-radius:16px;box-shadow:0 8px 30px #00000010}h1{font-size:24px;margin-top:0}.muted{color:#60665e}.notice{padding:12px;background:#f2f5ee;border-radius:8px}.error{color:#a21b1b}label{display:block;margin:16px 0 6px}input{box-sizing:border-box;width:100%;font:inherit;padding:10px;border:1px solid #aeb5aa;border-radius:8px}.actions{display:flex;flex-wrap:wrap;gap:12px;margin-top:24px}button{font:inherit;padding:10px 18px;border:0;border-radius:8px;background:#265c3b;color:#fff;cursor:pointer}.secondary{background:#6b7169}code{word-break:break-all}</style></head><body><main class="card"><h1>Event-driven Context</h1><p class="muted">{{.ClientName}} is requesting access through OAuth.</p>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}{{if .Login}}<p class="notice">Sign in with your existing Event-driven Context account. Your password is sent only to this service and is never shared with the client.</p><form method="post" action="/oauth/authorize"><input type="hidden" name="request_id" value="{{.RequestID}}"><label for="username">Username</label><input id="username" name="username" autocomplete="username" required><label for="password">Password</label><input id="password" name="password" type="password" autocomplete="current-password" required><p class="muted">New here? A username uses 3–64 lowercase letters, digits, _, . or -, and a password uses 12–72 bytes.</p><div class="actions"><button type="submit" name="decision" value="login">Sign in</button><button class="secondary" type="submit" name="decision" value="register">Create account</button><button class="secondary" type="submit" name="decision" value="deny" formnovalidate>Cancel</button></div></form>{{else}}<p>Signed in as <strong>{{.Username}}</strong>.</p><p class="notice">Approve access to:</p><ul>{{range .Scopes}}<li><strong>{{.}}</strong> — {{if eq . "context:read"}}read projects, members, events, metadata, and files{{else}}create projects, add members, and append immutable events{{end}}</li>{{end}}</ul><p class="muted">Access is limited to <code>{{.Resource}}</code> and expires after one hour. Project membership rules still apply.</p><form method="post" action="/oauth/authorize"><input type="hidden" name="request_id" value="{{.RequestID}}"><div class="actions"><button type="submit" name="decision" value="approve">Authorize</button><button class="secondary" type="submit" name="decision" value="deny">Cancel</button></div></form>{{end}}</main></body></html>`))

type oauthPageData struct {
	RequestID, ClientName, Username, Resource, Error string
	Scopes                                           []string
	Login                                            bool
}

func registerOAuthHandlers(mux *http.ServeMux, store *core.Store, config Config) {
	base := strings.TrimRight(config.PublicBaseURL, "/")
	if config.OAuthAccessTokenTTL <= 0 {
		config.OAuthAccessTokenTTL = time.Hour
	}
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", func(w http.ResponseWriter, _ *http.Request) { oauthProtectedResource(w, base) })
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, _ *http.Request) { oauthProtectedResource(w, base) })
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) { oauthAuthorizationServer(w, base) })
	mux.HandleFunc("POST /oauth/register", func(w http.ResponseWriter, r *http.Request) { oauthRegister(w, r, store) })
	mux.HandleFunc("GET /oauth/authorize", func(w http.ResponseWriter, r *http.Request) { oauthAuthorizeGet(w, r, store, base) })
	mux.Handle("POST /oauth/authorize", newAuthGate().wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { oauthAuthorizePost(w, r, store, base) })))
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		oauthToken(w, r, store, base, config.OAuthAccessTokenTTL)
	})
}

func oauthProtectedResource(w http.ResponseWriter, base string) {
	respond(w, http.StatusOK, map[string]any{
		"resource": base + "/mcp", "resource_name": "Event-driven Context MCP",
		"authorization_servers": []string{base}, "scopes_supported": core.OAuthScopes,
		"bearer_methods_supported": []string{"header"},
	})
}

func oauthAuthorizationServer(w http.ResponseWriter, base string) {
	respond(w, http.StatusOK, map[string]any{
		"issuer": base, "authorization_endpoint": base + "/oauth/authorize",
		"token_endpoint": base + "/oauth/token", "registration_endpoint": base + "/oauth/register",
		"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code"},
		"token_endpoint_auth_methods_supported": []string{"none"}, "code_challenge_methods_supported": []string{"S256"},
		"scopes_supported": core.OAuthScopes, "authorization_response_iss_parameter_supported": true,
	})
}

func oauthRegister(w http.ResponseWriter, r *http.Request, store *core.Store) {
	if r.Header.Get("Authorization") != "" {
		logOAuthRegistrationRejection("authorization_header")
		oauthError(w, http.StatusBadRequest, "invalid_client", "public clients must not send credentials")
		return
	}
	var in struct {
		ClientName              string   `json:"client_name"`
		RedirectURIs            []string `json:"redirect_uris"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		logOAuthRegistrationRejection("content_type")
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid JSON client metadata")
		return
	}
	decoder := json.NewDecoder(r.Body)
	if decoder.Decode(&in) != nil {
		logOAuthRegistrationRejection("json")
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid JSON client metadata")
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		logOAuthRegistrationRejection("trailing_json")
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid JSON client metadata")
		return
	}
	grantTypesSupported := oauthRegistrationGrantTypesSupported(in.GrantTypes)
	responseTypesSupported := len(in.ResponseTypes) == 0 || len(in.ResponseTypes) == 1 && in.ResponseTypes[0] == "code"
	tokenAuthSupported := in.TokenEndpointAuthMethod == "" || in.TokenEndpointAuthMethod == "none"
	if !grantTypesSupported || !responseTypesSupported || !tokenAuthSupported {
		slog.Warn("OAuth client registration rejected", "reason", "unsupported_metadata", "grant_types_supported", grantTypesSupported, "response_types_supported", responseTypesSupported, "token_auth_supported", tokenAuthSupported)
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "only authorization_code public clients are supported")
		return
	}
	client, err := store.RegisterOAuthClient(r.Context(), in.ClientName, in.RedirectURIs)
	if err != nil {
		var appErr *core.Error
		if errors.As(err, &appErr) && appErr.Code == "rate_limited" {
			logOAuthRegistrationRejection("capacity")
			oauthError(w, http.StatusTooManyRequests, "temporarily_unavailable", appErr.Message)
		} else {
			logOAuthRegistrationRejection("client_or_redirect")
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "client metadata was rejected")
		}
		return
	}
	respond(w, http.StatusCreated, map[string]any{
		"client_id": client.ClientID, "client_name": client.ClientName, "redirect_uris": client.RedirectURIs,
		"grant_types": []string{"authorization_code"}, "response_types": []string{"code"},
		"token_endpoint_auth_method": "none", "client_id_issued_at": time.Now().Unix(),
	})
}

func logOAuthRegistrationRejection(reason string) {
	slog.Warn("OAuth client registration rejected", "reason", reason)
}

func oauthRegistrationGrantTypesSupported(grantTypes []string) bool {
	if len(grantTypes) == 0 {
		return true
	}
	hasAuthorizationCode := false
	for _, grantType := range grantTypes {
		switch grantType {
		case "authorization_code":
			hasAuthorizationCode = true
		case "refresh_token":
			// ChatGPT includes refresh_token in DCR even when discovery does not
			// advertise it. The registration response below narrows the client to
			// the authorization_code grant that this server actually supports.
		default:
			return false
		}
	}
	return hasAuthorizationCode
}

func oauthAuthorizeGet(w http.ResponseWriter, r *http.Request, store *core.Store, base string) {
	if requestID := r.URL.Query().Get("request_id"); requestID != "" {
		renderOAuthRequest(w, r, store, base, requestID, "")
		return
	}
	q := r.URL.Query()
	client, err := store.OAuthClient(r.Context(), q.Get("client_id"), q.Get("redirect_uri"))
	if err != nil || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || !pkcePattern.MatchString(q.Get("code_challenge")) || q.Get("resource") != base+"/mcp" || len(q.Get("state")) > 2048 {
		oauthError(w, http.StatusBadRequest, "invalid_request", "invalid OAuth authorization request")
		return
	}
	scope, err := core.NormalizeOAuthScope(q.Get("scope"))
	if err != nil {
		oauthRedirect(w, r, q.Get("redirect_uri"), q.Get("state"), base, "", "invalid_scope")
		return
	}
	requestID, csrf := randomSecret("oauth_request_"), randomSecret("oauth_csrf_")
	in := core.OAuthRequest{ClientID: client.ClientID, RedirectURI: q.Get("redirect_uri"), State: q.Get("state"), CodeChallenge: q.Get("code_challenge"), Scope: scope, Resource: q.Get("resource")}
	if err = store.CreateOAuthRequest(r.Context(), requestID, csrf, in, time.Now().Add(oauthRequestTTL)); err != nil {
		fail(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "edc_oauth_csrf", Value: csrf, Path: "/oauth/authorize", Secure: strings.HasPrefix(base, "https://"), HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(oauthRequestTTL.Seconds())})
	http.Redirect(w, r, "/oauth/authorize?request_id="+url.QueryEscape(requestID), http.StatusSeeOther)
}

func oauthAuthorizePost(w http.ResponseWriter, r *http.Request, store *core.Store, base string) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "invalid form")
		return
	}
	requestID := r.FormValue("request_id")
	csrf, _ := r.Cookie("edc_oauth_csrf")
	if csrf == nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "authorization request expired")
		return
	}
	request, err := store.OAuthRequest(r.Context(), requestID, csrf.Value)
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "authorization request expired")
		return
	}
	switch r.FormValue("decision") {
	case "deny":
		oauthRedirect(w, r, request.RedirectURI, request.State, base, "", "access_denied")
	case "login":
		credentials := core.Credentials{Username: r.FormValue("username"), Password: r.FormValue("password")}
		login, loginErr := store.Login(r.Context(), credentials)
		if loginErr != nil {
			renderOAuthRequest(w, r, store, base, requestID, "Invalid username or password.")
			return
		}
		setOAuthSessionCookie(w, base, login)
		http.Redirect(w, r, "/oauth/authorize?request_id="+url.QueryEscape(requestID), http.StatusSeeOther)
	case "register":
		credentials := core.Credentials{Username: r.FormValue("username"), Password: r.FormValue("password")}
		if _, registerErr := store.Register(r.Context(), credentials); registerErr != nil {
			renderOAuthRequest(w, r, store, base, requestID, oauthRegistrationMessage(registerErr))
			return
		}
		login, loginErr := store.Login(r.Context(), credentials)
		if loginErr != nil {
			fail(w, loginErr)
			return
		}
		setOAuthSessionCookie(w, base, login)
		http.Redirect(w, r, "/oauth/authorize?request_id="+url.QueryEscape(requestID), http.StatusSeeOther)
	case "approve":
		session, _ := r.Cookie("edc_oauth_session")
		if session == nil {
			http.Redirect(w, r, "/oauth/authorize?request_id="+url.QueryEscape(requestID), http.StatusSeeOther)
			return
		}
		userID, _, authErr := store.Authenticate(r.Context(), session.Value)
		if authErr != nil {
			http.SetCookie(w, &http.Cookie{Name: "edc_oauth_session", Value: "", Path: "/oauth", MaxAge: -1})
			http.Redirect(w, r, "/oauth/authorize?request_id="+url.QueryEscape(requestID), http.StatusSeeOther)
			return
		}
		code := randomSecret("edc_code_")
		approved, approveErr := store.ApproveOAuthRequest(r.Context(), requestID, csrf.Value, userID, code, time.Now().Add(oauthCodeTTL))
		if approveErr != nil {
			oauthError(w, http.StatusBadRequest, "invalid_request", "authorization request expired")
			return
		}
		oauthRedirect(w, r, approved.RedirectURI, approved.State, base, code, "")
	default:
		oauthError(w, http.StatusBadRequest, "invalid_request", "missing authorization decision")
	}
}

func setOAuthSessionCookie(w http.ResponseWriter, base string, login core.LoginResult) {
	http.SetCookie(w, &http.Cookie{Name: "edc_oauth_session", Value: login.Token, Path: "/oauth", Secure: strings.HasPrefix(base, "https://"), HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: login.ExpiresAt})
}

func oauthRegistrationMessage(err error) string {
	var appErr *core.Error
	if errors.As(err, &appErr) {
		switch appErr.Code {
		case "invalid_input":
			return appErr.Message
		case "conflict":
			return "Username already exists. Sign in or choose another username."
		}
	}
	return "Could not create account. Please try again."
}

func renderOAuthRequest(w http.ResponseWriter, r *http.Request, store *core.Store, base, requestID, message string) {
	csrf, _ := r.Cookie("edc_oauth_csrf")
	if csrf == nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "authorization request expired")
		return
	}
	request, err := store.OAuthRequest(r.Context(), requestID, csrf.Value)
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "authorization request expired")
		return
	}
	data := oauthPageData{RequestID: requestID, ClientName: request.ClientName, Resource: request.Resource, Scopes: strings.Fields(request.Scope), Login: true, Error: message}
	if session, cookieErr := r.Cookie("edc_oauth_session"); cookieErr == nil {
		if userID, _, authErr := store.Authenticate(r.Context(), session.Value); authErr == nil {
			if user, meErr := store.Me(core.WithUser(r.Context(), userID)); meErr == nil {
				data.Login, data.Username = false, user.Username
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if message != "" {
		w.WriteHeader(http.StatusUnauthorized)
	}
	_ = oauthPage.Execute(w, data)
}

func oauthToken(w http.ResponseWriter, r *http.Request, store *core.Store, base string, ttl time.Duration) {
	if r.Header.Get("Authorization") != "" {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "public clients must not send credentials")
		return
	}
	if err := r.ParseForm(); err != nil || r.FormValue("grant_type") != "authorization_code" || !pkcePattern.MatchString(r.FormValue("code_verifier")) || r.FormValue("resource") != base+"/mcp" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "invalid authorization code exchange")
		return
	}
	token, info, err := store.ExchangeOAuthCode(r.Context(), r.FormValue("code"), r.FormValue("client_id"), r.FormValue("redirect_uri"), r.FormValue("code_verifier"), r.FormValue("resource"), ttl)
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "authorization code is invalid, expired, or already used")
		return
	}
	respond(w, http.StatusOK, map[string]any{"access_token": token, "token_type": "Bearer", "expires_in": int64(time.Until(info.ExpiresAt).Seconds()), "scope": strings.Join(info.Scopes, " ")})
}

func oauthRedirect(w http.ResponseWriter, r *http.Request, redirectURI, state, issuer, code, oauthErr string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "invalid redirect URI")
		return
	}
	q := u.Query()
	if code != "" {
		q.Set("code", code)
	} else {
		q.Set("error", oauthErr)
	}
	if state != "" {
		q.Set("state", state)
	}
	q.Set("iss", issuer)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func oauthError(w http.ResponseWriter, status int, code, description string) {
	respond(w, status, map[string]string{"error": code, "error_description": description})
}

func randomSecret(prefix string) string {
	return prefix + strings.ToLower(rand.Text()) + strings.ToLower(rand.Text())
}
