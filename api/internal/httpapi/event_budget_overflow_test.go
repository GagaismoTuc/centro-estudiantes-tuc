package httpapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// overflowBudget creates a one-day budget and returns its API path.
func overflowBudget(t *testing.T, f *fixture, ip, token string) (string, string) {
	t.Helper()
	r := f.do("POST", "/api/event-budgets", `{"name":"Week","startsOn":"2026-09-21","endsOn":"2026-09-25"}`, ip, token, nil)
	var b struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &b); err != nil || r.Code != 201 {
		t.Fatalf("budget: %d %s %v", r.Code, r.Body, err)
	}
	return b.ID, "/api/event-budgets/" + b.ID
}

type overflowLine struct {
	ID       string `json:"id"`
	Quantity int64  `json:"quantity"`
	Amount   int64  `json:"unitAmountCents"`
}

type overflowDetail struct {
	Lines  []overflowLine `json:"lines"`
	Totals struct {
		CostCents int64 `json:"costCents"`
	} `json:"totals"`
}

func getOverflowDetail(t *testing.T, f *fixture, path, ip, token string) overflowDetail {
	t.Helper()
	rec := f.do("GET", path, "", ip, token, nil)
	var d overflowDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil || rec.Code != 200 {
		t.Fatalf("GET budget = %d %s %v", rec.Code, rec.Body, err)
	}
	return d
}

// R1: a line whose total does not fit is rejected with 422 invalid_amount, nothing is written,
// and the budget keeps opening.
func TestEventBudgetRejectsLineThatOverflowsTotals(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	headings := seedHeadingsForTenant(t, ctx, f.tenant)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	const ip = "198.51.100.81"
	id, path := overflowBudget(t, f, ip, officer)
	huge := `{"kind":"cost","headingId":"` + headings["gazebos"] + `","description":"Huge","quantity":99999999,"unitAmountCents":9999999999900}`
	wantResponse(t, f.do("POST", path+"/lines", huge, ip, officer, nil), 422, `{"error":"invalid_amount"}`)
	var count int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM event_budget_lines WHERE budget_id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected line persisted %d rows: %v", count, err)
	}
	if d := getOverflowDetail(t, f, path, ip, officer); len(d.Lines) != 0 || d.Totals.CostCents != 0 {
		t.Fatalf("detail after rejected POST: %+v", d)
	}

	// The same rule on PUT: editing a line into an overflowing one leaves it unchanged.
	small := `{"kind":"cost","headingId":"` + headings["gazebos"] + `","description":"Small","quantity":1,"unitAmountCents":100}`
	r := f.do("POST", path+"/lines", small, ip, officer, nil)
	var line overflowLine
	if err := json.Unmarshal(r.Body.Bytes(), &line); err != nil || r.Code != 201 {
		t.Fatalf("small line: %d %s %v", r.Code, r.Body, err)
	}
	wantResponse(t, f.do("PUT", path+"/lines/"+line.ID, huge, ip, officer, nil), 422, `{"error":"invalid_amount"}`)
	var desc string
	var qty, amount int64
	if err := tdb.Super.QueryRow(ctx, `SELECT description,quantity,unit_amount_cents FROM event_budget_lines WHERE id=$1`, line.ID).Scan(&desc, &qty, &amount); err != nil || desc != "Small" || qty != 1 || amount != 100 {
		t.Fatalf("rejected PUT changed the line: %q %d %d %v", desc, qty, amount, err)
	}
	if d := getOverflowDetail(t, f, path, ip, officer); len(d.Lines) != 1 || d.Totals.CostCents != 100 {
		t.Fatalf("detail after rejected PUT: %+v", d)
	}
}

// R2: two lines that fit alone but overflow together, sent at the same time: one 201, one 422.
func TestEventBudgetParallelLinesThatOverflowTogether(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	headings := seedHeadingsForTenant(t, ctx, f.tenant)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	_, other, _, _ := f.signInMFA(t, ctx, "officer")
	const ip = "198.51.100.82"
	id, path := overflowBudget(t, f, ip, officer)
	// 5e18 fits in int64 (max ~9.22e18); two of them do not.
	body := `{"kind":"cost","headingId":"` + headings["gazebos"] + `","description":"Half","quantity":1,"unitAmountCents":5000000000000000000}`

	// Hold the budget row so both requests are in flight and wait on the same FOR UPDATE.
	lock, err := tdb.Super.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Rollback(context.Background()) })
	if _, err := lock.Exec(ctx, `SELECT id FROM event_budgets WHERE id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	type reply struct {
		code int
		body string
	}
	results := make(chan reply, 2)
	for _, token := range []string{officer, other} {
		go func(token string) {
			rec := f.do("POST", path+"/lines", body, ip, token, nil)
			results <- reply{rec.Code, strings.TrimSpace(rec.Body.String())}
		}(token)
	}
	waitForLockedQueries(t, ctx, "%SELECT starts_on::text,ends_on::text,archived_at IS NOT NULL FROM event_budgets%FOR UPDATE%", 2)
	if err := lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := map[int]int{}
	for i := 0; i < 2; i++ {
		r := <-results
		got[r.code]++
		if r.code == 422 && r.body != `{"error":"invalid_amount"}` {
			t.Fatalf("422 body %s, want invalid_amount", r.body)
		}
	}
	if got[201] != 1 || got[422] != 1 {
		t.Fatalf("parallel statuses %v, want one 201 and one 422", got)
	}
	if d := getOverflowDetail(t, f, path, ip, officer); len(d.Lines) != 1 || d.Totals.CostCents != 5000000000000000000 {
		t.Fatalf("detail after parallel adds: %+v", d)
	}
}

// R3: an ordinary large line is accepted and totals correctly.
func TestEventBudgetAcceptsLargeLineThatFits(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	headings := seedHeadingsForTenant(t, ctx, f.tenant)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	const ip = "198.51.100.83"
	_, path := overflowBudget(t, f, ip, officer)
	body := `{"kind":"cost","headingId":"` + headings["gazebos"] + `","description":"Sillas","quantity":1000,"unitAmountCents":500000}`
	if r := f.do("POST", path+"/lines", body, ip, officer, nil); r.Code != 201 {
		t.Fatalf("line: %d %s", r.Code, r.Body)
	}
	if d := getOverflowDetail(t, f, path, ip, officer); len(d.Lines) != 1 || d.Totals.CostCents != 500000000 {
		t.Fatalf("detail: %+v", d)
	}
}
