// Package budgets stores event budgets for a school.
package budgets

import (
	"context"
	"errors"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrForbidden    = errors.New("budgets: forbidden")
	ErrNameRequired = errors.New("budgets: name required")
	ErrInvalidRange = errors.New("budgets: invalid range")
)

type Budget struct {
	ID, Name, StartsOn, EndsOn string
	Archived                   bool
}
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) asOfficer(ctx context.Context, sess auth.Session, fn func(pgx.Tx) error) error {
	if sess.TenantID == "" {
		return ErrForbidden
	}
	return s.withRole(ctx, sess, false, fn)
}

func (s *Store) asReader(ctx context.Context, sess auth.Session, fn func(pgx.Tx) error) error {
	return s.withRole(ctx, sess, true, fn)
}
func (s *Store) withRole(ctx context.Context, sess auth.Session, read bool, fn func(pgx.Tx) error) error {
	if sess.TenantID == "" {
		return ErrForbidden
	}
	return db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		var allowed bool
		err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM memberships m WHERE m.tenant_id=$1 AND m.user_id=$2 AND (m.role='officer' OR ($3 AND m.role IN ('school_admin','veedor')) OR EXISTS (SELECT 1 FROM officer_positions p WHERE p.tenant_id=m.tenant_id AND p.roster_member_id=m.roster_member_id)))`, sess.TenantID, sess.UserID, read).Scan(&allowed)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrForbidden
		}
		if err := auth.RequireMFA(sess); err != nil {
			return err
		}
		return fn(tx)
	})
}
func (s *Store) AuthorizeWrite(ctx context.Context, sess auth.Session) error {
	return s.asOfficer(ctx, sess, func(pgx.Tx) error { return nil })
}
func (s *Store) Create(ctx context.Context, sess auth.Session, name, start, end string) (Budget, error) {
	var result Budget
	err := s.asOfficer(ctx, sess, func(tx pgx.Tx) error {
		name = strings.TrimSpace(name)
		if err := validateBudget(name, start, end); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `INSERT INTO event_budgets (tenant_id,name,starts_on,ends_on) VALUES ($1,$2,$3,$4) RETURNING id::text,name,starts_on::text,ends_on::text,archived_at IS NOT NULL`, sess.TenantID, name, start, end).Scan(&result.ID, &result.Name, &result.StartsOn, &result.EndsOn, &result.Archived)
		return err
	})
	return result, err
}

var (
	ErrNotFound   = errors.New("budgets: not found")
	ErrArchived   = errors.New("budgets: archived")
	ErrHasEntries = errors.New("budgets: has linked entries")
	ErrQuantity   = errors.New("budgets: invalid quantity")
	ErrAmount     = errors.New("budgets: invalid amount")
	ErrDay        = errors.New("budgets: invalid day")
	ErrKind       = errors.New("budgets: invalid kind")
)

type LineInput struct {
	Kind, Description                           string
	HeadingID, ContactID, ReferencePriceID, Day *string
	Quantity, UnitAmountCents                   *int64
}
type Line struct {
	ID string
	LineInput
}

func (s *Store) AddLine(ctx context.Context, sess auth.Session, budgetID string, in LineInput) (Line, error) {
	return s.saveLine(ctx, sess, budgetID, "", in)
}
func (s *Store) UpdateLine(ctx context.Context, sess auth.Session, budgetID, lineID string, in LineInput) (Line, error) {
	return s.saveLine(ctx, sess, budgetID, lineID, in)
}
func (s *Store) saveLine(ctx context.Context, sess auth.Session, budgetID, lineID string, in LineInput) (Line, error) {
	var out Line
	err := s.asOfficer(ctx, sess, func(tx pgx.Tx) error {
		if !validUUID(budgetID) {
			return ErrNotFound
		}
		var start, end string
		var archived bool
		err := tx.QueryRow(ctx, `SELECT starts_on::text,ends_on::text,archived_at IS NOT NULL FROM event_budgets WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, sess.TenantID, budgetID).Scan(&start, &end, &archived)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if archived {
			return ErrArchived
		}
		switch in.Kind {
		case "cost", "ticket", "foodtruck", "sponsor", "due":
		default:
			return ErrKind
		}
		if in.Kind != "ticket" && (in.Quantity == nil || *in.Quantity <= 0) {
			return ErrQuantity
		}
		if in.Kind == "due" && (in.Quantity == nil || *in.Quantity != 1) {
			return ErrQuantity
		}
		if in.Kind == "ticket" && in.Quantity != nil && *in.Quantity < 0 {
			return ErrQuantity
		}
		if in.Day != nil {
			d, err := time.Parse("2006-01-02", *in.Day)
			if err != nil || d.Format("2006-01-02") < start || d.Format("2006-01-02") > end {
				return ErrDay
			}
		}
		for _, v := range []*string{in.HeadingID, in.ContactID, in.ReferencePriceID} {
			if v != nil && !validUUID(*v) {
				return ErrNotFound
			}
		}
		if in.Kind == "cost" && in.HeadingID == nil {
			return ErrNotFound
		}
		if in.HeadingID != nil {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contact_headings WHERE tenant_id=$1 AND id=$2)`, sess.TenantID, *in.HeadingID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrNotFound
			}
		}
		if in.ContactID != nil {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contacts WHERE tenant_id=$1 AND id=$2)`, sess.TenantID, *in.ContactID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrNotFound
			}
		}
		if in.ReferencePriceID != nil {
			if in.ContactID == nil {
				return ErrNotFound
			}
			var amount int64
			err := tx.QueryRow(ctx, `SELECT amount_cents FROM contact_reference_prices WHERE tenant_id=$1 AND contact_id=$2 AND id=$3`, sess.TenantID, *in.ContactID, *in.ReferencePriceID).Scan(&amount)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			if in.UnitAmountCents == nil {
				in.UnitAmountCents = &amount
			}
		}
		if (in.UnitAmountCents == nil && in.Kind != "ticket") || (in.UnitAmountCents != nil && *in.UnitAmountCents <= 0) {
			return ErrAmount
		}
		if lineID == "" {
			err = tx.QueryRow(ctx, `INSERT INTO event_budget_lines (tenant_id,budget_id,heading_id,contact_id,kind,description,quantity,unit_amount_cents,day) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id::text`, sess.TenantID, budgetID, in.HeadingID, in.ContactID, in.Kind, in.Description, in.Quantity, in.UnitAmountCents, in.Day).Scan(&out.ID)
		} else {
			if !validUUID(lineID) {
				return ErrNotFound
			}
			err = tx.QueryRow(ctx, `UPDATE event_budget_lines SET heading_id=$3,contact_id=$4,kind=$5,description=$6,quantity=$7,unit_amount_cents=$8,day=$9 WHERE tenant_id=$1 AND budget_id=$2 AND id=$10 RETURNING id::text`, sess.TenantID, budgetID, in.HeadingID, in.ContactID, in.Kind, in.Description, in.Quantity, in.UnitAmountCents, in.Day, lineID).Scan(&out.ID)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
		}
		out.LineInput = in
		return err
	})
	return out, err
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validUUID(s string) bool { return uuidPattern.MatchString(s) }

