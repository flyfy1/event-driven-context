package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"event-driven-context/internal/core"
	"event-driven-context/internal/hubconnectors"
)

// Hub agent tokens are deliberately not accepted by user, project or approval routes.
func registerHubHandlers(mux *http.ServeMux, store *core.Store, config Config) {
	connector := hubconnectors.NewClient(nil)
	approvalURL := "/hub.html"
	if base, ok := agentSetupBase(config.IntegAuth.WebBaseURL); ok {
		approvalURL = base + "/hub.html"
	} else if len(config.AllowedOrigins) > 0 {
		if base, ok := agentSetupBase(config.AllowedOrigins[0]); ok {
			approvalURL = base + "/hub.html"
		}
	}
	registerHubPushHandlers(mux, store)
	registerHubGoogleHandlers(mux, store, approvalURL)
	key, _ := base64.StdEncoding.DecodeString(os.Getenv("EDC_HUB_CREDENTIAL_KEY"))
	mux.HandleFunc("GET /v1/hub/capabilities", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]any{"providers": hubconnectors.Catalog(), "authorization": map[string]any{"agent_registration": "POST /v1/hub/agents", "request_access": "POST /v1/hub/requests", "owner_approval_page": approvalURL, "owner_session_required": true, "scope": "one connection and one operation", "max_duration_seconds": 604800}, "credential_storage_configured": len(key) == 32})
	})
	mux.Handle("POST /v1/hub/agents", newAuthGate().wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Owner string `json:"owner"`
			Name  string `json:"name"`
		}
		if err := decode(r, &in); err != nil {
			failV2(w, err)
			return
		}
		out, err := store.RegisterHubAgent(r.Context(), in.Owner, in.Name)
		if err != nil {
			hubFail(w, err)
			return
		}
		notifyHubOwner(r.Context(), store, out.OwnerID)
		respond(w, 201, struct {
			core.HubAgentRegistration
			ApprovalURL string `json:"approval_url"`
		}{out, approvalURL})
	})))
	agent := func(require bool, next func(http.ResponseWriter, *http.Request, core.HubAgent)) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a, err := store.HubAgent(r.Context(), bearer(r), require)
			if err != nil {
				hubFail(w, err)
				return
			}
			next(w, r, a)
		})
	}
	mux.Handle("GET /v1/hub/agent", agent(false, func(w http.ResponseWriter, r *http.Request, a core.HubAgent) { respond(w, 200, a) }))
	mux.Handle("GET /v1/hub/sources", agent(true, func(w http.ResponseWriter, r *http.Request, a core.HubAgent) {
		out, err := store.HubConnections(r.Context(), a.OwnerID)
		if err != nil {
			hubFail(w, err)
			return
		}
		respond(w, 200, map[string]any{"connections": out, "notice": "Account discovery is approved; content requires a separate operation grant."})
	}))
	mux.Handle("GET /v1/hub/requests", agent(true, func(w http.ResponseWriter, r *http.Request, a core.HubAgent) {
		out, err := store.HubRequests(r.Context(), a.OwnerID, a.ID)
		if err != nil {
			hubFail(w, err)
			return
		}
		respond(w, 200, map[string]any{"requests": out})
	}))
	mux.Handle("POST /v1/hub/requests", agent(true, func(w http.ResponseWriter, r *http.Request, a core.HubAgent) {
		var in struct {
			ConnectionID    string `json:"connection_id"`
			Operation       string `json:"operation"`
			Reason          string `json:"reason"`
			DurationSeconds int64  `json:"duration_seconds"`
		}
		if err := decode(r, &in); err != nil {
			hubFail(w, err)
			return
		}
		c, err := store.HubConnection(r.Context(), a.OwnerID, in.ConnectionID)
		if err != nil {
			hubFail(w, err)
			return
		}
		if !hubOperation(c.ProviderID, in.Operation) {
			hubFail(w, core.Invalid("operation is not supported by this provider"))
			return
		}
		out, err := store.RequestHubAccess(r.Context(), a, in.ConnectionID, in.Operation, in.Reason, in.DurationSeconds)
		if err != nil {
			hubFail(w, err)
			return
		}
		notifyHubOwner(r.Context(), store, a.OwnerID)
		respond(w, 201, map[string]any{"approval_url": approvalURL, "request": out, "next_action": "Ask the owner to review this request in My authorizations.", "approval_path": "/hub.html"})
	}))
	mux.Handle("POST /v1/hub/execute", agent(true, func(w http.ResponseWriter, r *http.Request, a core.HubAgent) {
		var in struct {
			ConnectionID string         `json:"connection_id"`
			Operation    string         `json:"operation"`
			Args         map[string]any `json:"args"`
		}
		if err := decode(r, &in); err != nil {
			hubFail(w, err)
			return
		}
		if err := store.CheckHubAccess(r.Context(), a, in.ConnectionID, in.Operation); err != nil {
			hubFail(w, err)
			return
		}
		c, err := store.HubConnection(r.Context(), a.OwnerID, in.ConnectionID)
		if err != nil {
			hubFail(w, err)
			return
		}
		secret, err := hubProviderCredential(r.Context(), store, c, key)
		if err != nil {
			hubFail(w, err)
			return
		}
		result, err := connector.Execute(r.Context(), c.ProviderID, in.Operation, secret, in.Args)
		if err != nil {
			// Provider errors can contain URLs or credentials; only return controlled adapter codes.
			respond(w, http.StatusBadGateway, map[string]any{"error": map[string]string{"code": "provider_failed", "message": "Provider request failed. The owner may need to reconnect this account."}})
			return
		}
		// Recheck grants before releasing a response that raced with revocation.
		if err := store.CheckHubAccess(r.Context(), a, in.ConnectionID, in.Operation); err != nil {
			hubFail(w, err)
			return
		}
		if strings.Contains(result.ContentType, "json") && json.Valid(result.Body) {
			respond(w, 200, map[string]any{"connection_id": c.ID, "operation": in.Operation, "data": json.RawMessage(result.Body), "content_type": result.ContentType})
		} else {
			respond(w, 200, map[string]any{"connection_id": c.ID, "operation": in.Operation, "data_base64": base64.StdEncoding.EncodeToString(result.Body), "content_type": result.ContentType})
		}
	}))
	owner := func(next http.HandlerFunc) http.Handler { return authenticated(store, next) }
	mux.Handle("GET /v1/hub/owner", owner(func(w http.ResponseWriter, r *http.Request) {
		out, err := store.HubOverview(r.Context())
		v2RespondResult(w, 200, out, err)
	}))
	for _, kind := range []string{"agents", "requests"} {
		mux.Handle("POST /v1/hub/"+kind+"/{id}/decision", owner(func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Decision         string `json:"decision"`
				VerificationCode string `json:"verification_code,omitempty"`
			}
			if err := decode(r, &in); err != nil {
				hubFail(w, err)
				return
			}
			var err error
			if kind == "agents" {
				err = store.DecideHubAgent(r.Context(), r.PathValue("id"), in.Decision, in.VerificationCode)
			} else {
				err = store.DecideHubRequest(r.Context(), r.PathValue("id"), in.Decision)
			}
			v2RespondResult(w, 200, map[string]bool{"ok": err == nil}, err)
		}))
	}
	mux.Handle("POST /v1/hub/connections", owner(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ProviderID  string `json:"provider_id"`
			AccountID   string `json:"account_id"`
			DisplayName string `json:"display_name"`
			Credential  string `json:"credential"`
		}
		if err := decode(r, &in); err != nil {
			hubFail(w, err)
			return
		}
		p, ok := hubconnectors.Lookup(in.ProviderID)
		if !ok {
			hubFail(w, core.Invalid("unknown provider"))
			return
		}
		if in.Credential != "" && len(p.Operations) == 0 {
			hubFail(w, core.Invalid("this provider does not yet support API execution"))
			return
		}
		out, err := store.AddHubConnection(r.Context(), core.HubConnection{ProviderID: in.ProviderID, AccountID: in.AccountID, DisplayName: in.DisplayName}, in.Credential, key)
		v2RespondResult(w, 201, out, err)
	}))
	mux.Handle("POST /v1/hub/connections/{id}/disconnect", owner(func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if err := decode(r, &in); err != nil {
			hubFail(w, err)
			return
		}
		err := store.DisconnectHubConnection(r.Context(), r.PathValue("id"))
		v2RespondResult(w, 200, map[string]bool{"ok": err == nil}, err)
	}))
}
func hubOperation(provider, operation string) bool {
	p, ok := hubconnectors.Lookup(provider)
	if !ok {
		return false
	}
	for _, o := range p.Operations {
		if o.ID == operation {
			return true
		}
	}
	return false
}
func hubFail(w http.ResponseWriter, err error) {
	if e, ok := err.(*core.Error); ok && (e.Code == "authorization_required" || e.Code == "connection_required") {
		respond(w, http.StatusForbidden, map[string]any{"error": e, "next_action": "Ask the owner to review My authorizations. An agent cannot approve its own access.", "approval_path": "/hub.html"})
		return
	}
	failV2(w, err)
}
