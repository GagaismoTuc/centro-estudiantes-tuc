package finance

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/jackc/pgx/v5"
)

var (
	ErrDueName     = errors.New("finance: due name required")
	ErrEmptyRoster = errors.New("finance: empty roster")
	ErrDueFrozen   = errors.New("finance: due frozen")
)

const (
	DueStatusOwing  = "owing"
	DueStatusPaid   = "paid"
	DueStatusExempt = "exempt"
)

// Due summarizes a Student center fee obligation for Board, School admin, and Veedor views.
type Due struct {
	ID          string
	Name        string
	AmountCents int64
	DueDate     string
	CreatedAt   time.Time
	OwingCount  int
	TotalCount  int
}

// DueMember holds one Roster member's payment status for a Due without exposing document numbers.
type DueMember struct {
	RosterMemberID string
	FullName       string
	Status         string
	PaidCents      int64
}

// DueDetail pairs a Due summary with its fixed snapshot of Roster members.
type DueDetail struct {
	Due
	Members []DueMember
}

// MyDue is a single Roster member's view of one Due they owe.
type MyDue struct {
	ID          string
	Name        string
	AmountCents int64
	DueDate     string
	Status      string
	PaidCents   int64
}

// NewDue is the input to create a Due and snapshot the current Roster.
type NewDue struct {
	Name        string
	AmountCents int64
	DueDate     string
}

// UpdateDue holds optional fields for patching an existing Due.
type UpdateDue struct {
	Name        *string
	AmountCents *int64
	DueDate     *string
}

