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
	"time"
)

// payDate is "today" in Argentina for the fixture clock (2026-11-13 12:00 UTC); the next day is future.
const (
	payDate    = "2026-11-13"
	futureDate = "2026-11-14"
)

type paymentJSON struct {
	ID             string `json:"id"`
	DueID          string `json:"dueId"`
	RosterMemberID string `json:"rosterMemberId"`
	EntryID        string `json:"entryId"`
	AmountCents    int64  `json:"amountCents"`
	Date           string `json:"date"`
}

type paidMemberJSON struct {
	RosterMemberID string `json:"rosterMemberId"`
	FullName       string `json:"fullName"`
	Status         string `json:"status"`
	PaidCents      int64  `json:"paidCents"`
}

type paidDueJSON struct {
	ID         string           `json:"id"`
	OwingCount int              `json:"owingCount"`
	TotalCount int              `json:"totalCount"`
	Members    []paidMemberJSON `json:"members"`
}

type entryWithMemberJSON struct {
	ID           string `json:"id"`
	CategoryID   string `json:"categoryId"`
	Direction    string `json:"direction"`
	Date         string `json:"date"`
	Description  string `json:"description"`
	AmountCents  int64  `json:"amountCents"`
	RosterMember *struct {
		ID       string `json:"id"`
		FullName string `json:"fullName"`
	} `json:"rosterMember"`
}

// payFixture is a school with the Due "Cuota de noviembre" ($2.000) owed by Ana Ficticia (an officer
// with MFA) and Beto Ficticio (a student); Carla Ficticia is imported after the Due was created.
type payFixture struct {
	*fixture
	ctx                     context.Context
	officer, beto, adminMFA string
	due, betoID, carlaID    string
}

func newPayFixture(t *testing.T) *payFixture {
	t.Helper()
	ctx := integration(t)
	f := newFixture(t, ctx)
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, f.tenant); err != nil {
		t.Fatal(err)
	}
	user, officer, _, _ := f.signInMFA(t, ctx, "officer")
	ana := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, ana, f.tenant, user); err != nil {
		t.Fatal(err)
	}
	_, beto, betoID := f.student(t, ctx, "Beto Ficticio", "30111223")
	_, adminMFA, _, _ := f.signInMFA(t, ctx, "school_admin")
	rec := f.do(http.MethodPost, "/api/finance/dues", `{"name":"Cuota de noviembre","amountCents":200000,"dueDate":"2026-11-30"}`, "198.51.100.10", officer, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create due: %d %s", rec.Code, rec.Body)
	}
	var due dueDetailJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &due); err != nil {
		t.Fatal(err)
	}
	carlaID := seedMember(t, ctx, f.tenant, "Carla Ficticia", "30111224")
	return &payFixture{fixture: f, ctx: ctx, officer: officer, beto: beto, adminMFA: adminMFA, due: due.ID, betoID: betoID, carlaID: carlaID}
}

func (p *payFixture) pay(token, member string, amount int64, date string) *httptest.ResponseRecorder {
	body := fmt.Sprintf(`{"rosterMemberId":%q,"amountCents":%d,"date":%q}`, member, amount, date)
	return p.do(http.MethodPost, "/api/finance/dues/"+p.due+"/payments", body, "198.51.100.10", token, nil)
}

