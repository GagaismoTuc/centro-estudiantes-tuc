package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
)

type readProbe struct{ read bool }

func (p *readProbe) Read([]byte) (int, error) { p.read = true; return 0, io.EOF }

func TestOfficerPositionValidationAndUniqueness(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, token, _, _ := f.signInMFA(t, ctx, "school_admin")
	a := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	b := seedMember(t, ctx, f.tenant, "Beto Ficticio", "30111223")
	pathA, pathB := "/api/members/"+a+"/officer", "/api/members/"+b+"/officer"
	h := map[string]string{"Content-Type": "application/json"}
	wantResponse(t, f.do(http.MethodPost, pathA, `{"position":"Presidente"}`, "198.51.100.12", token, h), http.StatusCreated, "")
	wantResponse(t, f.do(http.MethodPost, pathA, `{"position":"Presidente"}`, "198.51.100.12", token, h), http.StatusConflict, `{"error":"already_officer"}`)
	wantResponse(t, f.do(http.MethodPost, pathA, `{"position":"Tesorero"}`, "198.51.100.12", token, h), http.StatusConflict, `{"error":"already_officer"}`)
	wantResponse(t, f.do(http.MethodPost, pathB, `{"position":"Presidente"}`, "198.51.100.12", token, h), http.StatusConflict, `{"error":"position_taken"}`)
	wantResponse(t, f.do(http.MethodPost, pathB, `{"position":"Otro"}`, "198.51.100.12", token, h), http.StatusUnprocessableEntity, `{"error":"invalid_position"}`)
	var count int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM officer_positions WHERE tenant_id=$1 AND roster_member_id=$2 AND position='Presidente'`, f.tenant, a).Scan(&count); err != nil || count != 1 {
		t.Fatalf("original position = %d: %v", count, err)
	}
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM officer_positions WHERE tenant_id=$1 AND roster_member_id=$2`, f.tenant, b).Scan(&count); err != nil || count != 0 {
		t.Fatalf("other positions = %d: %v", count, err)
	}
	for _, position := range []string{"Vicepresidente", "Secretario", "Tesorero"} {
		body, err := json.Marshal(map[string]string{"position": position})
		if err != nil {
			t.Fatal(err)
		}
		wantResponse(t, f.do(http.MethodPost, pathB, string(body), "198.51.100.12", token, h), http.StatusCreated, "")
		wantResponse(t, f.do(http.MethodDelete, pathB, "", "198.51.100.12", token, nil), http.StatusNoContent, "")
	}
	wantResponse(t, f.do(http.MethodDelete, pathA, "", "198.51.100.12", token, nil), http.StatusNoContent, "")
	wantResponse(t, f.do(http.MethodPost, pathB, `{"position":"Presidente"}`, "198.51.100.12", token, h), http.StatusCreated, "")
}

