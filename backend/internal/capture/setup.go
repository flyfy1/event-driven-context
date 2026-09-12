package capture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	edcrecorder "event-driven-context/skills/edc-recorder"
)

var setupHookEvents = []string{"SessionStart", "UserPromptSubmit", "Stop", "PreCompact", "SessionEnd"}

func (m *Manager) SetupPreview(options SetupOptions) (SetupPreview, error) {
	if options.Client != ClaudeCode {
		return SetupPreview{}, fmt.Errorf("unsupported setup client %q", options.Client)
	}
	directory, err := canonicalDirectory(options.Directory)
	if err != nil {
		return SetupPreview{}, err
	}
	edcPath, err := absoluteRegularPath(options.EDCPath, "edc executable")
	if err != nil {
		return SetupPreview{}, err
	}
	configPath, err := filepath.Abs(options.ConfigPath)
	if err != nil || strings.TrimSpace(options.ConfigPath) == "" {
		return SetupPreview{}, fmt.Errorf("config path must be absolute")
	}
	binding, err := m.CurrentLink(directory, options.Server, options.AccountID)
	if err != nil {
		return SetupPreview{}, fmt.Errorf("setup requires a linked project: %w", err)
	}
	command := shellQuote(edcPath) + " --config " + shellQuote(configPath) + " hook " + ClaudeCode
	preview := SetupPreview{Directory: directory, Client: options.Client, Command: command}

	settingsPath := filepath.Join(directory, ".claude", "settings.local.json")
	settingsBefore, err := readOptionalProjectFile(directory, settingsPath)
	if err != nil {
		return SetupPreview{}, err
	}
	settingsAfter, err := mergeClaudeSettings(settingsBefore, command, options.DisableHooks)
	if err != nil {
		return SetupPreview{}, err
	}
	preview.Changes = append(preview.Changes, fileChange(directory, settingsPath, settingsBefore, settingsAfter, 0o600))

	// Local Codex and Claude Code integrations use the authenticated edc CLI
	// directly. Remove the MCP entry written by older versions of this setup,
	// while preserving every unrelated project MCP server.
	mcpPath := filepath.Join(directory, ".mcp.json")
	mcpBefore, err := readOptionalProjectFile(directory, mcpPath)
	if err != nil {
		return SetupPreview{}, err
	}
	mcpAfter, removed, err := removeEDCMCPConfig(mcpBefore)
	if err != nil {
		return SetupPreview{}, err
	}
	if removed {
		preview.Changes = append(preview.Changes, fileChange(directory, mcpPath, mcpBefore, mcpAfter, 0o600))
		preview.Warnings = append(preview.Warnings, "the legacy Event-driven Context MCP entry will be removed; local agents use the authenticated edc CLI")
	}

	skillPath := filepath.Join(directory, ".claude", "skills", "edc-recorder", "SKILL.md")
	skillBefore, err := readOptionalProjectFile(directory, skillPath)
	if err != nil {
		return SetupPreview{}, err
	}
	preview.Changes = append(preview.Changes, fileChange(directory, skillPath, skillBefore, edcrecorder.Content, 0o600))

	scopePath := m.captureScopePath(binding, options.Client)
	scopeBefore, err := readOptionalProjectFile(m.root, scopePath)
	if err != nil {
		return SetupPreview{}, err
	}
	scopeAfter, _ := json.MarshalIndent(captureScope{Version: 1, Client: options.Client, Enabled: !options.DisableHooks}, "", "  ")
	scopeAfter = append(scopeAfter, '\n')
	scopeChange := fileChange(m.root, scopePath, scopeBefore, scopeAfter, 0o600)
	if options.DisableHooks {
		preview.Changes = append([]FileChange{scopeChange}, preview.Changes...)
	} else {
		preview.Changes = append(preview.Changes, scopeChange)
	}
	if options.DisableHooks {
		preview.Warnings = append(preview.Warnings, "automatic log hooks will be removed; the recorder skill remains configured for direct edc CLI use")
	}
	return preview, nil
}

// ApplySetup writes only the exact reviewed bytes. The explicit approved flag
// keeps confirmation at the CLI/UI boundary while making the business rule
// independently testable.
func (m *Manager) ApplySetup(preview SetupPreview, approved bool) error {
	if !approved {
		return fmt.Errorf("setup was not approved")
	}
	for _, change := range preview.Changes {
		current, err := readOptionalProjectFile(change.Root, change.Path)
		if err != nil {
			return err
		}
		if digest(current) != change.BeforeHash {
			return fmt.Errorf("setup target changed after preview: %s", change.Path)
		}
	}
	for _, change := range preview.Changes {
		if bytes.Equal(change.Before, change.After) {
			continue
		}
		if err := writeFileAtomic(change.Path, change.After, os.FileMode(change.Mode)); err != nil {
			return fmt.Errorf("apply setup %s: %w", change.Path, err)
		}
	}
	return nil
}

