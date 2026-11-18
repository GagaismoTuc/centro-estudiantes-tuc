package finance

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Error messages never carry the reason or a name (AGENTS.md privacy rules).
var (
	ErrReasonRequired = errors.New("finance: exemption reason required")
	ErrReasonTooLong  = errors.New("finance: exemption reason too long")
	ErrNotOwed        = errors.New("finance: roster member does not owe the due")
	ErrAlreadyExempt  = errors.New("finance: roster member already exempt")
)

// maxExemptionReason is the limit in characters (runes) of the trimmed reason, as stored.
const maxExemptionReason = 500

// CreateExemption records, with a reason, that a Roster member in the Due's snapshot does not owe it.
// It locks the member's finance_due_members row so concurrent Exemptions (and #78 Payments) for the
// same Student serialize; the due row is held FOR SHARE so UpdateDue/DeleteDue cannot race the freeze.
func (s *Store) CreateExemption(ctx context.Context, sess auth.Session, dueID, rosterMemberID, reason string) (out DueDetail, err error) {
	err = s.withDueRole(ctx, sess, false, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(dueID) || !uuidPattern.MatchString(rosterMemberID) {
			return ErrNotFound
		}
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return ErrReasonRequired
		}
		if utf8.RuneCountInString(reason) > maxExemptionReason {
			return ErrReasonTooLong
		}

		var due Due
		e := tx.QueryRow(ctx, `
			SELECT id::text, name, amount_cents, to_char(due_date, 'YYYY-MM-DD'), created_at
			FROM finance_dues
			WHERE tenant_id = $1 AND id = $2
			FOR SHARE`, sess.TenantID, dueID).
			Scan(&due.ID, &due.Name, &due.AmountCents, &due.DueDate, &due.CreatedAt)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		var inRoster bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM roster_members WHERE tenant_id = $1 AND id = $2)`,
			sess.TenantID, rosterMemberID).Scan(&inRoster); e != nil {
			return e
		}
		if !inRoster {
			return ErrNotFound
		}
		var one int
		e = tx.QueryRow(ctx, `
			SELECT 1 FROM finance_due_members
			WHERE tenant_id = $1 AND due_id = $2 AND roster_member_id = $3
			FOR UPDATE`, sess.TenantID, dueID, rosterMemberID).Scan(&one)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrNotOwed
		}
		if e != nil {
			return e
		}
		var exempt bool
		if e = tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM finance_due_exemptions
				WHERE tenant_id = $1 AND due_id = $2 AND roster_member_id = $3
			)`, sess.TenantID, dueID, rosterMemberID).Scan(&exempt); e != nil {
			return e
		}
		if exempt {
			return ErrAlreadyExempt
		}
		// Payments still in force (their Entry not reversed) rule out an Exemption (#79 R3).
		var paid int64
		if e = tx.QueryRow(ctx, `SELECT `+paidSQL("$2", "$3"), sess.TenantID, dueID, rosterMemberID).Scan(&paid); e != nil {
			return e
		}
		if paid > 0 {
			return ErrHasPayments
		}
		_, e = tx.Exec(ctx, `
			INSERT INTO finance_due_exemptions (tenant_id, due_id, roster_member_id, reason, created_by, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			sess.TenantID, dueID, rosterMemberID, reason, sess.UserID, s.now())
		var pe *pgconn.PgError
		if errors.As(e, &pe) && pe.Code == "23505" {
			return ErrAlreadyExempt
		}
		if e != nil {
			return e
		}
		out, e = buildDueDetail(ctx, tx, sess.TenantID, due)
		return e
	})
	return
}
