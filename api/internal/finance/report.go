package finance

import (
	"context"
	"errors"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/jackc/pgx/v5"
)

// ErrInvalidRange is a missing or malformed date, or from after to.
var ErrInvalidRange = errors.New("finance: invalid range")

// CategoryTotal is the signed net (income +, expense −) of one Category's Entries in a range.
// A Reversal counts in its original's Category with the opposite sign, so it can turn an income
// Category negative.
type CategoryTotal struct {
	CategoryID, Direction, Name string
	TotalCents                  int64
}

// Report is the Fund report (Informe de caja) for the inclusive range [From, To] by Entry date.
// ClosingCents is OpeningCents plus the sum of Totals.
type Report struct {
	From, To                   string
	OpeningCents, ClosingCents int64
	// Entries in the range, oldest first (entry date, then recording time).
	Entries []Entry
	// Totals of the Categories with Entries in the range: income Categories first, then by name.
	Totals []CategoryTotal
}

// ValidateRange checks that from and to are YYYY-MM-DD dates and from is not after to.
func ValidateRange(from, to string) error {
	f, err := time.Parse("2006-01-02", from)
	if err != nil || f.Format("2006-01-02") != from {
		return ErrInvalidRange
	}
	t, err := time.Parse("2006-01-02", to)
	if err != nil || t.Format("2006-01-02") != to {
		return ErrInvalidRange
	}
	if f.After(t) {
		return ErrInvalidRange
	}
	return nil
}

// reportTotalsQuery returns, in one snapshot, the opening balance (the row with an empty
// Category id, always first) and the signed total of each Category in the range.
const reportTotalsQuery = `WITH s AS (
  SELECT category_id, entry_date, CASE direction WHEN 'income' THEN amount_cents ELSE -amount_cents END AS cents
  FROM finance_entries WHERE tenant_id=$1 AND entry_date <= $3::date)
SELECT id, direction, name, cents FROM (
  SELECT '' AS id, '' AS direction, '' AS name, COALESCE(sum(cents),0)::bigint AS cents FROM s WHERE entry_date < $2::date
  UNION ALL
  SELECT c.id::text, c.direction, c.name, sum(s.cents)::bigint FROM s JOIN finance_categories c ON c.tenant_id=$1 AND c.id=s.category_id
    WHERE s.entry_date >= $2::date GROUP BY c.id, c.direction, c.name) t
ORDER BY id <> '', direction = 'expense', name, id`

// Report computes the Fund report on demand for any member of the school; it writes nothing.
func (s *Store) Report(ctx context.Context, sess auth.Session, from, to string) (out Report, err error) {
	if err := ValidateRange(from, to); err != nil {
		return Report{}, err
	}
	err = s.withMember(ctx, sess, false, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, reportTotalsQuery, sess.TenantID, from, to)
		if e != nil {
			return e
		}
		all, e := pgx.CollectRows(rows, pgx.RowToStructByPos[CategoryTotal])
		if e != nil {
			return e
		}
		out.OpeningCents, out.ClosingCents = all[0].TotalCents, all[0].TotalCents
		out.Totals = all[1:]
		for _, c := range out.Totals {
			out.ClosingCents += c.TotalCents
		}
		rows, e = tx.Query(ctx, `SELECT `+entryColumns+` WHERE e.tenant_id=$1 AND e.entry_date BETWEEN $2::date AND $3::date ORDER BY e.entry_date,e.recorded_at,e.id`, sess.TenantID, from, to)
		if e != nil {
			return e
		}
		out.Entries, e = pgx.CollectRows(rows, pgx.RowToStructByPos[Entry])
		return e
	})
	if err != nil {
		return Report{}, err
	}
	out.From, out.To = from, to
	return out, nil
}
