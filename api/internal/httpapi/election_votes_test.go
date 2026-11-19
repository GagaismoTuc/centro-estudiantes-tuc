package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// voteIP is the client address of every vote request; S11 checks it never reaches the logs.
const voteIP = "198.51.100.56"

// The S1 example moved to 2025: the database refuses to flush the box of an election whose end is
// still ahead of its own clock, so the voting day must be in the past for the real clock too.
var (
	vSubStart = time.Date(2025, time.March, 1, 0, 0, 0, 0, art)
	vSubEnd   = time.Date(2025, time.March, 6, 0, 0, 0, 0, art)
	vStart    = time.Date(2025, time.March, 10, 8, 0, 0, 0, art)
	vEnd      = time.Date(2025, time.March, 10, 18, 0, 0, 0, art)
)

// voteSchool is a school (see newSchool) whose election is the S1 example (voting 10 March 2025,
// 08:00 to 18:00 Argentina time) with two approved lists, a pending one and a rejected one, plus an
// earlier annulled election of the same school with its own approved list. The clock stands at
// 07:59 of the voting day, so sessions opened from here last past the end.
type voteSchool struct {
	*school
	electionID                     string
	blue, green, pending, rejected string
	otherElectionList              string
}

// voterAccount is a roster member with a student account linked to it.
type voterAccount struct{ userID, email, member string }

func newVoteSchool(t *testing.T, ctx context.Context) *voteSchool {
	t.Helper()
	s := newSchool(t, ctx)
	s.clk.t = vSubStart.Add(-24 * time.Hour) // the app clock goes back so the dates are still ahead
	e := decodeElection(t, s.create(s.member, encode(with(draft(),
		"submissionStartsAt", vSubStart.Format(time.RFC3339), "submissionEndsAt", vSubEnd.Format(time.RFC3339),
		"startsAt", vStart.Format(time.RFC3339), "endsAt", vEnd.Format(time.RFC3339)))), http.StatusCreated)
	v := &voteSchool{school: s, electionID: e.ID}
	rep := seedMember(t, ctx, s.tenant, "Rita Apoderada", "30333100")
	v.blue = seedList(t, ctx, s.tenant, e.ID, "Lista Azul", rep, "approved")
	v.green = seedList(t, ctx, s.tenant, e.ID, "Lista Verde", rep, "approved")
	v.pending = seedList(t, ctx, s.tenant, e.ID, "Lista Gris", rep, "pending")
	v.rejected = seedList(t, ctx, s.tenant, e.ID, "Lista Roja", rep, "rejected")
	var earlier string
	if err := tdb.Super.QueryRow(ctx, `
		INSERT INTO elections (tenant_id, commission_id, name, mode, submission_starts_at, submission_ends_at, starts_at, ends_at,
		                       annulled_at, annulment_reason)
		SELECT tenant_id, commission_id, 'Elección 2024', 'secret', $2, $3, $4, $5, $3, 'Se detectó un error en el padrón cargado'
		FROM elections WHERE id = $1 RETURNING id::text`, e.ID,
		time.Date(2024, time.September, 1, 0, 0, 0, 0, art), time.Date(2024, time.September, 6, 0, 0, 0, 0, art),
		time.Date(2024, time.September, 10, 8, 0, 0, 0, art), time.Date(2024, time.September, 10, 18, 0, 0, 0, art)).Scan(&earlier); err != nil {
		t.Fatal(err)
	}
	v.otherElectionList = seedList(t, ctx, s.tenant, earlier, "Lista Azul", rep, "approved")
	v.clk.t = vStart.Add(-time.Minute)
	return v
}

