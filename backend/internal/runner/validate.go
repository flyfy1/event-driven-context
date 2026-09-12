package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxUserPromptBytes = 16 << 10
	maxDailyInputBytes = 1 << 20
)

func validateTask(task Task) error {
	if task.RunID == "" || task.ProjectID == "" || task.SkillID == "" || task.SkillVersion == "" || task.SkillDigest == "" {
		return fmt.Errorf("run, project, skill, version, and digest are required")
	}
	if len(task.RunID) > 256 || len(task.ProjectID) > 256 || !utf8.ValidString(task.RunID) || !utf8.ValidString(task.ProjectID) {
		return fmt.Errorf("run and project IDs must be valid UTF-8 up to 256 bytes")
	}
	if len(task.UserPrompt) > maxUserPromptBytes || !utf8.ValidString(task.UserPrompt) {
		return fmt.Errorf("user prompt must be valid UTF-8 up to %d bytes", maxUserPromptBytes)
	}
	if len(task.Language) > 64 || !utf8.ValidString(task.Language) {
		return fmt.Errorf("language must be valid UTF-8 up to 64 bytes")
	}
	if decoded, err := hex.DecodeString(task.SkillDigest); err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("skill digest must be a SHA-256 hex value")
	}
	if task.SkillVersion != FixedSkillVersion {
		return fmt.Errorf("unsupported skill version %q", task.SkillVersion)
	}
	if task.SkillID != AudioTranscribeSkill && task.SkillID != DailyReviewSkill {
		return fmt.Errorf("unsupported skill %q", task.SkillID)
	}
	if len(task.Inputs) == 0 {
		return fmt.Errorf("at least one authorized input is required")
	}
	seen := map[string]bool{}
	totalTextBytes := 0
	for _, input := range task.Inputs {
		if input.EventID == "" || len(input.EventID) > 256 || !utf8.ValidString(input.EventID) || seen[input.EventID] {
			return fmt.Errorf("input event IDs must be non-empty and unique")
		}
		seen[input.EventID] = true
		hasText := input.Text != ""
		if hasText {
			if !utf8.ValidString(input.Text) {
				return fmt.Errorf("input %q text must be valid UTF-8", input.EventID)
			}
			totalTextBytes += len(input.Text)
			if totalTextBytes > maxDailyInputBytes {
				return fmt.Errorf("authorized text exceeds %d bytes", maxDailyInputBytes)
			}
		}
		hasAudio := input.AudioPath != "" || input.AudioSHA != ""
		if hasText == hasAudio {
			return fmt.Errorf("input %q must contain exactly one of text or audio", input.EventID)
		}
		if hasAudio && (input.AudioPath == "" || input.AudioSHA == "") {
			return fmt.Errorf("audio input %q requires path and sha256", input.EventID)
		}
		if hasAudio {
			if !utf8.ValidString(input.AudioPath) {
				return fmt.Errorf("audio input %q path must be valid UTF-8", input.EventID)
			}
			if decoded, err := hex.DecodeString(input.AudioSHA); err != nil || len(decoded) != sha256.Size {
				return fmt.Errorf("audio input %q sha256 must be a SHA-256 hex value", input.EventID)
			}
		}
	}
	if task.SkillID == AudioTranscribeSkill {
		if len(task.Inputs) != 1 || task.Inputs[0].AudioPath == "" {
			return fmt.Errorf("audio transcription requires exactly one audio input")
		}
	}
	if task.SkillID == DailyReviewSkill {
		for _, input := range task.Inputs {
			if input.Text == "" {
				return fmt.Errorf("daily review accepts text inputs only")
			}
		}
	}
	return nil
}

func loadSkillSnapshot(task Task, config Config) ([]byte, error) {
	if config.SkillRoot == "" {
		return nil, &RunnerError{Kind: CapabilityError, Err: fmt.Errorf("skill root is required")}
	}
	dir := filepath.Join(config.SkillRoot, task.SkillID)
	files, digest, err := readSkillPackage(dir)
	if err != nil {
		return nil, &RunnerError{Kind: CapabilityError, Err: fmt.Errorf("load fixed skill: %w", err)}
	}
	if !strings.EqualFold(digest, task.SkillDigest) {
		return nil, &RunnerError{Kind: IntegrityError, Err: fmt.Errorf("skill digest mismatch")}
	}
	skillText, ok := files["SKILL.md"]
	if !ok {
		return nil, &RunnerError{Kind: CapabilityError, Err: fmt.Errorf("fixed skill has no SKILL.md")}
	}
	return skillText, nil
}

// SkillDigest hashes relative paths and bytes in lexical order. Symlinks and
// non-regular files are rejected so a fixed package cannot escape its folder.
func SkillDigest(dir string) (string, error) {
	_, digest, err := readSkillPackage(dir)
	return digest, err
}

// readSkillPackage reads every package file once, then hashes and returns that
// same immutable snapshot so execution never reopens a verified mutable path.
func readSkillPackage(dir string) (map[string][]byte, string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dir {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill package contains symlink")
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("skill package contains non-regular file")
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	if len(files) == 0 {
		return nil, "", fmt.Errorf("skill package is empty")
	}
	sort.Strings(files)
	hash := sha256.New()
	snapshot := make(map[string][]byte, len(files))
	for _, relative := range files {
		fullPath := filepath.Join(dir, filepath.FromSlash(relative))
		contents, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, "", err
		}
		snapshot[relative] = contents
		_, _ = io.WriteString(hash, relative)
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(contents)
		_, _ = hash.Write([]byte{0})
	}
	return snapshot, hex.EncodeToString(hash.Sum(nil)), nil
}

func validateCandidate(task Task, candidate Candidate) error {
	if candidate.SchemaVersion != 1 || candidate.Outcome != "output" || strings.TrimSpace(candidate.Text) == "" {
		return fmt.Errorf("candidate envelope or text is invalid")
	}
	allowed := map[string]bool{}
	for _, input := range task.Inputs {
		allowed[input.EventID] = true
	}
	validateSources := func(ids []string) error {
		if len(ids) == 0 {
			return fmt.Errorf("source event IDs are required")
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if !allowed[id] {
				return fmt.Errorf("source event %q was not authorized", id)
			}
			if seen[id] {
				return fmt.Errorf("duplicate source event %q", id)
			}
			seen[id] = true
		}
		return nil
	}
	if err := validateSources(candidate.SourceEventIDs); err != nil {
		return err
	}
	switch task.SkillID {
	case AudioTranscribeSkill:
		if candidate.Kind != "transcript" || candidate.OutputSlot != "transcript" || len(candidate.Items) != 0 {
			return fmt.Errorf("transcript candidate has wrong kind or output slot")
		}
	case DailyReviewSkill:
		if candidate.Kind != "summary" || candidate.OutputSlot != "daily_review" {
			return fmt.Errorf("daily review candidate has wrong kind or output slot")
		}
		if len(candidate.Items) == 0 {
			return fmt.Errorf("daily review requires at least one categorized item")
		}
		for _, item := range candidate.Items {
			switch item.Kind {
			case "progress", "decision", "open_question", "suggestion":
			default:
				return fmt.Errorf("invalid daily review item kind %q", item.Kind)
			}
			if strings.TrimSpace(item.Text) == "" {
				return fmt.Errorf("daily review item text is required")
			}
			if err := validateSources(item.SourceEventIDs); err != nil {
				return err
			}
		}
	}
	return nil
}
