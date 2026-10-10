package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"errors"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,63}$`)

func normalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 254 || !utf8.ValidString(value) {
		return "", Invalid("email is required and must be valid")
	}
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || !strings.Contains(value, "@") {
		return "", Invalid("email is required and must be valid")
	}
	return value, nil
}

func (s *Store) Register(ctx context.Context, in Credentials) (User, error) {
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	if !usernamePattern.MatchString(in.Username) {
		return User{}, Invalid("username must be 3-64 lowercase letters, digits, _, . or -")
	}
	if len(in.Password) < 12 || len(in.Password) > 72 {
		return User{}, Invalid("password must be 12-72 bytes")
	}
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return User{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	u := User{ID: newID("usr"), Username: in.Username, Email: email, CreatedAt: now()}
	_, err = s.db.ExecContext(ctx, "INSERT INTO users(id,username,email,password_hash,created_at) VALUES(?,?,?,?,?)", u.ID, u.Username, u.Email, hash, u.CreatedAt)
	if err != nil {
		var n int
		if s.db.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE username=?", u.Username).Scan(&n) == nil && n > 0 {
			return User{}, ErrConflict
		}
		return User{}, identityWriteError(err)
	}
	return u, nil
}
func (s *Store) Login(ctx context.Context, in Credentials) (LoginResult, error) {
	var u User
	var hash []byte
	err := s.db.QueryRowContext(ctx, "SELECT id,username,COALESCE(email,''),password_hash,created_at FROM users WHERE username=?", strings.ToLower(strings.TrimSpace(in.Username))).Scan(&u.ID, &u.Username, &u.Email, &hash, &u.CreatedAt)
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
	return s.issueLogin(ctx, u)
}

func (s *Store) issueLogin(ctx context.Context, u User) (LoginResult, error) {
	token := "edc_" + rand.Text() + rand.Text()
	expires := time.Now().UTC().Add(30 * 24 * time.Hour)
	if _, err := s.db.ExecContext(ctx, "INSERT INTO tokens(hash,user_id,expires_at) VALUES(?,?,?)", digest([]byte(token)), u.ID, expires.Unix()); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{User: u, Token: token, ExpiresAt: expires}, nil
}

// LoginInteg maps a verified Integ.Auth identity to a local Context user and
// issues a Context session. A verified email may bind an existing legacy user,
// preserving its user ID, projects, memberships, and event authorship.
func (s *Store) LoginInteg(ctx context.Context, issuer, subject, email string) (LoginResult, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	parsedIssuer, err := url.Parse(issuer)
	if err != nil || parsedIssuer.Scheme == "" || parsedIssuer.Host == "" || parsedIssuer.RawQuery != "" || parsedIssuer.Fragment != "" {
		return LoginResult{}, Invalid("issuer must be an absolute URL")
	}
	subject = strings.TrimSpace(subject)
	if subject == "" || len(subject) > 512 || !utf8.ValidString(subject) {
		return LoginResult{}, Invalid("subject is required")
	}
	email, err = normalizeEmail(email)
	if err != nil {
		return LoginResult{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LoginResult{}, err
	}
	defer tx.Rollback()
	var u User
	err = tx.QueryRowContext(ctx, `
		SELECT u.id,u.username,COALESCE(u.email,''),u.created_at
		FROM integ_identities i JOIN users u ON u.id=i.user_id
		WHERE i.issuer=? AND i.subject=?`, issuer, subject).
		Scan(&u.ID, &u.Username, &u.Email, &u.CreatedAt)
	if err == nil {
		if u.Email != email {
			if _, err = tx.ExecContext(ctx, "UPDATE users SET email=? WHERE id=?", email, u.ID); err != nil {
				return LoginResult{}, identityWriteError(err)
			}
			u.Email = email
		}
		if _, err = tx.ExecContext(ctx, "UPDATE integ_identities SET email=? WHERE issuer=? AND subject=?", email, issuer, subject); err != nil {
			return LoginResult{}, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return LoginResult{}, err
	} else {
		err = tx.QueryRowContext(ctx, "SELECT id,username,email,created_at FROM users WHERE email=?", email).
			Scan(&u.ID, &u.Username, &u.Email, &u.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			u = User{ID: newID("usr"), Username: integUsername(email), Email: email, CreatedAt: now()}
			u.Username, err = availableUsername(ctx, tx, u.Username)
			if err != nil {
				return LoginResult{}, err
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO users(id,username,email,password_hash,created_at) VALUES(?,?,?,?,?)", u.ID, u.Username, u.Email, []byte("central-login-only"), u.CreatedAt); err != nil {
				return LoginResult{}, identityWriteError(err)
			}
		} else if err != nil {
			return LoginResult{}, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO integ_identities(issuer,subject,user_id,email,created_at) VALUES(?,?,?,?,?)", issuer, subject, u.ID, email, now()); err != nil {
			return LoginResult{}, identityWriteError(err)
		}
	}
	if err = tx.Commit(); err != nil {
		return LoginResult{}, err
	}
	return s.issueLogin(ctx, u)
}

func identityWriteError(err error) error {
	if strings.Contains(strings.ToLower(err.Error()), "constraint failed") {
		return ErrConflict
	}
	return err
}

func integUsername(email string) string {
	local := strings.SplitN(email, "@", 2)[0]
	var b strings.Builder
	for _, r := range local {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || strings.ContainsRune("_.-", r) {
			b.WriteRune(r)
		}
	}
	base := strings.Trim(b.String(), "_.-")
	if len(base) < 3 {
		base = "user"
	}
	if len(base) > 48 {
		base = base[:48]
	}
	return base
}

func availableUsername(ctx context.Context, tx *sql.Tx, base string) (string, error) {
	for n := 0; n < 100; n++ {
		candidate := base
		if n > 0 {
			candidate = base + "-" + strings.ToLower(rand.Text())[:8]
		}
		var exists int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM users WHERE username=?", candidate).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", ErrConflict
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

// SetUserEmail is the explicit legacy-account migration operation. Requiring
// both the immutable ID and username prevents an operator typo from assigning
// a confirmed address to a different existing account. It never overwrites a
// different non-empty email.
func (s *Store) SetUserEmail(ctx context.Context, userID, username, email string) (User, error) {
	userID = strings.TrimSpace(userID)
	username = strings.ToLower(strings.TrimSpace(username))
	email, err := normalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	if userID == "" || !usernamePattern.MatchString(username) {
		return User{}, Invalid("user ID and username are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var u User
	if err = tx.QueryRowContext(ctx, "SELECT id,username,COALESCE(email,''),created_at FROM users WHERE id=? AND username=?", userID, username).
		Scan(&u.ID, &u.Username, &u.Email, &u.CreatedAt); errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	} else if err != nil {
		return User{}, err
	}
	if u.Email != "" && u.Email != email {
		return User{}, ErrConflict
	}
	if u.Email == "" {
		if _, err = tx.ExecContext(ctx, "UPDATE users SET email=? WHERE id=?", email, u.ID); err != nil {
			return User{}, identityWriteError(err)
		}
		u.Email = email
	}
	if err = tx.Commit(); err != nil {
		return User{}, err
	}
	return u, nil
}
func (s *Store) Me(ctx context.Context) (User, error) {
	if UserID(ctx) == "" {
		return User{}, ErrUnauthenticated
	}
	var u User
	err := s.db.QueryRowContext(ctx, "SELECT id,username,COALESCE(email,''),created_at FROM users WHERE id=?", UserID(ctx)).Scan(&u.ID, &u.Username, &u.Email, &u.CreatedAt)
	return u, err
}