// seedList stores a list of election as superuser with status "approved", "pending" or "rejected".
func seedList(t *testing.T, ctx context.Context, tenant, election, name, rep, status string) string {
	t.Helper()
	var id string
	if err := tdb.Super.QueryRow(ctx, `
		INSERT INTO election_lists (tenant_id, election_id, name, representative_id, status, is_active, rejection_reason)
		VALUES ($1, $2, $3, $4, $5::text, $5::text <> 'rejected', CASE WHEN $5::text = 'rejected' THEN 'Fuera de reglamento' END)
		RETURNING id::text`, tenant, election, name, rep, status).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// voter creates a roster member with a student account; sign in with loginOK(a.email).
func (v *voteSchool) voter(t *testing.T, ctx context.Context, name, doc string) voterAccount {
	t.Helper()
	a := voterAccount{member: seedMember(t, ctx, v.tenant, name, doc), email: uniqueEmail("voter")}
	a.userID = v.seedUser(t, ctx, a.email, "student")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id = $1 WHERE tenant_id = $2 AND user_id = $3`,
		a.member, v.tenant, a.userID); err != nil {
		t.Fatal(err)
	}
	return a
}

// rosterLogin signs in again, at the current clock, the account linked to roster member.
func (v *voteSchool) rosterLogin(t *testing.T, ctx context.Context, member string) string {
	t.Helper()
	var email string
	if err := tdb.Super.QueryRow(ctx, `
		SELECT u.email FROM users u JOIN memberships m ON m.user_id = u.id
		WHERE m.tenant_id = $1 AND m.roster_member_id = $2`, v.tenant, member).Scan(&email); err != nil {
		t.Fatal(err)
	}
	return v.loginOK(t, email)
}

// presidenteMember is Beto's roster entry: a student who is an Officer (Presidente).
func (v *voteSchool) presidenteMember(t *testing.T, ctx context.Context) string {
	t.Helper()
	var id string
	if err := tdb.Super.QueryRow(ctx, `SELECT roster_member_id::text FROM officer_positions WHERE tenant_id = $1 AND position = 'Presidente'`,
		v.tenant).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func voteBody(choice any) string { return encode(map[string]any{"choice": choice}) }

func (v *voteSchool) vote(token, election, body string) *httptest.ResponseRecorder {
	return v.do(http.MethodPost, electionsPath+"/"+election+"/vote", body, voteIP, token, nil)
}

func (v *voteSchool) turnout(token, election string) *httptest.ResponseRecorder {
	return v.do(http.MethodGet, electionsPath+"/"+election+"/turnout", "", voteIP, token, nil)
}

func (v *voteSchool) results(token, election string) *httptest.ResponseRecorder {
	return v.do(http.MethodGet, electionsPath+"/"+election+"/results", "", voteIP, token, nil)
}

func wantVoted(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("vote = %d %s, want 204 with no body", rec.Code, rec.Body)
	}
	wantNoStore(t, rec)
}

// wantVoteError checks the status, the code and Cache-Control of a refused vote, turnout or results.
func wantVoteError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	wantResponse(t, rec, status, fmt.Sprintf(`{"error":%q}`, code))
	wantNoStore(t, rec)
}

// box counts the Ballots of election as superuser: pending ones and those already in the box.
func box(t *testing.T, ctx context.Context, election string) int {
	t.Helper()
	var n int
	if err := tdb.Super.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM election_ballot_pending WHERE election_id = $1)
		     + (SELECT count(*) FROM election_ballots WHERE election_id = $1)`, election).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// registry reads the Voter registry of election as superuser, sorted.
func registry(t *testing.T, ctx context.Context, election string) []string {
	t.Helper()
	var ids []string
	if err := tdb.Super.QueryRow(ctx, `
		SELECT coalesce(array_agg(roster_member_id::text ORDER BY roster_member_id::text), '{}')
		FROM election_voters WHERE election_id = $1`, election).Scan(&ids); err != nil {
		t.Fatal(err)
	}
	return ids
}

func snapshots(t *testing.T, ctx context.Context, election string) int {
	t.Helper()
	var n int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM election_electorates WHERE election_id = $1`, election).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// wantBox checks the number of Ballots and the Voter registry (members, any order) of election.
func wantBox(t *testing.T, ctx context.Context, election string, ballots int, voters ...string) {
	t.Helper()
	want := slices.Clone(voters)
	if want == nil {
		want = []string{}
	}
	slices.Sort(want)
	if n, got := box(t, ctx, election), registry(t, ctx, election); n != ballots || !slices.Equal(got, want) {
		t.Fatalf("box = %d ballots, registry = %v; want %d, %v", n, got, ballots, want)
	}
}

func rosterSize(t *testing.T, ctx context.Context, tenant string) int {
	t.Helper()
	var n int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM roster_members WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// turnoutJSON decodes a 200 turnout and checks it has exactly voted, electorate and hasVoted.
func turnoutJSON(t *testing.T, rec *httptest.ResponseRecorder) (voted, electorate int, hasVoted bool) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("turnout = %d %s, want 200", rec.Code, rec.Body)
	}
	wantNoStore(t, rec)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	keys := slices.Sorted(func(yield func(string) bool) {
		for k := range raw {
			if !yield(k) {
				return
			}
		}
	})
	if !slices.Equal(keys, []string{"electorate", "hasVoted", "voted"}) {
		t.Fatalf("turnout keys = %v, want only electorate, hasVoted and voted", keys)
	}
	var out struct {
		Voted, Electorate int
		HasVoted          bool
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Voted, out.Electorate, out.HasVoted
}

func wantTurnout(t *testing.T, rec *httptest.ResponseRecorder, voted, electorate int, hasVoted bool) {
	t.Helper()
	if v, e, h := turnoutJSON(t, rec); v != voted || e != electorate || h != hasVoted {
		t.Fatalf("turnout = %d of %d (hasVoted %t), want %d of %d (hasVoted %t)", v, e, h, voted, electorate, hasVoted)
	}
}

// foreignElection stores, as superuser, another school with an election open at the same time as
// the S1 example and one approved list; it returns the election and the list.
func foreignElection(t *testing.T, ctx context.Context) (election, list string) {
	t.Helper()
	tenant := newTenant(t, ctx)
	rep := seedMember(t, ctx, tenant, "Otra Apoderada", "40111222")
	if err := tdb.Super.QueryRow(ctx, `
		WITH c AS (INSERT INTO electoral_commissions (tenant_id) VALUES ($1) RETURNING id)
		INSERT INTO elections (tenant_id, commission_id, name, mode, submission_starts_at, submission_ends_at, starts_at, ends_at)
		SELECT $1, c.id, 'Elección 2025', 'secret', $2, $3, $4, $5 FROM c RETURNING id::text`,
		tenant, vSubStart, vSubEnd, vStart, vEnd).Scan(&election); err != nil {
		t.Fatal(err)
	}
	return election, seedList(t, ctx, tenant, election, "Lista Azul", rep, "approved")
}

// installBallotFault makes every Ballot write of election fail inside the vote's transaction, after
// its Voter registry row was written (S9), until the returned func removes it.
func installBallotFault(t *testing.T, ctx context.Context, election string) func() {
	t.Helper()
	name := "test_ballot_fault_" + strings.ReplaceAll(election, "-", "")
	if _, err := tdb.Super.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %[1]s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced ballot write failure'; END $$;
		CREATE TRIGGER %[1]s BEFORE INSERT ON election_ballot_pending FOR EACH ROW
		    WHEN (NEW.election_id = '%[2]s') EXECUTE FUNCTION %[1]s();`, name, election)); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	remove := func() {
		once.Do(func() {
			if _, err := tdb.Super.Exec(context.Background(), fmt.Sprintf(
				`DROP TRIGGER %[1]s ON election_ballot_pending; DROP FUNCTION %[1]s();`, name)); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(remove)
	return remove
}

// S1: votes count from StartsAt (inclusive) until EndsAt (exclusive): 07:59 and 18:00 are refused
// with 409 election_not_open writing nothing, 08:00 is accepted.
func TestVoteOpensAndClosesByTheClock(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	dario := v.voter(t, ctx, "Dario Ficticio", "30333102")
	carlaTok, darioTok := v.loginOK(t, carla.email), v.loginOK(t, dario.email)

	wantVoteError(t, v.vote(carlaTok, v.electionID, voteBody(v.blue)), http.StatusConflict, "election_not_open")
	wantBox(t, ctx, v.electionID, 0)
	if n := snapshots(t, ctx, v.electionID); n != 0 {
		t.Fatalf("snapshots at 07:59 = %d, want 0", n)
	}

	v.clk.t = vStart
	wantVoted(t, v.vote(carlaTok, v.electionID, voteBody(v.blue)))
	wantBox(t, ctx, v.electionID, 1, carla.member)

	v.clk.t = vEnd
	wantVoteError(t, v.vote(darioTok, v.electionID, voteBody(v.blue)), http.StatusConflict, "election_not_open")
	wantBox(t, ctx, v.electionID, 1, carla.member)
}

// S2: the Electorate is the roster at the first access after the start, taken once even by two
// simultaneous first accesses; a student added afterwards gets 403 not_in_electorate, and Y stays.
func TestVoteElectorateIsTheRosterWhenVotingOpens(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	dario := v.voter(t, ctx, "Dario Ficticio", "30333102")
	carlaTok, darioTok := v.loginOK(t, carla.email), v.loginOK(t, dario.email)
	v.clk.t = vStart
	size := rosterSize(t, ctx, v.tenant)

	recs := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i, tok := range []string{carlaTok, darioTok} {
		wg.Go(func() { recs[i] = v.turnout(tok, v.electionID) })
	}
	wg.Wait()
	for _, rec := range recs {
		wantTurnout(t, rec, 0, size, false)
	}
	if n := snapshots(t, ctx, v.electionID); n != 1 {
		t.Fatalf("snapshots = %d, want exactly 1", n)
	}

	late := v.voter(t, ctx, "Gabi Tarde", "30333109")
	lateTok := v.loginOK(t, late.email)
	wantVoteError(t, v.vote(lateTok, v.electionID, voteBody(v.blue)), http.StatusForbidden, "not_in_electorate")
	wantBox(t, ctx, v.electionID, 0)
	wantTurnout(t, v.turnout(lateTok, v.electionID), 0, size, false)

	wantVoted(t, v.vote(carlaTok, v.electionID, voteBody(v.blue)))
	wantTurnout(t, v.turnout(carlaTok, v.electionID), 1, size, true)
}

// S3: with no approved list at the start, voting does not open: 409 no_approved_lists on vote and
// turnout, writing nothing.
func TestVoteWithoutApprovedListsDoesNotOpen(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	if _, err := tdb.Super.Exec(ctx, `UPDATE election_lists SET status = 'pending' WHERE election_id = $1 AND status = 'approved'`, v.electionID); err != nil {
		t.Fatal(err)
	}
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	carlaTok := v.loginOK(t, carla.email)
	v.clk.t = vStart
	wantVoteError(t, v.vote(carlaTok, v.electionID, voteBody("blank")), http.StatusConflict, "no_approved_lists")
	wantVoteError(t, v.turnout(carlaTok, v.electionID), http.StatusConflict, "no_approved_lists")
	wantBox(t, ctx, v.electionID, 0)
	if n := snapshots(t, ctx, v.electionID); n != 0 {
		t.Fatalf("snapshots = %d, want 0", n)
	}
}

// electionRow reads what PUT /elections/{id} can change, as superuser.
func electionRow(t *testing.T, ctx context.Context, id string) string {
	t.Helper()
	var row string
	if err := tdb.Super.QueryRow(ctx, `
		SELECT e.name || '|' || e.starts_at || '|' || e.ends_at || '|' || e.updated_at || '|' ||
		       (SELECT string_agg(name, ',' ORDER BY position_order) FROM election_positions p WHERE p.tenant_id = e.tenant_id AND p.election_id = e.id)
		FROM elections e WHERE e.id = $1`, id).Scan(&row); err != nil {
		t.Fatal(err)
	}
	return row
}

// S4: from StartsAt on, editing the election (and its positions), presenting, loading, editing,
// approving or rejecting a list and replacing the commission all get 409 election_frozen with no
// change. One minute before the start the previous codes stay (PRESERVE).
func TestElectionConfigurationFrozenFromTheStart(t *testing.T) {
	ctx := integration(t)
	ls := newListSchool(t, ctx)
	pending := decodeList(t, ls.do(http.MethodPost, ls.listsPath(), listBody("Lista Azul", candidatesPayload(ls.betoID, ls.ciroID, ls.eliID, ls.fitoID)),
		electionIP, ls.betoTok, nil), http.StatusCreated)
	approved := decodeList(t, ls.do(http.MethodPost, ls.listsPath(), listBody("Lista Verde", candidatesPayload(ls.danaID, ls.gabiID, ls.hugoID, ls.irisID)),
		electionIP, ls.danaTok, nil), http.StatusCreated)
	decodeList(t, ls.do(http.MethodPost, ls.listPath(approved.ID)+"/approve", "", electionIP, ls.commTok, nil), http.StatusOK)
	other := seedMember(t, ctx, ls.tenant, "Otro Alumno", "30222120")
	start := ls.subEnd.Add(time.Hour)

	listsBefore := ls.snapshot(t, ctx)
	electionBefore := electionRow(t, ctx, ls.electionID)
	commissionBefore := storedMembers(t, ctx, ls.tenant)
	edited := encode(with(draft(), "name", "Elección cambiada", "positions", []string{"Presidente", "Vocal"}))
	submitBody := listBody("Lista Tarde", candidatesPayload(other, ls.gabiID, ls.hugoID, ls.irisID))
	commBody := encode(map[string]any{"name": "Lista Comision", "representativeRosterMemberId": other,
		"candidates": candidatesPayload(other, ls.ciroID, ls.eliID, ls.fitoID)})
	editBody := listBody("Editada", candidatesPayload(ls.betoID, ls.fitoID, ls.ciroID, ls.eliID))

	check := func(t *testing.T, election, list string) {
		t.Helper()
		for _, tc := range []struct {
			name, method, path, body, token, code string
		}{
			{"edit election", http.MethodPut, electionsPath + "/" + ls.electionID, edited, ls.commTok, election},
			{"present list", http.MethodPost, ls.listsPath(), submitBody, ls.betoTok, list},
			{"commission loads list", http.MethodPost, ls.commissionListsPath(), commBody, ls.commTok, list},
			{"representative edits list", http.MethodPut, ls.listPath(pending.ID), editBody, ls.betoTok, list},
			{"commission edits list", http.MethodPut, ls.listPath(pending.ID), editBody, ls.commTok, list},
			{"approve list", http.MethodPost, ls.listPath(pending.ID) + "/approve", "", ls.commTok, list},
			{"reject list", http.MethodPost, ls.listPath(pending.ID) + "/reject", `{"reason":"Fuera de fecha"}`, ls.commTok, list},
			{"reject approved list", http.MethodPost, ls.listPath(approved.ID) + "/reject", `{"reason":"Fuera de fecha"}`, ls.commTok, list},
		} {
			rec := ls.do(tc.method, tc.path, tc.body, electionIP, tc.token, nil)
			if rec.Code != http.StatusConflict || strings.TrimSpace(rec.Body.String()) != fmt.Sprintf(`{"error":%q}`, tc.code) {
				t.Errorf("%s = %d %s, want 409 %s", tc.name, rec.Code, rec.Body, tc.code)
			}
		}
		ls.assertSnapshotEqual(t, ctx, listsBefore, "frozen configuration")
		if got := electionRow(t, ctx, ls.electionID); got != electionBefore {
			t.Fatalf("election = %s, want unchanged %s", got, electionBefore)
		}
	}

	t.Run("one minute before the start the previous codes stay", func(t *testing.T) {
		ls.clk.t = start.Add(-time.Minute)
		check(t, "election_not_draft", "submission_closed")
	})
	for _, at := range []struct {
		name string
		t    time.Time
	}{{"at the start", start}, {"while open", start.Add(30 * time.Minute)}, {"after the end", start.Add(2 * time.Hour)}} {
		t.Run(at.name, func(t *testing.T) {
			ls.clk.t = at.t
			check(t, "election_frozen", "election_frozen")
			wantResponse(t, ls.designate(ls.officer, ls.ana, ls.betoID), http.StatusConflict, `{"error":"election_frozen"}`)
			if got := storedMembers(t, ctx, ls.tenant); !slices.Equal(got, commissionBefore) {
				t.Fatalf("commission = %v, want unchanged %v", got, commissionBefore)
			}
		})
	}
}

// S5: the choice is an approved list of the election or "blank"; a rejected or pending list, a
// list of another election, empty, null or missing get 422 invalid_choice; a list or election of
// another school gets 404 not_found. None of them changes the box or the registry.
func TestVoteChoices(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	foreign, foreignList := foreignElection(t, ctx)
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	dario := v.voter(t, ctx, "Dario Ficticio", "30333102")
	carlaTok, darioTok := v.loginOK(t, carla.email), v.loginOK(t, dario.email)
	v.clk.t = vStart

	for _, tc := range []struct {
		name, election, body, code string
		status                     int
	}{
		{"rejected list", v.electionID, voteBody(v.rejected), "invalid_choice", http.StatusUnprocessableEntity},
		{"pending list", v.electionID, voteBody(v.pending), "invalid_choice", http.StatusUnprocessableEntity},
		{"list of another election", v.electionID, voteBody(v.otherElectionList), "invalid_choice", http.StatusUnprocessableEntity},
		{"empty", v.electionID, voteBody(""), "invalid_choice", http.StatusUnprocessableEntity},
		{"null", v.electionID, voteBody(nil), "invalid_choice", http.StatusUnprocessableEntity},
		{"missing", v.electionID, `{}`, "invalid_choice", http.StatusUnprocessableEntity},
		{"neither a list nor blank", v.electionID, voteBody("en blanco"), "invalid_choice", http.StatusUnprocessableEntity},
		{"list of another school", v.electionID, voteBody(foreignList), "not_found", http.StatusNotFound},
		{"unknown list", v.electionID, voteBody("00000000-0000-4000-8000-000000000000"), "not_found", http.StatusNotFound},
		{"election of another school", foreign, voteBody(foreignList), "not_found", http.StatusNotFound},
		{"election id not a uuid", "elección", voteBody(v.blue), "not_found", http.StatusNotFound},
		{"unknown field", v.electionID, `{"choice":"blank","extra":1}`, "bad_request", http.StatusBadRequest},
		{"choice not a string", v.electionID, `{"choice":1}`, "bad_request", http.StatusBadRequest},
		{"malformed JSON", v.electionID, `{"choice":`, "bad_request", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantVoteError(t, v.vote(carlaTok, tc.election, tc.body), tc.status, tc.code)
			wantBox(t, ctx, v.electionID, 0)
			wantBox(t, ctx, foreign, 0)
		})
	}

	wantVoted(t, v.vote(carlaTok, v.electionID, voteBody("blank")))
	wantVoted(t, v.vote(darioTok, v.electionID, voteBody(v.blue)))
	wantBox(t, ctx, v.electionID, 2, carla.member, dario.member)
	var blank, blue int
	if err := tdb.Super.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE list_id IS NULL), count(*) FILTER (WHERE list_id = $2)
		FROM (SELECT list_id FROM election_ballot_pending WHERE election_id = $1
		      UNION ALL SELECT list_id FROM election_ballots WHERE election_id = $1) b`, v.electionID, v.blue).Scan(&blank, &blue); err != nil {
		t.Fatal(err)
	}
	if blank != 1 || blue != 1 {
		t.Fatalf("ballots: %d blank, %d Lista Azul; want 1 and 1", blank, blue)
	}
}

// S6: a second vote of the same student gets 409 already_voted and the box does not change; Ana
// Ficticia, Student and Officer, votes once; of two simultaneous votes of one student exactly one
// is accepted and the box grows by one.
func TestVoteOncePerStudent(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO officer_positions (tenant_id, roster_member_id, position) VALUES ($1, $2, 'Secretario')`,
		v.tenant, v.ana); err != nil {
		t.Fatal(err)
	}
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	anaTok, carlaTok := v.rosterLogin(t, ctx, v.ana), v.loginOK(t, carla.email)
	v.clk.t = vStart

	wantVoted(t, v.vote(anaTok, v.electionID, voteBody(v.blue)))
	wantVoteError(t, v.vote(anaTok, v.electionID, voteBody(v.green)), http.StatusConflict, "already_voted")
	wantVoteError(t, v.vote(anaTok, v.electionID, voteBody(v.blue)), http.StatusConflict, "already_voted")
	wantBox(t, ctx, v.electionID, 1, v.ana)

	recs := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i := range recs {
		wg.Go(func() { recs[i] = v.vote(carlaTok, v.electionID, voteBody(v.green)) })
	}
	wg.Wait()
	slices.SortFunc(recs, func(a, b *httptest.ResponseRecorder) int { return a.Code - b.Code })
	wantVoted(t, recs[0])
	wantVoteError(t, recs[1], http.StatusConflict, "already_voted")
	wantBox(t, ctx, v.electionID, 2, v.ana, carla.member)
}

