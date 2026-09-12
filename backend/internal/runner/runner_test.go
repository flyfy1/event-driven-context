package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode"
)

func TestExecuteDailyReviewUsesFixedArgumentsAndSkillSnapshot(t *testing.T) {
	skillRoot := copyTestSkills(t)
	digest := testSkillDigest(t, skillRoot, DailyReviewSkill)
	fakeDir := t.TempDir()
	result := `{"schema_version":1,"outcome":"output","output_slot":"daily_review","kind":"summary","text":"Daily review","source_event_ids":["evt-1","evt-2"],"items":[{"kind":"progress","text":"Build passed","source_event_ids":["evt-1"]},{"kind":"suggestion","text":"Consider a narrow rollout","source_event_ids":["evt-2"]}]}`
	mustWrite(t, filepath.Join(fakeDir, "result.json"), result, 0o600)
	skillPath := filepath.Join(skillRoot, DailyReviewSkill, "SKILL.md")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ "${OPENAI_API_KEY+x}" = x ] || [ "${EDC_RUNNER_TOKEN+x}" = x ]; then exit 71; fi
printf '%%s\n' "$@" > "$script_dir/args.txt"
cat > "$script_dir/prompt.txt"
printf 'mutated after digest validation\n' > %s
out=""
previous=""
for argument in "$@"; do
  if [ "$previous" = "-o" ]; then out="$argument"; fi
  previous="$argument"
done
cat "$script_dir/result.json" > "$out"
`, shellQuote(skillPath))
	codexPath := writeExecutable(t, fakeDir, "fake-codex", script)
	t.Setenv("OPENAI_API_KEY", "must-not-be-forwarded")
	t.Setenv("EDC_RUNNER_TOKEN", "must-not-be-forwarded")

	task := dailyTask(digest)
	candidate, err := Execute(context.Background(), task, Config{
		SkillRoot: skillRoot,
		WorkRoot:  t.TempDir(),
		CodexPath: codexPath,
		Timeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if candidate.Kind != "summary" || len(candidate.Items) != 2 {
		t.Fatalf("unexpected candidate: %#v", candidate)
	}

	argsBytes, err := os.ReadFile(filepath.Join(fakeDir, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(argsBytes)), "\n")
	wantPrefix := []string{
		"exec", "--ignore-user-config", "--ignore-rules", "--ephemeral",
		"--skip-git-repo-check", "--sandbox", "read-only", "--json",
		"-m", "gpt-5.6-sol", "-c", `model_reasoning_effort="high"`,
		"-c", `web_search="disabled"`, "--output-schema",
	}
	if len(args) < len(wantPrefix) || strings.Join(args[:len(wantPrefix)], "\x00") != strings.Join(wantPrefix, "\x00") {
		t.Fatalf("unexpected Codex argument prefix: %#v", args)
	}
	if args[len(args)-1] != "-" {
		t.Fatalf("prompt must be supplied on stdin, got final argument %q", args[len(args)-1])
	}
	disabled := map[string]int{}
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "--disable" {
			disabled[args[index+1]]++
		}
	}
	for _, feature := range disabledCodexFeatures {
		if disabled[feature] != 1 {
			t.Errorf("feature %q disabled %d times", feature, disabled[feature])
		}
	}
	promptBytes, err := os.ReadFile(filepath.Join(fakeDir, "prompt.txt"))
	if err != nil {
		t.Fatal(err)
	}
	prompt := string(promptBytes)
	if !strings.Contains(prompt, "# Daily Review") || strings.Contains(prompt, "mutated after digest validation") {
		t.Fatalf("execution did not use the verified skill snapshot")
	}
	if !strings.Contains(prompt, `"event_id":"evt-1"`) || !strings.Contains(prompt, `"event_id":"evt-2"`) {
		t.Fatalf("authorized inputs missing from prompt")
	}
}

func TestExecuteRejectsInvalidAndForgedDailyOutput(t *testing.T) {
	tests := []struct {
		name   string
		result string
	}{
		{name: "invalid JSON", result: `{not-json`},
		{name: "forged source", result: `{"schema_version":1,"outcome":"output","output_slot":"daily_review","kind":"summary","text":"Forged","source_event_ids":["evt-forged"],"items":[]}`},
		{name: "empty categorized items", result: `{"schema_version":1,"outcome":"output","output_slot":"daily_review","kind":"summary","text":"Empty","source_event_ids":["evt-1"],"items":[]}`},
		{name: "missing explicit items", result: `{"schema_version":1,"outcome":"output","output_slot":"daily_review","kind":"summary","text":"Missing items","source_event_ids":["evt-1"]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			skillRoot := copyTestSkills(t)
			fakeDir := t.TempDir()
			mustWrite(t, filepath.Join(fakeDir, "result.json"), test.result, 0o600)
			codexPath := writeExecutable(t, fakeDir, "fake-codex", fakeCodexScript())
			_, err := Execute(context.Background(), dailyTask(testSkillDigest(t, skillRoot, DailyReviewSkill)), Config{
				SkillRoot: skillRoot, WorkRoot: t.TempDir(), CodexPath: codexPath, Timeout: 2 * time.Second,
			})
			if !IsKind(err, InvalidOutput) {
				t.Fatalf("got %v, want invalid output", err)
			}
		})
	}
}

