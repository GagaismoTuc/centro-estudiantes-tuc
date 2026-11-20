package roster

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
)

// CodeLifetime is how long an access code stays valid.
const CodeLifetime = 30 * 24 * time.Hour

var (
	// ErrForbidden means the session user is not a school_admin of the session's tenant.
	ErrForbidden = errors.New("roster: forbidden")
	// ErrNotFound means the roster member does not exist in the session's tenant.
	ErrNotFound = errors.New("roster: member not found")
	// ErrHasAccount means the roster member already claimed an account.
	ErrHasAccount = errors.New("roster: member already has an account")
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Member is a roster entry with its claim state.
type Member struct {
	ID, FullName, DocumentNumber string
	HasActiveCode, HasAccount    bool
}

// Issued is a freshly issued code in plain text; it is never stored or shown again.
type Issued struct {
	MemberID, FullName, DocumentNumber, Code string
	ExpiresAt                                time.Time
}

// Store runs roster operations for school admins through the application pool.
type Store struct {
	pool *pgxpool.Pool
	key  []byte
	now  func() time.Time
	// publish publishes, in tx, the tenant's closed election not yet published (see SetPublisher).
	publish func(ctx context.Context, tx pgx.Tx, tenant string) error
}

// SetPublisher sets the function that publishes, in the caller's transaction, the tenant's election
// whose end has passed but whose result nobody read yet (elections.Store.PublishClosed). The Board
// operations run it first, so they see the Board that publication hands over (#58 S7, S10); with
// none set they skip it.
func (s *Store) SetPublisher(publish func(ctx context.Context, tx pgx.Tx, tenant string) error) {
	s.publish = publish
}

// publishClosed runs the publisher, if any, in tx.
func (s *Store) publishClosed(ctx context.Context, tx pgx.Tx, tenant string) error {
	if s.publish == nil {
		return nil
	}
	return s.publish(ctx, tx, tenant)
}

// NewStore returns a Store; key is the access-code HMAC key and now the clock. It panics when now
// is nil.
func NewStore(pool *pgxpool.Pool, key []byte, now func() time.Time) *Store {
	if now == nil {
		panic("roster: NewStore needs a clock")
	}
	return &Store{pool: pool, key: key, now: now}
}

// Authorize returns nil when sess may run roster operations (school_admin of its tenant with a
// verified second factor), ErrForbidden or auth.ErrMFARequired otherwise. Callers use it to refuse a
// request before reading its body; every operation checks again in its own transaction.
func (s *Store) Authorize(ctx context.Context, sess auth.Session) error {
	return s.asAdmin(ctx, sess, func(pgx.Tx) error { return nil })
}

// asAdmin runs fn in the session's tenant after checking, in the same transaction, that the user
// is a school_admin there, and then that the session verified the second factor
// (auth.ErrMFARequired otherwise). The session's tenant alone is never trusted.
func (s *Store) asAdmin(ctx context.Context, sess auth.Session, fn func(pgx.Tx) error) error {
	if sess.TenantID == "" {
		return ErrForbidden
	}
	return db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT FROM memberships WHERE tenant_id = $1 AND user_id = $2 AND role = 'school_admin')`,
			sess.TenantID, sess.UserID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return ErrForbidden
		}
		if err := auth.RequireMFA(sess); err != nil {
			return err
		}
		return fn(tx)
	})
}

// Import adds the members of a CSV file atomically; existing document numbers are skipped.
func (s *Store) Import(ctx context.Context, sess auth.Session, data []byte) (inserted, skipped int, err error) {
	err = s.asAdmin(ctx, sess, func(tx pgx.Tx) error {
		rows, err := parseCSV(data)
		if err != nil {
			return err
		}
		names, docs := make([]string, len(rows)), make([]string, len(rows))
		for i, r := range rows {
			names[i], docs[i] = r.name, r.doc
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO roster_members (tenant_id, full_name, document_number)
			SELECT $1, n, d FROM unnest($2::text[], $3::text[]) AS t(n, d)
			ON CONFLICT (tenant_id, document_number) DO NOTHING`, sess.TenantID, names, docs)
		inserted = int(tag.RowsAffected())
		skipped = len(rows) - inserted
		return err
	})
	if err != nil {
		return 0, 0, err
	}
	return inserted, skipped, nil
}

