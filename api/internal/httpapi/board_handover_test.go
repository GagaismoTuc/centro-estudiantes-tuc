package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"
)

// handoverSchool is a school (see newSchool: Beto Ficticio is Presidente, a legacy officer account
// designated Ana's commission) whose election has positions on the 2025 voting day (see
// newVoteSchool), with two approved lists (Lista Azul and Lista Verde) and a pending one. The clock
// stands one minute before the start.
func handoverSchool(t *testing.T, ctx context.Context, positions []string) *voteSchool {
	t.Helper()
	s := newSchool(t, ctx)
	s.clk.t = vSubStart.Add(-24 * time.Hour)
	e := decodeElection(t, s.create(s.member, encode(with(draft(),
		"submissionStartsAt", vSubStart.Format(time.RFC3339), "submissionEndsAt", vSubEnd.Format(time.RFC3339),
		"startsAt", vStart.Format(time.RFC3339), "endsAt", vEnd.Format(time.RFC3339), "positions", positions))), http.StatusCreated)
	v := &voteSchool{school: s, electionID: e.ID}
	rep := seedMember(t, ctx, s.tenant, "Rita Apoderada", "30333100")
	v.blue = seedList(t, ctx, s.tenant, e.ID, "Lista Azul", rep, "approved")
	v.green = seedList(t, ctx, s.tenant, e.ID, "Lista Verde", rep, "approved")
	v.pending = seedList(t, ctx, s.tenant, e.ID, "Lista Gris", rep, "pending")
	v.clk.t = vStart.Add(-time.Minute)
	return v
}

// seedCandidates stores members as the candidates of list, in the order of the election's positions.
func seedCandidates(t *testing.T, ctx context.Context, tenant, election, list string, members ...string) {
	t.Helper()
	for i, m := range members {
		if _, err := tdb.Super.Exec(ctx, `
			INSERT INTO election_list_candidates (tenant_id, election_id, list_id, position_order, candidate_id)
			VALUES ($1, $2, $3, $4, $5)`, tenant, election, list, i+1, m); err != nil {
			t.Fatal(err)
		}
	}
}

// publish closes the election (clock at its end) and reads its result as a student, which publishes it.
func (v *voteSchool) publish(t *testing.T, ctx context.Context) resultJSON {
	t.Helper()
	v.clk.t = vEnd
	_, tok := v.signIn(t, ctx, "student")
	return decodeResult(t, v.results(tok, v.electionID))
}

