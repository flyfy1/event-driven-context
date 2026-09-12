package noteindexer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"event-driven-context/internal/notes"
	"event-driven-context/internal/v2client"
)

func TestRunnerNeverPublishesFailedOrUnfinishedChildren(t *testing.T) {
	t.Setenv("EDC_TOKEN", "broad-user-token")
	t.Setenv("EDC_PLUGIN_TOKEN", "broad-plugin-token")
	t.Setenv("OPENAI_API_KEY", "unrelated-api-key")
	for _, tc := range []struct {
		name, script, want string
		timeout            time.Duration
	}{
		{"child failure", "printf '%s' \"$EDC_NOTES_RUN_SECRET\" >&2\nexit 7\n", "[redacted]", 10 * time.Second},
		{"exit without finish", "exit 0\n", "without successfully validating", 10 * time.Second},
		{"timeout", "sleep 5\n", "", 50 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publishes, cancels := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer scoped-plugin-token" {
					t.Error("incorrect backend auth")
				}
				switch r.URL.Path {
				case "/v1/projects/prj_one/notes/organizer/begin":
					json.NewEncoder(w).Encode(notes.OrganizationRun{RunID: "run_test", Timezone: "Asia/Singapore", ThroughSequence: 1, Snapshot: notes.Export{ProjectID: "prj_one", Files: []notes.File{}}, Events: []notes.EventPreview{{ID: "evt_one", Sequence: 1}}})
				case "/v1/projects/prj_one/notes/organizer/publish":
					publishes++
					json.NewEncoder(w).Encode(notes.PublishResult{Revision: 1, ThroughSequence: 1})
				case "/v1/projects/prj_one/notes/organizer/cancel":
					cancels++
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, err := v2client.New(server.URL, "scoped-plugin-token")
			if err != nil {
				t.Fatal(err)
			}
			child := filepath.Join(t.TempDir(), "codex-fixture")
			// The fixture runs through the real process launcher; broad credentials must
			// not reach it, while the short-lived draft-tool credential is available.
			script := "#!/bin/sh\nif [ -n \"$EDC_TOKEN$EDC_PLUGIN_TOKEN$OPENAI_API_KEY\" ]; then echo inherited-broad-token >&2; exit 94; fi\nif [ -z \"$EDC_NOTES_RUN_SECRET\" ]; then echo missing-run-token >&2; exit 95; fi\n" + tc.script
			if err = os.WriteFile(child, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			_, err = Run(context.Background(), client, Options{ProjectID: "prj_one", CodexPath: child, Timeout: tc.timeout})
			if err == nil {
				t.Fatal("failed or unfinished run succeeded")
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected failure: %v", err)
			}
			if tc.name == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected timeout, got %v", err)
			}
			if publishes != 0 || cancels != 1 {
				t.Fatalf("published=%d canceled=%d err=%v", publishes, cancels, err)
			}
			for _, secret := range []string{"broad-user-token", "broad-plugin-token", "unrelated-api-key"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("failure leaked broad credentials")
				}
			}
		})
	}
}

func TestSeedDefaultsPreservesEditablePolicies(t *testing.T) {
	original := "user's existing policy"
	files := map[string]string{"topics/organization.md": original}
	seedDefaults(files)
	if files["topics/organization.md"] != original {
		t.Fatal("defaults replaced user policy")
	}
	if files["goals/priorities.md"] == "" || files["topics/work/index.md"] == "" || files["topics/life/index.md"] == "" {
		t.Fatal("missing initial navigation")
	}
}
