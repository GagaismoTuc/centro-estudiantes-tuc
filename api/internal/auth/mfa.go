package auth

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/totp"
)

// MFAState is the two-factor state of a session in its active tenant.
type MFAState string

const (
	// MFANotRequired: student or no membership in the active tenant.
	MFANotRequired MFAState = "not_required"
	// MFAEnrollmentRequired: privileged role without a confirmed TOTP.
	MFAEnrollmentRequired MFAState = "enrollment_required"
	// MFAPending: privileged role with a confirmed TOTP, but this session has not verified it.
	MFAPending MFAState = "pending"
	// MFASatisfied: privileged role and this session verified the second factor.
	MFASatisfied MFAState = "satisfied"
)

var (
	// ErrMFARequired means a privileged action was attempted from a session without a verified second factor.
	ErrMFARequired = errors.New("auth: two-factor authentication required")
	// ErrAlreadyEnrolled means the user already has a confirmed TOTP.
	ErrAlreadyEnrolled = errors.New("auth: two-factor authentication already enrolled")
	// ErrInvalidCode covers wrong, replayed and used codes, and users without a (pending) TOTP.
	ErrInvalidCode = errors.New("auth: invalid two-factor code")
	// ErrNoAccount means no user has the given email.
	ErrNoAccount = errors.New("auth: no account with that email")
)

// Privileged reports whether role must use two-factor authentication.
func Privileged(role string) bool { return role == "school_admin" || role == "officer" }

// MFAStateOf returns the state of sess for role, the user's role in the session's active tenant.
func MFAStateOf(sess Session, role string) MFAState {
	switch {
	case !Privileged(role):
		return MFANotRequired
	case sess.MFAVerified:
		return MFASatisfied
	case sess.MFAEnrolled:
		return MFAPending
	default:
		return MFAEnrollmentRequired
	}
}

// RequireMFA is the enforcement point for privileged actions: call it, after the role check, in
// every operation reserved to school_admin or officer.
func RequireMFA(sess Session) error {
	if !sess.MFAVerified {
		return ErrMFARequired
	}
	return nil
}

// RecoveryCodeCount is how many recovery codes a confirmed enrollment gets.
const RecoveryCodeCount = 10

const recoveryLen = 10 // 50 bits

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// MFA enrolls and verifies TOTP second factors. Secrets are encrypted with key (TOTP_ENC_KEY).
type MFA struct {
	pool *pgxpool.Pool
	key  []byte
	// recoveryKey is the HMAC key of recovery codes, derived from key; nil when key is invalid.
	recoveryKey []byte
	now         func() time.Time
}

// NewMFA returns an MFA; key must be totp.KeyLen bytes (cmd/server refuses to start otherwise), or
// every operation fails. It panics when now is nil.
func NewMFA(pool *pgxpool.Pool, key []byte, now func() time.Time) *MFA {
	if now == nil {
		panic("auth: NewMFA needs a clock")
	}
	m := &MFA{pool: pool, key: key, now: now}
	if len(key) == totp.KeyLen {
		// A separate key per purpose: the AES key itself is never used as an HMAC key.
		m.recoveryKey, _ = hkdf.Key(sha256.New, key, nil, "recovery-codes", 32)
	}
	return m
}

// recoveryHash returns HMAC-SHA256 of a normalized recovery code under the derived recovery key, so
// a copy of the database alone does not allow guessing codes offline.
func (m *MFA) recoveryHash(normalized string) ([]byte, error) {
	if m.recoveryKey == nil {
		return nil, errors.New("auth: TOTP key is not configured")
	}
	h := hmac.New(sha256.New, m.recoveryKey)
	h.Write([]byte(normalized))
	return h.Sum(nil), nil
}