func TestExistingOfficerCannotBeAssignedAgain(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, admin, _, _ := f.signInMFA(t, ctx, "school_admin")
	user, _ := f.signIn(t, ctx, "officer")
	member := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, member, f.tenant, user); err != nil {
		t.Fatal(err)
	}
	rec := f.do(http.MethodPost, "/api/members/"+member+"/officer", `{"position":"Tesorero"}`, "198.51.100.12", admin, nil)
	wantResponse(t, rec, http.StatusConflict, `{"error":"already_officer"}`)
	var count int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM officer_positions WHERE tenant_id=$1`, f.tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("positions = %d, %v", count, err)
	}
}

func TestOfficerAssignmentAuthorizationAndIsolation(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, admin, _, _ := f.signInMFA(t, ctx, "school_admin")
	_, unverified := f.signIn(t, ctx, "school_admin")
	_, student := f.signIn(t, ctx, "student")
	_, officer := f.signIn(t, ctx, "officer")
	_, outsider := f.signIn(t, ctx, "")
	mine := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	other := newTenant(t, ctx)
	foreign := seedMember(t, ctx, other, "Beto Ficticio", "30111223")
	var foreignMembership string
	if err := tdb.Super.QueryRow(ctx, `WITH u AS (INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id) INSERT INTO memberships (tenant_id,user_id,role) SELECT $2,id,'student' FROM u RETURNING id::text`, uniqueEmail("foreign"), other).Scan(&foreignMembership); err != nil {
		t.Fatal(err)
	}
	path := "/api/members/" + mine + "/officer"
	for _, tc := range []struct {
		token  string
		status int
		body   string
	}{
		{"", http.StatusUnauthorized, unauthBody},
		{student, http.StatusForbidden, `{"error":"forbidden"}`},
		{officer, http.StatusForbidden, `{"error":"forbidden"}`},
		{outsider, http.StatusForbidden, `{"error":"forbidden"}`},
		{unverified, http.StatusForbidden, `{"error":"mfa_required"}`},
	} {
		wantResponse(t, f.do(http.MethodPost, path, `{"position":"Presidente"}`, "198.51.100.12", tc.token, nil), tc.status, tc.body)
		wantResponse(t, f.do(http.MethodDelete, path, "", "198.51.100.12", tc.token, nil), tc.status, tc.body)
	}
	wantResponse(t, f.do(http.MethodPost, "/api/members/"+foreign+"/officer", `{"position":"Presidente"}`, "198.51.100.12", admin, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, f.do(http.MethodDelete, "/api/members/"+foreign+"/officer", "", "198.51.100.12", admin, nil), http.StatusNotFound, `{"error":"not_found"}`)
	for _, id := range []string{foreignMembership, "00000000-0000-4000-8000-000000000000"} {
		wantResponse(t, f.do(http.MethodPost, "/api/members/"+id+"/officer", `{"position":"Presidente"}`, "198.51.100.12", admin, nil), http.StatusNotFound, `{"error":"not_found"}`)
	}
	probe := &readProbe{}
	req := httptest.NewRequest(http.MethodPost, path, probe)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: unverified})
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	wantResponse(t, rec, http.StatusForbidden, `{"error":"mfa_required"}`)
	if probe.read {
		t.Fatal("request body read before MFA check")
	}
	var count int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM officer_positions WHERE tenant_id=$1`, f.tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("positions = %d: %v", count, err)
	}
	studentID, assigned := f.signIn(t, ctx, "student")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE user_id=$2`, mine, studentID); err != nil {
		t.Fatal(err)
	}
	wantResponse(t, f.do(http.MethodPost, path, `{"position":"Presidente"}`, "198.51.100.12", admin, nil), http.StatusCreated, "")
	wantResponse(t, f.do(http.MethodPost, path, `{"position":"Tesorero"}`, "198.51.100.12", assigned, nil), http.StatusForbidden, `{"error":"forbidden"}`)
	wantResponse(t, f.do(http.MethodGet, "/api/roster", "", "198.51.100.12", assigned, nil), http.StatusForbidden, `{"error":"forbidden"}`)
}

func TestConcurrentOfficerPositionAssignment(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, admin, _, _ := f.signInMFA(t, ctx, "school_admin")
	a := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	b := seedMember(t, ctx, f.tenant, "Beto Ficticio", "30111223")
	results := make(chan *httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for _, member := range []string{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- f.do(http.MethodPost, "/api/members/"+member+"/officer", `{"position":"Presidente"}`, "198.51.100.12", admin, nil)
		}()
	}
	wg.Wait()
	close(results)
	var statuses []int
	for result := range results {
		statuses = append(statuses, result.Code)
		if result.Code == http.StatusConflict {
			wantResponse(t, result, http.StatusConflict, `{"error":"position_taken"}`)
		}
	}
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusCreated, http.StatusConflict}) {
		t.Fatalf("statuses = %v", statuses)
	}
	var count int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM officer_positions WHERE tenant_id=$1 AND position='Presidente'`, f.tenant).Scan(&count); err != nil || count != 1 {
		t.Fatalf("presidents = %d: %v", count, err)
	}
}

