package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
	oauthRequestTTL      = 10 * time.Minute
	oauthCodeTTL         = 5 * time.Minute
	oauthLocaleCookieTTL = 365 * 24 * time.Hour
	oauthLocaleCookie    = "event_context_locale"
)

var pkcePattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

var oauthPage = template.Must(template.New("oauth").Parse(`<!doctype html>
<html lang="{{.Language}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Event-driven Context OAuth</title><style>
body{font:16px system-ui,sans-serif;background:#f5f5f2;color:#20221f;margin:0}.card{max-width:520px;margin:8vh auto;padding:32px;background:#fff;border:1px solid #d9ddd5;border-radius:16px;box-shadow:0 8px 30px #00000010}h1{font-size:24px;margin-top:0}.muted{color:#60665e}.notice{padding:12px;background:#f2f5ee;border-radius:8px}.error{color:#a21b1b}label{display:block;margin:16px 0 6px}input{box-sizing:border-box;width:100%;font:inherit;padding:10px;border:1px solid #aeb5aa;border-radius:8px}.actions{display:flex;flex-wrap:wrap;gap:12px;margin-top:24px}button,.button{display:inline-block;font:inherit;padding:10px 18px;border:0;border-radius:8px;background:#265c3b;color:#fff;text-decoration:none;cursor:pointer}.secondary{background:#6b7169}.languages{display:flex;flex-wrap:wrap;gap:10px;margin-bottom:24px;font-size:14px}.languages a{color:#265c3b}code{word-break:break-all}
</style></head><body><main class="card">{{if .Expired}}<h1>{{.Copy.ExpiredTitle}}</h1><p class="notice">{{.Copy.ExpiredMessage}}</p>{{else}}<nav class="languages" aria-label="{{.Copy.LanguageLabel}}"><span>{{.Copy.LanguageLabel}}:</span><a href="/oauth/authorize?request_id={{.RequestID}}&amp;lang=en" hreflang="en">English</a><a href="/oauth/authorize?request_id={{.RequestID}}&amp;lang=zh-CN" hreflang="zh-CN">简体中文</a><a href="/oauth/authorize?request_id={{.RequestID}}&amp;lang=ms" hreflang="ms">Bahasa Melayu</a><a href="/oauth/authorize?request_id={{.RequestID}}&amp;lang=hi" hreflang="hi">हिन्दी</a></nav><h1>Event-driven Context</h1><p class="muted">{{.ClientName}} {{.Copy.ClientRequest}}</p>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}{{if .Login}}<p class="notice">{{.Copy.SignInNotice}}</p><p><a class="button" href="{{.IntegLoginURL}}">{{.Copy.IntegLoginLabel}}</a></p><form method="post" action="/oauth/authorize"><input type="hidden" name="request_id" value="{{.RequestID}}"><input type="hidden" name="lang" value="{{.Language}}"><label for="username">{{.Copy.UsernameLabel}}</label><input id="username" name="username" autocomplete="username" required><label for="email">{{.Copy.EmailLabel}}</label><input id="email" name="email" type="email" autocomplete="email"><label for="password">{{.Copy.PasswordLabel}}</label><input id="password" name="password" type="password" autocomplete="current-password" required><p class="muted">{{.Copy.NewAccountHint}}</p><div class="actions"><button type="submit" name="decision" value="login">{{.Copy.SignInLabel}}</button><button class="secondary" type="submit" name="decision" value="register">{{.Copy.CreateAccountLabel}}</button><button class="secondary" type="submit" name="decision" value="deny" formnovalidate>{{.Copy.CancelLabel}}</button></div></form>{{else}}<p>{{.Copy.SignedInAs}} <strong>{{.Username}}</strong>.</p><p class="notice">{{.Copy.ApproveAccess}}</p><ul>{{range .Scopes}}<li><strong>{{.Name}}</strong> — {{.Description}}</li>{{end}}</ul><p class="muted">{{.Copy.AccessPrefix}} <code>{{.Resource}}</code> {{.Copy.AccessSuffix}}</p><form method="post" action="/oauth/authorize"><input type="hidden" name="request_id" value="{{.RequestID}}"><input type="hidden" name="lang" value="{{.Language}}"><div class="actions"><button type="submit" name="decision" value="approve">{{.Copy.AuthorizeLabel}}</button><button class="secondary" type="submit" name="decision" value="deny">{{.Copy.CancelLabel}}</button></div></form>{{end}}{{end}}</main></body></html>`))

