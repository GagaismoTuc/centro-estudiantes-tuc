package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// resultList is one approved list of a published result.
type resultList struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Votes int    `json:"votes"`
}

type resultJSON struct {
	Status          string       `json:"status"`
	AnnulmentReason string       `json:"annulmentReason"`
	Mode            string       `json:"mode"`
	Lists           []resultList `json:"lists"`
	BlankVotes      int          `json:"blankVotes"`
	Voted           int          `json:"voted"`
	Electorate      int          `json:"electorate"`
	Outcome         string       `json:"outcome"`
	Winner          *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"winner"`
}

// sortedKeys returns the keys of the JSON object raw, sorted.
func sortedKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// decodeResult checks a 200 result with Cache-Control: no-store and exactly the keys of its status
// (S7: nothing else, so nothing that identifies a voter or a Ballot), and decodes it.
func decodeResult(t *testing.T, rec *httptest.ResponseRecorder) resultJSON {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("results = %d %s, want 200", rec.Code, rec.Body)
	}
	wantNoStore(t, rec)
	var r resultJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	keys := sortedKeys(t, rec.Body.Bytes())
	switch r.Status {
	case "annulled":
		if !slices.Equal(keys, []string{"annulmentReason", "status"}) {
			t.Fatalf("annulled result keys = %v, want only annulmentReason and status", keys)
		}
		return r
	case "published":
	default:
		t.Fatalf("status = %q, want published or annulled", r.Status)
	}
	want := []string{"blankVotes", "electorate", "lists", "mode", "outcome", "status", "voted"}
	if r.Outcome == "winner" {
		want = []string{"blankVotes", "electorate", "lists", "mode", "outcome", "status", "voted", "winner"}
	}
	if !slices.Equal(keys, want) {
		t.Fatalf("published result keys = %v, want %v", keys, want)
	}
	var raw struct {
		Lists  []json.RawMessage `json:"lists"`
		Winner json.RawMessage   `json:"winner"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Lists == nil {
		t.Fatalf("lists = null, want an array: %s", rec.Body)
	}
	for _, l := range raw.Lists {
		if k := sortedKeys(t, l); !slices.Equal(k, []string{"id", "name", "votes"}) {
			t.Fatalf("list keys = %v, want only id, name and votes", k)
		}
	}
	if raw.Winner != nil {
		if k := sortedKeys(t, raw.Winner); !slices.Equal(k, []string{"id", "name"}) {
			t.Fatalf("winner keys = %v, want only id and name", k)
		}
	}
	sum := r.BlankVotes
	for _, l := range r.Lists {
		sum += l.Votes
	}
	if sum != r.Voted {
		t.Fatalf("lists + blank = %d, want voted %d (S2)", sum, r.Voted)
	}
	return r
}

// wantPublished checks a published result: the lists in order (id, name, votes), blank, turnout,
// "Voto secreto", the outcome and the winner's list id ("" for none).
func wantPublished(t *testing.T, rec *httptest.ResponseRecorder, lists []resultList, blank, voted, electorate int, outcome, winner string) resultJSON {
	t.Helper()
	r := decodeResult(t, rec)
	if r.Status != "published" || r.Mode != "secret" {
		t.Fatalf("status, mode = %q, %q; want published, secret", r.Status, r.Mode)
	}
	if !slices.Equal(r.Lists, lists) || r.BlankVotes != blank || r.Voted != voted || r.Electorate != electorate || r.Outcome != outcome {
		t.Fatalf("result = %+v %d blank, %d of %d, %s; want %+v %d blank, %d of %d, %s",
			r.Lists, r.BlankVotes, r.Voted, r.Electorate, r.Outcome, lists, blank, voted, electorate, outcome)
	}
	switch {
	case winner == "" && r.Winner != nil:
		t.Fatalf("winner = %+v, want none", r.Winner)
	case winner != "" && (r.Winner == nil || r.Winner.ID != winner):
		t.Fatalf("winner = %+v, want list %s", r.Winner, winner)
	case winner != "":
		for _, l := range lists {
			if l.ID == winner && l.Name != r.Winner.Name {
				t.Fatalf("winner name = %q, want %q", r.Winner.Name, l.Name)
			}
		}
	}
	return r
}

