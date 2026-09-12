package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"

	"event-driven-context/internal/v2client"
)

const notesBaselineName = ".edc-notes-sync.json"
const notesLockName = ".edc-notes-sync.lock"

type notesBaseline struct {
	Version   int               `json:"version"`
	Server    string            `json:"server"`
	ProjectID string            `json:"project_id"`
	Revision  int64             `json:"revision"`
	Files     map[string]string `json:"files"`
}

type notesSyncResult struct {
	ProjectID      string   `json:"project_id"`
	Output         string   `json:"output"`
	Revision       int64    `json:"revision"`
	PreservedLocal []string `json:"preserved_local,omitempty"`
	Downloaded     int      `json:"downloaded"`
	Restored       []string `json:"restored,omitempty"`
}

func (a *app) notes(args []string) error {
	if len(args) == 0 || args[0] != "sync" {
		return fmt.Errorf("notes requires sync --project ID --output DIR")
	}
	f := a.flags("notes sync")
	projectID := f.String("project", "", "project ID (defaults to directory binding)")
	output := f.String("output", "", "local Markdown download folder (never uploads)")
	if err := parse(f, args[1:]); err != nil {
		return err
	}
	if *output == "" {
		return fmt.Errorf("--output is required")
	}
	if err := a.requiredProject(projectID); err != nil {
		return err
	}
	result, err := a.syncNotes(*projectID, *output)
	return a.result(result, err)
}

func canonicalNotesOrigin(raw string) string {
	u, _ := url.Parse(raw)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "443" && u.Scheme == "https" || port == "80" && u.Scheme == "http" {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return strings.ToLower(u.Scheme) + "://" + host
}

func (a *app) syncNotes(projectID, output string) (result notesSyncResult, err error) {
	output, err = filepath.Abs(output)
	if err != nil {
		return result, err
	}
	result = notesSyncResult{ProjectID: projectID, Output: output}
	if info, statErr := os.Lstat(output); statErr == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return result, fmt.Errorf("output must be a directory, not a symlink")
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return result, statErr
	}
	if err = os.MkdirAll(output, 0700); err != nil {
		return result, err
	}
	root, err := os.OpenRoot(output)
	if err != nil {
		return result, err
	}
	defer root.Close()
	if err = notesRegularOrMissing(root, notesLockName); err != nil {
		return result, err
	}
	lock, err := root.OpenFile(notesLockName, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return result, err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return result, fmt.Errorf("another notes sync is running for this folder")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	origin := canonicalNotesOrigin(a.client.BaseURL)
	baseline, err := readNotesBaseline(root, origin, projectID)
	if err != nil {
		return result, err
	}
	local, err := scanLocalNotes(root)
	if err != nil {
		return result, err
	}
	remote, err := a.client.ExportNotes(a.ctx, projectID)
	if err != nil {
		return result, err
	}
	if remote.Revision < baseline.Revision {
		return result, fmt.Errorf("remote notes revision is older than the local baseline; refusing to sync")
	}
	remoteFiles := make(map[string]string, len(remote.Files))
	for _, file := range remote.Files {
		remoteFiles[file.Path] = file.Content
	}
	if err = validateNotesUnion(root, local, remoteFiles, baseline.Files); err != nil {
		return result, err
	}
	var conflicts []string
	for _, name := range sortedNotesKeys(local, remoteFiles) {
		l, lok := local[name]
		r, rok := remoteFiles[name]
		old, tracked := baseline.Files[name]
		switch {
		case !lok && rok:
			if tracked {
				result.Restored = append(result.Restored, name)
			}
		case lok && !rok:
			result.PreservedLocal = append(result.PreservedLocal, name)
		case l == r:
		case !tracked:
			conflicts = append(conflicts, name)
		case v2client.NoteHash(l) == old:
			// Remote changed; download after the complete preflight.
		default:
			conflicts = append(conflicts, name)
		}
	}
	if len(conflicts) > 0 {
		return result, fmt.Errorf("notes sync conflicts; no files changed: %s; reconcile local content with the remote version and rerun", strings.Join(conflicts, ", "))
	}
	// Detect local edits made while fetching the remote snapshot.
	if err = notesLocalUnchanged(root, local); err != nil {
		return result, err
	}
	for _, name := range sortedNotesKeys(remoteFiles) {
		content := remoteFiles[name]
		if previous, ok := local[name]; ok && previous == content {
			continue
		}
		if err = notesCheckPath(root, name); err != nil {
			return result, err
		}
		current, present, readErr := readLocalNote(root, name)
		if readErr != nil {
			return result, readErr
		}
		previous, existed := local[name]
		if present != existed || current != previous {
			return result, fmt.Errorf("local note changed during sync: %s; rerun", name)
		}
		if err = atomicNotesWrite(root, name, []byte(content)); err != nil {
			return result, err
		}
		result.Downloaded++
	}
	baseline.Revision = remote.Revision
	baseline.Files = make(map[string]string, len(remoteFiles))
	for name, content := range remoteFiles {
		baseline.Files[name] = v2client.NoteHash(content)
	}
	// Final full check also detects newly added or edited files during writes.
	expected := make(map[string]string, len(local)+len(remoteFiles))
	for name, content := range local {
		expected[name] = content
	}
	for name, content := range remoteFiles {
		expected[name] = content
	}
	if err = notesLocalUnchanged(root, expected); err != nil {
		return result, err
	}
	data, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return result, err
	}
	if err = atomicNotesWrite(root, notesBaselineName, append(data, '\n')); err != nil {
		return result, err
	}
	result.Revision = remote.Revision
	return result, nil
}