func (s *Store) withDueRole(ctx context.Context, sess auth.Session, allowReaders bool, fn func(pgx.Tx) error) error {
	if sess.TenantID == "" || sess.UserID == "" {
		return ErrForbidden
	}
	return db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		roles := []string{"officer"}
		if allowReaders {
			roles = dueReaderRoles
		}
		allowed, err := hasDueRole(ctx, tx, sess, roles)
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

// dueReaderRoles see the full per-student list of a Due and who paid (ADR 0004).
var dueReaderRoles = []string{"school_admin", "officer", "veedor"}

// hasDueRole reports whether the session holds one of roles, or an officer position, in its tenant.
func hasDueRole(ctx context.Context, tx pgx.Tx, sess auth.Session, roles []string) (bool, error) {
	var allowed bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM memberships m
			WHERE m.tenant_id = $1 AND m.user_id = $2
			  AND (
				  m.role = ANY($3::text[])
				  OR EXISTS (
					  SELECT 1 FROM officer_positions p
					  WHERE p.tenant_id = m.tenant_id AND p.roster_member_id = m.roster_member_id
				  )
			  )
		)`, sess.TenantID, sess.UserID, roles).Scan(&allowed)
	return allowed, err
}

// AuthorizeDueWrite checks that sess is an officer with verified MFA before parsing request payloads.
func (s *Store) AuthorizeDueWrite(ctx context.Context, sess auth.Session) error {
	return s.withDueRole(ctx, sess, false, func(pgx.Tx) error { return nil })
}

func parseDueDate(raw string) (time.Time, error) {
	date, err := time.Parse("2006-01-02", raw)
	if err != nil || date.Format("2006-01-02") != raw {
		return time.Time{}, ErrDate
	}
	return date, nil
}

func buildDueDetail(ctx context.Context, tx pgx.Tx, tenantID string, due Due) (DueDetail, error) {
	members, err := loadDueMembers(ctx, tx, tenantID, due.ID, due.AmountCents)
	if err != nil {
		return DueDetail{}, err
	}
	due.OwingCount = 0
	for _, m := range members {
		if m.Status == DueStatusOwing {
			due.OwingCount++
		}
	}
	due.TotalCount = len(members)
	return DueDetail{Due: due, Members: members}, nil
}

func loadDueMembers(ctx context.Context, tx pgx.Tx, tenantID, dueID string, amountCents int64) ([]DueMember, error) {
	rows, err := tx.Query(ctx, `
		SELECT dm.roster_member_id::text, rm.full_name, `+paidSQL("dm.due_id", "dm.roster_member_id")+`
		FROM finance_due_members dm
		JOIN roster_members rm ON rm.tenant_id = dm.tenant_id AND rm.id = dm.roster_member_id
		WHERE dm.tenant_id = $1 AND dm.due_id = $2
		ORDER BY rm.full_name ASC, rm.id ASC`, tenantID, dueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []DueMember{}
	for rows.Next() {
		var m DueMember
		if err := rows.Scan(&m.RosterMemberID, &m.FullName, &m.PaidCents); err != nil {
			return nil, err
		}
		m.Status = dueStatus(amountCents, m.PaidCents)
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return members, nil
}

// CreateDue creates a Due and snapshots every current Roster member in a single transaction.
func (s *Store) CreateDue(ctx context.Context, sess auth.Session, in NewDue) (out DueDetail, err error) {
	err = s.withDueRole(ctx, sess, false, func(tx pgx.Tx) error {
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return ErrDueName
		}
		if in.AmountCents <= 0 {
			return ErrAmount
		}
		dueDate, e := parseDueDate(in.DueDate)
		if e != nil {
			return e
		}
		var dueID string
		var createdAt time.Time
		if e = tx.QueryRow(ctx, `
			INSERT INTO finance_dues (tenant_id, name, amount_cents, due_date, created_by, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id::text, created_at`,
			sess.TenantID, name, in.AmountCents, dueDate, sess.UserID, s.now()).Scan(&dueID, &createdAt); e != nil {
			return e
		}
		tag, e := tx.Exec(ctx, `
			INSERT INTO finance_due_members (tenant_id, due_id, roster_member_id)
			SELECT $1, $2, id FROM roster_members WHERE tenant_id = $1`, sess.TenantID, dueID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return ErrEmptyRoster
		}
		out, e = buildDueDetail(ctx, tx, sess.TenantID, Due{
			ID:          dueID,
			Name:        name,
			AmountCents: in.AmountCents,
			DueDate:     in.DueDate,
			CreatedAt:   createdAt,
		})
		return e
	})
	return
}

// ListDues returns all Dues for the active tenant for Officers, School admins, and Veedores.
func (s *Store) ListDues(ctx context.Context, sess auth.Session) (out []Due, err error) {
	out = []Due{}
	err = s.withDueRole(ctx, sess, true, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT d.id::text, d.name, d.amount_cents, to_char(d.due_date, 'YYYY-MM-DD'), d.created_at,
			       COUNT(dm.roster_member_id) FILTER (WHERE `+paidSQL("dm.due_id", "dm.roster_member_id")+` < d.amount_cents)::int,
			       COUNT(dm.roster_member_id)::int
			FROM finance_dues d
			LEFT JOIN finance_due_members dm ON dm.tenant_id = d.tenant_id AND dm.due_id = d.id
			WHERE d.tenant_id = $1
			GROUP BY d.id, d.name, d.amount_cents, d.due_date, d.created_at
			ORDER BY d.due_date DESC, d.created_at DESC, d.id DESC`, sess.TenantID)
		if e != nil {
			return e
		}
		defer rows.Close()
		out, e = pgx.CollectRows(rows, pgx.RowToStructByPos[Due])
		return e
	})
	return
}

// GetDue returns one Due with its Roster member statuses for Officers, School admins, and Veedores.
func (s *Store) GetDue(ctx context.Context, sess auth.Session, id string) (out DueDetail, err error) {
	err = s.withDueRole(ctx, sess, true, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(id) {
			return ErrNotFound
		}
		e := tx.QueryRow(ctx, `
			SELECT id::text, name, amount_cents, to_char(due_date, 'YYYY-MM-DD'), created_at
			FROM finance_dues
			WHERE tenant_id = $1 AND id = $2`, sess.TenantID, id).
			Scan(&out.ID, &out.Name, &out.AmountCents, &out.DueDate, &out.CreatedAt)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		out, e = buildDueDetail(ctx, tx, sess.TenantID, out.Due)
		return e
	})
	return
}