func mergeClaudeSettings(before []byte, command string, disable bool) ([]byte, error) {
	root, err := decodeJSONObject(before)
	if err != nil {
		return nil, fmt.Errorf("read Claude Code settings: %w", err)
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for _, event := range setupHookEvents {
		groups, err := objectSlice(hooks[event])
		if err != nil {
			return nil, fmt.Errorf("Claude Code hooks.%s must be an array", event)
		}
		filtered := groups[:0]
		found := false
		for _, group := range groups {
			handlers, handlerErr := objectSlice(group["hooks"])
			if handlerErr != nil {
				return nil, fmt.Errorf("Claude Code hooks.%s group has invalid hooks", event)
			}
			kept := handlers[:0]
			for _, handler := range handlers {
				if handler["type"] == "command" && handler["command"] == command {
					found = true
					if disable {
						continue
					}
				}
				kept = append(kept, handler)
			}
			if len(kept) > 0 {
				group["hooks"] = mapsToAny(kept)
				filtered = append(filtered, group)
			}
		}
		if !disable && !found {
			filtered = append(filtered, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": json.Number("5")}}})
		}
		if len(filtered) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = mapsToAny(filtered)
		}
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	} else {
		root["hooks"] = hooks
	}
	return marshalObject(root)
}

func removeEDCMCPConfig(before []byte) ([]byte, bool, error) {
	root, err := decodeJSONObject(before)
	if err != nil {
		return nil, false, fmt.Errorf("read Claude Code MCP config: %w", err)
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if servers == nil {
		return before, false, nil
	}
	removed := false
	for _, name := range []string{"event-driven-context", "event-context"} {
		if _, ok := servers[name]; ok {
			delete(servers, name)
			removed = true
		}
	}
	if !removed {
		return before, false, nil
	}
	if len(servers) == 0 {
		delete(root, "mcpServers")
	} else {
		root["mcpServers"] = servers
	}
	after, err := marshalObject(root)
	return after, true, err
}

func decodeJSONObject(data []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON data")
	}
	if root == nil {
		return nil, fmt.Errorf("top level must be an object")
	}
	return root, nil
}

func objectSlice(value any) ([]map[string]any, error) {
	if value == nil {
		return []map[string]any{}, nil
	}
	array, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("not an array")
	}
	out := make([]map[string]any, 0, len(array))
	for _, item := range array {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("array item is not an object")
		}
		out = append(out, object)
	}
	return out, nil
}

func mapsToAny(values []map[string]any) []any {
	out := make([]any, len(values))
	for i := range values {
		out[i] = values[i]
	}
	return out
}

func marshalObject(value map[string]any) ([]byte, error) {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func fileChange(root, path string, before, after []byte, mode os.FileMode) FileChange {
	return FileChange{Root: root, Path: path, Before: append([]byte(nil), before...), After: append([]byte(nil), after...), BeforeHash: digest(before), Diff: displayDiff(before, after), Mode: mode}
}

func displayDiff(before, after []byte) string {
	if bytes.Equal(before, after) {
		return "(no change)"
	}
	beforeDisplay := redactConfigForDisplay(before)
	afterDisplay := redactConfigForDisplay(after)
	return "--- current\n+++ proposed\n@@ full file @@\n-" + strings.ReplaceAll(beforeDisplay, "\n", "\n-") + "+" + strings.ReplaceAll(afterDisplay, "\n", "\n+")
}

func redactConfigForDisplay(data []byte) string {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return redactText(string(data))
	}
	redactConfigValue(value, "")
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return redactText(string(data))
	}
	return string(b)
}

func redactConfigValue(value any, parent string) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			lower := strings.ToLower(key)
			if parent == "env" || parent == "headers" || sensitiveConfigKey(lower) {
				current[key] = "[REDACTED]"
				continue
			}
			redactConfigValue(child, lower)
		}
	case []any:
		for _, child := range current {
			redactConfigValue(child, parent)
		}
	}
}

func sensitiveConfigKey(lower string) bool {
	return lower == "authorization" || lower == "cookie" || lower == "password" || lower == "secret" ||
		strings.HasSuffix(lower, "_api_key") || strings.HasSuffix(lower, "-api-key") ||
		strings.HasSuffix(lower, "_token") || strings.HasSuffix(lower, "-token") ||
		strings.HasSuffix(lower, "_secret") || strings.HasSuffix(lower, "-secret") ||
		strings.HasSuffix(lower, "_password") || strings.HasSuffix(lower, "-password")
}

func digest(data []byte) string {
	if data == nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func absoluteRegularPath(path, label string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s must be absolute", label)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s must be a regular file", label)
	}
	return resolved, nil
}

func readOptionalProjectFile(root, path string) ([]byte, error) {
	if !within(path, root) {
		return nil, fmt.Errorf("setup target escapes project directory")
	}
	for current := path; current != root; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("setup target traverses symlink: %s", current)
		}
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
