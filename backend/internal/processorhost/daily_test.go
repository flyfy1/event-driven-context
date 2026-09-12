package processorhost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"event-driven-context/internal/api"
	"event-driven-context/internal/core"
	"event-driven-context/internal/v2"
	"event-driven-context/internal/v2client"
	"github.com/google/uuid"
)

func TestDailyScheduledWindowPublishesOnce(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	store, err := core.Open(filepath.Join(dataRoot, "identity.db"), filepath.Join(dataRoot, "legacy"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := v2.New(store, filepath.Join(dataRoot, "v2"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	server := httptest.NewServer(api.V2Handler(store, service, nil))
	t.Cleanup(server.Close)

	client, err := v2client.New(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	credentials := core.Credentials{Username: "daily-host-test", Email: "daily-host-test@example.invalid", Password: "daily-host-password"}
	if _, err = client.Register(ctx, credentials); err != nil {
		t.Fatal(err)
	}
	login, err := client.Login(ctx, credentials)
	if err != nil {
		t.Fatal(err)
	}
	client.Token = login.Token
	project, err := client.CreateProject(ctx, core.ProjectInput{Name: "Daily host"})
	if err != nil {
		t.Fatal(err)
	}

	actual := time.Now().UTC()
	due := actual.Add(2 * time.Minute).Truncate(time.Minute)
	eventID, _ := uuid.NewV7()
	event := v2.EventInput{ID: eventID.String(), Type: "note", Content: v2.EventContent{Kind: "text", Text: "Scheduled decision"}, Source: map[string]json.RawMessage{"channel": json.RawMessage(`"cli"`)}}
	if _, err = client.RecordEvents(ctx, project.ID, v2.RecordEventsInput{Events: []v2.EventInput{event}}); err != nil {
		t.Fatal(err)
	}

	pluginRoot := dailyPluginRoot(t)
	manifestRaw, err := os.ReadFile(filepath.Join(pluginRoot, "daily-review", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest v2.Manifest
	if err = json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	config := json.RawMessage(fmt.Sprintf(`{"time":%q,"language":"en","prompt":"test"}`, due.Format("15:04")))
	installed, err := client.InstallPlugin(ctx, project.ID, v2.InstallPluginInput{Manifest: manifest, Config: config})
	if err != nil {
		t.Fatal(err)
	}

	countPath := filepath.Join(t.TempDir(), "runs")
	agentPath := filepath.Join(t.TempDir(), "agent")
	output := fmt.Sprintf(`{"state":{"name":%q,"format":"markdown","text":%q,"source_event_ids":[%q]}}`, due.Format("2006-01-02"), "## Decisions\n- Scheduled decision 〔"+event.ID+"〕", event.ID)
	script := "#!/bin/sh\nout=''\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = '-o' ]; then shift; out=$1; fi\n  shift\ndone\nprintf x >> '" + countPath + "'\ncat > \"$out\" <<'EOF'\n" + output + "\nEOF\n"
	if err = os.WriteFile(agentPath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	opts := Options{ProjectID: project.ID, PluginID: "daily-review", PluginDir: pluginRoot, PluginToken: installed.Token, AgentCommand: agentPath, Once: true, Now: func() time.Time { return due.Add(30 * time.Second) }}
	first, err := RunOnce(ctx, client, opts)
	if err != nil {
		t.Fatal(err)
	}
	if first.ScheduledDate != due.Format("2006-01-02") || first.ProcessedEvents != 1 || first.StateVersion != 1 || first.WindowTo != due.Format(time.RFC3339) {
		t.Fatalf("unexpected first run: %+v", first)
	}
	state, err := client.GetState(ctx, project.ID, "daily-review/"+first.ScheduledDate, nil)
	if err != nil {
		t.Fatal(err)
	}
	var evidence struct {
		ScheduledAt  string   `json:"scheduled_at"`
		WindowFrom   string   `json:"window_from"`
		WindowTo     string   `json:"window_to"`
		Timezone     string   `json:"timezone"`
		SkippedDates []string `json:"skipped_dates"`
	}
	if err = json.Unmarshal(state.Data, &evidence); err != nil || evidence.ScheduledAt != first.ScheduledAt || evidence.WindowFrom != first.WindowFrom || evidence.WindowTo != first.WindowTo || evidence.Timezone != "UTC" {
		t.Fatalf("missing fixed window evidence: %s %v", state.Data, err)
	}
	second, err := RunOnce(ctx, client, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Noop || second.Reason != "already_published" || second.StateVersion != 1 || second.WindowFrom != first.WindowFrom {
		t.Fatalf("repeated tick republished: %+v", second)
	}
	runs, err := os.ReadFile(countPath)
	if err != nil || string(runs) != "x" {
		t.Fatalf("agent ran more than once: %q %v", runs, err)
	}
}

func TestDailyPeriodAndDelayedSkipUseLatestDueDate(t *testing.T) {
	now := time.Date(2026, 9, 12, 13, 1, 0, 0, time.UTC) // 21:01 Asia/Singapore
	period, err := latestDailyPeriod(now, "Asia/Singapore", "21:00")
	if err != nil {
		t.Fatal(err)
	}
	if period.Date != "2026-09-12" || period.From.Format(time.RFC3339) != "2026-09-11T21:00:00+08:00" || period.To.Format(time.RFC3339) != "2026-09-12T21:00:00+08:00" {
		t.Fatalf("unexpected fixed period: %+v", period)
	}
	skipped := skippedDates(dailyCursorData{LastScheduledDate: "2026-09-08", Completed: true}, period)
	if fmt.Sprint(skipped) != "[2026-09-09 2026-09-10 2026-09-11]" {
		t.Fatalf("offline periods not marked skipped: %v", skipped)
	}
}

func dailyPluginRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "plugins"))
}
