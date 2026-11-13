package roster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
)

var (
	// ErrInvalidCode covers malformed, unknown, used, revoked and expired codes, and codes whose roster
	// member already has an account: callers must not be able to tell them apart.
	ErrInvalidCode = errors.New("roster: invalid access code")
	// ErrAlreadyMember means the user already belongs to the code's tenant.
	ErrAlreadyMember = errors.New("roster: already a member of the code's tenant")
)

// Claimed is the account a code was spent on.
type Claimed struct {
	UserID, TenantID string
	Token            string // new session token; set by Claim only
	VerifyToken      string // verify_email token to send to the new account; set by Claim only
}

// useCode runs fn in one transaction holding the live code row locked, with the code's tenant
// declared, and marks the code used when fn succeeds. A concurrent claim of the same code waits for
// the lock and then no longer matches used_at IS NULL, so it gets ErrInvalidCode.
func (s *Store) useCode(ctx context.Context, code string, fn func(tx pgx.Tx, tenant, member string) error) error {
	if !ValidCode(code) || len(s.key) < MinKeyLen {
		return ErrInvalidCode
	}
	hash := HashCode(s.key, code)
	return db.WithCodeHash(ctx, s.pool, hash, func(tx pgx.Tx) error {
		var tenant, member string
		err := tx.QueryRow(ctx, `
			SELECT tenant_id::text, roster_member_id::text FROM access_codes
			WHERE code_hash = $1 AND used_at IS NULL AND revoked_at IS NULL AND expires_at > $2
			FOR UPDATE`, hash, s.now()).Scan(&tenant, &member)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidCode
		}
		if err != nil {
			return fmt.Errorf("roster: find code: %w", err)
		}
		if err := db.SetTenant(ctx, tx, tenant); err != nil {
			return err
		}
		var linked bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT FROM memberships WHERE roster_member_id = $1)`,
			member).Scan(&linked); err != nil {
			return err
		}
		if linked {
			return ErrInvalidCode
		}
		if err := fn(tx, tenant, member); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE access_codes SET used_at = $2 WHERE code_hash = $1`,
			hash, s.now().UTC().Truncate(time.Microsecond))
		return err
	})
}

// Claim creates the account of the code's roster member: user, student membership and session.
// email must already be normalized and password validated.
func (s *Store) Claim(ctx context.Context, code, email, password string) (Claimed, error) {
	var out Claimed
	err := s.useCode(ctx, code, func(tx pgx.Tx, tenant, member string) error {
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT FROM users WHERE lower(email) = $1)`, email).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return auth.ErrEmailTaken
		}
		hash, err := auth.HashPassword(password)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id::text`,
			email, hash).Scan(&out.UserID); err != nil {
			return err
		}
		if err := addStudent(ctx, tx, tenant, out.UserID, member); err != nil {
			return err
		}
		out.TenantID = tenant
		if out.Token, err = auth.InsertSession(ctx, tx, out.UserID, &tenant, s.now()); err != nil {
			return err
		}
		out.VerifyToken, err = auth.IssueEmailToken(ctx, tx, out.UserID, auth.PurposeVerifyEmail, s.now())
		return err
	})
	return out, mapClaimError(err)
}

// Redeem adds the code's roster member to the signed-in user and switches the session (named by
// token) to the code's tenant.
func (s *Store) Redeem(ctx context.Context, sess auth.Session, token, code string) (Claimed, error) {
	out := Claimed{UserID: sess.UserID}
	err := s.useCode(ctx, code, func(tx pgx.Tx, tenant, member string) error {
		var already bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT FROM memberships WHERE tenant_id = $1 AND user_id = $2)`,
			tenant, sess.UserID).Scan(&already); err != nil {
			return err
		}
		if already {
			return ErrAlreadyMember
		}
		if err := addStudent(ctx, tx, tenant, sess.UserID, member); err != nil {
			return err
		}
		out.TenantID = tenant
		return auth.SwitchSessionTenant(ctx, tx, token, tenant)
	})
	return out, mapClaimError(err)
}

func addStudent(ctx context.Context, tx pgx.Tx, tenant, user, member string) error {
	_, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role, roster_member_id)
		VALUES ($1, $2, 'student', $3)`, tenant, user, member)
	return err
}

// mapClaimError turns unique violations from concurrent requests into the errors a sequential
// request would have got.
func mapClaimError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return err
	}
	switch pgErr.ConstraintName {
	case "users_email_lower_key":
		return auth.ErrEmailTaken
	case "memberships_tenant_id_user_id_key":
		return ErrAlreadyMember
	case "memberships_roster_member_id_key":
		return ErrInvalidCode
	}
	return err
}
