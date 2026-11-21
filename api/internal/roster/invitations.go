package roster

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/jackc/pgx/v5"
)

// Invite issues a single-use invitation in the school, invalidating an earlier invitation for the same email.
// It returns the token and the school's name, which the invitation mail names.
func (s *Store) Invite(ctx context.Context, sess auth.Session, email, role string) (token, school string, err error) {
	err = s.asAdmin(ctx, sess, func(tx pgx.Tx) error {
		// Serialize reissues, including when there is no earlier row to lock.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, sess.TenantID+":"+email); err != nil {
			return err
		}
		now := s.now().UTC().Truncate(time.Microsecond)
		if _, err := tx.Exec(ctx, `UPDATE school_invitations SET used_at=$3 WHERE tenant_id=$1 AND email=$2 AND used_at IS NULL`, sess.TenantID, email, now); err != nil {
			return err
		}
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return err
		}
		hash := sha256.Sum256(raw)
		if _, err := tx.Exec(ctx, `INSERT INTO school_invitations(tenant_id,token_hash,email,role,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6)`, sess.TenantID, hash[:], email, role, now, now.Add(48*time.Hour)); err != nil {
			return err
		}
		school, err = tenantName(ctx, tx, sess.TenantID)
		if err != nil {
			return err
		}
		token = base64.RawURLEncoding.EncodeToString(raw)
		return nil
	})
	if err != nil {
		return "", "", fmt.Errorf("issue invitation: %w", err)
	}
	return token, school, nil
}

// ErrInvitationLogin means the invitation belongs to an existing account which must sign in first.
var ErrInvitationLogin = errors.New("roster: invitation requires the invited account")

// AcceptInvitation spends a token and creates or joins its account in one transaction.
// Existing accounts must already be signed in and have proved their second factor.
func (s *Store) AcceptInvitation(ctx context.Context, token, password, previousToken string, sess auth.Session) (out Claimed, created bool, err error) {
	raw, decodeErr := base64.RawURLEncoding.DecodeString(token)
	if decodeErr != nil || len(raw) != 32 {
		return out, false, auth.ErrInvalidToken
	}
	hash := sha256.Sum256(raw)
	err = db.WithInvitationHash(ctx, s.pool, hash[:], func(tx pgx.Tx) error {
		var tenant, email, role string
		var expires time.Time
		err := tx.QueryRow(ctx, `SELECT tenant_id::text,email,role,expires_at FROM school_invitations WHERE token_hash=$1 AND used_at IS NULL FOR UPDATE`, hash[:]).Scan(&tenant, &email, &role, &expires)
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.ErrInvalidToken
		}
		if err != nil {
			return err
		}
		// Take time only after acquiring the row lock: a queued acceptance must not
		// use a timestamp from before the invitation expired while waiting.
		now := s.now().UTC().Truncate(time.Microsecond)
		if !expires.After(now) {
			return auth.ErrInvalidToken
		}
		if err := db.SetTenant(ctx, tx, tenant); err != nil {
			return err
		}
		var user string
		err = tx.QueryRow(ctx, `SELECT id::text FROM users WHERE lower(email)=$1`, email).Scan(&user)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if err := auth.ValidatePassword(password); err != nil {
				return err
			}
			pwHash, err := auth.HashPassword(password)
			if err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `INSERT INTO users(email,password_hash,email_verified_at) VALUES($1,$2,$3) RETURNING id::text`, email, pwHash, now).Scan(&user); err != nil {
				return err
			}
			if err := auth.DeleteSessionIn(ctx, tx, previousToken); err != nil {
				return err
			}
			created = true
		case err != nil:
			return err
		default:
			if sess.UserID == "" || sess.UserID != user {
				return ErrInvitationLogin
			}
			if err := auth.RequireMFA(sess); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE users SET email_verified_at=coalesce(email_verified_at,$2) WHERE id=$1`, user, now); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,$3) ON CONFLICT (tenant_id,user_id,role) DO NOTHING`, tenant, user, role); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE school_invitations SET used_at=$2 WHERE token_hash=$1`, hash[:], now); err != nil {
			return err
		}
		out.UserID, out.TenantID = user, tenant
		if created {
			out.Token, err = auth.InsertSession(ctx, tx, user, &tenant, now)
			out.Expires = now.Add(auth.SessionLifetime)
		} else {
			out.Token, out.Expires, err = auth.RotateSessionIntoTenant(ctx, tx, previousToken, user, tenant, now)
		}
		return err
	})
	if errors.Is(mapClaimError(err), auth.ErrEmailTaken) {
		return Claimed{}, false, ErrInvitationLogin
	}
	if err != nil {
		return Claimed{}, false, err
	}
	return out, created, nil
}
