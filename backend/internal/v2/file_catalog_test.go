package v2

import (
	"bytes"
	"encoding/json"
	"event-driven-context/internal/notes"
	"fmt"
	"testing"
)

func catalogFile(t *testing.T, f *fixture, body string) FileInfo {
	t.Helper()
	v, err := f.service.PutFile(f.aliceCtx, f.project.ID, FileUpload{Filename: "metadata.json", MediaType: "text/plain", SizeBytes: int64(len(body)), Reader: bytes.NewBufferString(body)})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func fileEvent(n int, fileID string) EventInput {
	return EventInput{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", n), Type: "note", Content: EventContent{Kind: "file", FileID: fileID}, Source: map[string]json.RawMessage{"channel": json.RawMessage(`"app"`)}}
}
func appendFile(t *testing.T, f *fixture, n int, fileID string) {
	t.Helper()
	out, err := f.service.RecordEvents(f.aliceCtx, f.project.ID, RecordEventsInput{Events: []EventInput{fileEvent(n, fileID)}})
	if err != nil {
		t.Fatal(err)
	}
	created(t, out)
}
func TestFileCatalogFreezesFilesAndReferences(t *testing.T) {
	f := newFixture(t)
	a := catalogFile(t, f, "alpha")
	b := catalogFile(t, f, "bravo")
	unused := catalogFile(t, f, "unused")
	appendFile(t, f, 1, a.ID)
	appendFile(t, f, 2, b.ID)
	first, err := f.service.ListFiles(f.aliceCtx, f.project.ID, FileCatalogInput{Limit: 1})
	if err != nil || len(first.Files) != 1 || first.NextCursor == "" || first.ThroughSequence != 2 {
		t.Fatalf("first %#v %v", first, err)
	}
	appendFile(t, f, 3, unused.ID)
	appendFile(t, f, 4, a.ID)
	second, err := f.service.ListFiles(f.aliceCtx, f.project.ID, FileCatalogInput{Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(second.Files) != 1 || second.NextCursor != "" || second.ThroughSequence != 2 {
		t.Fatalf("second %#v %v", second, err)
	}
	for _, entry := range append(first.Files, second.Files...) {
		if entry.ID == unused.ID || entry.ReferenceCount != 1 {
			t.Fatalf("snapshot leaked %#v", entry)
		}
	}
	metadata, err := f.service.FileMetadata(f.aliceCtx, f.project.ID, a.ID, &first.ThroughSequence)
	if err != nil || metadata.ReferenceCount != 1 {
		t.Fatalf("metadata %#v %v", metadata, err)
	}
	if _, err = f.service.FileMetadata(f.aliceCtx, f.project.ID, unused.ID, &first.ThroughSequence); err == nil {
		t.Fatal("out-of-range file exposed")
	}
	if _, err = f.service.ListFiles(f.bobCtx, f.project.ID, FileCatalogInput{}); err == nil {
		t.Fatal("foreign project exposed")
	}
	if _, err = f.service.ListFiles(f.aliceCtx, f.project.ID, FileCatalogInput{Cursor: first.NextCursor + "x"}); err == nil {
		t.Fatal("tampered cursor accepted")
	}
	if _, err = f.service.FileReferences(f.aliceCtx, f.project.ID, a.ID, FileCatalogInput{Cursor: first.NextCursor}); err == nil {
		t.Fatal("cursor reused across endpoints")
	}
}
func TestFileReferencePagesAndPluginVisibility(t *testing.T) {
	f := newFixture(t)
	a := catalogFile(t, f, "shared")
	for i := 1; i <= 7; i++ {
		appendFile(t, f, i, a.ID)
	}
	_, plugin := install(t, f)
	derived := textInput("22222222-2222-4222-8222-222222222222", "Extracted words")
	derived.Type = "derived"
	derived.Source = map[string]json.RawMessage{"channel": json.RawMessage(`"plugin"`)}
	derived.Refs = []Ref{{Rel: "derived_from", ID: fileEvent(1, a.ID).ID}}
	out, err := f.service.RecordEventsAsPlugin(f.aliceCtx, plugin, RecordEventsInput{Events: []EventInput{derived}})
	if err != nil {
		t.Fatal(err)
	}
	created(t, out)
	entry, err := f.service.FileMetadataAsPlugin(f.aliceCtx, plugin, a.ID, nil)
	if err != nil || entry.ReferenceCount != 8 || len(entry.References) != 5 || entry.ReferencesComplete {
		t.Fatalf("preview %#v %v", entry, err)
	}
	refs, err := f.service.FileReferencesAsPlugin(f.aliceCtx, plugin, a.ID, FileCatalogInput{Limit: 3})
	if err != nil || len(refs.References) != 3 || refs.NextCursor == "" {
		t.Fatalf("refs %#v %v", refs, err)
	}
	total := len(refs.References)
	for refs.NextCursor != "" {
		refs, err = f.service.FileReferencesAsPlugin(f.aliceCtx, plugin, a.ID, FileCatalogInput{Limit: 3, Cursor: refs.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
		total += len(refs.References)
	}
	if total != 8 || refs.References[len(refs.References)-1].Relation != "derived_from" {
		t.Fatal("reference associations lost")
	}
	restricted, err := f.service.InstallPlugin(f.aliceCtx, f.project.ID, InstallPluginInput{Manifest: Manifest{ID: "log-only", Version: "1", Name: "Restricted", Permissions: Permissions{ReadEvents: []string{"log"}}}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.service.AuthenticatePlugin(restricted.Token)
	if err != nil {
		t.Fatal(err)
	}
	page, err := f.service.ListFilesAsPlugin(f.aliceCtx, p, FileCatalogInput{})
	if err != nil || len(page.Files) != 0 {
		t.Fatalf("restricted %#v %v", page, err)
	}
	if _, err = f.service.FileMetadataAsPlugin(f.aliceCtx, p, a.ID, nil); err == nil {
		t.Fatal("inaccessible file metadata leaked")
	}
}
func TestAttachmentBackfillIsBoundedAndDoesNotRewindCoverage(t *testing.T) {
	f, p := organizerFixture(t)
	a := catalogFile(t, f, "historical attachment")
	for i := 1; i <= 12; i++ {
		appendFile(t, f, i, a.ID)
	}
	// Simulate a pre-attachment publication with a main checkpoint already at12.
	store, err := f.service.noteStore(f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	cp := notes.Checkpoint{AfterSequence: 12, PromptVersion: "1"}
	if _, err = store.Sync(notes.SyncInput{ExpectedRevision: &zero, Files: organizedFiles(), Replace: true, Checkpoint: &cp}, "fixture"); err != nil {
		t.Fatal(err)
	}
	run, err := f.service.BeginNotesOrganization(f.aliceCtx, p, notes.BeginInput{PromptVersion: "2"})
	if err != nil || len(run.Events) != 10 || run.AfterSequence != 12 || run.ThroughSequence != 12 {
		t.Fatalf("backfill %#v %v", run, err)
	}
	input := notes.PublishInput{RunID: run.RunID, Files: organizedFiles()}
	for _, e := range run.Events {
		if !e.Backfill {
			t.Fatal("missing backfill marker")
		}
		input.Accounted = append(input.Accounted, notes.AccountedEvent{ID: e.ID, Disposition: "used"})
	}
	if _, err = f.service.PublishNotesOrganization(f.aliceCtx, p, input); err == nil {
		t.Fatal("missing attachment link accepted")
	}
	checkpoint, _ := store.Checkpoint()
	if checkpoint.AttachmentBackfillThroughSequence != 0 {
		t.Fatal("failed backfill advanced")
	}
	input.Files[len(input.Files)-1].Content += "\n[Attachment](edc-file://" + a.ID + ") [source](edc-event://" + run.Events[0].ID + ")\n"
	if _, err = f.service.PublishNotesOrganization(f.aliceCtx, p, input); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Export()
	checkpoint, _ = store.Checkpoint()
	if err != nil || snapshot.ThroughSequence != 12 || checkpoint.AttachmentBackfillThroughSequence != 10 {
		t.Fatalf("publication %#v %#v %v", snapshot, checkpoint, err)
	}
	next, err := f.service.BeginNotesOrganization(f.aliceCtx, p, notes.BeginInput{PromptVersion: "2"})
	if err != nil || len(next.Events) != 2 || next.ThroughSequence != 12 {
		t.Fatalf("next %#v %v", next, err)
	}
	input = notes.PublishInput{RunID: next.RunID}
	for _, e := range next.Events {
		input.Accounted = append(input.Accounted, notes.AccountedEvent{ID: e.ID, Disposition: "used"})
	}
	if _, err = f.service.PublishNotesOrganization(f.aliceCtx, p, input); err != nil {
		t.Fatal(err)
	}
	idle, err := f.service.BeginNotesOrganization(f.aliceCtx, p, notes.BeginInput{PromptVersion: "2"})
	if err != nil || !idle.Noop {
		t.Fatalf("idle %#v %v", idle, err)
	}
}