// seedRoster adds fictitious roster members to tenant until it has exactly size of them.
func seedRoster(t *testing.T, ctx context.Context, tenant string, size int) {
	t.Helper()
	if _, err := tdb.Super.Exec(ctx, `
		INSERT INTO roster_members (tenant_id, full_name, document_number)
		SELECT $1, 'Votante Ficticio ' || g, (90000000 + g)::text
		FROM generate_series((SELECT count(*) FROM roster_members WHERE tenant_id = $1) + 1, $2) g`, tenant, size); err != nil {
		t.Fatal(err)
	}
	if n := rosterSize(t, ctx, tenant); n != size {
		t.Fatalf("roster = %d, want %d", n, size)
	}
}

// seedBallots stores, as superuser, n Ballots for list ("" for a Blank vote) of election, cast by
// members of its Electorate who had not voted yet: their Voter registry rows and pending Ballots, as
// election_ballot_cast writes them. With no snapshot yet it first takes it from the roster, as the
// first access after the start does. Casting hundreds of votes through HTTP would need an account
// per voter; the vote itself is covered by election_votes_test.go.
func seedBallots(t *testing.T, ctx context.Context, tenant, election, list string, n int) {
	t.Helper()
	if _, err := tdb.Super.Exec(ctx, `
		WITH roster AS (SELECT id FROM roster_members WHERE tenant_id = $1),
		     marker AS (
		         INSERT INTO election_electorates (tenant_id, election_id, size)
		         SELECT $1, $2, (SELECT count(*) FROM roster)
		         WHERE NOT EXISTS (SELECT FROM election_electorates WHERE tenant_id = $1 AND election_id = $2)
		         RETURNING election_id)
		INSERT INTO election_electorate_members (tenant_id, election_id, roster_member_id)
		SELECT $1, $2, roster.id FROM roster, marker`, tenant, election); err != nil {
		t.Fatal(err)
	}
	tag, err := tdb.Super.Exec(ctx, `
		WITH voters AS (
		    INSERT INTO election_voters (tenant_id, election_id, roster_member_id)
		    SELECT $1, $2, m.roster_member_id FROM election_electorate_members m
		    WHERE m.tenant_id = $1 AND m.election_id = $2
		      AND NOT EXISTS (SELECT FROM election_voters x WHERE x.tenant_id = $1 AND x.election_id = $2 AND x.roster_member_id = m.roster_member_id)
		    LIMIT $3
		    RETURNING 1)
		INSERT INTO election_ballot_pending (tenant_id, election_id, list_id)
		SELECT $1, $2, nullif($4, '')::uuid FROM voters`, tenant, election, n, list)
	if err != nil {
		t.Fatal(err)
	}
	if int(tag.RowsAffected()) != n {
		t.Fatalf("seeded %d ballots, want %d (not enough electorate members left)", tag.RowsAffected(), n)
	}
}

// closedSchool is a voteSchool with a third approved list (Lista Roja), a roster of 200 and the given
// Ballots per list ("blue", "green", "red" or "blank"), with the clock at the close; it returns the
// school and a student session opened at the close.
func closedSchool(t *testing.T, ctx context.Context, ballots map[string]int) (*voteSchool, string, string) {
	t.Helper()
	v := newVoteSchool(t, ctx)
	rep := seedMember(t, ctx, v.tenant, "Rosa Apoderada", "30333190")
	red := seedList(t, ctx, v.tenant, v.electionID, "Lista Roja", rep, "approved")
	seedRoster(t, ctx, v.tenant, 200)
	ids := map[string]string{"blue": v.blue, "green": v.green, "red": red, "blank": ""}
	for _, k := range []string{"blue", "green", "red", "blank"} {
		if n := ballots[k]; n > 0 {
			seedBallots(t, ctx, v.tenant, v.electionID, ids[k], n)
		}
	}
	v.clk.t = vEnd
	_, tok := v.signIn(t, ctx, "student")
	return v, tok, red
}

