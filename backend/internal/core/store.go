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
	"sync"
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
	dataDir   string
	mu        sync.Mutex
}

func Open(path string, dataPaths ...string) (*Store, error) {
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
	dataDir := filepath.Join(filepath.Dir(path), "data")
	if len(dataPaths) > 0 && dataPaths[0] != "" {
		dataDir = dataPaths[0]
	}
	if dataDir != ":memory:" {
		abs, err := filepath.Abs(dataDir)
		if err != nil {
			return nil, err
		}
		dataDir = abs
		if err := os.MkdirAll(dataDir, 0700); err != nil {
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
	if err = ensureProjectTimezoneColumn(db); err != nil {
		db.Close()
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(rand.Text()), bcrypt.DefaultCost)
	if err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, dummyHash: hash, dataDir: dataDir}
	if err := s.migrateLegacyEventStorage(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
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

func ensureProjectTimezoneColumn(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(projects)")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "timezone" {
			found = true
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = db.Exec("ALTER TABLE projects ADD COLUMN timezone TEXT NOT NULL DEFAULT 'UTC'")
	return err
}

func normalizeProjectTimezone(value string, defaultUTC bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" && defaultUTC {
		return "UTC", nil
	}
	if value == "" || value == "Local" || len(value) > 255 || !utf8.ValidString(value) {
		return "", Invalid("timezone must be a valid IANA timezone")
	}
	if _, err := time.LoadLocation(value); err != nil {
		return "", Invalid("timezone must be a valid IANA timezone")
	}
	return value, nil
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

// RequireProjectMember exposes the existing identity boundary to sibling
// storage packages without exposing SQLite or duplicating membership queries.
func (s *Store) RequireProjectMember(ctx context.Context, projectID string) error {
	return s.requireMember(ctx, projectID)
}

// RequireProjectOwner is the narrow management check used by project-scoped
// extensions such as plugin installation.
func (s *Store) RequireProjectOwner(ctx context.Context, projectID string) error {
	if err := s.requireMember(ctx, projectID); err != nil {
		return err
	}
	var owner string
	if err := s.db.QueryRowContext(ctx, "SELECT owner_user_id FROM projects WHERE id=?", projectID).Scan(&owner); err != nil {
		return err
	}
	if owner != UserID(ctx) {
		return ErrForbidden
	}
	return nil
}
func (s *Store) CreateProject(ctx context.Context, in ProjectInput) (Project, error) {
	if UserID(ctx) == "" {
		return Project{}, ErrUnauthenticated
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 200 || len(in.Description) > 4000 {
		return Project{}, Invalid("name required (max 200 bytes); description max 4000 bytes")
	}
	timezone, err := normalizeProjectTimezone(in.Timezone, true)
	if err != nil {
		return Project{}, err
	}
	p := Project{ID: newID("prj"), Name: in.Name, Description: in.Description, Timezone: timezone, OwnerUserID: UserID(ctx), CreatedAt: now()}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Project{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO projects(id,name,description,timezone,owner_user_id,created_at) VALUES(?,?,?,?,?,?)", p.ID, p.Name, p.Description, p.Timezone, p.OwnerUserID, p.CreatedAt); err != nil {
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
	rows, err := s.db.QueryContext(ctx, "SELECT p.id,p.name,p.description,p.timezone,p.owner_user_id,p.created_at FROM projects p JOIN members m ON m.project_id=p.id WHERE m.user_id=? ORDER BY p.created_at,p.id", UserID(ctx))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Project
		if err = rows.Scan(&p.ID, &p.Name, &p.Description, &p.Timezone, &p.OwnerUserID, &p.CreatedAt); err != nil {
			return out, err
		}
		out.Projects = append(out.Projects, p)
	}
	return out, rows.Err()
}

func (s *Store) UpdateProjectTimezone(ctx context.Context, projectID, timezone string) (Project, error) {
	if err := s.RequireProjectOwner(ctx, projectID); err != nil {
		return Project{}, err
	}
	timezone, err := normalizeProjectTimezone(timezone, false)
	if err != nil {
		return Project{}, err
	}
	if _, err = s.db.ExecContext(ctx, "UPDATE projects SET timezone=? WHERE id=?", timezone, projectID); err != nil {
		return Project{}, err
	}
	return s.projectByID(ctx, projectID)
}

func (s *Store) projectByID(ctx context.Context, projectID string) (Project, error) {
	var project Project
	err := s.db.QueryRowContext(ctx, "SELECT id,name,description,timezone,owner_user_id,created_at FROM projects WHERE id=?", projectID).Scan(
		&project.ID, &project.Name, &project.Description, &project.Timezone, &project.OwnerUserID, &project.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return project, err
}

// ProjectTimezone is for project-scoped services that have already authorized
// their caller and need the current scheduling timezone from the identity store.
func (s *Store) ProjectTimezone(ctx context.Context, projectID string) (string, error) {
	var timezone string
	err := s.db.QueryRowContext(ctx, "SELECT timezone FROM projects WHERE id=?", projectID).Scan(&timezone)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if timezone == "" {
		return "UTC", nil
	}
	return timezone, nil
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
	if in.Action != nil {
		action := *in.Action
		action.SourceEventIDs, err = normalizeEventIDs(action.SourceEventIDs)
		if err != nil {
			return in, nil, err
		}
		action.SupersedesEventIDs, err = normalizeEventIDs(action.SupersedesEventIDs)
		if err != nil {
			return in, nil, err
		}
		switch action.Kind {
		case "confirmation":
			if len(action.SourceEventIDs) == 0 {
				return in, nil, Invalid("confirmation requires source_event_ids")
			}
		case "correction":
			if len(action.SupersedesEventIDs) == 0 {
				return in, nil, Invalid("correction requires supersedes_event_ids")
			}
		default:
			return in, nil, Invalid("action.kind must be confirmation or correction")
		}
		if len(action.SourceEventIDs)+len(action.SupersedesEventIDs) > 128 {
			return in, nil, Invalid("action max 128 event references")
		}
		in.Action = &action
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
		if err = validateFilename(f.Filename); err != nil {
			return in, nil, err
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
	if in.Action != nil && in.Content.Kind != "text" {
		return in, nil, Invalid("event actions require text content")
	}
	return in, data, nil
}

func normalizeEventIDs(ids []string) ([]string, error) {
	out := append([]string(nil), ids...)
	for _, id := range out {
		if id == "" || len(id) > 128 || !utf8.ValidString(id) || strings.ContainsAny(id, "/\\\x00\r\n") {
			return nil, Invalid("event references must be 1-128 safe UTF-8 bytes")
		}
	}
	sort.Strings(out)
	deduped := out[:0]
	for _, id := range out {
		if len(deduped) == 0 || deduped[len(deduped)-1] != id {
			deduped = append(deduped, id)
		}
	}
	return deduped, nil
}

func prepareMediaRecord(in MediaRecordInput) (RecordInput, []byte, error) {
	metadata, err := normalizeMetadata(in.Metadata)
	if err != nil {
		return RecordInput{}, nil, err
	}
	if len(in.IdempotencyKey) > 128 {
		return RecordInput{}, nil, Invalid("idempotency_key max 128 bytes")
	}
	if in.OccurredAt != "" {
		in.OccurredAt, err = normalizedTime(in.OccurredAt)
		if err != nil {
			return RecordInput{}, nil, err
		}
	}
	if err = validateFilename(in.Filename); err != nil {
		return RecordInput{}, nil, err
	}
	if len(in.Data) == 0 {
		return RecordInput{}, nil, Invalid("media file must not be empty")
	}
	if len(in.Data) > MaxMediaBytes {
		return RecordInput{}, nil, TooLarge("media file max 20 MiB")
	}
	mediaType, err := validateMedia(in.MediaType, in.Data)
	if err != nil {
		return RecordInput{}, nil, err
	}
	record := RecordInput{
		ProjectID:      in.ProjectID,
		Content:        ContentInput{Kind: "file", File: &FileInput{Filename: in.Filename, MediaType: mediaType}},
		Metadata:       metadata,
		OccurredAt:     in.OccurredAt,
		IdempotencyKey: in.IdempotencyKey,
	}
	return record, in.Data, nil
}

func validateFilename(filename string) error {
	if filename == "" || len(filename) > 255 || !utf8.ValidString(filename) || strings.ContainsAny(filename, "/\\\x00\r\n") || filename == "." || filename == ".." {
		return Invalid("filename must be a UTF-8 basename of 1-255 bytes")
	}
	return nil
}

func validateMedia(declared string, data []byte) (string, error) {
	mediaType, params, err := mime.ParseMediaType(declared)
	if err != nil || len(params) != 0 {
		return "", Invalid("declare a supported media_type: audio/mp4, audio/mpeg, or audio/wav")
	}
	switch mediaType {
	case "audio/mp4":
		if !isAACMP4(data) {
			return "", Invalid("file content does not match audio/mp4 AAC")
		}
	case "audio/mpeg":
		if !isMP3(data) {
			return "", Invalid("file content does not match audio/mpeg")
		}
	case "audio/wav", "audio/x-wav", "audio/wave", "audio/vnd.wave":
		if !isWAV(data) {
			return "", Invalid("file content does not match audio/wav")
		}
		mediaType = "audio/wav"
	default:
		return "", Invalid("declare a supported media_type: audio/mp4, audio/mpeg, or audio/wav")
	}
	return mediaType, nil
}