func (p *payFixture) payOK(t *testing.T, amount int64) paymentJSON {
	t.Helper()
	rec := p.pay(p.officer, p.betoID, amount, payDate)
	if rec.Code != http.StatusCreated {
		t.Fatalf("pay %d: %d %s", amount, rec.Code, rec.Body)
	}
	var out paymentJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// ledger is what a rejected payment must leave unchanged: payments, entries and the balance.
type ledger struct {
	payments, entries int
	balance           int64
}

func (p *payFixture) ledger(t *testing.T) ledger {
	t.Helper()
	var l ledger
	if err := tdb.Super.QueryRow(p.ctx, `SELECT
		(SELECT count(*) FROM finance_due_payments WHERE tenant_id=$1),
		(SELECT count(*) FROM finance_entries WHERE tenant_id=$1),
		(SELECT COALESCE(sum(CASE direction WHEN 'income' THEN amount_cents ELSE -amount_cents END),0) FROM finance_entries WHERE tenant_id=$1)`,
		p.tenant).Scan(&l.payments, &l.entries, &l.balance); err != nil {
		t.Fatal(err)
	}
	return l
}

func (p *payFixture) wantLedger(t *testing.T, want ledger) {
	t.Helper()
	if got := p.ledger(t); got != want {
		t.Fatalf("ledger changed: got %+v, want %+v", got, want)
	}
}

func (p *payFixture) betoStatus(t *testing.T) paidMemberJSON {
	t.Helper()
	rec := p.do(http.MethodGet, "/api/finance/dues/"+p.due, "", "198.51.100.10", p.officer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body)
	}
	var d paidDueJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	for _, m := range d.Members {
		if m.RosterMemberID == p.betoID {
			return m
		}
	}
	t.Fatalf("Beto missing from detail: %s", rec.Body)
	return paidMemberJSON{}
}

func (p *payFixture) wantBeto(t *testing.T, status string, paid int64) {
	t.Helper()
	if got := p.betoStatus(t); got.Status != status || got.PaidCents != paid {
		t.Fatalf("Beto = %+v, want status %s paid %d", got, status, paid)
	}
}

func (p *payFixture) entry(t *testing.T, token, id string) entryWithMemberJSON {
	t.Helper()
	rec := p.do(http.MethodGet, "/api/finance/entries", "", "198.51.100.10", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("entries: %d %s", rec.Code, rec.Body)
	}
	var entries []entryWithMemberJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("entry %s missing: %s", id, rec.Body)
	return entryWithMemberJSON{}
}

func TestDuePaymentsR1RecordPayment(t *testing.T) {
	p := newPayFixture(t)
	before := p.ledger(t)
	pay := p.payOK(t, 200000)
	if pay.DueID != p.due || pay.RosterMemberID != p.betoID || pay.AmountCents != 200000 || pay.Date != payDate || pay.EntryID == "" {
		t.Fatalf("unexpected payment: %+v", pay)
	}
	p.wantBeto(t, "paid", 200000)
	p.wantLedger(t, ledger{payments: before.payments + 1, entries: before.entries + 1, balance: before.balance + 200000})

	var cuotas string
	if err := tdb.Super.QueryRow(p.ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND direction='income' AND name='Cuotas'`, p.tenant).Scan(&cuotas); err != nil {
		t.Fatal(err)
	}
	e := p.entry(t, p.officer, pay.EntryID)
	if e.CategoryID != cuotas || e.Direction != "income" || e.AmountCents != 200000 || e.Date != payDate || e.Description != "Pago de cuota: Cuota de noviembre" {
		t.Fatalf("unexpected entry: %+v", e)
	}
	var balance struct{ BalanceCents int64 }
	if rec := p.do(http.MethodGet, "/api/finance/balance", "", "198.51.100.10", p.officer, nil); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &balance) != nil || balance.BalanceCents != before.balance+200000 {
		t.Fatalf("balance: %d %s", rec.Code, rec.Body)
	}

	// The date is required, a real calendar day and not in the future (as #68 R3); nothing is written.
	after := p.ledger(t)
	for _, tc := range []struct{ date, code string }{
		{"", "invalid_date"},
		{"13/11/2026", "invalid_date"},
		{"2026-02-30", "invalid_date"},
		{futureDate, "future_date"},
	} {
		wantResponse(t, p.pay(p.officer, p.betoID, 100, tc.date), http.StatusUnprocessableEntity, fmt.Sprintf(`{"error":%q}`, tc.code))
	}
	p.wantLedger(t, after)
}

func TestDuePaymentsR2PaymentAndEntryInOneTransaction(t *testing.T) {
	p := newPayFixture(t)
	// Make the second write (the payment row) fail for this school only, after its Entry was inserted.
	trigger := fmt.Sprintf(`CREATE FUNCTION fail_payment_%[1]s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.tenant_id = '%[2]s' THEN RAISE EXCEPTION 'forced failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER fail_payment_%[1]s BEFORE INSERT ON finance_due_payments FOR EACH ROW EXECUTE FUNCTION fail_payment_%[1]s();`,
		strings.ReplaceAll(p.tenant, "-", ""), p.tenant)
	if _, err := tdb.Super.Exec(p.ctx, trigger); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		name := "fail_payment_" + strings.ReplaceAll(p.tenant, "-", "")
		_, _ = tdb.Super.Exec(context.Background(), `DROP TRIGGER `+name+` ON finance_due_payments; DROP FUNCTION `+name+`()`)
	})
	before := p.ledger(t)
	wantResponse(t, p.pay(p.officer, p.betoID, 200000, payDate), http.StatusInternalServerError, `{"error":"internal"}`)
	p.wantLedger(t, before)
	p.wantBeto(t, "owing", 0)
}

