package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const acctPath = "/api/finance/accountability-reports"

// acctNow is 2026-11-30 12:00 in Argentina: the period of the examples ends today.
var acctNow = time.Date(2026, 11, 30, 15, 0, 0, 0, time.UTC)

type acctEntry struct {
	ID          string `json:"id"`
	Date        string `json:"date"`
	Direction   string `json:"direction"`
	CategoryID  string `json:"categoryId"`
	Category    string `json:"category"`
	Description string `json:"description"`
	AmountCents int64  `json:"amountCents"`
	RecordedBy  string `json:"recordedBy"`
	ReversesID  string `json:"reversesId"`
	Reversed    bool   `json:"reversed"`
}

type acctDue struct {
	DueID       string `json:"dueId"`
	Name        string `json:"name"`
	DueDate     string `json:"dueDate"`
	AmountCents int64  `json:"amountCents"`
	PaidCount   int    `json:"paidCount"`
	TotalCount  int    `json:"totalCount"`
}

type acctReport struct {
	ID           string        `json:"id"`
	From         string        `json:"from"`
	To           string        `json:"to"`
	Term         string        `json:"term"`
	PublishedBy  string        `json:"publishedBy"`
	PublishedAt  time.Time     `json:"publishedAt"`
	OpeningCents int64         `json:"openingCents"`
	ClosingCents int64         `json:"closingCents"`
	Totals       []reportTotal `json:"totals"`
	Dues         []acctDue     `json:"dues"`
	Entries      []acctEntry   `json:"entries"`
}

type acctSummary struct {
	ID          string    `json:"id"`
	From        string    `json:"from"`
	To          string    `json:"to"`
	Term        string    `json:"term"`
	PublishedBy string    `json:"publishedBy"`
	PublishedAt time.Time `json:"publishedAt"`
}

func newAcctFixture(t *testing.T) *reportFixture {
	t.Helper()
	return newReportFixture(t, integration(t), acctNow)
}

func (rf *reportFixture) publish(token, body string) *httptest.ResponseRecorder {
	return rf.do(http.MethodPost, acctPath, body, reportIP, token, nil)
}