// board reads the "Miembros" screen as the school admin: "name|position" of every row with the
// officer role, sorted; a row without a name (an account with no roster entry) shows as "account|".
func (v *voteSchool) board(t *testing.T) []string {
	t.Helper()
	rec := v.do(http.MethodGet, "/api/members", "", electionIP, v.admin, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("members = %d %s", rec.Code, rec.Body)
	}
	var rows []struct {
		FullName *string  `json:"fullName"`
		Position *string  `json:"position"`
		Roles    []string `json:"roles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, r := range rows {
		if !slices.Contains(r.Roles, "officer") {
			continue
		}
		name, pos := "account", ""
		if r.FullName != nil {
			name = *r.FullName
		}
		if r.Position != nil {
			pos = *r.Position
		}
		out = append(out, name+"|"+pos)
	}
	slices.Sort(out)
	return out
}

// storedBoard reads the tenant's Board as superuser: "position:roster member:name" of every
// officer_positions row and "legacy:user" of every membership with role officer, sorted.
func storedBoard(t *testing.T, ctx context.Context, tenant string) []string {
	t.Helper()
	var out []string
	if err := tdb.Super.QueryRow(ctx, `
		SELECT coalesce(array_agg(x ORDER BY x), '{}') FROM (
			SELECT 'position:' || p.roster_member_id::text || ':' || p.position AS x FROM officer_positions p WHERE p.tenant_id = $1
			UNION ALL
			SELECT 'legacy:' || m.user_id::text FROM memberships m WHERE m.tenant_id = $1 AND m.role = 'officer') b`, tenant).Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// publication reads, as superuser, whether the election is published at its end, its winner list
// ("" for none) and whether its commission's mandate ended.
func publication(t *testing.T, ctx context.Context, election string) (published bool, winner string, commissionEnded bool) {
	t.Helper()
	if err := tdb.Super.QueryRow(ctx, `
		SELECT e.published_at IS NOT DISTINCT FROM e.ends_at AND e.published_at IS NOT NULL, coalesce(e.winner_list_id::text, ''), c.ended_at IS NOT NULL
		FROM elections e JOIN electoral_commissions c ON c.tenant_id = e.tenant_id AND c.id = e.commission_id
		WHERE e.id = $1`, election).Scan(&published, &winner, &commissionEnded); err != nil {
		t.Fatal(err)
	}
	return published, winner, commissionEnded
}

type meRoles struct {
	Roles []string `json:"roles"`
	Role  string   `json:"role"`
	MFA   string   `json:"mfa"`
}

// me reads GET /api/auth/me; a single role comes as "role" only, and is returned in Roles too.
func (v *voteSchool) me(t *testing.T, token string) meRoles {
	t.Helper()
	rec := v.do(http.MethodGet, "/api/auth/me", "", electionIP, token, nil)
	var m meRoles
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &m) != nil {
		t.Fatalf("me = %d %s", rec.Code, rec.Body)
	}
	if len(m.Roles) == 0 {
		m.Roles = []string{m.Role}
	}
	return m
}

// S1 (A18): once the result is published, the winning list's candidates are the Board with the
// positions of that election (not necessarily the four default ones), and nobody else is an Officer.
func TestWinningListBecomesTheBoard(t *testing.T) {
	ctx := integration(t)
	t.Run("the example", func(t *testing.T) {
		v := handoverSchool(t, ctx, defaultPositions)
		ana := seedMember(t, ctx, v.tenant, "Ana Ficticia", "30444001")
		ciro := seedMember(t, ctx, v.tenant, "Ciro Inventado", "30444002")
		dana := seedMember(t, ctx, v.tenant, "Dana Ejemplo", "30444003")
		eli := seedMember(t, ctx, v.tenant, "Eli Supuesto", "30444004")
		seedCandidates(t, ctx, v.tenant, v.electionID, v.blue, ana, ciro, dana, eli)
		seedCandidates(t, ctx, v.tenant, v.electionID, v.green,
			seedMember(t, ctx, v.tenant, "Fede Verde", "30444005"), seedMember(t, ctx, v.tenant, "Gabi Verde", "30444006"),
			seedMember(t, ctx, v.tenant, "Hugo Verde", "30444007"), seedMember(t, ctx, v.tenant, "Iris Verde", "30444008"))
		seedBallots(t, ctx, v.tenant, v.electionID, v.blue, 3)
		seedBallots(t, ctx, v.tenant, v.electionID, v.green, 1)
		if got, want := v.board(t), []string{"Beto Ficticio|Presidente", "account|"}; !slices.Equal(got, want) {
			t.Fatalf("board before = %v, want %v", got, want)
		}

		if r := v.publish(t, ctx); r.Outcome != "winner" || r.Winner == nil || r.Winner.ID != v.blue {
			t.Fatalf("result = %+v, want Lista Azul as winner", r)
		}
		want := []string{"Ana Ficticia|Presidente", "Ciro Inventado|Vicepresidente", "Dana Ejemplo|Secretario", "Eli Supuesto|Tesorero"}
		if got := v.board(t); !slices.Equal(got, want) {
			t.Fatalf("board after = %v, want %v", got, want)
		}
		wantStored := []string{"position:" + ana + ":Presidente", "position:" + ciro + ":Vicepresidente",
			"position:" + dana + ":Secretario", "position:" + eli + ":Tesorero"}
		slices.Sort(wantStored)
		if got := storedBoard(t, ctx, v.tenant); !slices.Equal(got, wantStored) {
			t.Fatalf("stored board = %v, want %v", got, wantStored)
		}
		if published, winner, _ := publication(t, ctx, v.electionID); !published || winner != v.blue {
			t.Fatalf("published = %t, winner = %q; want published at the end with %s", published, winner, v.blue)
		}
	})
	t.Run("positions of the election", func(t *testing.T) {
		v := handoverSchool(t, ctx, []string{"Presidenta", "Vocal titular"})
		lu := seedMember(t, ctx, v.tenant, "Lu Ficticia", "30444011")
		mo := seedMember(t, ctx, v.tenant, "Mo Inventado", "30444012")
		seedCandidates(t, ctx, v.tenant, v.electionID, v.blue, lu, mo)
		seedBallots(t, ctx, v.tenant, v.electionID, v.blue, 1)
		v.publish(t, ctx)
		if got, want := v.board(t), []string{"Lu Ficticia|Presidenta", "Mo Inventado|Vocal titular"}; !slices.Equal(got, want) {
			t.Fatalf("board after = %v, want %v", got, want)
		}
	})
}

// S2 (ADR 0002): an outgoing Officer keeps the Student role and votes in a later election; an
// outgoing Officer on the winning list holds the new position.
func TestOutgoingOfficersStayStudents(t *testing.T) {
	ctx := integration(t)
	v := handoverSchool(t, ctx, defaultPositions)
	_, _, gabi := v.student(t, ctx, "Gabi Saliente", "30444021")
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO officer_positions (tenant_id, roster_member_id, position) VALUES ($1, $2, 'Tesorero')`,
		v.tenant, gabi); err != nil {
		t.Fatal(err)
	}
	beto := v.presidenteMember(t, ctx)
	_, _, ciro := v.student(t, ctx, "Ciro Inventado", "30444022")
	dana := seedMember(t, ctx, v.tenant, "Dana Ejemplo", "30444023")
	eli := seedMember(t, ctx, v.tenant, "Eli Supuesto", "30444024")
	seedCandidates(t, ctx, v.tenant, v.electionID, v.blue, ciro, dana, beto, eli)
	seedBallots(t, ctx, v.tenant, v.electionID, v.blue, 2)
	v.publish(t, ctx)

	want := []string{"Beto Ficticio|Secretario", "Ciro Inventado|Presidente", "Dana Ejemplo|Vicepresidente", "Eli Supuesto|Tesorero"}
	if got := v.board(t); !slices.Equal(got, want) {
		t.Fatalf("board = %v, want %v", got, want)
	}
	gabiTok := v.rosterLogin(t, ctx, gabi)
	if m := v.me(t, gabiTok); !slices.Equal(m.Roles, []string{"student"}) || m.MFA != "not_required" {
		t.Fatalf("outgoing officer me = %+v, want only student, no second factor", m)
	}
	if m := v.me(t, v.rosterLogin(t, ctx, beto)); !slices.Equal(m.Roles, []string{"student", "officer"}) {
		t.Fatalf("re-elected officer me = %+v, want student and officer", m)
	}

	// A later election: the new Presidente designates a new commission, which creates it; the
	// outgoing Tesorero votes in it as a Student.
	ciroTok := v.rosterLogin(t, ctx, ciro)
	ciroTok, _ = v.confirm(t, ciroTok, v.enroll(t, ciroTok))
	v.clk.Advance(30 * time.Second)
	decodeCommission(t, v.designate(ciroTok, v.ana))
	next := decodeElection(t, v.create(v.member, encode(with(draft(),
		"submissionStartsAt", time.Date(2025, time.April, 1, 0, 0, 0, 0, art).Format(time.RFC3339),
		"submissionEndsAt", time.Date(2025, time.April, 6, 0, 0, 0, 0, art).Format(time.RFC3339),
		"startsAt", time.Date(2025, time.April, 10, 8, 0, 0, 0, art).Format(time.RFC3339),
		"endsAt", time.Date(2025, time.April, 10, 18, 0, 0, 0, art).Format(time.RFC3339)))), http.StatusCreated)
	rep := seedMember(t, ctx, v.tenant, "Rosa Apoderada", "30444029")
	nextList := seedList(t, ctx, v.tenant, next.ID, "Lista Celeste", rep, "approved")
	v.clk.t = time.Date(2025, time.April, 10, 8, 0, 0, 0, art)
	wantVoted(t, v.vote(v.rosterLogin(t, ctx, gabi), next.ID, voteBody(nextList)))
}

