package elections_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/elections"
)

// voting is a school with an election of the example dates (10 March 2027, 08:00 to 18:00) run by a
// commission of ana, with two approved lists.
type voting struct {
	school
	election, azul, roja string
}

func newVoting(t *testing.T, ctx context.Context) voting {
	t.Helper()
	v := voting{school: newSchool(t, ctx)}
	v.election = insertElection(t, ctx, v.tenant, nil)
	v.azul = insertList(t, ctx, v.tenant, v.election, v.ana, "Lista Azul", "approved")
	v.roja = insertList(t, ctx, v.tenant, v.election, v.beto, "Lista Roja", "approved")
	return v
}

// insertElection stores an election of the example dates in tenant, annulled when annulled is set.
func insertElection(t *testing.T, ctx context.Context, tenant string, annulled *time.Time) string {
	t.Helper()
	var commission, id string
	if err := tdb.Super.QueryRow(ctx, `SELECT id::text FROM electoral_commissions WHERE tenant_id = $1 AND ended_at IS NULL`, tenant).Scan(&commission); errors.Is(err, pgx.ErrNoRows) {
		err = tdb.Super.QueryRow(ctx, `INSERT INTO electoral_commissions (tenant_id) VALUES ($1) RETURNING id::text`, tenant).Scan(&commission)
		if err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	var reason *string
	if annulled != nil {
		r := "Motivo de prueba"
		reason = &r
	}
	if err := tdb.Super.QueryRow(ctx, `
		INSERT INTO elections (tenant_id, commission_id, name, mode, submission_starts_at, submission_ends_at, starts_at, ends_at, annulled_at, annulment_reason)
		VALUES ($1, $2, 'Elección 2027', 'secret', $3, $4, $5, $6, $7, $8) RETURNING id::text`,
		tenant, commission, example2027.SubmissionStartsAt, example2027.SubmissionEndsAt, example2027.StartsAt, example2027.EndsAt,
		annulled, reason).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertList(t *testing.T, ctx context.Context, tenant, election, rep, name, status string) string {
	t.Helper()
	var reason *string
	if status == "rejected" {
		r := "Incompleta"
		reason = &r
	}
	var id string
	if err := tdb.Super.QueryRow(ctx, `
		INSERT INTO election_lists (tenant_id, election_id, name, representative_id, status, is_active, rejection_reason)
		VALUES ($1, $2, $3, $4, $5, $5 <> 'rejected', $6) RETURNING id::text`, tenant, election, name, rep, status, reason).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func addRosterMember(t *testing.T, ctx context.Context, tenant, name, doc string) string {
	t.Helper()
	var id string
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO roster_members (tenant_id, full_name, document_number) VALUES ($1, $2, $3) RETURNING id::text`,
		tenant, name, doc).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// stored is what a vote may change: snapshot markers, snapshot members, voters, pending and boxed
// Ballots of tenant, read as superuser.
type storedVotes struct{ electorates, members, voters, pending, box int }

func votesOf(t *testing.T, ctx context.Context, tenant string) storedVotes {
	t.Helper()
	return storedVotes{
		superCount(t, ctx, "election_electorates", tenant), superCount(t, ctx, "election_electorate_members", tenant),
		superCount(t, ctx, "election_voters", tenant), superCount(t, ctx, "election_ballot_pending", tenant),
		superCount(t, ctx, "election_ballots", tenant),
	}
}

func at0800() *fakeClock { return &fakeClock{example2027.StartsAt} }

// S1: voting opens at StartsAt and closes at EndsAt (inclusive); a refused vote writes nothing.
func TestCastVoteOpensAndClosesByTheClock(t *testing.T) {
	ctx := integration(t)
	v := newVoting(t, ctx)
	clk := &fakeClock{at(time.March, 10, 7, 59)}
	st := newStore(clk)
	if err := st.CastVote(ctx, v.tenant, v.election, v.ana, v.azul); !errors.Is(err, elections.ErrElectionNotOpen) {
		t.Fatalf("07:59: err = %v, want ErrElectionNotOpen", err)
	}
	if got := votesOf(t, ctx, v.tenant); got != (storedVotes{}) {
		t.Fatalf("07:59 wrote %+v, want nothing", got)
	}
	clk.t = at(time.March, 10, 8, 0)
	if err := st.CastVote(ctx, v.tenant, v.election, v.ana, v.azul); err != nil {
		t.Fatalf("08:00: %v", err)
	}
	want := storedVotes{electorates: 1, members: 3, voters: 1, pending: 1}
	if got := votesOf(t, ctx, v.tenant); got != want {
		t.Fatalf("after 08:00 vote: %+v, want %+v", got, want)
	}
	clk.t = at(time.March, 10, 18, 0)
	if err := st.CastVote(ctx, v.tenant, v.election, v.beto, v.roja); !errors.Is(err, elections.ErrElectionNotOpen) {
		t.Fatalf("18:00: err = %v, want ErrElectionNotOpen", err)
	}
	if got := votesOf(t, ctx, v.tenant); got != want {
		t.Fatalf("18:00 changed the votes: %+v, want %+v", got, want)
	}
}

// S2: the Electorate is the roster at the first access after the start, taken once; a student added
// later is not in it, and its size is the turnout denominator.
func TestElectorateIsTheRosterAtTheFirstAccessAfterTheStart(t *testing.T) {
	ctx := integration(t)
	v := newVoting(t, ctx)
	clk := &fakeClock{at(time.March, 10, 7, 0)}
	st := newStore(clk)
	if _, err := st.Turnout(ctx, v.tenant, v.election, v.ana); !errors.Is(err, elections.ErrElectionNotOpen) {
		t.Fatalf("Turnout before the start: err = %v, want ErrElectionNotOpen", err)
	}
	if got := votesOf(t, ctx, v.tenant); got != (storedVotes{}) {
		t.Fatalf("before the start a snapshot was taken: %+v", got)
	}
	clk.t = at(time.March, 10, 8, 30)
	got, err := st.Turnout(ctx, v.tenant, v.election, v.ana)
	if err != nil {
		t.Fatalf("Turnout: %v", err)
	}
	if got != (elections.Turnout{Voted: 0, Electorate: 3}) {
		t.Fatalf("turnout = %+v, want 0 of 3", got)
	}
	dani := addRosterMember(t, ctx, v.tenant, "Dani Díaz", "999")
	if err := st.CastVote(ctx, v.tenant, v.election, dani, v.azul); !errors.Is(err, elections.ErrNotInElectorate) {
		t.Fatalf("student added after the start: err = %v, want ErrNotInElectorate", err)
	}
	if got, err := st.Turnout(ctx, v.tenant, v.election, dani); err != nil || got != (elections.Turnout{Voted: 0, Electorate: 3}) {
		t.Fatalf("turnout after adding a student = %+v (err %v), want 0 of 3", got, err)
	}
	if got := votesOf(t, ctx, v.tenant); got != (storedVotes{electorates: 1, members: 3}) {
		t.Fatalf("stored = %+v, want one snapshot of 3", got)
	}
}

// S2: concurrent first accesses (votes and turnout reads) produce exactly one snapshot.
func TestConcurrentFirstAccessesTakeOneSnapshot(t *testing.T) {
	ctx := integration(t)
	v := newVoting(t, ctx)
	st := newStore(at0800())
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for _, m := range []string{v.ana, v.beto, v.carla} {
		wg.Add(2)
		go func() { defer wg.Done(); errs <- st.CastVote(ctx, v.tenant, v.election, m, elections.BlankChoice) }()
		go func() { defer wg.Done(); _, err := st.Turnout(ctx, v.tenant, v.election, m); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent access: %v", err)
		}
	}
	if got := votesOf(t, ctx, v.tenant); got != (storedVotes{electorates: 1, members: 3, voters: 3, pending: 3}) {
		t.Fatalf("stored = %+v, want one snapshot of 3 and 3 votes", got)
	}
}

// S3: with no approved list at the start, voting does not open and nothing is written.
func TestCastVoteWithoutApprovedListsDoesNotOpen(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	election := insertElection(t, ctx, s.tenant, nil)
	pending := insertList(t, ctx, s.tenant, election, s.ana, "Lista Pendiente", "pending")
	insertList(t, ctx, s.tenant, election, s.beto, "Lista Rechazada", "rejected")
	st := newStore(at0800())
	for _, choice := range []string{pending, elections.BlankChoice} {
		if err := st.CastVote(ctx, s.tenant, election, s.ana, choice); !errors.Is(err, elections.ErrNoApprovedLists) {
			t.Fatalf("vote %q: err = %v, want ErrNoApprovedLists", choice, err)
		}
	}
	if _, err := st.Turnout(ctx, s.tenant, election, s.ana); !errors.Is(err, elections.ErrNoApprovedLists) {
		t.Fatalf("Turnout: err = %v, want ErrNoApprovedLists", err)
	}
	if got := votesOf(t, ctx, s.tenant); got != (storedVotes{}) {
		t.Fatalf("stored = %+v, want nothing", got)
	}
}

// T8 store side (S17): an annulled election takes no vote and shows no turnout.
func TestCastVoteOnAnAnnulledElection(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	annulled := example2027.StartsAt.Add(-time.Hour)
	election := insertElection(t, ctx, s.tenant, &annulled)
	list := insertList(t, ctx, s.tenant, election, s.ana, "Lista Azul", "approved")
	st := newStore(at0800())
	if err := st.CastVote(ctx, s.tenant, election, s.ana, list); !errors.Is(err, elections.ErrElectionAnnulled) {
		t.Fatalf("err = %v, want ErrElectionAnnulled", err)
	}
	if _, err := st.Turnout(ctx, s.tenant, election, s.ana); !errors.Is(err, elections.ErrElectionAnnulled) {
		t.Fatalf("Turnout: err = %v, want ErrElectionAnnulled", err)
	}
	if got := votesOf(t, ctx, s.tenant); got != (storedVotes{}) {
		t.Fatalf("stored = %+v, want nothing", got)
	}
}

// S5, S15: a Ballot is an approved list of this election or blank; anything else changes nothing.
// Ids of another school answer ErrNotFound.
func TestCastVoteChoices(t *testing.T) {
	ctx := integration(t)
	v := newVoting(t, ctx)
	pendingList := insertList(t, ctx, v.tenant, v.election, v.carla, "Lista Pendiente", "pending")
	rejected := insertList(t, ctx, v.tenant, v.election, v.carla, "Lista Rechazada", "rejected")
	past := example2027.StartsAt.AddDate(-1, 0, 0)
	older := insertElection(t, ctx, v.tenant, &past)
	otherElection := insertList(t, ctx, v.tenant, older, v.ana, "Lista Vieja", "approved")
	other := newVoting(t, ctx)
	st := newStore(at0800())
	// Take the snapshot first so that "nothing written" means the voter and Ballot counts.
	if _, err := st.Turnout(ctx, v.tenant, v.election, v.ana); err != nil {
		t.Fatal(err)
	}
	before := votesOf(t, ctx, v.tenant)
	for _, tc := range []struct {
		name, election, choice string
		want                   error
	}{
		{"pending list", v.election, pendingList, elections.ErrInvalidChoice},
		{"rejected list", v.election, rejected, elections.ErrInvalidChoice},
		{"list of another election", v.election, otherElection, elections.ErrInvalidChoice},
		{"empty", v.election, "", elections.ErrInvalidChoice},
		{"not a list id", v.election, "Lista Azul", elections.ErrInvalidChoice},
		{"list of another school", v.election, other.azul, elections.ErrNotFound},
		{"unknown list", v.election, "00000000-0000-4000-8000-000000000000", elections.ErrNotFound},
		{"election of another school", other.election, other.azul, elections.ErrNotFound},
		{"election id not a UUID", "nope", v.azul, elections.ErrNotFound},
	} {
		if err := st.CastVote(ctx, v.tenant, tc.election, v.ana, tc.choice); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		if got := votesOf(t, ctx, v.tenant); got != before {
			t.Fatalf("%s changed the votes: %+v, want %+v", tc.name, got, before)
		}
	}
	if got := votesOf(t, ctx, other.tenant); got != (storedVotes{}) {
		t.Fatalf("the other school changed: %+v", got)
	}
	if err := st.CastVote(ctx, v.tenant, v.election, v.ana, strings.ToUpper(v.azul)); err != nil {
		t.Fatalf("approved list in upper case: %v", err)
	}
	if err := st.CastVote(ctx, v.tenant, v.election, v.beto, elections.BlankChoice); err != nil {
		t.Fatalf("blank: %v", err)
	}
	if got := votesOf(t, ctx, v.tenant); got.voters != 2 || got.pending != 2 {
		t.Fatalf("after two valid votes: %+v, want 2 voters and 2 pending", got)
	}
	var lists []*string
	rows, err := tdb.Super.Query(ctx, `SELECT list_id::text FROM election_ballot_pending WHERE tenant_id = $1 ORDER BY list_id NULLS FIRST`, v.tenant)
	if err != nil {
		t.Fatal(err)
	}
	if lists, err = pgx.CollectRows(rows, pgx.RowTo[*string]); err != nil {
		t.Fatal(err)
	}
	if len(lists) != 2 || lists[0] != nil || lists[1] == nil || *lists[1] != v.azul {
		t.Fatalf("pending choices = %v, want blank and azul", lists)
	}
}

// S6: one vote per student; a second answers ErrAlreadyVoted and the Ballots do not change.
// Two simultaneous votes of one student: exactly one is accepted.
func TestCastVoteOncePerStudent(t *testing.T) {
	ctx := integration(t)
	v := newVoting(t, ctx)
	st := newStore(at0800())
	if err := st.CastVote(ctx, v.tenant, v.election, v.ana, v.azul); err != nil {
		t.Fatal(err)
	}
	before := votesOf(t, ctx, v.tenant)
	if err := st.CastVote(ctx, v.tenant, v.election, v.ana, v.roja); !errors.Is(err, elections.ErrAlreadyVoted) {
		t.Fatalf("second vote: err = %v, want ErrAlreadyVoted", err)
	}
	if got := votesOf(t, ctx, v.tenant); got != before {
		t.Fatalf("second vote changed the votes: %+v, want %+v", got, before)
	}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = st.CastVote(ctx, v.tenant, v.election, v.beto, elections.BlankChoice)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, elections.ErrAlreadyVoted):
			t.Fatalf("simultaneous vote: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("simultaneous votes accepted = %d (%v), want exactly 1", ok, errs)
	}
	if got := votesOf(t, ctx, v.tenant); got.voters != before.voters+1 || got.pending+got.box != before.pending+before.box+1 {
		t.Fatalf("after simultaneous votes: %+v, want one more voter and Ballot than %+v", got, before)
	}
}

// S9: the voter mark and the Ballot are written in one transaction. When the Ballot write fails
// (a trigger makes the pending insert raise, after the voter row was written in the same call), or
// the transaction fails after both writes (SetBallotFault, as if the server died before COMMIT),
// neither stays and the student can vote again.
func TestCastVoteRollsBackTheVoterWhenTheBallotWriteFails(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault func(t *testing.T, ctx context.Context, st *elections.Store, election string) (undo func(), want string)
	}{
		{"pending Ballot insert fails", func(t *testing.T, ctx context.Context, _ *elections.Store, election string) (func(), string) {
			name := "test_store_ballot_fault_" + strings.ReplaceAll(election, "-", "")
			if _, err := tdb.Super.Exec(ctx, fmt.Sprintf(`
				CREATE FUNCTION %[1]s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced ballot write failure'; END $$;
				CREATE TRIGGER %[1]s BEFORE INSERT ON election_ballot_pending FOR EACH ROW
				    WHEN (NEW.election_id = '%[2]s') EXECUTE FUNCTION %[1]s();`, name, election)); err != nil {
				t.Fatal(err)
			}
			return func() {
				if _, err := tdb.Super.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER %[1]s ON election_ballot_pending; DROP FUNCTION %[1]s();`, name)); err != nil {
					t.Error(err)
				}
			}, "forced ballot write failure"
		}},
		{"transaction fails after both writes", func(t *testing.T, _ context.Context, st *elections.Store, _ string) (func(), string) {
			elections.SetBallotFault(st, func() error { return errors.New("server died before commit") })
			return func() { elections.SetBallotFault(st, nil) }, "server died before commit"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := integration(t)
			v := newVoting(t, ctx)
			st := newStore(at0800())
			if err := st.CastVote(ctx, v.tenant, v.election, v.beto, v.roja); err != nil {
				t.Fatal(err)
			}
			before := votesOf(t, ctx, v.tenant)
			undo, want := tc.fault(t, ctx, st, v.election)
			err := st.CastVote(ctx, v.tenant, v.election, v.ana, v.azul)
			undo()
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want the forced failure %q", err, want)
			}
			if got := votesOf(t, ctx, v.tenant); got != before {
				t.Fatalf("after the failed write: %+v, want unchanged %+v", got, before)
			}
			if got, err := st.Turnout(ctx, v.tenant, v.election, v.ana); err != nil || got.HasVoted || got.Voted != 1 {
				t.Fatalf("turnout after the failure = %+v (err %v), want ana not voted and 1 vote", got, err)
			}
			if err := st.CastVote(ctx, v.tenant, v.election, v.ana, v.azul); err != nil {
				t.Fatalf("retry: %v", err)
			}
			if got := votesOf(t, ctx, v.tenant); got.voters != before.voters+1 || got.pending != before.pending+1 {
				t.Fatalf("after the retry: %+v, want one more voter and Ballot than %+v", got, before)
			}
		})
	}
}