// S7: only an account linked to a student of the Electorate votes: a student, a commission member
// and an Officer who is a student vote; a school admin or an officer without a roster entry gets
// 403 not_in_electorate, an account without membership 403 forbidden, no session 401.
func TestVoteWhoVotes(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	beto := v.presidenteMember(t, ctx)
	carlaTok, anaTok, betoTok := v.loginOK(t, carla.email), v.rosterLogin(t, ctx, v.ana), v.rosterLogin(t, ctx, beto)
	_, adminTok := v.signIn(t, ctx, "school_admin")
	_, officerTok := v.signIn(t, ctx, "officer")
	_, outsiderTok := v.signIn(t, ctx, "")
	v.clk.t = vStart

	for _, tc := range []struct {
		name, token, code string
		status            int
	}{
		{"school admin without a roster entry", adminTok, "not_in_electorate", http.StatusForbidden},
		{"officer without a roster entry", officerTok, "not_in_electorate", http.StatusForbidden},
		{"account without membership", outsiderTok, "forbidden", http.StatusForbidden},
		{"no session", "", "unauthenticated", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantVoteError(t, v.vote(tc.token, v.electionID, voteBody(v.blue)), tc.status, tc.code)
			wantBox(t, ctx, v.electionID, 0)
		})
	}
	wantVoted(t, v.vote(carlaTok, v.electionID, voteBody(v.blue)))
	wantVoted(t, v.vote(anaTok, v.electionID, voteBody(v.green)))
	wantVoted(t, v.vote(betoTok, v.electionID, voteBody("blank")))
	wantBox(t, ctx, v.electionID, 3, carla.member, v.ana, beto)
}