// S3: a winning candidate without an account holds the position on their roster entry, and the
// Officer role applies when they claim their access code.
func TestWinningCandidateWithoutAccountIsOfficerOnClaim(t *testing.T) {
	ctx := integration(t)
	v := handoverSchool(t, ctx, []string{"Presidente", "Vicepresidente"})
	ciro := seedMember(t, ctx, v.tenant, "Ciro Inventado", "30444031")
	dana := seedMember(t, ctx, v.tenant, "Dana Ejemplo", "30444032")
	seedCandidates(t, ctx, v.tenant, v.electionID, v.blue, ciro, dana)
	seedBallots(t, ctx, v.tenant, v.electionID, v.blue, 1)
	v.publish(t, ctx)
	if got, want := v.board(t), []string{"Ciro Inventado|Presidente", "Dana Ejemplo|Vicepresidente"}; !slices.Equal(got, want) {
		t.Fatalf("board = %v, want %v", got, want)
	}

	rec := v.claim(seedCode(t, ctx, v.tenant, dana, "active", v.clk.Now()), uniqueEmail("dana"), password, "192.0.2.1")
	var got meRoles
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("claim = %d %s", rec.Code, rec.Body)
	}
	if !slices.Equal(got.Roles, []string{"student", "officer"}) || got.MFA != "enrollment_required" {
		t.Fatalf("claim = %+v, want student and officer with enrollment required", got)
	}
	if m := v.me(t, cookieToken(t, rec)); !slices.Equal(m.Roles, []string{"student", "officer"}) {
		t.Fatalf("me after claim = %+v", m)
	}
}

