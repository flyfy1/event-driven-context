package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

const (
	AudioTranscribeSkill = "audio-transcribe"
	DailyReviewSkill     = "daily-review"
	FixedSkillVersion    = "1.0.0"
)

type Input struct {
	EventID   string `json:"event_id"`
	Text      string `json:"text,omitempty"`
	AudioPath string `json:"audio_path,omitempty"`
	AudioSHA  string `json:"audio_sha256,omitempty"`
}

type Task struct {
	RunID        string  `json:"run_id"`
	ProjectID    string  `json:"project_id"`
	SkillID      string  `json:"skill_id"`
	SkillVersion string  `json:"skill_version"`
	SkillDigest  string  `json:"skill_digest"`
	UserPrompt   string  `json:"user_prompt,omitempty"`
	Language     string  `json:"language,omitempty"`
	Inputs       []Input `json:"inputs"`
}

type CandidateItem struct {
	Kind           string   `json:"kind"`
	Text           string   `json:"text"`
	SourceEventIDs []string `json:"source_event_ids"`
}

type Candidate struct {
	SchemaVersion  int             `json:"schema_version"`
	Outcome        string          `json:"outcome"`
	OutputSlot     string          `json:"output_slot"`
	Kind           string          `json:"kind"`
	Text           string          `json:"text"`
	SourceEventIDs []string        `json:"source_event_ids"`
	Items          []CandidateItem `json:"items,omitempty"`
}

type Config struct {
	SkillRoot      string
	WorkRoot       string
	PythonPath     string
	ASRScriptPath  string
	ASRModelPath   string
	FFmpegPath     string
	FFprobePath    string
	CodexPath      string
	Timeout        time.Duration
	MaxOutputBytes int
}

type ErrorKind string

const (
	InvalidTask     ErrorKind = "invalid_task"
	CapabilityError ErrorKind = "capability"
	IntegrityError  ErrorKind = "integrity"
	TimeoutError    ErrorKind = "timeout"
	ExecutionError  ErrorKind = "execution"
	InvalidOutput   ErrorKind = "invalid_output"
)

type RunnerError struct {
	Kind ErrorKind
	Err  error
}

func (e *RunnerError) Error() string { return fmt.Sprintf("runner %s: %v", e.Kind, e.Err) }
func (e *RunnerError) Unwrap() error { return e.Err }
func IsKind(err error, kind ErrorKind) bool {
	var target *RunnerError
	return errors.As(err, &target) && target.Kind == kind
}

func Execute(ctx context.Context, task Task, config Config) (Candidate, error) {
	if err := validateTask(task); err != nil {
		return Candidate{}, &RunnerError{Kind: InvalidTask, Err: err}
	}
	skillText, err := loadSkillSnapshot(task, config)
	if err != nil {
		return Candidate{}, err
	}
	var candidate Candidate
	switch task.SkillID {
	case AudioTranscribeSkill:
		candidate, err = executeASR(ctx, task, config, skillText)
	case DailyReviewSkill:
		candidate, err = executeDailyReview(ctx, task, config, skillText)
	default:
		err = &RunnerError{Kind: CapabilityError, Err: fmt.Errorf("unsupported skill %q", task.SkillID)}
	}
	if err != nil {
		return Candidate{}, err
	}
	if err := validateCandidate(task, candidate); err != nil {
		return Candidate{}, &RunnerError{Kind: InvalidOutput, Err: err}
	}
	maxBytes := config.MaxOutputBytes
	if maxBytes <= 0 {
		maxBytes = 64 << 10
	}
	textBytes := len(candidate.Text)
	if !utf8.ValidString(candidate.Text) {
		return Candidate{}, &RunnerError{Kind: InvalidOutput, Err: fmt.Errorf("candidate text is not valid UTF-8")}
	}
	for _, item := range candidate.Items {
		if !utf8.ValidString(item.Text) {
			return Candidate{}, &RunnerError{Kind: InvalidOutput, Err: fmt.Errorf("candidate item text is not valid UTF-8")}
		}
		textBytes += len(item.Text)
	}
	encoded, encodeErr := json.Marshal(candidate)
	if encodeErr != nil || len(encoded) > maxBytes || textBytes > maxBytes {
		return Candidate{}, &RunnerError{Kind: InvalidOutput, Err: fmt.Errorf("candidate exceeds %d-byte output budget", maxBytes)}
	}
	return candidate, nil
}
