package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	if _, err = db.Exec(schema + "\n" + hubSchema + "\n" + HubDeviceSchema + "\n" + HubGoogleSchema); err != nil {
		db.Close()
		return nil, err
	}
	if err = ensureProjectTimezoneColumn(db); err != nil {
		db.Close()
		return nil, err
	}
	if err = ensureProjectOwners(db); err != nil {
		db.Close()
		return nil, err
	}
	if err = ensureIntegIdentitySchema(db); err != nil {
		db.Close()
		return nil, err
	}
	if err = ensureOAuthAccessExpiryColumn(db); err != nil {
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

func ensureProjectOwners(db *sql.DB) error {
	_, err := db.Exec(`
		INSERT INTO project_owners(project_id,user_id)
		SELECT p.id,p.owner_user_id
		FROM projects p
		JOIN members m ON m.project_id=p.id AND m.user_id=p.owner_user_id
		WHERE NOT EXISTS (
			SELECT 1 FROM project_owners owners WHERE owners.project_id=p.id
		)
		ON CONFLICT DO NOTHING
	`)
	return err
}

func ensureIntegIdentitySchema(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(users)")
	if err != nil {
		return err
	}
	foundEmail := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "email" {
			foundEmail = true
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if !foundEmail {
		if _, err = db.Exec("ALTER TABLE users ADD COLUMN email TEXT"); err != nil {
			return err
		}
	}
	_, err = db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS users_email_unique ON users(email) WHERE email IS NOT NULL;
		CREATE TABLE IF NOT EXISTS integ_identities (
			issuer TEXT NOT NULL,
			subject TEXT NOT NULL,
			user_id TEXT NOT NULL REFERENCES users(id),
			email TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY(issuer, subject),
			UNIQUE(issuer, user_id)
		);
	`)
	return err
}

func ensureOAuthAccessExpiryColumn(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(oauth_authorization_codes)")
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
		if name == "access_expires_at" {
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
	_, err = db.Exec("ALTER TABLE oauth_authorization_codes ADD COLUMN access_expires_at INTEGER NOT NULL DEFAULT 0")
	return err
}