func TestDuePaymentsR3PartialPayments(t *testing.T) {
	p := newPayFixture(t)
	before := p.ledger(t)
	first := p.payOK(t, 50000)
	p.wantBeto(t, "owing", 50000)
	second := p.payOK(t, 150000)
	p.wantBeto(t, "paid", 200000)
	if first.EntryID == second.EntryID {
		t.Fatalf("both payments share entry %s", first.EntryID)
	}
	p.wantLedger(t, ledger{payments: before.payments + 2, entries: before.entries + 2, balance: before.balance + 200000})
}

func TestDuePaymentsR4NoOverpayment(t *testing.T) {
	p := newPayFixture(t)
	p.payOK(t, 150000)
	before := p.ledger(t)
	wantResponse(t, p.pay(p.officer, p.betoID, 60000, payDate), http.StatusUnprocessableEntity, `{"error":"overpayment"}`)
	for _, body := range []string{
		fmt.Sprintf(`{"rosterMemberId":%q,"amountCents":0,"date":%q}`, p.betoID, payDate),
		fmt.Sprintf(`{"rosterMemberId":%q,"amountCents":-100,"date":%q}`, p.betoID, payDate),
		fmt.Sprintf(`{"rosterMemberId":%q,"amountCents":null,"date":%q}`, p.betoID, payDate),
		fmt.Sprintf(`{"rosterMemberId":%q,"date":%q}`, p.betoID, payDate),
	} {
		wantResponse(t, p.do(http.MethodPost, "/api/finance/dues/"+p.due+"/payments", body, "198.51.100.10", p.officer, nil), http.StatusUnprocessableEntity, `{"error":"invalid_amount"}`)
	}
	p.wantLedger(t, before)
	p.wantBeto(t, "owing", 150000)
}

