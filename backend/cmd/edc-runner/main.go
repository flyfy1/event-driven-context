package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	runnerpkg "event-driven-context/internal/runner"
)

const help = `edc-runner — execute fixed Event-driven Context automation tasks

Usage:
  edc-runner [global flags] tick
  edc-runner [global flags] watch [--interval 30s] [--max-runtime 10m]

The runner token is read from EDC_RUNNER_TOKEN or a private token file. It is
never accepted as a command-line flag. watch always has a finite runtime and
does not install cron or another scheduler.
`

type options struct {
	server            string
	tokenFile         string
	stateDir          string
	skillRoot         string
	pythonPath        string
	asrScriptPath     string
	asrModelPath      string
	ffmpegPath        string
	ffprobePath       string
	codexPath         string
	executionTimeout  time.Duration
	requestTimeout    time.Duration
	heartbeatInterval time.Duration
	maxOutputBytes    int
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "edc-runner:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	baseDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	opts := options{}
	global := flag.NewFlagSet("edc-runner", flag.ContinueOnError)
	global.SetOutput(os.Stderr)
	global.StringVar(&opts.server, "server", envDefault("EDC_RUNNER_SERVER", "http://127.0.0.1:8080"), "server origin URL")
	global.StringVar(&opts.tokenFile, "token-file", envDefault("EDC_RUNNER_TOKEN_FILE", filepath.Join(baseDir, "event-driven-context", "runner-token")), "private runner token file")
	global.StringVar(&opts.stateDir, "state-dir", envDefault("EDC_RUNNER_STATE_DIR", filepath.Join(baseDir, "event-driven-context", "runner-state")), "private runner state directory")
	global.StringVar(&opts.skillRoot, "skill-root", os.Getenv("EDC_RUNNER_SKILL_ROOT"), "fixed skill package directory")
	global.StringVar(&opts.pythonPath, "python", os.Getenv("EDC_RUNNER_PYTHON"), "Qwen Python executable")
	global.StringVar(&opts.asrScriptPath, "asr-script", os.Getenv("EDC_RUNNER_ASR_SCRIPT"), "Qwen adapter script")
	global.StringVar(&opts.asrModelPath, "asr-model", os.Getenv("EDC_RUNNER_ASR_MODEL"), "local Qwen model directory")
	global.StringVar(&opts.ffmpegPath, "ffmpeg", os.Getenv("EDC_RUNNER_FFMPEG"), "ffmpeg executable")
	global.StringVar(&opts.ffprobePath, "ffprobe", os.Getenv("EDC_RUNNER_FFPROBE"), "ffprobe executable")
	global.StringVar(&opts.codexPath, "codex", os.Getenv("EDC_RUNNER_CODEX"), "Codex executable")
	global.DurationVar(&opts.executionTimeout, "execution-timeout", durationEnv("EDC_RUNNER_EXECUTION_TIMEOUT", 10*time.Minute), "maximum model execution time")
	global.DurationVar(&opts.requestTimeout, "request-timeout", durationEnv("EDC_RUNNER_REQUEST_TIMEOUT", 30*time.Second), "maximum API request time")
	global.DurationVar(&opts.heartbeatInterval, "heartbeat-interval", durationEnv("EDC_RUNNER_HEARTBEAT_INTERVAL", 10*time.Second), "lease heartbeat interval")
	global.IntVar(&opts.maxOutputBytes, "max-output-bytes", intEnv("EDC_RUNNER_MAX_OUTPUT_BYTES", 64<<10), "maximum structured candidate bytes")
	global.Usage = func() { fmt.Fprint(os.Stderr, help); global.PrintDefaults() }
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	args = global.Args()
	if len(args) == 0 || args[0] == "help" {
		fmt.Print(help)
		return nil
	}
	if opts.executionTimeout <= 0 || opts.requestTimeout <= 0 || opts.heartbeatInterval <= 0 || opts.maxOutputBytes <= 0 {
		return fmt.Errorf("timeouts, heartbeat interval, and output limit must be positive")
	}
	token, err := readRunnerToken(opts.tokenFile)
	if err != nil {
		return err
	}
	client, err := newRunnerClient(opts.server, token, opts.requestTimeout)
	if err != nil {
		return err
	}
	state, release, err := openState(opts.stateDir, client.baseURL, token)
	if err != nil {
		return err
	}
	defer release()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "tick":
		if len(args) != 1 {
			return fmt.Errorf("tick takes no arguments")
		}
		result, err := tick(ctx, client, state, opts)
		if err != nil {
			return err
		}
		return printJSON(result)
	case "watch":
		watchFlags := flag.NewFlagSet("watch", flag.ContinueOnError)
		watchFlags.SetOutput(os.Stderr)
		interval := watchFlags.Duration("interval", 30*time.Second, "delay between dispatch checks")
		maxRuntime := watchFlags.Duration("max-runtime", 10*time.Minute, "finite watch duration")
		if err := watchFlags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if watchFlags.NArg() != 0 || *interval <= 0 || *maxRuntime <= 0 {
			return fmt.Errorf("watch interval and max runtime must be positive")
		}
		return watch(ctx, client, state, opts, *interval, *maxRuntime)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func watch(parent context.Context, client *runnerClient, state *localState, opts options, interval, maxRuntime time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, maxRuntime)
	defer cancel()
	for {
		result, err := tick(ctx, client, state, opts)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if !temporary(err) {
				return err
			}
			_ = printJSON(map[string]string{"status": "temporary_error", "code": stableErrorCode(err)})
		} else {
			_ = printJSON(result)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func runnerConfig(opts options, workRoot string) runnerpkg.Config {
	return runnerpkg.Config{
		SkillRoot: opts.skillRoot, WorkRoot: workRoot,
		PythonPath: opts.pythonPath, ASRScriptPath: opts.asrScriptPath,
		ASRModelPath: opts.asrModelPath, FFmpegPath: opts.ffmpegPath,
		FFprobePath: opts.ffprobePath, CodexPath: opts.codexPath,
		Timeout: opts.executionTimeout, MaxOutputBytes: opts.maxOutputBytes,
	}
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return -1
	}
	return parsed
}

func intEnv(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return -1
	}
	return parsed
}

func readRunnerToken(path string) (string, error) {
	if token := strings.TrimSpace(os.Getenv("EDC_RUNNER_TOKEN")); token != "" {
		if strings.ContainsAny(token, "\r\n\t ") {
			return "", fmt.Errorf("EDC_RUNNER_TOKEN must contain one token without whitespace")
		}
		return token, nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("read runner token file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("runner token file must be a private regular file (mode 0600)")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read runner token file: %w", err)
	}
	if len(contents) > 4096 {
		return "", fmt.Errorf("runner token file is too large")
	}
	token := strings.TrimSpace(string(contents))
	if token == "" || strings.ContainsAny(token, "\r\n\t ") {
		return "", fmt.Errorf("runner token file must contain one non-empty token")
	}
	return token, nil
}
