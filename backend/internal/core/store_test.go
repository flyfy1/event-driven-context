package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func user(t *testing.T, s *Store, name string) context.Context {
	t.Helper()
	u, e := s.Register(context.Background(), Credentials{name, "test-password-long-enough"})
	if e != nil {
		t.Fatal(e)
	}
	return WithUser(context.Background(), u.ID)
}
func textRecord(pid, text string) RecordInput {
	return RecordInput{ProjectID: pid, Content: ContentInput{Kind: "text", Text: &text}}
}
func meta(raw string) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		panic(err)
	}
	return m
}
func requireError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func TestSharedProjectAppendOnlyAndIsolation(t *testing.T) {
	s := openTest(t)
	alice := user(t, s, "alice")
	bob := user(t, s, "bob")
	outsider := user(t, s, "outsider")
	p, err := s.CreateProject(alice, ProjectInput{Name: "共同项目"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RecordEvent(bob, textRecord(p.ID, "not a member"))
	requireError(t, err, ErrNotFound)
	if _, err = s.AddMember(alice, MemberInput{p.ID, "bob"}); err != nil {
		t.Fatal(err)
	}
	_, err = s.AddMember(bob, MemberInput{p.ID, "outsider"})
	requireError(t, err, ErrForbidden)
	e, err := s.RecordEvent(bob, textRecord(p.ID, "Bob 的记录"))
	if err != nil {
		t.Fatal(err)
	}
	if e.ActorUserID != UserID(bob) || e.ActorUsername != "bob" {
		t.Fatalf("wrong author: %+v", e)
	}
	got, err := s.GetEvent(alice, EventRef{e.ID})
	if err != nil || *got.Content.Text != "Bob 的记录" {
		t.Fatalf("shared read: %+v %v", got, err)
	}
	_, err = s.GetEvent(outsider, EventRef{e.ID})
	requireError(t, err, ErrNotFound)
	_, err = s.QueryEvents(outsider, QueryInput{ProjectID: p.ID})
	requireError(t, err, ErrNotFound)
	_, err = s.ListMetadata(outsider, MetadataInput{ProjectID: p.ID})
	requireError(t, err, ErrNotFound)
	_, err = s.ListMembers(outsider, ProjectRef{p.ID})
	requireError(t, err, ErrNotFound)
	projects, err := s.ListProjects(outsider, Empty{})
	if err != nil || len(projects.Projects) != 0 {
		t.Fatal("project list leaks membership")
	}
	for _, table := range []string{"events", "files", "event_metadata"} {
		var count int
		if err = s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("event storage table remains in SQLite: %s", table)
		}
	}
	if _, err = os.Stat(s.eventPath(p.ID, e.ID)); err != nil {
		t.Fatalf("event manifest missing: %v", err)
	}
	got, err = s.GetEvent(alice, EventRef{e.ID})
	if err != nil || *got.Content.Text != "Bob 的记录" {
		t.Fatal("event mutated")
	}
}

func TestMetadataTypesFilteringTimeAndStablePagination(t *testing.T) {
	s := openTest(t)
	ctx := user(t, s, "alice")
	p, err := s.CreateProject(ctx, ProjectInput{Name: "metadata"})
	if err != nil {
		t.Fatal(err)
	}
	inputs := []string{`{"tag":"design","n":1,"enabled":true,"empty":null,"obj":{"b":2,"a":1},"tags":["a","b"],"quote\".key":"ok","large":9007199254740993}`, `{"tag":"design","n":"1","enabled":false}`, `{"tag":"code","n":1,"enabled":true}`}
	var saved []Event
	for i, m := range inputs {
		in := textRecord(p.ID, "content")
		in.Metadata = meta(m)
		in.OccurredAt = []string{"2026-09-01T08:00:00+08:00", "2026-09-02T00:00:00Z", "2026-09-03T00:00:00Z"}[i]
		e, err := s.RecordEvent(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		saved = append(saved, e)
	}
	cases := []struct {
		filter string
		exists []string
		count  int
	}{
		{`{"tag":"design","n":1}`, nil, 1}, {`{"n":"1"}`, nil, 1}, {`{"n":1}`, nil, 2}, {`{"enabled":true}`, nil, 2}, {`{"enabled":1}`, nil, 0}, {`{"empty":null}`, nil, 1}, {`{}`, []string{"empty"}, 1}, {`{"obj":{"a":1,"b":2}}`, nil, 1}, {`{"tags":["a","b"]}`, nil, 1}, {`{"quote\".key":"ok"}`, nil, 1}, {`{"large":9007199254740993}`, nil, 1}, {`{"large":9007199254740992}`, nil, 0},
	}
	for _, tc := range cases {
		q, err := s.QueryEvents(ctx, QueryInput{ProjectID: p.ID, Metadata: meta(tc.filter), MetadataExists: tc.exists})
		if err != nil || len(q.Events) != tc.count {
			t.Fatalf("filter %s: count %d want %d err %v", tc.filter, len(q.Events), tc.count, err)
		}
	}
	q, err := s.QueryEvents(ctx, QueryInput{ProjectID: p.ID, From: "2026-09-01T00:00:00Z", To: "2026-09-02T00:00:00Z", TimeField: "occurred_at"})
	if err != nil || len(q.Events) != 1 || q.Events[0].ID != saved[0].ID {
		t.Fatalf("[from,to) failed: %+v %v", q, err)
	}
	q, err = s.QueryEvents(ctx, QueryInput{ProjectID: p.ID, From: saved[0].RecordedAt, To: saved[1].RecordedAt})
	if err != nil || len(q.Events) != 1 {
		t.Fatal("recorded_at boundaries failed", err)
	}
	in := QueryInput{ProjectID: p.ID, Limit: 1}
	q, err = s.QueryEvents(ctx, in)
	if err != nil || q.NextCursor == "" {
		t.Fatal("missing cursor", err)
	}
	if _, err = s.RecordEvent(ctx, textRecord(p.ID, "after snapshot")); err != nil {
		t.Fatal(err)
	}
	seen := []string{q.Events[0].ID}
	for q.NextCursor != "" {
		in.Cursor = q.NextCursor
		q, err = s.QueryEvents(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range q.Events {
			seen = append(seen, e.ID)
		}
	}
	if len(seen) != 3 || seen[0] != saved[0].ID || seen[1] != saved[1].ID || seen[2] != saved[2].ID {
		t.Fatalf("unstable snapshot: %v", seen)
	}
	in.Metadata = meta(`{"n":1}`)
	if _, err = s.QueryEvents(ctx, in); err == nil {
		t.Fatal("cursor accepted changed filter")
	}
	fields, err := s.ListMetadata(ctx, MetadataInput{ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	var n MetadataField
	for _, f := range fields.Fields {
		if f.Key == "n" {
			n = f
		}
	}
	if n.EventCount != 3 || strings.Join(n.Types, ",") != "number,string" {
		t.Fatalf("wrong metadata inventory %+v", fields)
	}
	key := "n"
	values, err := s.ListMetadata(ctx, MetadataInput{ProjectID: p.ID, Key: &key, Limit: 1})
	if err != nil || !values.HasMore || len(values.Values) != 1 {
		t.Fatalf("metadata paging %+v %v", values, err)
	}
	values, err = s.ListMetadata(ctx, MetadataInput{ProjectID: p.ID, Key: &key, Limit: 1, Offset: 1})
	if err != nil || values.HasMore || len(values.Values) != 1 {
		t.Fatal("metadata second page", err)
	}
}

func TestFileValidationPersistenceAndAtomicity(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "persist.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	ctx := user(t, s, "alice")
	other := user(t, s, "other")
	p, err := s.CreateProject(ctx, ProjectInput{Name: "files"})
	if err != nil {
		t.Fatal(err)
	}
	bytes := []byte("会议记录\r\n原始文本\n")
	in := RecordInput{ProjectID: p.ID, Content: ContentInput{Kind: "file", File: &FileInput{Filename: "meeting.txt", MediaType: "text/plain; charset=utf-8", DataBase64: base64.StdEncoding.EncodeToString(bytes)}}, Metadata: meta(`{"source":"file"}`), IdempotencyKey: "file-once"}
	e, err := s.RecordEvent(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if e.Content.File.SHA256 != digest(bytes) {
		t.Fatal("hash mismatch")
	}
	_, err = s.GetFile(other, FileRef{e.Content.File.ID})
	requireError(t, err, ErrNotFound)
	for _, change := range []func(*FileInput){func(f *FileInput) { f.MediaType = "" }, func(f *FileInput) { f.MediaType = "application/pdf" }, func(f *FileInput) { f.MediaType = "text/plain; charset=gbk" }, func(f *FileInput) { f.DataBase64 = "@@" }, func(f *FileInput) { f.DataBase64 = base64.StdEncoding.EncodeToString([]byte{255, 0}) }, func(f *FileInput) { f.Filename = "../secret.txt" }, func(f *FileInput) { f.DataBase64 = base64.StdEncoding.EncodeToString(make([]byte, MaxContentBytes+1)) }} {
		bad := in
		f := *in.Content.File
		bad.Content.File = &f
		bad.IdempotencyKey = ""
		change(&f)
		if _, err = s.RecordEvent(ctx, bad); err == nil {
			t.Fatal("invalid file accepted")
		}
	}
	raw, err := os.ReadDir(s.filesDir(p.ID))
	if err != nil || len(raw) != 1 || raw[0].Name() != e.Content.File.ID {
		t.Fatalf("unexpected raw files: %+v %v", raw, err)
	}
	manifests, err := os.ReadDir(s.eventsDir(p.ID))
	if err != nil || len(manifests) != 1 || manifests[0].Name() != e.ID+".json" {
		t.Fatalf("unexpected event manifests: %+v %v", manifests, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetEvent(ctx, EventRef{e.ID})
	if err != nil || got.Content.File.ID != e.Content.File.ID {
		t.Fatal("event not durable", err)
	}
	file, err := s.GetFile(ctx, FileRef{e.Content.File.ID})
	if err != nil || file.DataBase64 != in.Content.File.DataBase64 {
		t.Fatal("file bytes not durable", err)
	}
}

func TestMigratesLegacySQLiteEventsIntoDataDirectory(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "auth.db")
	s, err := Open(dbPath, filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := user(t, s, "alice")
	p, err := s.CreateProject(ctx, ProjectInput{Name: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	bytes := []byte("legacy file")
	fileID, eventID := "file_legacy", "evt_legacy"
	for _, statement := range []string{
		`CREATE TABLE files (id TEXT PRIMARY KEY, project_id TEXT, filename TEXT, media_type TEXT, size_bytes INTEGER, sha256 TEXT, data BLOB)`,
		`CREATE TABLE events (seq INTEGER, id TEXT, project_id TEXT, actor_user_id TEXT, recorded_at TEXT, occurred_at TEXT, text_content TEXT, file_id TEXT, metadata TEXT, idempotency_key TEXT, request_hash TEXT)`,
	} {
		if _, err = s.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.db.Exec(`INSERT INTO files VALUES(?,?,?,?,?,?,?)`, fileID, p.ID, "legacy.txt", "text/plain", len(bytes), digest(bytes), bytes); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO events VALUES(?,?,?,?,?,?,?,?,?,?,?)`, 1, eventID, p.ID, UserID(ctx), now(), nil, nil, fileID, `{"source":"legacy"}`, "legacy-key", "legacy-hash"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dbPath, filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	event, err := s.GetEvent(ctx, EventRef{EventID: eventID})
	if err != nil || event.Content.File == nil || event.Content.File.ID != fileID || event.Provenance.Kind != ProvenanceOriginal {
		t.Fatalf("legacy event migration: %+v %v", event, err)
	}
	file, err := s.GetFile(ctx, FileRef{FileID: fileID})
	if err != nil || file.DataBase64 != base64.StdEncoding.EncodeToString(bytes) {
		t.Fatalf("legacy file migration: %+v %v", file, err)
	}
	for _, table := range []string{"events", "files", "event_metadata"} {
		var count int
		if err = s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("legacy table remains: %s", table)
		}
	}
}

func TestConcurrentIdempotencyAndTokenLifecycle(t *testing.T) {
	s := openTest(t)
	ctx := user(t, s, "alice")
	p, err := s.CreateProject(ctx, ProjectInput{Name: "retries"})
	if err != nil {
		t.Fatal(err)
	}
	in := textRecord(p.ID, "exactly once")
	in.IdempotencyKey = "request-1"
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	errs := make(chan error, 12)
	for range 12 {
		wg.Go(func() {
			e, err := s.RecordEvent(ctx, in)
			if err != nil {
				errs <- err
			} else {
				ids <- e.ID
			}
		})
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	one := ""
	for id := range ids {
		if one != "" && one != id {
			t.Fatal("duplicate event")
		}
		one = id
	}
	in.Content.Text = new(string)
	_, err = s.RecordEvent(ctx, in)
	requireError(t, err, ErrConflict)
	for _, creds := range []Credentials{{"missing", "test-password-long-enough"}, {"alice", "incorrect-password"}} {
		_, err = s.Login(ctx, creds)
		requireError(t, err, ErrUnauthenticated)
	}
	login, err := s.Login(ctx, Credentials{"alice", "test-password-long-enough"})
	if err != nil {
		t.Fatal(err)
	}
	id, _, err := s.Authenticate(ctx, login.Token)
	if err != nil || id != UserID(ctx) {
		t.Fatal("login token rejected", err)
	}
	var hash string
	if err = s.db.QueryRow("SELECT hash FROM tokens").Scan(&hash); err != nil || hash == login.Token || hash != digest([]byte(login.Token)) {
		t.Fatal("raw token stored")
	}
	if err = s.Logout(ctx, login.Token); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Authenticate(ctx, login.Token)
	requireError(t, err, ErrUnauthenticated)
	login, err = s.Login(ctx, Credentials{"alice", "test-password-long-enough"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE tokens SET expires_at=0"); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Authenticate(ctx, login.Token)
	requireError(t, err, ErrUnauthenticated)
}

func TestQueryBoundsResponseBytesWithoutLosingEvents(t *testing.T) {
	s := openTest(t)
	ctx := user(t, s, "alice")
	p, err := s.CreateProject(ctx, ProjectInput{Name: "large events"})
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, err := s.RecordEvent(ctx, textRecord(p.ID, strings.Repeat("a", MaxContentBytes))); err != nil {
			t.Fatal(err)
		}
	}
	in := QueryInput{ProjectID: p.ID, Limit: 100}
	seen := map[string]bool{}
	pages := 0
	for {
		out, err := s.QueryEvents(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		b, _ := json.Marshal(out.Events)
		if len(b) > MaxQueryPageBytes+1024 {
			t.Fatal("page byte budget exceeded")
		}
		for _, e := range out.Events {
			if seen[e.ID] {
				t.Fatal("duplicate page entry")
			}
			seen[e.ID] = true
		}
		if out.NextCursor == "" {
			break
		}
		in.Cursor = out.NextCursor
	}
	if pages < 2 || len(seen) != 5 {
		t.Fatalf("pagination lost events: pages=%d events=%d", pages, len(seen))
	}
}
