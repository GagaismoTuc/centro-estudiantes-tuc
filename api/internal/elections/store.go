// Package elections stores a school's Electoral commission and its elections, and derives an
// election's phase from the clock. Authorization (who may call what) belongs to the callers.
package elections

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
)

// ModeSecret is the only voting mode ("Voto secreto").
const ModeSecret = "secret"

// DefaultPositions are an election's positions when none are given, in order.
var DefaultPositions = []string{"Presidente", "Vicepresidente", "Secretario", "Tesorero"}

var (
	// ErrNotFound means the id does not exist in the tenant (or is not a UUID); it never says
	// whether the id exists in another tenant.
	ErrNotFound = errors.New("elections: not found")
	// ErrEmptyCommission means a commission was designated with no members.
	ErrEmptyCommission = errors.New("elections: empty commission")
	// ErrNoAccount means a designated roster member has no account in the tenant.
	ErrNoAccount = errors.New("elections: roster member has no account")
	// ErrElectionFrozen means the commission's election already opened, so it cannot be replaced.
	ErrElectionFrozen = errors.New("elections: election frozen")
	// ErrNoCommission means the tenant has no current Electoral commission.
	ErrNoCommission = errors.New("elections: no current commission")
	// ErrElectionInProgress means the tenant already has an election neither published nor annulled.
	ErrElectionInProgress = errors.New("elections: election in progress")
	// ErrInvalidPositions means the position list is empty or has a blank or repeated
	// (case-insensitive) name.
	ErrInvalidPositions = errors.New("elections: invalid positions")
	// ErrNotDraft means the election is no longer a draft and cannot be edited.
	ErrNotDraft = errors.New("elections: not a draft")
	// ErrElectionAnnulled means the election was annulled: it accepts no change and never reopens.
	ErrElectionAnnulled = errors.New("elections: election annulled")
	// ErrElectionPublished means the election's result is published (its voting ended), so it can no
	// longer be annulled.
	ErrElectionPublished = errors.New("elections: election published")
	// ErrReasonRequired means a required reason (annulment or list rejection) is missing or invalid.
	ErrReasonRequired = errors.New("elections: reason required")
	// ErrReasonTooLong means an annulment reason is longer than MaxReasonLength characters.
	ErrReasonTooLong = errors.New("elections: annulment reason too long")
)

// MaxReasonLength is the longest annulment reason, in characters, once trimmed.
const MaxReasonLength = 500

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// CommissionMember is a roster member on the commission.
type CommissionMember struct{ RosterMemberID, FullName string }

// Commission is a tenant's current Electoral commission, members ordered by name.
type Commission struct {
	ID        string
	CreatedAt time.Time
	Members   []CommissionMember
}

// Draft is the editable part of an election; checkDraft validates it before any write.
type Draft struct {
	Name, Mode                                             string
	SubmissionStartsAt, SubmissionEndsAt, StartsAt, EndsAt time.Time
	Positions                                              []string
}

// Election is a stored election with its positions in order.
type Election struct {
	ID, CommissionID, Name, Mode                           string
	SubmissionStartsAt, SubmissionEndsAt, StartsAt, EndsAt time.Time
	PublishedAt, AnnulledAt                                *time.Time
	// AnnulmentReason is the public reason of an annulled election; nil exactly when AnnulledAt is.
	AnnulmentReason      *string
	CreatedAt, UpdatedAt time.Time
	Positions            []string
	// Commission lists the members of the election's commission, ordered by name.
	Commission []CommissionMember
}

// Store runs election operations inside db.WithTenant.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
	// ballotFault, set only by tests (export_test.go), replaces a vote's Ballot write with its error.
	ballotFault func() error
}

// NewStore returns a Store with the clock now. It panics when now is nil.
func NewStore(pool *pgxpool.Pool, now func() time.Time) *Store {
	if now == nil {
		panic("elections: NewStore needs a clock")
	}
	return &Store{pool: pool, now: now}
}

func (s *Store) stamp() time.Time { return stored(s.now()) }

