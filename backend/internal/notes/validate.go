package notes

import (
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

func ValidatePath(name string) error {
	if name == "" || len(name) > 1024 || !utf8.ValidString(name) || path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00<>:\"|?*") || !strings.HasSuffix(name, ".md") {
		return invalid("notes require clean relative .md paths")
	}
	parts := strings.Split(name, "/")
	if len(parts) > 32 {
		return invalid("note path is too deep")
	}
	for _, part := range parts {
		if strings.HasPrefix(part, ".") || strings.TrimSpace(part) != part || strings.HasSuffix(part, ".") || len(part) > 240 {
			return invalid("invalid note path component")
		}
		for _, r := range part {
			if unicode.IsControl(r) {
				return invalid("invalid note path character")
			}
		}
		stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9' {
			return invalid("reserved note path component")
		}
	}
	if name == "index.md" {
		return nil
	}
	if len(parts) < 2 || !lens(parts[0]) {
		return invalid("notes must be index.md or inside daily, persons, topics, or goals")
	}
	if strings.EqualFold(path.Base(name), "organization.md") && (len(parts) != 2 || path.Base(name) != "organization.md") {
		return invalid("organization.md belongs only in a base lens folder")
	}
	return nil
}

func lens(s string) bool { return s == "daily" || s == "persons" || s == "topics" || s == "goals" }

func validateContent(name, text string) error {
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return invalid("notes must be UTF-8 text without NUL")
	}
	if len(text) > MaxFileBytes {
		return tooLarge("note exceeds 1 MiB")
	}
	if path.Base(name) == "organization.md" {
		return validateOrganization(name, text)
	}
	return nil
}

// Organization frontmatter is deliberately a fixed, flat YAML subset. The body
// is editable organizational guidance, never a source of executable settings.
func validateOrganization(name, text string) error {
	if len(strings.Fields(text)) >= 500 {
		return invalid("organization.md must contain fewer than 500 words")
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) < 3 || lines[0] != "---" {
		return invalid("organization.md requires fixed frontmatter")
	}
	want := map[string]string{"schema_version": "1", "lens": strings.Split(name, "/")[0], "body_style": "bullet_points", "organization_word_limit": "499", "default_note_word_limit": "999"}
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
		key, value, ok := strings.Cut(lines[i], ":")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || want[key] == "" || want[key] != value {
			return invalid("organization.md frontmatter fields and values are fixed")
		}
		delete(want, key)
	}
	if end < 0 || len(want) != 0 {
		return invalid("organization.md frontmatter is incomplete")
	}
	bullets := 0
	for _, line := range lines[end+1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, "- ") || strings.TrimSpace(line[2:]) == "" {
			return invalid("organization.md body must use flat bullet points")
		}
		bullets++
	}
	if bullets == 0 {
		return invalid("organization.md requires at least one bullet")
	}
	return nil
}
