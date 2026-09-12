package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"event-driven-context/internal/runner"
	audiotranscribe "event-driven-context/plugins/audio-transcribe"
)

const maxInputBytes = 64 << 10

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "audio-transcribe-adapter:", err)
		os.Exit(1)
	}
}

func run() error {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, maxInputBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxInputBytes {
		return fmt.Errorf("input exceeds %d bytes", maxInputBytes)
	}
	var input audiotranscribe.RunInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&input); err != nil {
		return fmt.Errorf("decode input: %w", err)
	}
	if err = decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("input must contain one JSON object")
	}
	timeout := 10 * time.Minute
	if value := os.Getenv("EDC_RUNNER_EXECUTION_TIMEOUT"); value != "" {
		timeout, err = time.ParseDuration(value)
		if err != nil || timeout <= 0 || timeout > 30*time.Minute {
			return fmt.Errorf("EDC_RUNNER_EXECUTION_TIMEOUT must be between 1ns and 30m")
		}
	}
	maxOutput := 64 << 10
	if value := os.Getenv("EDC_RUNNER_MAX_OUTPUT_BYTES"); value != "" {
		maxOutput, err = strconv.Atoi(value)
		if err != nil || maxOutput < 1 || maxOutput > 1<<20 {
			return fmt.Errorf("EDC_RUNNER_MAX_OUTPUT_BYTES must be between 1 and 1048576")
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	result, err := (audiotranscribe.Adapter{RunnerConfig: runner.Config{
		SkillRoot: os.Getenv("EDC_RUNNER_SKILL_ROOT"), WorkRoot: os.Getenv("EDC_RUNNER_WORK_ROOT"),
		PythonPath: os.Getenv("EDC_RUNNER_PYTHON"), ASRScriptPath: os.Getenv("EDC_RUNNER_ASR_SCRIPT"),
		ASRModelPath: os.Getenv("EDC_RUNNER_ASR_MODEL"), FFmpegPath: os.Getenv("EDC_RUNNER_FFMPEG"),
		FFprobePath: os.Getenv("EDC_RUNNER_FFPROBE"), Timeout: timeout, MaxOutputBytes: maxOutput,
	}}).Run(ctx, input)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(result)
}
