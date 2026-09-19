package core

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// HubDeviceSchema stores encrypted delivery tokens separately from append-only content.
const HubDeviceSchema = `
CREATE TABLE IF NOT EXISTS hub_devices (
 id TEXT PRIMARY KEY,
 owner_id TEXT NOT NULL REFERENCES users(id),
 token_hash TEXT NOT NULL UNIQUE,
 token_cipher BLOB NOT NULL,
 platform TEXT NOT NULL CHECK(platform='android'),
 updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS hub_devices_owner ON hub_devices(owner_id);
`

// RegisterHubDevice binds a delivery token to the currently authenticated owner.
// Re-registration on account switch moves the device, so prior owners stop receiving notifications.
func (s *Store) RegisterHubDevice(ctx context.Context, token, platform string, key []byte) error {
	owner := UserID(ctx)
	if owner == "" {
		return ErrUnauthenticated
	}
	if platform != "android" || !hubText(token, 4096) {
		return Invalid("an Android delivery token is required")
	}
	id := newID("device")
	cipher, err := sealHubCredential(key, HubConnection{ID: id, OwnerID: owner}, []byte(token))
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO hub_devices(id,owner_id,token_hash,token_cipher,platform,updated_at)
 SELECT ?,?,?,?,?,? WHERE (SELECT COUNT(*) FROM hub_devices WHERE owner_id=?)<20 OR EXISTS(SELECT 1 FROM hub_devices WHERE token_hash=? AND owner_id=?)
 ON CONFLICT(token_hash) DO UPDATE SET id=excluded.id,owner_id=excluded.owner_id,token_cipher=excluded.token_cipher,updated_at=excluded.updated_at`, id, owner, digest([]byte(token)), cipher, platform, time.Now().Unix(), owner, digest([]byte(token)), owner)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return &Error{"rate_limited", "too many registered devices"}
	}
	return nil
}

func (s *Store) DeleteHubDevice(ctx context.Context, token string) error {
	if UserID(ctx) == "" {
		return ErrUnauthenticated
	}
	if !hubText(token, 4096) {
		return Invalid("delivery token is required")
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM hub_devices WHERE owner_id=? AND token_hash=?", UserID(ctx), digest([]byte(token)))
	return err
}

// HubDeviceTokens is server-internal; no API exposes decrypted tokens.
func (s *Store) HubDeviceTokens(ctx context.Context, owner string, key []byte) ([]string, error) {
	a, err := hubCipher(key)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,token_cipher FROM hub_devices WHERE owner_id=? ORDER BY updated_at DESC LIMIT 20", owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		var b []byte
		if err := rows.Scan(&id, &b); err != nil {
			return nil, err
		}
		if len(b) < a.NonceSize() {
			return nil, &Error{"service_unavailable", "device credential unavailable"}
		}
		p, err := a.Open(nil, b[:a.NonceSize()], b[a.NonceSize():], []byte(owner+":"+id))
		if err != nil {
			return nil, &Error{"service_unavailable", "device credential unavailable"}
		}
		out = append(out, string(p))
	}
	return out, rows.Err()
}

// HasHubDevice provides a non-sensitive registration check.
func (s *Store) HasHubDevice(ctx context.Context) (bool, error) {
	if UserID(ctx) == "" {
		return false, ErrUnauthenticated
	}
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM hub_devices WHERE owner_id=? LIMIT 1", UserID(ctx)).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return n == 1, err
}