// stored returns t as PostgreSQL stores a timestamptz: UTC, finer digits than microseconds discarded.
func stored(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

// ReplaceCommission sets the members of the tenant's current commission, creating it when there is
// none. Repeated ids count once. Errors: ErrEmptyCommission, ErrNotFound (an id outside the tenant),
// ErrNoAccount, ErrElectionFrozen (an election of this commission already opened). On error nothing
// is written. Ids are compared in lower case, as PostgreSQL reads a UUID.
// It does no authorization: Designate is the entry point for requests.
func (s *Store) ReplaceCommission(ctx context.Context, tenant string, rosterMemberIDs []string) (Commission, error) {
	var out Commission
	err := db.WithTenant(ctx, s.pool, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = s.replaceCommission(ctx, tx, tenant, rosterMemberIDs)
		return err
	})
	return out, err
}

// replaceCommission is ReplaceCommission inside tx, already scoped to tenant.
func (s *Store) replaceCommission(ctx context.Context, tx pgx.Tx, tenant string, rosterMemberIDs []string) (Commission, error) {
	// A UUID is case-insensitive in PostgreSQL, so ids are compared in lower case: the same member
	// sent in two cases counts once.
	ids := make([]string, len(rosterMemberIDs))
	for i, id := range rosterMemberIDs {
		ids[i] = strings.ToLower(id)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var out Commission
	err := func() error {
		if len(ids) == 0 {
			return ErrEmptyCommission
		}
		for _, id := range ids {
			if !uuidPattern.MatchString(id) {
				return ErrNotFound
			}
		}
		if err := lockCommission(ctx, tx, tenant); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT EXISTS (SELECT FROM memberships ms WHERE ms.tenant_id = m.tenant_id AND ms.roster_member_id = m.id)
			FROM roster_members m WHERE m.tenant_id = $1 AND m.id = ANY ($2::uuid[])`, tenant, ids)
		if err != nil {
			return err
		}
		accounts, err := pgx.CollectRows(rows, pgx.RowTo[bool])
		if err != nil {
			return err
		}
		if len(accounts) != len(ids) {
			return ErrNotFound
		}
		if slices.Contains(accounts, false) {
			return ErrNoAccount
		}
		var commission string
		err = tx.QueryRow(ctx, `SELECT id::text FROM electoral_commissions WHERE tenant_id = $1 AND ended_at IS NULL`, tenant).Scan(&commission)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `INSERT INTO electoral_commissions (tenant_id, created_at) VALUES ($1, $2) RETURNING id::text`,
				tenant, s.stamp()).Scan(&commission)
		}
		if err != nil {
			return err
		}
		var frozen bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT FROM elections WHERE tenant_id = $1 AND commission_id = $2 AND starts_at <= $3)`,
			tenant, commission, s.now()).Scan(&frozen); err != nil {
			return err
		}
		if frozen {
			return ErrElectionFrozen
		}
		if _, err := tx.Exec(ctx, `DELETE FROM electoral_commission_members WHERE tenant_id = $1 AND commission_id = $2`, tenant, commission); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO electoral_commission_members (tenant_id, commission_id, roster_member_id)
			SELECT $1, $2, unnest($3::uuid[])`, tenant, commission, ids); err != nil {
			return err
		}
		out, err = currentCommission(ctx, tx, tenant)
		return err
	}()
	return out, err
}

// lockCommission takes the tenant's commission lock until tx ends. Designations take it so two first
// designations do not race to create the commission; draft writes take it before checking that the
// user is on the commission, so a replacement cannot commit between that check and the write.
func lockCommission(ctx context.Context, tx pgx.Tx, tenant string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('electoral_commission:' || $1::text, 0))`, tenant)
	return err
}

// CurrentCommission returns the tenant's current commission, or ErrNotFound.
func (s *Store) CurrentCommission(ctx context.Context, tenant string) (Commission, error) {
	var out Commission
	err := db.WithTenant(ctx, s.pool, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = currentCommission(ctx, tx, tenant)
		return err
	})
	return out, err
}