type oauthCopy struct {
	LanguageLabel, ClientRequest, SignInNotice, IntegLoginLabel, UsernameLabel, EmailLabel       string
	PasswordLabel                                                                                string
	NewAccountHint, SignInLabel, CreateAccountLabel, CancelLabel, SignedInAs                     string
	ApproveAccess, ReadDescription, WriteDescription, AccessPrefix, AccessSuffix, AuthorizeLabel string
	ExpiredTitle, ExpiredMessage                                                                 string
	Errors                                                                                       map[string]string
}

type oauthScopeDisplay struct {
	Name, Description string
}

var oauthCopies = map[string]oauthCopy{
	"en": {
		LanguageLabel: "Language", ClientRequest: "is requesting access through OAuth.",
		SignInNotice:    "Sign in with your existing Event-driven Context account. Your password is sent only to this service and is never shared with the client.",
		IntegLoginLabel: "Continue with Integ.Life",
		UsernameLabel:   "Username", EmailLabel: "Email (required to create an account)", PasswordLabel: "Password", NewAccountHint: "New here? A username uses 3–64 lowercase letters, digits, _, . or -, and a password uses 12–72 bytes.",
		SignInLabel: "Sign in", CreateAccountLabel: "Create account", CancelLabel: "Cancel", SignedInAs: "Signed in as", ApproveAccess: "Approve access to:",
		ReadDescription: "read projects, members, events, metadata, and files", WriteDescription: "create projects, add members, and append immutable events",
		AccessPrefix: "Access is limited to", AccessSuffix: "and expires after one hour. Project membership rules still apply.", AuthorizeLabel: "Authorize",
		ExpiredTitle: "Authorization request expired", ExpiredMessage: "Return to the app that opened this page and start the connection again. Refreshing this page will not work.",
		Errors: map[string]string{"invalid_credentials": "Invalid username or password.", "invalid_username": "Username must be 3–64 lowercase letters, digits, _, . or -.", "invalid_email": "Enter a valid email address to create an account.", "invalid_password": "Password must be 12–72 bytes.", "username_exists": "Username or email already exists. Sign in or choose another one.", "registration_failed": "Could not create account. Please try again."},
	},
	"zh-CN": {
		LanguageLabel: "语言", ClientRequest: "正在通过 OAuth 请求访问。",
		SignInNotice:    "请使用现有的 Event-driven Context 账号登录。密码只会发送到本服务，不会与客户端共享。",
		IntegLoginLabel: "使用 Integ.Life 继续",
		UsernameLabel:   "用户名", EmailLabel: "邮箱（创建账号时必填）", PasswordLabel: "密码", NewAccountHint: "还没有账号？用户名须为 3–64 位小写字母、数字、_、. 或 -，密码须为 12–72 字节。",
		SignInLabel: "登录", CreateAccountLabel: "创建账号", CancelLabel: "取消", SignedInAs: "当前登录用户", ApproveAccess: "授权访问以下范围：",
		ReadDescription: "读取项目、成员、事件、元数据和文件", WriteDescription: "创建项目、添加成员并追加不可变事件",
		AccessPrefix: "访问权限仅限于", AccessSuffix: "，并在一小时后过期。项目成员权限规则仍然适用。", AuthorizeLabel: "授权",
		ExpiredTitle: "授权请求已过期", ExpiredMessage: "请返回打开此页面的应用，重新发起连接。刷新此页面无法继续。",
		Errors: map[string]string{"invalid_credentials": "用户名或密码错误。", "invalid_username": "用户名须为 3–64 位小写字母、数字、_、. 或 -。", "invalid_email": "请输入有效邮箱以创建账号。", "invalid_password": "密码须为 12–72 字节。", "username_exists": "用户名或邮箱已存在，请登录或更换。", "registration_failed": "无法创建账号，请重试。"},
	},
	"ms": {
		LanguageLabel: "Bahasa", ClientRequest: "meminta akses melalui OAuth.",
		SignInNotice:    "Log masuk dengan akaun Event-driven Context sedia ada. Kata laluan hanya dihantar kepada perkhidmatan ini dan tidak dikongsi dengan klien.",
		IntegLoginLabel: "Teruskan dengan Integ.Life",
		UsernameLabel:   "Nama pengguna", EmailLabel: "E-mel (diperlukan untuk mencipta akaun)", PasswordLabel: "Kata laluan", NewAccountHint: "Pengguna baharu? Nama pengguna menggunakan 3–64 huruf kecil, nombor, _, . atau -, dan kata laluan menggunakan 12–72 bait.",
		SignInLabel: "Log masuk", CreateAccountLabel: "Cipta akaun", CancelLabel: "Batal", SignedInAs: "Log masuk sebagai", ApproveAccess: "Benarkan akses kepada:",
		ReadDescription: "baca projek, ahli, peristiwa, metadata dan fail", WriteDescription: "cipta projek, tambah ahli dan lampirkan peristiwa kekal",
		AccessPrefix: "Akses terhad kepada", AccessSuffix: "dan tamat selepas satu jam. Peraturan keahlian projek masih terpakai.", AuthorizeLabel: "Benarkan",
		ExpiredTitle: "Permintaan kebenaran telah tamat", ExpiredMessage: "Kembali ke aplikasi yang membuka halaman ini dan mulakan sambungan semula. Memuat semula halaman ini tidak akan berjaya.",
		Errors: map[string]string{"invalid_credentials": "Nama pengguna atau kata laluan tidak sah.", "invalid_username": "Nama pengguna mesti terdiri daripada 3–64 huruf kecil, nombor, _, . atau -.", "invalid_email": "Masukkan alamat e-mel yang sah untuk mencipta akaun.", "invalid_password": "Kata laluan mesti terdiri daripada 12–72 bait.", "username_exists": "Nama pengguna atau e-mel sudah wujud. Log masuk atau pilih yang lain.", "registration_failed": "Akaun tidak dapat dicipta. Sila cuba lagi."},
	},
	"hi": {
		LanguageLabel: "भाषा", ClientRequest: "OAuth के माध्यम से पहुँच का अनुरोध कर रहा है।",
		SignInNotice:    "अपने मौजूदा Event-driven Context खाते से साइन इन करें। आपका पासवर्ड केवल इस सेवा को भेजा जाता है और क्लाइंट के साथ साझा नहीं किया जाता।",
		IntegLoginLabel: "Integ.Life के साथ जारी रखें",
		UsernameLabel:   "उपयोगकर्ता नाम", EmailLabel: "ईमेल (खाता बनाने के लिए आवश्यक)", PasswordLabel: "पासवर्ड", NewAccountHint: "नए उपयोगकर्ता? नाम में 3–64 छोटे अंग्रेज़ी अक्षर, अंक, _, . या - और पासवर्ड में 12–72 बाइट होने चाहिए।",
		SignInLabel: "साइन इन", CreateAccountLabel: "खाता बनाएँ", CancelLabel: "रद्द करें", SignedInAs: "साइन इन उपयोगकर्ता", ApproveAccess: "इन अनुमतियों को स्वीकृत करें:",
		ReadDescription: "प्रोजेक्ट, सदस्य, इवेंट, मेटाडेटा और फ़ाइलें पढ़ें", WriteDescription: "प्रोजेक्ट बनाएँ, सदस्य जोड़ें और अपरिवर्तनीय इवेंट जोड़ें",
		AccessPrefix: "पहुँच केवल", AccessSuffix: "तक सीमित है और एक घंटे बाद समाप्त हो जाती है। प्रोजेक्ट सदस्यता नियम लागू रहते हैं।", AuthorizeLabel: "अनुमति दें",
		ExpiredTitle: "अनुमति अनुरोध की समय सीमा समाप्त हो गई", ExpiredMessage: "उस ऐप पर वापस जाएँ जिसने यह पेज खोला था और कनेक्शन फिर से शुरू करें। इस पेज को रीफ़्रेश करने से अनुरोध जारी नहीं होगा।",
		Errors: map[string]string{"invalid_credentials": "उपयोगकर्ता नाम या पासवर्ड गलत है।", "invalid_username": "उपयोगकर्ता नाम में 3–64 छोटे अंग्रेज़ी अक्षर, अंक, _, . या - होने चाहिए।", "invalid_email": "खाता बनाने के लिए एक मान्य ईमेल पता दर्ज करें।", "invalid_password": "पासवर्ड 12–72 बाइट का होना चाहिए।", "username_exists": "यह उपयोगकर्ता नाम या ईमेल पहले से मौजूद है। साइन इन करें या दूसरा चुनें।", "registration_failed": "खाता नहीं बनाया जा सका। फिर से प्रयास करें।"},
	},
}