// S4 (A17): with a tie or "Sin ganador" (no votes, no approved lists) the Board stays exactly as it was.
func TestNoWinnerLeavesTheBoardAsItWas(t *testing.T) {
	ctx := integration(t)
	for _, tc := range []struct {
		name, outcome string
		setup         func(t *testing.T, v *voteSchool)
	}{
		{"tie", "tie", func(t *testing.T, v *voteSchool) {
			seedBallots(t, ctx, v.tenant, v.electionID, v.blue, 2)
			seedBallots(t, ctx, v.tenant, v.electionID, v.green, 2)
		}},
		{"no votes for any list", "no_votes", func(t *testing.T, v *voteSchool) {
			seedBallots(t, ctx, v.tenant, v.electionID, "", 3)
		}},
		{"no approved lists", "no_lists", func(t *testing.T, v *voteSchool) {
			if _, err := tdb.Super.Exec(ctx, `UPDATE election_lists SET status = 'pending' WHERE election_id = $1`, v.electionID); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := handoverSchool(t, ctx, defaultPositions)
			seedCandidates(t, ctx, v.tenant, v.electionID, v.blue, seedMember(t, ctx, v.tenant, "Ana Ficticia", "30444041"))
			seedCandidates(t, ctx, v.tenant, v.electionID, v.green, seedMember(t, ctx, v.tenant, "Ciro Inventado", "30444042"))
			tc.setup(t, v)
			boardBefore, storedBefore := v.board(t), storedBoard(t, ctx, v.tenant)
			if r := v.publish(t, ctx); r.Outcome != tc.outcome {
				t.Fatalf("outcome = %q, want %q", r.Outcome, tc.outcome)
			}
			if got := v.board(t); !slices.Equal(got, boardBefore) {
				t.Fatalf("board = %v, want unchanged %v", got, boardBefore)
			}
			if got := storedBoard(t, ctx, v.tenant); !slices.Equal(got, storedBefore) {
				t.Fatalf("stored board = %v, want unchanged %v", got, storedBefore)
			}
			if published, winner, _ := publication(t, ctx, v.electionID); !published || winner != "" {
				t.Fatalf("published = %t, winner = %q; want published with no winner", published, winner)
			}
		})
	}
}

// S5: two simultaneous reads right after the close make a single handover, and reading again
// changes nothing.
func TestHandoverHappensOnce(t *testing.T) {
	ctx := integration(t)
	v := handoverSchool(t, ctx, []string{"Presidente", "Vicepresidente"})
	ciro := seedMember(t, ctx, v.tenant, "Ciro Inventado", "30444051")
	dana := seedMember(t, ctx, v.tenant, "Dana Ejemplo", "30444052")
	seedCandidates(t, ctx, v.tenant, v.electionID, v.blue, ciro, dana)
	seedBallots(t, ctx, v.tenant, v.electionID, v.blue, 1)
	v.clk.t = vEnd
	_, a := v.signIn(t, ctx, "student")
	_, b := v.signIn(t, ctx, "student")
	recs := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i, token := range []string{a, b} {
		wg.Go(func() { recs[i] = v.results(token, v.electionID) })
	}
	wg.Wait()
	decodeResult(t, recs[0])
	if recs[1].Code != http.StatusOK || !bytes.Equal(recs[0].Body.Bytes(), recs[1].Body.Bytes()) {
		t.Fatalf("simultaneous reads differ:\n%d %s\n%d %s", recs[0].Code, recs[0].Body, recs[1].Code, recs[1].Body)
	}
	want := []string{"position:" + ciro + ":Presidente", "position:" + dana + ":Vicepresidente"}
	slices.Sort(want)
	if got := storedBoard(t, ctx, v.tenant); !slices.Equal(got, want) {
		t.Fatalf("stored board = %v, want %v", got, want)
	}
	var stamps []time.Time
	if err := tdb.Super.QueryRow(ctx, `
		SELECT ARRAY[e.published_at, c.ended_at] FROM elections e
		JOIN electoral_commissions c ON c.tenant_id = e.tenant_id AND c.id = e.commission_id WHERE e.id = $1`, v.electionID).Scan(&stamps); err != nil {
		t.Fatal(err)
	}

	v.clk.Advance(72 * time.Hour)
	_, later := v.signIn(t, ctx, "student")
	if rec := v.results(later, v.electionID); rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), recs[0].Body.Bytes()) {
		t.Fatalf("read again = %d %s, want %s", rec.Code, rec.Body, recs[0].Body)
	}
	if got := storedBoard(t, ctx, v.tenant); !slices.Equal(got, want) {
		t.Fatalf("stored board after rereading = %v, want %v", got, want)
	}
	var again []time.Time
	if err := tdb.Super.QueryRow(ctx, `
		SELECT ARRAY[e.published_at, c.ended_at] FROM elections e
		JOIN electoral_commissions c ON c.tenant_id = e.tenant_id AND c.id = e.commission_id WHERE e.id = $1`, v.electionID).Scan(&again); err != nil {
		t.Fatal(err)
	}
	if !slices.EqualFunc(stamps, again, time.Time.Equal) {
		t.Fatalf("published_at, ended_at = %v after rereading, want %v", again, stamps)
	}
}

