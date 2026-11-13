package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Purpose is what an emailed token may be used for; a token works for its own purpose only.
type Purpose string

const (
	PurposeVerifyEmail   Purpose = "verify_email"
	PurposeSignIn        Purpose = "sign_in"
	PurposeResetPassword Purpose = "reset_password"
)

// Lifetime is how long a token of purpose p stays valid.
func (p Purpose) Lifetime() time.Duration {
	switch p {
	case PurposeVerifyEmail:
		return 48 * time.Hour
	case PurposeSignIn:
		return 15 * time.Minute
	default:
		return 30 * time.Minute
	}
}

// ErrInvalidToken covers malformed, unknown, used, expired and other-purpose tokens alike.
var ErrInvalidToken = errors.New("auth: invalid email token")

// Emailed is a freshly issued token and the address to send it to. Token is empty when nothing
// must be sent (unknown email, or email already verified).
type Emailed struct {
	Email, Token string
}

// IssueEmailToken stores a new token of purpose p for userID inside tx and returns it raw (base64url
// of 32 random bytes). Only its SHA-256 is stored. The user's previous unused token of the same
// purpose is marked used first; the user row is locked so concurrent issues cannot both stay live.
func IssueEmailToken(ctx context.Context, tx pgx.Tx, userID string, p Purpose, now time.Time) (string, error) {
	now = now.UTC().Truncate(time.Microsecond)
	if _, err := tx.Exec(ctx, `SELECT FROM users WHERE id = $1 FOR NO KEY UPDATE`, userID); err != nil {
		return "", fmt.Errorf("auth: lock user: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE email_tokens SET used_at = $3 WHERE user_id = $1 AND purpose = $2 AND used_at IS NULL`,
		userID, p, now); err != nil {
		return "", fmt.Errorf("auth: invalidate tokens: %w", err)
	}
	raw := make([]byte, tokenLen)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: token: %w", err)
	}
	sum := sha256.Sum256(raw)
	if _, err := tx.Exec(ctx, `INSERT INTO email_tokens (token_hash, user_id, purpose, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, sum[:], userID, p, now, now.Add(p.Lifetime())); err != nil {
		return "", fmt.Errorf("auth: insert email token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// RequestVerification issues a verify_email token unless the user's email is already verified.
func (s *Store) RequestVerification(ctx context.Context, userID string) (Emailed, error) {
	return s.issue(ctx, `id::text = $1`, userID, PurposeVerifyEmail)
}

// RequestByEmail issues a sign_in or reset_password token for the account with email, if any.
func (s *Store) RequestByEmail(ctx context.Context, email string, p Purpose) (Emailed, error) {
	return s.issue(ctx, `lower(email) = $1`, NormalizeEmail(email), p)
}

func (s *Store) issue(ctx context.Context, where, arg string, p Purpose) (Emailed, error) {
	var out Emailed
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var id string
		var verified bool
		err := tx.QueryRow(ctx, `SELECT id::text, email, email_verified_at IS NOT NULL FROM users WHERE `+where, arg).
			Scan(&id, &out.Email, &verified)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && p == PurposeVerifyEmail && verified) {
			out = Emailed{}
			return nil
		}
		if err != nil {
			return err
		}
		out.Token, err = IssueEmailToken(ctx, tx, id, p, s.now())
		return err
	})
	if err != nil {
		return Emailed{}, fmt.Errorf("auth: issue %s token: %w", p, err)
	}
	return out, nil
}

// consume marks the live token of purpose p used inside tx and returns its user, or ErrInvalidToken.
func (s *Store) consume(ctx context.Context, tx pgx.Tx, token string, p Purpose) (string, error) {
	h, ok := hashToken(token)
	if !ok {
		return "", ErrInvalidToken
	}
	var user string
	err := tx.QueryRow(ctx, `UPDATE email_tokens SET used_at = $3
		WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > $3
		RETURNING user_id::text`, h, p, s.now().UTC().Truncate(time.Microsecond)).Scan(&user)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrInvalidToken
	}
	return user, err
}

func (s *Store) markVerified(ctx context.Context, tx pgx.Tx, user string) error {
	_, err := tx.Exec(ctx, `UPDATE users SET email_verified_at = coalesce(email_verified_at, $2) WHERE id = $1`,
		user, s.now().UTC().Truncate(time.Microsecond))
	return err
}

// VerifyEmail consumes a verify_email token and marks the user's email verified.
func (s *Store) VerifyEmail(ctx context.Context, token string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		user, err := s.consume(ctx, tx, token, PurposeVerifyEmail)
		if err != nil {
			return err
		}
		return s.markVerified(ctx, tx, user)
	})
}

// ConsumeSignInLink consumes a sign_in token, marks the email verified (the link proves inbox
// access) and returns the user; the caller then creates the session.
func (s *Store) ConsumeSignInLink(ctx context.Context, token string) (string, error) {
	var user string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		if user, err = s.consume(ctx, tx, token, PurposeSignIn); err != nil {
			return err
		}
		return s.markVerified(ctx, tx, user)
	})
	return user, err
}

// ResetPassword consumes a reset_password token, stores the new password and signs the user out
// everywhere. A weak password returns ErrWeakPassword before the token is touched.
func (s *Store) ResetPassword(ctx context.Context, token, password string) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		user, err := s.consume(ctx, tx, token, PurposeResetPassword)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, user, hash); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, user)
		return err
	})
}
