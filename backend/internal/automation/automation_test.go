package automation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"event-driven-context/internal/core"
	"event-driven-context/internal/runner"
)

type testFixture struct {
	store   *core.Store
	dataDir string
	now     *time.Time
	alice   context.Context
	bob     context.Context
	project core.Project
	c       *Coordinator
	token   string
	runner  RegisteredRunner
}

func newFixture(t *testing.T) *testFixture {
	t.Helper()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	store, err := core.Open(filepath.Join(root, "edc.db"), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	aliceUser, err := store.Register(context.Background(), core.Credentials{Username: "alice", Email: "automation-alice@example.invalid", Password: "test-password-long-enough"})
	if err != nil {
		t.Fatal(err)
	}
	bobUser, err := store.Register(context.Background(), core.Credentials{Username: "bob", Email: "automation-bob@example.invalid", Password: "test-password-long-enough"})
	if err != nil {
		t.Fatal(err)
	}
	alice := core.WithUser(context.Background(), aliceUser.ID)
	bob := core.WithUser(context.Background(), bobUser.ID)
	project, err := store.CreateProject(alice, core.ProjectInput{Name: "records"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	c, err := New(store, dataDir, testSkills(), WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	registration, err := c.RegisterRunner(alice, RegisterRunnerInput{Name: "local", Capabilities: []string{"audio_transcription", "daily_review"}})
	if err != nil {
		t.Fatal(err)
	}
	return &testFixture{store: store, dataDir: dataDir, now: &now, alice: alice, bob: bob, project: project, c: c, token: registration.Token, runner: registration.Runner}
}

func testSkills() []SkillDescriptor {
	return []SkillDescriptor{
		{ID: AudioTranscribeSkill, Version: FixedSkillVersion, Digest: strings.Repeat("a", 64)},
		{ID: DailyReviewSkill, Version: FixedSkillVersion, Digest: strings.Repeat("b", 64)},
	}
}

func (f *testFixture) audioInstallation(t *testing.T, prompt string, metadata map[string]json.RawMessage) Installation {
	t.Helper()
	installation, err := f.c.CreateInstallation(f.alice, CreateInstallationInput{
		ProjectID: f.project.ID, RunnerID: f.runner.ID, SkillID: AudioTranscribeSkill,
		UserPrompt: prompt, Language: "en", Trigger: Trigger{Type: "event", MetadataEquals: metadata},
	})
	if err != nil {
		t.Fatal(err)
	}
	return installation
}

func (f *testFixture) recordAudio(t *testing.T, metadata map[string]json.RawMessage) core.Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "core", "testdata", "fixture.m4a"))
	if err != nil {
		t.Fatal(err)
	}
	event, err := f.store.RecordMediaEvent(f.alice, core.MediaRecordInput{ProjectID: f.project.ID, Filename: "recording.m4a", MediaType: "audio/mp4", Data: data, Metadata: metadata, IdempotencyKey: newID("capture")})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func transcriptCandidate(eventID, text string) runner.Candidate {
	return runner.Candidate{SchemaVersion: 1, Outcome: "output", OutputSlot: "transcript", Kind: "transcript", Text: text, SourceEventIDs: []string{eventID}}
}

func credential(claim *Claim) AttemptCredential {
	return AttemptCredential{AttemptID: claim.AttemptID, FencingToken: claim.FencingToken}
}

func TestAudioLifecycleIsolationPauseRetryAndSupersedes(t *testing.T) {
	f := newFixture(t)
	if runners, err := f.c.ListRunners(f.bob); err != nil || len(runners) != 0 {
		t.Fatalf("runner scope leaked: %#v %v", runners, err)
	}
	if _, err := f.c.CreateInstallation(f.bob, CreateInstallationInput{ProjectID: f.project.ID, RunnerID: f.runner.ID, SkillID: AudioTranscribeSkill, Trigger: Trigger{Type: "event"}}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("cross-project create got %v", err)
	}

	installation := f.audioInstallation(t, "first config", map[string]json.RawMessage{"nested": json.RawMessage(`{"b":2,"a":1}`)})
	prior := f.recordAudio(t, nil)
	claim, err := f.c.Dispatch(context.Background(), f.token)
	if err != nil {
		t.Fatal(err)
	}
	if claim != nil {
		t.Fatal("metadata mismatch triggered a run")
	}
	event := f.recordAudio(t, map[string]json.RawMessage{"nested": json.RawMessage(` { "a": 1, "b": 2 } `)})
	claim, err = f.c.Dispatch(context.Background(), f.token)
	if err != nil || claim == nil {
		t.Fatalf("dispatch: %#v %v", claim, err)
	}
	if claim.Run.RevisionID != installation.CurrentRevisionID || claim.Task.UserPrompt != "first config" || claim.Task.Inputs[0].AudioPath != "" || claim.Task.Inputs[0].AudioSHA == "" || claim.InputFiles[0].FileID != event.Content.File.ID {
		t.Fatalf("invalid fixed claim: %#v", claim)
	}
	if _, file, err := f.c.OpenInputFile(context.Background(), f.token, claim.Run.ID, claim.InputFiles[0].FileID, credential(claim)); err != nil {
		t.Fatal(err)
	} else {
		_ = file.Close()
	}
	if _, _, err := f.c.OpenInputFile(context.Background(), "wrong", claim.Run.ID, claim.InputFiles[0].FileID, credential(claim)); !errors.Is(err, core.ErrUnauthenticated) {
		t.Fatalf("runner scope read got %v", err)
	}
	if _, err = f.c.SetInstallation(f.alice, installation.ID, InstallationActionInput{Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.c.Submit(context.Background(), f.token, claim.Run.ID, credential(claim), SubmitInput{Candidate: ptrCandidate(transcriptCandidate(event.ID, "must reject"))}); err == nil {
		t.Fatal("pause did not fence submit")
	}
	if got := f.c.state.Runs[claim.Run.ID]; got.Status != RunCancelled {
		t.Fatalf("paused run status %s", got.Status)
	}

	installation = f.audioInstallation(t, "active", nil)
	event = f.recordAudio(t, nil)
	claim, err = dispatchForInstallation(f.c, f.token, installation.ID)
	if err != nil {
		t.Fatal(err)
	}
	candidate := transcriptCandidate(event.ID, "faithful transcript")
	succeeded, err := f.c.Submit(context.Background(), f.token, claim.Run.ID, credential(claim), SubmitInput{Candidate: &candidate})
	if err != nil || succeeded.Status != RunSucceeded || succeeded.OutputEventID == "" {
		t.Fatalf("submit: %#v %v", succeeded, err)
	}
	if repeated, err := f.c.Submit(context.Background(), f.token, claim.Run.ID, credential(claim), SubmitInput{Candidate: &candidate}); err != nil || repeated.ID != succeeded.ID {
		t.Fatalf("idempotent submit: %#v %v", repeated, err)
	}
	inbox, err := f.c.ListInbox(f.alice)
	if err != nil || len(inbox.Entries) != 1 || inbox.Entries[0].Candidate.Text != candidate.Text {
		t.Fatalf("inbox: %#v %v", inbox, err)
	}
	output, err := f.store.GetEvent(f.alice, core.EventRef{EventID: succeeded.OutputEventID})
	if err != nil || output.Provenance.Kind != core.ProvenanceTranscript || output.Provenance.RunID != succeeded.ID || len(output.Provenance.SourceEventIDs) != 1 || output.Provenance.SourceEventIDs[0] != event.ID {
		t.Fatalf("derived event: %#v %v", output, err)
	}

	rerun, err := f.c.RequestRun(f.alice, RequestRunInput{InstallationID: installation.ID, RequestID: "retry-1", SourceEventIDs: []string{event.ID}})
	if err != nil || len(rerun.SupersedesEventIDs) != 1 || rerun.SupersedesEventIDs[0] != succeeded.OutputEventID {
		t.Fatalf("rerun supersedes freeze: %#v %v", rerun, err)
	}
	claim, err = dispatchForInstallation(f.c, f.token, installation.ID)
	if err != nil || claim.Run.ID != rerun.ID {
		t.Fatalf("rerun dispatch: %#v %v", claim, err)
	}
	firstCredential := credential(claim)
	*f.now = f.now.Add(3 * time.Minute)
	second, err := dispatchForInstallation(f.c, f.token, installation.ID)
	if err != nil || second.AttemptID == claim.AttemptID {
		t.Fatalf("lease retry: %#v %v", second, err)
	}
	if _, err = f.c.Submit(context.Background(), f.token, rerun.ID, firstCredential, SubmitInput{Candidate: ptrCandidate(transcriptCandidate(event.ID, "stale"))}); err == nil {
		t.Fatal("old attempt was accepted")
	}
	completed, err := f.c.Submit(context.Background(), f.token, rerun.ID, credential(second), SubmitInput{Candidate: ptrCandidate(transcriptCandidate(event.ID, "updated transcript"))})
	if err != nil {
		t.Fatal(err)
	}
	newOutput, err := f.store.GetEvent(f.alice, core.EventRef{EventID: completed.OutputEventID})
	if err != nil || len(newOutput.Relations.SupersedesEventIDs) != 1 || newOutput.Relations.SupersedesEventIDs[0] != succeeded.OutputEventID {
		t.Fatalf("supersedes relation: %#v %v", newOutput, err)
	}
	_ = prior
}

func dispatchForInstallation(c *Coordinator, token, installationID string) (*Claim, error) {
	for i := 0; i < 20; i++ {
		claim, err := c.Dispatch(context.Background(), token)
		if err != nil || claim == nil || claim.Run.InstallationID == installationID {
			return claim, err
		}
	}
	return nil, errors.New("installation claim not found")
}

func ptrCandidate(value runner.Candidate) *runner.Candidate { return &value }

func TestFutureOnlyEmptySequenceAndDiskFailure(t *testing.T) {
	f := newFixture(t)
	installation := f.audioInstallation(t, "v1", nil)
	revised, err := f.c.ReviseInstallation(f.alice, installation.ID, ReviseInstallationInput{UserPrompt: "v2", Trigger: Trigger{Type: "event"}})
	if err != nil {
		t.Fatal(err)
	}
	if !revised.Revisions[0].Closed || revised.Revisions[0].ActiveThroughSequence != 0 {
		t.Fatalf("zero sequence revision was not explicitly closed: %#v", revised.Revisions)
	}
	event := f.recordAudio(t, nil)
	claim, err := f.c.Dispatch(context.Background(), f.token)
	if err != nil || claim == nil || claim.Run.RevisionID != revised.CurrentRevisionID || claim.Task.UserPrompt != "v2" || claim.Run.Inputs[0].EventID != event.ID {
		t.Fatalf("future config: %#v %v", claim, err)
	}

	before := cloneInstallation(f.c.state.Installations[installation.ID])
	nextPath := f.c.journalPath(f.c.state.Sequence + 1)
	if err = os.MkdirAll(nextPath, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = f.c.SetInstallation(f.alice, installation.ID, InstallationActionInput{Action: "pause"}); err == nil {
		t.Fatal("expected durable journal failure")
	}
	after := f.c.state.Installations[installation.ID]
	if after.Enabled != before.Enabled || after.Revisions[len(after.Revisions)-1].Closed != before.Revisions[len(before.Revisions)-1].Closed {
		t.Fatalf("memory advanced after disk failure: before=%#v after=%#v", before, after)
	}
}

func TestCandidateCrashRecoveryIsIdempotent(t *testing.T) {
	for _, phase := range []string{"candidate", "event", "commit"} {
		t.Run(phase, func(t *testing.T) {
			f := newFixture(t)
			installation := f.audioInstallation(t, "", nil)
			event := f.recordAudio(t, nil)
			claim, err := dispatchForInstallation(f.c, f.token, installation.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch phase {
			case "candidate":
				f.c.hooks.AfterCandidate = true
			case "event":
				f.c.hooks.AfterEvent = true
			case "commit":
				f.c.hooks.AfterCommit = true
			}
			candidate := transcriptCandidate(event.ID, "recovered")
			if _, err = f.c.Submit(context.Background(), f.token, claim.Run.ID, credential(claim), SubmitInput{Candidate: &candidate}); err == nil {
				t.Fatal("injected phase did not stop")
			}
			reopened, err := New(f.store, f.dataDir, testSkills(), WithClock(func() time.Time { return *f.now }))
			if err != nil {
				t.Fatalf("reopen: %v", err)
			}
			run := reopened.state.Runs[claim.Run.ID]
			if run.Status != RunSucceeded || len(run.OutputEventIDs) != 1 || len(reopened.state.Inbox) != 1 {
				t.Fatalf("recovery state: %#v inbox=%d", run, len(reopened.state.Inbox))
			}
			snapshot, err := f.store.AutomationSnapshot(f.alice, core.AutomationSnapshotInput{ProjectID: f.project.ID})
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, record := range snapshot.Records {
				if record.Event.Provenance.RunID == run.ID {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("got %d derived events, want one", count)
			}
		})
	}
}

func TestDailySlotsSuggestionsAndNoOutput(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	fall, err := dailySlotForDate(time.Date(2025, 11, 2, 12, 0, 0, 0, newYork), newYork, 1, 30)
	if err != nil || fall.UTC().Format(time.RFC3339) != "2025-11-02T05:30:00Z" {
		t.Fatalf("fall-back first occurrence: %s %v", fall, err)
	}
	spring, err := dailySlotForDate(time.Date(2025, 3, 9, 12, 0, 0, 0, newYork), newYork, 2, 30)
	if err != nil || spring.In(newYork).Format("15:04") != "03:00" {
		t.Fatalf("spring-forward shift: %s %v", spring, err)
	}

	f := newFixture(t)
	local := time.FixedZone("test", 8*60*60)
	*f.now = time.Date(2026, 9, 12, 20, 0, 0, 0, local).UTC()
	daily, err := f.c.CreateInstallation(f.alice, CreateInstallationInput{ProjectID: f.project.ID, RunnerID: f.runner.ID, SkillID: DailyReviewSkill, Trigger: Trigger{Type: "daily", LocalTime: "19:00", Timezone: "Asia/Singapore"}})
	if err != nil {
		t.Fatal(err)
	}
	// Activation occurs after today's slot, so the first dispatch has no run.
	if claim, err := f.c.Dispatch(context.Background(), f.token); err != nil || claim != nil {
		t.Fatalf("unexpected same-day backfill: %#v %v", claim, err)
	}
	*f.now = f.now.Add(72 * time.Hour)
	if claim, err := f.c.Dispatch(context.Background(), f.token); err != nil || claim != nil {
		t.Fatalf("empty daily should be durable no_output without runner: %#v %v", claim, err)
	}
	var noOutput Run
	skipped := 0
	for _, run := range f.c.state.Runs {
		if run.InstallationID == daily.ID {
			if run.Status == RunSkipped && run.FailureCode == "skipped_misfire" {
				skipped++
			} else {
				noOutput = run
			}
		}
	}
	if skipped != 2 || noOutput.Status != RunSucceeded || noOutput.NoOutputReason != "no_relevant_records" {
		t.Fatalf("daily catch-up skipped=%d latest=%#v", skipped, noOutput)
	}
}

func TestDailyCandidateKeepsSuggestionsOutOfSummary(t *testing.T) {
	f := newFixture(t)
	local := f.now.In(time.FixedZone("sg", 8*60*60))
	dueLocal := local.Add(2 * time.Minute)
	daily, err := f.c.CreateInstallation(f.alice, CreateInstallationInput{
		ProjectID: f.project.ID, RunnerID: f.runner.ID, SkillID: DailyReviewSkill,
		Trigger: Trigger{Type: "daily", LocalTime: dueLocal.Format("15:04"), Timezone: "Asia/Singapore"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := "The build passed; consider changing the retry delay."
	source, err := f.store.RecordEvent(f.alice, core.RecordInput{ProjectID: f.project.ID, Content: core.ContentInput{Kind: "text", Text: &text}})
	if err != nil {
		t.Fatal(err)
	}
	*f.now = f.now.Add(3 * time.Minute)
	claim, err := dispatchForInstallation(f.c, f.token, daily.ID)
	if err != nil || claim == nil || len(claim.Task.Inputs) != 1 || claim.Task.Inputs[0].EventID != source.ID {
		t.Fatalf("daily dispatch: %#v %v", claim, err)
	}
	candidate := runner.Candidate{
		SchemaVersion: 1, Outcome: "output", OutputSlot: "daily_review", Kind: "summary",
		Text: "Build passed. Suggestion: make retry instant.", SourceEventIDs: []string{source.ID},
		Items: []runner.CandidateItem{
			{Kind: "progress", Text: "Build passed.", SourceEventIDs: []string{source.ID}},
			{Kind: "suggestion", Text: "Make retry instant.", SourceEventIDs: []string{source.ID}},
		},
	}
	run, err := f.c.Submit(context.Background(), f.token, claim.Run.ID, credential(claim), SubmitInput{Candidate: &candidate})
	if err != nil || run.Status != RunSucceeded || len(run.OutputEventIDs) != 2 {
		t.Fatalf("daily submit: %#v %v", run, err)
	}
	kinds := map[string]core.Event{}
	for _, id := range run.OutputEventIDs {
		event, eventErr := f.store.GetEvent(f.alice, core.EventRef{EventID: id})
		if eventErr != nil {
			t.Fatal(eventErr)
		}
		kinds[event.Provenance.Kind] = event
	}
	summary := kinds[core.ProvenanceSummary]
	suggestion := kinds[core.ProvenanceSuggestion]
	if summary.Content.Text == nil || strings.Contains(*summary.Content.Text, "retry instant") || suggestion.Content.Text == nil || *suggestion.Content.Text != "Make retry instant." {
		t.Fatalf("suggestion leaked into summary: summary=%#v suggestion=%#v", summary, suggestion)
	}
	inbox, err := f.c.ListInbox(f.alice)
	if err != nil || len(inbox.Entries) != 1 || len(inbox.Entries[0].Candidate.Items) != 2 || !strings.Contains(inbox.Entries[0].Candidate.Text, "Suggestion") {
		t.Fatalf("full candidate missing from inbox: %#v %v", inbox, err)
	}
}

func TestLeaseExpiresAfterThirdAttempt(t *testing.T) {
	f := newFixture(t)
	installation := f.audioInstallation(t, "", nil)
	f.recordAudio(t, nil)
	for attempt := 1; attempt <= 3; attempt++ {
		claim, err := dispatchForInstallation(f.c, f.token, installation.ID)
		if err != nil || claim == nil || claim.Run.AttemptCount != attempt {
			t.Fatalf("attempt %d: %#v %v", attempt, claim, err)
		}
		*f.now = f.now.Add(3 * time.Minute)
	}
	claim, err := f.c.Dispatch(context.Background(), f.token)
	if err != nil || claim != nil {
		t.Fatalf("fourth attempt was leased: %#v %v", claim, err)
	}
	var failed Run
	for _, run := range f.c.state.Runs {
		if run.InstallationID == installation.ID {
			failed = run
		}
	}
	if failed.Status != RunFailed || failed.AttemptCount != 3 || failed.FailureCode != "lease_expired" {
		t.Fatalf("retry terminal state: %#v", failed)
	}
}

func TestManualRunDiscoversAutomaticWorkAndSerializesVersions(t *testing.T) {
	f := newFixture(t)
	installation := f.audioInstallation(t, "", nil)
	event := f.recordAudio(t, nil)
	if _, err := f.c.RequestRun(f.alice, RequestRunInput{InstallationID: installation.ID, RequestID: "manual-before-dispatch", SourceEventIDs: []string{event.ID}}); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("manual request did not discover queued automatic run: %v", err)
	}
	claim, err := dispatchForInstallation(f.c, f.token, installation.ID)
	if err != nil || claim == nil || claim.Run.TriggerType != "event" {
		t.Fatalf("discovered auto run: %#v %v", claim, err)
	}
	badCredential := credential(claim)
	badCredential.AttemptID = "../" + claim.AttemptID
	if _, err = f.c.Submit(context.Background(), f.token, claim.Run.ID, badCredential, SubmitInput{Candidate: ptrCandidate(transcriptCandidate(event.ID, "unsafe"))}); err == nil {
		t.Fatal("direct coordinator accepted a path-unsafe attempt id")
	}
	if _, err = f.c.Submit(context.Background(), f.token, claim.Run.ID, credential(claim), SubmitInput{Candidate: ptrCandidate(transcriptCandidate(event.ID, "v1"))}); err != nil {
		t.Fatal(err)
	}
	first, err := f.c.RequestRun(f.alice, RequestRunInput{InstallationID: installation.ID, RequestID: "version-2", SourceEventIDs: []string{event.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.c.RequestRun(f.alice, RequestRunInput{InstallationID: installation.ID, RequestID: "version-3", SourceEventIDs: []string{event.ID}}); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("parallel version run was accepted: %v", err)
	}
	repeated, err := f.c.RequestRun(f.alice, RequestRunInput{InstallationID: installation.ID, RequestID: "version-2", SourceEventIDs: []string{event.ID}})
	if err != nil || repeated.ID != first.ID {
		t.Fatalf("same request id was not idempotent: %#v %v", repeated, err)
	}
}

func TestDailyInputCoverageReportsDedupAndLimit(t *testing.T) {
	start := time.Now().UTC().Add(-time.Hour)
	end := start.Add(2 * time.Hour)
	records := make([]core.AutomationRecord, 0, 130)
	for i := 0; i < 130; i++ {
		text := "entry"
		records = append(records, core.AutomationRecord{Sequence: int64(i + 1), Event: core.Event{ID: newID("evt"), RecordedAt: start.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano), Content: core.Content{Kind: "text", Text: &text}, Provenance: core.Provenance{Kind: core.ProvenanceOriginal}}})
	}
	selection := selectDailyInputs(core.AutomationSnapshot{Records: records}, start, end)
	if len(selection.Inputs) != 128 || selection.Eligible != 130 || selection.Omitted != 2 {
		t.Fatalf("coverage: %#v", selection)
	}
}

func TestDailyInputCoverageDoesNotTreatLineageDedupAsTruncation(t *testing.T) {
	start := time.Now().UTC().Add(-time.Hour)
	end := start.Add(2 * time.Hour)
	originalText := "rough source text"
	transcriptText := "latest transcript"
	otherText := "independent record"
	originalID := "evt_original"
	snapshot := core.AutomationSnapshot{Records: []core.AutomationRecord{
		{Sequence: 1, Event: core.Event{ID: originalID, RecordedAt: start.Add(time.Minute).Format(time.RFC3339Nano), Content: core.Content{Kind: "text", Text: &originalText}, Provenance: core.Provenance{Kind: core.ProvenanceOriginal}}},
		{Sequence: 2, Event: core.Event{ID: "evt_transcript", RecordedAt: start.Add(2 * time.Minute).Format(time.RFC3339Nano), Content: core.Content{Kind: "text", Text: &transcriptText}, Provenance: core.Provenance{Kind: core.ProvenanceTranscript, SourceEventIDs: []string{originalID}, SkillID: AudioTranscribeSkill}}},
		{Sequence: 3, Event: core.Event{ID: "evt_other", RecordedAt: start.Add(3 * time.Minute).Format(time.RFC3339Nano), Content: core.Content{Kind: "text", Text: &otherText}, Provenance: core.Provenance{Kind: core.ProvenanceOriginal}}},
	}}
	selection := selectDailyInputs(snapshot, start, end)
	if selection.Eligible != 2 || selection.Omitted != 0 || len(selection.Inputs) != 2 {
		t.Fatalf("lineage dedup coverage: %#v", selection)
	}
	if selection.Inputs[0].EventID != "evt_transcript" || selection.Inputs[1].EventID != "evt_other" {
		t.Fatalf("expected newest transcript and independent record, got %#v", selection.Inputs)
	}
}

func TestCorruptJournalRefusesReopen(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(f.c.journalPath(f.c.state.Sequence+1), []byte(`{"version":1`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(f.store, f.dataDir, testSkills()); err == nil {
		t.Fatal("truncated journal was accepted")
	}
}
