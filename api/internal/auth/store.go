package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
)

// SessionLifetime is the absolute lifetime of a session; it is not extended by activity.
const SessionLifetime = 12 * time.Hour

const tokenLen = 32

var (
	// ErrInvalidCredentials covers both an unknown email and a wrong password.
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	// ErrNoSession means the token is missing, malformed, unknown or expired.
	ErrNoSession = errors.New("auth: no session")
	// ErrEmailTaken is returned when an account with the same email (case-insensitive) exists.
	ErrEmailTaken = errors.New("auth: email already registered")
	// ErrWeakPassword is returned by ValidatePassword.
	ErrWeakPassword = fmt.Errorf("auth: password must have %d to %d characters", MinPasswordLen, MaxPasswordLen)
)

// Length limits, in characters (Unicode code points), of every new password: bootstrap, claim and reset.
const (
	MinPasswordLen = 10
	MaxPasswordLen = 128
)

// ValidatePassword is the one rule for new passwords; it returns ErrWeakPassword or nil.
func ValidatePassword(password string) error {
	if n := utf8.RuneCountInString(password); n < MinPasswordLen || n > MaxPasswordLen {
		return ErrWeakPassword
	}
	return nil
}

// Session is a signed-in user. TenantID is empty when the user has no membership.
type Session struct {
	UserID   string
	Email    string
	TenantID string
	// EmailVerified reports that the user proved access to the inbox.
	EmailVerified bool
	// MFAEnrolled reports that the user has a confirmed TOTP.
	MFAEnrolled bool
	// MFAVerified reports that this session proved the second factor (sessions.mfa_at is set).
	MFAVerified bool
}

// Store reads and writes accounts and sessions through the application pool.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
	// dummy is verified when the email is unknown; computed here so the first miss is not slower.
	dummy string
}

// NewStore returns a Store; now is the clock used for session expiry. It panics when now is nil.
func NewStore(pool *pgxpool.Pool, now func() time.Time) *Store {
	if now == nil {
		panic("auth: NewStore needs a clock")
	}
	return &Store{pool: pool, now: now, dummy: dummyHash()}
}

// NormalizeEmail trims and lowercases an email; accounts are matched on this form.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// Authenticate returns the user id for email and password, or ErrInvalidCredentials.
// An unknown email still runs one argon2id verification so timing does not reveal accounts.
func (s *Store) Authenticate(ctx context.Context, email, password string) (string, error) {
	var id, hash string
	err := s.pool.QueryRow(ctx, `SELECT id::text, password_hash FROM users WHERE lower(email) = $1`,
		NormalizeEmail(email)).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		_, _ = VerifyPassword(s.dummy, password)
		return "", ErrInvalidCredentials
	}
	if err != nil {
		return "", fmt.Errorf("auth: find user: %w", err)
	}
	ok, err := VerifyPassword(hash, password)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrInvalidCredentials
	}
	return id, nil
}

// ErrSamePassword is returned by ChangePassword when the new password equals the current one.
var ErrSamePassword = errors.New("auth: new password equals the current one")

// ChangePassword replaces the password of userID, signed in with the session named by token. It
// checks newPassword (ErrWeakPassword), then current against the stored hash (ErrInvalidCredentials),
// then that they differ (ErrSamePassword). On success, in one transaction, it stores the new hash,
// marks every pending emailed token of the user used, deletes every other session of the user and
// rotates this one (same tenant, second-factor state and absolute expiry), returning the new token and
// its expiry. An error changes nothing; ErrNoSession means the session ended meanwhile, which is also
// the outcome of the loser of two concurrent changes (see wrongCurrentErr).
func (s *Store) ChangePassword(ctx context.Context, token, userID, current, newPassword string) (string, time.Time, error) {
	if err := ValidatePassword(newPassword); err != nil {
		return "", time.Time{}, err
	}
	h, ok := hashToken(token)
	if !ok {
		return "", time.Time{}, ErrNoSession
	}
	var old string
	err := s.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&old)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, ErrNoSession
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth: find user: %w", err)
	}
	if ok, err := VerifyPassword(old, current); err != nil {
		return "", time.Time{}, err
	} else if !ok {
		return "", time.Time{}, s.wrongCurrentErr(ctx, h, userID)
	}
	if newPassword == current {
		return "", time.Time{}, ErrSamePassword
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	var tok string
	var expires time.Time
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Only the hash the current password was checked against is replaced: a concurrent change
		// that won the race leaves this one with a current password that no longer matches.
		tag, err := tx.Exec(ctx, `UPDATE users SET password_hash = $3 WHERE id = $1 AND password_hash = $2`,
			userID, old, hash)
		if err != nil {
			return fmt.Errorf("auth: update password: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return errPasswordReplaced
		}
		// As in ResetPassword: no emailed link sent before the change may outlive it.
		if _, err := tx.Exec(ctx,
			`UPDATE email_tokens SET used_at = $2 WHERE user_id = $1 AND used_at IS NULL`, userID, now); err != nil {
			return fmt.Errorf("auth: use email tokens: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND token_hash <> $2`, userID, h); err != nil {
			return fmt.Errorf("auth: delete other sessions: %w", err)
		}
		tok, expires, err = rotateSession(ctx, tx, token, userID, now, false, nil)
		return err
	})
	if errors.Is(err, errPasswordReplaced) {
		return "", time.Time{}, s.wrongCurrentErr(ctx, h, userID)
	}
	if err != nil {
		return "", time.Time{}, err
	}
	return tok, expires, nil
}