// S6 (A3): once the result is published, with or without a winner, the commission's members can no
// longer edit, approve lists, annul nor create elections (403 forbidden); the Officers designate a
// new commission for the next election.
func TestCommissionMandateEndsWithThePublication(t *testing.T) {
	ctx := integration(t)
	for _, tc := range []struct {
		name   string
		winner bool
	}{{"with a winner", true}, {"without a winner", false}} {
		t.Run(tc.name, func(t *testing.T) {
			v := handoverSchool(t, ctx, []string{"Presidente"})
			_, _, ciro := v.student(t, ctx, "Ciro Inventado", "30444061")
			seedCandidates(t, ctx, v.tenant, v.electionID, v.blue, ciro)
			if tc.winner {
				seedBallots(t, ctx, v.tenant, v.electionID, v.blue, 1)
			}
			v.publish(t, ctx)
			if _, _, ended := publication(t, ctx, v.electionID); !ended {
				t.Fatal("commission mandate not ended by the publication")
			}
			before := storedMembers(t, ctx, v.tenant)
			for _, rec := range []*httptest.ResponseRecorder{
				v.edit(v.member, v.electionID, encode(draft())),
				v.do(http.MethodPost, electionsPath+"/"+v.electionID+"/lists/"+v.pending+"/approve", "", electionIP, v.member, nil),
				v.annul(v.member, v.electionID, reasonBody(exampleReason)),
				v.create(v.member, encode(draft())),
			} {
				wantResponse(t, rec, http.StatusForbidden, forbiddenBody)
			}
			var pending string
			if err := tdb.Super.QueryRow(ctx, `SELECT status FROM election_lists WHERE id = $1`, v.pending).Scan(&pending); err != nil || pending != "pending" {
				t.Fatalf("pending list status = %q (%v), want pending", pending, err)
			}
			var elections int
			if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM elections WHERE tenant_id = $1 AND annulled_at IS NULL`, v.tenant).Scan(&elections); err != nil || elections != 1 {
				t.Fatalf("elections = %d (%v), want only the published one", elections, err)
			}
			if got := storedMembers(t, ctx, v.tenant); !slices.Equal(got, before) {
				t.Fatalf("commission = %v, want %v", got, before)
			}

			// The Officers (the new Board, or the old one without a winner) designate a new commission.
			designator := v.officer
			if tc.winner {
				tok := v.rosterLogin(t, ctx, ciro)
				designator, _ = v.confirm(t, tok, v.enroll(t, tok))
				v.clk.Advance(30 * time.Second)
				wantResponse(t, v.designate(v.officer, v.ana), http.StatusForbidden, forbiddenBody)
			}
			decodeCommission(t, v.designate(designator, v.ana))
			wantStoredMembers(t, ctx, v.tenant, v.ana)
			var current, ended int
			if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FILTER (WHERE ended_at IS NULL), count(*) FILTER (WHERE ended_at IS NOT NULL)
				FROM electoral_commissions WHERE tenant_id = $1`, v.tenant).Scan(&current, &ended); err != nil || current != 1 || ended != 1 {
				t.Fatalf("commissions current = %d, ended = %d (%v); want a new one and the ended one", current, ended, err)
			}
		})
	}
}