type oauthPageData struct {
	RequestID, ClientName, Username, Resource, Error, Language, IntegLoginURL string
	Copy                                                                      oauthCopy
	Scopes                                                                    []oauthScopeDisplay
	Login                                                                     bool
	Expired                                                                   bool
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
	http.SetCookie(w, &http.Cookie{Name: oauthCSRFCookieName(requestID), Value: csrf, Path: "/oauth/authorize", Secure: strings.HasPrefix(base, "https://"), HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(oauthRequestTTL.Seconds())})
	http.Redirect(w, r, oauthRequestLocation(requestID, oauthLanguage(r)), http.StatusSeeOther)
}

func oauthAuthorizePost(w http.ResponseWriter, r *http.Request, store *core.Store, base string) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "invalid form")
		return
	}
	requestID := r.FormValue("request_id")
	csrf, _ := r.Cookie(oauthCSRFCookieName(requestID))
	if csrf == nil {
		renderOAuthExpired(w, r, base)
		return
	}
	request, err := store.OAuthRequest(r.Context(), requestID, csrf.Value)
	if err != nil {
		renderOAuthExpired(w, r, base)
		return
	}
	switch r.FormValue("decision") {
	case "deny":
		clearOAuthCSRFCookie(w, base, requestID)
		oauthRedirect(w, r, request.RedirectURI, request.State, base, "", "access_denied")
	case "login":
		credentials := core.Credentials{Username: r.FormValue("username"), Password: r.FormValue("password")}
		login, loginErr := store.Login(r.Context(), credentials)
		if loginErr != nil {
			renderOAuthRequest(w, r, store, base, requestID, "invalid_credentials")
			return
		}
		setOAuthSessionCookie(w, base, login)
		http.Redirect(w, r, oauthRequestLocation(requestID, oauthLanguage(r)), http.StatusSeeOther)
	case "register":
		credentials := core.Credentials{Username: r.FormValue("username"), Email: r.FormValue("email"), Password: r.FormValue("password")}
		if _, registerErr := store.Register(r.Context(), credentials); registerErr != nil {
			renderOAuthRequest(w, r, store, base, requestID, oauthRegistrationErrorKey(registerErr))
			return
		}
		login, loginErr := store.Login(r.Context(), credentials)
		if loginErr != nil {
			fail(w, loginErr)
			return
		}
		setOAuthSessionCookie(w, base, login)
		http.Redirect(w, r, oauthRequestLocation(requestID, oauthLanguage(r)), http.StatusSeeOther)
	case "approve":
		userID, hasSession, authenticated := authenticateOAuthBrowserSession(r, store)
		if !hasSession {
			http.Redirect(w, r, oauthRequestLocation(requestID, oauthLanguage(r)), http.StatusSeeOther)
			return
		}
		if !authenticated {
			clearOAuthBrowserSessionCookies(w)
			http.Redirect(w, r, oauthRequestLocation(requestID, oauthLanguage(r)), http.StatusSeeOther)
			return
		}
		code := randomSecret("edc_code_")
		approved, approveErr := store.ApproveOAuthRequest(r.Context(), requestID, csrf.Value, userID, code, time.Now().Add(oauthCodeTTL))
		if approveErr != nil {
			renderOAuthExpired(w, r, base)
			return
		}
		clearOAuthCSRFCookie(w, base, requestID)
		oauthRedirect(w, r, approved.RedirectURI, approved.State, base, code, "")
	default:
		oauthError(w, http.StatusBadRequest, "invalid_request", "missing authorization decision")
	}
}