func readNotesBaseline(root *os.Root, origin, projectID string) (notesBaseline, error) {
	out := notesBaseline{Version: 1, Server: origin, ProjectID: projectID, Files: map[string]string{}}
	if err := notesRegularOrMissing(root, notesBaselineName); err != nil {
		return out, err
	}
	f, err := root.Open(notesBaselineName)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4<<20+1))
	if err != nil {
		return out, err
	}
	if len(data) > 4<<20 {
		return out, fmt.Errorf("notes baseline exceeds size limit")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&out); err != nil {
		return out, fmt.Errorf("invalid notes baseline: %w", err)
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return out, fmt.Errorf("invalid notes baseline trailing data")
	}
	if out.Version != 1 || out.Files == nil || out.Revision < 0 || len(out.Files) > v2client.MaxNotesFiles {
		return out, fmt.Errorf("unsupported or invalid notes baseline")
	}
	if out.Server != origin || out.ProjectID != projectID {
		return out, fmt.Errorf("output folder is bound to another server or project; choose a different --output folder")
	}
	for name, hash := range out.Files {
		if err = v2client.ValidateNotePath(name); err != nil {
			return out, err
		}
		if len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
			return out, fmt.Errorf("invalid baseline hash for %q", name)
		}
	}
	return out, nil
}

func notesRegularOrMissing(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing non-regular file or symlink: %s", name)
	}
	return nil
}

func notesCheckPath(root *os.Root, name string) error {
	parts := strings.Split(name, "/")
	for i := range parts {
		current := strings.Join(parts[:i+1], "/")
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink: %s", current)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("note parent is not a directory: %s", current)
		}
		if i == len(parts)-1 && !info.Mode().IsRegular() {
			return fmt.Errorf("note target is not a regular file: %s", current)
		}
	}
	return nil
}

func readLocalNote(root *os.Root, name string) (string, bool, error) {
	if err := notesCheckPath(root, name); err != nil {
		return "", false, err
	}
	f, err := root.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, v2client.MaxNoteBytes+1))
	if err != nil {
		return "", true, err
	}
	if len(data) > v2client.MaxNoteBytes || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return "", true, fmt.Errorf("note must be UTF-8 and at most 1 MiB: %s", name)
	}
	return string(data), true, nil
}

func scanLocalNotes(root *os.Root) (map[string]string, error) {
	out := make(map[string]string)
	total := 0
	err := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink: %s", name)
		}
		if strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !strings.EqualFold(path.Ext(name), ".md") {
			return nil
		}
		if err := v2client.ValidateNotePath(name); err != nil {
			return err
		}
		content, _, err := readLocalNote(root, name)
		if err != nil {
			return err
		}
		total += len(content)
		if total > v2client.MaxNotesExportBytes || len(out) >= v2client.MaxNotesFiles {
			return fmt.Errorf("local notes exceed 16 MiB or 10000-file limit")
		}
		out[name] = content
		return nil
	})
	return out, err
}

func validateNotesUnion(root *os.Root, local, remote, baseline map[string]string) error {
	seen := make(map[string]string)
	files := make(map[string]bool)
	for _, name := range sortedNotesKeys(local, remote) {
		files[strings.ToLower(name)] = true
	}
	for _, name := range sortedNotesKeys(local, remote, baseline) {
		if err := notesCheckPath(root, name); err != nil {
			return err
		}
		parts := strings.Split(name, "/")
		for i := range parts {
			p := strings.Join(parts[:i+1], "/")
			k := strings.ToLower(p)
			if i < len(parts)-1 && files[k] {
				return fmt.Errorf("note file also used as directory: %s", p)
			}
			if previous, ok := seen[k]; ok && previous != p {
				return fmt.Errorf("case-aliased note paths: %s and %s", previous, p)
			}
			seen[k] = p
		}
	}
	return nil
}

func sortedNotesKeys(maps ...map[string]string) []string {
	unique := make(map[string]bool)
	for _, m := range maps {
		for name := range m {
			unique[name] = true
		}
	}
	names := make([]string, 0, len(unique))
	for name := range unique {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func notesLocalUnchanged(root *os.Root, expected map[string]string) error {
	actual, err := scanLocalNotes(root)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("local notes changed during sync; baseline preserved, rerun")
	}
	return nil
}

func atomicNotesWrite(root *os.Root, name string, data []byte) error {
	if err := notesCheckPath(root, name); err != nil {
		return err
	}
	dir := path.Dir(name)
	if err := root.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Create the temporary file through Root as well, preserving confinement.
	var file *os.File
	var err error
	var temp string
	for attempt := 0; attempt < 100; attempt++ {
		temp = path.Join(dir, fmt.Sprintf(".edc-notes-write-%d-%d", os.Getpid(), attempt))
		file, err = root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if !errors.Is(err, os.ErrExist) {
			break
		}
	}
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = notesCheckPath(root, name); err != nil {
		return err
	}
	if err = root.Rename(temp, name); err != nil {
		return err
	}
	parent, err := root.Open(dir)
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}