// Enroll creates (or replaces) the user's pending TOTP secret and returns it for display, once.
// Returns ErrAlreadyEnrolled when the user's TOTP is confirmed.
func (m *MFA) Enroll(ctx context.Context, userID, email string) (secret, uri string, err error) {
	raw, err := totp.NewSecret()
	if err != nil {
		return "", "", err
	}
	sealed, err := totp.Seal(m.key, userID, raw)
	if err != nil {
		return "", "", err
	}
	tag, err := m.pool.Exec(ctx, `
		INSERT INTO user_totp (user_id, secret_enc) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET secret_enc = EXCLUDED.secret_enc, last_used_step = NULL
		WHERE user_totp.confirmed_at IS NULL`, userID, sealed)
	if err != nil {
		return "", "", fmt.Errorf("auth: enroll: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", "", ErrAlreadyEnrolled
	}
	return totp.EncodeSecret(raw), totp.URI(email, raw), nil
}

// Confirm checks code against the pending secret, confirms it, replaces the recovery codes and
// rotates the session named by token into a two-factor session. It returns the new session token,
// its (unchanged) absolute expiry and the recovery codes in display form (shown once).
func (m *MFA) Confirm(ctx context.Context, token, userID, code string) (string, time.Time, []string, error) {
	now := m.now().UTC().Truncate(time.Microsecond)
	var newToken string
	var expires time.Time
	var codes []string
	err := pgx.BeginFunc(ctx, m.pool, func(tx pgx.Tx) error {
		var sealed []byte
		var confirmed *time.Time
		var last *int64
		err := tx.QueryRow(ctx, `SELECT secret_enc, confirmed_at, last_used_step FROM user_totp WHERE user_id = $1 FOR UPDATE`,
			userID).Scan(&sealed, &confirmed, &last)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidCode
		}
		if err != nil {
			return err
		}
		if confirmed != nil {
			return ErrAlreadyEnrolled
		}
		step, err := m.check(userID, sealed, last, code, now)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE user_totp SET confirmed_at = $2, last_used_step = $3 WHERE user_id = $1`,
			userID, now, step); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM recovery_codes WHERE user_id = $1`, userID); err != nil {
			return err
		}
		for range RecoveryCodeCount {
			c, err := newRecoveryCode()
			if err != nil {
				return err
			}
			h, err := m.recoveryHash(normalizeRecovery(c))
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO recovery_codes (user_id, code_hash) VALUES ($1, $2)`, userID, h); err != nil {
				return err
			}
			codes = append(codes, c)
		}
		newToken, expires, err = rotateSession(ctx, tx, token, userID, now, true, nil)
		return err
	})
	if err != nil {
		return "", time.Time{}, nil, err
	}
	return newToken, expires, codes, nil
}

// Verify checks a TOTP code or, when code is empty, a recovery code, and rotates the session named
// by token into a two-factor session, returning the new token and its (unchanged) expiry. Wrong,
// replayed (time step at or before the last accepted one) and used codes, and users without a
// confirmed TOTP, all return ErrInvalidCode.
func (m *MFA) Verify(ctx context.Context, token, userID, code, recoveryCode string) (string, time.Time, error) {
	now := m.now().UTC().Truncate(time.Microsecond)
	var newToken string
	var expires time.Time
	err := pgx.BeginFunc(ctx, m.pool, func(tx pgx.Tx) error {
		var sealed []byte
		var last *int64
		// The row lock serializes concurrent verifications of one user, so a code is accepted once.
		err := tx.QueryRow(ctx, `SELECT secret_enc, last_used_step FROM user_totp
			WHERE user_id = $1 AND confirmed_at IS NOT NULL FOR UPDATE`, userID).Scan(&sealed, &last)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidCode
		}
		if err != nil {
			return err
		}
		if code == "" {
			n := normalizeRecovery(recoveryCode)
			if !validRecovery(n) {
				return ErrInvalidCode
			}
			h, err := m.recoveryHash(n)
			if err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `UPDATE recovery_codes SET used_at = $3
				WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`, userID, h, now)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return ErrInvalidCode
			}
		} else {
			step, err := m.check(userID, sealed, last, code, now)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE user_totp SET last_used_step = $2 WHERE user_id = $1`, userID, step); err != nil {
				return err
			}
		}
		newToken, expires, err = rotateSession(ctx, tx, token, userID, now, true, nil)
		return err
	})
	return newToken, expires, err
}

