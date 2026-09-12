package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"event-driven-context/internal/automation"
	"event-driven-context/internal/runner"
)

func TestTickExecutesAndSubmitsDailyCandidate(t *testing.T) {
	skillRoot := repositorySkillRoot(t)
	claim := dailyClaim(t, skillRoot)
	var submitted runner.Candidate
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		assertRunnerRequest(t, request, request.URL.Path != "/v1/runner/dispatch")
		switch request.URL.Path {
		case "/v1/runner/dispatch":
			writeJSON(response, claim)
		case "/v1/runner/runs/run-1/heartbeat":
			writeJSON(response, activeRun(claim))
		case "/v1/runner/runs/run-1/submit":
			var input automation.SubmitInput
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input.Candidate == nil {
				t.Error("candidate is required")
			} else {
				submitted = *input.Candidate
			}
			writeJSON(response, activeRun(claim))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client := testClient(t, server.URL, time.Second)
	state, release, err := openState(privateTempDir(t), server.URL, "runner-token")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	result, err := tick(context.Background(), client, state, dailyOptions(t, skillRoot, "valid"))
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if result.Status != "candidate_submitted" || submitted.Kind != "summary" || len(submitted.Items) != 1 {
		t.Fatalf("unexpected result=%#v candidate=%#v", result, submitted)
	}
	paths, _ := state.pending()
	if len(paths) != 0 {
		t.Fatalf("submitted candidate remained pending: %v", paths)
	}
}

func TestTickPersistsCandidateAcrossSubmitTimeout(t *testing.T) {
	skillRoot := repositorySkillRoot(t)
	claim := dailyClaim(t, skillRoot)
	var dispatches atomic.Int32
	var submissions atomic.Int32
	var heartbeats atomic.Int32
	var allowSubmit atomic.Bool
	submitStarted := make(chan struct{}, 1)
	releaseFirstSubmit := make(chan struct{})
	firstSubmitReleased := false
	defer func() {
		if !firstSubmitReleased {
			close(releaseFirstSubmit)
		}
	}()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/runner/dispatch":
			dispatches.Add(1)
			writeJSON(response, claim)
		case "/v1/runner/runs/run-1/heartbeat":
			heartbeats.Add(1)
			writeJSON(response, activeRun(claim))
		case "/v1/runner/runs/run-1/submit":
			submissions.Add(1)
			if !allowSubmit.Load() {
				select {
				case submitStarted <- struct{}{}:
				default:
				}
				<-releaseFirstSubmit
				return
			}
			writeJSON(response, activeRun(claim))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client := testClient(t, server.URL, 10*time.Second)
	client.http.Transport = &pathTimeoutTransport{base: http.DefaultTransport, path: "/v1/runner/runs/run-1/submit", timeout: 2 * time.Second}
	state, release, err := openState(privateTempDir(t), server.URL, "runner-token")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	opts := dailyOptions(t, skillRoot, "valid")
	opts.executionTimeout = 10 * time.Second
	if _, err = tick(context.Background(), client, state, opts); err == nil {
		t.Fatal("submit timeout should be returned")
	}
	var networkError net.Error
	if !errors.Is(err, context.DeadlineExceeded) && (!errors.As(err, &networkError) || !networkError.Timeout()) {
		t.Fatalf("got %v, want a transport deadline error", err)
	}
	select {
	case <-submitStarted:
	default:
		t.Fatal("timeout occurred before the candidate reached submit")
	}
	paths, _ := state.pending()
	if len(paths) != 1 {
		t.Fatalf("got %d pending candidates, want 1", len(paths))
	}
	close(releaseFirstSubmit)
	firstSubmitReleased = true
	allowSubmit.Store(true)
	result, err := tick(context.Background(), client, state, opts)
	if err != nil {
		t.Fatalf("retry tick: %v", err)
	}
	if result.Status != "candidate_submitted" || dispatches.Load() != 1 || submissions.Load() != 2 || heartbeats.Load() != 1 {
		t.Fatalf("unexpected retry result=%#v dispatches=%d submissions=%d heartbeats=%d", result, dispatches.Load(), submissions.Load(), heartbeats.Load())
	}
	paths, _ = state.pending()
	if len(paths) != 0 {
		t.Fatalf("pending candidate was not removed")
	}
}

type pathTimeoutTransport struct {
	base    http.RoundTripper
	path    string
	timeout time.Duration
}

func (t *pathTimeoutTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path != t.path {
		return t.base.RoundTrip(request)
	}
	ctx, cancel := context.WithTimeout(request.Context(), t.timeout)
	defer cancel()
	return t.base.RoundTrip(request.Clone(ctx))
}

func TestTickCancelsExecutionWhenLeaseIsLost(t *testing.T) {
	skillRoot := repositorySkillRoot(t)
	claim := dailyClaim(t, skillRoot)
	var heartbeats atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/runner/dispatch":
			writeJSON(response, claim)
		case "/v1/runner/runs/run-1/heartbeat":
			if heartbeats.Add(1) == 1 {
				writeJSON(response, activeRun(claim))
				return
			}
			response.WriteHeader(http.StatusConflict)
			writeJSON(response, map[string]any{"error": map[string]string{"code": "stale_attempt"}})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client := testClient(t, server.URL, time.Second)
	state, release, err := openState(privateTempDir(t), server.URL, "runner-token")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	opts := dailyOptions(t, skillRoot, "slow")
	opts.heartbeatInterval = 20 * time.Millisecond
	opts.executionTimeout = 10 * time.Second
	started := time.Now()
	_, err = tick(context.Background(), client, state, opts)
	var remote *remoteError
	if !errors.As(err, &remote) || remote.Code != "stale_attempt" {
		t.Fatalf("got %v, want stale attempt", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("lease loss took %s to cancel execution", elapsed)
	}
}

func TestTickDownloadsPrivateAudioAndCleansAttempt(t *testing.T) {
	skillRoot := repositorySkillRoot(t)
	audio := []byte("synthetic audio bytes")
	audioHash := sha256.Sum256(audio)
	digest, err := runner.SkillDigest(filepath.Join(skillRoot, runner.AudioTranscribeSkill))
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	claim := automation.Claim{
		Run:       automation.Run{ID: "run-audio", ProjectID: "project-1", SkillID: runner.AudioTranscribeSkill, SkillVersion: runner.FixedSkillVersion, SkillDigest: digest, Inputs: []automation.RunInput{{EventID: "evt-audio", FileID: "file-1"}}, CurrentAttempt: &automation.Attempt{ID: "att-audio", FencingToken: 4, LeaseExpiresAt: expires}},
		AttemptID: "att-audio", FencingToken: 4, LeaseExpiresAt: expires,
		Task:       automationTask("run-audio", "project-1", runner.AudioTranscribeSkill, digest, []runner.Input{{EventID: "evt-audio", AudioSHA: hex.EncodeToString(audioHash[:])}}),
		InputFiles: []automation.ClaimInputFile{{EventID: "evt-audio", FileID: "file-1", Filename: "voice.m4a", MediaType: "audio/mp4", SizeBytes: len(audio), SHA256: hex.EncodeToString(audioHash[:])}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/runner/dispatch":
			writeJSON(response, claim)
		case "/v1/runner/runs/run-audio/heartbeat", "/v1/runner/runs/run-audio/submit":
			writeJSON(response, activeRun(claim))
		case "/v1/runner/runs/run-audio/input-files/file-1/content":
			response.Header().Set("Content-Length", fmt.Sprint(len(audio)))
			_, _ = response.Write(audio)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	dir := privateTempDir(t)
	evidencePath := filepath.Join(dir, "audio-path.txt")
	pythonScript := filepath.Join(dir, "fake-asr.py")
	pythonSource := fmt.Sprintf(`import json, os, sys
r=json.load(sys.stdin)
p=r['audio_path']
assert os.stat(p).st_mode & 0o077 == 0
open(%q,'w').write(p)
json.dump({'text':'transcript','generation_tokens':1,'complete':True},sys.stdout)
`, evidencePath)
	writeFile(t, pythonScript, pythonSource, 0o600)
	ffmpeg := writeExecutable(t, dir, "ffmpeg", "#!/bin/sh\nexit 0\n")
	ffprobe := writeExecutable(t, dir, "ffprobe", "#!/bin/sh\nprintf '1.0\\n'\n")
	model := filepath.Join(dir, "model")
	if err = os.Mkdir(model, 0o700); err != nil {
		t.Fatal(err)
	}
	opts := options{skillRoot: skillRoot, pythonPath: "/usr/bin/python3", asrScriptPath: pythonScript, asrModelPath: model, ffmpegPath: ffmpeg, ffprobePath: ffprobe, executionTimeout: 10 * time.Second, requestTimeout: 5 * time.Second, heartbeatInterval: time.Hour, maxOutputBytes: 64 << 10}
	client := testClient(t, server.URL, 5*time.Second)
	state, release, err := openState(filepath.Join(dir, "state"), server.URL, "runner-token")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err = tick(context.Background(), client, state, opts); err != nil {
		t.Fatalf("audio tick: %v", err)
	}
	pathBytes, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(string(pathBytes)); !os.IsNotExist(err) {
		t.Fatalf("downloaded input was not cleaned: %v", err)
	}
}

func TestClaimMustMatchFixedRunInputs(t *testing.T) {
	skillRoot := repositorySkillRoot(t)
	claim := dailyClaim(t, skillRoot)
	claim.Run.Inputs[0].EventID = "different-event"
	if err := validateClaim(claim); err == nil {
		t.Fatal("mismatched fixed run input was accepted")
	}
}

func TestStateAndTokenArePrivateAndRunnerScoped(t *testing.T) {
	root := privateTempDir(t)
	stateA, releaseA, err := openState(root, "http://127.0.0.1:8080", "token-a")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseA()
	stateB, releaseB, err := openState(root, "http://127.0.0.1:8080", "token-b")
	if err != nil {
		t.Fatal(err)
	}
	releaseB()
	if stateA.dir == stateB.dir || stateA.fingerprint == stateB.fingerprint {
		t.Fatal("runner identities shared a state namespace")
	}
	if _, _, err = openState(root, "http://127.0.0.1:8080", "token-a"); err == nil {
		t.Fatal("second runner acquired the same identity lock")
	}
	tokenFile := filepath.Join(root, "token")
	writeFile(t, tokenFile, "secret-token\n", 0o644)
	t.Setenv("EDC_RUNNER_TOKEN", "")
	if _, err = readRunnerToken(tokenFile); err == nil {
		t.Fatal("broad token file permissions were accepted")
	}
	if err = os.Chmod(tokenFile, 0o600); err != nil {
		t.Fatal(err)
	}
	if token, err := readRunnerToken(tokenFile); err != nil || token != "secret-token" {
		t.Fatalf("private token file: token=%q err=%v", token, err)
	}
}

func dailyClaim(t *testing.T, skillRoot string) automation.Claim {
	t.Helper()
	digest, err := runner.SkillDigest(filepath.Join(skillRoot, runner.DailyReviewSkill))
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	return automation.Claim{
		Run:       automation.Run{ID: "run-1", ProjectID: "project-1", SkillID: runner.DailyReviewSkill, SkillVersion: runner.FixedSkillVersion, SkillDigest: digest, Inputs: []automation.RunInput{{EventID: "evt-1"}}, CurrentAttempt: &automation.Attempt{ID: "att-1", FencingToken: 7, LeaseExpiresAt: expires}},
		AttemptID: "att-1", FencingToken: 7, LeaseExpiresAt: expires,
		Task: automationTask("run-1", "project-1", runner.DailyReviewSkill, digest, []runner.Input{{EventID: "evt-1", Text: "The build passed."}}),
	}
}

func automationTask(runID, projectID, skillID, digest string, inputs []runner.Input) runner.Task {
	return runner.Task{RunID: runID, ProjectID: projectID, SkillID: skillID, SkillVersion: runner.FixedSkillVersion, SkillDigest: digest, Language: "English", Inputs: inputs}
}

func activeRun(claim automation.Claim) automation.Run {
	run := claim.Run
	run.CurrentAttempt = &automation.Attempt{ID: claim.AttemptID, FencingToken: claim.FencingToken, LeaseExpiresAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)}
	return run
}

func dailyOptions(t *testing.T, skillRoot, mode string) options {
	t.Helper()
	dir := privateTempDir(t)
	result := `{"schema_version":1,"outcome":"output","output_slot":"daily_review","kind":"summary","text":"Review","source_event_ids":["evt-1"],"items":[{"kind":"progress","text":"Build passed","source_event_ids":["evt-1"]}]}`
	script := `#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cat >/dev/null
if [ -f "$script_dir/slow" ]; then sleep 30; fi
out=""
previous=""
for argument in "$@"; do
  if [ "$previous" = "-o" ]; then out="$argument"; fi
  previous="$argument"
done
cat "$script_dir/result.json" > "$out"
`
	writeFile(t, filepath.Join(dir, "result.json"), result, 0o600)
	if mode == "slow" {
		writeFile(t, filepath.Join(dir, "slow"), "1", 0o600)
	}
	codex := writeExecutable(t, dir, "codex", script)
	return options{skillRoot: skillRoot, codexPath: codex, executionTimeout: 10 * time.Second, requestTimeout: 5 * time.Second, heartbeatInterval: time.Hour, maxOutputBytes: 64 << 10}
}

func testClient(t *testing.T, server string, timeout time.Duration) *runnerClient {
	t.Helper()
	client, err := newRunnerClient(server, "runner-token", timeout)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func assertRunnerRequest(t *testing.T, request *http.Request, attempt bool) {
	t.Helper()
	if request.Header.Get("Authorization") != "Bearer runner-token" {
		t.Error("runner authorization header missing")
	}
	if attempt && (request.Header.Get("X-EDC-Attempt-ID") == "" || request.Header.Get("X-EDC-Fencing-Token") == "") {
		t.Error("attempt fencing headers missing")
	}
}

func writeJSON(response http.ResponseWriter, value any) {
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(value)
}

func repositorySkillRoot(t *testing.T) string {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "skills"))
}

func privateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeExecutable(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	writeFile(t, path, contents, 0o700)
	return path
}

func writeFile(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}
