package v2

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"event-driven-context/internal/notes"
)

func organizerFixture(t *testing.T) (*fixture, PluginPrincipal) {
	t.Helper()
	f := newFixture(t)
	out, err := f.service.InstallPlugin(f.aliceCtx, f.project.ID, InstallPluginInput{Manifest: Manifest{ID: "notes-indexer", Version: "0.1.0", Name: "Notes", Permissions: Permissions{ReadEvents: []string{"log", "note", "derived"}, OrganizeNotes: true}}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.service.AuthenticatePlugin(out.Token)
	if err != nil {
		t.Fatal(err)
	}
	return f, p
}
func organizedFiles() []notes.WriteFile {
	var out []notes.WriteFile
	for _, lens := range []string{"daily", "persons", "topics", "goals"} {
		out = append(out, notes.WriteFile{Path: lens + "/organization.md", Content: fmt.Sprintf("---\nschema_version: 1\nlens: %s\nbody_style: bullet_points\norganization_word_limit: 499\ndefault_note_word_limit: 999\n---\n- Keep concise.\n", lens)})
	}
	for _, name := range []string{"index.md", "daily/index.md", "persons/index.md", "topics/index.md", "topics/work/index.md", "topics/life/index.md", "goals/index.md", "goals/priorities.md"} {
		out = append(out, notes.WriteFile{Path: name, Content: "---\nid: note_" + strings.NewReplacer("/", "_", ".md", "").Replace(name) + "\ntitle: Notes\n---\n# Notes\n\n> Summary.\n"})
	}
	return out
}
func TestOrganizerPublishesCheckpointWithFrozenBatch(t *testing.T) {
	f, p := organizerFixture(t)
	_, err := f.service.RecordEvents(f.aliceCtx, f.project.ID, RecordEventsInput{Events: []EventInput{textInput(eventOne, "first")}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.service.BeginNotesOrganization(f.aliceCtx, p, notes.BeginInput{PromptVersion: "1"})
	if err != nil || run.ThroughSequence != 1 {
		t.Fatalf("begin %#v %v", run, err)
	}
	busy, err := f.service.BeginNotesOrganization(f.aliceCtx, p, notes.BeginInput{PromptVersion: "1"})
	if err != nil || busy.Reason != "already_running" {
		t.Fatalf("lease %#v %v", busy, err)
	}
	_, err = f.service.RecordEvents(f.aliceCtx, f.project.ID, RecordEventsInput{Events: []EventInput{textInput(eventTwo, "later")}})
	if err != nil {
		t.Fatal(err)
	}
	in := notes.PublishInput{RunID: run.RunID, Files: organizedFiles()}
	if _, err = f.service.PublishNotesOrganization(f.aliceCtx, p, in); err == nil {
		t.Fatal("unaccounted publication accepted")
	}
	store, _ := f.service.noteStore(f.project.ID)
	cp, _ := store.Checkpoint()
	snapshot, _ := store.Export()
	if cp.AfterSequence != 0 || snapshot.Revision != 0 {
		t.Fatal("failure advanced publication")
	}
	in.Accounted = []notes.AccountedEvent{{ID: eventOne, Disposition: "used"}}
	in.Files[len(in.Files)-1].Content += "\n[source](edc-event://" + eventTwo + ")\n"
	if _, err = f.service.PublishNotesOrganization(f.aliceCtx, p, in); err == nil {
		t.Fatal("future evidence accepted")
	}
	in.Files = organizedFiles()
	in.Files[len(in.Files)-1].Content += "\n[source](edc-event://" + eventOne + ")\n"
	out, err := f.service.PublishNotesOrganization(f.aliceCtx, p, in)
	if err != nil || out.Revision != 1 || out.ThroughSequence != 1 {
		t.Fatalf("publish %#v %v", out, err)
	}
	cp, err = store.Checkpoint()
	if err != nil || cp.AfterSequence != 1 || cp.RunID != run.RunID {
		t.Fatalf("checkpoint %#v %v", cp, err)
	}
	if _, err = f.service.PublishNotesOrganization(f.aliceCtx, p, in); err == nil {
		t.Fatal("reused lease")
	}
	next, err := f.service.BeginNotesOrganization(f.aliceCtx, p, notes.BeginInput{PromptVersion: "1"})
	if err != nil || len(next.Events) != 1 || next.Events[0].ID != eventTwo {
		t.Fatalf("next %#v %v", next, err)
	}
	if err = f.service.CancelNotesOrganization(f.aliceCtx, p, next.RunID); err != nil {
		t.Fatal(err)
	}
	retry, err := f.service.BeginNotesOrganization(f.aliceCtx, p, notes.BeginInput{PromptVersion: "1"})
	if err != nil || retry.RunID == next.RunID || retry.AfterSequence != 1 {
		t.Fatalf("retry %#v %v", retry, err)
	}
}
func TestOrganizerPermissionsExpiryAndPause(t *testing.T) {
	f, p := organizerFixture(t)
	_, ordinary := install(t, f)
	if _, err := f.service.BeginNotesOrganization(f.aliceCtx, ordinary, notes.BeginInput{PromptVersion: "1"}); err == nil {
		t.Fatal("ungranted plugin")
	}
	_, err := f.service.RecordEvents(f.aliceCtx, f.project.ID, RecordEventsInput{Events: []EventInput{textInput(eventOne, "first")}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.service.BeginNotesOrganization(f.aliceCtx, p, notes.BeginInput{PromptVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	lease := f.service.noteLeases[p.ProjectID]
	lease.Expires = time.Now().Add(-time.Second)
	f.service.noteLeases[p.ProjectID] = lease
	in := notes.PublishInput{RunID: run.RunID, Files: organizedFiles(), Accounted: []notes.AccountedEvent{{ID: eventOne, Disposition: "irrelevant"}}}
	if _, err = f.service.PublishNotesOrganization(f.aliceCtx, p, in); err == nil {
		t.Fatal("expired lease")
	}
	run, err = f.service.BeginNotesOrganization(f.aliceCtx, p, notes.BeginInput{PromptVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.SetPluginStatus(f.aliceCtx, f.project.ID, "notes-indexer", "paused"); err != nil {
		t.Fatal(err)
	}
	in.RunID = run.RunID
	if _, err = f.service.PublishNotesOrganization(f.aliceCtx, p, in); err == nil {
		t.Fatal("paused plugin publication")
	}
}