// acctState is every stored report row of the school, so a rejected request can prove it wrote nothing.
func (rf *reportFixture) acctState(t *testing.T) string {
	t.Helper()
	var out string
	if err := tdb.Super.QueryRow(rf.ctx, `SELECT count(*)::text || ':' || COALESCE(md5(string_agg(id::text || term || period_from || period_to || published_by_name || report::text, ',' ORDER BY id)), '')
		FROM finance_accountability_reports WHERE tenant_id=$1`, rf.tenant).Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func decodeAcct(t *testing.T, r *httptest.ResponseRecorder, status int) acctReport {
	t.Helper()
	if r.Code != status {
		t.Fatalf("status=%d want %d body=%s", r.Code, status, r.Body)
	}
	var out acctReport
	dec := json.NewDecoder(bytes.NewReader(r.Body.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (rf *reportFixture) publishOK(t *testing.T, token, from, to, term string) (acctReport, []byte) {
	t.Helper()
	r := rf.publish(token, fmt.Sprintf(`{"from":%q,"to":%q,"term":%q}`, from, to, term))
	return decodeAcct(t, r, http.StatusCreated), r.Body.Bytes()
}

// renameMember changes the Roster name of the fixture's Officer.
func (rf *reportFixture) renameOfficer(t *testing.T, name string) {
	t.Helper()
	if _, err := tdb.Super.Exec(rf.ctx, `UPDATE roster_members SET full_name=$2 WHERE tenant_id=$1 AND full_name='Carla Ficticia'`, rf.tenant, name); err != nil {
		t.Fatal(err)
	}
}

// newOfficer signs in another Officer with a two-factor session, linked to a Roster member named name.
func (rf *reportFixture) newOfficer(t *testing.T, name, doc string) string {
	t.Helper()
	user, token, _, _ := rf.signInMFA(t, rf.ctx, "officer")
	member := seedMember(t, rf.ctx, rf.tenant, name, doc)
	if _, err := tdb.Super.Exec(rf.ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, member, rf.tenant, user); err != nil {
		t.Fatal(err)
	}
	return token
}

func (rf *reportFixture) createDue(t *testing.T, name, dueDate string, cents int64) string {
	t.Helper()
	r := rf.do(http.MethodPost, "/api/finance/dues", fmt.Sprintf(`{"name":%q,"amountCents":%d,"dueDate":%q}`, name, cents, dueDate), reportIP, rf.officer, nil)
	if r.Code != http.StatusCreated {
		t.Fatalf("create due: %d %s", r.Code, r.Body)
	}
	var out struct{ ID string }
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil || out.ID == "" {
		t.Fatalf("create due body=%s err=%v", r.Body, err)
	}
	return out.ID
}

func (rf *reportFixture) payDue(t *testing.T, due, member, date string, cents int64) string {
	t.Helper()
	r := rf.do(http.MethodPost, "/api/finance/dues/"+due+"/payments", fmt.Sprintf(`{"rosterMemberId":%q,"amountCents":%d,"date":%q}`, member, cents, date), reportIP, rf.officer, nil)
	if r.Code != http.StatusCreated {
		t.Fatalf("pay due: %d %s", r.Code, r.Body)
	}
	var out struct{ EntryID string }
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil || out.EntryID == "" {
		t.Fatalf("pay due body=%s err=%v", r.Body, err)
	}
	return out.EntryID
}

// TestAccountabilityPublish covers R1: an Officer publishes a period for a Term and gets the copy;
// an invalid range, a future end or a blank Term is rejected with nothing written.
func TestAccountabilityPublish(t *testing.T) {
	rf := newAcctFixture(t)
	rf.renameOfficer(t, "Ana Ficticia")
	rf.record(t, "income/Ventas", "2026-02-27", 100000, "Kiosco de febrero")
	march := rf.record(t, "income/Ventas", "2026-03-01", 50000, "Kiosco de marzo")
	rent := rf.record(t, "expense/Alquileres", "2026-11-30", 20000, "Alquiler del salón")

	before := rf.acctState(t)
	rep, body := rf.publishOK(t, rf.officer, "2026-03-01", "2026-11-30", "Gestión 2026")
	if rep.ID == "" || rep.PublishedBy != "Ana Ficticia" || !rep.PublishedAt.Equal(rf.clk.t) || rep.Term != "Gestión 2026" || rep.From != "2026-03-01" || rep.To != "2026-11-30" {
		t.Fatalf("published=%+v", rep)
	}
	if rep.OpeningCents != 100000 || rep.ClosingCents != 130000 || len(rep.Entries) != 2 || rep.Entries[0].ID != march || rep.Entries[1].ID != rent ||
		rep.Entries[0].Category != "Ventas" || rep.Entries[0].RecordedBy != "Ana Ficticia" || len(rep.Totals) != 2 || len(rep.Dues) != 0 {
		t.Fatalf("copy=%+v", rep)
	}
	if after := rf.acctState(t); after == before || !strings.HasPrefix(after, "1:") {
		t.Fatalf("stored=%s", after)
	}
	if got := rf.get(acctPath+"/"+rep.ID, rf.officer); got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), body) {
		t.Fatalf("GET status=%d differs from the POST copy:\n%s\n%s", got.Code, got.Body, body)
	}

	for _, c := range []struct{ name, body, code string }{
		{"reversed range", `{"from":"2026-11-30","to":"2026-03-01","term":"Gestión 2026"}`, "invalid_range"},
		{"malformed date", `{"from":"2026-03-01","to":"2026-11-31","term":"Gestión 2026"}`, "invalid_range"},
		{"missing from", `{"to":"2026-11-30","term":"Gestión 2026"}`, "invalid_range"},
		{"future to", `{"from":"2026-03-01","to":"2026-12-01","term":"Gestión 2026"}`, "future_date"},
		{"empty term", `{"from":"2026-03-01","to":"2026-11-30","term":""}`, "term_required"},
		{"blank term", `{"from":"2026-03-01","to":"2026-11-30","term":"   "}`, "term_required"},
		{"missing term", `{"from":"2026-03-01","to":"2026-11-30"}`, "term_required"},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := rf.acctState(t)
			wantResponse(t, rf.publish(rf.officer, c.body), http.StatusUnprocessableEntity, `{"error":"`+c.code+`"}`)
			if after := rf.acctState(t); after != before {
				t.Fatalf("rejected publish wrote: %s -> %s", before, after)
			}
		})
	}
	t.Run("today ends in Argentina", func(t *testing.T) {
		prev := rf.clk.t
		rf.clk.t = time.Date(2026, 12, 1, 2, 59, 0, 0, time.UTC) // 2026-11-30 23:59 in Argentina
		defer func() { rf.clk.t = prev }()
		before := rf.acctState(t)
		wantResponse(t, rf.publish(rf.officer, `{"from":"2026-03-01","to":"2026-12-01","term":"Gestión 2026"}`), http.StatusUnprocessableEntity, `{"error":"future_date"}`)
		if after := rf.acctState(t); after != before {
			t.Fatalf("rejected publish wrote: %s -> %s", before, after)
		}
	})
}

