package finance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/jackc/pgx/v5"
)

// ErrTermRequired is a Financial accountability report without a Term (Gestión).
var ErrTermRequired = errors.New("finance: term required")

// FrozenEntry is an Entry as a Financial accountability report keeps it: with its Category's name at
// publication and never the Roster member who paid (ADR 0004).
type FrozenEntry struct {
	ID          string `json:"id"`
	Date        string `json:"date"`
	Direction   string `json:"direction"`
	CategoryID  string `json:"categoryId"`
	Category    string `json:"category"`
	Description string `json:"description"`
	AmountCents int64  `json:"amountCents"`
	RecordedBy  string `json:"recordedBy"`
	ReversesID  string `json:"reversesId,omitempty"`
	// Reversed tells whether the Entry had a Reversal when the report was published.
	Reversed bool `json:"reversed"`
}

// FrozenTotal is a CategoryTotal as published.
type FrozenTotal struct {
	CategoryID string `json:"categoryId"`
	Direction  string `json:"direction"`
	Name       string `json:"name"`
	TotalCents int64  `json:"totalCents"`
}

// DueTotal is how many Roster members paid a Due out of those who owe it (Exemptions excluded),
// as of publication: "312 de 470 pagaron". It never names anyone.
type DueTotal struct {
	DueID       string `json:"dueId"`
	Name        string `json:"name"`
	DueDate     string `json:"dueDate"`
	AmountCents int64  `json:"amountCents"`
	PaidCount   int    `json:"paidCount"`
	TotalCount  int    `json:"totalCount"`
}

// AccountabilityCopy is the frozen content of a Financial accountability report, stored as JSON.
type AccountabilityCopy struct {
	OpeningCents int64         `json:"openingCents"`
	ClosingCents int64         `json:"closingCents"`
	Entries      []FrozenEntry `json:"entries"`
	Totals       []FrozenTotal `json:"totals"`
	Dues         []DueTotal    `json:"dues"`
}

// AccountabilityReport is a published Financial accountability report (Rendición de cuentas). The
// list leaves Copy empty.
type AccountabilityReport struct {
	ID, From, To, Term, PublishedBy string
	PublishedAt                     time.Time
	Copy                            AccountabilityCopy
}

// NewAccountabilityReport is what an Officer asks to publish.
type NewAccountabilityReport struct {
	From, To, Term string
}

// accountabilityDuesQuery counts, per Due whose due date falls in the period, who paid it in full
// and who owes it (Exemptions excluded), with paidSQL like the Due list.
var accountabilityDuesQuery = `
	SELECT d.id::text, d.name, to_char(d.due_date, 'YYYY-MM-DD'), d.amount_cents,
	       COUNT(dm.roster_member_id) FILTER (WHERE e.roster_member_id IS NULL AND ` + paidSQL("dm.due_id", "dm.roster_member_id") + ` >= d.amount_cents)::int,
	       COUNT(dm.roster_member_id) FILTER (WHERE e.roster_member_id IS NULL)::int
	FROM finance_dues d
	LEFT JOIN finance_due_members dm ON dm.tenant_id = d.tenant_id AND dm.due_id = d.id
	LEFT JOIN finance_due_exemptions e
		ON e.tenant_id = dm.tenant_id AND e.due_id = dm.due_id AND e.roster_member_id = dm.roster_member_id
	WHERE d.tenant_id = $1 AND d.due_date BETWEEN $2::date AND $3::date
	GROUP BY d.id, d.name, d.due_date, d.amount_cents
	ORDER BY d.due_date, d.name, d.id`