// S8: Fede Ficticio, in the Electorate without an account, claims his access code with the existing
// flow at 10:00 and votes at 10:02 with the session the claim opened.
func TestStudentClaimsTheCodeDuringTheElectionAndVotes(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	fede := seedMember(t, ctx, v.tenant, "Fede Ficticio", "30333103")
	carlaTok := v.loginOK(t, carla.email)
	v.clk.t = vStart
	wantVoted(t, v.vote(carlaTok, v.electionID, voteBody(v.blue))) // the snapshot is taken here

	v.clk.t = time.Date(2025, time.March, 10, 10, 0, 0, 0, art)
	code := seedCode(t, ctx, v.tenant, fede, "active", v.clk.Now())
	email := uniqueEmail("fede")
	rec := v.claim(code, email, password, "192.0.2.77")
	userID, _ := userByEmail(t, ctx, email)
	wantResponse(t, rec, http.StatusCreated, meBody(userID, email, v.tenant, v.tenantName, "student", "not_required", false))
	fedeTok := cookieToken(t, rec)

	v.clk.t = v.clk.t.Add(2 * time.Minute)
	wantVoted(t, v.vote(fedeTok, v.electionID, voteBody(v.green)))
	wantBox(t, ctx, v.electionID, 2, carla.member, fede)
	if _, _, has := turnoutJSON(t, v.turnout(fedeTok, v.electionID)); !has {
		t.Fatal("Fede's turnout hasVoted = false, want true")
	}
}