func setOAuthSessionCookie(w http.ResponseWriter, base string, login core.LoginResult) {
	http.SetCookie(w, &http.Cookie{Name: "edc_oauth_session", Value: login.Token, Path: "/oauth", Secure: strings.HasPrefix(base, "https://"), HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: login.ExpiresAt})
}

func oauthBrowserSessionTokens(r *http.Request) []string {
	var tokens []string
	if token := requestSessionToken(r); token != "" {
		tokens = append(tokens, token)
	}
	if cookie, err := r.Cookie("edc_oauth_session"); err == nil && cookie.Value != "" && (len(tokens) == 0 || tokens[0] != cookie.Value) {
		tokens = append(tokens, cookie.Value)
	}
	return tokens
}

func authenticateOAuthBrowserSession(r *http.Request, store *core.Store) (userID string, hasSession, authenticated bool) {
	tokens := oauthBrowserSessionTokens(r)
	for _, token := range tokens {
		userID, _, err := store.Authenticate(r.Context(), token)
		if err == nil {
			return userID, true, true
		}
	}
	return "", len(tokens) > 0, false
}

func clearOAuthBrowserSessionCookies(w http.ResponseWriter) {
	clearSessionCookies(w)
	http.SetCookie(w, &http.Cookie{Name: "edc_oauth_session", Value: "", Path: "/oauth", MaxAge: -1})
}

