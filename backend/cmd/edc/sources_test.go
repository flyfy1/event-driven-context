package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"event-driven-context/internal/core"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceDiscoveryAndBrowserOnboarding(t *testing.T) {
	t.Setenv("EDC_TOKEN", "fixture-owner-token")
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/hub/capabilities":
			w.Write([]byte(`{"providers":[{"id":"gmail","implementation_status":"adapter_available","operations":[{"id":"messages.list"}]},{"id":"telegram-user","implementation_status":"not_implemented","operations":[]}],"credential_storage_configured":true,"authorization":{"owner_approval_page":"https://context.example/hub.html"}}`))
		case "/v1/hub/integrations":
			w.Write([]byte(`{"providers":[{"provider":{"id":"gmail"},"connectable":true,"onboarding_method":"browser_oauth","connections":[{"id":"conn_fixture","operations":[{"id":"messages.list","authorized":false,"grants":[]}]}]}]}`))
		case "/v1/hub/integrations-agent":
			w.Write([]byte(`{"providers":[{"connections":[{"id":"conn_fixture","operations":[{"id":"messages.list","authorized":true,"grants":[{"id":"grant_fixture"}]}]}]}]}`))
		case "/v1/hub/execute":
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			if r.Method != "POST" || in["connection_id"] != "conn_fixture" || in["operation"] != "messages.list" {
				t.Error("read contract", in)
			}
			w.Write([]byte(`{"data":{"messages":[]}}`))
		default:
			t.Error("unexpected route", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	runSource := func(args ...string) map[string]any {
		var out, stderr bytes.Buffer
		base := []string{"--server", server.URL, "--config", filepath.Join(t.TempDir(), "empty.json"), "source"}
		if err := run(t.Context(), append(base, args...), streams{strings.NewReader(""), &out, &stderr}); err != nil {
			t.Fatal(err, stderr.String())
		}
		var value map[string]any
		if json.Unmarshal(out.Bytes(), &value) != nil {
			t.Fatal("not JSON", out.String())
		}
		return value
	}
	result := runSource("catalog", "--available-only")
	if len(result["providers"].([]any)) != 1 {
		t.Fatal("catalog filter")
	}
	result = runSource("operations", "--connection", "conn_fixture")
	op := result["operations"].([]any)[0].(map[string]any)
	if op["authorized"] != true || len(op["grants"].([]any)) != 1 {
		t.Fatal("effective grants lost")
	}
	requests = nil
	result = runSource("connect", "--provider", "gmail", "--name", "Work & private")
	target, err := url.Parse(result["onboarding_url"].(string))
	if err != nil || result["connected"] != false || target.Host != "context.example" || target.Path != "/hub.html" || target.Query().Get("api") != server.URL || target.Query().Get("provider") != "gmail" || target.Query().Get("name") != "Work & private" {
		t.Fatal("browser onboarding", result)
	}

	for _, request := range requests {
		if strings.HasPrefix(request, "POST ") {
			t.Fatal("CLI started cookie-bound OAuth outside browser")
		}
	}
	result = runSource("read", "--connection", "conn_fixture", "--operation", "messages.list", "--args", `{"limit":1}`)
	if result["data"] == nil {
		t.Fatal("read data missing")
	}
}

func TestSourceImportZIPPreservesBinaryUpload(t *testing.T) {
	t.Setenv("EDC_TOKEN", "fixture-owner-token")
	content := []byte{'P', 'K', 0, 255, 128, 1}
	file := filepath.Join(t.TempDir(), "chat.zip")
	if err := os.WriteFile(file, content, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var in core.HubImportInput
		if r.URL.Path != "/v1/hub/imports" || r.Method != "POST" || json.NewDecoder(r.Body).Decode(&in) != nil {
			t.Fatal("import route")
		}
		raw, err := base64.StdEncoding.DecodeString(in.ContentBase64)
		if err != nil || !bytes.Equal(raw, content) || in.Content != "" || in.Format != "whatsapp-zip" || in.Filename != "chat.zip" {
			t.Fatal("binary changed")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"snapshot":true}`))
	}))
	defer server.Close()
	var out, stderr bytes.Buffer
	err := run(t.Context(), []string{"--server", server.URL, "--config", filepath.Join(t.TempDir(), "empty.json"), "source", "import", "--provider", "whatsapp-import", "--account", "personal", "--name", "Personal", "--format", "whatsapp-zip", "--file", file}, streams{strings.NewReader(""), &out, &stderr})
	if err != nil || calls != 1 {
		t.Fatal("CLI ZIP upload", err, stderr.String())
	}
}

func TestSourcePublicDiscoveryWithoutLogin(t *testing.T) {
	t.Setenv("EDC_TOKEN", "")
	t.Setenv("EDC_SERVER", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/hub/capabilities" || r.Header.Get("Authorization") != "" {
			t.Fatal("public discovery contract")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"providers":[{"id":"gmail","implementation_status":"adapter_available","operations":[{"id":"messages.list"}]}]}`))
	}))
	defer server.Close()
	for _, args := range [][]string{{"source", "catalog", "--available-only"}, {"source", "operations", "--provider", "gmail"}} {
		var out, stderr bytes.Buffer
		base := []string{"--server", server.URL, "--config", filepath.Join(t.TempDir(), "empty.json")}
		if err := run(t.Context(), append(base, args...), streams{strings.NewReader(""), &out, &stderr}); err != nil || !json.Valid(out.Bytes()) {
			t.Fatal("public discovery required login", err, stderr.String())
		}
	}
}
