package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type dueMemberJSON struct {
	RosterMemberID string `json:"rosterMemberId"`
	FullName       string `json:"fullName"`
	Status         string `json:"status"`
}

type dueSummaryJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AmountCents int64  `json:"amountCents"`
	DueDate     string `json:"dueDate"`
	OwingCount  int    `json:"owingCount"`
	TotalCount  int    `json:"totalCount"`
}

type dueDetailJSON struct {
	dueSummaryJSON
	Members []dueMemberJSON `json:"members"`
}

type myDueJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AmountCents int64  `json:"amountCents"`
	DueDate     string `json:"dueDate"`
	Status      string `json:"status"`
}

func duesCount(t *testing.T, f *fixture) (dues, members int) {
	t.Helper()
	ctx := t.Context()
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM finance_dues WHERE tenant_id=$1`, f.tenant).Scan(&dues); err != nil {
		t.Fatal(err)
	}
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM finance_due_members WHERE tenant_id=$1`, f.tenant).Scan(&members); err != nil {
		t.Fatal(err)
	}
	return dues, members
}

func TestDuesR1CreateAndValidation(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	user, officer, _, _ := f.signInMFA(t, ctx, "officer")
	anaID := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, anaID, f.tenant, user); err != nil {
		t.Fatal(err)
	}

	r := f.do(http.MethodPost, "/api/finance/dues", `{"name":"Cuota de noviembre","amountCents":200000,"dueDate":"2026-11-30"}`, "198.51.100.10", officer, nil)
	if r.Code != http.StatusCreated {
		t.Fatalf("create due: status=%d body=%s", r.Code, r.Body)
	}
	var got dueDetailJSON
	if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "Cuota de noviembre" || got.AmountCents != 200000 || got.DueDate != "2026-11-30" || got.OwingCount != 1 || got.TotalCount != 1 {
		t.Fatalf("unexpected created due: %+v", got)
	}

	duesBefore, membersBefore := duesCount(t, f)
	for _, tc := range []struct {
		name string
		body string
		code string
	}{
		{"empty name", `{"name":"","amountCents":200000,"dueDate":"2026-11-30"}`, "name_required"},
		{"blank name", `{"name":"   ","amountCents":200000,"dueDate":"2026-11-30"}`, "name_required"},
		{"zero amount", `{"name":"Cuota","amountCents":0,"dueDate":"2026-11-30"}`, "invalid_amount"},
		{"negative amount", `{"name":"Cuota","amountCents":-500,"dueDate":"2026-11-30"}`, "invalid_amount"},
		{"null amount", `{"name":"Cuota","amountCents":null,"dueDate":"2026-11-30"}`, "invalid_amount"},
		{"missing date", `{"name":"Cuota","amountCents":200000,"dueDate":""}`, "invalid_date"},
		{"bad date format", `{"name":"Cuota","amountCents":200000,"dueDate":"30/11/2026"}`, "invalid_date"},
		{"invalid calendar date", `{"name":"Cuota","amountCents":200000,"dueDate":"2026-02-30"}`, "invalid_date"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(http.MethodPost, "/api/finance/dues", tc.body, "198.51.100.10", officer, nil)
			wantResponse(t, rec, http.StatusUnprocessableEntity, fmt.Sprintf(`{"error":%q}`, tc.code))
		})
	}
	duesAfter, membersAfter := duesCount(t, f)
	if duesAfter != duesBefore || membersAfter != membersBefore {
		t.Fatalf("rejections modified rows: dues %d->%d members %d->%d", duesBefore, duesAfter, membersBefore, membersAfter)
	}
}