// errPasswordReplaced aborts a change whose current password was checked against a hash that a
// concurrent change replaced before this one could.
var errPasswordReplaced = errors.New("auth: password replaced meanwhile")

// wrongCurrentErr is the error of a change whose current password does not match the stored hash.
// Every change of the hash ends each session it does not rotate (ChangePassword, ResetPassword), so
// when this session is gone a concurrent change won: ErrNoSession, never a wrong current password
// (two tabs must not cost a sign-in attempt). With the session alive it is ErrInvalidCredentials.
func (s *Store) wrongCurrentErr(ctx context.Context, tokenHash []byte, userID string) error {
	var alive bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT FROM sessions WHERE token_hash = $1 AND user_id = $2 AND expires_at > $3)`,
		tokenHash, userID, s.now()).Scan(&alive)
	if err != nil {
		return fmt.Errorf("auth: find session: %w", err)
	}
	if !alive {
		return ErrNoSession
	}
	return ErrInvalidCredentials
}

// CreateSession starts a session for userID and returns the raw token for the cookie. Only its
// SHA-256 is stored. The active tenant is the user's oldest membership, or none.
func (s *Store) CreateSession(ctx context.Context, userID string) (string, error) {
	var tenant *string
	err := db.WithUser(ctx, s.pool, userID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT tenant_id::text FROM memberships WHERE user_id = $1 ORDER BY created_at, tenant_id LIMIT 1`,
			userID).Scan(&tenant)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return "", fmt.Errorf("auth: pick tenant: %w", err)
	}
	return InsertSession(ctx, s.pool, userID, tenant, s.now())
}

type execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// InsertSession stores a new session for userID with the given active tenant (nil for none) and
// returns the raw token for the cookie. q is the pool or a transaction.
func InsertSession(ctx context.Context, q execer, userID string, tenant *string, now time.Time) (string, error) {
	return insertSession(ctx, q, userID, tenant, now, nil)
}

// insertSession is InsertSession with mfaAt, set only for a session that proved the second factor.
func insertSession(ctx context.Context, q execer, userID string, tenant *string, now time.Time, mfaAt *time.Time) (string, error) {
	return insertSessionUntil(ctx, q, userID, tenant, now, now.Add(SessionLifetime), mfaAt)
}

// insertSessionUntil is insertSession with an explicit absolute expiry (used when rotating a session,
// which must never extend its lifetime).
func insertSessionUntil(ctx context.Context, q execer, userID string, tenant *string, now, expires time.Time, mfaAt *time.Time) (string, error) {
	raw := make([]byte, tokenLen)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: token: %w", err)
	}
	sum := sha256.Sum256(raw)
	if _, err := q.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, tenant_id, created_at, expires_at, mfa_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		sum[:], userID, tenant, now, expires, mfaAt); err != nil {
		return "", fmt.Errorf("auth: insert session: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// RotateSessionIntoTenant replaces, inside tx, the live session named by token (of userID) with a
// new token whose active tenant is tenant, keeping its second-factor state and absolute expiry. It
// returns the new token and that expiry, or ErrNoSession.
func RotateSessionIntoTenant(ctx context.Context, tx pgx.Tx, token, userID, tenant string, now time.Time) (string, time.Time, error) {
	return rotateSession(ctx, tx, token, userID, now.UTC().Truncate(time.Microsecond), false, &tenant)
}

// DeleteSessionIn deletes the session named by token through q (the pool or a transaction); an
// unknown or malformed token is not an error.
func DeleteSessionIn(ctx context.Context, q execer, token string) error {
	h, ok := hashToken(token)
	if !ok {
		return nil
	}
	if _, err := q.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, h); err != nil {
		return fmt.Errorf("auth: delete session: %w", err)
	}
	return nil
}

// hashToken returns the stored form of a cookie token, or false when it is not a valid token.
func hashToken(token string) ([]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != tokenLen {
		return nil, false
	}
	sum := sha256.Sum256(raw)
	return sum[:], true
}

// Lookup returns the unexpired session for token, or ErrNoSession.
func (s *Store) Lookup(ctx context.Context, token string) (Session, error) {
	h, ok := hashToken(token)
	if !ok {
		return Session{}, ErrNoSession
	}
	var sess Session
	var tenant *string
	err := s.pool.QueryRow(ctx, `
		SELECT s.user_id::text, u.email, s.tenant_id::text, u.email_verified_at IS NOT NULL, s.mfa_at IS NOT NULL,
			EXISTS (SELECT FROM user_totp t WHERE t.user_id = s.user_id AND t.confirmed_at IS NOT NULL)
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > $2`, h, s.now()).
		Scan(&sess.UserID, &sess.Email, &tenant, &sess.EmailVerified, &sess.MFAVerified, &sess.MFAEnrolled)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, fmt.Errorf("auth: lookup session: %w", err)
	}
	if tenant != nil {
		sess.TenantID = *tenant
	}
	return sess, nil
}