func oauthRegistrationErrorKey(err error) string {
	var appErr *core.Error
	if errors.As(err, &appErr) {
		switch appErr.Code {
		case "invalid_input":
			if strings.Contains(appErr.Message, "username") {
				return "invalid_username"
			}
			if strings.Contains(appErr.Message, "email") {
				return "invalid_email"
			}
			return "invalid_password"
		case "conflict":
			return "username_exists"
		}
	}
	return "registration_failed"
}

func renderOAuthRequest(w http.ResponseWriter, r *http.Request, store *core.Store, base, requestID, errorKey string) {
	csrf, _ := r.Cookie(oauthCSRFCookieName(requestID))
	if csrf == nil {
		renderOAuthExpired(w, r, base)
		return
	}
	request, err := store.OAuthRequest(r.Context(), requestID, csrf.Value)
	if err != nil {
		renderOAuthExpired(w, r, base)
		return
	}
	language := oauthLanguage(r)
	setOAuthLanguageCookie(w, base, language)
	copy := oauthCopies[language]
	scopes := make([]oauthScopeDisplay, 0, len(strings.Fields(request.Scope)))
	for _, scope := range strings.Fields(request.Scope) {
		description := copy.WriteDescription
		if scope == core.ScopeRead {
			description = copy.ReadDescription
		}
		scopes = append(scopes, oauthScopeDisplay{Name: scope, Description: description})
	}
	data := oauthPageData{
		RequestID: requestID, ClientName: request.ClientName, Resource: request.Resource, Scopes: scopes,
		Login: true, Language: language, Copy: copy, IntegLoginURL: integAuthOAuthStartLocation(requestID, language),
	}
	if errorKey != "" {
		data.Error = copy.Errors[errorKey]
	}
	if userID, hasSession, authenticated := authenticateOAuthBrowserSession(r, store); authenticated {
		if user, meErr := store.Me(core.WithUser(r.Context(), userID)); meErr == nil {
			data.Login, data.Username = false, user.Username
		}
	} else if hasSession {
		clearOAuthBrowserSessionCookies(w)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Language", language)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if errorKey != "" {
		w.WriteHeader(http.StatusUnauthorized)
	}
	_ = oauthPage.Execute(w, data)
}