// List returns the tenant's roster ordered by name.
func (s *Store) List(ctx context.Context, sess auth.Session) ([]Member, error) {
	out := []Member{}
	err := s.asAdmin(ctx, sess, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT m.id::text, m.full_name, m.document_number,
			       EXISTS (SELECT FROM access_codes c WHERE c.roster_member_id = m.id
			               AND c.used_at IS NULL AND c.revoked_at IS NULL AND c.expires_at > $2),
			       EXISTS (SELECT FROM memberships ms WHERE ms.roster_member_id = m.id)
			FROM roster_members m WHERE m.tenant_id = $1
			ORDER BY m.full_name, m.id`, sess.TenantID, s.now())
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Member])
		return err
	})
	return out, err
}

// Issue gives one member a new code, revoking their previous unused one.
func (s *Store) Issue(ctx context.Context, sess auth.Session, memberID string) (Issued, error) {
	var out Issued
	err := s.asAdmin(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(memberID) {
			return ErrNotFound
		}
		if err := lockIssuance(ctx, tx, sess.TenantID); err != nil {
			return err
		}
		var hasAccount bool
		err := tx.QueryRow(ctx, `
			SELECT m.id::text, m.full_name, m.document_number,
			       EXISTS (SELECT FROM memberships ms WHERE ms.roster_member_id = m.id)
			FROM roster_members m WHERE m.id = $1 AND m.tenant_id = $2 FOR UPDATE OF m`,
			memberID, sess.TenantID).Scan(&out.MemberID, &out.FullName, &out.DocumentNumber, &hasAccount)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if hasAccount {
			return ErrHasAccount
		}
		return s.issue(ctx, tx, sess.TenantID, &out)
	})
	return out, err
}

// lockIssuance serializes code issuing per tenant until the transaction ends. Row locks alone are not
// enough: a concurrent bulk issue would not re-check "has an active code" after waiting and would revoke
// codes the first call already returned. Under READ COMMITTED, every statement after the lock sees the
// codes committed by the previous holder.
func lockIssuance(ctx context.Context, tx pgx.Tx, tenant string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('access_codes:' || $1::text, 0))`, tenant)
	return err
}

// IssueMissing gives a code to every member without an account and without an active code.
func (s *Store) IssueMissing(ctx context.Context, sess auth.Session) ([]Issued, error) {
	out := []Issued{}
	err := s.asAdmin(ctx, sess, func(tx pgx.Tx) error {
		if err := lockIssuance(ctx, tx, sess.TenantID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT m.id::text, m.full_name, m.document_number FROM roster_members m
			WHERE m.tenant_id = $1
			  AND NOT EXISTS (SELECT FROM memberships ms WHERE ms.roster_member_id = m.id)
			  AND NOT EXISTS (SELECT FROM access_codes c WHERE c.roster_member_id = m.id
			                  AND c.used_at IS NULL AND c.revoked_at IS NULL AND c.expires_at > $2)
			ORDER BY m.full_name, m.id FOR UPDATE OF m`, sess.TenantID, s.now())
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Issued, error) {
			var i Issued
			return i, r.Scan(&i.MemberID, &i.FullName, &i.DocumentNumber)
		})
		if err != nil {
			return err
		}
		for i := range out {
			if err := s.issue(ctx, tx, sess.TenantID, &out[i]); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// issue revokes the member's unused code and stores the HMAC of a new one, filling in Code and
// ExpiresAt.
func (s *Store) issue(ctx context.Context, tx pgx.Tx, tenant string, m *Issued) error {
	if len(s.key) < MinKeyLen {
		return errors.New("roster: access code key is not configured")
	}
	code, err := NewCode()
	if err != nil {
		return err
	}
	now := s.now().UTC().Truncate(time.Microsecond) // PostgreSQL precision
	if _, err := tx.Exec(ctx, `
		UPDATE access_codes SET revoked_at = $2
		WHERE roster_member_id = $1 AND used_at IS NULL AND revoked_at IS NULL`, m.MemberID, now); err != nil {
		return fmt.Errorf("roster: revoke code: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO access_codes (tenant_id, roster_member_id, code_hash, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, tenant, m.MemberID, HashCode(s.key, code), now, now.Add(CodeLifetime)); err != nil {
		return fmt.Errorf("roster: insert code: %w", err)
	}
	m.Code, m.ExpiresAt = code, now.Add(CodeLifetime)
	return nil
}
