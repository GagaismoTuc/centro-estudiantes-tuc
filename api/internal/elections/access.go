package elections

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
)

// ErrForbidden means the session user may not run the operation in the session's tenant.
var ErrForbidden = errors.New("elections: forbidden")

// access is what the session user is in one tenant, read in the operation's own transaction.
type access struct {
	member         bool   // has a membership in the tenant
	designator     bool   // may designate the commission: an officer, or the school admin when there are no officers
	onCommission   bool   // their roster entry is on the current commission
	rosterMemberID string // the user's roster_member_id in the tenant, if any
}

func readAccess(ctx context.Context, tx pgx.Tx, tenant, user string) (access, error) {
	var a access
	var officer, admin, veedor, officersExist, commissionExists bool
	var rosterID *string
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT FROM memberships WHERE tenant_id = $1 AND user_id = $2),
		       EXISTS (SELECT FROM memberships m WHERE m.tenant_id = $1 AND m.user_id = $2 AND (m.role = 'officer'
		               OR EXISTS (SELECT FROM officer_positions p WHERE p.tenant_id = m.tenant_id AND p.roster_member_id = m.roster_member_id))),
		       EXISTS (SELECT FROM memberships WHERE tenant_id = $1 AND user_id = $2 AND role = 'school_admin'),
		       EXISTS (SELECT FROM memberships WHERE tenant_id = $1 AND user_id = $2 AND role = 'veedor'),
		       EXISTS (SELECT FROM electoral_commissions WHERE tenant_id = $1 AND ended_at IS NULL),
		       EXISTS (SELECT FROM officer_positions WHERE tenant_id = $1)
		       OR EXISTS (SELECT FROM memberships WHERE tenant_id = $1 AND role = 'officer'),
		       EXISTS (SELECT FROM electoral_commission_members cm
		               JOIN electoral_commissions c ON c.tenant_id = cm.tenant_id AND c.id = cm.commission_id AND c.ended_at IS NULL
		               JOIN memberships ms ON ms.tenant_id = cm.tenant_id AND ms.roster_member_id = cm.roster_member_id
		               WHERE cm.tenant_id = $1 AND ms.user_id = $2),
		       (SELECT roster_member_id::text FROM memberships WHERE tenant_id = $1 AND user_id = $2 AND roster_member_id IS NOT NULL LIMIT 1)`,
		tenant, user).
		Scan(&a.member, &officer, &admin, &veedor, &commissionExists, &officersExist, &a.onCommission, &rosterID)
	if rosterID != nil {
		a.rosterMemberID = *rosterID
	}
	a.designator = officer || (admin && !officersExist) || (veedor && (!officersExist || commissionExists))
	return a, err
}

func requireVeedorMFA(ctx context.Context, tx pgx.Tx, sess auth.Session) error {
	var veedor bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT FROM memberships WHERE tenant_id=$1 AND user_id=$2 AND role='veedor')`, sess.TenantID, sess.UserID).Scan(&veedor); err != nil {
		return err
	}
	if veedor {
		return auth.RequireMFA(sess)
	}
	return nil
}

// asDesignator runs fn in the session's tenant after checking, in the same transaction, that the
// user may designate the commission (ErrForbidden) and then that the session verified the second
// factor (auth.ErrMFARequired).
func (s *Store) asDesignator(ctx context.Context, sess auth.Session, fn func(pgx.Tx) error) error {
	if sess.TenantID == "" {
		return ErrForbidden
	}
	return db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		if err := requireVeedorMFA(ctx, tx, sess); err != nil {
			return err
		}
		a, err := readAccess(ctx, tx, sess.TenantID, sess.UserID)
		if err != nil {
			return err
		}
		if !a.designator {
			return ErrForbidden
		}
		if err := auth.RequireMFA(sess); err != nil {
			return err
		}
		return fn(tx)
	})
}

// AuthorizeDesignation returns nil when sess may designate the commission now, ErrForbidden or
// auth.ErrMFARequired otherwise. Callers use it to refuse a request before reading its body;
// Designate checks again in its own transaction.
func (s *Store) AuthorizeDesignation(ctx context.Context, sess auth.Session) error {
	return s.asDesignator(ctx, sess, func(pgx.Tx) error { return nil })
}

// Designate replaces the members of the session tenant's current commission (see ReplaceCommission),
// checking the permission in the same transaction as the write.
func (s *Store) Designate(ctx context.Context, sess auth.Session, rosterMemberIDs []string) (Commission, error) {
	var out Commission
	err := s.asDesignator(ctx, sess, func(tx pgx.Tx) error {
		var err error
		out, err = s.replaceCommission(ctx, tx, sess.TenantID, rosterMemberIDs)
		return err
	})
	return out, err
}