func TestDuesR2FixedRosterSnapshot(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	user, officer, _, _ := f.signInMFA(t, ctx, "officer")

	// Empty roster returns 422 empty_roster and writes nothing.
	wantResponse(t, f.do(http.MethodPost, "/api/finance/dues", `{"name":"Cuota de noviembre","amountCents":200000,"dueDate":"2026-11-30"}`, "198.51.100.10", officer, nil), http.StatusUnprocessableEntity, `{"error":"empty_roster"}`)
	if d, m := duesCount(t, f); d != 0 || m != 0 {
		t.Fatalf("empty roster persisted rows: dues=%d members=%d", d, m)
	}

	anaID := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, anaID, f.tenant, user); err != nil {
		t.Fatal(err)
	}
	_, betoToken, _ := f.student(t, ctx, "Beto Ficticio", "30111223")

	create := f.do(http.MethodPost, "/api/finance/dues", `{"name":"Cuota de noviembre","amountCents":200000,"dueDate":"2026-11-30"}`, "198.51.100.10", officer, nil)
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", create.Code, create.Body)
	}
	var created dueDetailJSON
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// Import Carla Ficticia after creation; Carla must not appear on the Due or owe it.
	_, carlaToken, _ := f.student(t, ctx, "Carla Ficticia", "30111224")

	detail := f.do(http.MethodGet, "/api/finance/dues/"+created.ID, "", "198.51.100.10", officer, nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail: %d %s", detail.Code, detail.Body)
	}
	var gotDetail dueDetailJSON
	if err := json.Unmarshal(detail.Body.Bytes(), &gotDetail); err != nil {
		t.Fatal(err)
	}
	if len(gotDetail.Members) != 2 || gotDetail.Members[0].FullName != "Ana Ficticia" || gotDetail.Members[1].FullName != "Beto Ficticio" {
		t.Fatalf("unexpected members snapshot: %+v", gotDetail.Members)
	}

	var betoMine, carlaMine []myDueJSON
	if r := f.do(http.MethodGet, "/api/finance/dues/mine", "", "198.51.100.10", betoToken, nil); r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &betoMine) != nil || len(betoMine) != 1 {
		t.Fatalf("beto mine: code=%d body=%s", r.Code, r.Body)
	}
	if r := f.do(http.MethodGet, "/api/finance/dues/mine", "", "198.51.100.10", carlaToken, nil); r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &carlaMine) != nil || len(carlaMine) != 0 {
		t.Fatalf("carla mine: code=%d body=%s", r.Code, r.Body)
	}
}

func TestDuesR3UpdateAndDelete(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	user, officer, _, _ := f.signInMFA(t, ctx, "officer")
	anaID := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, anaID, f.tenant, user); err != nil {
		t.Fatal(err)
	}

	create := f.do(http.MethodPost, "/api/finance/dues", `{"name":"Cuota de noviembre","amountCents":200000,"dueDate":"2026-11-30"}`, "198.51.100.10", officer, nil)
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", create.Code, create.Body)
	}
	var created dueDetailJSON
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// Rejections on PATCH leave stored row unchanged.
	wantResponse(t, f.do(http.MethodPatch, "/api/finance/dues/"+created.ID, `{"name":"  "}`, "198.51.100.10", officer, nil), http.StatusUnprocessableEntity, `{"error":"name_required"}`)
	wantResponse(t, f.do(http.MethodPatch, "/api/finance/dues/"+created.ID, `{"amountCents":0}`, "198.51.100.10", officer, nil), http.StatusUnprocessableEntity, `{"error":"invalid_amount"}`)
	wantResponse(t, f.do(http.MethodPatch, "/api/finance/dues/"+created.ID, `{"dueDate":"invalid"}`, "198.51.100.10", officer, nil), http.StatusUnprocessableEntity, `{"error":"invalid_date"}`)
	afterReject := f.do(http.MethodGet, "/api/finance/dues/"+created.ID, "", "198.51.100.10", officer, nil)
	var unchanged dueDetailJSON
	if afterReject.Code != http.StatusOK || json.Unmarshal(afterReject.Body.Bytes(), &unchanged) != nil || unchanged.Name != "Cuota de noviembre" || unchanged.AmountCents != 200000 || unchanged.DueDate != "2026-11-30" {
		t.Fatalf("rejected PATCH mutated row: code=%d got=%+v", afterReject.Code, unchanged)
	}

	patch := f.do(http.MethodPatch, "/api/finance/dues/"+created.ID, `{"name":"Cuota de diciembre","amountCents":250000,"dueDate":"2026-12-15"}`, "198.51.100.10", officer, nil)
	if patch.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", patch.Code, patch.Body)
	}
	var updated dueDetailJSON
	if err := json.Unmarshal(patch.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Cuota de diciembre" || updated.AmountCents != 250000 || updated.DueDate != "2026-12-15" {
		t.Fatalf("unexpected updated due: %+v", updated)
	}

	del := f.do(http.MethodDelete, "/api/finance/dues/"+created.ID, "", "198.51.100.10", officer, nil)
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", del.Code, del.Body)
	}
	wantResponse(t, f.do(http.MethodGet, "/api/finance/dues/"+created.ID, "", "198.51.100.10", officer, nil), http.StatusNotFound, `{"error":"not_found"}`)
	if d, m := duesCount(t, f); d != 0 || m != 0 {
		t.Fatalf("delete left rows: dues=%d members=%d", d, m)
	}
}