// TestAccountabilityFrozen covers R2: a Reversal, a Category rename and a new payment after
// publication leave the copy and its CSV byte for byte the same; PUT and DELETE answer 405.
func TestAccountabilityFrozen(t *testing.T) {
	rf := newAcctFixture(t)
	march := rf.record(t, "income/Ventas", "2026-03-10", 50000, "Kiosco de marzo")
	_, _, beto := rf.student(t, rf.ctx, "Beto Ficticio", "30111223")
	due := rf.createDue(t, "Cuota de noviembre", "2026-11-15", 200000)
	rep, _ := rf.publishOK(t, rf.officer, "2026-03-01", "2026-11-30", "Gestión 2026")
	first := rf.get(acctPath+"/"+rep.ID, rf.officer)
	firstCSV := rf.get(acctPath+"/"+rep.ID+".csv", rf.officer)
	if first.Code != http.StatusOK || firstCSV.Code != http.StatusOK {
		t.Fatalf("read status=%d/%d", first.Code, firstCSV.Code)
	}

	if r := rf.do(http.MethodPost, "/api/finance/entries/"+march+"/reversal", "", reportIP, rf.officer, nil); r.Code != http.StatusCreated {
		t.Fatalf("reversal: %d %s", r.Code, r.Body)
	}
	if r := rf.do(http.MethodPatch, "/api/finance/categories/"+rf.cat["income/Ventas"], `{"name":"Ventas de comida"}`, reportIP, rf.officer, nil); r.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", r.Code, r.Body)
	}
	rf.payDue(t, due, beto, "2026-11-20", 200000)
	var live struct{ Entries []json.RawMessage }
	liveRec := rf.get("/api/finance/report?from=2026-03-01&to=2026-11-30", rf.officer)
	if err := json.Unmarshal(liveRec.Body.Bytes(), &live); err != nil || len(live.Entries) != 3 || !strings.Contains(liveRec.Body.String(), `"name":"Ventas de comida"`) {
		t.Fatalf("the live report did not change: %s", liveRec.Body)
	}

	again := rf.get(acctPath+"/"+rep.ID, rf.officer)
	if again.Code != http.StatusOK || !bytes.Equal(again.Body.Bytes(), first.Body.Bytes()) {
		t.Fatalf("copy changed:\n%s\n%s", first.Body, again.Body)
	}
	againCSV := rf.get(acctPath+"/"+rep.ID+".csv", rf.officer)
	if againCSV.Code != http.StatusOK || !bytes.Equal(againCSV.Body.Bytes(), firstCSV.Body.Bytes()) {
		t.Fatalf("CSV changed:\n%s\n%s", firstCSV.Body, againCSV.Body)
	}
	frozen := decodeAcct(t, again, http.StatusOK)
	if len(frozen.Entries) != 1 || frozen.Entries[0].Category != "Ventas" || frozen.Entries[0].Reversed || frozen.Totals[0].Name != "Ventas" ||
		frozen.ClosingCents != 50000 || len(frozen.Dues) != 1 || frozen.Dues[0].PaidCount != 0 {
		t.Fatalf("frozen=%+v", frozen)
	}

	for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
		before := rf.acctState(t)
		if r := rf.do(method, acctPath+"/"+rep.ID, `{"term":"Otra"}`, reportIP, rf.officer, nil); r.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status=%d", method, r.Code)
		}
		if after := rf.acctState(t); after != before {
			t.Fatalf("%s changed the report", method)
		}
	}
}

