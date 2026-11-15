package httpapi_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"
)

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
	foreign := seedMember(t, ctx, newTenant(t, ctx), "Beto Ficticio", "30111223")
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
	var count int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM officer_positions WHERE tenant_id=$1`, f.tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("positions = %d: %v", count, err)
	}
}

func TestConcurrentOfficerPositionAssignment(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, admin, _, _ := f.signInMFA(t, ctx, "school_admin")
	a := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	b := seedMember(t, ctx, f.tenant, "Beto Ficticio", "30111223")
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for _, member := range []string{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- f.do(http.MethodPost, "/api/members/"+member+"/officer", `{"position":"Presidente"}`, "198.51.100.12", admin, nil).Code
		}()
	}
	wg.Wait()
	close(results)
	var statuses []int
	for result := range results {
		statuses = append(statuses, result)
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
