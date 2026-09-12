package capture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"event-driven-context/internal/v2"
	"github.com/google/uuid"
)

var captureNamespace = uuid.MustParse("b5dcd8e4-a153-5a4e-a14a-b2b9c65cc3d7")

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)("(?:api[_-]?key|access[_-]?token|auth[_-]?token|authorization|password|secret)"\s*:\s*)"(?:\\.|[^"\\])*"`),
	regexp.MustCompile(`(?i)\b(Bearer\s+)[A-Za-z0-9._~+/=-]{12,}`),
	regexp.MustCompile(`\b(sk-(?:proj-)?[A-Za-z0-9_-]{12,})\b`),
	regexp.MustCompile(`\b(gh[opusr]_[A-Za-z0-9]{20,})\b`),
	regexp.MustCompile(`\b(AKIA[0-9A-Z]{16})\b`),
	regexp.MustCompile(`(?i)(\b(?:api[_-]?key|access[_-]?token|auth[_-]?token|authorization|password|secret)\b\s*[:=]\s*)[^\s,;]+`),
}

func DecodeHookInput(r io.Reader) (HookInput, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxHookInput+1))
	if err != nil {
		return HookInput{}, err
	}
	if len(b) > MaxHookInput {
		return HookInput{}, fmt.Errorf("hook input exceeds 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	var in HookInput
	if err = decoder.Decode(&in); err != nil {
		return HookInput{}, fmt.Errorf("decode hook input: %w", err)
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return HookInput{}, fmt.Errorf("hook input contains trailing data")
	}
	return in, nil
}

func NormalizeCodex(in HookInput) (normalizedHook, error) {
	in.SessionID = strings.TrimSpace(in.SessionID)
	in.TurnID = strings.TrimSpace(in.TurnID)
	in.CWD = strings.TrimSpace(in.CWD)
	if in.SessionID == "" || in.CWD == "" {
		return normalizedHook{}, fmt.Errorf("Codex hook requires session_id and cwd")
	}
	if len(in.SessionID) > 512 || len(in.TurnID) > 512 || len(in.HookEventName) > 64 {
		return normalizedHook{}, fmt.Errorf("Codex hook identifier is too large")
	}

	kind, text, stablePart := "", "", ""
	result := normalizedHook{Input: in}
	metadata := map[string]json.RawMessage{
		"client_event": mustJSON(in.HookEventName),
	}
	if in.Model != "" {
		model, _ := truncateUTF8(redactText(in.Model), MaxHookField)
		metadata["model"] = mustJSON(model)
	}
	switch in.HookEventName {
	case "SessionStart":
		if !oneOf(in.Source, "startup", "resume", "clear", "compact") {
			return normalizedHook{}, fmt.Errorf("invalid Codex SessionStart source")
		}
		kind, text = "session_started", "Codex session started ("+in.Source+")."
		result.LoadContext = true
		metadata["source"] = mustJSON(in.Source)
	case "UserPromptSubmit":
		if in.TurnID == "" || in.Prompt == "" {
			return normalizedHook{}, fmt.Errorf("Codex UserPromptSubmit requires turn_id and prompt")
		}
		kind, text, stablePart = "user_message", in.Prompt, in.TurnID
	case "Stop":
		if in.TurnID == "" {
			return normalizedHook{}, fmt.Errorf("Codex Stop requires turn_id")
		}
		kind, text, stablePart = "assistant_message", in.LastAssistantMessage, in.TurnID
		if text == "" {
			text = "Codex turn stopped without an assistant message."
			metadata["message_missing"] = mustJSON(true)
		}
		result.Reminder = !in.StopHookActive
		metadata["stop_hook_active"] = mustJSON(in.StopHookActive)
	case "PreCompact":
		if in.TurnID == "" || !oneOf(in.Trigger, "manual", "auto") {
			return normalizedHook{}, fmt.Errorf("Codex PreCompact requires turn_id and a valid trigger")
		}
		kind, text, stablePart = "context_compacting", "Codex context compaction requested ("+in.Trigger+").", in.TurnID+"\x00"+in.Trigger
		metadata["trigger"] = mustJSON(in.Trigger)
	case "SessionEnd":
		if in.Reason != "other" {
			return normalizedHook{}, fmt.Errorf("invalid Codex SessionEnd reason")
		}
		kind, text = "session_ended", "Codex session ended (other)."
		metadata["reason"] = mustJSON(in.Reason)
	default:
		return normalizedHook{}, fmt.Errorf("unsupported Codex hook event %q", in.HookEventName)
	}

	text = redactText(text)
	var truncated bool
	text, truncated = truncateUTF8(text, MaxHookField)
	metadata["kind"] = mustJSON(kind)
	if truncated {
		metadata["truncated"] = mustJSON([]string{"content.text"})
	}
	source := map[string]json.RawMessage{
		"channel":    mustJSON("hook"),
		"client":     mustJSON(Codex),
		"session_id": mustJSON(in.SessionID),
	}
	event := v2.EventInput{Type: "log", Content: v2.EventContent{Kind: "text", Text: text}, Metadata: metadata, Source: source}
	if stablePart != "" {
		key := Codex + "\x00" + in.SessionID + "\x00" + in.HookEventName + "\x00" + stablePart + "\x00" + eventDigest(event)
		event.ID = uuid.NewSHA1(captureNamespace, []byte(key)).String()
		result.StableID = true
	} else {
		id, err := uuid.NewV7()
		if err != nil {
			return normalizedHook{}, err
		}
		event.ID = id.String()
	}
	result.Event = event
	return result, nil
}

func NormalizeClaudeCode(in HookInput) (normalizedHook, error) {
	in.SessionID = strings.TrimSpace(in.SessionID)
	in.PromptID = strings.TrimSpace(in.PromptID)
	in.CWD = strings.TrimSpace(in.CWD)
	if in.SessionID == "" || in.CWD == "" {
		return normalizedHook{}, fmt.Errorf("Claude Code hook requires session_id and cwd")
	}
	if len(in.SessionID) > 512 || len(in.PromptID) > 512 || len(in.HookEventName) > 64 {
		return normalizedHook{}, fmt.Errorf("Claude Code hook identifier is too large")
	}
	kind, text := "", ""
	stablePart := in.PromptID
	result := normalizedHook{Input: in}
	metadata := map[string]json.RawMessage{
		"client_event": mustJSON(in.HookEventName),
	}
	switch in.HookEventName {
	case "SessionStart":
		if !oneOf(in.Source, "startup", "resume", "clear", "compact", "fork") {
			return normalizedHook{}, fmt.Errorf("invalid Claude Code SessionStart source")
		}
		kind, text = "session_started", "Claude Code session started ("+in.Source+")."
		stablePart = in.Source
		result.LoadContext = true
		metadata["source"] = mustJSON(in.Source)
		if in.Model != "" {
			model, _ := truncateUTF8(redactText(in.Model), MaxHookField)
			metadata["model"] = mustJSON(model)
		}
	case "UserPromptSubmit":
		if in.Prompt == "" {
			return normalizedHook{}, fmt.Errorf("Claude Code UserPromptSubmit requires prompt")
		}
		kind, text = "user_message", in.Prompt
	case "Stop":
		if in.LastAssistantMessage == "" {
			return normalizedHook{}, fmt.Errorf("Claude Code Stop requires last_assistant_message")
		}
		kind, text = "assistant_message", in.LastAssistantMessage
		result.Reminder = !in.StopHookActive
		metadata["stop_hook_active"] = mustJSON(in.StopHookActive)
	case "PreCompact":
		if !oneOf(in.Trigger, "manual", "auto") {
			return normalizedHook{}, fmt.Errorf("invalid Claude Code PreCompact trigger")
		}
		kind, text = "context_compacting", "Claude Code context compaction requested ("+in.Trigger+")."
		metadata["trigger"] = mustJSON(in.Trigger)
	case "SessionEnd":
		if !oneOf(in.Reason, "clear", "resume", "logout", "prompt_input_exit", "other") {
			return normalizedHook{}, fmt.Errorf("invalid Claude Code SessionEnd reason")
		}
		kind, text = "session_ended", "Claude Code session ended ("+in.Reason+")."
		stablePart = in.Reason
		metadata["reason"] = mustJSON(in.Reason)
	default:
		return normalizedHook{}, fmt.Errorf("unsupported Claude Code hook event %q", in.HookEventName)
	}

	text = redactText(text)
	var truncated bool
	text, truncated = truncateUTF8(text, MaxHookField)
	metadata["kind"] = mustJSON(kind)
	if truncated {
		metadata["truncated"] = mustJSON([]string{"content.text"})
	}
	source := map[string]json.RawMessage{
		"channel":    mustJSON("hook"),
		"client":     mustJSON(ClaudeCode),
		"session_id": mustJSON(in.SessionID),
	}
	event := v2.EventInput{Type: "log", Content: v2.EventContent{Kind: "text", Text: text}, Metadata: metadata, Source: source}
	if stablePart != "" {
		if in.PromptID != "" {
			// prompt_id identifies the user prompt, not each assistant response.
			// Include the normalized payload so Stop-hook continuation responses get
			// distinct IDs while an exact retry remains idempotent.
			stablePart = in.PromptID + "\x00" + eventDigest(event)
		}
		key := ClaudeCode + "\x00" + in.SessionID + "\x00" + in.HookEventName + "\x00" + stablePart
		event.ID = uuid.NewSHA1(captureNamespace, []byte(key)).String()
		result.StableID = true
	} else {
		id, err := uuid.NewV7()
		if err != nil {
			return normalizedHook{}, err
		}
		event.ID = id.String()
	}
	result.Event = event
	return result, nil
}

func redactText(text string) string {
	for _, pattern := range secretPatterns {
		text = pattern.ReplaceAllStringFunc(text, func(match string) string {
			if strings.HasPrefix(match, `"`) {
				if i := strings.Index(match, ":"); i >= 0 {
					return match[:i+1] + `"[REDACTED]"`
				}
			}
			if strings.HasPrefix(strings.ToLower(match), "bearer ") {
				return match[:7] + "[REDACTED]"
			}
			if i := strings.IndexAny(match, ":="); i >= 0 {
				return match[:i+1] + " [REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return text
}

func truncateUTF8(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	b := []byte(value)[:limit]
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return string(b), true
}

func mustJSON(value any) json.RawMessage {
	b, _ := json.Marshal(value)
	return b
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func eventDigest(event v2.EventInput) string {
	b, _ := json.Marshal(event)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