// check decrypts the secret and verifies code against it, rejecting steps at or before last.
func (m *MFA) check(userID string, sealed []byte, last *int64, code string, now time.Time) (int64, error) {
	secret, err := totp.Open(m.key, userID, sealed)
	if err != nil {
		return 0, fmt.Errorf("auth: totp secret of %s: %w", userID, err)
	}
	lastUsed := int64(-1)
	if last != nil {
		lastUsed = *last
	}
	step, ok := totp.Verify(secret, code, now, lastUsed)
	if !ok {
		return 0, ErrInvalidCode
	}
	return step, nil
}

// rotateSession deletes the live session named by token and inserts a new one for the same user with
// the same absolute expiry, returning its token and expiry. The new session keeps the old tenant, or
// gets newTenant when it is not nil. With mfa the new session proved the second factor now;
// otherwise it keeps the old session's mfa_at. A privilege change always gets a new token.
func rotateSession(ctx context.Context, tx pgx.Tx, token, userID string, now time.Time, mfa bool, newTenant *string) (string, time.Time, error) {
	h, ok := hashToken(token)
	if !ok {
		return "", time.Time{}, ErrNoSession
	}
	var tenant *string
	var expires time.Time
	var mfaAt *time.Time
	err := tx.QueryRow(ctx, `DELETE FROM sessions WHERE token_hash = $1 AND user_id = $2 AND expires_at > $3
		RETURNING tenant_id::text, expires_at, mfa_at`, h, userID, now).Scan(&tenant, &expires, &mfaAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, ErrNoSession
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth: rotate session: %w", err)
	}
	if mfa {
		mfaAt = &now
	}
	if newTenant != nil {
		tenant = newTenant
	}
	// Keep the original absolute expiry: rotating must never extend the session.
	tok, err := insertSessionUntil(ctx, tx, userID, tenant, now, expires, mfaAt)
	return tok, expires, err
}

// newRecoveryCode returns 10 random Crockford base32 characters as XXXXX-XXXXX.
func newRecoveryCode() (string, error) {
	raw := make([]byte, recoveryLen)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	var b strings.Builder
	for i, v := range raw {
		if i == recoveryLen/2 {
			b.WriteByte('-')
		}
		b.WriteByte(crockford[v&31]) // 256 is a multiple of 32, so this is uniform
	}
	return b.String(), nil
}

// normalizeRecovery turns what a person typed into the hashed form: uppercase, without spaces or
// hyphens, with the look-alikes O→0 and I/L→1.
func normalizeRecovery(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch r {
		case ' ', '-', '\t':
		case 'O':
			b.WriteByte('0')
		case 'I', 'L':
			b.WriteByte('1')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func validRecovery(n string) bool {
	return len(n) == recoveryLen && strings.Trim(n, crockford) == ""
}

// ResetMFA deletes the TOTP, the recovery codes and every session of the account with email (the
// support path for a lost phone) and returns the normalized email, or ErrNoAccount.
func (s *Store) ResetMFA(ctx context.Context, email string) (string, error) {
	email = NormalizeEmail(email)
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `SELECT id::text FROM users WHERE lower(email) = $1`, email).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoAccount
		}
		if err != nil {
			return err
		}
		for _, q := range []string{
			`DELETE FROM recovery_codes WHERE user_id = $1`,
			`DELETE FROM user_totp WHERE user_id = $1`,
			`DELETE FROM sessions WHERE user_id = $1`,
		} {
			if _, err := tx.Exec(ctx, q, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return email, nil
}