// T6 store side (S12): turnout is "X of Y" and whether the caller voted; after the close it moves
// the pending Ballots into the box.
func TestTurnoutCountsVotersAndTheCallersOwnVote(t *testing.T) {
	ctx := integration(t)
	v := newVoting(t, ctx)
	clk := at0800()
	st := newStore(clk)
	if err := st.CastVote(ctx, v.tenant, v.election, v.ana, v.azul); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		member string
		want   elections.Turnout
	}{
		{v.ana, elections.Turnout{Voted: 1, Electorate: 3, HasVoted: true}},
		{v.beto, elections.Turnout{Voted: 1, Electorate: 3}},
		{"", elections.Turnout{Voted: 1, Electorate: 3}},
	} {
		if got, err := st.Turnout(ctx, v.tenant, v.election, tc.member); err != nil || got != tc.want {
			t.Fatalf("turnout for %q = %+v (err %v), want %+v", tc.member, got, err, tc.want)
		}
	}
	if got := votesOf(t, ctx, v.tenant); got.pending != 1 || got.box != 0 {
		t.Fatalf("while open: %+v, want the Ballot pending", got)
	}
	clk.t = example2027.EndsAt
	if got, err := st.Turnout(ctx, v.tenant, v.election, v.ana); err != nil || got != (elections.Turnout{Voted: 1, Electorate: 3, HasVoted: true}) {
		t.Fatalf("turnout after the close = %+v (err %v)", got, err)
	}
	if got := votesOf(t, ctx, v.tenant); got.pending != 0 || got.box != 1 {
		t.Fatalf("after the close: %+v, want the Ballot in the box", got)
	}
	if _, err := st.Turnout(ctx, v.tenant, "00000000-0000-4000-8000-000000000000", v.ana); !errors.Is(err, elections.ErrNotFound) {
		t.Fatalf("unknown election: err = %v, want ErrNotFound", err)
	}
}