func TestDuesR4FullListVisibilityAndNoDNI(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	user, officer, _, _ := f.signInMFA(t, ctx, "officer")
	anaID := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, anaID, f.tenant, user); err != nil {
		t.Fatal(err)
	}
	seedMember(t, ctx, f.tenant, "Beto Ficticio", "30999888")
	_, adminMFA, _, _ := f.signInMFA(t, ctx, "school_admin")
	_, adminNoMFA := f.signIn(t, ctx, "school_admin")

	create := f.do(http.MethodPost, "/api/finance/dues", `{"name":"Cuota de noviembre","amountCents":200000,"dueDate":"2026-11-30"}`, "198.51.100.10", officer, nil)
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", create.Code, create.Body)
	}
	var created dueDetailJSON
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// Unverified privileged user gets mfa_required.
	wantResponse(t, f.do(http.MethodGet, "/api/finance/dues", "", "198.51.100.10", adminNoMFA, nil), http.StatusForbidden, `{"error":"mfa_required"}`)
	wantResponse(t, f.do(http.MethodGet, "/api/finance/dues/"+created.ID, "", "198.51.100.10", adminNoMFA, nil), http.StatusForbidden, `{"error":"mfa_required"}`)

	for _, token := range []string{officer, adminMFA} {
		listRec := f.do(http.MethodGet, "/api/finance/dues", "", "198.51.100.10", token, nil)
		if listRec.Code != http.StatusOK {
			t.Fatalf("list: %d %s", listRec.Code, listRec.Body)
		}
		if strings.Contains(listRec.Body.String(), "30111222") || strings.Contains(listRec.Body.String(), "30999888") {
			t.Fatalf("list exposed DNI: %s", listRec.Body)
		}
		var list []dueSummaryJSON
		if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].OwingCount != 2 || list[0].AmountCents != 200000 || list[0].DueDate != "2026-11-30" {
			t.Fatalf("list=%+v err=%v", list, err)
		}

		detailRec := f.do(http.MethodGet, "/api/finance/dues/"+created.ID, "", "198.51.100.10", token, nil)
		if detailRec.Code != http.StatusOK {
			t.Fatalf("detail: %d %s", detailRec.Code, detailRec.Body)
		}
		if strings.Contains(detailRec.Body.String(), "30111222") || strings.Contains(detailRec.Body.String(), "30999888") {
			t.Fatalf("detail exposed DNI: %s", detailRec.Body)
		}
		var detail dueDetailJSON
		if err := json.Unmarshal(detailRec.Body.Bytes(), &detail); err != nil || len(detail.Members) != 2 || detail.Members[0].Status != "owing" {
			t.Fatalf("detail=%+v err=%v", detail, err)
		}
	}
}

func TestDuesR5StudentMineAndForbiddenFullList(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	user, officer, _, _ := f.signInMFA(t, ctx, "officer")
	anaID := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, anaID, f.tenant, user); err != nil {
		t.Fatal(err)
	}
	_, betoToken, _ := f.student(t, ctx, "Beto Ficticio", "30111223")

	create := f.do(http.MethodPost, "/api/finance/dues", `{"name":"Cuota de noviembre","amountCents":200000,"dueDate":"2026-11-30"}`, "198.51.100.10", officer, nil)
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", create.Code, create.Body)
	}
	var created dueDetailJSON
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// Student cannot read full list or detail.
	wantResponse(t, f.do(http.MethodGet, "/api/finance/dues", "", "198.51.100.10", betoToken, nil), http.StatusForbidden, `{"error":"forbidden"}`)
	wantResponse(t, f.do(http.MethodGet, "/api/finance/dues/"+created.ID, "", "198.51.100.10", betoToken, nil), http.StatusForbidden, `{"error":"forbidden"}`)

	// Student imported after creation is absent from /finance/dues/mine.
	_, lateToken, _ := f.student(t, ctx, "Carla Ficticia", "30111224")
	var lateMine []myDueJSON
	if rec := f.do(http.MethodGet, "/api/finance/dues/mine", "", "198.51.100.10", lateToken, nil); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &lateMine) != nil || len(lateMine) != 0 {
		t.Fatalf("late student mine=%+v code=%d body=%s", lateMine, rec.Code, rec.Body)
	}

	// Student and Officer (ADR 0002) both see their own dues via /finance/dues/mine.
	for _, token := range []string{betoToken, officer} {
		rec := f.do(http.MethodGet, "/api/finance/dues/mine", "", "198.51.100.10", token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("mine: %d %s", rec.Code, rec.Body)
		}
		var mine []myDueJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &mine); err != nil || len(mine) != 1 || mine[0].Name != "Cuota de noviembre" || mine[0].AmountCents != 200000 || mine[0].DueDate != "2026-11-30" || mine[0].Status != "owing" {
			t.Fatalf("mine=%+v err=%v", mine, err)
		}
	}
	_, veedorPending := f.signIn(t, ctx, "veedor")
	wantResponse(t, f.do(http.MethodGet, "/api/finance/dues/mine", "", "198.51.100.10", veedorPending, nil), http.StatusForbidden, `{"error":"mfa_required"}`)
}

