package finance

import (
	"context"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/jackc/pgx/v5"
)

// CalculatorSummary holds the aggregate totals an Officer's financial calculators read.
// Balance and OwedOnDues are exact decimal strings in integer cents (sums may exceed int64).
type CalculatorSummary struct {
	RosterCount int
	Balance     string
	OwedOnDues  string
}

// CalculatorSummary returns the active school's current roster headcount, Fund balance and total
// still owed across all Dues (excluding exempt Roster members, A13). Restricted to Officers with a
// verified second factor (ADR 0001 2FA rule 5). Read-only; never exposes names or per-student rows.
func (s *Store) CalculatorSummary(ctx context.Context, sess auth.Session) (out CalculatorSummary, err error) {
	err = s.withDueRole(ctx, sess, false, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `SELECT count(*)::int FROM roster_members WHERE tenant_id = $1`, sess.TenantID).Scan(&out.RosterCount); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `SELECT COALESCE(sum(CASE direction WHEN 'income' THEN amount_cents ELSE -amount_cents END), 0)::text FROM finance_entries WHERE tenant_id = $1`, sess.TenantID).Scan(&out.Balance); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(GREATEST(d.amount_cents - COALESCE(p.paid, 0), 0)), 0)::text
			FROM finance_due_members dm
			JOIN finance_dues d ON d.tenant_id = dm.tenant_id AND d.id = dm.due_id
			LEFT JOIN finance_due_exemptions e
				ON e.tenant_id = dm.tenant_id AND e.due_id = dm.due_id AND e.roster_member_id = dm.roster_member_id
			LEFT JOIN (
				SELECT dp.due_id, dp.roster_member_id, SUM(dp.amount_cents) AS paid
				FROM finance_due_payments dp
				WHERE dp.tenant_id = $1
				  AND NOT EXISTS (
					  SELECT 1 FROM finance_entries r
					  WHERE r.tenant_id = dp.tenant_id AND r.reverses_id = dp.entry_id
				  )
				GROUP BY dp.due_id, dp.roster_member_id
			) p ON p.due_id = dm.due_id AND p.roster_member_id = dm.roster_member_id
			WHERE dm.tenant_id = $1 AND e.roster_member_id IS NULL`, sess.TenantID).Scan(&out.OwedOnDues)
	})
	return
}