func TestRunProcessKillsProcessGroupOnTimeoutAndNormalExit(t *testing.T) {
	tests := []struct {
		name      string
		timeout   time.Duration
		exitDelay string
		wantKind  ErrorKind
	}{
		{name: "timeout", timeout: 500 * time.Millisecond, exitDelay: "wait", wantKind: TimeoutError},
		{name: "normal exit", timeout: 2 * time.Second, exitDelay: "exit 0", wantKind: ""},
		{name: "failed exit", timeout: 2 * time.Second, exitDelay: "exit 3", wantKind: ExecutionError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			pidPath := filepath.Join(dir, "child.pid")
			script := `#!/bin/sh
set -eu
/bin/sleep 30 </dev/null >/dev/null 2>&1 &
child=$!
printf '%s' "$child" > ` + shellQuote(pidPath) + `
` + test.exitDelay + "\n"
			path := writeExecutable(t, dir, "process-tree", script)
			_, err := runProcess(context.Background(), test.timeout, path, nil, "", dir)
			if test.wantKind == "" && err != nil {
				t.Fatalf("runProcess: %v", err)
			}
			if test.wantKind != "" && !IsKind(err, test.wantKind) {
				t.Fatalf("got %v, want %s", err, test.wantKind)
			}
			pidBytes, readErr := os.ReadFile(pidPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			pid, parseErr := strconv.Atoi(string(pidBytes))
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			deadline := time.Now().Add(time.Second)
			for processAlive(pid) && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if processAlive(pid) {
				t.Fatalf("child process %d survived runner cleanup", pid)
			}
		})
	}
}

func TestExecuteASRValidatesInputAndOutput(t *testing.T) {
	skillRoot := copyTestSkills(t)
	digest := testSkillDigest(t, skillRoot, AudioTranscribeSkill)
	dir := t.TempDir()
	audioPath := filepath.Join(dir, "fixture.m4a")
	mustWrite(t, audioPath, "fake audio bytes", 0o600)
	audioHash := sha256.Sum256([]byte("fake audio bytes"))
	ffmpegPath := writeExecutable(t, dir, "ffmpeg", "#!/bin/sh\nexit 0\n")
	ffprobePath := writeExecutable(t, dir, "ffprobe", "#!/bin/sh\nprintf '2.9\\n'\n")
	pythonPath := writeExecutable(t, dir, "python", `#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ "${OPENAI_API_KEY+x}" = x ]; then exit 71; fi
printf '%s\n' "$@" > "$script_dir/python-args.txt"
cat > "$script_dir/python-request.json"
printf '{"text":"faithful transcript","generation_tokens":12,"complete":true}'
`)
	modelPath := filepath.Join(dir, "model")
	if err := os.Mkdir(modelPath, 0o700); err != nil {
		t.Fatal(err)
	}
	adapterPath := filepath.Join(dir, "qwen_asr.py")
	mustWrite(t, adapterPath, "# adapter placeholder", 0o600)
	t.Setenv("OPENAI_API_KEY", "must-not-be-forwarded")
	task := Task{
		RunID: "run-asr", ProjectID: "project-1", SkillID: AudioTranscribeSkill,
		SkillVersion: FixedSkillVersion, SkillDigest: digest, UserPrompt: "Keep hesitations", Language: "zh-CN",
		Inputs: []Input{{EventID: "evt-audio", AudioPath: audioPath, AudioSHA: hex.EncodeToString(audioHash[:])}},
	}
	config := Config{
		SkillRoot: skillRoot, WorkRoot: t.TempDir(), PythonPath: pythonPath,
		ASRScriptPath: adapterPath, ASRModelPath: modelPath, FFmpegPath: ffmpegPath,
		FFprobePath: ffprobePath, Timeout: 2 * time.Second,
	}
	candidate, err := Execute(context.Background(), task, config)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if candidate.Text != "faithful transcript" || candidate.SourceEventIDs[0] != "evt-audio" {
		t.Fatalf("unexpected candidate: %#v", candidate)
	}
	requestBytes, err := os.ReadFile(filepath.Join(dir, "python-request.json"))
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(requestBytes, &request); err != nil {
		t.Fatal(err)
	}
	if request["language"] != "Chinese" || request["ffmpeg_path"] != ffmpegPath || request["max_tokens"] != float64(asrMaxTokens) {
		t.Fatalf("unexpected ASR request: %#v", request)
	}
	if request["audio_path"] == audioPath || !strings.Contains(request["system_prompt"].(string), "# Audio Transcribe") {
		t.Fatalf("ASR did not use its verified private copy and fixed skill")
	}

	task.Inputs[0].AudioSHA = strings.Repeat("0", 64)
	if _, err := Execute(context.Background(), task, config); !IsKind(err, IntegrityError) {
		t.Fatalf("SHA mismatch got %v, want integrity error", err)
	}
}