func TestOfficerWithoutAccountGetsRoleOnClaim(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, admin, _, _ := f.signInMFA(t, ctx, "school_admin")
	member := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	code := seedCode(t, ctx, f.tenant, member, "active", f.clk.Now())
	path := "/api/members/" + member + "/officer"
	wantResponse(t, f.do(http.MethodPost, path, `{"position":"Tesorero"}`, "198.51.100.12", admin, nil), http.StatusCreated, "")
	rec := f.claim(code, uniqueEmail("officer"), password, "192.0.2.1")
	if rec.Code != http.StatusCreated {
		t.Fatalf("claim: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Roles []string `json:"roles"`
		MFA   string   `json:"mfa"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || !slices.Equal(got.Roles, []string{"student", "officer"}) || got.MFA != "enrollment_required" {
		t.Fatalf("claim = %s, err = %v", rec.Body.String(), err)
	}
}

func TestSchoolAdminCanRedeemStudentRoleInSameSchool(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	id, token, _, _ := f.signInMFA(t, ctx, "school_admin")
	member := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	code := seedCode(t, ctx, f.tenant, member, "active", f.clk.Now())
	rec := f.redeem(code, token)
	var got struct {
		Roles []string `json:"roles"`
		MFA   string   `json:"mfa"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK || !slices.Equal(got.Roles, []string{"student", "school_admin"}) || got.MFA != "satisfied" {
		t.Fatalf("redeem = %d %s, err = %v", rec.Code, rec.Body.String(), err)
	}
	if got := memberAccounts(t, ctx, member); len(got) != 1 || got[0] != id+"|student|"+f.tenant {
		t.Fatalf("student membership = %v", got)
	}
}

func TestAssignAndRemoveOfficerPreservesStudentAndRosterIdentity(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, admin, _, _ := f.signInMFA(t, ctx, "school_admin")
	id, student := f.signIn(t, ctx, "student")
	member := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, member, f.tenant, id); err != nil {
		t.Fatal(err)
	}
	path := "/api/members/" + member + "/officer"
	wantResponse(t, f.do(http.MethodPost, path, `{"position":"Tesorero"}`, "198.51.100.12", admin, nil), http.StatusCreated, "")
	checkMe := func(wantRoles []string, wantMFA string) {
		t.Helper()
		rec := f.do(http.MethodGet, "/api/auth/me", "", "198.51.100.12", student, nil)
		var m struct {
			Roles []string `json:"roles"`
			Role  string   `json:"role"`
			MFA   string   `json:"mfa"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		if len(m.Roles) == 0 {
			m.Roles = []string{m.Role}
		}
		if rec.Code != 200 || !slices.Equal(m.Roles, wantRoles) || m.MFA != wantMFA {
			t.Fatalf("me = %d %s", rec.Code, rec.Body.String())
		}
	}
	checkMe([]string{"student", "officer"}, "enrollment_required")
	wantResponse(t, f.do(http.MethodDelete, path, "", "198.51.100.12", admin, nil), http.StatusNoContent, "")
	checkMe([]string{"student"}, "not_required")
	wantResponse(t, f.do(http.MethodDelete, path, "", "198.51.100.12", admin, nil), http.StatusNotFound, `{"error":"not_found"}`)
	var count int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM roster_members WHERE tenant_id=$1 AND id=$2`, f.tenant, member).Scan(&count); err != nil || count != 1 {
		t.Fatalf("roster count = %d: %v", count, err)
	}
	if got := memberAccounts(t, ctx, member); len(got) != 1 || got[0] != id+"|student|"+f.tenant {
		t.Fatalf("student membership = %v", got)
	}
}

func TestMembersListIncludesAccountsAndUnclaimedOfficersOnlyFromActiveSchool(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, admin, _, _ := f.signInMFA(t, ctx, "school_admin")
	mine := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	other := newTenant(t, ctx)
	foreign := seedMember(t, ctx, other, "Beto Ficticio", "30111223")
	_, err := tdb.Super.Exec(ctx, `INSERT INTO officer_positions (tenant_id, roster_member_id, position) VALUES ($1,$2,'Tesorero'), ($3,$4,'Presidente')`, f.tenant, mine, other, foreign)
	if err != nil {
		t.Fatal(err)
	}
	rec := f.do(http.MethodGet, "/api/members", "", "198.51.100.12", admin, nil)
	wantNoStore(t, rec)
	var rows []struct {
		Email    *string  `json:"email"`
		FullName *string  `json:"fullName"`
		Position *string  `json:"position"`
		Roles    []string `json:"roles"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &rows) != nil {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	var account, unclaimed bool
	for _, row := range rows {
		if slices.Contains(row.Roles, "school_admin") && row.Email != nil {
			account = true
		}
		if row.FullName != nil && *row.FullName == "Ana Ficticia" && row.Email == nil && row.Position != nil && *row.Position == "Tesorero" {
			unclaimed = true
		}
		if row.FullName != nil && *row.FullName == "Beto Ficticio" {
			t.Fatal("foreign member listed")
		}
	}
	if !account || !unclaimed {
		t.Fatalf("missing account or unclaimed officer: %s", rec.Body.String())
	}
}

// S7 (#58, A18): once the latest published election had a winner, the School admin can no longer
// assign or remove positions by hand: 409 board_elected and the Board stays as it was. Before any
// published election, or when the latest published one had no winner, it still can (#55). An
// annulled election does not count as published.
func TestManualBoardChangesAfterAnElection(t *testing.T) {
	ctx := integration(t)
	h := map[string]string{"Content-Type": "application/json"}
	assign := func(v *voteSchool, member, position string) *httptest.ResponseRecorder {
		return v.do(http.MethodPost, "/api/members/"+member+"/officer", `{"position":"`+position+`"}`, electionIP, v.admin, h)
	}
	remove := func(v *voteSchool, member string) *httptest.ResponseRecorder {
		return v.do(http.MethodDelete, "/api/members/"+member+"/officer", "", electionIP, v.admin, nil)
	}
	// stillManual assigns Tesorero to a new member and removes it again.
	stillManual := func(t *testing.T, v *voteSchool) {
		t.Helper()
		x := seedMember(t, ctx, v.tenant, "Xime Ficticia", "30555099")
		wantResponse(t, assign(v, x, "Tesorero"), http.StatusCreated, "")
		wantResponse(t, remove(v, x), http.StatusNoContent, "")
	}
	// elected publishes a win of Lista Azul with Ana Ficticia as its only candidate (Presidente).
	elected := func(t *testing.T) (*voteSchool, string) {
		t.Helper()
		v := handoverSchool(t, ctx, defaultPositions)
		ana := seedMember(t, ctx, v.tenant, "Ana Ficticia", "30555001")
		seedCandidates(t, ctx, v.tenant, v.electionID, v.blue, ana)
		seedBallots(t, ctx, v.tenant, v.electionID, v.blue, 1)
		if r := v.publish(t, ctx); r.Outcome != "winner" {
			t.Fatalf("outcome = %q, want winner", r.Outcome)
		}
		return v, ana
	}
	wantBlocked := func(t *testing.T, v *voteSchool, holder string) {
		t.Helper()
		boardBefore, storedBefore := v.board(t), storedBoard(t, ctx, v.tenant)
		x := seedMember(t, ctx, v.tenant, "Xime Ficticia", "30555099")
		wantResponse(t, assign(v, x, "Tesorero"), http.StatusConflict, `{"error":"board_elected"}`)
		wantResponse(t, remove(v, holder), http.StatusConflict, `{"error":"board_elected"}`)
		// Malformed input is still reported as such (validation runs before the Board check).
		wantResponse(t, assign(v, x, "Otro"), http.StatusUnprocessableEntity, `{"error":"invalid_position"}`)
		if got := v.board(t); !slices.Equal(got, boardBefore) {
			t.Fatalf("board = %v, want unchanged %v", got, boardBefore)
		}
		if got := storedBoard(t, ctx, v.tenant); !slices.Equal(got, storedBefore) {
			t.Fatalf("stored board = %v, want unchanged %v", got, storedBefore)
		}
	}

	t.Run("latest published election had a winner", func(t *testing.T) {
		v, ana := elected(t)
		if got, want := v.board(t), []string{"Ana Ficticia|Presidente"}; !slices.Equal(got, want) {
			t.Fatalf("board = %v, want %v", got, want)
		}
		wantBlocked(t, v, ana)
	})
	t.Run("a later annulled election does not hide the winner", func(t *testing.T) {
		v, ana := elected(t)
		if _, err := tdb.Super.Exec(ctx, `
			INSERT INTO elections (tenant_id, commission_id, name, mode, submission_starts_at, submission_ends_at, starts_at, ends_at, published_at, annulled_at, annulment_reason)
			SELECT tenant_id, commission_id, name, mode, submission_starts_at + interval '1 year', submission_ends_at + interval '1 year',
			       starts_at + interval '1 year', ends_at + interval '1 year', ends_at + interval '1 year', starts_at + interval '1 year', $2
			FROM elections WHERE id = $1`, v.electionID, exampleReason); err != nil {
			t.Fatal(err)
		}
		wantBlocked(t, v, ana)
	})
	t.Run("before any published election", func(t *testing.T) {
		stillManual(t, handoverSchool(t, ctx, defaultPositions))
	})
	for _, tc := range []struct {
		name, outcome      string
		blue, green, blank int
	}{
		{"latest published election was a tie", "tie", 2, 2, 0},
		{"latest published election had no votes for any list", "no_votes", 0, 0, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := handoverSchool(t, ctx, defaultPositions)
			seedCandidates(t, ctx, v.tenant, v.electionID, v.blue, seedMember(t, ctx, v.tenant, "Ana Ficticia", "30555011"))
			seedBallots(t, ctx, v.tenant, v.electionID, v.blue, tc.blue)
			seedBallots(t, ctx, v.tenant, v.electionID, v.green, tc.green)
			seedBallots(t, ctx, v.tenant, v.electionID, "", tc.blank)
			if r := v.publish(t, ctx); r.Outcome != tc.outcome {
				t.Fatalf("outcome = %q, want %q", r.Outcome, tc.outcome)
			}
			stillManual(t, v)
		})
	}
	t.Run("only an annulled election", func(t *testing.T) {
		v := handoverSchool(t, ctx, defaultPositions)
		seedCandidates(t, ctx, v.tenant, v.electionID, v.blue, seedMember(t, ctx, v.tenant, "Ana Ficticia", "30555021"))
		seedBallots(t, ctx, v.tenant, v.electionID, v.blue, 1)
		if _, err := tdb.Super.Exec(ctx, `UPDATE elections SET annulled_at = starts_at, annulment_reason = $2 WHERE id = $1`, v.electionID, exampleReason); err != nil {
			t.Fatal(err)
		}
		if r := v.publish(t, ctx); r.Status != "annulled" {
			t.Fatalf("result = %+v, want annulled", r)
		}
		stillManual(t, v)
	})
	t.Run("an election annulled after its publication with a winner", func(t *testing.T) {
		v, _ := elected(t)
		if _, err := tdb.Super.Exec(ctx, `UPDATE elections SET annulled_at = ends_at, annulment_reason = $2 WHERE id = $1`, v.electionID, exampleReason); err != nil {
			t.Fatal(err)
		}
		stillManual(t, v)
	})
}

// S7/S10 (#58): the result is published at the close (#57), so a manual change after the close and
// before anyone reads the result gets 409 board_elected and is not silently discarded, and the
// "Miembros" screen already shows the new Board.
func TestManualBoardChangeBetweenCloseAndFirstRead(t *testing.T) {
	ctx := integration(t)
	closed := func(t *testing.T) (*voteSchool, string) {
		t.Helper()
		v := handoverSchool(t, ctx, defaultPositions)
		ana := seedMember(t, ctx, v.tenant, "Ana Ficticia", "30555001")
		seedCandidates(t, ctx, v.tenant, v.electionID, v.blue, ana)
		seedBallots(t, ctx, v.tenant, v.electionID, v.blue, 1)
		v.clk.t = vEnd
		return v, ana
	}
	wantBoard := func(t *testing.T, v *voteSchool) {
		t.Helper()
		if got, want := v.board(t), []string{"Ana Ficticia|Presidente"}; !slices.Equal(got, want) {
			t.Fatalf("board = %v, want %v", got, want)
		}
	}
	t.Run("assign", func(t *testing.T) {
		v, _ := closed(t)
		x := seedMember(t, ctx, v.tenant, "Xime Ficticia", "30555099")
		wantResponse(t, v.do(http.MethodPost, "/api/members/"+x+"/officer", `{"position":"Tesorero"}`, electionIP, v.admin,
			map[string]string{"Content-Type": "application/json"}), http.StatusConflict, `{"error":"board_elected"}`)
		wantBoard(t, v)
	})
	t.Run("remove", func(t *testing.T) {
		v, _ := closed(t)
		var member string // Beto Ficticio, Presidente of the outgoing Board
		if err := tdb.Super.QueryRow(ctx, `SELECT roster_member_id::text FROM officer_positions WHERE tenant_id = $1`, v.tenant).Scan(&member); err != nil {
			t.Fatal(err)
		}
		wantResponse(t, v.do(http.MethodDelete, "/api/members/"+member+"/officer", "", electionIP, v.admin, nil),
			http.StatusConflict, `{"error":"board_elected"}`)
		wantBoard(t, v)
	})
	t.Run("members list", func(t *testing.T) {
		v, ana := closed(t)
		wantBoard(t, v)
		if got, want := storedBoard(t, ctx, v.tenant), []string{"position:" + ana + ":Presidente"}; !slices.Equal(got, want) {
			t.Fatalf("stored board = %v, want %v", got, want)
		}
		if published, winner, _ := publication(t, ctx, v.electionID); !published || winner != v.blue {
			t.Fatalf("published = %t, winner = %q; want published with %s", published, winner, v.blue)
		}
	})
}