// DeleteSession removes the session for token; an unknown token is not an error.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	return DeleteSessionIn(ctx, s.pool, token)
}

// Membership returns the session user's roles slice in the session's tenant and that tenant's name,
// read in one query; without a tenant or when the membership is removed after sign-in, it returns nil, "", false, nil.
// commission reports that the user's roster entry is on the tenant's current Electoral commission (see CommissionRole).
func (s *Store) Membership(ctx context.Context, sess Session) (roles []string, tenantName string, commission bool, err error) {
	if sess.TenantID == "" {
		return nil, "", false, nil
	}
	err = db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT array_agg(r.role ORDER BY CASE r.role WHEN 'student' THEN 0 WHEN 'officer' THEN 1 ELSE 2 END), r.name,
			       EXISTS (SELECT FROM electoral_commission_members cm
			               JOIN electoral_commissions c ON c.tenant_id = cm.tenant_id AND c.id = cm.commission_id AND c.ended_at IS NULL
			               JOIN memberships ms ON ms.tenant_id = cm.tenant_id AND ms.roster_member_id = cm.roster_member_id
			               WHERE cm.tenant_id = $2 AND ms.user_id = $1)
			FROM (SELECT DISTINCT roles.role, o.name
			      FROM memberships m JOIN organizations o ON o.id = m.tenant_id
			      CROSS JOIN LATERAL (SELECT m.role UNION SELECT 'officer' WHERE EXISTS (
			          SELECT 1 FROM officer_positions p
			          WHERE p.tenant_id = m.tenant_id AND p.roster_member_id = m.roster_member_id
			      )) roles
			      WHERE m.user_id = $1 AND m.tenant_id = $2) r
			GROUP BY r.name`, sess.UserID, sess.TenantID).Scan(&roles, &tenantName, &commission)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", false, nil // membership removed after sign-in
	}
	if err != nil {
		return nil, "", false, fmt.Errorf("auth: membership: %w", err)
	}
	return roles, tenantName, commission, nil
}

// DefaultContactHeadings are seeded for every school at creation time (R1).
var DefaultContactHeadings = []string{
	"artistas",
	"foodtrucks",
	"mesas y sillas",
	"gazebos",
	"cristalería",
	"insumos plásticos",
	"sonido e iluminación",
	"seguridad",
	"impresiones",
	"otros",
}

// Bootstrap creates a school, its default contact headings, its first user and a school_admin
// membership in one transaction.
func (s *Store) Bootstrap(ctx context.Context, orgName, email, password string) (orgID string, err error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return "", err
	}
	if orgID, err = newUUID(); err != nil {
		return "", err
	}
	err = db.WithTenant(ctx, s.pool, orgID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO organizations (id, kind, name) VALUES ($1, 'school', $2)`, orgID, orgName); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT seed_finance_categories($1)`, orgID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO contact_headings (tenant_id, name)
			SELECT $1, name FROM unnest($2::text[]) AS name
			ON CONFLICT (tenant_id, lower(name)) DO NOTHING`, orgID, DefaultContactHeadings); err != nil {
			return err
		}
		var userID string
		if err := tx.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id::text`,
			NormalizeEmail(email), hash).Scan(&userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'school_admin')`, orgID, userID)
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == "users_email_lower_key" {
		return "", ErrEmailTaken
	}
	if err != nil {
		return "", fmt.Errorf("auth: bootstrap: %w", err)
	}
	return orgID, nil
}

// newUUID returns a random (version 4) UUID.
func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

type ctxKey struct{}

// ContextWithSession returns ctx carrying sess.
func ContextWithSession(ctx context.Context, sess Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, sess)
}

// SessionFromContext returns the session stored by the session middleware, if any.
func SessionFromContext(ctx context.Context) (Session, bool) {
	sess, ok := ctx.Value(ctxKey{}).(Session)
	return sess, ok
}
