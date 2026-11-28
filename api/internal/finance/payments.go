package finance

import (
	"context"
	"errors"
	"fmt"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/jackc/pgx/v5"
)

var (
	ErrOverpayment = errors.New("finance: payment exceeds what is owed")
	ErrExempt      = errors.New("finance: roster member is exempt from the due")
	ErrHasPayments = errors.New("finance: roster member has payments for the due")
)

// paymentDescriptionPrefix starts the description of a payment's Entry; the Due's name follows.
const paymentDescriptionPrefix = "Pago de cuota: "

// NewDuePayment is a full or partial payment of a Due by one Roster member of its snapshot.
type NewDuePayment struct {
	RosterMemberID, Date string
	AmountCents          int64
}

// DuePayment is a recorded payment and the id of its income Entry in "Cuotas".
type DuePayment struct {
	ID, DueID, RosterMemberID, EntryID, Date string
	AmountCents                              int64
}

// paidSQL is the SQL expression for what a Roster member paid of a Due: their payments whose Entry
// has no Reversal. due and member are SQL expressions of the outer query; $1 is the tenant.
func paidSQL(due, member string) string { return fmt.Sprintf(paidSQLFormat, due, member) }

const paidSQLFormat = `COALESCE((SELECT sum(p.amount_cents) FROM finance_due_payments p
	WHERE p.tenant_id = $1 AND p.due_id = %s AND p.roster_member_id = %s
	  AND NOT EXISTS (SELECT 1 FROM finance_entries r WHERE r.tenant_id = p.tenant_id AND r.reverses_id = p.entry_id)), 0)::bigint`

// RecordDuePayment writes, in one transaction, a payment and its income Entry in the reserved
// category "Cuotas". The member's snapshot row is locked FOR UPDATE so concurrent payments for one
// member serialize and together never exceed the Due's amount; the Due row is locked FOR SHARE so a
// concurrent edit or delete waits and then finds the Due frozen.
func (s *Store) RecordDuePayment(ctx context.Context, sess auth.Session, dueID string, in NewDuePayment) (out DuePayment, err error) {
	err = s.withMember(ctx, sess, true, func(tx pgx.Tx) error {
		if in.AmountCents <= 0 {
			return ErrAmount
		}
		date, e := parseDueDate(in.Date)
		if e != nil {
			return e
		}
		if in.Date > s.now().In(argentina).Format("2006-01-02") {
			return ErrFutureDate
		}
		if !uuidPattern.MatchString(dueID) || !uuidPattern.MatchString(in.RosterMemberID) {
			return ErrNotFound
		}
		var dueName string
		var dueAmount int64
		e = tx.QueryRow(ctx, `SELECT name, amount_cents FROM finance_dues WHERE tenant_id=$1 AND id=$2 FOR SHARE`, sess.TenantID, dueID).Scan(&dueName, &dueAmount)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		var inRoster bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM roster_members WHERE tenant_id=$1 AND id=$2)`, sess.TenantID, in.RosterMemberID).Scan(&inRoster); e != nil {
			return e
		}
		if !inRoster {
			return ErrNotFound
		}
		var one int
		e = tx.QueryRow(ctx, `SELECT 1 FROM finance_due_members WHERE tenant_id=$1 AND due_id=$2 AND roster_member_id=$3 FOR UPDATE`, sess.TenantID, dueID, in.RosterMemberID).Scan(&one)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrNotOwed
		}
		if e != nil {
			return e
		}
		var paid int64
		var exempt bool
		if e = tx.QueryRow(ctx, `SELECT `+paidSQL("$2", "$3")+`,
			EXISTS (SELECT 1 FROM finance_due_exemptions WHERE tenant_id=$1 AND due_id=$2 AND roster_member_id=$3)`,
			sess.TenantID, dueID, in.RosterMemberID).Scan(&paid, &exempt); e != nil {
			return e
		}
		if exempt {
			return ErrExempt
		}
		if in.AmountCents > dueAmount-paid {
			return ErrOverpayment
		}
		var category string
		if e = tx.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND direction='income' AND name='Cuotas' AND reserved`, sess.TenantID).Scan(&category); e != nil {
			return e
		}
		name, e := officerName(ctx, tx, sess)
		if e != nil {
			return e
		}
		if e = tx.QueryRow(ctx, `INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name,recorded_at)
			VALUES ($1,$2,'income',$3,$4,$5,$6,$7,$8) RETURNING id::text`,
			sess.TenantID, category, date, in.AmountCents, paymentDescription(dueName), sess.UserID, name, s.now()).Scan(&out.EntryID); e != nil {
			return e
		}
		if e = tx.QueryRow(ctx, `INSERT INTO finance_due_payments(tenant_id,due_id,roster_member_id,entry_id,amount_cents,payment_date,recorded_by,recorded_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`,
			sess.TenantID, dueID, in.RosterMemberID, out.EntryID, in.AmountCents, date, sess.UserID, s.now()).Scan(&out.ID); e != nil {
			return e
		}
		out.DueID, out.RosterMemberID, out.Date, out.AmountCents = dueID, in.RosterMemberID, in.Date, in.AmountCents
		return nil
	})
	if err != nil {
		return DuePayment{}, err
	}
	return
}

// paymentDescription is "Pago de cuota: " plus the Due's name, cut to the Entry's 500 characters.
func paymentDescription(dueName string) string {
	runes := []rune(dueName)
	if limit := 500 - len([]rune(paymentDescriptionPrefix)); len(runes) > limit {
		runes = runes[:limit]
	}
	return paymentDescriptionPrefix + string(runes)
}

func dueActivitySQL(tenant, due string) string {
	return fmt.Sprintf(`EXISTS (SELECT 1 FROM finance_due_payments WHERE tenant_id=%s AND due_id=%s)
		OR EXISTS (SELECT 1 FROM finance_due_exemptions WHERE tenant_id=%s AND due_id=%s)`, tenant, due, tenant, due)
}

func hasDueActivity(ctx context.Context, tx pgx.Tx, tenantID, dueID string) (bool, error) {
	var frozen bool
	err := tx.QueryRow(ctx, `SELECT `+dueActivitySQL("$1", "$2"), tenantID, dueID).Scan(&frozen)
	return frozen, err
}

// dueStatus is "exempt" for an exempt member, "paid" once their payments cover the Due's amount,
// else "owing".
func dueStatus(amountCents, paid int64, exempt bool) string {
	switch {
	case exempt:
		return DueStatusExempt
	case paid >= amountCents:
		return DueStatusPaid
	}
	return DueStatusOwing
}
