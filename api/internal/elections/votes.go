package elections

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
)

// BlankChoice is the choice of a Blank vote; any other choice is the id of a list.
const BlankChoice = "blank"

var (
	// ErrElectionNotOpen means the clock is before the election's StartsAt or at or after its EndsAt.
	ErrElectionNotOpen = errors.New("elections: election not open")
	// ErrNoApprovedLists means the election has no approved list, so its voting never opens.
	ErrNoApprovedLists = errors.New("elections: no approved lists")
	// ErrNotInElectorate means the roster member is not in the election's Electorate snapshot.
	ErrNotInElectorate = errors.New("elections: not in electorate")
	// ErrInvalidChoice means the choice is neither BlankChoice nor an approved list of the election.
	ErrInvalidChoice = errors.New("elections: invalid choice")
	// ErrAlreadyVoted means the roster member is already in the Voter registry of the election.
	ErrAlreadyVoted = errors.New("elections: already voted")
)

// Turnout is how many members of the Electorate voted out of its size, and whether the caller did.
type Turnout struct {
	Voted, Electorate int
	HasVoted          bool
}

// Frozen reports whether e's configuration (the election, its positions, its lists and its
// commission) can no longer change at now: from StartsAt on (S4).
func Frozen(now time.Time, e Election) bool { return !now.Before(e.StartsAt) }

// votingElection is what a vote reads of its election.
type votingElection struct {
	startsAt, endsAt time.Time
	annulled         bool
}

// readVotingElection reads election id of tenant, locking its row when lock is set: ErrNotFound when
// id is not a UUID or not an election of the tenant.
func readVotingElection(ctx context.Context, tx pgx.Tx, tenant, id string, lock bool) (votingElection, error) {
	var e votingElection
	if !uuidPattern.MatchString(id) {
		return e, ErrNotFound
	}
	q := `SELECT starts_at, ends_at, annulled_at IS NOT NULL FROM elections WHERE tenant_id = $1 AND id = $2`
	if lock {
		q += ` FOR UPDATE`
	}
	err := tx.QueryRow(ctx, q, tenant, id).Scan(&e.startsAt, &e.endsAt, &e.annulled)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// checkVotingOpen returns ErrElectionAnnulled, ErrElectionNotOpen (before the start; and after the
// end when closedOK is false) or ErrNoApprovedLists, in that order.
func checkVotingOpen(ctx context.Context, tx pgx.Tx, tenant, election string, e votingElection, now time.Time, closedOK bool) error {
	if e.annulled {
		return ErrElectionAnnulled
	}
	if now.Before(e.startsAt) || (!closedOK && !now.Before(e.endsAt)) {
		return ErrElectionNotOpen
	}
	var approved bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT FROM election_lists WHERE tenant_id = $1 AND election_id = $2 AND status = 'approved')`,
		tenant, election).Scan(&approved); err != nil {
		return err
	}
	if !approved {
		return ErrNoApprovedLists
	}
	return nil
}

// ensureElectorate takes the election's Electorate snapshot (the whole roster) unless it exists. The
// caller holds the election row locked (FOR UPDATE), so of two first accesses the second waits and
// then finds the snapshot; one statement reads the roster for both the size and the members.
func ensureElectorate(ctx context.Context, tx pgx.Tx, tenant, election string) error {
	_, err := tx.Exec(ctx, `
		WITH roster AS (SELECT id FROM roster_members WHERE tenant_id = $1),
		     marker AS (
		         INSERT INTO election_electorates (tenant_id, election_id, size)
		         SELECT $1, $2, (SELECT count(*) FROM roster)
		         WHERE NOT EXISTS (SELECT FROM election_electorates WHERE tenant_id = $1 AND election_id = $2)
		         RETURNING election_id)
		INSERT INTO election_electorate_members (tenant_id, election_id, roster_member_id)
		SELECT $1, $2, roster.id FROM roster, marker`, tenant, election)
	return err
}

// hasElectorate reports whether the election's snapshot exists.
func hasElectorate(ctx context.Context, tx pgx.Tx, tenant, election string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT FROM election_electorates WHERE tenant_id = $1 AND election_id = $2)`, tenant, election).Scan(&ok)
	return ok, err
}

// CastVote records that rosterMemberID voted in the tenant's election electionID and stores its
// Ballot (choice is BlankChoice or the id of an approved list of the election), in one transaction
// (S9): on any error neither is written. Errors, in this order: ErrNotFound (election or list unknown
// in the tenant), ErrElectionAnnulled, ErrElectionNotOpen, ErrNoApprovedLists, ErrNotInElectorate,
// ErrInvalidChoice, ErrAlreadyVoted. It does no authorization: Vote is the entry point for requests.
// Nothing here is logged: the choice must never be tied to the voter (S11).
func (s *Store) CastVote(ctx context.Context, tenant, electionID, rosterMemberID, choice string) error {
	return db.WithTenant(ctx, s.pool, tenant, func(tx pgx.Tx) error {
		return s.castVote(ctx, tx, tenant, electionID, choice, func() (string, error) { return rosterMemberID, nil })
	})
}

