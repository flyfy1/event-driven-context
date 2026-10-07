package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"event-driven-context/internal/core"
	"event-driven-context/internal/hubconnectors"
)

type hubIntegrationOperation struct {
	hubconnectors.Operation
	Authorized *bool                 `json:"authorized,omitempty"`
	Grants     []hubIntegrationGrant `json:"grants,omitempty"`
}
type hubIntegrationGrant struct {
	ID          string               `json:"id"`
	ExpiresAt   time.Time            `json:"expires_at"`
	Constraints *core.HubConstraints `json:"constraints,omitempty"`
}
type hubIntegrationConnection struct {
	core.HubConnection
	Operations []hubIntegrationOperation `json:"operations"`
}
type hubIntegration struct {
	Provider              hubconnectors.Provider     `json:"provider"`
	DeploymentConfigured  bool                       `json:"deployment_configured"`
	Connectable           bool                       `json:"connectable"`
	ConnectedAccountCount int                        `json:"connected_account_count"`
	Visible               bool                       `json:"visible"`
	HiddenReason          string                     `json:"hidden_reason"`
	OnboardingMethod      string                     `json:"onboarding_method"`
	Connections           []hubIntegrationConnection `json:"connections"`
}
type hubIntegrationRegistry struct {
	Providers    []hubIntegration `json:"providers"`
	FeatureFlags map[string]bool  `json:"feature_flags"`
	Notice       string           `json:"notice"`
}

func hubRegistryGoogleReady(g hubconnectors.GoogleOAuth, key []byte, provider string) bool {
	if len(key) != 32 {
		return false
	}
	// This validates local settings and adapter support only; it performs no OAuth request.
	_, err := g.AuthorizationURL(strings.Repeat("s", 32), strings.Repeat("v", 43), []string{provider})
	return err == nil
}

// registerHubRegistryHandlers provides authenticated, deployment-aware discovery.
// Public capabilities remain a credential-free catalog rather than account discovery.
func registerHubRegistryHandlers(mux *http.ServeMux, store *core.Store) {
	key, err := base64.StdEncoding.DecodeString(os.Getenv("EDC_HUB_CREDENTIAL_KEY"))
	if err != nil {
		key = nil
	}
	google := hubGoogleConfig()
	configureHubPush()
	build := func(ctx context.Context, owner string, agent *core.HubAgent) (hubIntegrationRegistry, error) {
		accounts, err := store.HubConnections(ctx, owner)
		if err != nil {
			return hubIntegrationRegistry{}, err
		}
		var grants []core.HubRequest
		if agent != nil {
			grants, err = store.HubRequests(ctx, owner, agent.ID)
			if err != nil {
				return hubIntegrationRegistry{}, err
			}
		}
		result := hubIntegrationRegistry{Providers: []hubIntegration{}, FeatureFlags: map[string]bool{"hub_approvals": true, "google_oauth": hubRegistryGoogleReady(google, key, "google-drive"), "push": hubPushSender != nil && len(key) == 32}, Notice: "Configured accounts have locally readable saved credentials; provider availability and credential validity have not been checked live."}
		for _, provider := range hubconnectors.Catalog() {
			implemented := provider.ImplementationStatus == "adapter_available" && len(provider.Operations) > 0
			if !implemented {
				provider.Operations = []hubconnectors.Operation{}
			}
			item := hubIntegration{Provider: provider, DeploymentConfigured: implemented && len(key) == 32, OnboardingMethod: "unavailable", Connections: []hubIntegrationConnection{}}
			if item.DeploymentConfigured {
				item.Connectable = true
				item.OnboardingMethod = "owner_cli"
				if provider.AuthMode == "oauth2" && len(hubconnectors.GoogleScopes(provider.ID)) > 0 && hubRegistryGoogleReady(google, key, provider.ID) {
					item.OnboardingMethod = "browser_oauth"
				}
			}
			credentialUnavailable := false
			for _, account := range accounts {
				if account.ProviderID != provider.ID || account.Status != "configured" {
					continue
				}

				// Disabled deployments do not advertise account operations as executable.
				if !item.DeploymentConfigured {
					continue
				}
				// Local decryption is a deployment readiness check, not a live provider probe.
				// Discard the plaintext immediately; neither metadata nor logs expose it.
				secret, err := store.HubCredential(ctx, account, key)
				if err != nil {
					if e, ok := err.(*core.Error); ok && e.Code == "service_unavailable" || errors.Is(err, core.ErrNotFound) {
						credentialUnavailable = true
						continue
					}
					return hubIntegrationRegistry{}, err
				}
				if provider.ID == "whatsapp-business" {
					if _, err := hubWhatsAppVerifier(account, secret); err != nil {
						credentialUnavailable = true
						continue
					}
				}
				item.ConnectedAccountCount++
				connection := hubIntegrationConnection{HubConnection: account, Operations: []hubIntegrationOperation{}}
				for _, operation := range provider.Operations {
					op := hubIntegrationOperation{Operation: operation}
					if agent != nil {
						err := store.CheckHubAccess(ctx, *agent, account.ID, operation.ID)
						authorized := err == nil
						if err != nil {
							if e, ok := err.(*core.Error); !ok || e.Code != "authorization_required" {
								return hubIntegrationRegistry{}, err
							}
						}
						op.Authorized = &authorized
						if authorized {
							for _, grant := range grants {
								if grant.ConnectionID == account.ID && grant.Operation == operation.ID && grant.Status == "approved" && grant.ExpiresAt.After(time.Now()) {
									op.Grants = append(op.Grants, hubIntegrationGrant{ID: grant.ID, ExpiresAt: grant.ExpiresAt, Constraints: grant.Constraints})
								}
							}
						}
					}
					connection.Operations = append(connection.Operations, op)
				}
				item.Connections = append(item.Connections, connection)
			}
			item.Visible = item.DeploymentConfigured && item.ConnectedAccountCount > 0
			switch {
			case !implemented:
				item.HiddenReason = "adapter_unavailable"
			case len(key) != 32:
				item.HiddenReason = "credential_storage_unconfigured"
			case item.ConnectedAccountCount == 0 && credentialUnavailable:
				item.HiddenReason = "credential_unavailable"
			case item.ConnectedAccountCount == 0:
				item.HiddenReason = "no_configured_accounts"
			}
			result.Providers = append(result.Providers, item)
		}
		return result, nil
	}
	mux.Handle("GET /v1/hub/integrations", authenticated(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		result, err := build(r.Context(), core.UserID(r.Context()), nil)
		v2RespondResult(w, 200, result, err)
	})))
	mux.HandleFunc("GET /v1/hub/integrations-agent", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		agent, err := store.HubAgent(r.Context(), bearer(r), true)
		if err != nil {
			hubFail(w, err)
			return
		}
		result, err := build(r.Context(), agent.OwnerID, &agent)
		v2RespondResult(w, 200, result, err)
	})
}