// TestAccountabilityDueTotalsWithoutNames covers R3: per Due falling due in the period, "N de M
// pagaron" with M excluding Exemptions (480 members, 10 exempt, 312 paid: 312 of 470), and no
// Roster member name in the copy or its CSV, not even on payment Entries.
func TestAccountabilityDueTotalsWithoutNames(t *testing.T) {
	rf := newAcctFixture(t)
	_, student, beto := rf.student(t, rf.ctx, "Beto Ficticio", "30111223")
	// Carla (the Officer) and Beto plus 478 more: 480 Roster members owe the Due.
	if _, err := tdb.Super.Exec(rf.ctx, `INSERT INTO roster_members(tenant_id,full_name,document_number)
		SELECT $1, 'Alumno Ficticio ' || g, (40000000 + g)::text FROM generate_series(1, 478) g`, rf.tenant); err != nil {
		t.Fatal(err)
	}
	due := rf.createDue(t, "Cuota de noviembre", "2026-11-15", 200000)
	rf.createDue(t, "Cuota de febrero", "2026-02-28", 100000) // due before the period: not in the copy
	var officer, cuotas string
	if err := tdb.Super.QueryRow(rf.ctx, `SELECT m.user_id::text, c.id::text FROM memberships m, finance_categories c
		WHERE m.tenant_id=$1 AND m.role='officer' AND c.tenant_id=$1 AND c.name='Cuotas'`, rf.tenant).Scan(&officer, &cuotas); err != nil {
		t.Fatal(err)
	}
	if _, err := tdb.Super.Exec(rf.ctx, `INSERT INTO finance_due_exemptions(tenant_id,due_id,roster_member_id,reason,created_by)
		SELECT $1, $2, id, 'Beca', $3 FROM roster_members WHERE tenant_id=$1 AND full_name LIKE 'Alumno Ficticio %' ORDER BY full_name LIMIT 10`, rf.tenant, due, officer); err != nil {
		t.Fatal(err)
	}
	rf.payDue(t, due, beto, "2026-11-10", 200000)
	rows, err := tdb.Super.Query(rf.ctx, `SELECT id::text FROM roster_members r WHERE tenant_id=$1 AND full_name LIKE 'Alumno Ficticio %'
		AND NOT EXISTS (SELECT 1 FROM finance_due_exemptions e WHERE e.tenant_id=r.tenant_id AND e.roster_member_id=r.id) ORDER BY full_name LIMIT 311`, rf.tenant)
	if err != nil {
		t.Fatal(err)
	}
	var payers []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		payers = append(payers, id)
	}
	if rows.Close(); rows.Err() != nil || len(payers) != 311 {
		t.Fatalf("payers=%d err=%v", len(payers), rows.Err())
	}
	for _, member := range payers {
		if _, err := tdb.Super.Exec(rf.ctx, `WITH e AS (INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name)
			VALUES ($1,$3,'income','2026-11-10',200000,'Pago de cuota: Cuota de noviembre',$4,'Carla Ficticia') RETURNING id)
			INSERT INTO finance_due_payments(tenant_id,due_id,roster_member_id,entry_id,amount_cents,payment_date,recorded_by)
			SELECT $1,$2,$5,id,200000,'2026-11-10',$4 FROM e`, rf.tenant, due, cuotas, officer, member); err != nil {
			t.Fatal(err)
		}
	}
	// The officer's live ledger names Beto: the name exists where it may be seen.
	if ledger := rf.get("/api/finance/entries", rf.officer); !strings.Contains(ledger.Body.String(), "Beto Ficticio") {
		t.Fatalf("the officer ledger does not name the payer: %s", ledger.Body)
	}

	rep, body := rf.publishOK(t, rf.officer, "2026-03-01", "2026-11-30", "Gestión 2026")
	if len(rep.Dues) != 1 || rep.Dues[0] != (acctDue{DueID: due, Name: "Cuota de noviembre", DueDate: "2026-11-15", AmountCents: 200000, PaidCount: 312, TotalCount: 470}) {
		t.Fatalf("dues=%+v, want 312 of 470", rep.Dues)
	}
	payments := 0
	for _, e := range rep.Entries {
		if e.Description == "Pago de cuota: Cuota de noviembre" {
			payments++
		}
	}
	if payments != 312 {
		t.Fatalf("payment entries in the copy=%d, want 312", payments)
	}
	csvRec := rf.get(acctPath+"/"+rep.ID+".csv", student)
	records := readCSV(t, csvRec, "rendicion-de-cuentas-2026-03-01-a-2026-11-30.csv")
	found := false
	for _, rec := range records {
		found = found || strings.Join(rec, ";") == "Cuota de noviembre;2026-11-15;312;470"
	}
	if !found {
		t.Fatalf("CSV lacks the Due total row: %v", records[:20])
	}
	for name, got := range map[string]string{"POST": string(body), "GET": rf.get(acctPath+"/"+rep.ID, student).Body.String(), "CSV": csvRec.Body.String()} {
		if strings.Contains(got, "Beto Ficticio") || strings.Contains(got, "Alumno Ficticio") || strings.Contains(got, beto) {
			t.Fatalf("%s names a Roster member", name)
		}
	}
}