// S9: when the Ballot write fails after the Voter registry row was written, neither stays, and the
// student can vote again.
func TestVoteFailureBetweenTheTwoWritesKeepsNeither(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	carlaTok := v.loginOK(t, carla.email)
	v.clk.t = vStart

	remove := installBallotFault(t, ctx, v.electionID)
	wantVoteError(t, v.vote(carlaTok, v.electionID, voteBody(v.blue)), http.StatusInternalServerError, "internal")
	wantBox(t, ctx, v.electionID, 0)
	remove()

	wantVoted(t, v.vote(carlaTok, v.electionID, voteBody(v.blue)))
	wantBox(t, ctx, v.electionID, 1, carla.member)
}

// S11: nothing the server logs while votes are cast (accepted, refused or failing) carries the
// chosen list, nor any identifier of the voter (account, roster member, email, IP).
func TestVoteLogsNeverLinkTheChoiceToTheVoter(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	dario := v.voter(t, ctx, "Dario Ficticio", "30333102")
	carlaTok, darioTok := v.loginOK(t, carla.email), v.loginOK(t, dario.email)
	v.clk.t = vStart

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	wantVoted(t, v.vote(carlaTok, v.electionID, voteBody(v.blue)))
	wantVoteError(t, v.vote(carlaTok, v.electionID, voteBody(v.blue)), http.StatusConflict, "already_voted")
	wantVoteError(t, v.vote(darioTok, v.electionID, voteBody(v.pending)), http.StatusUnprocessableEntity, "invalid_choice")
	remove := installBallotFault(t, ctx, v.electionID)
	wantVoteError(t, v.vote(darioTok, v.electionID, voteBody(v.green)), http.StatusInternalServerError, "internal")
	remove()

	got := logs.String()
	if !strings.Contains(got, "cast vote") {
		t.Fatalf("logs = %q, want the failed vote logged (capture works)", got)
	}
	for _, secret := range []string{v.blue, v.green, v.pending, carla.userID, carla.member, carla.email, dario.userID, dario.member, dario.email, voteIP} {
		if strings.Contains(strings.ToLower(got), strings.ToLower(secret)) {
			t.Fatalf("logs carry %q: %s", secret, got)
		}
	}
}