func TestExecuteASRRejectsInvalidDurationAndOutputBudget(t *testing.T) {
	for _, duration := range []string{"NaN", "+Inf", "601"} {
		t.Run(duration, func(t *testing.T) {
			task, config := fakeASRTask(t, duration, `{"text":"ok","generation_tokens":1,"complete":true}`)
			_, err := Execute(context.Background(), task, config)
			if !IsKind(err, InvalidTask) {
				t.Fatalf("got %v, want invalid task", err)
			}
		})
	}
	t.Run("output budget", func(t *testing.T) {
		task, config := fakeASRTask(t, "2", `{"text":"this transcript exceeds a tiny output budget","generation_tokens":1,"complete":true}`)
		config.MaxOutputBytes = 32
		_, err := Execute(context.Background(), task, config)
		if !IsKind(err, InvalidOutput) {
			t.Fatalf("got %v, want invalid output", err)
		}
	})
}

func TestRealAdapters(t *testing.T) {
	if os.Getenv("EDC_RUNNER_REAL") != "1" {
		t.Skip("set EDC_RUNNER_REAL=1 and the documented EDC_RUNNER_* paths")
	}
	required := func(name string) string {
		value := os.Getenv(name)
		if value == "" {
			t.Fatalf("%s is required", name)
		}
		return value
	}
	_, sourceFile, _, _ := runtime.Caller(0)
	packageDir := filepath.Dir(sourceFile)
	backendDir := filepath.Clean(filepath.Join(packageDir, "..", ".."))
	skillRoot := filepath.Join(backendDir, "skills")
	audioPath := required("EDC_RUNNER_AUDIO")
	audioBytes, err := os.ReadFile(audioPath)
	if err != nil {
		t.Fatal(err)
	}
	audioSHA := sha256.Sum256(audioBytes)
	baseConfig := Config{
		SkillRoot: skillRoot, WorkRoot: t.TempDir(), Timeout: 5 * time.Minute,
		PythonPath: required("EDC_RUNNER_PYTHON"), ASRScriptPath: filepath.Join(packageDir, "qwen_asr.py"),
		ASRModelPath: required("EDC_RUNNER_ASR_MODEL"), FFmpegPath: required("EDC_RUNNER_FFMPEG"),
		FFprobePath: required("EDC_RUNNER_FFPROBE"), CodexPath: required("EDC_RUNNER_CODEX"),
	}
	audioDigest := testSkillDigest(t, skillRoot, AudioTranscribeSkill)
	transcript, err := Execute(context.Background(), Task{
		RunID: "real-asr-validation", ProjectID: "local-validation", SkillID: AudioTranscribeSkill,
		SkillVersion: FixedSkillVersion, SkillDigest: audioDigest, UserPrompt: "忠实转录，保留原意。", Language: "Chinese",
		Inputs: []Input{{EventID: "fixture-audio", AudioPath: audioPath, AudioSHA: hex.EncodeToString(audioSHA[:])}},
	}, baseConfig)
	if err != nil {
		t.Fatalf("real ASR: %v", err)
	}
	if transcript.Kind != "transcript" || strings.TrimSpace(transcript.Text) == "" {
		t.Fatalf("real ASR returned an invalid candidate")
	}
	t.Logf("real ASR candidate validated (%d UTF-8 bytes)", len(transcript.Text))
	if expected := os.Getenv("EDC_RUNNER_EXPECT_TRANSCRIPT"); expected != "" {
		actualNormalized := normalizeTranscriptEvidence(transcript.Text)
		expectedNormalized := normalizeTranscriptEvidence(expected)
		if !strings.Contains(actualNormalized, expectedNormalized) && !strings.Contains(expectedNormalized, actualNormalized) {
			t.Fatalf("real ASR transcript did not match the expected fixture semantics")
		}
		// This is printed only when the caller explicitly supplies the expected
		// synthetic fixture text. Never enable it for user recordings.
		t.Logf("synthetic fixture transcript: %q", transcript.Text)
	}

	dailyDigest := testSkillDigest(t, skillRoot, DailyReviewSkill)
	review, err := Execute(context.Background(), Task{
		RunID: "real-daily-validation", ProjectID: "local-validation", SkillID: DailyReviewSkill,
		SkillVersion: FixedSkillVersion, SkillDigest: dailyDigest, UserPrompt: "Use concise English.", Language: "English",
		Inputs: []Input{
			{EventID: "evt-progress", Text: "The upload flow passed desktop and narrow-screen testing today."},
			{EventID: "evt-decision", Text: "Decision: audio files remain limited to ten minutes."},
			{EventID: "evt-question", Text: "Open question: which retry interval should the runner use?"},
		},
	}, baseConfig)
	if err != nil {
		t.Fatalf("real Codex daily review: %v", err)
	}
	if review.Kind != "summary" || len(review.Items) == 0 {
		t.Fatalf("real daily review returned an invalid candidate")
	}
	t.Logf("real Codex candidate validated (%d items, %d UTF-8 bytes)", len(review.Items), len(review.Text))
}

