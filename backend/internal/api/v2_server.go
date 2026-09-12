package api

import (
	"crypto/rand"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"event-driven-context/internal/core"
	"event-driven-context/internal/mcpserver"
	"event-driven-context/internal/v2"
)

func V2Handler(store *core.Store, service v2.ServiceAPI, allowedOrigins []string) http.Handler {
	return V2HandlerWithConfig(store, service, Config{AllowedOrigins: allowedOrigins})
}

// V2HandlerWithConfig is the production V2 application surface. The legacy
// HandlerWithConfig remains available for migration tests and old fixtures,
// but the server command uses this handler.
func V2HandlerWithConfig(store *core.Store, service v2.ServiceAPI, config Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		respond(w, http.StatusOK, map[string]string{"status": "ok", "api": "v2"})
	})
	gate := newAuthGate()
	mux.Handle("POST /v1/auth/register", gate.wrap(jsonEndpoint(http.StatusCreated, store.Register)))
	mux.Handle("POST /v1/auth/login", gate.wrap(jsonEndpoint(http.StatusOK, store.Login)))
	mux.Handle("POST /v1/auth/logout", authenticated(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := store.Logout(r.Context(), bearer(r)); err != nil {
			failV2(w, err)
			return
		}
		respond(w, http.StatusOK, core.Empty{})
	})))
	mux.Handle("GET /v1/me", v2UserAuthenticated(store, config, core.ScopeRead, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, err := store.Me(r.Context())
		v2RespondResult(w, http.StatusOK, out, err)
	})))
	RegisterV2Handlers(mux, store, service, config)
	if config.PublicBaseURL != "" {
		registerOAuthHandlers(mux, store, config)
	}
	mux.Handle("/mcp", mcpserver.V2HTTP(store, service, config.PublicBaseURL))
	return v2HTTPMiddleware(mux, config)
}

func v2HTTPMiddleware(next http.Handler, config Config) http.Handler {
	allowed := map[string]bool{}
	for _, origin := range config.AllowedOrigins {
		allowed[origin] = true
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
			if !originAllowed && !isOAuthAuthorizationFormPost(r) {
				respond(logged, http.StatusForbidden, map[string]any{"error": v2.Error{Code: "forbidden", Message: "origin not allowed"}})
				return
			}
			if originAllowed {
				logged.Header().Set("Access-Control-Allow-Origin", origin)
				logged.Header().Set("Access-Control-Expose-Headers", "Content-Disposition, X-EDC-File-ID, X-EDC-SHA256, X-Request-ID")
				logged.Header().Set("Vary", "Origin")
				if r.Method == http.MethodOptions {
					logged.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
					logged.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
					logged.Header().Set("Access-Control-Max-Age", "600")
					logged.WriteHeader(http.StatusNoContent)
					return
				}
			}
		}
		maxBytes := int64(core.MaxRequestBytes)
		if v2IsFileUpload(r) {
			maxBytes = int64(v2.MaxFileBytes + v2MultipartOverhead)
		}
		r.Body = http.MaxBytesReader(logged, r.Body, maxBytes)
		next.ServeHTTP(logged, r)
	})
}

func v2IsFileUpload(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	return len(parts) == 4 && parts[0] == "v1" && parts[1] == "projects" && parts[2] != "" && parts[3] == "files"
}
