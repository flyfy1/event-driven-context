package v2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"event-driven-context/internal/core"
)

const (
	eventOne   = "11111111-1111-4111-8111-111111111111"
	eventTwo   = "22222222-2222-4222-8222-222222222222"
	eventThree = "33333333-3333-4333-8333-333333333333"
)

type fixture struct {
	t                *testing.T
	root             string
	identity         *core.Store
	service          *Service
	alice, bob       core.User
	aliceCtx, bobCtx context.Context
	project, other   core.Project
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	identity, err := core.Open(filepath.Join(root, "identity.db"), filepath.Join(root, "legacy"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = identity.Close() })
	alice, err := identity.Register(context.Background(), core.Credentials{Username: "alice", Password: "password-123"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := identity.Register(context.Background(), core.Credentials{Username: "bob-user", Password: "password-123"})
	if err != nil {
		t.Fatal(err)
	}
	a := core.WithUser(context.Background(), alice.ID)
	b := core.WithUser(context.Background(), bob.ID)
	project, err := identity.CreateProject(a, core.ProjectInput{Name: "private"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := identity.CreateProject(b, core.ProjectInput{Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(identity, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return &fixture{t, root, identity, service, alice, bob, a, b, project, other}
}

func textInput(id, text string) EventInput {
	return EventInput{ID: id, Type: "note", Content: EventContent{Kind: "text", Text: text}, Metadata: map[string]json.RawMessage{"count": json.RawMessage(`9007199254740993`)}, Source: map[string]json.RawMessage{"channel": json.RawMessage(`"app"`)}}
}
func created(t *testing.T, result RecordEventsResult) EventWriteResult {
	t.Helper()
	if len(result.Results) != 1 || result.Results[0].Status != "created" {
		t.Fatalf("write result %#v", result)
	}
	return result.Results[0]
}
func install(t *testing.T, f *fixture) (InstallPluginResult, PluginPrincipal) {
	t.Helper()
	manifest := Manifest{ID: "project-brief", Version: "0.1.0", Name: "Project brief", Skills: []string{"skills/brief/SKILL.md"}, State: []StateDeclaration{{Key: "current"}, {Key: "_cursor"}}, SessionContext: []string{"current"}, Processor: json.RawMessage(`{"entry":{"type":"agent"}}`), Config: json.RawMessage(`{"prompt":"brief"}`), Permissions: Permissions{ReadEvents: []string{"note", "derived"}, WriteEvents: []string{"derived"}, WriteState: []string{"current", "_cursor"}}}
	out, err := f.service.InstallPlugin(f.aliceCtx, f.project.ID, InstallPluginInput{Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.service.AuthenticatePlugin(out.Token)
	if err != nil {
		t.Fatal(err)
	}
	return out, p
}

func TestEventDedupeRefsBatchAndRestart(t *testing.T) {
	f := newFixture(t)
	first, err := f.service.RecordEvents(f.aliceCtx, f.project.ID, RecordEventsInput{Events: []EventInput{textInput(eventOne, "budget 5")}})
	if err != nil {
		t.Fatal(err)
	}
	created(t, first)
	dup := textInput(eventOne, "budget 5")
	dup.Metadata = map[string]json.RawMessage{"count": json.RawMessage(" 9007199254740993 ")}
	dup.Refs = []Ref{}
	result, err := f.service.RecordEvents(f.aliceCtx, f.project.ID, RecordEventsInput{Events: []EventInput{dup, textInput(eventTwo, "other")}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Results[0].Status != "duplicate" || result.Results[1].Status != "created" {
		t.Fatalf("results %#v", result)
	}
	conflict, err := f.service.RecordEvents(f.aliceCtx, f.project.ID, RecordEventsInput{Events: []EventInput{textInput(eventOne, "changed")}})
	if err != nil {
		t.Fatal(err)
	}
	if conflict.Results[0].Status != "conflict" {
		t.Fatalf("conflict %#v", conflict)
	}
	bad := textInput(eventThree, "bad ref")
	foreign, err := f.service.RecordEvents(f.bobCtx, f.other.ID, RecordEventsInput{Events: []EventInput{textInput("44444444-4444-4444-8444-444444444444", "foreign")}})
	if err != nil {
		t.Fatal(err)
	}
	created(t, foreign)
	bad.Refs = []Ref{{Rel: "derived_from", ID: "44444444-4444-4444-8444-444444444444"}}
	invalid, err := f.service.RecordEvents(f.aliceCtx, f.project.ID, RecordEventsInput{Events: []EventInput{bad}})
	if err != nil {
		t.Fatal(err)
	}
	if invalid.Results[0].Status != "invalid" || invalid.Results[0].Error.Code != "invalid_ref" {
		t.Fatalf("invalid %#v", invalid)
	}
	if _, err = f.service.GetEvent(f.bobCtx, f.project.ID, eventOne); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("cross project read err=%v", err)
	}
	if err = f.service.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(f.identity, filepath.Join(f.root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	page, err := reopened.QueryEvents(f.aliceCtx, f.project.ID, QueryEventsInput{})
	if err != nil || len(page.Events) != 2 || page.Events[0].ID != eventOne {
		t.Fatalf("restart page=%#v err=%v", page, err)
	}
}

func TestCursorSnapshotAndTamper(t *testing.T) {
	f := newFixture(t)
	for _, in := range []EventInput{textInput(eventOne, "one"), textInput(eventTwo, "two"), textInput(eventThree, "three")} {
		r, e := f.service.RecordEvents(f.aliceCtx, f.project.ID, RecordEventsInput{Events: []EventInput{in}})
		if e != nil {
			t.Fatal(e)
		}
		created(t, r)
	}
	first, err := f.service.QueryEvents(f.aliceCtx, f.project.ID, QueryEventsInput{Limit: 1})
	if err != nil || first.NextCursor == "" || len(first.Events) != 1 {
		t.Fatalf("first %#v err=%v", first, err)
	}
	fourth := textInput("55555555-5555-4555-8555-555555555555", "later")
	later, err := f.service.RecordEvents(f.aliceCtx, f.project.ID, RecordEventsInput{Events: []EventInput{fourth}})
	if err != nil {
		t.Fatal(err)
	}
	created(t, later)
	raw, _ := base64.RawURLEncoding.DecodeString(first.NextCursor)
	var cursor map[string]any
	if json.Unmarshal(raw, &cursor) != nil {
		t.Fatal("cursor")
	}
	cursor["a"] = -1
	raw, _ = json.Marshal(cursor)
	tampered := base64.RawURLEncoding.EncodeToString(raw)
	if _, err = f.service.QueryEvents(f.aliceCtx, f.project.ID, QueryEventsInput{Limit: 1, Cursor: tampered}); err == nil {
		t.Fatal("tampered cursor accepted")
	}
	second, err := f.service.QueryEvents(f.aliceCtx, f.project.ID, QueryEventsInput{Limit: 1, Cursor: first.NextCursor})
	if err != nil || second.LatestSequence != first.LatestSequence || len(second.Events) != 1 || second.Events[0].Sequence != 2 {
		t.Fatalf("second %#v err=%v", second, err)
	}
}

func TestFilesDedupeReferenceAuthorizationAndCleanup(t *testing.T) {
	f := newFixture(t)
	data := []byte("plain text source")
	sum := sha256.Sum256(data)
	put := func(hash string) FileInfo {
		info, err := f.service.PutFile(f.aliceCtx, f.project.ID, FileUpload{Filename: "notes.txt", MediaType: "text/plain", SHA256: hash, SizeBytes: int64(len(data)), Reader: bytes.NewReader(data)})
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	file := put("")
	if again := put(hex.EncodeToString(sum[:])); again.ID != file.ID {
		t.Fatal("file dedupe failed")
	}
	unrefData := []byte("unused")
	unref, err := f.service.PutFile(f.aliceCtx, f.project.ID, FileUpload{Filename: "unused.txt", MediaType: "text/plain", SizeBytes: int64(len(unrefData)), Reader: bytes.NewReader(unrefData)})
	if err != nil {
		t.Fatal(err)
	}
	in := textInput(eventOne, "")
	in.Content = EventContent{Kind: "file", FileID: file.ID}
	r, err := f.service.RecordEvents(f.aliceCtx, f.project.ID, RecordEventsInput{Events: []EventInput{in}})
	if err != nil {
		t.Fatal(err)
	}
	created(t, r)
	event, err := f.service.GetEvent(f.aliceCtx, f.project.ID, eventOne)
	if err != nil || event.Content.SHA256 != file.SHA256 || event.Content.Filename != "notes.txt" {
		t.Fatalf("file event %#v err=%v", event, err)
	}
	_, reader, err := f.service.OpenFile(f.aliceCtx, f.project.ID, file.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(reader)
	_ = reader.Close()
	if !bytes.Equal(got, data) {
		t.Fatal("file bytes changed")
	}
	_, principal := install(t, f)
	if _, reader, err = f.service.OpenFileAsPlugin(context.Background(), principal, file.ID); err != nil {
		t.Fatal(err)
	} else {
		_ = reader.Close()
	}
	if _, _, err = f.service.OpenFileAsPlugin(context.Background(), principal, unref.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("plugin read unreferenced err=%v", err)
	}
	clean, err := f.service.CleanupUnreferencedFiles(context.Background(), time.Now().Add(time.Minute))
	if err != nil || clean.Removed != 1 {
		t.Fatalf("cleanup %#v err=%v", clean, err)
	}
	if _, _, err = f.service.OpenFile(f.aliceCtx, f.project.ID, unref.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("cleaned file err=%v", err)
	}
}

func TestPluginStateNamespacePauseRemovalAndRestart(t *testing.T) {
	f := newFixture(t)
	r, err := f.service.RecordEvents(f.aliceCtx, f.project.ID, RecordEventsInput{Events: []EventInput{textInput(eventOne, "source")}})
	if err != nil {
		t.Fatal(err)
	}
	seq := created(t, r).Sequence
	logInput := textInput(eventThree, "hidden log")
	logInput.Type = "log"
	logResult, err := f.service.RecordEvents(f.aliceCtx, f.project.ID, RecordEventsInput{Events: []EventInput{logInput}})
	if err != nil {
		t.Fatal(err)
	}
	created(t, logResult)
	installed, principal := install(t, f)
	derived := textInput(eventTwo, "derived")
	derived.Type = "derived"
	derived.Source = map[string]json.RawMessage{"channel": json.RawMessage(`"plugin"`)}
	derived.Refs = []Ref{{Rel: "derived_from", ID: eventOne}}
	written, err := f.service.RecordEventsAsPlugin(context.Background(), principal, RecordEventsInput{Events: []EventInput{derived}})
	if err != nil {
		t.Fatal(err)
	}
	created(t, written)
	stored, err := f.service.GetEvent(f.aliceCtx, f.project.ID, eventTwo)
	if err != nil || stored.Actor.Type != "plugin" || stored.Actor.ID != "project-brief" {
		t.Fatalf("plugin actor %#v err=%v", stored.Actor, err)
	}
	meta, err := f.service.ListMetadataAsPlugin(context.Background(), principal, MetadataInput{Key: "count"})
	if err != nil || len(meta.Values) != 1 || meta.Values[0].EventCount != 2 {
		t.Fatalf("plugin metadata leaked unreadable event %#v err=%v", meta, err)
	}
	if installed.Installation.Manifest.Processor == nil || installed.Installation.ConfigRevision != 1 {
		t.Fatalf("manifest lost %#v", installed.Installation)
	}
	zero := int64(0)
	public, err := f.service.PutStateAsPlugin(context.Background(), principal, PutStateInput{Key: "project-brief/current", ExpectedVersion: &zero, Content: StateContent{Format: "markdown", Text: "brief"}, BasedOnSequence: seq, Refs: []string{eventOne}})
	if err != nil || public.Version != 1 || public.Lag != 2 {
		t.Fatalf("state %#v err=%v", public, err)
	}
	if _, err = f.service.PutStateAsPlugin(context.Background(), principal, PutStateInput{Key: "other/current", Content: StateContent{Format: "text", Text: "x"}, BasedOnSequence: seq}); errorCode(err) != "forbidden_namespace" {
		t.Fatalf("namespace err=%v", err)
	}
	if _, err = f.service.PutStateAsPlugin(context.Background(), principal, PutStateInput{Key: "project-brief/current", ExpectedVersion: &zero, Content: StateContent{Format: "text", Text: "stale"}, BasedOnSequence: seq}); errorCode(err) != "state_version_mismatch" {
		t.Fatalf("CAS err=%v", err)
	}
	private, err := f.service.PutStateAsPlugin(context.Background(), principal, PutStateInput{Key: "project-brief/_cursor", Content: StateContent{Format: "text", Text: "1"}, BasedOnSequence: seq})
	if err != nil || private.Version != 1 {
		t.Fatal(err)
	}
	listed, err := f.service.ListState(f.aliceCtx, f.project.ID, ListStateInput{})
	if err != nil || len(listed.States) != 1 || listed.States[0].Key != "project-brief/current" {
		t.Fatalf("user list %#v err=%v", listed, err)
	}
	if _, err = f.service.GetState(f.aliceCtx, f.project.ID, GetStateInput{Keys: []string{"project-brief/_cursor"}}); errorCode(err) != "forbidden_namespace" {
		t.Fatalf("private read err=%v", err)
	}
	one := int64(1)
	updated, err := f.service.PutStateAsPlugin(context.Background(), principal, PutStateInput{Key: "project-brief/current", ExpectedVersion: &one, Content: StateContent{Format: "markdown", Text: "brief v2"}, BasedOnSequence: written.Results[0].Sequence, Refs: []string{eventOne, eventTwo}})
	if err != nil || updated.Version != 2 {
		t.Fatalf("state v2 %#v err=%v", updated, err)
	}
	history, err := f.service.GetState(f.aliceCtx, f.project.ID, GetStateInput{Keys: []string{"project-brief/current"}, Version: &one})
	if err != nil || len(history.States) != 1 || history.States[0].Version != 1 || history.States[0].Content.Text != "brief" {
		t.Fatalf("state history %#v err=%v", history, err)
	}
	if _, err = f.service.SetPluginStatus(f.aliceCtx, f.project.ID, "project-brief", "paused"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.QueryEventsAsPlugin(context.Background(), principal, QueryEventsInput{}); errorCode(err) != "plugin_paused" {
		t.Fatalf("stale principal bypassed pause: %v", err)
	}
	if _, err = f.service.AuthenticatePlugin(installed.Token); errorCode(err) != "plugin_paused" {
		t.Fatalf("paused token err=%v", err)
	}
	if err = f.service.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(f.identity, filepath.Join(f.root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if _, err = reopened.AuthenticatePlugin(installed.Token); errorCode(err) != "plugin_paused" {
		t.Fatalf("restart pause err=%v", err)
	}
	if _, err = reopened.SetPluginStatus(f.aliceCtx, f.project.ID, "project-brief", "active"); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.AuthenticatePlugin(installed.Token); err != nil {
		t.Fatalf("resumed token err=%v", err)
	}
	if _, err = reopened.RemovePlugin(f.aliceCtx, f.project.ID, "project-brief"); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.AuthenticatePlugin(installed.Token); !errors.Is(err, core.ErrUnauthenticated) {
		t.Fatalf("removed token err=%v", err)
	}
	states, err := reopened.ListState(f.aliceCtx, f.project.ID, ListStateInput{})
	if err != nil || len(states.States) != 1 {
		t.Fatalf("removed state not retained %#v %v", states, err)
	}
}

func TestUserDelegatedStateAndManualRun(t *testing.T) {
	f := newFixture(t)
	r, _ := f.service.RecordEvents(f.aliceCtx, f.project.ID, RecordEventsInput{Events: []EventInput{textInput(eventOne, "source")}})
	seq := created(t, r).Sequence
	_, principal := install(t, f)
	state, err := f.service.PutState(f.aliceCtx, f.project.ID, PutStateInput{Key: "project-brief/current", AsPluginID: "project-brief", Content: StateContent{Format: "text", Text: "managed"}, BasedOnSequence: seq, Refs: []string{eventOne}})
	if err != nil || state.Producer.PluginID != "project-brief" {
		t.Fatalf("delegated state %#v err=%v", state, err)
	}
	if _, err = f.service.PutState(f.bobCtx, f.project.ID, PutStateInput{}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("cross project put err=%v", err)
	}
	req, err := f.service.RequestManualRun(f.aliceCtx, f.project.ID, "project-brief", ManualRunInput{RequestID: "retry-1", SourceEventIDs: []string{eventOne}})
	if err != nil || req.Status != "accepted" {
		t.Fatalf("request %#v err=%v", req, err)
	}
	duplicate, err := f.service.RequestManualRun(f.aliceCtx, f.project.ID, "project-brief", ManualRunInput{RequestID: "retry-1", SourceEventIDs: []string{eventOne}})
	if err != nil || duplicate.CreatedAt != req.CreatedAt {
		t.Fatalf("request dedupe %#v err=%v", duplicate, err)
	}
	mailbox, err := f.service.GetStateAsPlugin(context.Background(), principal, GetStateInput{Keys: []string{"project-brief/_requests"}})
	if err != nil || len(mailbox.States) != 1 || !bytes.Contains(mailbox.States[0].Data, []byte("retry-1")) {
		t.Fatalf("mailbox %#v err=%v", mailbox, err)
	}
}

func TestPersistFailureDoesNotPublishMemoryEvent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission failure requires non-root")
	}
	f := newFixture(t)
	root := filepath.Join(f.root, "data", "v2")
	if err := os.Chmod(root, 0500); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.RecordEvents(f.aliceCtx, f.project.ID, RecordEventsInput{Events: []EventInput{textInput(eventOne, "must not publish")}})
	_ = os.Chmod(root, 0700)
	if err == nil {
		t.Fatal("expected persistence failure")
	}
	page, queryErr := f.service.QueryEvents(f.aliceCtx, f.project.ID, QueryEventsInput{})
	if queryErr != nil || len(page.Events) != 0 {
		t.Fatalf("failed write visible in memory %#v err=%v", page, queryErr)
	}
	if closeErr := f.service.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	reopened, reopenErr := New(f.identity, filepath.Join(f.root, "data"))
	if reopenErr != nil {
		t.Fatal(reopenErr)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	page, _ = reopened.QueryEvents(f.aliceCtx, f.project.ID, QueryEventsInput{})
	if len(page.Events) != 0 {
		t.Fatalf("failed write visible after restart %#v", page)
	}
}

func errorCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}