// S12: while open every member sees "X of Y voted" and whether they voted themselves, nothing
// else; the per-list votes get 409 results_not_available until the end publishes them (#57).
func TestTurnoutShowsOnlyHowManyVoted(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	dario := v.voter(t, ctx, "Dario Ficticio", "30333102")
	carlaTok, darioTok := v.loginOK(t, carla.email), v.loginOK(t, dario.email)
	_, adminTok := v.signIn(t, ctx, "school_admin")
	_, outsiderTok := v.signIn(t, ctx, "")

	wantVoteError(t, v.turnout(carlaTok, v.electionID), http.StatusConflict, "election_not_open")
	v.clk.t = vStart
	size := rosterSize(t, ctx, v.tenant)
	wantTurnout(t, v.turnout(carlaTok, v.electionID), 0, size, false)
	wantVoted(t, v.vote(carlaTok, v.electionID, voteBody(v.blue)))
	wantTurnout(t, v.turnout(carlaTok, v.electionID), 1, size, true)
	wantTurnout(t, v.turnout(darioTok, v.electionID), 1, size, false)
	wantTurnout(t, v.turnout(adminTok, v.electionID), 1, size, false)
	wantVoteError(t, v.turnout(outsiderTok, v.electionID), http.StatusForbidden, "forbidden")
	wantVoteError(t, v.turnout("", v.electionID), http.StatusUnauthorized, "unauthenticated")

	for _, tok := range []string{carlaTok, adminTok} {
		wantVoteError(t, v.results(tok, v.electionID), http.StatusConflict, "results_not_available")
	}
	wantVoteError(t, v.results(outsiderTok, v.electionID), http.StatusForbidden, "forbidden")
	wantVoteError(t, v.results("", v.electionID), http.StatusUnauthorized, "unauthenticated")

	v.clk.t = vEnd
	wantTurnout(t, v.turnout(darioTok, v.electionID), 1, size, false)
	wantPublished(t, v.results(carlaTok, v.electionID),
		[]resultList{{v.blue, "Lista Azul", 1}, {v.green, "Lista Verde", 0}}, 0, 1, size, "winner", v.blue)
}