func currentCommission(ctx context.Context, tx pgx.Tx, tenant string) (Commission, error) {
	var c Commission
	err := tx.QueryRow(ctx, `SELECT id::text, created_at FROM electoral_commissions WHERE tenant_id = $1 AND ended_at IS NULL`, tenant).
		Scan(&c.ID, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	rows, err := tx.Query(ctx, `
		SELECT m.id::text, m.full_name FROM electoral_commission_members cm
		JOIN roster_members m ON m.tenant_id = cm.tenant_id AND m.id = cm.roster_member_id
		WHERE cm.tenant_id = $1 AND cm.commission_id = $2 ORDER BY m.full_name, m.id`, tenant, c.ID)
	if err != nil {
		return c, err
	}
	c.Members, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CommissionMember])
	return c, err
}

// IsCommissionMember reports whether userID's roster entry in the tenant is on its current commission.
func (s *Store) IsCommissionMember(ctx context.Context, tenant, userID string) (bool, error) {
	if !uuidPattern.MatchString(userID) {
		return false, nil
	}
	var ok bool
	err := db.WithTenant(ctx, s.pool, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT FROM electoral_commission_members cm
				JOIN electoral_commissions c ON c.tenant_id = cm.tenant_id AND c.id = cm.commission_id AND c.ended_at IS NULL
				JOIN memberships ms ON ms.tenant_id = cm.tenant_id AND ms.roster_member_id = cm.roster_member_id
				WHERE cm.tenant_id = $1 AND ms.user_id = $2)`, tenant, userID).Scan(&ok)
	})
	return ok, err
}

// CreateElection stores d as an election of the tenant's current commission, after checkDraft.
// Errors: those of checkDraft, ErrNoCommission and ErrElectionInProgress (decided by the database, so
// of two concurrent calls exactly one wins). It does no authorization: Create is the entry point for
// requests.
func (s *Store) CreateElection(ctx context.Context, tenant string, d Draft) (Election, error) {
	var out Election
	err := db.WithTenant(ctx, s.pool, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = s.createElection(ctx, tx, tenant, d)
		return err
	})
	return out, err
}

func (s *Store) createElection(ctx context.Context, tx pgx.Tx, tenant string, d Draft) (Election, error) {
	d, err := checkDraft(s.now(), d)
	if err != nil {
		return Election{}, err
	}
	var commission string
	err = tx.QueryRow(ctx, `SELECT id::text FROM electoral_commissions WHERE tenant_id = $1 AND ended_at IS NULL`, tenant).Scan(&commission)
	if errors.Is(err, pgx.ErrNoRows) {
		return Election{}, ErrNoCommission
	}
	if err != nil {
		return Election{}, err
	}
	now := s.stamp()
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO elections (tenant_id, commission_id, name, mode, submission_starts_at, submission_ends_at, starts_at, ends_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9) RETURNING id::text`,
		tenant, commission, d.Name, d.Mode, d.SubmissionStartsAt, d.SubmissionEndsAt, d.StartsAt, d.EndsAt, now).Scan(&id)
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "elections_one_in_progress" {
		return Election{}, ErrElectionInProgress
	}
	if err != nil {
		return Election{}, err
	}
	if err := insertPositions(ctx, tx, tenant, id, d.Positions); err != nil {
		return Election{}, err
	}
	return getElection(ctx, tx, `e.id = $2`, tenant, id)
}

// UpdateDraft replaces the name, mode, dates and positions of a draft election, after checkDraft.
// Errors: ErrNotFound, ErrElectionAnnulled, ErrNotDraft (published or past StatusDraft at the store's
// clock) and those of checkDraft. It does no authorization: Edit is the entry point for requests.
func (s *Store) UpdateDraft(ctx context.Context, tenant, id string, d Draft) (Election, error) {
	var out Election
	err := db.WithTenant(ctx, s.pool, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = s.updateDraft(ctx, tx, tenant, id, d)
		return err
	})
	return out, err
}