// S1: before the end (an instant before it) the result is 409 results_not_available with no
// count; from the end itself (inclusive) it is published, with no background process.
func TestResultsArePublishedFromTheEnd(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	carlaTok := v.loginOK(t, carla.email)
	wantVoteError(t, v.results(carlaTok, v.electionID), http.StatusConflict, "results_not_available")
	v.clk.t = vStart
	wantVoted(t, v.vote(carlaTok, v.electionID, voteBody(v.blue)))
	wantVoteError(t, v.results(carlaTok, v.electionID), http.StatusConflict, "results_not_available")
	v.clk.t = vEnd.Add(-time.Nanosecond)
	wantVoteError(t, v.results(carlaTok, v.electionID), http.StatusConflict, "results_not_available")

	v.clk.t = vEnd
	size := rosterSize(t, ctx, v.tenant)
	wantPublished(t, v.results(carlaTok, v.electionID),
		[]resultList{{v.blue, "Lista Azul", 1}, {v.green, "Lista Verde", 0}}, 0, 1, size, "winner", v.blue)
}

// S1, S5: an app clock at the end while the database clock is still before it (the dates are ahead
// of the real clock) gets 409 results_not_available, never a partial tally, and the box stays as cast.
func TestResultsWaitForTheDatabaseClock(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	e := decodeElection(t, s.create(s.member, encode(draft())), http.StatusCreated)
	rep := seedMember(t, ctx, s.tenant, "Rita Apoderada", "30333100")
	blue := seedList(t, ctx, s.tenant, e.ID, "Lista Azul", rep, "approved")
	v := &voteSchool{school: s, electionID: e.ID, blue: blue}
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	v.clk.t = exStart.Add(-time.Minute)
	carlaTok := v.loginOK(t, carla.email)
	v.clk.t = exStart
	wantVoted(t, v.vote(carlaTok, e.ID, voteBody(blue)))

	v.clk.t = exEnd
	wantVoteError(t, v.results(carlaTok, e.ID), http.StatusConflict, "results_not_available")
	var boxed int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM election_ballots WHERE election_id = $1`, e.ID).Scan(&boxed); err != nil || boxed != 0 {
		t.Fatalf("ballots in the box = %d (%v), want 0 before the database clock reaches the end", boxed, err)
	}
	wantBox(t, ctx, e.ID, 1, carla.member)
}

// S2: Electorate 200; Lista Azul 70, Lista Verde 50, blank 10 → 130 of 200 voted; an approved list
// with 0 votes is listed; pending and rejected lists are not; lists plus blank add up to X.
func TestResultsShowVotesPerListBlankAndTurnout(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	rep := seedMember(t, ctx, v.tenant, "Rosa Apoderada", "30333190")
	sky := seedList(t, ctx, v.tenant, v.electionID, "Lista Celeste", rep, "approved")
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	carlaTok := v.loginOK(t, carla.email)
	seedRoster(t, ctx, v.tenant, 200)
	v.clk.t = vStart
	// One real vote through the API takes the snapshot of the 200; the rest are seeded.
	wantVoted(t, v.vote(carlaTok, v.electionID, voteBody(v.blue)))
	seedBallots(t, ctx, v.tenant, v.electionID, v.blue, 69)
	seedBallots(t, ctx, v.tenant, v.electionID, v.green, 50)
	seedBallots(t, ctx, v.tenant, v.electionID, "", 10)

	v.clk.t = vEnd
	r := wantPublished(t, v.results(carlaTok, v.electionID), []resultList{
		{v.blue, "Lista Azul", 70}, {v.green, "Lista Verde", 50}, {sky, "Lista Celeste", 0},
	}, 10, 130, 200, "winner", v.blue)
	for _, l := range r.Lists {
		if l.ID == v.pending || l.ID == v.rejected || l.ID == v.otherElectionList {
			t.Fatalf("lists = %+v, want only the approved lists of the election", r.Lists)
		}
	}
	wantBox(t, ctx, v.electionID, 130, registry(t, ctx, v.electionID)...)
}

// S3: the list with most votes wins and a Blank vote never does; there is no quorum.
func TestResultsSimpleMajorityBlankNeverWins(t *testing.T) {
	ctx := integration(t)
	for _, tc := range []struct {
		name               string
		blue, green, blank int
	}{
		{"Azul 3, blank 10", 3, 0, 10},
		{"Azul 70, Verde 50", 70, 50, 0},
		{"1 vote of 200 for Azul", 1, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, tok, red := closedSchool(t, ctx, map[string]int{"blue": tc.blue, "green": tc.green, "blank": tc.blank})
			lists := []resultList{{v.blue, "Lista Azul", tc.blue}, {v.green, "Lista Verde", tc.green}, {red, "Lista Roja", 0}}
			if tc.green == 0 {
				lists = []resultList{{v.blue, "Lista Azul", tc.blue}, {red, "Lista Roja", 0}, {v.green, "Lista Verde", 0}}
			}
			wantPublished(t, v.results(tok, v.electionID), lists, tc.blank, tc.blue+tc.green+tc.blank, 200, "winner", v.blue)
		})
	}
}

// S4: a tie for first place (Azul 40, Verde 40, Roja 10) has no winner; with every list at 0, even
// with Blank votes, there is no winner either; neither names a list as winner.
func TestResultsTieAndNoVotesHaveNoWinner(t *testing.T) {
	ctx := integration(t)
	t.Run("tie", func(t *testing.T) {
		v, tok, red := closedSchool(t, ctx, map[string]int{"blue": 40, "green": 40, "red": 10})
		wantPublished(t, v.results(tok, v.electionID),
			[]resultList{{v.blue, "Lista Azul", 40}, {v.green, "Lista Verde", 40}, {red, "Lista Roja", 10}}, 0, 90, 200, "tie", "")
	})
	t.Run("only blank votes", func(t *testing.T) {
		v, tok, red := closedSchool(t, ctx, map[string]int{"blank": 5})
		wantPublished(t, v.results(tok, v.electionID),
			[]resultList{{v.blue, "Lista Azul", 0}, {red, "Lista Roja", 0}, {v.green, "Lista Verde", 0}}, 5, 5, 200, "no_votes", "")
	})
	t.Run("no votes at all", func(t *testing.T) {
		v, tok, red := closedSchool(t, ctx, nil)
		wantPublished(t, v.results(tok, v.electionID),
			[]resultList{{v.blue, "Lista Azul", 0}, {red, "Lista Roja", 0}, {v.green, "Lista Verde", 0}}, 0, 0, 200, "no_votes", "")
	})
}

// S5: two simultaneous reads right after the close return exactly the same result, and so does a
// read days later; a late vote is refused and changes nothing. Nothing is stored, so a read that
// fails halfway leaves no partial result behind.
func TestResultsAreStable(t *testing.T) {
	ctx := integration(t)
	v, tok, red := closedSchool(t, ctx, map[string]int{"blue": 7, "green": 5, "blank": 2})
	_, other := v.signIn(t, ctx, "school_admin")
	recs := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i, token := range []string{tok, other} {
		wg.Go(func() { recs[i] = v.results(token, v.electionID) })
	}
	wg.Wait()
	wantPublished(t, recs[0], []resultList{{v.blue, "Lista Azul", 7}, {v.green, "Lista Verde", 5}, {red, "Lista Roja", 0}}, 2, 14, 200, "winner", v.blue)
	if !bytes.Equal(recs[0].Body.Bytes(), recs[1].Body.Bytes()) {
		t.Fatalf("simultaneous reads differ:\n%s\n%s", recs[0].Body, recs[1].Body)
	}

	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	wantVoteError(t, v.vote(v.loginOK(t, carla.email), v.electionID, voteBody(v.blue)), http.StatusConflict, "election_not_open")
	v.clk.t = vEnd.Add(72 * time.Hour)
	seedRoster(t, ctx, v.tenant, 210) // the roster changes after the close; the result does not
	_, later := v.signIn(t, ctx, "student")
	if rec := v.results(later, v.electionID); rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), recs[0].Body.Bytes()) {
		t.Fatalf("read days later = %d %s, want exactly %s", rec.Code, rec.Body, recs[0].Body)
	}
}

// S6: the published result says "Voto secreto".
func TestResultsSayTheModeIsSecret(t *testing.T) {
	ctx := integration(t)
	v, tok, _ := closedSchool(t, ctx, map[string]int{"blue": 1})
	if r := decodeResult(t, v.results(tok, v.electionID)); r.Mode != "secret" {
		t.Fatalf("mode = %q, want secret", r.Mode)
	}
}

// S7: the result carries nothing that identifies who voted or what each one voted: only the keys
// of the contract, and none of the voters' ids, emails or the Ballots' ids in the box.
func TestResultsCarryNothingAboutVoters(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	dario := v.voter(t, ctx, "Dario Ficticio", "30333102")
	carlaTok, darioTok := v.loginOK(t, carla.email), v.loginOK(t, dario.email)
	v.clk.t = vStart
	wantVoted(t, v.vote(carlaTok, v.electionID, voteBody(v.blue)))
	wantVoted(t, v.vote(darioTok, v.electionID, voteBody("blank")))
	v.clk.t = vEnd
	rec := v.results(carlaTok, v.electionID)
	wantPublished(t, rec, []resultList{{v.blue, "Lista Azul", 1}, {v.green, "Lista Verde", 0}}, 1, 2, rosterSize(t, ctx, v.tenant), "winner", v.blue)

	var ballotIDs []string
	if err := tdb.Super.QueryRow(ctx, `SELECT coalesce(array_agg(id::text), '{}') FROM election_ballots WHERE election_id = $1`, v.electionID).Scan(&ballotIDs); err != nil {
		t.Fatal(err)
	}
	if len(ballotIDs) != 2 {
		t.Fatalf("ballots in the box = %d, want 2", len(ballotIDs))
	}
	body := strings.ToLower(rec.Body.String())
	for _, secret := range append(ballotIDs, carla.userID, carla.member, carla.email, dario.userID, dario.member, dario.email, "carla", "dario", "30333101") {
		if strings.Contains(body, strings.ToLower(secret)) {
			t.Fatalf("result carries %q: %s", secret, rec.Body)
		}
	}
}

// S8: every member of the school sees the same result (student, Officer, commission member, school
// admin); no membership → 403 forbidden; no session → 401 unauthenticated; an election of another
// school, or an id that is not one, → 404 not_found with no count.
func TestResultsWhoSeesThem(t *testing.T) {
	ctx := integration(t)
	v, studentTok, _ := closedSchool(t, ctx, map[string]int{"blue": 2, "green": 1})
	foreign, _ := foreignElection(t, ctx)
	_, officerTok := v.signIn(t, ctx, "officer")
	_, adminTok := v.signIn(t, ctx, "school_admin")
	_, outsiderTok := v.signIn(t, ctx, "")
	commissionTok := v.rosterLogin(t, ctx, v.ana)

	// The officer reads first: the first read publishes the result, and Lista Azul (with no
	// candidates) becomes the Board, so the officer would no longer be one (#58 S1).
	want := v.results(officerTok, v.electionID)
	decodeResult(t, want)
	for _, tc := range []struct{ name, token string }{
		{"student", studentTok}, {"commission member", commissionTok}, {"school admin", adminTok},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := v.results(tc.token, v.electionID)
			decodeResult(t, rec)
			if !bytes.Equal(rec.Body.Bytes(), want.Body.Bytes()) {
				t.Fatalf("result = %s, want %s", rec.Body, want.Body)
			}
		})
	}
	wantVoteError(t, v.results(outsiderTok, v.electionID), http.StatusForbidden, "forbidden")
	wantVoteError(t, v.results("", v.electionID), http.StatusUnauthorized, "unauthenticated")
	wantVoteError(t, v.results(studentTok, foreign), http.StatusNotFound, "not_found")
	wantVoteError(t, v.results(studentTok, "not-an-election"), http.StatusNotFound, "not_found")
	wantBox(t, ctx, foreign, 0)
	if n := snapshots(t, ctx, foreign); n != 0 {
		t.Fatalf("foreign snapshots = %d, want 0", n)
	}
}

// S9: an election whose voting never opened for lack of approved lists publishes at the end
// "Sin ganador: no hubo listas aprobadas", 0 of Y, Y being the Electorate the close takes from the
// roster; it stays the same when the roster changes later.
func TestResultsWithoutApprovedLists(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	if _, err := tdb.Super.Exec(ctx, `UPDATE election_lists SET status = 'pending' WHERE election_id = $1 AND status = 'approved'`, v.electionID); err != nil {
		t.Fatal(err)
	}
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	carlaTok := v.loginOK(t, carla.email)
	v.clk.t = vStart
	wantVoteError(t, v.turnout(carlaTok, v.electionID), http.StatusConflict, "no_approved_lists")
	v.clk.t = vEnd
	size := rosterSize(t, ctx, v.tenant)
	rec := v.results(carlaTok, v.electionID)
	wantPublished(t, rec, []resultList{}, 0, 0, size, "no_lists", "")
	v.voter(t, ctx, "Gabi Tarde", "30333109")
	if again := v.results(carlaTok, v.electionID); !bytes.Equal(again.Body.Bytes(), rec.Body.Bytes()) {
		t.Fatalf("result after a roster change = %s, want %s", again.Body, rec.Body)
	}
}

// S10: an annulled election publishes no result: before the end 409 results_not_available, from
// the end "anulada" with its reason and no count.
func TestResultsOfAnAnnulledElection(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	carlaTok := v.loginOK(t, carla.email)
	v.clk.t = vStart
	wantVoted(t, v.vote(carlaTok, v.electionID, voteBody(v.blue)))
	if _, err := tdb.Super.Exec(ctx, `UPDATE elections SET annulled_at = $2, annulment_reason = $3 WHERE id = $1`,
		v.electionID, v.clk.Now(), exampleReason); err != nil {
		t.Fatal(err)
	}
	v.clk.t = vEnd.Add(-time.Minute)
	wantVoteError(t, v.results(carlaTok, v.electionID), http.StatusConflict, "results_not_available")
	v.clk.t = vEnd
	r := decodeResult(t, v.results(carlaTok, v.electionID))
	if r.Status != "annulled" || r.AnnulmentReason != exampleReason {
		t.Fatalf("result = %+v, want annulled with reason %q", r, exampleReason)
	}
}

// S1 (#57 follow-up): the result is published at the close with no background process, so from the
// end on, and without anyone reading the result, the school's next election can be created; an
// instant before the end the election is still in progress. The closed election keeps its result
// and is stored as published at its end. #58 S6: that publication ends the commission's mandate, so
// its member gets 403 forbidden (and nothing is written); a new commission is designated and creates
// the next election.
func TestNextElectionAfterTheClose(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	carlaTok := v.loginOK(t, carla.email)
	v.clk.t = vStart
	wantVoted(t, v.vote(carlaTok, v.electionID, voteBody(v.blue)))

	v.clk.t = vEnd.Add(-time.Nanosecond)
	wantResponse(t, v.create(v.member, encode(draft())), http.StatusConflict, `{"error":"election_in_progress"}`)

	v.clk.t = vEnd
	wantResponse(t, v.create(v.member, encode(draft())), http.StatusForbidden, forbiddenBody)
	var unpublished bool
	if err := tdb.Super.QueryRow(ctx, `SELECT published_at IS NULL FROM elections WHERE id = $1`, v.electionID).Scan(&unpublished); err != nil || !unpublished {
		t.Fatalf("published_at IS NULL: %t (%v), want the refused create to write nothing", unpublished, err)
	}
	electorate := rosterSize(t, ctx, v.tenant)
	_, newMember, dora := v.student(t, ctx, "Dora Ficticia", "30333150")
	// Lista Azul won with no candidates, so the Board is empty and the school admin designates (ADR 0003).
	decodeCommission(t, v.designate(v.admin, dora))
	newMember, _ = v.confirm(t, newMember, v.enroll(t, newMember))
	next := decodeElection(t, v.create(newMember, encode(draft())), http.StatusCreated)
	for _, path := range []string{currentPath, latestPath} {
		if got := decodeElection(t, v.get(carlaTok, path), http.StatusOK); got.ID != next.ID {
			t.Fatalf("%s = %s, want the next election %s", path, got.ID, next.ID)
		}
	}
	if got := decodeElection(t, v.get(carlaTok, electionsPath+"/"+v.electionID), http.StatusOK); got.State != "closed" {
		t.Fatalf("closed election state = %q, want closed", got.State)
	}
	var published bool
	if err := tdb.Super.QueryRow(ctx, `SELECT published_at = ends_at FROM elections WHERE id = $1`, v.electionID).Scan(&published); err != nil || !published {
		t.Fatalf("published_at = ends_at: %t (%v), want true", published, err)
	}
	wantPublished(t, v.results(carlaTok, v.electionID),
		[]resultList{{v.blue, "Lista Azul", 1}, {v.green, "Lista Verde", 0}}, 0, 1, electorate, "winner", v.blue)
}