// S10 (testing item (c)): Ballots stay pending, unreadable by ce_app, until the close; then the
// whole box is written once in a uniformly random order, so its storage order (what a query without
// ORDER BY returns) does not follow the vote order, not even by groups. The box keeps no time and no
// link to the voter, its ids are random, and ce_app can read neither the pending Ballots nor the
// box's ids or system columns. A flush called by ce_app while open is erased by the final one.
func TestBallotBoxKeepsNoLinkToTheVoter(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	election := insertElection(t, ctx, s.tenant, nil)
	const n = 45
	voters := make([]string, n)
	lists := make([]string, n)
	for i := range n {
		voters[i] = addRosterMember(t, ctx, s.tenant, fmt.Sprintf("Votante %02d", i), fmt.Sprintf("8%04d", i))
		lists[i] = insertList(t, ctx, s.tenant, election, voters[i], fmt.Sprintf("Lista %02d", i), "approved")
	}
	clk := at0800()
	st := newStore(clk)
	appBox := func() []string {
		t.Helper()
		var out []string
		if err := db.WithTenant(ctx, tdb.App, s.tenant, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT list_id::text FROM election_ballots WHERE election_id = $1`, election)
			if err != nil {
				return err
			}
			out, err = pgx.CollectRows(rows, pgx.RowTo[string])
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	const misuseAt = 20
	for i := range n {
		if err := st.CastVote(ctx, s.tenant, election, voters[i], lists[i]); err != nil {
			t.Fatalf("vote %d: %v", i, err)
		}
		got := votesOf(t, ctx, s.tenant)
		if got.voters != i+1 || got.pending+got.box != got.voters {
			t.Fatalf("after %d votes: %+v, want as many Ballots as voters", i+1, got)
		}
		if i < misuseAt {
			if got.box != 0 || len(appBox()) != 0 {
				t.Fatalf("after %d votes the box is not empty while open: %+v", i+1, got)
			}
		}
		if i+1 == misuseAt {
			// A misuse: ce_app flushes while open. The final flush rewrites the whole box.
			if err := db.WithTenant(ctx, tdb.App, s.tenant, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `SELECT election_ballots_flush($1)`, election)
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	clk.t = example2027.EndsAt
	if _, err := st.Turnout(ctx, s.tenant, election, ""); err != nil {
		t.Fatal(err)
	}
	if got := votesOf(t, ctx, s.tenant); got.box != n || got.pending != 0 {
		t.Fatalf("after the close: %+v, want %d Ballots in the box", got, n)
	}

	type ballot struct{ ID, List string }
	rows, err := tdb.Super.Query(ctx, `SELECT id::text, list_id::text FROM election_ballots WHERE tenant_id = $1 ORDER BY ctid`, s.tenant)
	if err != nil {
		t.Fatal(err)
	}
	box, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ballot])
	if err != nil {
		t.Fatal(err)
	}
	stored := make([]string, len(box))
	ids := make([]string, len(box))
	for i, b := range box {
		stored[i], ids[i] = b.List, b.ID
		if b.ID[14] != '4' {
			t.Fatalf("ballot id %s is not a random (version 4) UUID", b.ID)
		}
	}
	if !slices.Equal(slices.Sorted(slices.Values(stored)), slices.Sorted(slices.Values(lists))) {
		t.Fatal("the box does not hold exactly one Ballot per vote")
	}
	// The storage order, by ctid and as ce_app reads it without ORDER BY, is one uniform shuffle of
	// the 45 votes. Shuffled batches of 10 would keep every Ballot in its vote's group of 10 (45 of
	// 45) and the misused flush would keep the first 20 at the front; a uniform shuffle keeps about
	// 10 Ballots in their group and about 1 at its exact position. The bounds fail by chance with a
	// probability below one in a million.
	cast := map[string]int{}
	for i, l := range lists {
		cast[l] = i
	}
	for name, order := range map[string][]string{"ctid order": stored, "ce_app without ORDER BY": appBox()} {
		if len(order) != n {
			t.Fatalf("%s: %d Ballots, want %d", name, len(order), n)
		}
		same, sameGroup, firstMisused := 0, 0, 0
		for i, l := range order {
			if cast[l] == i {
				same++
			}
			if cast[l]/10 == i/10 {
				sameGroup++
			}
			if i < misuseAt && cast[l] < misuseAt {
				firstMisused++
			}
		}
		if slices.Equal(order, lists) || same >= 10 || sameGroup >= 30 || firstMisused == misuseAt {
			t.Fatalf("%s follows the vote order: %d at their vote position, %d in their group of 10, %d of the first %d at the front",
				name, same, sameGroup, firstMisused, misuseAt)
		}
	}
	if slices.IsSorted(ids) {
		t.Fatal("ballot ids follow the storage order")
	}
	var timeCols, linkCols []string
	rows, err = tdb.Super.Query(ctx, `
		SELECT table_name || '.' || column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name IN ('election_ballots', 'election_ballot_pending', 'election_voters')
		  AND data_type IN ('timestamp with time zone', 'timestamp without time zone', 'date', 'time without time zone', 'interval')`)
	if err != nil {
		t.Fatal(err)
	}
	if timeCols, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		t.Fatal(err)
	}
	if len(timeCols) != 0 {
		t.Fatalf("time columns: %v", timeCols)
	}
	// No column of the box or pending Ballots names a voter, account or session, and no foreign key
	// leaves them toward roster_members, users, memberships, sessions or election_voters.
	rows, err = tdb.Super.Query(ctx, `
		SELECT conrelid::regclass::text || ' -> ' || confrelid::regclass::text FROM pg_constraint
		WHERE contype = 'f' AND conrelid IN ('public.election_ballots'::regclass, 'public.election_ballot_pending'::regclass)
		  AND confrelid NOT IN ('public.organizations'::regclass, 'public.elections'::regclass, 'public.election_lists'::regclass)`)
	if err != nil {
		t.Fatal(err)
	}
	if linkCols, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		t.Fatal(err)
	}
	if len(linkCols) != 0 {
		t.Fatalf("Ballot foreign keys toward a voter: %v", linkCols)
	}

	// As ce_app: the list column is readable after the close, the order-revealing ones are not.
	var counted int
	if err := db.WithTenant(ctx, tdb.App, s.tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(list_id) FROM election_ballots WHERE election_id = $1`, election).Scan(&counted)
	}); err != nil || counted != n {
		t.Fatalf("ce_app counted %d Ballots (err %v), want %d", counted, err, n)
	}
	for _, q := range []string{
		`SELECT xmin FROM election_ballots`, `SELECT ctid FROM election_ballots`, `SELECT id FROM election_ballots`,
		`SELECT count(*) FROM election_ballot_pending`,
	} {
		err := db.WithTenant(ctx, tdb.App, s.tenant, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, q); return err })
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "42501" {
			t.Fatalf("%s as ce_app: err = %v, want 42501", q, err)
		}
	}
}

