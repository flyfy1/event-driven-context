package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"event-driven-context/internal/automation"
	"event-driven-context/internal/core"
	"event-driven-context/internal/runner"
)

type automationHTTPFixture struct {
	store       *core.Store
	coordinator *automation.Coordinator
	handler     http.Handler
	alice       core.User
	aliceToken  string
	bobToken    string
	project     core.Project
	runner      automation.RunnerRegistration
}

func newAutomationHTTPFixture(t *testing.T) *automationHTTPFixture {
	t.Helper()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	store, err := core.Open(filepath.Join(root, "edc.db"), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	password := "test-password-long-enough"
	alice, err := store.Register(context.Background(), core.Credentials{Username: "alice", Email: "api-automation-alice@example.invalid", Password: password})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := store.Register(context.Background(), core.Credentials{Username: "bob", Email: "api-automation-bob@example.invalid", Password: password})
	if err != nil {
		t.Fatal(err)
	}
	aliceLogin, err := store.Login(context.Background(), core.Credentials{Username: alice.Username, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	bobLogin, err := store.Login(context.Background(), core.Credentials{Username: bob.Username, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	aliceContext := core.WithUser(context.Background(), alice.ID)
	project, err := store.CreateProject(aliceContext, core.ProjectInput{Name: "private"})
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := automation.New(store, dataDir, []automation.SkillDescriptor{
		{ID: runner.AudioTranscribeSkill, Version: runner.FixedSkillVersion, Digest: strings.Repeat("a", 64)},
		{ID: runner.DailyReviewSkill, Version: runner.FixedSkillVersion, Digest: strings.Repeat("b", 64)},
	})
	if err != nil {
		t.Fatal(err)
	}
	registration, err := coordinator.RegisterRunner(aliceContext, automation.RegisterRunnerInput{Name: "local", Capabilities: []string{"audio_transcription", "daily_review"}})
	if err != nil {
		t.Fatal(err)
	}
	return &automationHTTPFixture{
		store: store, coordinator: coordinator, handler: HandlerWithConfig(store, Config{Automation: coordinator}),
		alice: alice, aliceToken: aliceLogin.Token, bobToken: bobLogin.Token, project: project, runner: registration,
	}
}

func (f *automationHTTPFixture) installAudio(t *testing.T) automation.Installation {
	t.Helper()
	installation, err := f.coordinator.CreateInstallation(core.WithUser(context.Background(), f.alice.ID), automation.CreateInstallationInput{
		ProjectID: f.project.ID, RunnerID: f.runner.Runner.ID, SkillID: runner.AudioTranscribeSkill, Trigger: automation.Trigger{Type: "event"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return installation
}

func (f *automationHTTPFixture) audio(t *testing.T, key string) core.Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "core", "testdata", "fixture.m4a"))
	if err != nil {
		t.Fatal(err)
	}
	event, err := f.store.RecordMediaEvent(core.WithUser(context.Background(), f.alice.ID), core.MediaRecordInput{
		ProjectID: f.project.ID, Filename: "recording.m4a", MediaType: "audio/mp4", Data: data, IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func requestJSON(t *testing.T, handler http.Handler, method, path, token string, body any, headers ...[2]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for _, header := range headers {
		req.Header.Add(header[0], header[1])
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func decodeHTTP[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %d %q: %v", response.Code, response.Body.String(), err)
	}
	return out
}

func attemptHeaders(claim automation.Claim) [][2]string {
	return [][2]string{{"X-EDC-Attempt-ID", claim.AttemptID}, {"X-EDC-Fencing-Token", strconv.FormatUint(claim.FencingToken, 10)}}
}

func TestAutomationHTTPScopesHeadersPauseAndIdempotentSubmit(t *testing.T) {
	f := newAutomationHTTPFixture(t)
	installation := f.installAudio(t)

	if response := requestJSON(t, f.handler, http.MethodPost, "/v1/runner/dispatch", f.runner.Token, nil); response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("empty dispatch = %d %q", response.Code, response.Body.String())
	}
	if response := requestJSON(t, f.handler, http.MethodPost, "/v1/runner/dispatch", f.aliceToken, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("user bearer dispatched: %d %q", response.Code, response.Body.String())
	}
	for _, path := range []string{"/v1/me", "/v1/projects"} {
		if response := requestJSON(t, f.handler, http.MethodGet, path, f.runner.Token, nil); response.Code != http.StatusUnauthorized {
			t.Fatalf("runner bearer accessed %s: %d", path, response.Code)
		}
	}

	event := f.audio(t, "http-audio-1")
	dispatch := requestJSON(t, f.handler, http.MethodPost, "/v1/runner/dispatch", f.runner.Token, nil)
	if dispatch.Code != http.StatusOK {
		t.Fatalf("dispatch = %d %q", dispatch.Code, dispatch.Body.String())
	}
	claim := decodeHTTP[automation.Claim](t, dispatch)
	if claim.Run.InstallationID != installation.ID || claim.Task.Inputs[0].AudioPath != "" {
		t.Fatalf("unexpected claim: %#v", claim)
	}
	if response := requestJSON(t, f.handler, http.MethodGet, "/v1/automation/runs/"+claim.Run.ID, f.bobToken, nil); response.Code != http.StatusNotFound {
		t.Fatalf("other user read run: %d %q", response.Code, response.Body.String())
	}
	if response := requestJSON(t, f.handler, http.MethodPost, "/v1/runner/runs/"+claim.Run.ID+"/heartbeat", f.runner.Token, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("missing attempt headers: %d %q", response.Code, response.Body.String())
	}
	duplicate := attemptHeaders(claim)
	duplicate = append(duplicate, [2]string{"X-EDC-Attempt-ID", claim.AttemptID})
	if response := requestJSON(t, f.handler, http.MethodPost, "/v1/runner/runs/"+claim.Run.ID+"/heartbeat", f.runner.Token, nil, duplicate...); response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate attempt header: %d %q", response.Code, response.Body.String())
	}
	for _, invalid := range []string{"../candidate", "/tmp/candidate", "att_" + strings.Repeat("a", 64), "att_" + strings.Repeat("g", 32)} {
		headers := [][2]string{{"X-EDC-Attempt-ID", invalid}, {"X-EDC-Fencing-Token", "1"}}
		if response := requestJSON(t, f.handler, http.MethodPost, "/v1/runner/runs/"+claim.Run.ID+"/heartbeat", f.runner.Token, nil, headers...); response.Code != http.StatusBadRequest {
			t.Fatalf("unsafe attempt %q: %d %q", invalid, response.Code, response.Body.String())
		}
	}
	pause := requestJSON(t, f.handler, http.MethodPost, "/v1/automation/installations/"+installation.ID+"/actions", f.aliceToken, map[string]any{"action": "pause"})
	if pause.Code != http.StatusOK {
		t.Fatalf("pause = %d %q", pause.Code, pause.Body.String())
	}
	inputPath := fmt.Sprintf("/v1/runner/runs/%s/input-files/%s/content", claim.Run.ID, claim.InputFiles[0].FileID)
	if response := requestJSON(t, f.handler, http.MethodGet, inputPath, f.runner.Token, nil, attemptHeaders(claim)...); response.Code != http.StatusConflict {
		t.Fatalf("paused input read: %d %q", response.Code, response.Body.String())
	}
	candidate := runner.Candidate{SchemaVersion: 1, Outcome: "output", OutputSlot: "transcript", Kind: "transcript", Text: "blocked", SourceEventIDs: []string{event.ID}}
	if response := requestJSON(t, f.handler, http.MethodPost, "/v1/runner/runs/"+claim.Run.ID+"/submit", f.runner.Token, automation.SubmitInput{Candidate: &candidate}, attemptHeaders(claim)...); response.Code != http.StatusConflict {
		t.Fatalf("paused submit: %d %q", response.Code, response.Body.String())
	}

	active := f.installAudio(t)
	event = f.audio(t, "http-audio-2")
	dispatch = requestJSON(t, f.handler, http.MethodPost, "/v1/runner/dispatch", f.runner.Token, nil)
	claim = decodeHTTP[automation.Claim](t, dispatch)
	if claim.Run.InstallationID != active.ID {
		t.Fatalf("wrong active claim: %#v", claim.Run)
	}
	candidate = runner.Candidate{SchemaVersion: 1, Outcome: "output", OutputSlot: "transcript", Kind: "transcript", Text: "faithful", SourceEventIDs: []string{event.ID}}
	submitPath := "/v1/runner/runs/" + claim.Run.ID + "/submit"
	first := requestJSON(t, f.handler, http.MethodPost, submitPath, f.runner.Token, automation.SubmitInput{Candidate: &candidate}, attemptHeaders(claim)...)
	if first.Code != http.StatusOK {
		t.Fatalf("submit = %d %q", first.Code, first.Body.String())
	}
	firstRun := decodeHTTP[automation.Run](t, first)
	repeated := requestJSON(t, f.handler, http.MethodPost, submitPath, f.runner.Token, automation.SubmitInput{Candidate: &candidate}, attemptHeaders(claim)...)
	if repeated.Code != http.StatusOK || decodeHTTP[automation.Run](t, repeated).ID != firstRun.ID {
		t.Fatalf("repeat submit = %d %q", repeated.Code, repeated.Body.String())
	}
	different := candidate
	different.Text = "different"
	if response := requestJSON(t, f.handler, http.MethodPost, submitPath, f.runner.Token, automation.SubmitInput{Candidate: &different}, attemptHeaders(claim)...); response.Code != http.StatusConflict {
		t.Fatalf("different candidate = %d %q", response.Code, response.Body.String())
	}

	inboxResponse := requestJSON(t, f.handler, http.MethodGet, "/v1/inbox", f.aliceToken, nil)
	if inboxResponse.Code != http.StatusOK {
		t.Fatalf("inbox = %d %q", inboxResponse.Code, inboxResponse.Body.String())
	}
	inbox := decodeHTTP[automation.InboxEntries](t, inboxResponse)
	if len(inbox.Entries) != 1 || inbox.Entries[0].Candidate.Text != "faithful" {
		t.Fatalf("inbox wrapper: %#v", inbox)
	}
	if response := requestJSON(t, f.handler, http.MethodPost, "/v1/inbox/"+inbox.Entries[0].ID+"/read", f.bobToken, nil); response.Code != http.StatusNotFound {
		t.Fatalf("other user read inbox: %d %q", response.Code, response.Body.String())
	}
	if response := requestJSON(t, f.handler, http.MethodPost, "/v1/inbox/"+inbox.Entries[0].ID+"/read", f.aliceToken, nil); response.Code != http.StatusOK {
		t.Fatalf("read inbox = %d %q", response.Code, response.Body.String())
	}
	for path, key := range map[string]string{
		"/v1/automation/runners": "runners", "/v1/automation/installations": "installations",
		"/v1/automation/runs": "runs", "/v1/inbox": "entries",
	} {
		response := requestJSON(t, f.handler, http.MethodGet, path, f.aliceToken, nil)
		var wrapper map[string]json.RawMessage
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &wrapper) != nil || wrapper[key] == nil {
			t.Fatalf("wrapper %s missing %s: %d %q", path, key, response.Code, response.Body.String())
		}
	}
}