type Totals struct {
	Cost, Income, Result int64
	BreakEven            *int64
}
type DayTotals struct {
	Day    string
	Totals Totals
}
type Detail struct {
	Budget
	Lines  []Line
	Totals Totals
	Days   []DayTotals
}

type accumulator struct{ cost, income, other, tickets big.Int }

func (a *accumulator) add(l Line) {
	if l.Quantity == nil {
		return
	}
	if l.Kind == "ticket" {
		a.tickets.Add(&a.tickets, big.NewInt(*l.Quantity))
	}
	if l.UnitAmountCents == nil {
		return
	}
	v := new(big.Int).Mul(big.NewInt(*l.Quantity), big.NewInt(*l.UnitAmountCents))
	if l.Kind == "cost" {
		a.cost.Add(&a.cost, v)
	} else {
		a.income.Add(&a.income, v)
		if l.Kind != "ticket" {
			a.other.Add(&a.other, v)
		}
	}
}
func (a *accumulator) totals() (Totals, error) {
	var out Totals
	delta := new(big.Int).Sub(&a.income, &a.cost)
	if !a.cost.IsInt64() || !a.income.IsInt64() || !delta.IsInt64() {
		return out, errors.New("budget totals overflow")
	}
	out.Cost = a.cost.Int64()
	out.Income = a.income.Int64()
	out.Result = delta.Int64()
	if a.tickets.Sign() > 0 {
		amount := new(big.Int).Sub(&a.cost, &a.other)
		if amount.Sign() < 0 {
			amount.SetInt64(0)
		}
		unit := new(big.Int).Mul(&a.tickets, big.NewInt(10000))
		amount.Add(amount, new(big.Int).Sub(unit, big.NewInt(1)))
		amount.Div(amount, unit)
		amount.Mul(amount, big.NewInt(10000))
		if !amount.IsInt64() {
			return out, errors.New("budget price overflow")
		}
		v := amount.Int64()
		out.BreakEven = &v
	}
	return out, nil
}
func (s *Store) Get(ctx context.Context, sess auth.Session, id string) (Detail, error) {
	var out Detail
	err := s.asReader(ctx, sess, func(tx pgx.Tx) error {
		if !validUUID(id) {
			return ErrNotFound
		}
		err := tx.QueryRow(ctx, `SELECT id::text,name,starts_on::text,ends_on::text,archived_at IS NOT NULL FROM event_budgets WHERE tenant_id=$1 AND id=$2 FOR SHARE`, sess.TenantID, id).Scan(&out.ID, &out.Name, &out.StartsOn, &out.EndsOn, &out.Archived)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id::text,kind,description,heading_id::text,contact_id::text,quantity,unit_amount_cents,day::text FROM event_budget_lines WHERE tenant_id=$1 AND budget_id=$2 ORDER BY created_at,id`, sess.TenantID, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		out.Lines = []Line{}
		var all accumulator
		byDay := map[string]*accumulator{}
		for rows.Next() {
			var l Line
			if err := rows.Scan(&l.ID, &l.Kind, &l.Description, &l.HeadingID, &l.ContactID, &l.Quantity, &l.UnitAmountCents, &l.Day); err != nil {
				return err
			}
			out.Lines = append(out.Lines, l)
			all.add(l)
			if l.Day != nil {
				if byDay[*l.Day] == nil {
					byDay[*l.Day] = &accumulator{}
				}
				byDay[*l.Day].add(l)
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		out.Totals, err = all.totals()
		if err != nil {
			return err
		}
		a, _ := time.Parse("2006-01-02", out.StartsOn)
		b, _ := time.Parse("2006-01-02", out.EndsOn)
		out.Days = []DayTotals{}
		for d := a; !d.After(b); d = d.AddDate(0, 0, 1) {
			day := d.Format("2006-01-02")
			v := byDay[day]
			if v == nil {
				v = &accumulator{}
			}
			totals, err := v.totals()
			if err != nil {
				return err
			}
			out.Days = append(out.Days, DayTotals{Day: day, Totals: totals})
		}
		return nil
	})
	return out, err
}

func (s *Store) Delete(ctx context.Context, sess auth.Session, id string) error {
	return s.asOfficer(ctx, sess, func(tx pgx.Tx) error {
		if !validUUID(id) {
			return ErrNotFound
		}
		var exists int
		// FOR UPDATE serializes with finance.Store.Create (FOR SHARE) on event_budgets.
		err := tx.QueryRow(ctx, `SELECT 1 FROM event_budgets WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, sess.TenantID, id).Scan(&exists)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var hasEntries bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM finance_entries WHERE tenant_id=$1 AND event_budget_id=$2)`, sess.TenantID, id).Scan(&hasEntries); err != nil {
			return err
		}
		if hasEntries {
			return ErrHasEntries
		}
		tag, err := tx.Exec(ctx, `DELETE FROM event_budgets WHERE tenant_id=$1 AND id=$2`, sess.TenantID, id)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "23001" || pgErr.Code == "23503") {
			return ErrHasEntries
		}
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}
func (s *Store) Archive(ctx context.Context, sess auth.Session, id string) (Budget, error) {
	var b Budget
	err := s.asOfficer(ctx, sess, func(tx pgx.Tx) error {
		if !validUUID(id) {
			return ErrNotFound
		}
		err := tx.QueryRow(ctx, `UPDATE event_budgets SET archived_at=coalesce(archived_at,now()) WHERE tenant_id=$1 AND id=$2 RETURNING id::text,name,starts_on::text,ends_on::text,archived_at IS NOT NULL`, sess.TenantID, id).Scan(&b.ID, &b.Name, &b.StartsOn, &b.EndsOn, &b.Archived)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return b, err
}

func (s *Store) List(ctx context.Context, sess auth.Session) ([]Budget, error) {
	out := []Budget{}
	err := s.asReader(ctx, sess, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id::text,name,starts_on::text,ends_on::text,archived_at IS NOT NULL FROM event_budgets WHERE tenant_id=$1 ORDER BY starts_on DESC,id`, sess.TenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var b Budget
			if err := rows.Scan(&b.ID, &b.Name, &b.StartsOn, &b.EndsOn, &b.Archived); err != nil {
				return err
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	return out, err
}

func validateBudget(name, start, end string) error {
	if strings.TrimSpace(name) == "" {
		return ErrNameRequired
	}
	a, e1 := time.Parse("2006-01-02", start)
	b, e2 := time.Parse("2006-01-02", end)
	if e1 != nil || e2 != nil || b.Before(a) || b.Sub(a) > 30*24*time.Hour {
		return ErrInvalidRange
	}
	return nil
}
func (s *Store) Update(ctx context.Context, sess auth.Session, id, name, start, end string) (Budget, error) {
	var out Budget
	err := s.asOfficer(ctx, sess, func(tx pgx.Tx) error {
		if !validUUID(id) {
			return ErrNotFound
		}
		var archived bool
		err := tx.QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM event_budgets WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, sess.TenantID, id).Scan(&archived)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if archived {
			return ErrArchived
		}
		name = strings.TrimSpace(name)
		if err := validateBudget(name, start, end); err != nil {
			return err
		}
		var outside bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM event_budget_lines WHERE tenant_id=$1 AND budget_id=$2 AND (day<$3::date OR day>$4::date))`, sess.TenantID, id, start, end).Scan(&outside); err != nil {
			return err
		}
		if outside {
			return ErrDay
		}
		return tx.QueryRow(ctx, `UPDATE event_budgets SET name=$3,starts_on=$4,ends_on=$5 WHERE tenant_id=$1 AND id=$2 RETURNING id::text,name,starts_on::text,ends_on::text,archived_at IS NOT NULL`, sess.TenantID, id, name, start, end).Scan(&out.ID, &out.Name, &out.StartsOn, &out.EndsOn, &out.Archived)
	})
	return out, err
}

func (s *Store) DeleteLine(ctx context.Context, sess auth.Session, id, lineID string) error {
	return s.asOfficer(ctx, sess, func(tx pgx.Tx) error {
		if !validUUID(id) || !validUUID(lineID) {
			return ErrNotFound
		}
		var archived bool
		err := tx.QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM event_budgets WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, sess.TenantID, id).Scan(&archived)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if archived {
			return ErrArchived
		}
		tag, err := tx.Exec(ctx, `DELETE FROM event_budget_lines WHERE tenant_id=$1 AND budget_id=$2 AND id=$3`, sess.TenantID, id, lineID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}
