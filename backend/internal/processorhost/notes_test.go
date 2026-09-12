package processorhost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"event-driven-context/internal/v2"
	"event-driven-context/internal/v2client"
)

func TestNotesHostUsesBuiltInProtocolWithoutStateCursor(t *testing.T) {
	_, manifest, spec, err := loadProcessor(dailyPluginRoot(t), notesPluginID)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Permissions.WriteState) != 0 || !manifest.Permissions.OrganizeNotes || spec.Entry.Skill != "" {
		t.Fatal("notes manifest must use dedicated organization permission and built-in prompt")
	}
	begins := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer plugin-only-token" {
			t.Error("host used a user token")
		}
		if r.URL.Path == "/v1/projects/prj_one/notes/organizer/begin" && r.Method == http.MethodPost {
			begins++
			io.WriteString(w, `{"noop":true,"reason":"up_to_date","after_sequence":0,"through_sequence":0,"snapshot":{"project_id":"prj_one","revision":0,"files":[]},"events":[]}`)
			return
		}
		if r.URL.Path != "/v1/projects/prj_one/plugins" {
			t.Errorf("unexpected cursor/event query %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}

		json.NewEncoder(w).Encode(map[string]any{"plugins": []v2.Installation{{ProjectID: "prj_one", PluginID: notesPluginID, PluginVersion: manifest.Version, Manifest: manifest, Permissions: manifest.Permissions, Status: "active"}}})
	}))
	t.Cleanup(server.Close)
	client, err := v2client.New(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = RunOnce(context.Background(), client, Options{ProjectID: "prj_one", PluginID: notesPluginID, PluginDir: dailyPluginRoot(t), PluginToken: "plugin-only-token", Timeout: 11 * time.Minute})
	if err == nil || !strings.Contains(err.Error(), "10m") {
		t.Fatalf("expected notes timeout validation before any State cursor: %v", err)
	}
	if begins != 0 {
		t.Fatal("timeout validation reached organizer")
	}
	result, err := RunOnce(context.Background(), client, Options{ProjectID: "prj_one", PluginID: notesPluginID, PluginDir: dailyPluginRoot(t), PluginToken: "plugin-only-token", AgentCommand: "/usr/bin/false"})
	if err != nil || !result.Noop || result.Reason != "up_to_date" || begins != 1 {
		t.Fatalf("no-op notes run failed: %+v, begins=%d, err=%v", result, begins, err)
	}

}

func TestNotesHostRejectsMissingPermissionAndProtocol(t *testing.T) {
	_, manifest, spec, err := loadProcessor(dailyPluginRoot(t), notesPluginID)
	if err != nil {
		t.Fatal(err)
	}
	installation := v2.Installation{Manifest: manifest, Permissions: manifest.Permissions}
	installation.Permissions.OrganizeNotes = false
	_, err = runNotes(context.Background(), nil, Options{}, spec, installation, Result{})
	if err == nil || !strings.Contains(err.Error(), "organize_notes") {
		t.Fatalf("%v", err)
	}
	installation.Permissions.OrganizeNotes = true
	spec.Entry.Skill = "untrusted/SKILL.md"
	_, err = runNotes(context.Background(), nil, Options{}, spec, installation, Result{})
	if err == nil || !strings.Contains(err.Error(), "built-in") {
		t.Fatalf("%v", err)
	}
}

func TestNotesWatchBackoffCapsAndResets(t *testing.T) {
	delay := time.Duration(0)
	for _, want := range []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
		delay = notesWatchBackoff(15*time.Second, delay, true)
		if delay != want {
			t.Fatalf("got %s want %s", delay, want)
		}
	}
	delay = notesWatchBackoff(15*time.Second, delay, false)
	if delay != 0 {
		t.Fatalf("success did not reset: %s", delay)
	}
	if got := notesWatchBackoff(15*time.Second, delay, true); got != 15*time.Second {
		t.Fatalf("retry did not restart: %s", got)
	}
}