// S12: turnout right at the close answers from the Voter registry even while the database clock is
// still before the end (app and database clocks apart, or a transaction begun just before the end):
// the box is written only once the database itself sees the election closed.
func TestTurnoutAtTheCloseBeforeTheDatabaseClock(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	// Dates ahead of the real clock: the database never sees this election closed.
	e := decodeElection(t, s.create(s.member, encode(draft())), http.StatusCreated)
	rep := seedMember(t, ctx, s.tenant, "Rita Apoderada", "30333100")
	blue := seedList(t, ctx, s.tenant, e.ID, "Lista Azul", rep, "approved")
	v := &voteSchool{school: s, electionID: e.ID, blue: blue}
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	dario := v.voter(t, ctx, "Dario Ficticio", "30333102")
	v.clk.t = exStart.Add(-time.Minute)
	carlaTok, darioTok := v.loginOK(t, carla.email), v.loginOK(t, dario.email)
	v.clk.t = exStart
	wantVoted(t, v.vote(carlaTok, e.ID, voteBody(blue)))
	size := rosterSize(t, ctx, s.tenant)

	v.clk.t = exEnd
	wantTurnout(t, v.turnout(carlaTok, e.ID), 1, size, true)
	wantTurnout(t, v.turnout(darioTok, e.ID), 1, size, false)
	wantBox(t, ctx, e.ID, 1, carla.member)
}