func (s *Store) updateDraft(ctx context.Context, tx pgx.Tx, tenant, id string, d Draft) (Election, error) {
	if !uuidPattern.MatchString(id) {
		return Election{}, ErrNotFound
	}
	cur, err := getElection(ctx, tx, `e.id = $2 FOR UPDATE OF e`, tenant, id)
	if err != nil {
		return Election{}, err
	}
	if cur.AnnulledAt != nil {
		return Election{}, ErrElectionAnnulled
	}
	if cur.PublishedAt != nil || State(s.now(), cur) != StatusDraft {
		return Election{}, ErrNotDraft
	}
	if d, err = checkDraft(s.now(), d); err != nil {
		return Election{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE elections SET name = $3, mode = $4, submission_starts_at = $5, submission_ends_at = $6,
		       starts_at = $7, ends_at = $8, updated_at = $9
		WHERE tenant_id = $1 AND id = $2`,
		tenant, id, d.Name, d.Mode, d.SubmissionStartsAt, d.SubmissionEndsAt, d.StartsAt, d.EndsAt, s.stamp()); err != nil {
		return Election{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM election_positions WHERE tenant_id = $1 AND election_id = $2`, tenant, id); err != nil {
		return Election{}, err
	}
	if err := insertPositions(ctx, tx, tenant, id, d.Positions); err != nil {
		return Election{}, err
	}
	return getElection(ctx, tx, `e.id = $2`, tenant, id)
}

func insertPositions(ctx context.Context, tx pgx.Tx, tenant, election string, names []string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO election_positions (tenant_id, election_id, position_order, name)
		SELECT $1, $2, ord, name FROM unnest($3::text[]) WITH ORDINALITY AS p(name, ord)`, tenant, election, names)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "23505" || pgErr.Code == "23514") && pgErr.TableName == "election_positions" {
		return ErrInvalidPositions
	}
	return err
}

// GetElection returns the tenant's election id, or ErrNotFound.
func (s *Store) GetElection(ctx context.Context, tenant, id string) (Election, error) {
	var out Election
	err := db.WithTenant(ctx, s.pool, tenant, func(tx pgx.Tx) error {
		if !uuidPattern.MatchString(id) {
			return ErrNotFound
		}
		var err error
		out, err = getElection(ctx, tx, `e.id = $2`, tenant, id)
		return err
	})
	return out, err
}

// CurrentElection returns the tenant's election in progress (neither published nor annulled), or
// ErrNotFound.
func (s *Store) CurrentElection(ctx context.Context, tenant string) (Election, error) {
	var out Election
	err := db.WithTenant(ctx, s.pool, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = getElection(ctx, tx, `e.published_at IS NULL AND e.annulled_at IS NULL`, tenant)
		return err
	})
	return out, err
}

// getElection reads one election matching where (plus an optional locking clause); args[0] is the
// tenant ($1) and the rest are where's parameters.
func getElection(ctx context.Context, tx pgx.Tx, where string, args ...any) (Election, error) {
	var e Election
	err := tx.QueryRow(ctx, `
		SELECT e.id::text, e.commission_id::text, e.name, e.mode, e.submission_starts_at, e.submission_ends_at,
		       e.starts_at, e.ends_at, e.published_at, e.annulled_at, e.annulment_reason, e.created_at, e.updated_at,
		       ARRAY(SELECT p.name FROM election_positions p WHERE p.tenant_id = e.tenant_id AND p.election_id = e.id ORDER BY p.position_order)
		FROM elections e WHERE e.tenant_id = $1 AND `+where, args...).
		Scan(&e.ID, &e.CommissionID, &e.Name, &e.Mode, &e.SubmissionStartsAt, &e.SubmissionEndsAt,
			&e.StartsAt, &e.EndsAt, &e.PublishedAt, &e.AnnulledAt, &e.AnnulmentReason, &e.CreatedAt, &e.UpdatedAt, &e.Positions)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, ErrNotFound
	}
	if err != nil {
		return e, err
	}
	rows, err := tx.Query(ctx, `
		SELECT m.id::text, m.full_name FROM electoral_commission_members cm
		JOIN roster_members m ON m.tenant_id = cm.tenant_id AND m.id = cm.roster_member_id
		WHERE cm.tenant_id = $1 AND cm.commission_id = $2 ORDER BY m.full_name, m.id`, args[0], e.CommissionID)
	if err != nil {
		return e, err
	}
	e.Commission, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CommissionMember])
	return e, err
}
