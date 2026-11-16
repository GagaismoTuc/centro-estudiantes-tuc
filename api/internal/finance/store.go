// Package finance implements a tenant's append-only fund ledger.
package finance

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrForbidden       = errors.New("finance: forbidden")
	ErrNotFound        = errors.New("finance: not found")
	ErrAmount          = errors.New("finance: invalid amount")
	ErrDate            = errors.New("finance: invalid date")
	ErrFutureDate      = errors.New("finance: future date")
	ErrDescription     = errors.New("finance: description required")
	ErrDescriptionLong = errors.New("finance: description too long")
	ErrCategory        = errors.New("finance: invalid category")
	ErrReserved        = errors.New("finance: reserved category")
)
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var argentina = time.FixedZone("Argentina", -3*60*60)

type Category struct {
	ID, Direction, Name string
	Active, Reserved    bool
}
type Entry struct {
	ID, CategoryID, Direction, Date, Description, RecordedBy string
	AmountCents                                              int64
	RecordedAt                                               time.Time
}
type NewEntry struct {
	CategoryID, Direction, Date, Description string
	AmountCents                              int64
}
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewStore(pool *pgxpool.Pool, now func() time.Time) *Store {
	if now == nil {
		panic("finance: clock required")
	}
	return &Store{pool, now}
}

// withMember rechecks the active tenant membership on every operation.
func (s *Store) withMember(ctx context.Context, sess auth.Session, officer bool, fn func(pgx.Tx) error) error {
	if sess.TenantID == "" || sess.UserID == "" {
		return ErrForbidden
	}
	return db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		var member, isOfficer bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships WHERE tenant_id=$1 AND user_id=$2),
   EXISTS(SELECT 1 FROM memberships m WHERE m.tenant_id=$1 AND m.user_id=$2 AND (m.role='officer' OR EXISTS
     (SELECT 1 FROM officer_positions p WHERE p.tenant_id=m.tenant_id AND p.roster_member_id=m.roster_member_id)))`, sess.TenantID, sess.UserID).Scan(&member, &isOfficer)
		if err != nil {
			return err
		}
		if !member || (officer && !isOfficer) {
			return ErrForbidden
		}
		if officer {
			if err := auth.RequireMFA(sess); err != nil {
				return err
			}
			var hasRosterName bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM memberships m JOIN roster_members r ON r.tenant_id=m.tenant_id AND r.id=m.roster_member_id WHERE m.tenant_id=$1 AND m.user_id=$2)`, sess.TenantID, sess.UserID).Scan(&hasRosterName); err != nil {
				return err
			}
			if !hasRosterName {
				return ErrForbidden
			}
		}
		return fn(tx)
	})
}
func (s *Store) Authorize(ctx context.Context, sess auth.Session) error {
	return s.withMember(ctx, sess, true, func(pgx.Tx) error { return nil })
}
func (s *Store) Categories(ctx context.Context, sess auth.Session) (out []Category, err error) {
	out = []Category{}
	err = s.withMember(ctx, sess, false, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id::text,direction,name,active,reserved FROM finance_categories WHERE tenant_id=$1 ORDER BY direction,name,id`, sess.TenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Category])
		return err
	})
	return
}
func (s *Store) Create(ctx context.Context, sess auth.Session, in NewEntry) (out Entry, err error) {
	err = s.withMember(ctx, sess, true, func(tx pgx.Tx) error {
		if in.AmountCents <= 0 {
			return ErrAmount
		}
		date, e := time.Parse("2006-01-02", in.Date)
		if e != nil || date.Format("2006-01-02") != in.Date {
			return ErrDate
		}
		if in.Date > s.now().In(argentina).Format("2006-01-02") {
			return ErrFutureDate
		}
		in.Description = strings.TrimSpace(in.Description)
		if in.Description == "" {
			return ErrDescription
		}
		if utf8.RuneCountInString(in.Description) > 500 {
			return ErrDescriptionLong
		}
		if !uuidPattern.MatchString(in.CategoryID) {
			return ErrNotFound
		}
		var direction string
		var active, reserved bool
		e = tx.QueryRow(ctx, `SELECT direction,active,reserved FROM finance_categories WHERE tenant_id=$1 AND id=$2`, sess.TenantID, in.CategoryID).Scan(&direction, &active, &reserved)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if !active || direction != in.Direction {
			return ErrCategory
		}
		if reserved {
			return ErrReserved
		}
		var name string
		e = tx.QueryRow(ctx, `SELECT rm.full_name FROM memberships m JOIN roster_members rm ON rm.tenant_id=m.tenant_id AND rm.id=m.roster_member_id WHERE m.tenant_id=$1 AND m.user_id=$2 LIMIT 1`, sess.TenantID, sess.UserID).Scan(&name)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrForbidden
		}
		if e != nil {
			return e
		}
		recordedAt := s.now()
		e = tx.QueryRow(ctx, `INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name,recorded_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id::text, recorded_at`, sess.TenantID, in.CategoryID, in.Direction, date, in.AmountCents, in.Description, sess.UserID, name, recordedAt).Scan(&out.ID, &out.RecordedAt)
		if e != nil {
			return e
		}
		out.CategoryID = in.CategoryID
		out.Direction = in.Direction
		out.Date = in.Date
		out.AmountCents = in.AmountCents
		out.Description = in.Description
		out.RecordedBy = name
		return nil
	})
	return
}
func (s *Store) List(ctx context.Context, sess auth.Session) (out []Entry, err error) {
	out = []Entry{}
	err = s.withMember(ctx, sess, false, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT id::text,category_id::text,direction,to_char(entry_date,'YYYY-MM-DD'),description,recorded_by_name,amount_cents,recorded_at FROM finance_entries WHERE tenant_id=$1 ORDER BY recorded_at DESC,id DESC`, sess.TenantID)
		if e != nil {
			return e
		}
		defer rows.Close()
		out, e = pgx.CollectRows(rows, pgx.RowToStructByPos[Entry])
		return e
	})
	return
}
func (s *Store) Get(ctx context.Context, sess auth.Session, id string) (out Entry, err error) {
	err = s.withMember(ctx, sess, false, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(id) {
			return ErrNotFound
		}
		e := tx.QueryRow(ctx, `SELECT id::text,category_id::text,direction,to_char(entry_date,'YYYY-MM-DD'),description,recorded_by_name,amount_cents,recorded_at FROM finance_entries WHERE tenant_id=$1 AND id=$2`, sess.TenantID, id).Scan(&out.ID, &out.CategoryID, &out.Direction, &out.Date, &out.Description, &out.RecordedBy, &out.AmountCents, &out.RecordedAt)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return e
	})
	return
}
func (s *Store) Balance(ctx context.Context, sess auth.Session) (out string, err error) {
	err = s.withMember(ctx, sess, false, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COALESCE(sum(CASE direction WHEN 'income' THEN amount_cents ELSE -amount_cents END),0)::text FROM finance_entries WHERE tenant_id=$1`, sess.TenantID).Scan(&out)
	})
	return
}