// S14: voting asks for no second factor: a student session votes, and so does an Officer who is a
// student with a session that has not verified the second factor it has enrolled.
func TestVoteNeedsNoSecondFactor(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	beto := v.presidenteMember(t, ctx)
	carlaTok, betoTok := v.loginOK(t, carla.email), v.rosterLogin(t, ctx, beto)
	v.clk.t = vStart
	if mfa, _ := meJSON(t, v.fixture, carlaTok); mfa != "not_required" {
		t.Fatalf("student mfa = %q, want not_required", mfa)
	}
	if mfa, _ := meJSON(t, v.fixture, betoTok); mfa != "pending" {
		t.Fatalf("officer mfa = %q, want pending", mfa)
	}
	wantVoted(t, v.vote(carlaTok, v.electionID, voteBody(v.blue)))
	wantVoted(t, v.vote(betoTok, v.electionID, voteBody(v.green)))
	wantBox(t, ctx, v.electionID, 2, carla.member, beto)
}

// S15: an election of another school is not found for vote, turnout and results, and its box and
// registry stay empty.
func TestVoteAndTurnoutOfAnotherSchoolAreNotFound(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	foreign, foreignList := foreignElection(t, ctx)
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	carlaTok := v.loginOK(t, carla.email)
	v.clk.t = vStart
	wantVoteError(t, v.vote(carlaTok, foreign, voteBody(foreignList)), http.StatusNotFound, "not_found")
	wantVoteError(t, v.vote(carlaTok, foreign, voteBody("blank")), http.StatusNotFound, "not_found")
	wantVoteError(t, v.turnout(carlaTok, foreign), http.StatusNotFound, "not_found")
	wantVoteError(t, v.results(carlaTok, foreign), http.StatusNotFound, "not_found")
	wantBox(t, ctx, foreign, 0)
	if n := snapshots(t, ctx, foreign); n != 0 {
		t.Fatalf("foreign snapshots = %d, want 0", n)
	}
}

// S17: on an annulled election a vote gets 409 election_annulled and the box does not change; its
// turnout is no longer shown (409 election_annulled).
func TestVoteOnAnAnnulledElection(t *testing.T) {
	ctx := integration(t)
	v := newVoteSchool(t, ctx)
	carla := v.voter(t, ctx, "Carla Ficticia", "30333101")
	dario := v.voter(t, ctx, "Dario Ficticio", "30333102")
	carlaTok, darioTok := v.loginOK(t, carla.email), v.loginOK(t, dario.email)
	v.clk.t = vStart
	wantVoted(t, v.vote(carlaTok, v.electionID, voteBody(v.blue)))
	if _, err := tdb.Super.Exec(ctx, `UPDATE elections SET annulled_at = $2, annulment_reason = $3 WHERE id = $1`,
		v.electionID, v.clk.Now(), exampleReason); err != nil {
		t.Fatal(err)
	}
	wantVoteError(t, v.vote(darioTok, v.electionID, voteBody(v.blue)), http.StatusConflict, "election_annulled")
	wantVoteError(t, v.vote(carlaTok, v.electionID, voteBody(v.blue)), http.StatusConflict, "election_annulled")
	wantBox(t, ctx, v.electionID, 1, carla.member)
	wantVoteError(t, v.turnout(carlaTok, v.electionID), http.StatusConflict, "election_annulled")
}
