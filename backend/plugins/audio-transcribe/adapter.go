package audiotranscribe

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"event-driven-context/internal/runner"
	"event-driven-context/internal/v2"
	"github.com/google/uuid"
)

const (
	PluginID      = "audio-transcribe"
	PluginVersion = "1.0.0"
)

type RunInput struct {
	RunID          string            `json:"run_id"`
	ProjectID      string            `json:"project_id"`
	PluginID       string            `json:"plugin_id"`
	PluginVersion  string            `json:"plugin_version"`
	ConfigRevision int64             `json:"config_revision"`
	Generation     int64             `json:"generation"`
	Events         []AuthorizedEvent `json:"events"`
	Files          []AuthorizedFile  `json:"files"`
	PreviousState  json.RawMessage   `json:"previous_state,omitempty"`
	Config         json.RawMessage   `json:"config"`
}

type AuthorizedEvent struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Content v2.EventContent `json:"content"`
}

type AuthorizedFile struct {
	EventID   string `json:"event_id"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type"`
}

type Config struct {
	Language string `json:"language,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
}

type Result struct {
	DerivedEvents []DerivedCandidate `json:"derived_events"`
}

type DerivedCandidate struct {
	Slot           string   `json:"slot"`
	Kind           string   `json:"kind"`
	Text           string   `json:"text"`
	SourceEventIDs []string `json:"source_event_ids"`
}

type Adapter struct {
	RunnerConfig runner.Config
}

func (a Adapter) Run(ctx context.Context, input RunInput) (Result, error) {
	config, event, file, err := validateRunInput(input)
	if err != nil {
		return Result{}, err
	}
	digest, err := runner.SkillDigest(filepath.Join(a.RunnerConfig.SkillRoot, PluginID))
	if err != nil {
		return Result{}, fmt.Errorf("load audio-transcribe skill: %w", err)
	}
	candidate, err := runner.Execute(ctx, runner.Task{
		RunID: input.RunID, ProjectID: input.ProjectID,
		SkillID: PluginID, SkillVersion: runner.FixedSkillVersion, SkillDigest: digest,
		UserPrompt: config.Prompt, Language: config.Language,
		Inputs: []runner.Input{{EventID: event.ID, AudioPath: file.Path, AudioSHA: file.SHA256}},
	}, a.RunnerConfig)
	if err != nil {
		return Result{}, err
	}
	return Result{DerivedEvents: []DerivedCandidate{{
		Slot: "transcript", Kind: "transcript", Text: candidate.Text,
		SourceEventIDs: append([]string(nil), candidate.SourceEventIDs...),
	}}}, nil
}

func validateRunInput(input RunInput) (Config, AuthorizedEvent, AuthorizedFile, error) {
	if input.RunID == "" || input.ProjectID == "" || input.PluginID != PluginID || input.PluginVersion != PluginVersion || input.ConfigRevision < 1 || input.Generation < 1 {
		return Config{}, AuthorizedEvent{}, AuthorizedFile{}, fmt.Errorf("run identity, plugin version, config revision, and generation are required")
	}
	if len(input.Events) != 1 || len(input.Files) != 1 {
		return Config{}, AuthorizedEvent{}, AuthorizedFile{}, fmt.Errorf("audio-transcribe requires one authorized event and file")
	}
	event, file := input.Events[0], input.Files[0]
	parsedID, err := uuid.Parse(event.ID)
	if err != nil || parsedID.String() != strings.ToLower(event.ID) || (event.Type != "note" && event.Type != "log") {
		return Config{}, AuthorizedEvent{}, AuthorizedFile{}, fmt.Errorf("authorized source event is invalid")
	}
	if event.Content.Kind != "file" || event.Content.FileID == "" || event.Content.MediaType != file.MediaType || !strings.EqualFold(event.Content.SHA256, file.SHA256) || file.EventID != event.ID || file.Path == "" || !strings.HasPrefix(file.MediaType, "audio/") {
		return Config{}, AuthorizedEvent{}, AuthorizedFile{}, fmt.Errorf("authorized file does not match its source event")
	}
	sha, err := hex.DecodeString(file.SHA256)
	if err != nil || len(sha) != 32 {
		return Config{}, AuthorizedEvent{}, AuthorizedFile{}, fmt.Errorf("authorized file sha256 is invalid")
	}
	config := Config{}
	rawConfig := input.Config
	if len(rawConfig) == 0 {
		rawConfig = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(rawConfig))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&config); err != nil {
		return Config{}, AuthorizedEvent{}, AuthorizedFile{}, fmt.Errorf("audio-transcribe config is invalid")
	}
	if err = decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Config{}, AuthorizedEvent{}, AuthorizedFile{}, fmt.Errorf("audio-transcribe config is invalid")
	}
	if len(config.Language) > 64 || len(config.Prompt) > 16<<10 || !utf8.ValidString(config.Language) || !utf8.ValidString(config.Prompt) {
		return Config{}, AuthorizedEvent{}, AuthorizedFile{}, fmt.Errorf("audio-transcribe config exceeds its limits")
	}
	return config, event, file, nil
}