// TestAccountabilityPreviousTerm covers R4: an Officer of the incoming Board publishes the outgoing
// Term's period, and the report shows that they published it and for which Term.
func TestAccountabilityPreviousTerm(t *testing.T) {
	rf := newAcctFixture(t)
	rf.record(t, "income/Ventas", "2025-04-10", 70000, "Kiosco de abril")
	incoming := rf.newOfficer(t, "Dario Ficticio", "30111501")
	rep, _ := rf.publishOK(t, incoming, "2025-03-01", "2025-11-30", "Gestión 2025")
	if rep.PublishedBy != "Dario Ficticio" || rep.Term != "Gestión 2025" || len(rep.Entries) != 1 || rep.Entries[0].RecordedBy != "Carla Ficticia" {
		t.Fatalf("report=%+v", rep)
	}
	list := rf.get(acctPath, rf.officer)
	var summaries []acctSummary
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &summaries) != nil || len(summaries) != 1 ||
		summaries[0].PublishedBy != "Dario Ficticio" || summaries[0].Term != "Gestión 2025" || summaries[0].From != "2025-03-01" || summaries[0].To != "2025-11-30" {
		t.Fatalf("list=%d %s", list.Code, list.Body)
	}
}

// TestAccountabilityTwoPublications covers R5: two identical POSTs at once leave two independent
// frozen reports, and overlapping periods publish too.
func TestAccountabilityTwoPublications(t *testing.T) {
	rf := newAcctFixture(t)
	rf.record(t, "income/Ventas", "2026-05-10", 30000, "Kiosco de mayo")
	body := `{"from":"2026-03-01","to":"2026-11-30","term":"Gestión 2026"}`
	results := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = rf.publish(rf.officer, body)
		}()
	}
	wg.Wait()
	a, b := decodeAcct(t, results[0], http.StatusCreated), decodeAcct(t, results[1], http.StatusCreated)
	if a.ID == b.ID {
		t.Fatalf("both publications got id %s", a.ID)
	}
	rf.publishOK(t, rf.officer, "2026-06-01", "2026-11-30", "Gestión 2026")
	if state := rf.acctState(t); !strings.HasPrefix(state, "3:") {
		t.Fatalf("stored=%s, want 3 reports", state)
	}
	for _, rep := range []acctReport{a, b} {
		got := decodeAcct(t, rf.get(acctPath+"/"+rep.ID, rf.officer), http.StatusOK)
		if got.ID != rep.ID || len(got.Entries) != 1 || got.ClosingCents != 30000 {
			t.Fatalf("report %s=%+v", rep.ID, got)
		}
	}
}

