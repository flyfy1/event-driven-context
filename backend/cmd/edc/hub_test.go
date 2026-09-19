package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHubAgentConnectKeepsCredentialPrivate(t *testing.T) {
	t.Setenv("EDC_TOKEN", "")
	t.Setenv("EDC_SERVER", "")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/hub/agents" {
			http.NotFound(w, r)
			return
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "agent_1", "name": "test-agent", "status": "pending", "verification_code": "COMPARE123", "token": "edc_agent_test_secret", "expires_at": "2099-01-01T00:00:00Z"})
	}))
	defer server.Close()
	root := t.TempDir()
	ownerConfig := filepath.Join(root, "owner.json")
	agentConfig := filepath.Join(root, "agent.json")
	var out, stderr bytes.Buffer
	args := []string{"--server", server.URL, "--config", ownerConfig, "agent", "connect", "--owner", "alice", "--name", "test-agent", "--output", agentConfig}
	if err := run(t.Context(), args, streams{strings.NewReader(""), &out, &stderr}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String()+stderr.String(), "edc_agent_test_secret") {
		t.Fatal("agent credential printed")
	}
	if !strings.Contains(out.String(), "COMPARE123") {
		t.Fatal("verification code missing")
	}
	info, err := os.Stat(agentConfig)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe agent config", err)
	}
	cfg, err := readConfig(agentConfig)
	if err != nil || cfg.Token != "edc_agent_test_secret" || cfg.Server != server.URL {
		t.Fatal("agent configuration not saved")
	}
	if _, err = os.Stat(ownerConfig); !os.IsNotExist(err) {
		t.Fatal("owner config modified")
	}
	out.Reset()
	if err := run(t.Context(), args, streams{strings.NewReader(""), &out, &stderr}); err == nil {
		t.Fatal("existing config overwritten")
	}
	if calls != 1 {
		t.Fatal("created orphan registration before config check")
	}
}
func TestHubCLIProducesMachineReadableAuthorizationError(t *testing.T) {
	t.Setenv("EDC_TOKEN", "edc_agent_test")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		w.Write([]byte(`{"error":{"code":"authorization_required","message":"Owner approval required"}}`))
	}))
	defer server.Close()
	var out, stderr bytes.Buffer
	err := run(t.Context(), []string{"--server", server.URL, "--config", filepath.Join(t.TempDir(), "empty.json"), "api", "call", "--connection", "conn_1", "--operation", "messages.list"}, streams{strings.NewReader(""), &out, &stderr})
	if err == nil {
		t.Fatal("authorization error returned success")
	}
	var result map[string]any
	if json.Unmarshal(out.Bytes(), &result) != nil || result["approval_path"] != "/hub.html" {
		t.Fatal("missing structured recovery", out.String())
	}
}
