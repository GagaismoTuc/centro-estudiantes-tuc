package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type exemptMemberJSON struct {
	RosterMemberID  string  `json:"rosterMemberId"`
	FullName        string  `json:"fullName"`
	Status          string  `json:"status"`
	ExemptionReason *string `json:"exemptionReason"`
}

type exemptDetailJSON struct {
	dueSummaryJSON
	Members []exemptMemberJSON `json:"members"`
}

// exemptionsFixture is a school with Officer Ana (MFA) and Students Beto and Carla, all owing one Due.
type exemptionsFixture struct {
	*fixture
	officer, betoToken, carlaToken string
	ana, beto, carla               string
	due                            dueDetailJSON
}

func newExemptionsFixture(t *testing.T, ctx context.Context) *exemptionsFixture {
	t.Helper()
	f := newFixture(t, ctx)
	e := &exemptionsFixture{fixture: f}
	var user string
	user, e.officer, _, _ = f.signInMFA(t, ctx, "officer")
	e.ana = seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, e.ana, f.tenant, user); err != nil {
		t.Fatal(err)
	}
	_, e.betoToken, e.beto = f.student(t, ctx, "Beto Ficticio", "30111223")
	_, e.carlaToken, e.carla = f.student(t, ctx, "Carla Ficticia", "30111224")
	e.due = e.createDue(t, "Cuota de noviembre")
	return e
}