// UpdateDue edits a Due's name, amount, or due date. Amount and due date lock once payments or exemptions exist.
func (s *Store) UpdateDue(ctx context.Context, sess auth.Session, id string, in UpdateDue) (out DueDetail, err error) {
	err = s.withDueRole(ctx, sess, false, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(id) {
			return ErrNotFound
		}
		if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
			return ErrDueName
		}
		if in.AmountCents != nil && *in.AmountCents <= 0 {
			return ErrAmount
		}
		if in.DueDate != nil {
			if _, e := parseDueDate(*in.DueDate); e != nil {
				return e
			}
		}

		var cur Due
		e := tx.QueryRow(ctx, `
			SELECT id::text, name, amount_cents, to_char(due_date, 'YYYY-MM-DD'), created_at
			FROM finance_dues
			WHERE tenant_id = $1 AND id = $2
			FOR UPDATE`, sess.TenantID, id).
			Scan(&cur.ID, &cur.Name, &cur.AmountCents, &cur.DueDate, &cur.CreatedAt)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}

		nextName := cur.Name
		if in.Name != nil {
			nextName = strings.TrimSpace(*in.Name)
		}
		nextAmount := cur.AmountCents
		if in.AmountCents != nil {
			nextAmount = *in.AmountCents
		}
		nextDateStr := cur.DueDate
		if in.DueDate != nil {
			nextDateStr = *in.DueDate
		}

		if nextAmount != cur.AmountCents || nextDateStr != cur.DueDate {
			frozen, e := hasDueActivity(ctx, tx, sess.TenantID, id)
			if e != nil {
				return e
			}
			if frozen {
				return ErrDueFrozen
			}
		}

		nextDate, e := parseDueDate(nextDateStr)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `
			UPDATE finance_dues SET name = $3, amount_cents = $4, due_date = $5
			WHERE tenant_id = $1 AND id = $2`,
			sess.TenantID, id, nextName, nextAmount, nextDate); e != nil {
			return e
		}
		out, e = buildDueDetail(ctx, tx, sess.TenantID, Due{
			ID:          cur.ID,
			Name:        nextName,
			AmountCents: nextAmount,
			DueDate:     nextDateStr,
			CreatedAt:   cur.CreatedAt,
		})
		return e
	})
	return
}

// DeleteDue removes a Due and its Roster snapshot when it has no Payments or Exemptions.
func (s *Store) DeleteDue(ctx context.Context, sess auth.Session, id string) error {
	return s.withDueRole(ctx, sess, false, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(id) {
			return ErrNotFound
		}
		var one int
		e := tx.QueryRow(ctx, `SELECT 1 FROM finance_dues WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, sess.TenantID, id).Scan(&one)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		frozen, e := hasDueActivity(ctx, tx, sess.TenantID, id)
		if e != nil {
			return e
		}
		if frozen {
			return ErrDueFrozen
		}
		if _, e = tx.Exec(ctx, `DELETE FROM finance_due_members WHERE tenant_id = $1 AND due_id = $2`, sess.TenantID, id); e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `DELETE FROM finance_dues WHERE tenant_id = $1 AND id = $2`, sess.TenantID, id)
		return e
	})
}

// ListMyDues returns only the Dues whose fixed Roster snapshot includes the session's Roster member.
func (s *Store) ListMyDues(ctx context.Context, sess auth.Session) (out []MyDue, err error) {
	out = []MyDue{}
	if sess.TenantID == "" || sess.UserID == "" {
		return nil, ErrForbidden
	}
	err = db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		var isMember bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM memberships WHERE tenant_id = $1 AND user_id = $2)`, sess.TenantID, sess.UserID).Scan(&isMember); e != nil {
			return e
		}
		if !isMember {
			return ErrForbidden
		}
		var rosterMemberID string
		e := tx.QueryRow(ctx, `
			SELECT roster_member_id::text
			FROM memberships
			WHERE tenant_id = $1 AND user_id = $2 AND roster_member_id IS NOT NULL
			LIMIT 1`, sess.TenantID, sess.UserID).Scan(&rosterMemberID)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		rows, e := tx.Query(ctx, `
			SELECT d.id::text, d.name, d.amount_cents, to_char(d.due_date, 'YYYY-MM-DD'), `+paidSQL("d.id", "dm.roster_member_id")+`
			FROM finance_dues d
			JOIN finance_due_members dm ON dm.tenant_id = d.tenant_id AND dm.due_id = d.id
			WHERE d.tenant_id = $1 AND dm.roster_member_id = $2
			ORDER BY d.due_date DESC, d.created_at DESC, d.id DESC`, sess.TenantID, rosterMemberID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var d MyDue
			if e := rows.Scan(&d.ID, &d.Name, &d.AmountCents, &d.DueDate, &d.PaidCents); e != nil {
				return e
			}
			d.Status = dueStatus(d.AmountCents, d.PaidCents)
			out = append(out, d)
		}
		return rows.Err()
	})
	return
}
