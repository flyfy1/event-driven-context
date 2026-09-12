package audiotranscribe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"event-driven-context/internal/runner"
	"event-driven-context/internal/v2"
)

const sourceEventID = "0c5478b1-e3f7-4e7d-a927-5a16c0c3bf7e"

func TestAdapterReturnsSourceLinkedTranscriptCandidate(t *testing.T) {
	dir := t.TempDir()
	audioPath := filepath.Join(dir, "recording.m4a")
	audio := []byte("fake audio bytes")
	if err := os.WriteFile(audioPath, audio, 0o600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(audio)
	sha := hex.EncodeToString(hash[:])
	ffprobe := writeExecutable(t, dir, "ffprobe", "#!/bin/sh\nprintf '2.5\\n'\n")
	ffmpeg := writeExecutable(t, dir, "ffmpeg", "#!/bin/sh\nexit 0\n")
	python := writeExecutable(t, dir, "python", "#!/bin/sh\ncat >/dev/null\nprintf '%s' '{\"text\":\"faithful transcript\",\"generation_tokens\":12,\"complete\":true}'\n")
	model := filepath.Join(dir, "model")
	if err := os.Mkdir(model, 0o700); err != nil {
		t.Fatal(err)
	}
	asrScript := filepath.Join(dir, "qwen_asr.py")
	if err := os.WriteFile(asrScript, []byte("# test adapter"), 0o600); err != nil {
		t.Fatal(err)
	}

	adapter := Adapter{RunnerConfig: runner.Config{
		SkillRoot: skillRoot(t), WorkRoot: filepath.Join(dir, "work"), PythonPath: python,
		ASRScriptPath: asrScript, ASRModelPath: model, FFmpegPath: ffmpeg,
		FFprobePath: ffprobe, Timeout: 2 * time.Second,
	}}
	result, err := adapter.Run(context.Background(), validInput(audioPath, sha))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.DerivedEvents) != 1 {
		t.Fatalf("got %d candidates, want one", len(result.DerivedEvents))
	}
	candidate := result.DerivedEvents[0]
	if candidate.Slot != "transcript" || candidate.Kind != "transcript" || candidate.Text != "faithful transcript" || len(candidate.SourceEventIDs) != 1 || candidate.SourceEventIDs[0] != sourceEventID {
		t.Fatalf("unexpected candidate: %#v", candidate)
	}
}

func TestAdapterRejectsUnmatchedFileAndTrailingConfig(t *testing.T) {
	input := validInput("/tmp/unused", strings.Repeat("a", 64))
	input.Files[0].EventID = "70a6fd0b-718e-47b6-9c44-10f780e72064"
	if _, _, _, err := validateRunInput(input); err == nil {
		t.Fatal("unmatched file was accepted")
	}
	input = validInput("/tmp/unused", strings.Repeat("a", 64))
	input.Config = json.RawMessage(`{} {}`)
	if _, _, _, err := validateRunInput(input); err == nil {
		t.Fatal("multiple config JSON values were accepted")
	}
}

func TestAdapterRealQwenASR(t *testing.T) {
	if os.Getenv("EDC_AUDIO_TRANSCRIBE_REAL") != "1" {
		t.Skip("set EDC_AUDIO_TRANSCRIBE_REAL=1 and the EDC_RUNNER_* paths")
	}
	required := func(name string) string {
		value := os.Getenv(name)
		if value == "" {
			t.Fatalf("%s is required", name)
		}
		return value
	}
	audioPath := required("EDC_RUNNER_AUDIO")
	audio, err := os.ReadFile(audioPath)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(audio)
	adapter := Adapter{RunnerConfig: runner.Config{
		SkillRoot: skillRoot(t), WorkRoot: t.TempDir(), Timeout: 5 * time.Minute,
		PythonPath: required("EDC_RUNNER_PYTHON"), ASRScriptPath: required("EDC_RUNNER_ASR_SCRIPT"),
		ASRModelPath: required("EDC_RUNNER_ASR_MODEL"), FFmpegPath: required("EDC_RUNNER_FFMPEG"),
		FFprobePath: required("EDC_RUNNER_FFPROBE"),
	}}
	result, err := adapter.Run(context.Background(), validInput(audioPath, hex.EncodeToString(hash[:])))
	if err != nil {
		t.Fatalf("real adapter run: %v", err)
	}
	if len(result.DerivedEvents) != 1 || strings.TrimSpace(result.DerivedEvents[0].Text) == "" || result.DerivedEvents[0].SourceEventIDs[0] != sourceEventID {
		t.Fatalf("real adapter returned invalid candidate: %#v", result)
	}
	t.Logf("real transcript candidate validated (%d UTF-8 bytes): %.120s", len(result.DerivedEvents[0].Text), result.DerivedEvents[0].Text)
}

func validInput(audioPath, sha string) RunInput {
	return RunInput{
		RunID: "run-1", ProjectID: "project-1", PluginID: PluginID, PluginVersion: PluginVersion,
		ConfigRevision: 1, Generation: 1, Config: json.RawMessage(`{"language":"auto","prompt":"Keep hesitations."}`),
		Events: []AuthorizedEvent{{ID: sourceEventID, Type: "note", Content: v2.EventContent{
			Kind: "file", FileID: "file-1", MediaType: "audio/mp4", SHA256: strings.ToUpper(sha),
		}}},
		Files: []AuthorizedFile{{EventID: sourceEventID, Path: audioPath, SHA256: sha, MediaType: "audio/mp4"}},
	}
}

func skillRoot(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "skills"))
}

func writeExecutable(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