func (e *exemptionsFixture) createDue(t *testing.T, name string) dueDetailJSON {
	t.Helper()
	r := e.do(http.MethodPost, "/api/finance/dues", fmt.Sprintf(`{"name":%q,"amountCents":200000,"dueDate":"2026-11-30"}`, name), "198.51.100.10", e.officer, nil)
	if r.Code != http.StatusCreated {
		t.Fatalf("create due: %d %s", r.Code, r.Body)
	}
	var d dueDetailJSON
	if err := json.Unmarshal(r.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func (e *exemptionsFixture) exempt(token, dueID, body string) *httptest.ResponseRecorder {
	return e.do(http.MethodPost, "/api/finance/dues/"+dueID+"/exemptions", body, "198.51.100.10", token, nil)
}

func exemptBody(member, reason string) string {
	b, _ := json.Marshal(map[string]string{"rosterMemberId": member, "reason": reason})
	return string(b)
}

func (e *exemptionsFixture) exemptions(t *testing.T) int {
	t.Helper()
	var n int
	if err := tdb.Super.QueryRow(context.Background(), `SELECT count(*) FROM finance_due_exemptions WHERE tenant_id=$1`, e.tenant).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *exemptionsFixture) detail(t *testing.T, token, dueID string) (exemptDetailJSON, string) {
	t.Helper()
	r := e.do(http.MethodGet, "/api/finance/dues/"+dueID, "", "198.51.100.10", token, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("detail: %d %s", r.Code, r.Body)
	}
	var d exemptDetailJSON
	if err := json.Unmarshal(r.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	return d, r.Body.String()
}

// wantRejected asserts the status and error code and that no Exemption was written.
func (e *exemptionsFixture) wantRejected(t *testing.T, rec *httptest.ResponseRecorder, before, status int, code string) {
	t.Helper()
	wantResponse(t, rec, status, fmt.Sprintf(`{"error":%q}`, code))
	if after := e.exemptions(t); after != before {
		t.Fatalf("%s: exemptions %d -> %d, want nothing written", code, before, after)
	}
}

func memberStatus(d exemptDetailJSON, id string) (string, string) {
	for _, m := range d.Members {
		if m.RosterMemberID == id {
			reason := ""
			if m.ExemptionReason != nil {
				reason = *m.ExemptionReason
			}
			return m.Status, reason
		}
	}
	return "", ""
}

func TestExemptionsR1CreateShowsExempt(t *testing.T) {
	ctx := integration(t)
	e := newExemptionsFixture(t, ctx)

	r := e.exempt(e.officer, e.due.ID, exemptBody(e.beto, "Beca"))
	if r.Code != http.StatusCreated {
		t.Fatalf("exempt: %d %s", r.Code, r.Body)
	}
	var created exemptDetailJSON
	if err := json.Unmarshal(r.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if status, reason := memberStatus(created, e.beto); status != "exempt" || reason != "Beca" {
		t.Fatalf("201 body: beto status=%q reason=%q", status, reason)
	}
	got, _ := e.detail(t, e.officer, e.due.ID)
	if status, reason := memberStatus(got, e.beto); status != "exempt" || reason != "Beca" {
		t.Fatalf("detail: beto status=%q reason=%q", status, reason)
	}
	var stored string
	if err := tdb.Super.QueryRow(ctx, `SELECT reason FROM finance_due_exemptions WHERE tenant_id=$1 AND due_id=$2 AND roster_member_id=$3`, e.tenant, e.due.ID, e.beto).Scan(&stored); err != nil || stored != "Beca" {
		t.Fatalf("stored reason=%q err=%v", stored, err)
	}
	if e.exemptions(t) != 1 {
		t.Fatalf("exemptions = %d, want 1", e.exemptions(t))
	}
}

func TestExemptionsR2ReasonRequired(t *testing.T) {
	ctx := integration(t)
	e := newExemptionsFixture(t, ctx)
	before := e.exemptions(t)
	for _, tc := range []struct{ name, body, code string }{
		{"empty", exemptBody(e.beto, ""), "reason_required"},
		{"spaces", exemptBody(e.beto, "   "), "reason_required"},
		{"absent", fmt.Sprintf(`{"rosterMemberId":%q}`, e.beto), "reason_required"},
		{"null", fmt.Sprintf(`{"rosterMemberId":%q,"reason":null}`, e.beto), "reason_required"},
		{"501 characters", exemptBody(e.beto, strings.Repeat("á", 501)), "reason_too_long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e.wantRejected(t, e.exempt(e.officer, e.due.ID, tc.body), before, http.StatusUnprocessableEntity, tc.code)
		})
	}
	// 500 characters is the limit and is accepted.
	if r := e.exempt(e.officer, e.due.ID, exemptBody(e.beto, strings.Repeat("á", 500))); r.Code != http.StatusCreated {
		t.Fatalf("500 characters: %d %s", r.Code, r.Body)
	}
}

func TestExemptionsR3OnePerStudentAndRace(t *testing.T) {
	ctx := integration(t)
	e := newExemptionsFixture(t, ctx)
	if r := e.exempt(e.officer, e.due.ID, exemptBody(e.beto, "Beca")); r.Code != http.StatusCreated {
		t.Fatalf("first exemption: %d %s", r.Code, r.Body)
	}
	e.wantRejected(t, e.exempt(e.officer, e.due.ID, exemptBody(e.beto, "Otra beca")), 1, http.StatusConflict, "already_exempt")
	var stored string
	if err := tdb.Super.QueryRow(ctx, `SELECT reason FROM finance_due_exemptions WHERE due_id=$1 AND roster_member_id=$2`, e.due.ID, e.beto).Scan(&stored); err != nil || stored != "Beca" {
		t.Fatalf("stored reason changed: %q err=%v", stored, err)
	}

	// Two simultaneous requests for Carla: one 201, the other 409 already_exempt, one row.
	recs := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range recs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			recs[i] = e.exempt(e.officer, e.due.ID, exemptBody(e.carla, "Beca"))
		}(i)
	}
	close(start)
	wg.Wait()
	codes := map[int]int{}
	for _, r := range recs {
		codes[r.Code]++
		if r.Code == http.StatusConflict {
			wantResponse(t, r, http.StatusConflict, `{"error":"already_exempt"}`)
		}
	}
	if codes[http.StatusCreated] != 1 || codes[http.StatusConflict] != 1 {
		t.Fatalf("race codes = %v, want one 201 and one 409", codes)
	}
	var rows int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM finance_due_exemptions WHERE due_id=$1 AND roster_member_id=$2`, e.due.ID, e.carla).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("race rows=%d err=%v, want 1", rows, err)
	}
}

func TestExemptionsR4OutOfDenominator(t *testing.T) {
	ctx := integration(t)
	e := newExemptionsFixture(t, ctx)
	if r := e.exempt(e.officer, e.due.ID, exemptBody(e.beto, "Beca")); r.Code != http.StatusCreated {
		t.Fatalf("exempt: %d %s", r.Code, r.Body)
	}
	// 3 members, Beto exempt, no payments yet (S10): "0 de 2 pagaron"; Ana and Carla owe.
	got, _ := e.detail(t, e.officer, e.due.ID)
	if got.TotalCount != 2 || got.OwingCount != 2 || len(got.Members) != 3 {
		t.Fatalf("detail counts total=%d owing=%d members=%d, want 2/2/3", got.TotalCount, got.OwingCount, len(got.Members))
	}
	var debtors []string
	for _, m := range got.Members {
		if m.Status == "owing" {
			debtors = append(debtors, m.FullName)
		}
	}
	if strings.Join(debtors, ",") != "Ana Ficticia,Carla Ficticia" {
		t.Fatalf("debtors = %v, want Ana and Carla", debtors)
	}
	var list []dueSummaryJSON
	if r := e.do(http.MethodGet, "/api/finance/dues", "", "198.51.100.10", e.officer, nil); r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &list) != nil || len(list) != 1 || list[0].TotalCount != 2 || list[0].OwingCount != 2 {
		t.Fatalf("list: code=%d body=%s", r.Code, r.Body)
	}
}

func TestExemptionsR5FreezesDue(t *testing.T) {
	ctx := integration(t)
	e := newExemptionsFixture(t, ctx)
	if r := e.exempt(e.officer, e.due.ID, exemptBody(e.beto, "Beca")); r.Code != http.StatusCreated {
		t.Fatalf("exempt: %d %s", r.Code, r.Body)
	}
	path := "/api/finance/dues/" + e.due.ID
	wantResponse(t, e.do(http.MethodPatch, path, `{"amountCents":250000}`, "198.51.100.10", e.officer, nil), http.StatusConflict, `{"error":"due_frozen"}`)
	wantResponse(t, e.do(http.MethodPatch, path, `{"dueDate":"2026-12-15"}`, "198.51.100.10", e.officer, nil), http.StatusConflict, `{"error":"due_frozen"}`)
	wantResponse(t, e.do(http.MethodDelete, path, "", "198.51.100.10", e.officer, nil), http.StatusConflict, `{"error":"due_frozen"}`)
	got, _ := e.detail(t, e.officer, e.due.ID)
	if got.Name != "Cuota de noviembre" || got.AmountCents != 200000 || got.DueDate != "2026-11-30" || len(got.Members) != 3 {
		t.Fatalf("frozen due changed: %+v", got)
	}
	if d, m := duesCount(t, e.fixture); d != 1 || m != 3 || e.exemptions(t) != 1 {
		t.Fatalf("rows changed: dues=%d members=%d exemptions=%d", d, m, e.exemptions(t))
	}
}

func TestExemptionsR6WhoSeesTheReason(t *testing.T) {
	ctx := integration(t)
	e := newExemptionsFixture(t, ctx)
	_, adminMFA, _, _ := e.signInMFA(t, ctx, "school_admin")
	if r := e.exempt(e.officer, e.due.ID, exemptBody(e.beto, "Beca")); r.Code != http.StatusCreated {
		t.Fatalf("exempt: %d %s", r.Code, r.Body)
	}
	// Officers and School admins see the Exemption and its reason (veedor arrives with #28).
	for _, token := range []string{e.officer, adminMFA} {
		got, _ := e.detail(t, token, e.due.ID)
		if status, reason := memberStatus(got, e.beto); status != "exempt" || reason != "Beca" {
			t.Fatalf("reader detail: status=%q reason=%q", status, reason)
		}
	}
	// The exempt Student sees only "exempt", without a reason field.
	r := e.do(http.MethodGet, "/api/finance/dues/mine", "", "198.51.100.10", e.betoToken, nil)
	var betoMine []map[string]any
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &betoMine) != nil || len(betoMine) != 1 || betoMine[0]["status"] != "exempt" {
		t.Fatalf("beto mine: %d %s", r.Code, r.Body)
	}
	if _, has := betoMine[0]["exemptionReason"]; has || strings.Contains(r.Body.String(), "Beca") {
		t.Fatalf("beto mine exposes the reason: %s", r.Body)
	}
	// Another Student sees nothing of the Exemption and cannot read the detail.
	r = e.do(http.MethodGet, "/api/finance/dues/mine", "", "198.51.100.10", e.carlaToken, nil)
	var carlaMine []myDueJSON
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &carlaMine) != nil || len(carlaMine) != 1 || carlaMine[0].Status != "owing" || strings.Contains(r.Body.String(), "Beca") || strings.Contains(r.Body.String(), "exempt") {
		t.Fatalf("carla mine: %d %s", r.Code, r.Body)
	}
	wantResponse(t, e.do(http.MethodGet, "/api/finance/dues/"+e.due.ID, "", "198.51.100.10", e.carlaToken, nil), http.StatusForbidden, `{"error":"forbidden"}`)
}

func TestExemptionsR7WriteAuthorization(t *testing.T) {
	ctx := integration(t)
	e := newExemptionsFixture(t, ctx)
	_, adminMFA, _, _ := e.signInMFA(t, ctx, "school_admin")
	_, officerNoMFA := e.signIn(t, ctx, "officer")
	body := exemptBody(e.beto, "Beca")
	// The veedor role arrives with #28: memberships.role does not allow it yet.
	for _, c := range []struct {
		name, token, body string
		status            int
		code              string
	}{
		{"no session", "", body, http.StatusUnauthorized, "unauthenticated"},
		{"student", e.carlaToken, body, http.StatusForbidden, "forbidden"},
		{"school admin", adminMFA, body, http.StatusForbidden, "forbidden"},
		{"officer without MFA", officerNoMFA, body, http.StatusForbidden, "mfa_required"},
		{"officer without MFA, malformed body", officerNoMFA, `{not-json`, http.StatusForbidden, "mfa_required"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e.wantRejected(t, e.exempt(c.token, e.due.ID, c.body), 0, c.status, c.code)
		})
	}
	got, _ := e.detail(t, e.officer, e.due.ID)
	if got.TotalCount != 3 || got.OwingCount != 3 || got.AmountCents != 200000 {
		t.Fatalf("due changed after rejections: %+v", got)
	}
}

func TestExemptionsR8IsolationAndSnapshot(t *testing.T) {
	ctx := integration(t)
	e := newExemptionsFixture(t, ctx)
	other := newExemptionsFixture(t, ctx)
	// Dani joins the Roster after the Due was created, so Dani is outside its snapshot.
	dani := seedMember(t, ctx, e.tenant, "Dani Ficticio", "30111225")
	for _, tc := range []struct {
		name, due, body string
		status          int
		code            string
	}{
		{"due of another school", other.due.ID, exemptBody(e.beto, "Beca"), http.StatusNotFound, "not_found"},
		{"member of another school", e.due.ID, exemptBody(other.beto, "Beca"), http.StatusNotFound, "not_found"},
		{"unknown due", "00000000-0000-0000-0000-000000000000", exemptBody(e.beto, "Beca"), http.StatusNotFound, "not_found"},
		{"member outside the snapshot", e.due.ID, exemptBody(dani, "Beca"), http.StatusUnprocessableEntity, "not_owed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e.wantRejected(t, e.exempt(e.officer, tc.due, tc.body), 0, tc.status, tc.code)
		})
	}
	if other.exemptions(t) != 0 {
		t.Fatalf("other school got exemptions: %d", other.exemptions(t))
	}
}

func TestExemptionsR9NoReasonOrNameInErrorsOrLogs(t *testing.T) {
	ctx := integration(t)
	e := newExemptionsFixture(t, ctx)
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prev)

	const reason = "Beca Privada Ficticia"
	dani := seedMember(t, ctx, e.tenant, "Dani Ficticio", "30111225")
	recs := []*httptest.ResponseRecorder{
		e.exempt(e.officer, e.due.ID, exemptBody(e.beto, reason)),
		e.exempt(e.officer, e.due.ID, exemptBody(e.beto, reason)),
		e.exempt(e.officer, e.due.ID, exemptBody(dani, reason)),
		e.exempt(e.officer, e.due.ID, exemptBody(e.carla, strings.Repeat("x", 490)+reason)),
		e.exempt(e.carlaToken, e.due.ID, exemptBody(e.beto, reason)),
		e.exempt(e.officer, "not-a-uuid", exemptBody(e.beto, reason)),
		e.exempt(e.officer, e.due.ID, `{"rosterMemberId":"`+e.beto+`","reason":"`+reason+`","extra":1}`),
	}
	if recs[0].Code != http.StatusCreated {
		t.Fatalf("exempt: %d %s", recs[0].Code, recs[0].Body)
	}
	for i, r := range recs[1:] {
		if r.Code < 400 || r.Code >= 500 {
			t.Fatalf("request %d: want a 4xx rejection, got %d %s", i+1, r.Code, r.Body)
		}
		for _, secret := range []string{reason, "Beto Ficticio", "Dani Ficticio", "Carla Ficticia", "30111223"} {
			if strings.Contains(r.Body.String(), secret) {
				t.Fatalf("error body %d leaked %q: %s", i+1, secret, r.Body)
			}
		}
	}
	for _, secret := range []string{reason, "Beto Ficticio", "Dani Ficticio", "Carla Ficticia", "30111223"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("logs leaked %q: %s", secret, logs.String())
		}
	}
}