// TestAccountabilityRoles covers R7: only an Officer with a two-factor session publishes; every
// member reads the list, a report and its CSV; without a session everything is 401.
// The Veedor role does not exist yet (#28); finance's member check lets it read once it does.
func TestAccountabilityRoles(t *testing.T) {
	rf := newAcctFixture(t)
	rep, _ := rf.publishOK(t, rf.officer, "2026-03-01", "2026-11-30", "Gestión 2026")
	_, officerNoMFA := rf.signIn(t, rf.ctx, "officer")
	_, student, _ := rf.student(t, rf.ctx, "Beto Ficticio", "30111223")
	_, adminMFA, _, _ := rf.signInMFA(t, rf.ctx, "school_admin")
	_, admin := rf.signIn(t, rf.ctx, "school_admin")
	body := `{"from":"2026-03-01","to":"2026-11-30","term":"Gestión 2026"}`

	for _, c := range []struct {
		name, token, body string
		status            int
		want              string
	}{
		{"officer without two-factor", officerNoMFA, body, http.StatusForbidden, `{"error":"mfa_required"}`},
		{"officer without two-factor, body unread", officerNoMFA, `{not json`, http.StatusForbidden, `{"error":"mfa_required"}`},
		{"student", student, body, http.StatusForbidden, `{"error":"forbidden"}`},
		{"school admin with two-factor", adminMFA, body, http.StatusForbidden, `{"error":"forbidden"}`},
		{"no session", "", body, http.StatusUnauthorized, `{"error":"unauthenticated"}`},
	} {
		t.Run("publish "+c.name, func(t *testing.T) {
			before := rf.acctState(t)
			wantResponse(t, rf.publish(c.token, c.body), c.status, c.want)
			if after := rf.acctState(t); after != before {
				t.Fatalf("rejected publish wrote: %s -> %s", before, after)
			}
		})
	}
	for name, token := range map[string]string{"officer": rf.officer, "officer without two-factor": officerNoMFA, "student": student, "school admin": admin, "school admin with two-factor": adminMFA} {
		t.Run("read "+name, func(t *testing.T) {
			if r := rf.get(acctPath, token); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), rep.ID) {
				t.Fatalf("list %d %s", r.Code, r.Body)
			}
			if got := decodeAcct(t, rf.get(acctPath+"/"+rep.ID, token), http.StatusOK); got.ID != rep.ID {
				t.Fatalf("report id=%s", got.ID)
			}
			readCSV(t, rf.get(acctPath+"/"+rep.ID+".csv", token), "rendicion-de-cuentas-2026-03-01-a-2026-11-30.csv")
		})
	}
	for _, path := range []string{acctPath, acctPath + "/" + rep.ID, acctPath + "/" + rep.ID + ".csv"} {
		wantResponse(t, rf.get(path, ""), http.StatusUnauthorized, `{"error":"unauthenticated"}`)
	}
}

// TestAccountabilityIsolation covers R8: another school's report id answers 404 not_found, as JSON
// and as CSV, and never shows in the list.
func TestAccountabilityIsolation(t *testing.T) {
	a := newAcctFixture(t)
	rep, _ := a.publishOK(t, a.officer, "2026-03-01", "2026-11-30", "Gestión 2026")
	b := newReportFixture(t, a.ctx, acctNow)
	for _, path := range []string{acctPath + "/" + rep.ID, acctPath + "/" + rep.ID + ".csv", acctPath + "/not-a-uuid"} {
		wantResponse(t, b.get(path, b.officer), http.StatusNotFound, `{"error":"not_found"}`)
	}
	wantResponse(t, b.get(acctPath, b.officer), http.StatusOK, `[]`)
}