// castVote is CastVote inside tx; voter is called after the not-found checks and returns the voting
// roster member ("" for none) or an authorization error.
func (s *Store) castVote(ctx context.Context, tx pgx.Tx, tenant, electionID, choice string, voter func() (string, error)) error {
	e, err := readVotingElection(ctx, tx, tenant, electionID, true)
	if err != nil {
		return err
	}
	electionID = strings.ToLower(electionID)
	choice = strings.ToLower(strings.TrimSpace(choice))
	var list *string
	validChoice := choice == BlankChoice
	if uuidPattern.MatchString(choice) {
		var listElection string
		var status ListStatus
		err := tx.QueryRow(ctx, `SELECT election_id::text, status FROM election_lists WHERE tenant_id = $1 AND id = $2`, tenant, choice).
			Scan(&listElection, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		list, validChoice = &choice, listElection == electionID && status == ListApproved
	}
	member, err := voter()
	if err != nil {
		return err
	}
	if err := checkVotingOpen(ctx, tx, tenant, electionID, e, s.now(), false); err != nil {
		return err
	}
	if err := ensureElectorate(ctx, tx, tenant, electionID); err != nil {
		return err
	}
	inElectorate := false
	if uuidPattern.MatchString(member) {
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT FROM election_electorate_members WHERE tenant_id = $1 AND election_id = $2 AND roster_member_id = $3)`,
			tenant, electionID, member).Scan(&inElectorate); err != nil {
			return err
		}
	}
	if !inElectorate {
		return ErrNotInElectorate
	}
	if !validChoice {
		return ErrInvalidChoice
	}
	// election_ballot_cast writes the voter row and the pending Ballot. The election row is locked, so
	// a concurrent vote of the same student waits here and then hits the registry's primary key.
	_, err = tx.Exec(ctx, `SELECT election_ballot_cast($1, $2, $3)`, electionID, member, list)
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "election_voters_pkey" {
		return ErrAlreadyVoted
	}
	if err != nil {
		return err
	}
	if s.ballotFault != nil {
		return s.ballotFault()
	}
	return nil
}

// Turnout returns how many members of the Electorate of the tenant's election voted, its size and
// whether rosterMemberID ("" for none) voted. The first access after the start takes the Electorate
// snapshot; every access after the end writes the pending Ballots into the box, which stays empty
// until then and is written whole, shuffled once. Errors: ErrNotFound, ErrElectionAnnulled,
// ErrElectionNotOpen (before the start), ErrNoApprovedLists.
func (s *Store) Turnout(ctx context.Context, tenant, electionID, rosterMemberID string) (Turnout, error) {
	var out Turnout
	err := db.WithTenant(ctx, s.pool, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = s.turnout(ctx, tx, tenant, electionID, rosterMemberID)
		return err
	})
	return out, err
}

func (s *Store) turnout(ctx context.Context, tx pgx.Tx, tenant, electionID, rosterMemberID string) (Turnout, error) {
	var out Turnout
	e, err := readVotingElection(ctx, tx, tenant, electionID, false)
	if err != nil {
		return out, err
	}
	electionID = strings.ToLower(electionID)
	now := s.now()
	if err := checkVotingOpen(ctx, tx, tenant, electionID, e, now, true); err != nil {
		return out, err
	}
	ok, err := hasElectorate(ctx, tx, tenant, electionID)
	if err != nil {
		return out, err
	}
	closed := !now.Before(e.endsAt)
	// Writes take the election lock first, as votes do, so they never wait on a vote in the
	// opposite order, and the flush sees every committed vote.
	if !ok || closed {
		if _, err := readVotingElection(ctx, tx, tenant, electionID, true); err != nil {
			return out, err
		}
	}
	if !ok {
		if err := ensureElectorate(ctx, tx, tenant, electionID); err != nil {
			return out, err
		}
	}
	if closed {
		if _, err := tx.Exec(ctx, `SELECT election_ballots_flush($1)`, electionID); err != nil {
			return out, err
		}
	}
	member := ""
	if uuidPattern.MatchString(rosterMemberID) {
		member = rosterMemberID
	}
	err = tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM election_voters WHERE tenant_id = $1 AND election_id = $2),
		       (SELECT size FROM election_electorates WHERE tenant_id = $1 AND election_id = $2),
		       EXISTS (SELECT FROM election_voters WHERE tenant_id = $1 AND election_id = $2 AND roster_member_id::text = lower($3))`,
		tenant, electionID, member).Scan(&out.Voted, &out.Electorate, &out.HasVoted)
	return out, err
}

// Vote is CastVote for the session user's roster entry. Errors follow the HTTP precedence:
// ErrNotFound (election or list), ErrForbidden (no membership in the tenant), then CastVote's;
// a member without a roster entry gets ErrNotInElectorate. No second factor is needed (S14).
func (s *Store) Vote(ctx context.Context, sess auth.Session, electionID, choice string) error {
	if sess.TenantID == "" {
		return ErrForbidden
	}
	return db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		return s.castVote(ctx, tx, sess.TenantID, electionID, choice, func() (string, error) {
			a, err := readAccess(ctx, tx, sess.TenantID, sess.UserID)
			if err != nil {
				return "", err
			}
			if !a.member {
				return "", ErrForbidden
			}
			return a.rosterMemberID, nil
		})
	})
}

// ViewTurnout is Turnout for any member of the session's tenant (ErrForbidden otherwise), with
// HasVoted for the user's own roster entry. No second factor is needed.
func (s *Store) ViewTurnout(ctx context.Context, sess auth.Session, electionID string) (Turnout, error) {
	var out Turnout
	if sess.TenantID == "" {
		return out, ErrForbidden
	}
	err := db.WithTenant(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		a, err := readAccess(ctx, tx, sess.TenantID, sess.UserID)
		if err != nil {
			return err
		}
		if !a.member {
			return ErrForbidden
		}
		out, err = s.turnout(ctx, tx, sess.TenantID, electionID, a.rosterMemberID)
		return err
	})
	return out, err
}
