package notes

import (
	"crypto/sha256"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

var noteIDPattern = regexp.MustCompile(`^note_[A-Za-z0-9_-]+$`)
var eventLinkPattern = regexp.MustCompile(`edc-event://([^\s)\]>]+)`)
var fileLinkPattern = regexp.MustCompile(`edc-file://([^\s)\]>]+)`)

var wikiPattern = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

func DocumentID(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return ""
	}
	for _, line := range lines[1:] {
		if line == "---" {
			break
		}
		if strings.HasPrefix(line, "id:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		}
	}
	return ""
}

func PolicyHash(files []File) string {
	var values []string
	for _, f := range files {
		if path.Base(f.Path) == "organization.md" {
			values = append(values, f.Path+"\n"+f.Content)
		}
	}
	sort.Strings(values)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(values, "\n"))))
}

// ValidateOrganizedTree is the host/backend publication boundary, independent
// of model instructions. It validates structure and references, not factual truth.
func ValidateOrganizedTree(files []WriteFile, previous []File, sources map[string]bool, fileSources ...map[string]bool) error {
	ids := map[string]string{}
	paths := map[string]bool{}
	for _, f := range files {
		if err := ValidatePath(f.Path); err != nil {
			return err
		}
		if err := validateContent(f.Path, f.Content); err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
		if paths[f.Path] {
			return invalid("duplicate document path")
		}
		paths[f.Path] = true
		if path.Base(f.Path) == "organization.md" {
			continue
		}
		if len(strings.Fields(f.Content)) >= 1000 {
			return invalid(f.Path + ": split document to fewer than 1000 words")
		}
		id := DocumentID(f.Content)
		if !noteIDPattern.MatchString(id) {
			return invalid(f.Path + ": stable note_ id frontmatter required")
		}
		if old := ids[id]; old != "" {
			return invalid("duplicate note id: " + id)
		}
		ids[id] = f.Path
		if !strings.Contains(f.Content, "\ntitle:") || !strings.Contains(f.Content, "\n> ") {
			return invalid(f.Path + ": title and visible summary required")
		}
	}
	for _, required := range []string{"index.md", "daily/organization.md", "persons/organization.md", "topics/organization.md", "goals/organization.md", "daily/index.md", "persons/index.md", "topics/index.md", "goals/index.md", "topics/work/index.md", "topics/life/index.md", "goals/priorities.md"} {
		if !paths[required] {
			return invalid("missing required note: " + required)
		}
	}
	for _, old := range previous {
		if id := DocumentID(old.Content); id != "" && ids[id] == "" {
			return invalid("preserve existing stable ID when moving/splitting: " + id)
		}
	}
	for _, f := range files {
		if path.Base(f.Path) == "organization.md" {
			continue
		}
		for _, m := range eventLinkPattern.FindAllStringSubmatch(f.Content, -1) {
			if !sources[m[1]] {
				return invalid(f.Path + ": unknown or unreadable event citation " + m[1])
			}
		}
		for _, m := range fileLinkPattern.FindAllStringSubmatch(f.Content, -1) {
			if len(fileSources) == 0 || !fileSources[0][m[1]] {
				return invalid(f.Path + ": unknown or unreadable file link " + m[1])
			}
		}
		for _, m := range wikiPattern.FindAllStringSubmatch(f.Content, -1) {
			id := strings.SplitN(m[1], "|", 2)[0]
			if ids[id] == "" {
				return invalid(f.Path + ": unresolved wiki link " + id)
			}
		}
	}
	return nil
}

// FileLinkIDs returns stable attachment references without interpreting labels.
func FileLinkIDs(text string) []string {
	out := []string{}
	for _, m := range fileLinkPattern.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	return out
}