// Candidates lists the roster members of the session's tenant with an account, by name: those a
// designator may put on the commission.
func (s *Store) Candidates(ctx context.Context, sess auth.Session) ([]CommissionMember, error) {
	out := []CommissionMember{}
	err := s.asDesignator(ctx, sess, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT m.id::text, m.full_name FROM roster_members m
			WHERE m.tenant_id = $1 AND EXISTS (SELECT FROM memberships ms WHERE ms.tenant_id = m.tenant_id AND ms.roster_member_id = m.id)
			ORDER BY m.full_name, m.id`, sess.TenantID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CommissionMember])
		return err
	})
	return out, err
}

// CommissionStatus is the current commission (nil when none) and the caller's relation to it.
type CommissionStatus struct {
	Commission             *Commission
	IsMember, CanDesignate bool
}

// Status returns the session tenant's commission for any member of the tenant (ErrForbidden
// otherwise); it needs no second factor.
func (s *Store) Status(ctx context.Context, sess auth.Session) (CommissionStatus, error) {
	var out CommissionStatus
	if sess.TenantID == "" {
		return out, ErrForbidden
	}
	err := db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		if err := requireVeedorMFA(ctx, tx, sess); err != nil {
			return err
		}
		a, err := readAccess(ctx, tx, sess.TenantID, sess.UserID)
		if err != nil {
			return err
		}
		if !a.member {
			return ErrForbidden
		}
		out.IsMember, out.CanDesignate = a.onCommission, a.designator
		c, err := currentCommission(ctx, tx, sess.TenantID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		out.Commission = &c
		return nil
	})
	return out, err
}

// asCommissionMember runs fn in the session's tenant after checking, in the same transaction, that
// the user's roster entry is on the current commission (ErrForbidden) and then that the session
// verified the second factor (auth.ErrMFARequired). It holds the commission lock from before the
// check until the transaction ends, so a concurrent Designate commits either before the check or
// after fn's write.
func (s *Store) asCommissionMember(ctx context.Context, sess auth.Session, fn func(pgx.Tx) error) error {
	if sess.TenantID == "" {
		return ErrForbidden
	}
	return db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		var veedor bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT FROM memberships WHERE tenant_id=$1 AND user_id=$2 AND role='veedor')`, sess.TenantID, sess.UserID).Scan(&veedor); err != nil {
			return err
		}
		if veedor {
			return ErrForbidden
		}
		if err := lockCommission(ctx, tx, sess.TenantID); err != nil {
			return err
		}
		a, err := readAccess(ctx, tx, sess.TenantID, sess.UserID)
		if err != nil {
			return err
		}
		if !a.onCommission {
			return ErrForbidden
		}
		if err := auth.RequireMFA(sess); err != nil {
			return err
		}
		return fn(tx)
	})
}

// AuthorizeDraft returns nil when sess may create or edit the draft now, ErrForbidden or
// auth.ErrMFARequired otherwise. Callers use it to refuse a request before reading its body; Create
// and Edit check again in their own transaction.
func (s *Store) AuthorizeDraft(ctx context.Context, sess auth.Session) error {
	return s.asCommissionMember(ctx, sess, func(pgx.Tx) error { return nil })
}

// Create is CreateElection for a member of the current commission, checked in the same transaction.
func (s *Store) Create(ctx context.Context, sess auth.Session, d Draft) (Election, error) {
	var out Election
	err := s.asCommissionMember(ctx, sess, func(tx pgx.Tx) error {
		var err error
		out, err = s.createElection(ctx, tx, sess.TenantID, d)
		return err
	})
	return out, err
}

// Edit is UpdateDraft for a member of the current commission, checked in the same transaction.
func (s *Store) Edit(ctx context.Context, sess auth.Session, id string, d Draft) (Election, error) {
	var out Election
	err := s.asCommissionMember(ctx, sess, func(tx pgx.Tx) error {
		var err error
		out, err = s.updateDraft(ctx, tx, sess.TenantID, id, d)
		return err
	})
	return out, err
}

// asMember runs fn in the session's tenant when the user has a membership there (ErrForbidden
// otherwise); a Veedor must prove the second factor before reading election data.
func (s *Store) asMember(ctx context.Context, sess auth.Session, fn func(pgx.Tx) error) error {
	if sess.TenantID == "" {
		return ErrForbidden
	}
	return db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		var member, veedor bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT FROM memberships WHERE tenant_id = $1 AND user_id = $2), EXISTS (SELECT FROM memberships WHERE tenant_id = $1 AND user_id = $2 AND role = 'veedor')`, sess.TenantID, sess.UserID).Scan(&member, &veedor); err != nil {
			return err
		}
		if !member {
			return ErrForbidden
		}
		if veedor {
			if err := auth.RequireMFA(sess); err != nil {
				return err
			}
		}
		return fn(tx)
	})
}

// View returns the election id of the session's tenant to any member of it: ErrForbidden, or
// ErrNotFound for an id that is not a UUID or not an election of the tenant.
func (s *Store) View(ctx context.Context, sess auth.Session, id string) (Election, error) {
	var out Election
	err := s.asMember(ctx, sess, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(id) {
			return ErrNotFound
		}
		var err error
		out, err = getElection(ctx, tx, `e.id = $2`, sess.TenantID, id)
		return err
	})
	return out, err
}

// ViewCurrent returns the tenant's election in progress to any member of it: ErrForbidden, or
// ErrNotFound when there is none.
func (s *Store) ViewCurrent(ctx context.Context, sess auth.Session) (Election, error) {
	var out Election
	err := s.asMember(ctx, sess, func(tx pgx.Tx) error {
		var err error
		out, err = getElection(ctx, tx, `e.published_at IS NULL AND e.annulled_at IS NULL`, sess.TenantID)
		return err
	})
	return out, err
}
