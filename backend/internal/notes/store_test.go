package notes

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testStore(t *testing.T) Store {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return Store{Root: filepath.Join(root, "prj_test"), ProjectID: "prj_test"}
}
func input(rev int64, files ...WriteFile) SyncInput {
	return SyncInput{ExpectedRevision: &rev, Files: files}
}
func code(t *testing.T, err error, want string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("error = %v, want %s", err, want)
	}
}

func TestGenerationPublicationAndRetry(t *testing.T) {
	s := testStore(t)
	empty, err := s.Export()
	if err != nil || empty.Revision != 0 || empty.Files == nil {
		t.Fatalf("empty %#v %v", empty, err)
	}
	first, err := s.Sync(input(0, WriteFile{"topics/work/a.md", "# One\n"}, WriteFile{"index.md", "# Notes\n"}), "usr_one")
	if err != nil || first.Revision != 1 || len(first.Files) != 2 || first.Files[0].Path != "index.md" {
		t.Fatalf("first %#v %v", first, err)
	}
	oldTarget, err := os.Readlink(filepath.Join(s.Root, "notes"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Sync(input(1, WriteFile{"topics/work/a.md", "# Two\n"}), "usr_two")
	if err != nil || second.Revision != 2 || len(second.Files) != 2 {
		t.Fatalf("second %#v %v", second, err)
	}
	old, err := os.ReadFile(filepath.Join(s.Root, oldTarget, "topics/work/a.md"))
	if err != nil || string(old) != "# One\n" {
		t.Fatalf("old %q %v", old, err)
	}
	meta, err := os.ReadFile(filepath.Join(s.Root, filepath.Dir(oldTarget), "publication.json"))
	if err != nil || !strings.Contains(string(meta), "usr_one") {
		t.Fatalf("metadata %s %v", meta, err)
	}
	_, err = s.Sync(input(1, WriteFile{"index.md", "stale"}), "usr_one")
	code(t, err, "notes_revision_mismatch")
	// A new Store reads exactly the last complete generation after a restart.
	reopened, err := (Store{Root: s.Root, ProjectID: s.ProjectID}).Export()
	if err != nil || reopened.Revision != 2 || reopened.Files[1].Content != "# Two\n" {
		t.Fatalf("reopen %#v %v", reopened, err)
	}
	for _, in := range []SyncInput{input(2), input(2, WriteFile{"topics/work/a.md", "# Two\n"})} {
		out, err := s.Sync(in, "usr_one")
		if err != nil || out.Revision != 2 {
			t.Fatalf("noop %#v %v", out, err)
		}
	}
	// Unpublished staging data from an interrupted writer is never visible.
	stage := filepath.Join(s.Root, "notes-system", "revisions", ".stage-interrupted")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := s.Export(); err != nil || out.Revision != 2 {
		t.Fatalf("interruption %#v %v", out, err)
	}
}

func TestRejectBatchBeforePublication(t *testing.T) {
	s := testStore(t)
	_, err := s.Sync(input(0, WriteFile{"index.md", "old"}), "usr")
	if err != nil {
		t.Fatal(err)
	}
	bad := [][]WriteFile{
		{{"index.md", "new"}, {"../escape.md", "bad"}},
		{{"topics/Work/a.md", "a"}, {"topics/work/b.md", "b"}},
		{{"topics/a.md", "file"}, {"topics/a.md/b.md", "overlap"}},
		{{"index.md", "one"}, {"index.md", "two"}},
		{{"goals/a.md", strings.Repeat("a", MaxFileBytes+1)}},
		{{"persons/organization.md", "- missing frontmatter"}},
	}
	for _, files := range bad {
		if _, err := s.Sync(input(1, files...), "usr"); err == nil {
			t.Fatalf("accepted %#v", files[0].Path)
		}
		out, err := s.Export()
		if err != nil || out.Revision != 1 || len(out.Files) != 1 || out.Files[0].Content != "old" {
			t.Fatalf("partial write %#v %v", out, err)
		}
	}
	_, err = s.Sync(SyncInput{}, "usr")
	code(t, err, "invalid_input")
}

func TestExportSizeAndSymlinkChecks(t *testing.T) {
	s := testStore(t)
	var files []WriteFile
	// JSON escaping, not just raw byte count, must fit the export bound.
	for i := 0; i < 4; i++ {
		files = append(files, WriteFile{"topics/" + string(rune('a'+i)) + ".md", strings.Repeat("<", MaxFileBytes)})
	}
	_, err := s.Sync(input(0, files...), "usr")
	code(t, err, "too_large")
	if out, err := s.Export(); err != nil || out.Revision != 0 {
		t.Fatalf("size failure published %#v %v", out, err)
	}
	_, err = s.Sync(input(0, WriteFile{"index.md", "notes"}), "usr")
	if err != nil {
		t.Fatal(err)
	}
	target, _ := os.Readlink(filepath.Join(s.Root, "notes"))
	if err := os.Symlink("/etc/passwd", filepath.Join(s.Root, target, "goals.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Export(); err == nil {
		t.Fatal("followed file symlink")
	}
	other := testStore(t)
	if err := os.MkdirAll(other.Root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/tmp", filepath.Join(other.Root, "notes-system")); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Sync(input(0, WriteFile{"index.md", "x"}), "usr"); err == nil {
		t.Fatal("followed storage parent symlink")
	}
}

func TestOrganizationContract(t *testing.T) {
	valid := "---\nschema_version: 1\nlens: goals\nbody_style: bullet_points\norganization_word_limit: 499\ndefault_note_word_limit: 999\n---\n- Organize by outcome.\n"
	if err := validateContent("goals/organization.md", valid); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{strings.Replace(valid, "499", "500", 1), strings.Replace(valid, "lens: goals", "lens: persons", 1), valid + "A paragraph\n", valid + "- " + strings.Repeat("word ", 500), strings.Replace(valid, "---\n-", "extra: true\n---\n-", 1)} {
		if err := validateContent("goals/organization.md", text); err == nil {
			t.Fatal("accepted invalid organization")
		}
	}
	for _, name := range []string{"/index.md", "daily/../index.md", "daily//a.md", "daily/a\\b.md", ".edc-notes-sync.md", "goals/a/organization.md", "persons/CON.md", "topics/work/A.md/../b.md"} {
		if err := ValidatePath(name); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
}

func TestExportCoverageComesFromSelectedGeneration(t *testing.T) {
	s := testStore(t)
	in := input(0, WriteFile{Path: "index.md", Content: "# First"})
	in.Checkpoint = &Checkpoint{AfterSequence: 7, AttachmentBackfillThroughSequence: 3}
	first, err := s.Sync(in, "fixture")
	if err != nil || first.ThroughSequence != 7 {
		t.Fatalf("first %#v %v", first, err)
	}
	in = input(1, WriteFile{Path: "index.md", Content: "# Second"})
	in.Checkpoint = &Checkpoint{AfterSequence: 11, AttachmentBackfillThroughSequence: 7}
	if _, err = s.Sync(in, "fixture"); err != nil {
		t.Fatal(err)
	}
	latest, err := s.Export()
	if err != nil || latest.Revision != 2 || latest.ThroughSequence != 11 || latest.Files[0].Content != "# Second" {
		t.Fatalf("latest %#v %v", latest, err)
	}
	if first.ThroughSequence != 7 || first.Files[0].Content != "# First" {
		t.Fatal("previous snapshot changed")
	}
}