// S8 (A20, ADR 0001 2FA rules 1 and 5): a winning candidate's Officer operations demand the second
// factor like any Officer's: enrollment when they have none, verification when they have one.
func TestNewOfficersNeedTheSecondFactor(t *testing.T) {
	ctx := integration(t)
	v := handoverSchool(t, ctx, []string{"Presidente", "Vicepresidente"})
	_, ciroTok, ciro := v.student(t, ctx, "Ciro Inventado", "30444071")
	_, danaTok, dana := v.student(t, ctx, "Dana Ejemplo", "30444072")
	v.confirm(t, danaTok, v.enroll(t, danaTok))
	if m := v.me(t, ciroTok); !slices.Equal(m.Roles, []string{"student"}) || m.MFA != "not_required" {
		t.Fatalf("candidate before = %+v, want a student with no second factor", m)
	}
	seedCandidates(t, ctx, v.tenant, v.electionID, v.blue, ciro, dana)
	seedBallots(t, ctx, v.tenant, v.electionID, v.blue, 1)
	v.publish(t, ctx)

	ciroTok = v.rosterLogin(t, ctx, ciro)
	if m := v.me(t, ciroTok); !slices.Equal(m.Roles, []string{"student", "officer"}) || m.MFA != "enrollment_required" {
		t.Fatalf("new officer without TOTP = %+v, want officer with enrollment required", m)
	}
	wantResponse(t, v.designate(ciroTok, v.ana), http.StatusForbidden, mfaBody)
	danaTok = v.rosterLogin(t, ctx, dana)
	if m := v.me(t, danaTok); m.MFA != "pending" {
		t.Fatalf("new officer with TOTP = %+v, want pending", m)
	}
	wantResponse(t, v.designate(danaTok, v.ana), http.StatusForbidden, mfaBody)
	var current int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM electoral_commissions WHERE tenant_id = $1 AND ended_at IS NULL`, v.tenant).Scan(&current); err != nil || current != 0 {
		t.Fatalf("current commissions = %d (%v), want none designated", current, err)
	}

	ciroTok, _ = v.confirm(t, ciroTok, v.enroll(t, ciroTok))
	decodeCommission(t, v.designate(ciroTok, v.ana))
}

// S9 (ADR 0001 rules 1, 4 and 10): the handover only touches the election's school; another
// school's Board, commission and election stay as they were.
func TestHandoverTouchesOnlyItsSchool(t *testing.T) {
	ctx := integration(t)
	other := handoverSchool(t, ctx, defaultPositions)
	seedCandidates(t, ctx, other.tenant, other.electionID, other.blue, seedMember(t, ctx, other.tenant, "Otra Candidata", "30444081"))
	v := handoverSchool(t, ctx, defaultPositions)
	seedCandidates(t, ctx, v.tenant, v.electionID, v.blue, seedMember(t, ctx, v.tenant, "Ana Ficticia", "30444082"))
	seedBallots(t, ctx, v.tenant, v.electionID, v.blue, 1)
	other.clk.t = vEnd
	otherBoard, otherStored := other.board(t), storedBoard(t, ctx, other.tenant)
	otherCommission := storedMembers(t, ctx, other.tenant)

	v.publish(t, ctx)
	if got, want := v.board(t), []string{"Ana Ficticia|Presidente"}; !slices.Equal(got, want) {
		t.Fatalf("board = %v, want %v", got, want)
	}
	if got := other.board(t); !slices.Equal(got, otherBoard) {
		t.Fatalf("other school's board = %v, want unchanged %v", got, otherBoard)
	}
	if got := storedBoard(t, ctx, other.tenant); !slices.Equal(got, otherStored) {
		t.Fatalf("other school's stored board = %v, want unchanged %v", got, otherStored)
	}
	if got := storedMembers(t, ctx, other.tenant); !slices.Equal(got, otherCommission) {
		t.Fatalf("other school's commission = %v, want unchanged %v", got, otherCommission)
	}
	if published, winner, ended := publication(t, ctx, other.electionID); published || winner != "" || ended {
		t.Fatalf("other school's election: published = %t, winner = %q, commission ended = %t; want untouched", published, winner, ended)
	}
}
