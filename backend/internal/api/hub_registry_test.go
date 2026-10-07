package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"event-driven-context/internal/core"
)

func registryRequest(t *testing.T, h http.Handler, path, token string) (int, hubIntegrationRegistry) {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var out hubIntegrationRegistry
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	if bytes.Contains(w.Body.Bytes(), []byte("test-provider-private-secret")) {
		t.Fatal("credential leaked")
	}
	return w.Code, out
}
func registryProvider(t *testing.T, r hubIntegrationRegistry, id string) hubIntegration {
	t.Helper()
	for _, p := range r.Providers {
		if p.Provider.ID == id {
			return p
		}
	}
	t.Fatalf("missing provider %s", id)
	return hubIntegration{}
}
func registryFixture(t *testing.T) (*v2APIFixture, []byte, context.Context) {
	t.Helper()
	key := bytes.Repeat([]byte{17}, 32)
	t.Setenv("EDC_HUB_CREDENTIAL_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("EDC_HUB_GOOGLE_CLIENT_ID", "")
	t.Setenv("EDC_HUB_GOOGLE_CLIENT_SECRET", "")
	t.Setenv("EDC_HUB_GOOGLE_REDIRECT_URL", "")
	f := newV2APIFixture(t)
	return f, key, core.WithUser(context.Background(), f.alice.ID)
}
func TestHubRegistryOwnerIsolationAndManualVisibility(t *testing.T) {
	f, key, owner := registryFixture(t)
	for _, provider := range []string{"gmail", "telegram-bot", "whatsapp-business"} {
		if _, err := f.store.AddHubConnection(owner, core.HubConnection{ProviderID: provider, AccountID: provider + "-account", DisplayName: "Owner account"}, "test-provider-private-secret", key); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	registerHubRegistryHandlers(mux, f.store)
	if status, _ := registryRequest(t, mux, "/v1/hub/integrations", ""); status != 401 {
		t.Fatal("anonymous owner registry", status)
	}
	status, out := registryRequest(t, mux, "/v1/hub/integrations", f.token)
	if status != 200 {
		t.Fatal(status)
	}
	for _, id := range []string{"gmail", "telegram-bot"} {
		p := registryProvider(t, out, id)
		if !p.Connectable || !p.Visible || !p.DeploymentConfigured || p.ConnectedAccountCount != 1 || p.OnboardingMethod != "owner_cli" || len(p.Connections) != 1 || len(p.Connections[0].Operations) == 0 {
			t.Fatalf("manual adapter not usable: %+v", p)
		}
	}
	invalid := registryProvider(t, out, "whatsapp-business")
	if invalid.Visible || invalid.HiddenReason != "credential_unavailable" || len(invalid.Connections) != 0 || len(invalid.Provider.Operations) == 0 {
		t.Fatal("invalid webhook credential advertised as usable")
	}
	drive := registryProvider(t, out, "google-drive")
	if drive.Visible || drive.HiddenReason != "no_configured_accounts" {
		t.Fatal("empty source visible")
	}
	if !out.FeatureFlags["hub_approvals"] || out.FeatureFlags["google_oauth"] {
		t.Fatal("incorrect feature flags")
	}
	_, other := registryRequest(t, mux, "/v1/hub/integrations", f.bobToken)
	for _, p := range other.Providers {
		if p.Visible || p.ConnectedAccountCount != 0 || len(p.Connections) != 0 {
			t.Fatal("owner metadata leaked")
		}
	}
}
func TestHubRegistryMissingAndMalformedConfiguration(t *testing.T) {
	f, key, owner := registryFixture(t)
	if _, err := f.store.AddHubConnection(owner, core.HubConnection{ProviderID: "gmail", AccountID: "mail", DisplayName: "Mail"}, "test-provider-private-secret", key); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "not-base64", base64.StdEncoding.EncodeToString(key) + "!"} {
		t.Setenv("EDC_HUB_CREDENTIAL_KEY", bad)
		mux := http.NewServeMux()
		registerHubRegistryHandlers(mux, f.store)
		_, out := registryRequest(t, mux, "/v1/hub/integrations", f.token)
		p := registryProvider(t, out, "gmail")
		if p.Visible || p.DeploymentConfigured || p.Connectable || p.HiddenReason != "credential_storage_unconfigured" || len(p.Connections) != 0 {
			t.Fatalf("malformed key usable: %+v", p)
		}
	}
	t.Setenv("EDC_HUB_CREDENTIAL_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("EDC_HUB_CREDENTIAL_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{18}, 32)))
	wrongKeyMux := http.NewServeMux()
	registerHubRegistryHandlers(wrongKeyMux, f.store)
	_, wrongKeyOut := registryRequest(t, wrongKeyMux, "/v1/hub/integrations", f.token)
	wrongKeyProvider := registryProvider(t, wrongKeyOut, "gmail")
	if wrongKeyProvider.Visible || wrongKeyProvider.ConnectedAccountCount != 0 || len(wrongKeyProvider.Connections) != 0 || wrongKeyProvider.HiddenReason != "credential_unavailable" {
		t.Fatal("undecryptable account advertised as available")
	}
	t.Setenv("EDC_HUB_CREDENTIAL_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("EDC_HUB_GOOGLE_CLIENT_ID", "client")
	t.Setenv("EDC_HUB_GOOGLE_CLIENT_SECRET", "secret")
	t.Setenv("EDC_HUB_GOOGLE_REDIRECT_URL", "http://external.example/callback")
	mux := http.NewServeMux()
	registerHubRegistryHandlers(mux, f.store)
	_, out := registryRequest(t, mux, "/v1/hub/integrations", f.token)
	if out.FeatureFlags["google_oauth"] || registryProvider(t, out, "gmail").OnboardingMethod != "owner_cli" || !registryProvider(t, out, "gmail").Connectable {
		t.Fatal("invalid OAuth URI accepted")
	}
	t.Setenv("EDC_HUB_GOOGLE_REDIRECT_URL", "https://hub.example/v1/hub/google/callback")
	mux = http.NewServeMux()
	registerHubRegistryHandlers(mux, f.store)
	_, out = registryRequest(t, mux, "/v1/hub/integrations", f.token)
	if !out.FeatureFlags["google_oauth"] || registryProvider(t, out, "gmail").OnboardingMethod != "browser_oauth" {
		t.Fatal("valid OAuth config unavailable")
	}
}
func TestHubRegistryAgentExactGrantIsolation(t *testing.T) {
	f, key, owner := registryFixture(t)
	c, err := f.store.AddHubConnection(owner, core.HubConnection{ProviderID: "gmail", AccountID: "mail", DisplayName: "Mail"}, "test-provider-private-secret", key)
	if err != nil {
		t.Fatal(err)
	}
	var agents []core.HubAgentRegistration
	for _, name := range []string{"first", "second"} {
		a, err := f.store.RegisterHubAgent(context.Background(), f.alice.Username, name)
		if err != nil {
			t.Fatal(err)
		}
		agents = append(agents, a)
	}
	mux := http.NewServeMux()
	registerHubRegistryHandlers(mux, f.store)
	if status, _ := registryRequest(t, mux, "/v1/hub/integrations-agent", agents[0].Token); status != 403 {
		t.Fatal("pending agent discovered accounts", status)
	}
	for _, a := range agents {
		if err := f.store.DecideHubAgent(owner, a.ID, "approve", a.VerificationCode); err != nil {
			t.Fatal(err)
		}
	}
	if status, _ := registryRequest(t, mux, "/v1/hub/integrations", agents[0].Token); status != 401 {
		t.Fatal("agent entered owner registry")
	}
	if status, _ := registryRequest(t, mux, "/v1/hub/integrations-agent", f.token); status != 401 {
		t.Fatal("owner session masqueraded as agent")
	}
	approvedAgent, err := f.store.HubAgent(context.Background(), agents[0].Token, true)
	if err != nil {
		t.Fatal(err)
	}
	request, err := f.store.RequestHubAccess(context.Background(), approvedAgent, c.ID, "messages.list", "Read relevant messages", 3600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.DecideHubRequest(owner, request.ID, "approve"); err != nil {
		t.Fatal(err)
	}
	calendar, err := f.store.AddHubConnection(owner, core.HubConnection{ProviderID: "google-calendar", AccountID: "calendar", DisplayName: "Calendar"}, "test-provider-private-secret", key)
	if err != nil {
		t.Fatal(err)
	}
	bounds := &core.HubConstraints{CalendarID: "primary", TimeMin: "2026-09-19T00:00:00Z", TimeMax: "2026-09-20T00:00:00Z"}
	calendarGrant, err := f.store.RequestHubAccess(context.Background(), approvedAgent, calendar.ID, "events.list", "Read this day", 3600, bounds)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.DecideHubRequest(owner, calendarGrant.ID, "approve"); err != nil {
		t.Fatal(err)
	}
	for index, a := range agents {
		status, out := registryRequest(t, mux, "/v1/hub/integrations-agent", a.Token)
		if status != 200 {
			t.Fatal(status)
		}
		cp := registryProvider(t, out, "google-calendar")
		for _, op := range cp.Connections[0].Operations {
			if op.ID == "events.list" {
				if index == 0 {
					if len(op.Grants) != 1 || op.Grants[0].Constraints == nil || *op.Grants[0].Constraints != *bounds || op.Grants[0].ID != calendarGrant.ID {
						t.Fatal("calendar boundaries omitted or broadened")
					}
				} else if len(op.Grants) != 0 {
					t.Fatal("another agent's calendar grant leaked")
				}
			}
		}
		p := registryProvider(t, out, "gmail")
		if len(p.Connections) != 1 {
			t.Fatal("source missing")
		}
		for _, op := range p.Connections[0].Operations {
			want := index == 0 && op.ID == "messages.list"
			if op.Authorized == nil || *op.Authorized != want {
				t.Fatalf("grant broadened for agent %d op %s", index, op.ID)
			}
		}
	}
	if err := f.store.DecideHubRequest(owner, request.ID, "revoke"); err != nil {
		t.Fatal(err)
	}
	_, out := registryRequest(t, mux, "/v1/hub/integrations-agent", agents[0].Token)
	for _, op := range registryProvider(t, out, "gmail").Connections[0].Operations {
		if op.Authorized == nil || *op.Authorized {
			t.Fatal("revoked grant still authorized")
		}
	}
}

func TestHubRegistryMicrosoftManualOnboardingDoesNotRequireGoogle(t *testing.T) {
	f, key, owner := registryFixture(t)
	for _, id := range []string{"outlook-mail", "microsoft-calendar", "onedrive", "microsoft-todo"} {
		if _, err := f.store.AddHubConnection(owner, core.HubConnection{ProviderID: id, AccountID: id + "-account", DisplayName: "Microsoft account"}, "test-provider-private-secret", key); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	registerHubRegistryHandlers(mux, f.store)
	status, out := registryRequest(t, mux, "/v1/hub/integrations", f.token)
	if status != 200 {
		t.Fatal(status)
	}
	for _, id := range []string{"outlook-mail", "microsoft-calendar", "onedrive", "microsoft-todo"} {
		p := registryProvider(t, out, id)
		if !p.Connectable || !p.Visible || !p.DeploymentConfigured || p.OnboardingMethod != "owner_cli" {
			t.Fatalf("Microsoft CLI onboarding incorrectly depends on Google config: %+v", p)
		}
	}
	if !registryProvider(t, out, "gmail").Connectable || registryProvider(t, out, "gmail").OnboardingMethod != "owner_cli" || out.FeatureFlags["google_oauth"] {
		t.Fatal("Google should retain CLI onboarding without browser OAuth")
	}
}