func TestDuesR6WriteAuthorizationTable(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	user, officerMFA, _, _ := f.signInMFA(t, ctx, "officer")
	anaID := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, anaID, f.tenant, user); err != nil {
		t.Fatal(err)
	}
	_, officerNoMFA := f.signIn(t, ctx, "officer")
	_, adminMFA, _, _ := f.signInMFA(t, ctx, "school_admin")
	_, studentToken, _ := f.student(t, ctx, "Beto Ficticio", "30111223")

	// Position holder (role=student + officer_positions row) with MFA can write; without MFA gets mfa_required.
	posUser, posMFA, _, _ := f.signInMFA(t, ctx, "student")
	_, posNoMFA := f.signIn(t, ctx, "student")
	carlaID := seedMember(t, ctx, f.tenant, "Carla Ficticia", "30111224")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, carlaID, f.tenant, posUser); err != nil {
		t.Fatal(err)
	}
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO officer_positions(tenant_id,roster_member_id,position) VALUES($1,$2,'Tesorero')`, f.tenant, carlaID); err != nil {
		t.Fatal(err)
	}
	// Link posNoMFA user to a second position holder to verify MFA-before-parse on position holders too.
	posNoMFAUser, posNoMFAToken := f.signIn(t, ctx, "student")
	diegoID := seedMember(t, ctx, f.tenant, "Diego Ficticio", "30111225")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, diegoID, f.tenant, posNoMFAUser); err != nil {
		t.Fatal(err)
	}
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO officer_positions(tenant_id,roster_member_id,position) VALUES($1,$2,'Secretario')`, f.tenant, diegoID); err != nil {
		t.Fatal(err)
	}
	_ = posNoMFA

	create := f.do(http.MethodPost, "/api/finance/dues", `{"name":"Cuota base","amountCents":200000,"dueDate":"2026-11-30"}`, "198.51.100.10", officerMFA, nil)
	if create.Code != http.StatusCreated {
		t.Fatalf("create base: %d %s", create.Code, create.Body)
	}
	var base dueDetailJSON
	if err := json.Unmarshal(create.Body.Bytes(), &base); err != nil {
		t.Fatal(err)
	}

	// Veedor arrives with #28 (memberships.role CHECK currently allows school_admin, officer, student).
	cases := []struct {
		name   string
		token  string
		status int
		code   string
	}{
		{"no session", "", http.StatusUnauthorized, "unauthenticated"},
		{"student", studentToken, http.StatusForbidden, "forbidden"},
		{"school_admin", adminMFA, http.StatusForbidden, "forbidden"},
		{"officer without MFA", officerNoMFA, http.StatusForbidden, "mfa_required"},
		{"position holder without MFA", posNoMFAToken, http.StatusForbidden, "mfa_required"},
	}

	for _, c := range cases {
		for _, op := range []struct {
			method, path, body string
		}{
			{http.MethodPost, "/api/finance/dues", `{"name":"No","amountCents":1000,"dueDate":"2026-11-30"}`},
			{http.MethodPatch, "/api/finance/dues/" + base.ID, `{"name":"Cambiada"}`},
			{http.MethodDelete, "/api/finance/dues/" + base.ID, ""},
		} {
			t.Run(c.name+" "+op.method, func(t *testing.T) {
				rec := f.do(op.method, op.path, op.body, "198.51.100.10", c.token, nil)
				wantResponse(t, rec, c.status, fmt.Sprintf(`{"error":%q}`, c.code))
			})
		}
	}

	// Auth/MFA runs before JSON parsing: malformed JSON without MFA still returns 403 mfa_required.
	wantResponse(t, f.do(http.MethodPost, "/api/finance/dues", `{not-json`, "198.51.100.10", officerNoMFA, nil), http.StatusForbidden, `{"error":"mfa_required"}`)

	// Confirm nothing changed in the database after all rejections.
	if d, _ := duesCount(t, f); d != 1 {
		t.Fatalf("dues count changed after rejections: %d", d)
	}
	var storedName string
	if err := tdb.Super.QueryRow(ctx, `SELECT name FROM finance_dues WHERE id=$1`, base.ID).Scan(&storedName); err != nil || storedName != "Cuota base" {
		t.Fatalf("stored due changed: name=%q err=%v", storedName, err)
	}

	// Position holder with MFA can PATCH and DELETE.
	patch := f.do(http.MethodPatch, "/api/finance/dues/"+base.ID, `{"name":"Cuota editada"}`, "198.51.100.10", posMFA, nil)
	if patch.Code != http.StatusOK {
		t.Fatalf("position holder patch: %d %s", patch.Code, patch.Body)
	}
}