func TestDuePaymentsR5ConcurrentPayments(t *testing.T) {
	p := newPayFixture(t)
	p.payOK(t, 150000)
	before := p.ledger(t)

	// Hold Beto's debt row so both requests are in flight together and must wait on it (the second
	// one queues behind the first on the same row).
	hold, err := tdb.Super.Begin(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(context.Background()) }()
	var one int
	if err := hold.QueryRow(p.ctx, `SELECT 1 FROM finance_due_members WHERE tenant_id=$1 AND due_id=$2 AND roster_member_id=$3 FOR UPDATE`, p.tenant, p.due, p.betoID).Scan(&one); err != nil {
		t.Fatal(err)
	}
	var holderPID int
	if err := hold.QueryRow(p.ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}

	recs := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i := range recs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recs[i] = p.pay(p.officer, p.betoID, 50000, payDate)
		}()
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		var waiting int
		if err := tdb.Super.QueryRow(p.ctx, `SELECT count(*) FROM pg_stat_activity WHERE pid <> $1 AND wait_event_type = 'Lock' AND query LIKE '%FROM finance_due_members%FOR UPDATE%'`, holderPID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		if time.Now().After(deadline) {
			_ = hold.Rollback(context.Background())
			wg.Wait()
			t.Fatalf("payments did not wait on the debt row lock: %d waiting", waiting)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := hold.Rollback(p.ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	codes := map[int]int{}
	for _, r := range recs {
		codes[r.Code]++
		if r.Code == http.StatusUnprocessableEntity && strings.TrimSpace(r.Body.String()) != `{"error":"overpayment"}` {
			t.Fatalf("loser body = %s", r.Body)
		}
	}
	if codes[http.StatusCreated] != 1 || codes[http.StatusUnprocessableEntity] != 1 {
		t.Fatalf("concurrent payments: %d %s / %d %s", recs[0].Code, recs[0].Body, recs[1].Code, recs[1].Body)
	}
	p.wantLedger(t, ledger{payments: before.payments + 1, entries: before.entries + 1, balance: before.balance + 50000})
	p.wantBeto(t, "paid", 200000)
}

func TestDuePaymentsR6OnlyWhoOwes(t *testing.T) {
	p := newPayFixture(t)
	other := newFixture(t, p.ctx)
	otherMember := seedMember(t, p.ctx, other.tenant, "Diego Ficticio", "30111225")
	before := p.ledger(t)
	wantResponse(t, p.pay(p.officer, p.carlaID, 50000, payDate), http.StatusUnprocessableEntity, `{"error":"not_owed"}`)
	for name, member := range map[string]string{
		"nonexistent":  "00000000-0000-0000-0000-000000000000",
		"not a uuid":   "beto",
		"other school": otherMember,
	} {
		t.Run(name, func(t *testing.T) {
			wantResponse(t, p.pay(p.officer, member, 50000, payDate), http.StatusNotFound, `{"error":"not_found"}`)
		})
	}
	p.wantLedger(t, before)
}

func TestDuePaymentsR7ReversalDiscountsPayment(t *testing.T) {
	p := newPayFixture(t)
	p.payOK(t, 50000)
	second := p.payOK(t, 150000)
	p.wantBeto(t, "paid", 200000)
	before := p.ledger(t)

	rec := p.do(http.MethodPost, "/api/finance/entries/"+second.EntryID+"/reversal", "", "198.51.100.10", p.officer, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("reverse payment entry: %d %s", rec.Code, rec.Body)
	}
	p.wantBeto(t, "owing", 50000)
	p.wantLedger(t, ledger{payments: before.payments, entries: before.entries + 1, balance: before.balance - 150000})

	wantResponse(t, p.do(http.MethodPost, "/api/finance/entries/"+second.EntryID+"/reversal", "", "198.51.100.10", p.officer, nil), http.StatusConflict, `{"error":"already_reversed"}`)
	p.wantBeto(t, "owing", 50000)

	// The discounted amount can be paid again.
	p.payOK(t, 150000)
	p.wantBeto(t, "paid", 200000)
}

func TestDuePaymentsR8FrozenWithPayment(t *testing.T) {
	p := newPayFixture(t)
	pay := p.payOK(t, 50000)
	stored := func() (name string, amount int64, date string) {
		t.Helper()
		if err := tdb.Super.QueryRow(p.ctx, `SELECT name, amount_cents, to_char(due_date,'YYYY-MM-DD') FROM finance_dues WHERE id=$1`, p.due).Scan(&name, &amount, &date); err != nil {
			t.Fatal(err)
		}
		return
	}
	frozen := func(t *testing.T) {
		t.Helper()
		for _, body := range []string{`{"amountCents":300000}`, `{"dueDate":"2026-12-31"}`} {
			wantResponse(t, p.do(http.MethodPatch, "/api/finance/dues/"+p.due, body, "198.51.100.10", p.officer, nil), http.StatusConflict, `{"error":"due_frozen"}`)
		}
		wantResponse(t, p.do(http.MethodDelete, "/api/finance/dues/"+p.due, "", "198.51.100.10", p.officer, nil), http.StatusConflict, `{"error":"due_frozen"}`)
		if name, amount, date := stored(); name != "Cuota de noviembre" || amount != 200000 || date != "2026-11-30" {
			t.Fatalf("frozen due changed: %s %d %s", name, amount, date)
		}
	}
	frozen(t)

	// Still frozen after every payment was reversed: there was money (owner, 2026-11-15).
	if rec := p.do(http.MethodPost, "/api/finance/entries/"+pay.EntryID+"/reversal", "", "198.51.100.10", p.officer, nil); rec.Code != http.StatusCreated {
		t.Fatalf("reverse: %d %s", rec.Code, rec.Body)
	}
	p.wantBeto(t, "owing", 0)
	frozen(t)
}

func TestDuePaymentsR9MembersCSV(t *testing.T) {
	p := newPayFixture(t)
	p.payOK(t, 50000)
	want := "\ufeffNombre;Pagado;Falta;Estado\r\n" +
		"Ana Ficticia;0,00;2000,00;Debe\r\n" +
		"Beto Ficticio;500,00;1500,00;Debe\r\n"
	for name, token := range map[string]string{"officer": p.officer, "school_admin": p.adminMFA} {
		t.Run(name, func(t *testing.T) {
			rec := p.do(http.MethodGet, "/api/finance/dues/"+p.due+"/members.csv", "", "198.51.100.10", token, nil)
			if rec.Code != http.StatusOK || rec.Body.String() != want {
				t.Fatalf("csv: %d %q", rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); ct != "text/csv; charset=utf-8" {
				t.Fatalf("content type %q", ct)
			}
			if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
				t.Fatalf("content disposition %q", cd)
			}
			if strings.Contains(rec.Body.String(), "30111223") {
				t.Fatal("csv carries a DNI")
			}
		})
	}
	p.payOK(t, 150000)
	rec := p.do(http.MethodGet, "/api/finance/dues/"+p.due+"/members.csv", "", "198.51.100.10", p.officer, nil)
	if !strings.Contains(rec.Body.String(), "Beto Ficticio;2000,00;0,00;Pagó\r\n") {
		t.Fatalf("paid row missing: %q", rec.Body.String())
	}

	wantResponse(t, p.do(http.MethodGet, "/api/finance/dues/"+p.due+"/members.csv", "", "198.51.100.10", p.beto, nil), http.StatusForbidden, `{"error":"forbidden"}`)
	wantResponse(t, p.do(http.MethodGet, "/api/finance/dues/"+p.due+"/members.csv", "", "198.51.100.10", "", nil), http.StatusUnauthorized, unauthBody)
}

func TestDuePaymentsR10StudentView(t *testing.T) {
	p := newPayFixture(t)
	pay := p.payOK(t, 50000)

	if e := p.entry(t, p.beto, pay.EntryID); e.RosterMember != nil || e.AmountCents != 50000 || e.Description != "Pago de cuota: Cuota de noviembre" {
		t.Fatalf("student sees payment entry %+v", e)
	}
	if rec := p.do(http.MethodGet, "/api/finance/entries", "", "198.51.100.10", p.beto, nil); strings.Contains(rec.Body.String(), "Beto Ficticio") || strings.Contains(rec.Body.String(), p.betoID) {
		t.Fatalf("student ledger names the payer: %s", rec.Body)
	}
	if rec := p.do(http.MethodGet, "/api/finance/entries/"+pay.EntryID, "", "198.51.100.10", p.beto, nil); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "rosterMember") {
		t.Fatalf("student entry detail names the payer: %d %s", rec.Code, rec.Body)
	}
	for name, token := range map[string]string{"officer": p.officer, "school_admin": p.adminMFA} {
		e := p.entry(t, token, pay.EntryID)
		if e.RosterMember == nil || e.RosterMember.ID != p.betoID || e.RosterMember.FullName != "Beto Ficticio" {
			t.Fatalf("%s does not see the payer: %+v", name, e)
		}
	}
	// Without a verified second factor, as for the per-student list, nobody sees who paid.
	_, officerNoMFA := p.signIn(t, p.ctx, "officer")
	_, adminNoMFA := p.signIn(t, p.ctx, "school_admin")
	for name, token := range map[string]string{"officer without MFA": officerNoMFA, "school_admin without MFA": adminNoMFA} {
		if e := p.entry(t, token, pay.EntryID); e.RosterMember != nil {
			t.Fatalf("%s sees the payer: %+v", name, e)
		}
		if rec := p.do(http.MethodGet, "/api/finance/entries/"+pay.EntryID, "", "198.51.100.10", token, nil); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "rosterMember") {
			t.Fatalf("%s entry detail names the payer: %d %s", name, rec.Code, rec.Body)
		}
	}

	rec := p.do(http.MethodGet, "/api/finance/dues/mine", "", "198.51.100.10", p.beto, nil)
	var mine []struct {
		ID          string `json:"id"`
		AmountCents int64  `json:"amountCents"`
		PaidCents   int64  `json:"paidCents"`
		Status      string `json:"status"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &mine) != nil || len(mine) != 1 || mine[0].ID != p.due || mine[0].PaidCents != 50000 || mine[0].Status != "owing" {
		t.Fatalf("mine: %d %s", rec.Code, rec.Body)
	}
	p.payOK(t, 150000)
	rec = p.do(http.MethodGet, "/api/finance/dues/mine", "", "198.51.100.10", p.beto, nil)
	if json.Unmarshal(rec.Body.Bytes(), &mine) != nil || mine[0].PaidCents != 200000 || mine[0].Status != "paid" {
		t.Fatalf("mine after full payment: %s", rec.Body)
	}
	var list []dueSummaryJSON
	if rec := p.do(http.MethodGet, "/api/finance/dues", "", "198.51.100.10", p.officer, nil); json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list) != 1 || list[0].OwingCount != 1 || list[0].TotalCount != 2 {
		t.Fatalf("list counts: %s", rec.Body)
	}
}

func TestDuePaymentsR11WriteAuthorization(t *testing.T) {
	p := newPayFixture(t)
	_, officerNoMFA := p.signIn(t, p.ctx, "officer")
	before := p.ledger(t)
	for _, c := range []struct {
		name, token string
		status      int
		body        string
	}{
		{"no session", "", http.StatusUnauthorized, unauthBody},
		{"student", p.beto, http.StatusForbidden, `{"error":"forbidden"}`},
		{"school_admin", p.adminMFA, http.StatusForbidden, `{"error":"forbidden"}`},
		{"officer without MFA", officerNoMFA, http.StatusForbidden, `{"error":"mfa_required"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			wantResponse(t, p.pay(c.token, p.betoID, 50000, payDate), c.status, c.body)
		})
	}
	// MFA is checked before the body is read.
	wantResponse(t, p.do(http.MethodPost, "/api/finance/dues/"+p.due+"/payments", `{not-json`, "198.51.100.10", officerNoMFA, nil), http.StatusForbidden, `{"error":"mfa_required"}`)
	p.wantLedger(t, before)
	p.wantBeto(t, "owing", 0)
}