// S10 (review round 2): the shuffle does not depend on the session's random seed. ce_app may call
// setseed before the first flush after the close; with a seeded generator it could replay the
// permutation and, with the registry's order, tie each voter to their Ballot. Two schools with the
// same 45 votes in the same order, flushed after the same setseed, must store different orders.
func TestBallotBoxOrderDoesNotFollowTheSessionSeed(t *testing.T) {
	ctx := integration(t)
	const n = 45
	order := func() []int {
		t.Helper()
		s := newSchool(t, ctx)
		election := insertElection(t, ctx, s.tenant, nil)
		lists := make([]string, n)
		cast := map[string]int{}
		clk := at0800()
		st := newStore(clk)
		voters := make([]string, n)
		for i := range n {
			voters[i] = addRosterMember(t, ctx, s.tenant, fmt.Sprintf("Votante %02d", i), fmt.Sprintf("7%04d", i))
			lists[i] = insertList(t, ctx, s.tenant, election, voters[i], fmt.Sprintf("Lista %02d", i), "approved")
			cast[lists[i]] = i
		}
		for i := range n {
			if err := st.CastVote(ctx, s.tenant, election, voters[i], lists[i]); err != nil {
				t.Fatalf("vote %d: %v", i, err)
			}
		}
		var stored []string
		if err := db.WithTenant(ctx, tdb.App, s.tenant, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT setseed(0.42)`); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `SELECT election_ballots_flush($1)`, election); err != nil {
				return err
			}
			rows, err := tx.Query(ctx, `SELECT list_id::text FROM election_ballots WHERE election_id = $1`, election)
			if err != nil {
				return err
			}
			stored, err = pgx.CollectRows(rows, pgx.RowTo[string])
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if len(stored) != n {
			t.Fatalf("box holds %d Ballots, want %d", len(stored), n)
		}
		out := make([]int, n)
		for i, l := range stored {
			out[i] = cast[l]
		}
		return out
	}
	if a, b := order(), order(); slices.Equal(a, b) {
		t.Fatalf("both boxes stored the votes in the same order after setseed(0.42): %v", a)
	}
}

// S4: the configuration is frozen from StartsAt on.
func TestFrozenFromTheStart(t *testing.T) {
	for _, tc := range []struct {
		now  time.Time
		want bool
	}{
		{at(time.March, 10, 7, 59), false},
		{example2027.StartsAt.Add(-time.Nanosecond), false},
		{example2027.StartsAt, true},
		{example2027.EndsAt, true},
	} {
		if got := elections.Frozen(tc.now, example2027); got != tc.want {
			t.Fatalf("Frozen(%s) = %v, want %v", tc.now, got, tc.want)
		}
	}
}

// S7 and the D4 precedence through the session: unknown election before membership, no membership
// -> ErrForbidden, a member without a roster entry -> ErrNotInElectorate, a student votes once.
func TestVoteThroughTheSession(t *testing.T) {
	ctx := integration(t)
	v := newVoting(t, ctx)
	st := newStore(at0800())
	var stranger, admin string
	for i, u := range []*string{&stranger, &admin} {
		if err := tdb.Super.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id::text`,
			fmt.Sprintf("vote-%d-%s@example.com", i, v.tenant)).Scan(u); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'school_admin')`, v.tenant, admin); err != nil {
		t.Fatal(err)
	}
	sess := func(user string) auth.Session { return auth.Session{UserID: user, TenantID: v.tenant} }
	for _, tc := range []struct {
		name             string
		sess             auth.Session
		election, choice string
		want             error
	}{
		{"no tenant", auth.Session{UserID: stranger}, v.election, v.azul, elections.ErrForbidden},
		{"stranger, unknown election", sess(stranger), "00000000-0000-4000-8000-000000000000", v.azul, elections.ErrNotFound},
		{"stranger, unknown list", sess(stranger), v.election, "00000000-0000-4000-8000-000000000000", elections.ErrNotFound},
		{"stranger", sess(stranger), v.election, v.azul, elections.ErrForbidden},
		{"school admin without a roster entry", sess(admin), v.election, v.azul, elections.ErrNotInElectorate},
	} {
		if err := st.Vote(ctx, tc.sess, tc.election, tc.choice); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	if err := st.Vote(ctx, sess(v.anaUser), v.election, v.azul); err != nil {
		t.Fatalf("ana: %v", err)
	}
	if err := st.Vote(ctx, sess(v.anaUser), v.election, v.azul); !errors.Is(err, elections.ErrAlreadyVoted) {
		t.Fatalf("ana again: err = %v, want ErrAlreadyVoted", err)
	}
	if got, err := st.ViewTurnout(ctx, sess(v.anaUser), v.election); err != nil || got != (elections.Turnout{Voted: 1, Electorate: 3, HasVoted: true}) {
		t.Fatalf("ana's turnout = %+v (err %v)", got, err)
	}
	if got, err := st.ViewTurnout(ctx, sess(admin), v.election); err != nil || got != (elections.Turnout{Voted: 1, Electorate: 3}) {
		t.Fatalf("admin's turnout = %+v (err %v)", got, err)
	}
	if _, err := st.ViewTurnout(ctx, sess(stranger), v.election); !errors.Is(err, elections.ErrForbidden) {
		t.Fatalf("stranger's turnout: err = %v, want ErrForbidden", err)
	}
}
