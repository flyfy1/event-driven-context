package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"event-driven-context/internal/buildinfo"
	"event-driven-context/internal/updater"
)

func TestVersionDoesNotRequireLogin(t *testing.T) {
	var stdout bytes.Buffer
	a := &app{ctx: context.Background(), io: streams{in: strings.NewReader(""), out: &stdout, err: &bytes.Buffer{}}}
	if err := a.dispatch("version", nil); err != nil {
		t.Fatal(err)
	}
	var info buildinfo.Info
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil || info.Version != buildinfo.Version {
		t.Fatalf("version output=%q info=%#v error=%v", stdout.String(), info, err)
	}
}

func TestVersionDoesNotReadBrokenConfig(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(config, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDC_CONFIG", config)
	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"version"}, streams{in: strings.NewReader(""), out: &stdout, err: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), buildinfo.Version) {
		t.Fatalf("version output = %q", stdout.String())
	}
}

func TestUpdateCheckReportsServerPolicyWithoutInstalling(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/edc-cli" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(buildinfo.CLIPolicy{
			LatestVersion: buildinfo.Version, MinimumVersion: buildinfo.MinimumCLIVersion,
			ReleaseAPIURL: server.URL + "/release", ReleasePageURL: server.URL + "/page",
		})
	}))
	defer server.Close()
	var stdout bytes.Buffer
	a := &app{
		ctx: context.Background(), io: streams{in: strings.NewReader(""), out: &stdout, err: &bytes.Buffer{}},
		updates: &updater.Client{HTTP: server.Client()}, executable: filepath.Join(t.TempDir(), "edc"), server: server.URL,
	}
	if err := a.dispatch("update", []string{"--check"}); err != nil {
		t.Fatal(err)
	}
	var status updater.Status
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil || !status.PolicyAvailable || status.CLIVersion != buildinfo.Version || status.UpdateAvailable {
		t.Fatalf("status output=%q status=%#v error=%v", stdout.String(), status, err)
	}
}