func TestDuePaymentsR12TenantIsolation(t *testing.T) {
	p := newPayFixture(t)
	other := newPayFixture(t)
	before := p.ledger(t)
	otherBefore := other.ledger(t)
	for _, id := range []string{other.due, "not-a-uuid", "00000000-0000-0000-0000-000000000000"} {
		body := fmt.Sprintf(`{"rosterMemberId":%q,"amountCents":50000,"date":%q}`, other.betoID, payDate)
		wantResponse(t, p.do(http.MethodPost, "/api/finance/dues/"+id+"/payments", body, "198.51.100.10", p.officer, nil), http.StatusNotFound, `{"error":"not_found"}`)
		wantResponse(t, p.do(http.MethodGet, "/api/finance/dues/"+id+"/members.csv", "", "198.51.100.10", p.officer, nil), http.StatusNotFound, `{"error":"not_found"}`)
	}
	p.wantLedger(t, before)
	other.wantLedger(t, otherBefore)
}

func TestDuePaymentsR13Privacy(t *testing.T) {
	p := newPayFixture(t)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	p.payOK(t, 150000)
	for _, r := range []struct {
		rec  *httptest.ResponseRecorder
		body string
	}{
		{p.pay(p.officer, p.betoID, 60000, payDate), `{"error":"overpayment"}`},
		{p.pay(p.officer, p.carlaID, 100, payDate), `{"error":"not_owed"}`},
		{p.pay(p.officer, p.betoID, 100, futureDate), `{"error":"future_date"}`},
	} {
		wantResponse(t, r.rec, http.StatusUnprocessableEntity, r.body)
	}
	p.do(http.MethodGet, "/api/finance/dues/"+p.due+"/members.csv", "", "198.51.100.10", p.officer, nil)
	for _, leak := range []string{"Beto Ficticio", "Carla Ficticia", "30111223", "30111224", p.betoID, p.carlaID} {
		if strings.Contains(buf.String(), leak) {
			t.Fatalf("logs leaked %q: %s", leak, buf.String())
		}
	}
}
