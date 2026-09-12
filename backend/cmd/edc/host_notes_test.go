package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"event-driven-context/internal/v2client"
)

func TestHostDispatchAcceptsPluginTokenWithoutUserLogin(t *testing.T) {
	t.Setenv("EDC_PLUGIN_TOKEN", "")
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/v1/projects/prj_one/plugins" || r.Header.Get("Authorization") != "Bearer only-plugin-token" {
			t.Errorf("unexpected auth request %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"error":{"code":"forbidden","message":"test stop"}}`)
	}))
	t.Cleanup(server.Close)
	client, err := v2client.New(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(t.TempDir(), "plugin.token")
	if err = os.WriteFile(tokenPath, []byte("only-plugin-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &app{ctx: context.Background(), client: client, io: streams{in: strings.NewReader(""), out: io.Discard, err: io.Discard}}
	err = a.dispatch("host", []string{"run", "--project", "prj_one", "--plugin", "notes-indexer", "--plugin-dir", t.TempDir(), "--plugin-token-file", tokenPath, "--once"})
	if !called || err == nil || !strings.Contains(err.Error(), "read plugin installation") {
		t.Fatalf("plugin-only host did not reach installation: called=%v err=%v", called, err)
	}
	if err = a.dispatch("notes", []string{"sync", "--project", "prj_one", "--output", t.TempDir()}); err == nil || !strings.Contains(err.Error(), "login first") {
		t.Fatalf("user command bypassed auth: %v", err)
	}
}

func TestPluginOnlyHostRequiresExplicitProject(t *testing.T) {
	a := &app{io: streams{out: io.Discard, err: io.Discard}}
	err := a.dispatch("host", []string{"run", "--plugin", "notes-indexer", "--plugin-dir", t.TempDir(), "--once"})
	if err == nil || !strings.Contains(err.Error(), "--project is required") {
		t.Fatalf("%v", err)
	}
}
