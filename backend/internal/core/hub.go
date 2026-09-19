package core

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// Hub authorization metadata shares the identity database, not the event store.
//
//go:embed hub_schema.sql
var hubSchema string

type HubAgent struct {
	ID               string    `json:"id"`
	OwnerID          string    `json:"-"`
	Name             string    `json:"name"`
	VerificationCode string    `json:"verification_code"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
	ExpiresAt        time.Time `json:"expires_at"`
}
type HubAgentRegistration struct {
	HubAgent
	Token string `json:"token"`
}
type HubConnection struct {
	ID          string `json:"id"`
	OwnerID     string `json:"-"`
	ProviderID  string `json:"provider_id"`
	AccountID   string `json:"account_id"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
}
type HubRequest struct {
	ID             string    `json:"id"`
	AgentID        string    `json:"agent_id"`
	AgentName      string    `json:"agent_name"`
	ConnectionID   string    `json:"connection_id"`
	ConnectionName string    `json:"connection_name"`
	Operation      string    `json:"operation"`
	Reason         string    `json:"reason"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}
type HubOverview struct {
	Agents      []HubAgent      `json:"agents"`
	Requests    []HubRequest    `json:"requests"`
	Connections []HubConnection `json:"connections"`
}

func hubText(s string, max int) bool {
	return strings.TrimSpace(s) != "" && len(s) <= max && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}
func hubEffectiveStatus(status string, expires time.Time) string {
	if (status == "approved" || status == "pending") && !expires.After(time.Now()) {
		return "expired"
	}
	return status
}
func (s *Store) RegisterHubAgent(ctx context.Context, owner, name string) (HubAgentRegistration, error) {
	if !hubText(owner, 254) || !hubText(name, 100) {
		return HubAgentRegistration{}, Invalid("owner and agent name are required")
	}
	var uid string
	if err := s.db.QueryRowContext(ctx, "SELECT id FROM users WHERE username=?", owner).Scan(&uid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return HubAgentRegistration{}, ErrNotFound
		}
		return HubAgentRegistration{}, err
	}
	t := time.Now().UTC().Truncate(time.Second)
	a := HubAgent{ID: newID("agent"), OwnerID: uid, Name: name, VerificationCode: strings.ToUpper(rand.Text()[:10]), Status: "pending", CreatedAt: t, ExpiresAt: t.Add(15 * time.Minute)}
	token := "edc_agent_" + rand.Text() + rand.Text()
	result, err := s.db.ExecContext(ctx, `INSERT INTO hub_agents(id,owner_id,name,token_hash,verification_code,status,created_at,expires_at)
 SELECT ?,?,?,?,?,?,?,? WHERE (SELECT COUNT(*) FROM hub_agents WHERE owner_id=? AND status='pending' AND expires_at>?)<10
 AND (SELECT COUNT(*) FROM hub_agents WHERE owner_id=? AND status IN ('approved','pending') AND expires_at>?)<1000`, a.ID, uid, name, digest([]byte(token)), a.VerificationCode, a.Status, t.Unix(), a.ExpiresAt.Unix(), uid, t.Unix(), uid, t.Unix())
	if err != nil {
		return HubAgentRegistration{}, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return HubAgentRegistration{}, &Error{"rate_limited", "too many agent registrations"}
	}
	return HubAgentRegistration{a, token}, nil
}
func scanHubAgent(row interface{ Scan(...any) error }) (HubAgent, error) {
	var a HubAgent
	var created, expires int64
	err := row.Scan(&a.ID, &a.OwnerID, &a.Name, &a.VerificationCode, &a.Status, &created, &expires)
	a.CreatedAt = time.Unix(created, 0).UTC()
	a.ExpiresAt = time.Unix(expires, 0).UTC()
	a.Status = hubEffectiveStatus(a.Status, a.ExpiresAt)
	return a, err
}
func (s *Store) HubAgent(ctx context.Context, token string, requireApproved bool) (HubAgent, error) {
	if len(token) > 256 || !strings.HasPrefix(token, "edc_agent_") {
		return HubAgent{}, ErrUnauthenticated
	}
	a, err := scanHubAgent(s.db.QueryRowContext(ctx, "SELECT id,owner_id,name,verification_code,status,created_at,expires_at FROM hub_agents WHERE token_hash=?", digest([]byte(token))))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrUnauthenticated
	}
	if err != nil {
		return a, err
	}
	if requireApproved && a.Status != "approved" {
		return a, &Error{"authorization_required", "ask the owner to approve this agent in My authorizations"}
	}
	return a, nil
}
func (s *Store) HubConnections(ctx context.Context, owner string) ([]HubConnection, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,provider_id,account_id,display_name,status FROM hub_connections WHERE owner_id=? ORDER BY created_at,id", owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HubConnection{}
	for rows.Next() {
		c := HubConnection{OwnerID: owner}
		if err := rows.Scan(&c.ID, &c.ProviderID, &c.AccountID, &c.DisplayName, &c.Status); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) HubConnection(ctx context.Context, owner, id string) (HubConnection, error) {
	c := HubConnection{OwnerID: owner}
	err := s.db.QueryRowContext(ctx, "SELECT id,provider_id,account_id,display_name,status FROM hub_connections WHERE id=? AND owner_id=?", id, owner).Scan(&c.ID, &c.ProviderID, &c.AccountID, &c.DisplayName, &c.Status)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return c, err
}

// Reconfiguring an existing account preserves its identity but revokes all prior
// grants. A fresh credential must never silently inherit an older authorization.
func (s *Store) AddHubConnection(ctx context.Context, c HubConnection, secret string, key []byte) (HubConnection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.OwnerID = UserID(ctx)
	if c.OwnerID == "" {
		return c, ErrUnauthenticated
	}
	if !hubText(c.ProviderID, 80) || !hubText(c.AccountID, 254) || !hubText(c.DisplayName, 150) || len(secret) > 16384 {
		return c, Invalid("provider, account and display name are required")
	}
	var existing string
	err := s.db.QueryRowContext(ctx, "SELECT id FROM hub_connections WHERE owner_id=? AND provider_id=? AND account_id=?", c.OwnerID, c.ProviderID, c.AccountID).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}
	if existing != "" && secret == "" {
		return c, ErrConflict
	}
	c.ID = existing
	if c.ID == "" {
		c.ID = newID("conn")
	}
	c.Status = "needs_auth"
	encrypted := []byte{}
	if secret != "" {
		encrypted, err = sealHubCredential(key, c, []byte(secret))
		if err != nil {
			return c, err
		}
		c.Status = "configured"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return c, err
	}
	defer tx.Rollback()
	if existing != "" {
		_, err = tx.ExecContext(ctx, "UPDATE hub_connections SET display_name=?,status=?,credential=? WHERE id=? AND owner_id=?", c.DisplayName, c.Status, encrypted, c.ID, c.OwnerID)
		if err == nil {
			_, err = tx.ExecContext(ctx, "UPDATE hub_requests SET status='revoked' WHERE connection_id=? AND status IN ('pending','approved')", c.ID)
		}
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, `INSERT INTO hub_connections(id,owner_id,provider_id,account_id,display_name,status,credential,created_at)
    SELECT ?,?,?,?,?,?,?,? WHERE (SELECT COUNT(*) FROM hub_connections WHERE owner_id=?)<100`, c.ID, c.OwnerID, c.ProviderID, c.AccountID, c.DisplayName, c.Status, encrypted, time.Now().Unix(), c.OwnerID)
		if err == nil {
			n, _ := result.RowsAffected()
			if n != 1 {
				err = ErrConflict
			}
		}
	}
	if err != nil {
		return c, err
	}
	return c, tx.Commit()
}
func hubCipher(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, &Error{"service_unavailable", "server requires a 32-byte EDC_HUB_CREDENTIAL_KEY before storing provider credentials"}
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func sealHubCredential(key []byte, c HubConnection, plain []byte) ([]byte, error) {
	a, err := hubCipher(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, plain, []byte(c.OwnerID+":"+c.ID)), nil
}
func (s *Store) HubCredential(ctx context.Context, c HubConnection, key []byte) (string, error) {
	var b []byte
	err := s.db.QueryRowContext(ctx, "SELECT credential FROM hub_connections WHERE id=? AND owner_id=? AND status='configured'", c.ID, c.OwnerID).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	a, err := hubCipher(key)
	if err != nil {
		return "", err
	}
	if len(b) < a.NonceSize() {
		return "", &Error{"service_unavailable", "connection credential unavailable"}
	}
	p, err := a.Open(nil, b[:a.NonceSize()], b[a.NonceSize():], []byte(c.OwnerID+":"+c.ID))
	if err != nil {
		return "", &Error{"service_unavailable", "connection credential unavailable"}
	}
	return string(p), nil
}
func (s *Store) DisconnectHubConnection(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE hub_connections SET status='disconnected',credential=X'' WHERE id=? AND owner_id=?", id, UserID(ctx))
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrNotFound
	}
	_, err = tx.ExecContext(ctx, "UPDATE hub_requests SET status='revoked' WHERE connection_id=? AND status IN ('pending','approved')", id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) HubRequests(ctx context.Context, owner, agentID string) ([]HubRequest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.agent_id,a.name,r.connection_id,c.display_name,r.operation,r.reason,r.status,r.created_at,r.expires_at
 FROM hub_requests r JOIN hub_agents a ON a.id=r.agent_id JOIN hub_connections c ON c.id=r.connection_id
 WHERE a.owner_id=? AND (?='' OR a.id=?) ORDER BY r.created_at DESC,r.id`, owner, agentID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HubRequest{}
	for rows.Next() {
		var r HubRequest
		var created, expires int64
		if err := rows.Scan(&r.ID, &r.AgentID, &r.AgentName, &r.ConnectionID, &r.ConnectionName, &r.Operation, &r.Reason, &r.Status, &created, &expires); err != nil {
			return nil, err
		}
		r.CreatedAt = time.Unix(created, 0).UTC()
		r.ExpiresAt = time.Unix(expires, 0).UTC()
		r.Status = hubEffectiveStatus(r.Status, r.ExpiresAt)
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) HubOverview(ctx context.Context) (HubOverview, error) {
	out := HubOverview{Agents: []HubAgent{}, Requests: []HubRequest{}, Connections: []HubConnection{}}
	owner := UserID(ctx)
	if owner == "" {
		return out, ErrUnauthenticated
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,owner_id,name,verification_code,status,created_at,expires_at FROM hub_agents WHERE owner_id=? ORDER BY created_at DESC,id", owner)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		a, e := scanHubAgent(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Agents = append(out.Agents, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.Connections, err = s.HubConnections(ctx, owner)
	if err != nil {
		return out, err
	}
	out.Requests, err = s.HubRequests(ctx, owner, "")
	return out, err
}
func (s *Store) RequestHubAccess(ctx context.Context, a HubAgent, connection, operation, reason string, seconds int64) (HubRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.Status != "approved" {
		return HubRequest{}, ErrActionForbidden
	}
	if !hubText(operation, 100) || !hubText(reason, 1000) || seconds < 60 || seconds > 604800 {
		return HubRequest{}, Invalid("operation, reason and duration between 60 and 604800 seconds are required")
	}
	c, err := s.HubConnection(ctx, a.OwnerID, connection)
	if err != nil {
		return HubRequest{}, err
	}
	if c.Status != "configured" {
		return HubRequest{}, &Error{"connection_required", "ask the owner to configure this account first"}
	}
	t := time.Now().UTC().Truncate(time.Second)
	expires := t.Add(time.Duration(seconds) * time.Second)
	if expires.After(a.ExpiresAt) {
		expires = a.ExpiresAt
	}
	r := HubRequest{ID: newID("grant"), AgentID: a.ID, AgentName: a.Name, ConnectionID: c.ID, ConnectionName: c.DisplayName, Operation: operation, Reason: reason, Status: "pending", CreatedAt: t, ExpiresAt: expires}
	// Return the existing pending/active request to avoid repeated notifications.
	existing, err := s.HubRequests(ctx, a.OwnerID, a.ID)
	if err != nil {
		return r, err
	}
	for _, e := range existing {
		if e.ConnectionID == connection && e.Operation == operation && (e.Status == "pending" || e.Status == "approved") {
			return e, nil
		}
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO hub_requests(id,agent_id,connection_id,operation,reason,status,created_at,expires_at)
 SELECT ?,?,?,?,?,?,?,? WHERE (SELECT COUNT(*) FROM hub_requests WHERE agent_id=? AND status IN ('pending','approved') AND expires_at>?)<200
 AND EXISTS(SELECT 1 FROM hub_agents WHERE id=? AND owner_id=? AND status='approved' AND expires_at>?)
 AND EXISTS(SELECT 1 FROM hub_connections WHERE id=? AND owner_id=? AND status='configured')`, r.ID, a.ID, c.ID, operation, reason, r.Status, t.Unix(), expires.Unix(), a.ID, t.Unix(), a.ID, a.OwnerID, t.Unix(), c.ID, a.OwnerID)
	if err != nil {
		return r, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return r, &Error{"rate_limited", "too many authorization requests"}
	}
	return r, nil
}
func (s *Store) DecideHubAgent(ctx context.Context, id, decision, code string) error {
	status := map[string]string{"approve": "approved", "deny": "denied", "revoke": "revoked"}[decision]
	if status == "" {
		return Invalid("invalid decision")
	}
	if decision == "approve" {
		var expected string
		if err := s.db.QueryRowContext(ctx, "SELECT verification_code FROM hub_agents WHERE id=? AND owner_id=?", id, UserID(ctx)).Scan(&expected); err != nil {
			return ErrNotFound
		}
		if strings.ToUpper(strings.TrimSpace(code)) != expected {
			return Invalid("verification code does not match the requesting agent")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t := time.Now().Unix()
	var result sql.Result
	if decision == "revoke" {
		result, err = tx.ExecContext(ctx, "UPDATE hub_agents SET status=? WHERE id=? AND owner_id=? AND status IN ('approved','pending')", status, id, UserID(ctx))
	} else {
		result, err = tx.ExecContext(ctx, "UPDATE hub_agents SET status=?,expires_at=? WHERE id=? AND owner_id=? AND status='pending' AND expires_at>?", status, t+604800, id, UserID(ctx), t)
	}
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrNotFound
	}
	if decision != "approve" {
		if _, err = tx.ExecContext(ctx, "UPDATE hub_requests SET status='revoked' WHERE agent_id=? AND status IN ('pending','approved')", id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) DecideHubRequest(ctx context.Context, id, decision string) error {
	status := map[string]string{"approve": "approved", "deny": "denied", "revoke": "revoked"}[decision]
	if status == "" {
		return Invalid("invalid decision")
	}
	var result sql.Result
	var err error
	if decision == "approve" {
		result, err = s.db.ExecContext(ctx, `UPDATE hub_requests SET status=? WHERE id=? AND status='pending' AND expires_at>?
 AND agent_id IN (SELECT id FROM hub_agents WHERE owner_id=? AND status='approved' AND expires_at>?)
 AND connection_id IN (SELECT id FROM hub_connections WHERE owner_id=? AND status='configured')`, status, id, time.Now().Unix(), UserID(ctx), time.Now().Unix(), UserID(ctx))
	} else {
		result, err = s.db.ExecContext(ctx, `UPDATE hub_requests SET status=? WHERE id=? AND status IN ('pending','approved')
 AND agent_id IN (SELECT id FROM hub_agents WHERE owner_id=?)`, status, id, UserID(ctx))
	}
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) CheckHubAccess(ctx context.Context, a HubAgent, connection, operation string) error {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM hub_requests r JOIN hub_agents a ON a.id=r.agent_id JOIN hub_connections c ON c.id=r.connection_id
 WHERE r.agent_id=? AND r.connection_id=? AND r.operation=? AND r.status='approved' AND r.expires_at>?
 AND a.status='approved' AND a.expires_at>? AND a.owner_id=? AND c.owner_id=a.owner_id AND c.status='configured'`, a.ID, connection, operation, time.Now().Unix(), time.Now().Unix(), a.OwnerID).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return &Error{"authorization_required", "request this connection and operation with edc access request; ask the owner to approve it in My authorizations"}
	}
	return nil
}