func oauthCSRFCookieName(requestID string) string {
	digest := sha256.Sum256([]byte(requestID))
	return "edc_oauth_csrf_" + hex.EncodeToString(digest[:16])
}

func clearOAuthCSRFCookie(w http.ResponseWriter, base, requestID string) {
	http.SetCookie(w, &http.Cookie{Name: oauthCSRFCookieName(requestID), Value: "", Path: "/oauth/authorize", Secure: strings.HasPrefix(base, "https://"), HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

func renderOAuthExpired(w http.ResponseWriter, r *http.Request, base string) {
	language := oauthLanguage(r)
	setOAuthLanguageCookie(w, base, language)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Language", language)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusBadRequest)
	_ = oauthPage.Execute(w, oauthPageData{Language: language, Copy: oauthCopies[language], Expired: true})
}

func oauthLanguage(r *http.Request) string {
	requested := r.URL.Query().Get("lang")
	if r.Method == http.MethodPost {
		requested = r.FormValue("lang")
	}
	if language := normalizeOAuthLanguage(requested); language != "" {
		return language
	}
	if cookie, err := r.Cookie(oauthLocaleCookie); err == nil {
		if language := normalizeOAuthLanguage(cookie.Value); language != "" {
			return language
		}
	}
	for _, preference := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		if language := normalizeOAuthLanguage(strings.TrimSpace(strings.SplitN(preference, ";", 2)[0])); language != "" {
			return language
		}
	}
	return "en"
}

func setOAuthLanguageCookie(w http.ResponseWriter, base, language string) {
	domain := ""
	if parsed, err := url.Parse(base); err == nil {
		host := parsed.Hostname()
		if host == "integ.life" || strings.HasSuffix(host, ".integ.life") {
			domain = ".integ.life"
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: oauthLocaleCookie, Value: language, Path: "/", Domain: domain,
		Secure: strings.HasPrefix(base, "https://"), SameSite: http.SameSiteLaxMode,
		MaxAge: int(oauthLocaleCookieTTL.Seconds()),
	})
}

func normalizeOAuthLanguage(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch {
	case raw == "zh" || strings.HasPrefix(raw, "zh-"):
		return "zh-CN"
	case raw == "ms" || strings.HasPrefix(raw, "ms-"):
		return "ms"
	case raw == "hi" || strings.HasPrefix(raw, "hi-"):
		return "hi"
	case raw == "en" || strings.HasPrefix(raw, "en-"):
		return "en"
	default:
		return ""
	}
}

func oauthRequestLocation(requestID, language string) string {
	return "/oauth/authorize?request_id=" + url.QueryEscape(requestID) + "&lang=" + url.QueryEscape(language)
}

func integAuthOAuthStartLocation(requestID, language string) string {
	return "/v1/auth/integ/start?" + url.Values{
		"ui_locales": {language},
		"return_to":  {oauthRequestLocation(requestID, language)},
	}.Encode()
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
