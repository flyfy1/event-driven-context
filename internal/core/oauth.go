package core

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	ScopeRead  = "context:read"
	ScopeWrite = "context:write"
)

var OAuthScopes = []string{ScopeRead, ScopeWrite}

type OAuthClient struct {
	ClientID     string   `json:"client_id"`
	ClientName   string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
}

type OAuthRequest struct {
	ClientID      string
	ClientName    string
	RedirectURI   string
	State         string
	CodeChallenge string
	Scope         string
	Resource      string
}

type OAuthTokenInfo struct {
	UserID    string
	ClientID  string
	Scopes    []string
	Resource  string
	ExpiresAt time.Time
}

func NormalizeOAuthScope(raw string) (string, error) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		fields = append([]string(nil), OAuthScopes...)
	}
	seen := map[string]bool{}
	for _, scope := range fields {
		if !slices.Contains(OAuthScopes, scope) || seen[scope] {
			return "", Invalid("unsupported or duplicate OAuth scope")
		}
		seen[scope] = true
	}
	var normalized []string
	for _, scope := range OAuthScopes {
		if seen[scope] {
			normalized = append(normalized, scope)
		}
	}
	return strings.Join(normalized, " "), nil
}

func validRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" || u.User != nil || u.Hostname() == "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	return u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
}

func (s *Store) RegisterOAuthClient(ctx context.Context, name string, redirects []string) (OAuthClient, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 || len(redirects) == 0 || len(redirects) > 10 {
		return OAuthClient{}, Invalid("client_name and 1-10 redirect_uris are required")
	}
	seen := map[string]bool{}
	for _, redirect := range redirects {
		if len(redirect) > 2048 || !validRedirectURI(redirect) || seen[redirect] {
			return OAuthClient{}, Invalid("redirect_uris must be unique HTTPS or loopback callback URLs")
		}
		seen[redirect] = true
	}
	b, _ := json.Marshal(redirects)
	client := OAuthClient{ClientID: "edc_client_" + strings.ToLower(strings.ReplaceAll(newID(""), "_", "")), ClientName: name, RedirectURIs: redirects}
	result, err := s.db.ExecContext(ctx, `INSERT INTO oauth_clients(client_id,client_name,redirect_uris,created_at)
		SELECT ?,?,?,? WHERE (SELECT count(*) FROM oauth_clients)<1000`, client.ClientID, client.ClientName, string(b), time.Now().Unix())
	if err != nil {
		return OAuthClient{}, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return OAuthClient{}, &Error{Code: "rate_limited", Message: "OAuth client registration capacity reached"}
	}
	return client, nil
}

func (s *Store) OAuthClient(ctx context.Context, clientID, redirectURI string) (OAuthClient, error) {
	var client OAuthClient
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT client_id,client_name,redirect_uris FROM oauth_clients WHERE client_id=?", clientID).Scan(&client.ClientID, &client.ClientName, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return client, ErrNotFound
	}
	if err != nil {
		return client, err
	}
	if err = json.Unmarshal([]byte(raw), &client.RedirectURIs); err != nil {
		return client, err
	}
	if redirectURI != "" && !slices.Contains(client.RedirectURIs, redirectURI) {
		return OAuthClient{}, ErrNotFound
	}
	return client, nil
}

func (s *Store) CreateOAuthRequest(ctx context.Context, requestID, csrf string, in OAuthRequest, expires time.Time) error {
	_, _ = s.db.ExecContext(ctx, "DELETE FROM oauth_requests WHERE expires_at<=? OR used_at IS NOT NULL", time.Now().Unix())
	result, err := s.db.ExecContext(ctx, `INSERT INTO oauth_requests(id_hash,client_id,redirect_uri,state,code_challenge,scope,resource,csrf_hash,expires_at)
		SELECT ?,?,?,?,?,?,?,?,? WHERE (SELECT count(*) FROM oauth_requests)<1000`, digest([]byte(requestID)), in.ClientID, in.RedirectURI, in.State, in.CodeChallenge, in.Scope, in.Resource, digest([]byte(csrf)), expires.Unix())
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return &Error{Code: "rate_limited", Message: "OAuth authorization capacity reached"}
	}
	return nil
}

func (s *Store) OAuthRequest(ctx context.Context, requestID, csrf string) (OAuthRequest, error) {
	var out OAuthRequest
	err := s.db.QueryRowContext(ctx, `SELECT r.client_id,c.client_name,r.redirect_uri,r.state,r.code_challenge,r.scope,r.resource
		FROM oauth_requests r JOIN oauth_clients c ON c.client_id=r.client_id
		WHERE r.id_hash=? AND r.csrf_hash=? AND r.used_at IS NULL AND r.expires_at>?`, digest([]byte(requestID)), digest([]byte(csrf)), time.Now().Unix()).Scan(
		&out.ClientID, &out.ClientName, &out.RedirectURI, &out.State, &out.CodeChallenge, &out.Scope, &out.Resource)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrUnauthenticated
	}
	return out, err
}