func TestDuesR8TenantIsolation(t *testing.T) {
	ctx := integration(t)
	fA := newFixture(t, ctx)
	uA, offA, _, _ := fA.signInMFA(t, ctx, "officer")
	mA := seedMember(t, ctx, fA.tenant, "Ana Ficticia", "30111222")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, mA, fA.tenant, uA); err != nil {
		t.Fatal(err)
	}

	create := fA.do(http.MethodPost, "/api/finance/dues", `{"name":"Cuota A","amountCents":200000,"dueDate":"2026-11-30"}`, "198.51.100.10", offA, nil)
	if create.Code != http.StatusCreated {
		t.Fatalf("create A: %d %s", create.Code, create.Body)
	}
	var dueA dueDetailJSON
	if err := json.Unmarshal(create.Body.Bytes(), &dueA); err != nil {
		t.Fatal(err)
	}

	fB := newFixture(t, ctx)
	uB, offB, _, _ := fB.signInMFA(t, ctx, "officer")
	mB := seedMember(t, ctx, fB.tenant, "Beto Ficticio", "30111223")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, mB, fB.tenant, uB); err != nil {
		t.Fatal(err)
	}

	wantResponse(t, fB.do(http.MethodGet, "/api/finance/dues/"+dueA.ID, "", "198.51.100.10", offB, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, fB.do(http.MethodPatch, "/api/finance/dues/"+dueA.ID, `{"name":"Hack"}`, "198.51.100.10", offB, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, fB.do(http.MethodDelete, "/api/finance/dues/"+dueA.ID, "", "198.51.100.10", offB, nil), http.StatusNotFound, `{"error":"not_found"}`)

	var listB []dueSummaryJSON
	if r := fB.do(http.MethodGet, "/api/finance/dues", "", "198.51.100.10", offB, nil); r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &listB) != nil || len(listB) != 0 {
		t.Fatalf("school B list leaked school A: code=%d body=%s", r.Code, r.Body)
	}
	var mineB []myDueJSON
	if r := fB.do(http.MethodGet, "/api/finance/dues/mine", "", "198.51.100.10", offB, nil); r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &mineB) != nil || len(mineB) != 0 {
		t.Fatalf("school B mine leaked school A: code=%d body=%s", r.Code, r.Body)
	}

	// School A due remains intact.
	var nameA string
	if err := tdb.Super.QueryRow(ctx, `SELECT name FROM finance_dues WHERE id=$1`, dueA.ID).Scan(&nameA); err != nil || nameA != "Cuota A" {
		t.Fatalf("school A due modified: name=%q err=%v", nameA, err)
	}
}

func TestDuesR9PrivacyNoSensitiveLogs(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	user, officer, _, _ := f.signInMFA(t, ctx, "officer")
	anaID := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, anaID, f.tenant, user); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	recs := []*httptest.ResponseRecorder{
		f.do(http.MethodPost, "/api/finance/dues", `{"name":"Cuota Privacidad","amountCents":200000,"dueDate":"2026-11-30"}`, "198.51.100.10", officer, nil),
		f.do(http.MethodGet, "/api/finance/dues", "", "198.51.100.10", officer, nil),
		f.do(http.MethodGet, "/api/finance/dues/mine", "", "198.51.100.10", officer, nil),
		f.do(http.MethodGet, "/api/finance/dues/not-a-uuid", "", "198.51.100.10", officer, nil),
	}
	for _, r := range recs {
		if r.Code >= 500 {
			t.Fatalf("unexpected 5xx: %d %s", r.Code, r.Body)
		}
	}
	logs := buf.String()
	for _, forbidden := range []string{"Ana Ficticia", "30111222", "owing", "Cuota Privacidad"} {
		if strings.Contains(logs, forbidden) {
			t.Fatalf("logs leaked %q: %s", forbidden, logs)
		}
	}
}
