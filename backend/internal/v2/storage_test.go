package v2

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProjectWritesPreserveSnapshotOnPersistFailure(t *testing.T) {
	f := newFixture(t)
	installed, _ := install(t, f)
	s := f.service
	p := s.projectLocked(f.project.ID)
	p.Files["file_test"] = FileInfo{ID: "file_test", ProjectID: f.project.ID, UploadedAt: "2020-01-01T00:00:00Z"}
	other := s.projectLocked(f.other.ID)
	other.Files["file_other"] = FileInfo{ID: "file_other", ProjectID: f.other.ID, UploadedAt: "2020-01-01T00:00:00Z"}
	if err := s.persistLocked(); err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(s.data)
	if err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(s.root, "index.json")
	backup := index + ".backup"
	if err := os.Rename(index, backup); err != nil {
		t.Fatal(err)
	}
	// Force rename failure after candidate serialization and temporary-file fsync,
	// independently of permissions (also works when tests run as root).
	if err := os.Mkdir(index, 0700); err != nil {
		t.Fatal(err)
	}
	operations := map[string]func() error{
		"event and file reference": func() error {
			input := textInput(eventOne, "")
			input.Content = EventContent{Kind: "file", FileID: "file_test"}
			_, err := s.RecordEvents(f.aliceCtx, f.project.ID, RecordEventsInput{Events: []EventInput{input}})
			return err
		},
		"state": func() error {
			_, err := s.PutState(f.aliceCtx, f.project.ID, PutStateInput{Key: "project-brief/current", AsPluginID: "project-brief", Content: StateContent{Format: "text", Text: "state"}})
			return err
		},
		"manual run and mailbox": func() error {
			_, err := s.RequestManualRun(f.aliceCtx, f.project.ID, "project-brief", ManualRunInput{RequestID: "retry-1"})
			return err
		},
		"installation and token": func() error {
			manifest := installed.Installation.Manifest
			manifest.ID = "another-plugin"
			_, err := s.InstallPlugin(f.aliceCtx, f.project.ID, InstallPluginInput{Manifest: manifest})
			return err
		},
		"config revision": func() error {
			_, err := s.RevisePlugin(f.aliceCtx, f.project.ID, "project-brief", RevisePluginInput{ExpectedRevision: 1, Config: json.RawMessage(`{"changed":true}`)})
			return err
		},
		"status": func() error {
			_, err := s.SetPluginStatus(f.aliceCtx, f.project.ID, "project-brief", "paused")
			return err
		},
		"removal and token": func() error {
			_, err := s.RemovePlugin(f.aliceCtx, f.project.ID, "project-brief")
			return err
		},
		"cross-project file cleanup": func() error {
			_, err := s.CleanupUnreferencedFiles(f.aliceCtx, time.Now())
			return err
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			err := operation()
			var pathErr *os.LinkError
			if !errors.As(err, &pathErr) || pathErr.Op != "rename" {
				t.Fatalf("expected persist rename failure, got %v", err)
			}
			after, err := json.Marshal(s.data)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("failed write changed in-memory snapshot")
			}
			if s.data.Projects[f.other.ID] != other {
				t.Fatal("unrelated project was copied")
			}
		})
	}
	if err := os.Remove(index); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, index); err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, disk) {
		t.Fatal("failed writes changed persisted snapshot")
	}
	result, err := s.RecordEvents(f.aliceCtx, f.project.ID, RecordEventsInput{Events: []EventInput{textInput(eventOne, "retry")}})
	if err != nil {
		t.Fatal(err)
	}
	if created(t, result).Sequence != 1 {
		t.Fatal("failed write consumed a sequence")
	}
	if s.data.Projects[f.other.ID] != other {
		t.Fatal("successful write copied unrelated project")
	}
}

func TestProjectSnapshotClonePreservesValuesAndIsolatesNestedData(t *testing.T) {
	p := &projectData{
		Events:        []Event{{Metadata: map[string]json.RawMessage{"nested": json.RawMessage(`{"value":1}`)}, Source: map[string]json.RawMessage{"channel": json.RawMessage(`"app"`)}, Refs: []Ref{}}},
		States:        map[string][]State{"key": {{Data: json.RawMessage(`{"value":1}`), Refs: []string{}}}},
		Installations: map[string]Installation{"plugin": {Config: json.RawMessage(`{}`), Permissions: Permissions{ReadEvents: []string{}}, ConfigRevisions: []PluginConfigRevision{{Config: json.RawMessage(`{}`)}}}},
		ManualRuns:    map[string]ManualRunRequest{"run": {SourceEventIDs: []string{"event"}}},
	}
	normalizeProjectData(p)
	original := snapshot{Projects: map[string]*projectData{"touched": p, "other": p}, TokenHashes: map[string]string{"hash": "installation"}}
	before, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	candidate := cloneSnapshotForProject(original, "touched")
	copied, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, copied) {
		t.Fatal("clone changed stored values, including empty slices")
	}
	cp := candidate.Projects["touched"]
	cp.Events[0].Metadata["nested"][0] = ' '
	cp.Events[0].Source["channel"][0] = ' '
	cp.States["key"][0].Data[0] = ' '
	cp.Installations["plugin"].Config[0] = ' '
	cp.Installations["plugin"].ConfigRevisions[0].Config[0] = ' '
	cp.ManualRuns["run"].SourceEventIDs[0] = "changed"
	delete(candidate.TokenHashes, "hash")
	after, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("candidate mutation changed original snapshot")
	}
	if candidate.Projects["other"] != p {
		t.Fatal("unrelated project was cloned")
	}
}
