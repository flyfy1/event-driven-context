package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

type Store struct {
	db        *sql.DB
	dummyHash []byte
}

func Open(path string) (*Store, error) {
	if path != ":memory:" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		path = abs
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err = f.Close(); err != nil {
			return nil, err
		}
		if err = os.Chmod(path, 0600); err != nil {
			return nil, err
		}
	}
	dsn := path
	if path != ":memory:" {
		dsn = (&url.URL{Scheme: "file", Path: path}).String()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// A single connection makes PRAGMAs apply consistently and serializes this MVP's writes.
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(rand.Text()), bcrypt.DefaultCost)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, dummyHash: hash}, nil
}
func (s *Store) Close() error    { return s.db.Close() }
func newID(prefix string) string { return prefix + "_" + strings.ToLower(rand.Text()) }
func digest(b []byte) string     { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

const timeFormat = "2006-01-02T15:04:05.000000000Z"

func now() string { return time.Now().UTC().Format(timeFormat) }
func normalizedTime(v string) (string, error) {
	t, e := time.Parse(time.RFC3339Nano, v)
	if e != nil {
		return "", Invalid("time must be RFC3339 with timezone")
	}
	return t.UTC().Format(timeFormat), nil
}

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,63}$`)

func (s *Store) Register(ctx context.Context, in Credentials) (User, error) {
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	if !usernamePattern.MatchString(in.Username) {
		return User{}, Invalid("username must be 3-64 lowercase letters, digits, _, . or -")
	}
	if len(in.Password) < 12 || len(in.Password) > 72 {
		return User{}, Invalid("password must be 12-72 bytes")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	u := User{newID("usr"), in.Username, now()}
	_, err = s.db.ExecContext(ctx, "INSERT INTO users VALUES(?,?,?,?)", u.ID, u.Username, hash, u.CreatedAt)
	if err != nil {
		var n int
		if s.db.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE username=?", u.Username).Scan(&n) == nil && n > 0 {
			return User{}, ErrConflict
		}
		return User{}, err
	}
	return u, nil
}
func (s *Store) Login(ctx context.Context, in Credentials) (LoginResult, error) {
	var u User
	var hash []byte
	err := s.db.QueryRowContext(ctx, "SELECT id,username,password_hash,created_at FROM users WHERE username=?", strings.ToLower(strings.TrimSpace(in.Username))).Scan(&u.ID, &u.Username, &hash, &u.CreatedAt)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return LoginResult{}, err
		}
		hash = s.dummyHash
	}
	passwordErr := bcrypt.CompareHashAndPassword(hash, []byte(in.Password))
	if err != nil || passwordErr != nil {
		return LoginResult{}, ErrUnauthenticated
	}
	token := "edc_" + rand.Text() + rand.Text()
	expires := time.Now().UTC().Add(30 * 24 * time.Hour)
	_, err = s.db.ExecContext(ctx, "INSERT INTO tokens VALUES(?,?,?)", digest([]byte(token)), u.ID, expires.Unix())
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{u, token, expires}, nil
}
func (s *Store) Authenticate(ctx context.Context, token string) (string, time.Time, error) {
	if len(token) > 256 || token == "" {
		return "", time.Time{}, ErrUnauthenticated
	}
	var id string
	var expires int64
	err := s.db.QueryRowContext(ctx, "SELECT user_id,expires_at FROM tokens WHERE hash=?", digest([]byte(token))).Scan(&id, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, ErrUnauthenticated
	}
	if err != nil {
		return "", time.Time{}, err
	}
	if expires <= time.Now().Unix() {
		return "", time.Time{}, ErrUnauthenticated
	}
	return id, time.Unix(expires, 0), nil
}
func (s *Store) Logout(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM tokens WHERE hash=?", digest([]byte(token)))
	return err
}
func (s *Store) Me(ctx context.Context) (User, error) {
	if UserID(ctx) == "" {
		return User{}, ErrUnauthenticated
	}
	var u User
	err := s.db.QueryRowContext(ctx, "SELECT id,username,created_at FROM users WHERE id=?", UserID(ctx)).Scan(&u.ID, &u.Username, &u.CreatedAt)
	return u, err
}
func (s *Store) requireMember(ctx context.Context, pid string) error {
	if UserID(ctx) == "" {
		return ErrUnauthenticated
	}
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM members WHERE project_id=? AND user_id=?", pid, UserID(ctx)).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
func (s *Store) CreateProject(ctx context.Context, in ProjectInput) (Project, error) {
	if UserID(ctx) == "" {
		return Project{}, ErrUnauthenticated
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 200 || len(in.Description) > 4000 {
		return Project{}, Invalid("name required (max 200 bytes); description max 4000 bytes")
	}
	p := Project{newID("prj"), in.Name, in.Description, UserID(ctx), now()}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Project{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO projects VALUES(?,?,?,?,?)", p.ID, p.Name, p.Description, p.OwnerUserID, p.CreatedAt); err != nil {
		return Project{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO members VALUES(?,?)", p.ID, p.OwnerUserID); err != nil {
		return Project{}, err
	}
	return p, tx.Commit()
}
func (s *Store) ListProjects(ctx context.Context, _ Empty) (Projects, error) {
	out := Projects{Projects: []Project{}}
	if UserID(ctx) == "" {
		return out, ErrUnauthenticated
	}
	rows, err := s.db.QueryContext(ctx, "SELECT p.id,p.name,p.description,p.owner_user_id,p.created_at FROM projects p JOIN members m ON m.project_id=p.id WHERE m.user_id=? ORDER BY p.created_at,p.id", UserID(ctx))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Project
		if err = rows.Scan(&p.ID, &p.Name, &p.Description, &p.OwnerUserID, &p.CreatedAt); err != nil {
			return out, err
		}
		out.Projects = append(out.Projects, p)
	}
	return out, rows.Err()
}
func (s *Store) AddMember(ctx context.Context, in MemberInput) (User, error) {
	if err := s.requireMember(ctx, in.ProjectID); err != nil {
		return User{}, err
	}
	var owner string
	err := s.db.QueryRowContext(ctx, "SELECT owner_user_id FROM projects WHERE id=?", in.ProjectID).Scan(&owner)
	if err != nil {
		return User{}, err
	}
	if owner != UserID(ctx) {
		return User{}, ErrForbidden
	}
	var u User
	err = s.db.QueryRowContext(ctx, "SELECT id,username,created_at FROM users WHERE username=?", strings.ToLower(strings.TrimSpace(in.Username))).Scan(&u.ID, &u.Username, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO members VALUES(?,?) ON CONFLICT DO NOTHING", in.ProjectID, u.ID)
	return u, err
}
func (s *Store) ListMembers(ctx context.Context, in ProjectRef) (Members, error) {
	out := Members{Members: []User{}}
	if err := s.requireMember(ctx, in.ProjectID); err != nil {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT u.id,u.username,u.created_at FROM users u JOIN members m ON m.user_id=u.id WHERE m.project_id=? ORDER BY u.username", in.ProjectID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var u User
		if err = rows.Scan(&u.ID, &u.Username, &u.CreatedAt); err != nil {
			return out, err
		}
		out.Members = append(out.Members, u)
	}
	return out, rows.Err()
}

// Canonical JSON ignores object key order and whitespace, preserves numbers exactly.
func canonical(raw json.RawMessage) (string, string, error) {
	if !json.Valid(raw) {
		return "", "", Invalid("metadata must contain valid JSON values")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return "", "", Invalid("invalid metadata")
	}
	typ := "null"
	switch v.(type) {
	case string:
		typ = "string"
	case json.Number:
		typ = "number"
	case bool:
		typ = "boolean"
	case []any:
		typ = "array"
	case map[string]any:
		typ = "object"
	}
	b, err := json.Marshal(v)
	return typ, string(b), err
}
func normalizeMetadata(m map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if len(m) > 128 {
		return nil, Invalid("metadata max 128 top-level fields")
	}
	out := map[string]json.RawMessage{}
	for k, v := range m {
		if len(k) == 0 || len(k) > 128 || !utf8.ValidString(k) {
			return nil, Invalid("metadata keys must be 1-128 UTF-8 bytes")
		}
		_, c, err := canonical(v)
		if err != nil {
			return nil, err
		}
		out[k] = json.RawMessage(c)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, Invalid("invalid metadata")
	}
	if len(b) > MaxMetadataBytes {
		return nil, Invalid("metadata max 32 KiB")
	}
	return out, nil
}
func prepareRecord(in RecordInput) (RecordInput, []byte, error) {
	var err error
	in.Metadata, err = normalizeMetadata(in.Metadata)
	if err != nil {
		return in, nil, err
	}
	if len(in.IdempotencyKey) > 128 {
		return in, nil, Invalid("idempotency_key max 128 bytes")
	}
	if in.OccurredAt != "" {
		in.OccurredAt, err = normalizedTime(in.OccurredAt)
		if err != nil {
			return in, nil, err
		}
	}
	var data []byte
	switch in.Content.Kind {
	case "text":
		if in.Content.Text == nil || in.Content.File != nil {
			return in, nil, Invalid("text content requires text and no file")
		}
		if len(*in.Content.Text) > MaxContentBytes || !utf8.ValidString(*in.Content.Text) {
			return in, nil, Invalid("text must be UTF-8, max 1 MiB")
		}
	case "file":
		if in.Content.File == nil || in.Content.Text != nil {
			return in, nil, Invalid("file content requires file and no text")
		}
		f := *in.Content.File
		in.Content.File = &f
		if f.Filename == "" || len(f.Filename) > 255 || strings.ContainsAny(f.Filename, "/\\\x00\r\n") {
			return in, nil, Invalid("filename must be a basename of 1-255 bytes")
		}
		media, params, e := mime.ParseMediaType(f.MediaType)
		if e != nil || !strings.HasPrefix(media, "text/") {
			return in, nil, Invalid("declare a supported media_type: text/*; binary files are not yet accepted")
		}
		for k, v := range params {
			if k != "charset" || !strings.EqualFold(v, "utf-8") {
				return in, nil, Invalid("only UTF-8 text files are accepted")
			}
		}
		if len(f.DataBase64) > base64.StdEncoding.EncodedLen(MaxContentBytes)+2 {
			return in, nil, Invalid("file max 1 MiB")
		}
		data, e = base64.StdEncoding.Strict().DecodeString(f.DataBase64)
		if e != nil || len(data) > MaxContentBytes || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return in, nil, Invalid("file must contain valid base64-encoded UTF-8 text, no NUL bytes, max 1 MiB")
		}
		f.MediaType = media
		f.DataBase64 = base64.StdEncoding.EncodeToString(data)
	default:
		return in, nil, Invalid("content.kind must be text or file")
	}
	return in, data, nil
}

func (s *Store) RecordEvent(ctx context.Context, in RecordInput) (Event, error) {
	if err := s.requireMember(ctx, in.ProjectID); err != nil {
		return Event{}, err
	}
	in, data, err := prepareRecord(in)
	if err != nil {
		return Event{}, err
	}
	b, _ := json.Marshal(in)
	requestHash := digest(b)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback()
	if in.IdempotencyKey != "" {
		var id, hash string
		err = tx.QueryRowContext(ctx, "SELECT id,request_hash FROM events WHERE project_id=? AND actor_user_id=? AND idempotency_key=?", in.ProjectID, UserID(ctx), in.IdempotencyKey).Scan(&id, &hash)
		if err == nil {
			tx.Rollback()
			if hash != requestHash {
				return Event{}, ErrConflict
			}
			return s.GetEvent(ctx, EventRef{id})
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return Event{}, err
		}
	}
	e := Event{ID: newID("evt"), ProjectID: in.ProjectID, ActorUserID: UserID(ctx), RecordedAt: now(), OccurredAt: in.OccurredAt, Content: Content{Kind: in.Content.Kind, Text: in.Content.Text}, Metadata: in.Metadata}
	var fileID any
	if in.Content.File != nil {
		f := in.Content.File
		info := FileInfo{newID("file"), f.Filename, f.MediaType, len(data), digest(data)}
		e.Content.File = &info
		fileID = info.ID
		if _, err = tx.ExecContext(ctx, "INSERT INTO files VALUES(?,?,?,?,?,?,?)", info.ID, in.ProjectID, info.Filename, info.MediaType, info.SizeBytes, info.SHA256, data); err != nil {
			return Event{}, err
		}
	}
	meta, _ := json.Marshal(in.Metadata)
	if _, err = tx.ExecContext(ctx, "INSERT INTO events(id,project_id,actor_user_id,recorded_at,occurred_at,text_content,file_id,metadata,idempotency_key,request_hash) VALUES(?,?,?,?,?,?,?,?,?,?)", e.ID, e.ProjectID, e.ActorUserID, e.RecordedAt, nullString(e.OccurredAt), in.Content.Text, fileID, string(meta), nullString(in.IdempotencyKey), requestHash); err != nil {
		return Event{}, err
	}
	for k, v := range in.Metadata {
		typ, val, err := canonical(v)
		if err != nil {
			return Event{}, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO event_metadata VALUES(?,?,?,?)", e.ID, k, typ, val); err != nil {
			return Event{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Event{}, err
	}
	return s.GetEvent(ctx, EventRef{e.ID})
}
func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

const eventSelect = `SELECT e.id,e.project_id,e.actor_user_id,u.username,e.recorded_at,e.occurred_at,e.text_content,e.metadata,f.id,f.filename,f.media_type,f.size_bytes,f.sha256 FROM events e JOIN users u ON u.id=e.actor_user_id LEFT JOIN files f ON f.id=e.file_id `

type scanner interface{ Scan(...any) error }

func scanEvent(row scanner) (Event, error) {
	var e Event
	var occurred, txt, fid, name, media, sha sql.NullString
	var size sql.NullInt64
	var meta string
	err := row.Scan(&e.ID, &e.ProjectID, &e.ActorUserID, &e.ActorUsername, &e.RecordedAt, &occurred, &txt, &meta, &fid, &name, &media, &size, &sha)
	if err != nil {
		return e, err
	}
	e.OccurredAt = occurred.String
	e.Content.Kind = "text"
	if txt.Valid {
		e.Content.Text = &txt.String
	}
	if fid.Valid {
		e.Content.Kind = "file"
		e.Content.File = &FileInfo{fid.String, name.String, media.String, int(size.Int64), sha.String}
	}
	err = json.Unmarshal([]byte(meta), &e.Metadata)
	return e, err
}
func (s *Store) GetEvent(ctx context.Context, in EventRef) (Event, error) {
	if UserID(ctx) == "" {
		return Event{}, ErrUnauthenticated
	}
	e, err := scanEvent(s.db.QueryRowContext(ctx, eventSelect+"WHERE e.id=? AND EXISTS (SELECT 1 FROM members m WHERE m.project_id=e.project_id AND m.user_id=?)", in.EventID, UserID(ctx)))
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	return e, err
}
func (s *Store) GetFile(ctx context.Context, in FileRef) (FileResult, error) {
	if UserID(ctx) == "" {
		return FileResult{}, ErrUnauthenticated
	}
	var out FileResult
	var data []byte
	err := s.db.QueryRowContext(ctx, "SELECT f.id,f.filename,f.media_type,f.size_bytes,f.sha256,f.data FROM files f WHERE f.id=? AND EXISTS(SELECT 1 FROM members m WHERE m.project_id=f.project_id AND m.user_id=?)", in.FileID, UserID(ctx)).Scan(&out.File.ID, &out.File.Filename, &out.File.MediaType, &out.File.SizeBytes, &out.File.SHA256, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	out.DataBase64 = base64.StdEncoding.EncodeToString(data)
	return out, err
}

type eventCursor struct {
	After     int64  `json:"after"`
	Snapshot  int64  `json:"snapshot"`
	QueryHash string `json:"query"`
}

func (s *Store) QueryEvents(ctx context.Context, in QueryInput) (Events, error) {
	out := Events{Events: []Event{}}
	if err := s.requireMember(ctx, in.ProjectID); err != nil {
		return out, err
	}
	if in.Limit == 0 {
		in.Limit = 50
	}
	if in.Limit < 1 || in.Limit > 100 {
		return out, Invalid("limit must be 1-100")
	}
	if in.TimeField == "" {
		in.TimeField = "recorded_at"
	}
	if in.TimeField != "recorded_at" && in.TimeField != "occurred_at" {
		return out, Invalid("time_field must be recorded_at or occurred_at")
	}
	var err error
	for _, p := range []*string{&in.From, &in.To} {
		if *p != "" {
			*p, err = normalizedTime(*p)
			if err != nil {
				return out, err
			}
		}
	}
	if in.From != "" && in.To != "" && in.From >= in.To {
		return out, Invalid("from must be before to")
	}
	in.Metadata, err = normalizeMetadata(in.Metadata)
	if err != nil {
		return out, err
	}
	if len(in.MetadataExists) > 128 {
		return out, Invalid("metadata_exists max 128 keys")
	}
	for _, k := range in.MetadataExists {
		if len(k) == 0 || len(k) > 128 {
			return out, Invalid("metadata key must be 1-128 bytes")
		}
	}
	in.MetadataExists = append([]string(nil), in.MetadataExists...)
	sort.Strings(in.MetadataExists)
	rawCursor := in.Cursor
	in.Cursor = ""
	queryBytes, _ := json.Marshal(in)
	queryHash := digest(queryBytes)
	cur := eventCursor{QueryHash: queryHash}
	if rawCursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(rawCursor)
		if e != nil || len(b) > 1024 || json.Unmarshal(b, &cur) != nil || cur.QueryHash != queryHash || cur.After < 0 || cur.Snapshot < cur.After {
			return out, Invalid("invalid cursor or changed query parameters")
		}
	} else if err = s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM events WHERE project_id=?", in.ProjectID).Scan(&cur.Snapshot); err != nil {
		return out, err
	}
	where := "WHERE e.project_id=? AND e.seq>? AND e.seq<=?"
	args := []any{in.ProjectID, cur.After, cur.Snapshot}
	if in.From != "" {
		where += " AND e." + in.TimeField + ">=?"
		args = append(args, in.From)
	}
	if in.To != "" {
		where += " AND e." + in.TimeField + "<?"
		args = append(args, in.To)
	}
	for k, v := range in.Metadata {
		typ, val, _ := canonical(v)
		where += " AND EXISTS(SELECT 1 FROM event_metadata em WHERE em.event_id=e.id AND em.key=? AND em.type=? AND em.value=?)"
		args = append(args, k, typ, val)
	}
	for _, k := range in.MetadataExists {
		where += " AND EXISTS(SELECT 1 FROM event_metadata em WHERE em.event_id=e.id AND em.key=?)"
		args = append(args, k)
	}
	args = append(args, in.Limit+1)
	rows, err := s.db.QueryContext(ctx, eventSelect+where+" ORDER BY e.seq ASC LIMIT ?", args...)
	if err != nil {
		return out, err
	}
	pageBytes, hasMore := 0, false
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		encoded, err := json.Marshal(e)
		if err != nil {
			rows.Close()
			return out, err
		}
		// limit is an upper bound. Bound page bytes too, while always allowing
		// one complete event even if JSON escaping makes it exceed the budget.
		if len(out.Events) == in.Limit || (len(out.Events) > 0 && pageBytes+len(encoded) > MaxQueryPageBytes) {
			hasMore = true
			break
		}
		pageBytes += len(encoded)
		out.Events = append(out.Events, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if hasMore {
		if err = s.db.QueryRowContext(ctx, "SELECT seq FROM events WHERE id=?", out.Events[len(out.Events)-1].ID).Scan(&cur.After); err != nil {
			return out, err
		}
		b, _ := json.Marshal(cur)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	return out, nil
}
func (s *Store) ListMetadata(ctx context.Context, in MetadataInput) (MetadataResult, error) {
	out := MetadataResult{}
	if err := s.requireMember(ctx, in.ProjectID); err != nil {
		return out, err
	}
	if in.Limit == 0 {
		in.Limit = 50
	}
	if in.Limit < 1 || in.Limit > 100 || in.Offset < 0 {
		return out, Invalid("limit must be 1-100; offset must be nonnegative")
	}
	if in.Key != nil {
		if len(*in.Key) == 0 || len(*in.Key) > 128 {
			return out, Invalid("key must be 1-128 bytes")
		}
		rows, err := s.db.QueryContext(ctx, "SELECT em.value,COUNT(*) FROM event_metadata em JOIN events e ON e.id=em.event_id WHERE e.project_id=? AND em.key=? GROUP BY em.value ORDER BY em.value LIMIT ? OFFSET ?", in.ProjectID, *in.Key, in.Limit+1, in.Offset)
		if err != nil {
			return out, err
		}
		defer rows.Close()
		for rows.Next() {
			var v MetadataValue
			var raw string
			if err = rows.Scan(&raw, &v.EventCount); err != nil {
				return out, err
			}
			v.Value = json.RawMessage(raw)
			out.Values = append(out.Values, v)
		}
		if len(out.Values) > in.Limit {
			out.HasMore = true
			out.Values = out.Values[:in.Limit]
		}
		return out, rows.Err()
	}
	rows, err := s.db.QueryContext(ctx, "SELECT em.key,GROUP_CONCAT(DISTINCT em.type),COUNT(*) FROM event_metadata em JOIN events e ON e.id=em.event_id WHERE e.project_id=? GROUP BY em.key ORDER BY em.key LIMIT ? OFFSET ?", in.ProjectID, in.Limit+1, in.Offset)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var f MetadataField
		var types string
		if err = rows.Scan(&f.Key, &types, &f.EventCount); err != nil {
			return out, err
		}
		f.Types = strings.Split(types, ",")
		sort.Strings(f.Types)
		out.Fields = append(out.Fields, f)
	}
	if len(out.Fields) > in.Limit {
		out.HasMore = true
		out.Fields = out.Fields[:in.Limit]
	}
	return out, rows.Err()
}

var _ Backend = (*Store)(nil)
