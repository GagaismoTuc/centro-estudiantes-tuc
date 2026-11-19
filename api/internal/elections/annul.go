package elections

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
)

// asElectionCommission runs fn with election id of the session's tenant, locked, after checking in
// the same transaction, in order: a membership in the tenant (ErrForbidden), the election
// (ErrNotFound), the user's roster entry on the election's commission, ended or not (ErrForbidden),
// and the second factor (auth.ErrMFARequired). It holds the commission lock, like asCommissionMember,
// so a concurrent Designate commits either before the check or after fn's write.
func (s *Store) asElectionCommission(ctx context.Context, sess auth.Session, id string, fn func(pgx.Tx, Election, bool) error) error {
	if sess.TenantID == "" {
		return ErrForbidden
	}
	return db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		var veedor bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT FROM memberships WHERE tenant_id = $1 AND user_id = $2 AND role = 'veedor')`, sess.TenantID, sess.UserID).Scan(&veedor); err != nil {
			return err
		}
		if veedor {
			if err := auth.RequireMFA(sess); err != nil {
				return err
			}
		}
		if err := lockCommission(ctx, tx, sess.TenantID); err != nil {
			return err
		}
		a, err := readAccess(ctx, tx, sess.TenantID, sess.UserID)
		if err != nil {
			return err
		}
		if !a.member {
			return ErrForbidden
		}
		if !uuidPattern.MatchString(id) {
			return ErrNotFound
		}
		e, err := getElection(ctx, tx, `e.id = $2 FOR UPDATE OF e`, sess.TenantID, id)
		if err != nil {
			return err
		}
		var onCommission bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT FROM electoral_commission_members cm
			               JOIN memberships ms ON ms.tenant_id = cm.tenant_id AND ms.roster_member_id = cm.roster_member_id
			               WHERE cm.tenant_id = $1 AND cm.commission_id = $2 AND ms.user_id = $3)`,
			sess.TenantID, e.CommissionID, sess.UserID).Scan(&onCommission); err != nil {
			return err
		}
		if !onCommission && !veedor {
			return ErrForbidden
		}
		if err := auth.RequireMFA(sess); err != nil {
			return err
		}
		return fn(tx, e, veedor)
	})
}

// AuthorizeAnnulment returns nil when sess may annul election id now; otherwise ErrForbidden,
// ErrNotFound or auth.ErrMFARequired. Callers use it to refuse a request before reading its body;
// Annul checks again in its own transaction.
func (s *Store) AuthorizeAnnulment(ctx context.Context, sess auth.Session, id string) error {
	return s.asElectionCommission(ctx, sess, id, func(pgx.Tx, Election, bool) error { return nil })
}

// Annul annuls election id of the session's tenant with a public reason, for a member of its
// commission (see asElectionCommission), and ends that commission's mandate, which frees the tenant
// for another election. Errors after the permission checks: ErrReasonRequired, ErrReasonTooLong, then
// ErrElectionAnnulled (the first reason stays) and ErrElectionPublished (published, or closed at the
// store's clock). On error nothing is written; of two concurrent calls exactly one annuls.
func (s *Store) Annul(ctx context.Context, sess auth.Session, id, reason string) (Election, error) {
	var out Election
	err := s.asElectionCommission(ctx, sess, id, func(tx pgx.Tx, e Election, veedor bool) error {
		reason := strings.TrimSpace(reason)
		switch {
		case reason == "":
			return ErrReasonRequired
		case utf8.RuneCountInString(reason) > MaxReasonLength:
			return ErrReasonTooLong
		case e.AnnulledAt != nil:
			return ErrElectionAnnulled
		case e.PublishedAt != nil || State(s.now(), e) == StatusClosed:
			return ErrElectionPublished
		}
		now := s.stamp()
		if _, err := tx.Exec(ctx, `UPDATE elections SET annulled_at = $3, annulment_reason = $4, annulled_by_veedor = $5, updated_at = $3 WHERE tenant_id = $1 AND id = $2`,
			sess.TenantID, e.ID, now, reason, veedor); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE electoral_commissions SET ended_at = $3 WHERE tenant_id = $1 AND id = $2 AND ended_at IS NULL`,
			sess.TenantID, e.CommissionID, now); err != nil {
			return err
		}
		var err error
		out, err = getElection(ctx, tx, `e.id = $2`, sess.TenantID, e.ID)
		return err
	})
	return out, err
}

// ViewLatest returns, to any member of the session's tenant, its election in progress or, when there
// is none, its most recently created election (an annulled one stays visible with its reason):
// ErrForbidden, or ErrNotFound when the tenant has no election.
func (s *Store) ViewLatest(ctx context.Context, sess auth.Session) (Election, error) {
	var out Election
	err := s.asMember(ctx, sess, func(tx pgx.Tx) error {
		var err error
		out, err = getElection(ctx, tx,
			`TRUE ORDER BY (e.published_at IS NULL AND e.annulled_at IS NULL) DESC, e.created_at DESC, e.id DESC LIMIT 1`, sess.TenantID)
		return err
	})
	return out, err
}