func (s *Store) ApproveOAuthRequest(ctx context.Context, requestID, csrf, userID, code string, expires time.Time) (OAuthRequest, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OAuthRequest{}, err
	}
	defer tx.Rollback()
	var out OAuthRequest
	err = tx.QueryRowContext(ctx, `SELECT r.client_id,c.client_name,r.redirect_uri,r.state,r.code_challenge,r.scope,r.resource
		FROM oauth_requests r JOIN oauth_clients c ON c.client_id=r.client_id
		WHERE r.id_hash=? AND r.csrf_hash=? AND r.used_at IS NULL AND r.expires_at>?`, digest([]byte(requestID)), digest([]byte(csrf)), time.Now().Unix()).Scan(
		&out.ClientID, &out.ClientName, &out.RedirectURI, &out.State, &out.CodeChallenge, &out.Scope, &out.Resource)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrUnauthenticated
	}
	if err != nil {
		return out, err
	}
	now := time.Now().Unix()
	if _, err = tx.ExecContext(ctx, `INSERT INTO oauth_authorization_codes(code_hash,client_id,user_id,redirect_uri,code_challenge,scope,resource,expires_at)
		VALUES(?,?,?,?,?,?,?,?)`, digest([]byte(code)), out.ClientID, userID, out.RedirectURI, out.CodeChallenge, out.Scope, out.Resource, expires.Unix()); err != nil {
		return out, err
	}
	result, err := tx.ExecContext(ctx, "UPDATE oauth_requests SET used_at=? WHERE id_hash=? AND used_at IS NULL", now, digest([]byte(requestID)))
	if err != nil {
		return out, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return out, ErrUnauthenticated
	}
	return out, tx.Commit()
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (s *Store) ExchangeOAuthCode(ctx context.Context, code, clientID, redirectURI, verifier, resource string, ttl time.Duration) (string, OAuthTokenInfo, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", OAuthTokenInfo{}, err
	}
	defer tx.Rollback()
	var userID, storedClient, storedRedirect, challenge, scope, storedResource string
	err = tx.QueryRowContext(ctx, `SELECT user_id,client_id,redirect_uri,code_challenge,scope,resource FROM oauth_authorization_codes
		WHERE code_hash=? AND used_at IS NULL AND expires_at>?`, digest([]byte(code)), time.Now().Unix()).Scan(&userID, &storedClient, &storedRedirect, &challenge, &scope, &storedResource)
	challengeMatches := subtle.ConstantTimeCompare([]byte(pkceChallenge(verifier)), []byte(challenge)) == 1
	if err != nil || storedClient != clientID || storedRedirect != redirectURI || storedResource != resource || !challengeMatches {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", OAuthTokenInfo{}, err
		}
		return "", OAuthTokenInfo{}, ErrUnauthenticated
	}
	now := time.Now()
	result, err := tx.ExecContext(ctx, "UPDATE oauth_authorization_codes SET used_at=? WHERE code_hash=? AND used_at IS NULL", now.Unix(), digest([]byte(code)))
	if err != nil {
		return "", OAuthTokenInfo{}, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return "", OAuthTokenInfo{}, ErrUnauthenticated
	}
	token := "edco_" + strings.ToLower(strings.ReplaceAll(newID(""), "_", "")) + strings.ToLower(strings.ReplaceAll(newID(""), "_", ""))
	expires := now.Add(ttl)
	if _, err = tx.ExecContext(ctx, `INSERT INTO oauth_access_tokens(hash,client_id,user_id,scope,resource,expires_at,created_at) VALUES(?,?,?,?,?,?,?)`, digest([]byte(token)), clientID, userID, scope, resource, expires.Unix(), now.Unix()); err != nil {
		return "", OAuthTokenInfo{}, err
	}
	if err = tx.Commit(); err != nil {
		return "", OAuthTokenInfo{}, err
	}
	return token, OAuthTokenInfo{UserID: userID, ClientID: clientID, Scopes: strings.Fields(scope), Resource: resource, ExpiresAt: expires}, nil
}

func (s *Store) AuthenticateOAuth(ctx context.Context, token, resource string) (OAuthTokenInfo, error) {
	if token == "" || len(token) > 256 {
		return OAuthTokenInfo{}, ErrUnauthenticated
	}
	var out OAuthTokenInfo
	var scope string
	var expires int64
	err := s.db.QueryRowContext(ctx, `SELECT user_id,client_id,scope,resource,expires_at FROM oauth_access_tokens WHERE hash=?`, digest([]byte(token))).Scan(&out.UserID, &out.ClientID, &scope, &out.Resource, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthTokenInfo{}, ErrUnauthenticated
	}
	if err != nil {
		return OAuthTokenInfo{}, err
	}
	if expires <= time.Now().Unix() || out.Resource != resource {
		return OAuthTokenInfo{}, ErrUnauthenticated
	}
	var scopeErr error
	if _, scopeErr = NormalizeOAuthScope(scope); scopeErr != nil {
		return OAuthTokenInfo{}, ErrUnauthenticated
	}
	out.Scopes = strings.Fields(scope)
	out.ExpiresAt = time.Unix(expires, 0)
	return out, nil
}
