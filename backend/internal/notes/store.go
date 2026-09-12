package notes

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var generationPattern = regexp.MustCompile(`^([1-9][0-9]*)-[0-9a-f]{32}$`)

// Store is rooted at one authorized project directory. Its caller serializes
// Export and Sync. Immutable generations make a pointer switch the only commit.
type Store struct{ Root, ProjectID string }

func (s Store) Export() (Export, error) {
	out := Export{ProjectID: s.ProjectID, Files: []File{}}
	if err := checkDirectories(s.Root, false); err != nil {
		return out, err
	}
	target, err := os.Readlink(filepath.Join(s.Root, "notes"))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("read published notes pointer: %w", err)
	}
	parts := strings.Split(filepath.ToSlash(target), "/")
	if len(parts) != 4 || parts[0] != "notes-system" || parts[1] != "revisions" || parts[3] != "notes" {
		return out, fmt.Errorf("invalid notes generation target")
	}
	match := generationPattern.FindStringSubmatch(parts[2])
	if match == nil {
		return out, fmt.Errorf("invalid notes generation")
	}
	out.Revision, err = strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return out, err
	}
	root := filepath.Join(s.Root, target)
	if err = checkDirectories(root, false); err != nil {
		return out, err
	}
	publication, err := readPublication(filepath.Join(filepath.Dir(root), "publication.json"))
	if err != nil {
		return out, err
	}
	if publication.Revision != out.Revision {
		return out, fmt.Errorf("notes revision metadata mismatch")
	}
	out.ThroughSequence = publication.Checkpoint.AfterSequence
	totalBytes := 0
	err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks inside notes are forbidden")
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("notes must be regular files")
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if err = ValidatePath(rel); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > MaxFileBytes {
			return tooLarge("note exceeds 1 MiB")
		}
		totalBytes += int(info.Size())
		if totalBytes > MaxExportBytes {
			return tooLarge("notes export exceeds 16 MiB")
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		if err = validateContent(rel, string(data)); err != nil {
			return err
		}
		out.Files = append(out.Files, makeFile(rel, string(data)))
		if len(out.Files) > MaxFiles {
			return tooLarge("notes exceed 10000 files")
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	if err = validateExport(out); err != nil {
		return out, err
	}
	return out, nil
}

func (s Store) Sync(in SyncInput, author string) (Export, error) {
	if in.ExpectedRevision == nil || *in.ExpectedRevision < 0 {
		return Export{}, invalid("expected_revision is required and must be nonnegative")
	}
	current, err := s.Export()
	if err != nil {
		return Export{}, err
	}
	if *in.ExpectedRevision != current.Revision {
		return Export{}, &Error{"notes_revision_mismatch", "notes changed remotely; rerun sync"}
	}
	if len(in.Files) > MaxFiles {
		return Export{}, tooLarge("notes exceed 10000 files")
	}
	files := make(map[string]File, len(current.Files)+len(in.Files))
	if !in.Replace {
		for _, f := range current.Files {
			files[f.Path] = f
		}
	}
	seen := map[string]bool{}
	checkpoint, err := s.Checkpoint()
	if err != nil {
		return Export{}, err
	}
	changed := in.Replace
	if in.Checkpoint != nil {
		changed = changed || checkpoint != *in.Checkpoint
		checkpoint = *in.Checkpoint
	}
	for _, f := range in.Files {
		if err = ValidatePath(f.Path); err != nil {
			return Export{}, err
		}
		if err = validateContent(f.Path, f.Content); err != nil {
			return Export{}, err
		}
		if seen[f.Path] {
			return Export{}, invalid("duplicate note path")
		}
		seen[f.Path] = true
		next := makeFile(f.Path, f.Content)
		if prev, ok := files[f.Path]; !ok || prev.SHA256 != next.SHA256 {
			changed = true
		}
		files[f.Path] = next
	}
	if !changed {
		return current, nil
	}
	if current.Revision == math.MaxInt64 {
		return Export{}, fmt.Errorf("notes revision exhausted")
	}
	next := Export{ProjectID: s.ProjectID, Revision: current.Revision + 1, ThroughSequence: checkpoint.AfterSequence, Files: make([]File, 0, len(files))}
	for _, f := range files {
		next.Files = append(next.Files, f)
	}
	sort.Slice(next.Files, func(i, j int) bool { return next.Files[i].Path < next.Files[j].Path })
	if err = validateExport(next); err != nil {
		return Export{}, err
	}
	if err = s.publish(next, author, checkpoint); err != nil {
		return Export{}, err
	}
	return next, nil
}

func makeFile(name, text string) File {
	h := sha256.Sum256([]byte(text))
	return File{Path: name, Content: text, SHA256: hex.EncodeToString(h[:])}
}

func validateExport(out Export) error {
	if len(out.Files) > MaxFiles {
		return tooLarge("notes exceed 10000 files")
	}
	// Check every prefix so Foo/a.md and foo/b.md cannot alias on macOS.
	names := map[string]string{}
	fileNames := map[string]bool{}
	totalBytes := 0
	for _, f := range out.Files {
		totalBytes += len(f.Content)
		if totalBytes > MaxExportBytes {
			return tooLarge("notes export exceeds 16 MiB")
		}
		parts := strings.Split(f.Path, "/")
		for i := range parts {
			prefix := strings.Join(parts[:i+1], "/")
			folded := strings.ToLower(prefix)
			if old, ok := names[folded]; ok && old != prefix {
				return invalid("note paths differ only by case")
			}
			if i != len(parts)-1 && fileNames[folded] {
				return invalid("note path overlaps a directory")
			}
			names[folded] = prefix
		}
		fileNames[strings.ToLower(f.Path)] = true
	}
	data, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if len(data)+1 > MaxExportBytes {
		return tooLarge("encoded notes export exceeds 16 MiB")
	}
	return nil
}

func (s Store) publish(out Export, author string, checkpoint Checkpoint) error {
	revisions := filepath.Join(s.Root, "notes-system", "revisions")
	if err := checkDirectories(revisions, true); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(revisions, ".stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	noteRoot := filepath.Join(stage, "notes")
	if err = os.Mkdir(noteRoot, 0700); err != nil {
		return err
	}
	dirs := map[string]bool{stage: true, noteRoot: true}
	for _, f := range out.Files {
		name := filepath.Join(noteRoot, filepath.FromSlash(f.Path))
		dir := filepath.Dir(name)
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		for d := dir; d != noteRoot; d = filepath.Dir(d) {
			dirs[d] = true
		}
		if err = writeSynced(name, []byte(f.Content)); err != nil {
			return err
		}
	}
	meta, _ := json.Marshal(struct {
		Revision   int64      `json:"revision"`
		Actor      string     `json:"actor_user_id"`
		At         string     `json:"published_at"`
		Checkpoint Checkpoint `json:"checkpoint"`
	}{out.Revision, author, time.Now().UTC().Format(time.RFC3339Nano), checkpoint})
	if err = writeSynced(filepath.Join(stage, "publication.json"), meta); err != nil {
		return err
	}
	ordered := make([]string, 0, len(dirs))
	for dir := range dirs {
		ordered = append(ordered, dir)
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, dir := range ordered {
		if err = syncDirectory(dir); err != nil {
			return err
		}
	}
	var suffix [16]byte
	if _, err = rand.Read(suffix[:]); err != nil {
		return err
	}
	gen := fmt.Sprintf("%d-%x", out.Revision, suffix)
	if err = os.Rename(stage, filepath.Join(revisions, gen)); err != nil {
		return err
	}
	if err = syncDirectory(revisions); err != nil {
		return err
	}
	link := filepath.Join(s.Root, ".notes-"+gen)
	if err = os.Symlink(filepath.Join("notes-system", "revisions", gen, "notes"), link); err != nil {
		return err
	}
	defer os.Remove(link)
	if err = os.Rename(link, filepath.Join(s.Root, "notes")); err != nil {
		return err
	}
	// If fsync fails after rename, the caller sees an uncertain outcome. A retry
	// fetches the published hashes rather than blindly repeating the old revision.
	return syncDirectory(s.Root)
}

func writeSynced(name string, data []byte) error {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func syncDirectory(name string) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func checkDirectories(name string, create bool) error {
	// Only the intentionally published notes pointer may be a symlink.
	name = filepath.Clean(name)
	if name == filepath.Dir(name) {
		return nil
	}
	if err := checkDirectories(filepath.Dir(name), create); err != nil {
		return err
	}
	info, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return nil
		}
		if err = os.Mkdir(name, 0700); err != nil {
			return err
		}
		return syncDirectory(filepath.Dir(name))
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("notes storage parent must be a real directory")
	}
	return nil
}