func dailyTask(digest string) Task {
	return Task{
		RunID: "run-daily", ProjectID: "project-1", SkillID: DailyReviewSkill,
		SkillVersion: FixedSkillVersion, SkillDigest: digest, UserPrompt: "Be concise", Language: "English",
		Inputs: []Input{{EventID: "evt-1", Text: "The build passed."}, {EventID: "evt-2", Text: "A narrow rollout might help."}},
	}
}

func fakeCodexScript() string {
	return `#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cat >/dev/null
out=""
previous=""
for argument in "$@"; do
  if [ "$previous" = "-o" ]; then out="$argument"; fi
  previous="$argument"
done
cat "$script_dir/result.json" > "$out"
`
}

func fakeASRTask(t *testing.T, duration, output string) (Task, Config) {
	t.Helper()
	skillRoot := copyTestSkills(t)
	dir := t.TempDir()
	audioPath := filepath.Join(dir, "audio.m4a")
	mustWrite(t, audioPath, "audio", 0o600)
	hash := sha256.Sum256([]byte("audio"))
	ffmpeg := writeExecutable(t, dir, "ffmpeg", "#!/bin/sh\nexit 0\n")
	ffprobe := writeExecutable(t, dir, "ffprobe", "#!/bin/sh\nprintf '%s\\n' "+shellQuote(duration)+"\n")
	python := writeExecutable(t, dir, "python", "#!/bin/sh\ncat >/dev/null\nprintf '%s' "+shellQuote(output)+"\n")
	model := filepath.Join(dir, "model")
	if err := os.Mkdir(model, 0o700); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "adapter.py")
	mustWrite(t, adapter, "# fake", 0o600)
	return Task{
			RunID: "run", ProjectID: "project", SkillID: AudioTranscribeSkill, SkillVersion: FixedSkillVersion,
			SkillDigest: testSkillDigest(t, skillRoot, AudioTranscribeSkill),
			Inputs:      []Input{{EventID: "event", AudioPath: audioPath, AudioSHA: hex.EncodeToString(hash[:])}},
		}, Config{
			SkillRoot: skillRoot, WorkRoot: t.TempDir(), PythonPath: python, ASRScriptPath: adapter,
			ASRModelPath: model, FFmpegPath: ffmpeg, FFprobePath: ffprobe, Timeout: 2 * time.Second,
		}
}

func copyTestSkills(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, _ := runtime.Caller(0)
	backendDir := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	sourceRoot := filepath.Join(backendDir, "skills")
	destinationRoot := t.TempDir()
	for _, skillID := range []string{AudioTranscribeSkill, DailyReviewSkill} {
		contents, err := os.ReadFile(filepath.Join(sourceRoot, skillID, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(destinationRoot, skillID)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return destinationRoot
}

func testSkillDigest(t *testing.T, root, skillID string) string {
	t.Helper()
	digest, err := SkillDigest(filepath.Join(root, skillID))
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func writeExecutable(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	mustWrite(t, path, contents, 0o700)
	return path
}

func mustWrite(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	status, statusErr := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if statusErr == nil {
		fields := strings.Fields(string(status))
		return len(fields) < 3 || fields[2] != "Z"
	}
	if output, psErr := exec.Command("/bin/ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output(); psErr == nil {
		return !strings.HasPrefix(strings.TrimSpace(string(output)), "Z")
	}
	return true
}

func normalizeTranscriptEvidence(value string) string {
	value = strings.NewReplacer("30000", "三万", "30,000", "三万", "3万", "三万").Replace(strings.ToLower(value))
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, value)
}
