package elections

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
)

// ErrResultsNotAvailable means the election has not reached its EndsAt, by the store's clock or by
// the database's.
var ErrResultsNotAvailable = errors.New("elections: results not available")

// Outcome says who won an election with a published result.
type Outcome string

const (
	OutcomeWinner  Outcome = "winner"   // one list has the most votes
	OutcomeTie     Outcome = "tie"      // two or more lists share the most votes
	OutcomeNoVotes Outcome = "no_votes" // every list has 0 votes, Blank votes or not
	OutcomeNoLists Outcome = "no_lists" // no approved list: voting never opened
)

// ListVotes is an approved list of an election and its votes.
type ListVotes struct {
	ID, Name string
	Votes    int
}

// Result is an election's result: annulled (with its reason only) or published.
type Result struct {
	Annulled        bool
	AnnulmentReason string
	// Lists are every approved list, most votes first, then by name and id.
	Lists                    []ListVotes
	Blank, Voted, Electorate int
	Outcome                  Outcome
	Winner                   *ListVotes // only for OutcomeWinner
}

// ViewResult returns, to any member of the session's tenant (ErrForbidden otherwise; no second
// factor), the result of election id: ErrNotFound, then ErrResultsNotAvailable before its EndsAt.
// See result.
func (s *Store) ViewResult(ctx context.Context, sess auth.Session, id string) (Result, error) {
	var out Result
	err := s.asMember(ctx, sess, func(tx pgx.Tx) error {
		var err error
		out, err = s.result(ctx, tx, sess.TenantID, id)
		return err
	})
	return out, err
}

// result computes the result on read and, the first time, publishes it (see publish: the winner,
// the end of the commission's mandate and the Board handover). It holds the election row locked (FOR
// UPDATE), as castVote does before checking the clock, so no Ballot is cast while it counts nor,
// the end having passed, after it. The box is written by election_ballots_flush, which the
// database refuses until its own clock reaches the end: until then this is ErrResultsNotAvailable,
// never a partial tally. After the end the Ballots, the lists and the annulment never change
// (Annul refuses a closed election), so every read gives the same result. An election that never
// opened takes its Electorate snapshot here, as the turnout does, so Y stays fixed too.
func (s *Store) result(ctx context.Context, tx pgx.Tx, tenant, id string) (Result, error) {
	var out Result
	if !uuidPattern.MatchString(id) {
		return out, ErrNotFound
	}
	e, err := readVotingElection(ctx, tx, tenant, id, true)
	if err != nil {
		return out, err
	}
	if s.now().Before(e.endsAt) {
		return out, ErrResultsNotAvailable
	}
	if e.annulled {
		out.Annulled = true
		err := tx.QueryRow(ctx, `SELECT annulment_reason FROM elections WHERE tenant_id = $1 AND id = $2`, tenant, id).Scan(&out.AnnulmentReason)
		return out, err
	}
	var moved int
	err = tx.QueryRow(ctx, `
		SELECT election_ballots_flush(id) FROM elections
		WHERE tenant_id = $1 AND id = $2 AND now() >= ends_at`, tenant, id).Scan(&moved)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrResultsNotAvailable
	}
	if err != nil {
		return out, err
	}
	if err := ensureElectorate(ctx, tx, tenant, id); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `
		SELECT l.id::text, l.name, count(b.list_id)
		FROM election_lists l
		LEFT JOIN election_ballots b ON b.tenant_id = l.tenant_id AND b.election_id = l.election_id AND b.list_id = l.id
		WHERE l.tenant_id = $1 AND l.election_id = $2 AND l.status = 'approved'
		GROUP BY l.id, l.name
		ORDER BY count(b.list_id) DESC, l.name, l.id`, tenant, id)
	if err != nil {
		return out, err
	}
	out.Lists, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ListVotes, error) {
		var l ListVotes
		return l, r.Scan(&l.ID, &l.Name, &l.Votes)
	})
	if err != nil {
		return out, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM election_ballots WHERE tenant_id = $1 AND election_id = $2 AND list_id IS NULL),
		       (SELECT count(*) FROM election_voters WHERE tenant_id = $1 AND election_id = $2),
		       (SELECT size FROM election_electorates WHERE tenant_id = $1 AND election_id = $2)`,
		tenant, id).Scan(&out.Blank, &out.Voted, &out.Electorate); err != nil {
		return out, err
	}
	switch {
	case len(out.Lists) == 0:
		out.Outcome = OutcomeNoLists
	case out.Lists[0].Votes == 0:
		out.Outcome = OutcomeNoVotes
	case len(out.Lists) > 1 && out.Lists[1].Votes == out.Lists[0].Votes:
		out.Outcome = OutcomeTie
	default:
		out.Outcome = OutcomeWinner
		out.Winner = &out.Lists[0]
	}
	return out, s.publish(ctx, tx, tenant, id, out)
}