// PublishAccountabilityReport freezes and stores the Fund report of a period for a Term. Any Officer
// publishes any period, also one of the previous Term. The copy is read and written in one
// REPEATABLE READ transaction, so an Entry or payment committed meanwhile is wholly in or out.
func (s *Store) PublishAccountabilityReport(ctx context.Context, sess auth.Session, in NewAccountabilityReport) (out AccountabilityReport, err error) {
	opts := pgx.TxOptions{IsoLevel: pgx.RepeatableRead}
	err = s.withMemberOptions(ctx, sess, true, opts, func(tx pgx.Tx) error {
		if e := ValidateRange(in.From, in.To); e != nil {
			return e
		}
		now := s.now()
		if in.To > now.In(argentina).Format("2006-01-02") {
			return ErrFutureDate
		}
		term := strings.TrimSpace(in.Term)
		if term == "" {
			return ErrTermRequired
		}
		rep, e := buildReport(ctx, tx, sess.TenantID, in.From, in.To)
		if e != nil {
			return e
		}
		if s.afterReportRead != nil {
			s.afterReportRead()
		}
		out.Copy = freeze(rep)
		rows, e := tx.Query(ctx, accountabilityDuesQuery, sess.TenantID, in.From, in.To)
		if e != nil {
			return e
		}
		if out.Copy.Dues, e = pgx.CollectRows(rows, pgx.RowToStructByPos[DueTotal]); e != nil {
			return e
		}
		name, e := officerName(ctx, tx, sess)
		if e != nil {
			return e
		}
		copyJSON, e := json.Marshal(out.Copy)
		if e != nil {
			return e
		}
		out.From, out.To, out.Term, out.PublishedBy = in.From, in.To, term, name
		return tx.QueryRow(ctx, `INSERT INTO finance_accountability_reports(tenant_id,period_from,period_to,term,published_by,published_by_name,published_at,report)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text, published_at`,
			sess.TenantID, in.From, in.To, term, sess.UserID, name, now, copyJSON).Scan(&out.ID, &out.PublishedAt)
	})
	if err != nil {
		return AccountabilityReport{}, err
	}
	return out, nil
}

// freeze keeps what a published report shows of a Fund report: no payer names.
func freeze(rep Report) AccountabilityCopy {
	out := AccountabilityCopy{OpeningCents: rep.OpeningCents, ClosingCents: rep.ClosingCents,
		Entries: make([]FrozenEntry, 0, len(rep.Entries)), Totals: make([]FrozenTotal, 0, len(rep.Totals)), Dues: []DueTotal{}}
	names := make(map[string]string, len(rep.Totals))
	for _, c := range rep.Totals {
		names[c.CategoryID] = c.Name
		out.Totals = append(out.Totals, FrozenTotal{CategoryID: c.CategoryID, Direction: c.Direction, Name: c.Name, TotalCents: c.TotalCents})
	}
	// Every Entry in the range has its Category among the Totals.
	for _, e := range rep.Entries {
		out.Entries = append(out.Entries, FrozenEntry{ID: e.ID, Date: e.Date, Direction: e.Direction, CategoryID: e.CategoryID, Category: names[e.CategoryID],
			Description: e.Description, AmountCents: e.AmountCents, RecordedBy: e.RecordedBy, ReversesID: e.ReversesID, Reversed: e.ReversalID != ""})
	}
	return out
}

// AccountabilityReports lists the school's published reports, newest first, for any member.
func (s *Store) AccountabilityReports(ctx context.Context, sess auth.Session) (out []AccountabilityReport, err error) {
	out = []AccountabilityReport{}
	err = s.withMember(ctx, sess, false, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT id::text, to_char(period_from,'YYYY-MM-DD'), to_char(period_to,'YYYY-MM-DD'), term, published_by_name, published_at
			FROM finance_accountability_reports WHERE tenant_id=$1 ORDER BY published_at DESC, id DESC`, sess.TenantID)
		if e != nil {
			return e
		}
		var r AccountabilityReport
		_, e = pgx.ForEachRow(rows, []any{&r.ID, &r.From, &r.To, &r.Term, &r.PublishedBy, &r.PublishedAt}, func() error {
			out = append(out, r)
			return nil
		})
		return e
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// AccountabilityReport reads one published report with its frozen copy, for any member.
func (s *Store) AccountabilityReport(ctx context.Context, sess auth.Session, id string) (out AccountabilityReport, err error) {
	err = s.withMember(ctx, sess, false, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(id) {
			return ErrNotFound
		}
		var copyJSON []byte
		e := tx.QueryRow(ctx, `SELECT id::text, to_char(period_from,'YYYY-MM-DD'), to_char(period_to,'YYYY-MM-DD'), term, published_by_name, published_at, report
			FROM finance_accountability_reports WHERE tenant_id=$1 AND id=$2`, sess.TenantID, id).
			Scan(&out.ID, &out.From, &out.To, &out.Term, &out.PublishedBy, &out.PublishedAt, &copyJSON)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		return json.Unmarshal(copyJSON, &out.Copy)
	})
	if err != nil {
		return AccountabilityReport{}, err
	}
	return out, nil
}
