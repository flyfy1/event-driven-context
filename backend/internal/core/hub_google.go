package core

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"errors"
	"event-driven-context/internal/hubconnectors"
	"time"
)

const HubGoogleSchema = `CREATE TABLE IF NOT EXISTS hub_google_states (
 state_hash TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id),
 provider_id TEXT NOT NULL, display_name TEXT NOT NULL, verifier BLOB NOT NULL,
 expires_at INTEGER NOT NULL
);`

type HubGoogleState struct {
	State       string
	OwnerID     string
	ProviderID  string
	DisplayName string
	Verifier    string
}

func (s *Store) StartHubGoogle(ctx context.Context, providerID, displayName string, key []byte) (HubGoogleState, error) {
	owner := UserID(ctx)
	if owner == "" {
		return HubGoogleState{}, ErrUnauthenticated
	}
	if len(hubconnectors.GoogleScopes(providerID)) == 0 || !hubText(displayName, 150) {
		return HubGoogleState{}, Invalid("Google provider and display name required")
	}
	state := HubGoogleState{State: rand.Text() + rand.Text(), OwnerID: owner, ProviderID: providerID, DisplayName: displayName, Verifier: rand.Text() + rand.Text()}
	hash := digest([]byte(state.State))
	sealed, err := sealHubCredential(key, HubConnection{ID: "oauth_" + hash, OwnerID: owner}, []byte(state.Verifier))
	if err != nil {
		return HubGoogleState{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HubGoogleState{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM hub_google_states WHERE expires_at<=?", time.Now().Unix()); err != nil {
		return HubGoogleState{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO hub_google_states(state_hash,owner_id,provider_id,display_name,verifier,expires_at)
 SELECT ?,?,?,?,?,? WHERE (SELECT COUNT(*) FROM hub_google_states WHERE owner_id=?)<10`, hash, owner, providerID, displayName, sealed, time.Now().Add(10*time.Minute).Unix(), owner)
	if err != nil {
		return HubGoogleState{}, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return HubGoogleState{}, &Error{"rate_limited", "too many pending Google authorizations"}
	}
	if err = tx.Commit(); err != nil {
		return HubGoogleState{}, err
	}
	return state, nil
}

// ConsumeHubGoogle atomically consumes even expired or denied callbacks. Raw
// state is never stored; the verifier is encrypted and bound to owner + state.
func (s *Store) ConsumeHubGoogle(ctx context.Context, state string, key []byte) (HubGoogleState, error) {
	if len(state) < 32 || len(state) > 512 {
		return HubGoogleState{}, Invalid("invalid or expired Google authorization")
	}
	hash := digest([]byte(state))
	out := HubGoogleState{}
	var encrypted []byte
	var expires int64
	err := s.db.QueryRowContext(ctx, `DELETE FROM hub_google_states WHERE state_hash=? RETURNING owner_id,provider_id,display_name,verifier,expires_at`, hash).Scan(&out.OwnerID, &out.ProviderID, &out.DisplayName, &encrypted, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return out, Invalid("invalid or expired Google authorization")
	}
	if err != nil {
		return out, err
	}
	if expires <= time.Now().Unix() {
		return out, Invalid("invalid or expired Google authorization")
	}
	a, err := hubCipher(key)
	if err != nil {
		return out, err
	}
	if len(encrypted) < a.NonceSize() {
		return out, Invalid("invalid Google authorization")
	}
	b, err := a.Open(nil, encrypted[:a.NonceSize()], encrypted[a.NonceSize():], []byte(out.OwnerID+":oauth_"+hash))
	if err != nil {
		return out, Invalid("invalid Google authorization")
	}
	out.Verifier = string(b)
	return out, nil
}

// UpdateHubCredential cannot revive a disconnected connection after an in-flight
// refresh. A new owner-authorized OAuth callback uses SaveHubGoogleConnection.
func (s *Store) UpdateHubCredential(ctx context.Context, c HubConnection, expectedSecret, secret string, key []byte) error {
	if len(secret) == 0 || len(secret) > 16384 {
		return Invalid("invalid provider credential")
	}
	var previous []byte
	err := s.db.QueryRowContext(ctx, "SELECT credential FROM hub_connections WHERE id=? AND owner_id=? AND provider_id=? AND status='configured'", c.ID, c.OwnerID, c.ProviderID).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	a, err := hubCipher(key)
	if err != nil {
		return err
	}
	if len(previous) < a.NonceSize() {
		return ErrNotFound
	}
	plain, err := a.Open(nil, previous[:a.NonceSize()], previous[a.NonceSize():], []byte(c.OwnerID+":"+c.ID))
	if err != nil {
		return ErrNotFound
	}
	if subtle.ConstantTimeCompare(plain, []byte(expectedSecret)) != 1 {
		return ErrConflict
	}
	b, err := sealHubCredential(key, c, []byte(secret))
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, "UPDATE hub_connections SET credential=? WHERE id=? AND owner_id=? AND provider_id=? AND status='configured' AND credential=?", b, c.ID, c.OwnerID, c.ProviderID, previous)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

// SaveHubGoogleConnection creates a connection or reconnects the same verified
// account without changing its stable connection ID. Credential replacement
// revokes prior pending and approved grants through AddHubConnection.
func (s *Store) SaveHubGoogleConnection(ctx context.Context, c HubConnection, secret string, key []byte) (HubConnection, error) {
	c.OwnerID = UserID(ctx)
	if c.OwnerID == "" {
		return c, ErrUnauthenticated
	}
	if len(hubconnectors.GoogleScopes(c.ProviderID)) == 0 || !hubText(c.AccountID, 254) || !hubText(c.DisplayName, 150) || len(secret) == 0 || len(secret) > 16384 {
		return c, Invalid("invalid Google connection")
	}
	return s.AddHubConnection(ctx, c, secret, key)
}