// TestAccountabilityCSV covers the `{id}.csv` route the `{id}` handler serves: the published copy
// as a CSV for any reader (header, summary, totals per Due, Entries without "alumno"), and 404
// not_found for an unknown, empty or foreign id.
func TestAccountabilityCSV(t *testing.T) {
	rf := newAcctFixture(t)
	_, student, beto := rf.student(t, rf.ctx, "Beto Ficticio", "30111223")
	march := rf.record(t, "income/Ventas", "2026-03-10", 50000, "Kiosco de marzo")
	rf.record(t, "expense/Alquileres", "2026-04-02", 20000, "Alquiler del salón")
	due := rf.createDue(t, "Cuota de noviembre", "2026-11-15", 200000)
	rf.payDue(t, due, beto, "2026-11-10", 200000)
	if r := rf.do(http.MethodPost, "/api/finance/entries/"+march+"/reversal", "", reportIP, rf.officer, nil); r.Code != http.StatusCreated {
		t.Fatalf("reversal: %d %s", r.Code, r.Body)
	}
	rep, _ := rf.publishOK(t, rf.officer, "2026-03-01", "2026-11-30", "Gestión 2026")

	rec := rf.get(acctPath+"/"+rep.ID+".csv", student)
	if !strings.Contains(rec.Body.String(), "\r\n\r\n") {
		t.Fatalf("sections are not separated by an empty line: %q", rec.Body)
	}
	got := readCSV(t, rec, "rendicion-de-cuentas-2026-03-01-a-2026-11-30.csv")
	want := [][]string{
		{"concepto", "valor"},
		{"Gestión", "Gestión 2026"},
		{"Período", "2026-03-01 a 2026-11-30"},
		{"Publicada por", "Carla Ficticia"},
		{"Publicada el", rf.clk.t.In(time.FixedZone("Argentina", -3*60*60)).Format("2006-01-02 15:04")},
		{"concepto", "tipo", "monto"},
		{"Saldo inicial", "", "0,00"},
		{"Cuotas", "ingreso", "2000,00"},
		{"Ventas", "ingreso", "0,00"},
		{"Alquileres", "gasto", "-200,00"},
		{"Saldo final", "", "1800,00"},
		{"cuota", "vencimiento", "pagaron", "de"},
		{"Cuota de noviembre", "2026-11-15", "1", "2"},
		{"fecha", "tipo", "Categoría", "descripción", "monto", "registrado por", "revertido", "con comprobante"},
		{"2026-03-10", "ingreso", "Ventas", "Kiosco de marzo", "500,00", "Carla Ficticia", "sí", "no"},
		{"2026-04-02", "gasto", "Alquileres", "Alquiler del salón", "-200,00", "Carla Ficticia", "no", "no"},
		{"2026-11-10", "ingreso", "Cuotas", "Pago de cuota: Cuota de noviembre", "2000,00", "Carla Ficticia", "no", "no"},
		{"2026-11-30", "gasto", "Ventas", "Reversión: Kiosco de marzo", "-500,00", "Carla Ficticia", "no", "no"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("CSV rows:\n%q\nwant:\n%q", got, want)
	}
	if officerCSV := rf.get(acctPath+"/"+rep.ID+".csv", rf.officer); !bytes.Equal(officerCSV.Body.Bytes(), rec.Body.Bytes()) {
		t.Fatalf("the Officer's CSV differs from the Student's")
	}

	other := newReportFixture(t, rf.ctx, acctNow)
	for name, path := range map[string]string{
		"unknown id": acctPath + "/00000000-0000-4000-8000-000000000000.csv",
		"empty id":   acctPath + "/.csv",
		"not an id":  acctPath + "/r1.csv",
	} {
		t.Run(name, func(t *testing.T) {
			wantResponse(t, rf.get(path, rf.officer), http.StatusNotFound, `{"error":"not_found"}`)
		})
	}
	wantResponse(t, other.get(acctPath+"/"+rep.ID+".csv", other.officer), http.StatusNotFound, `{"error":"not_found"}`)
}
