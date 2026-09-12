package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"event-driven-context/internal/core"
	"event-driven-context/internal/mcpserver"
)

func Handler(store *core.Store, allowedOrigins []string) http.Handler {
	return HandlerWithConfig(store, Config{AllowedOrigins: allowedOrigins})
}

type Config struct {
	AllowedOrigins      []string
	PublicBaseURL       string
	OAuthAccessTokenTTL time.Duration
}

func HandlerWithConfig(store *core.Store, config Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]string{"status": "ok"}) })
	gate := newAuthGate()
	mux.Handle("POST /v1/auth/register", gate.wrap(jsonEndpoint(201, store.Register)))
	mux.Handle("POST /v1/auth/login", gate.wrap(jsonEndpoint(200, store.Login)))
	mux.Handle("POST /v1/auth/logout", authenticated(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := store.Logout(r.Context(), bearer(r)); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, core.Empty{})
	})))
	mux.Handle("GET /v1/me", authenticated(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, err := store.Me(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, out)
	})))
	mux.Handle("POST /v1/projects", authenticated(store, jsonEndpoint(201, store.CreateProject)))
	mux.Handle("GET /v1/projects", authenticated(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, err := store.ListProjects(r.Context(), core.Empty{})
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, out)
	})))
	mux.Handle("POST /v1/members", authenticated(store, jsonEndpoint(200, store.AddMember)))
	mux.Handle("POST /v1/members/query", authenticated(store, jsonEndpoint(200, store.ListMembers)))
	mux.Handle("POST /v1/events", authenticated(store, jsonEndpoint(200, store.RecordEvent)))
	mux.Handle("POST /v1/events/query", authenticated(store, jsonEndpoint(200, store.QueryEvents)))
	mux.Handle("POST /v1/metadata/query", authenticated(store, jsonEndpoint(200, store.ListMetadata)))
	mux.Handle("GET /v1/events/{id}", authenticated(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, err := store.GetEvent(r.Context(), core.EventRef{EventID: r.PathValue("id")})
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, out)
	})))
	mux.Handle("GET /v1/files/{id}", authenticated(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, err := store.GetFile(r.Context(), core.FileRef{FileID: r.PathValue("id")})
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, out)
	})))
	if config.PublicBaseURL != "" {
		registerOAuthHandlers(mux, store, config)
	}
	mux.Handle("/mcp", mcpserver.HTTP(store, config.PublicBaseURL))
	allowed := map[string]bool{}
	for _, o := range config.AllowedOrigins {
		allowed[o] = true
	}
	if config.PublicBaseURL != "" {
		allowed[config.PublicBaseURL] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if !validRequestID(requestID) {
			requestID = "req_" + strings.ToLower(rand.Text())
		}
		w.Header().Set("X-Request-ID", requestID)
		logged := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		started := time.Now()
		defer func() {
			slog.Info("HTTP request", "request_id", requestID, "method", r.Method, "path", r.URL.Path, "status", logged.status, "duration_ms", time.Since(started).Milliseconds())
		}()
		logged.Header().Set("X-Content-Type-Options", "nosniff")
		logged.Header().Set("Cache-Control", "no-store")
		if origin := r.Header.Get("Origin"); origin != "" {
			originAllowed := allowed[origin]
			// OAuth authorization is a browser navigation flow, not a CORS API.
			// OpenAI-hosted authorization windows can submit the server-rendered
			// form with a client or opaque Origin. The handler still requires the
			// short-lived request-bound CSRF cookie before accepting any decision.
			if !originAllowed && !isOAuthAuthorizationFormPost(r) {
				respond(logged, 403, map[string]any{"error": core.Error{Code: "forbidden", Message: "origin not allowed"}})
				return
			}
			if originAllowed {
				logged.Header().Set("Access-Control-Allow-Origin", origin)
				logged.Header().Set("Vary", "Origin")
				if r.Method == http.MethodOptions {
					logged.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
					logged.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
					logged.Header().Set("Access-Control-Max-Age", "600")
					logged.WriteHeader(http.StatusNoContent)
					return
				}
			}
		}
		r.Body = http.MaxBytesReader(logged, r.Body, core.MaxRequestBytes)
		mux.ServeHTTP(logged, r)
	})
}

func isOAuthAuthorizationFormPost(r *http.Request) bool {
	if r.Method != http.MethodPost || r.URL.Path != "/oauth/authorize" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/x-www-form-urlencoded"
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func validRequestID(id string) bool {
	if len(id) < 8 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("_.-", r)) {
			return false
		}
	}
	return true
}
func bearer(r *http.Request) string {
	f := strings.Fields(r.Header.Get("Authorization"))
	if len(f) == 2 && strings.EqualFold(f[0], "Bearer") {
		return f[1]
	}
	return ""
}
func authenticated(s *core.Store, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _, err := s.Authenticate(r.Context(), bearer(r))
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			fail(w, err)
			return
		}
		h.ServeHTTP(w, r.WithContext(core.WithUser(r.Context(), id)))
	})
}
func jsonEndpoint[I, O any](status int, fn func(context.Context, I) (O, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in I
		if err := decode(r, &in); err != nil {
			fail(w, err)
			return
		}
		out, err := fn(r.Context(), in)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, status, out)
	})
}
func decode(r *http.Request, out any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return core.Invalid("Content-Type must be application/json")
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			return &core.Error{Code: "too_large", Message: "request max 2 MiB"}
		}
		return core.Invalid("could not read request")
	}
	trimmed := strings.TrimSpace(string(b))
	if !utf8.Valid(b) || len(trimmed) == 0 || trimmed[0] != '{' {
		return core.Invalid("body must be a UTF-8 JSON object")
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err = d.Decode(out); err != nil {
		return core.Invalid("invalid JSON: %s", err)
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return core.Invalid("expected exactly one JSON object")
	}
	return nil
}
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("response write failed", "error", err)
	}
}
func fail(w http.ResponseWriter, err error) {
	var e *core.Error
	status := 500
	if errors.As(err, &e) {
		switch e.Code {
		case "invalid_input":
			status = 400
		case "unauthenticated":
			status = 401
		case "forbidden":
			status = 403
		case "not_found":
			status = 404
		case "conflict":
			status = 409
		case "too_large":
			status = 413
		case "rate_limited":
			status = 429
		}
	} else {
		slog.Error("API operation failed", "error", err)
		e = &core.Error{Code: "internal", Message: "internal server error"}
	}
	respond(w, status, map[string]any{"error": e})
}

// Global bounds on password hashing work; a public deployment should also apply
// per-client limits at its ingress. Never trust a caller's X-Forwarded-For here.
type authGate struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
	slots  chan struct{}
}

func newAuthGate() *authGate {
	return &authGate{tokens: 20, last: time.Now(), slots: make(chan struct{}, 4)}
}
func (g *authGate) wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		t := time.Now()
		g.tokens = min(20, g.tokens+t.Sub(g.last).Seconds()/2)
		g.last = t
		ok := g.tokens >= 1
		if ok {
			g.tokens--
		}
		g.mu.Unlock()
		if !ok {
			w.Header().Set("Retry-After", "2")
			respond(w, 429, map[string]any{"error": core.Error{Code: "rate_limited", Message: "too many authentication requests"}})
			return
		}
		select {
		case g.slots <- struct{}{}:
			defer func() { <-g.slots }()
			h.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "2")
			respond(w, 429, map[string]any{"error": core.Error{Code: "rate_limited", Message: "authentication busy"}})
		}
	})
}
