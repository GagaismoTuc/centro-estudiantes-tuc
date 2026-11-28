package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func linkedOverflowBudget(t *testing.T, f *fixture, ip, token string) (string, string) {
	t.Helper()
	r := f.do("POST", "/api/event-budgets", `{"name":"Semana","startsOn":"2026-09-21","endsOn":"2026-09-25"}`, ip, token, nil)
	var budget struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &budget); err != nil || r.Code != 201 {
		t.Fatalf("budget: %d %s %v", r.Code, r.Body, err)
	}
	return budget.ID, "/api/event-budgets/" + budget.ID
}

func TestLinkedEntryRejectsOverflow(t *testing.T) {
	ctx := integration(t)
	f, officer, _, _, _, category, _, headings := setupBudgetActualFixture(t, ctx)
	const ip = "198.51.100.81"
	id, path := linkedOverflowBudget(t, f, ip, officer)
	line := fmt.Sprintf(`{"kind":"cost","headingId":%q,"description":"Gazebo","quantity":1,"unitAmountCents":500000}`, headings["gazebos"])
	if r := f.do(http.MethodPost, path+"/lines", line, ip, officer, nil); r.Code != http.StatusCreated {
		t.Fatalf("line: %d %s", r.Code, r.Body)
	}
	body := fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":"2026-11-14","amountCents":9999999999999999900,"description":"Huge","eventBudgetId":%q,"headingId":%q}`, category, id, headings["gazebos"])
	wantResponse(t, f.do(http.MethodPost, "/api/finance/entries", body, ip, officer, nil), 422, `{"error":"invalid_amount"}`)
	first := fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":"2026-11-14","amountCents":5000000000000000000,"description":"First","eventBudgetId":%q,"headingId":%q}`, category, id, headings["gazebos"])
	if r := f.do(http.MethodPost, "/api/finance/entries", first, ip, officer, nil); r.Code != 201 {
		t.Fatalf("first entry: %d %s", r.Code, r.Body)
	}
	wantResponse(t, f.do(http.MethodPost, "/api/finance/entries", first, ip, officer, nil), 422, `{"error":"invalid_amount"}`)

	var n int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM finance_entries WHERE event_budget_id=$1`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("entry count = %d: %v", n, err)
	}
	getOverflowDetail(t, f, path, ip, officer)
}

func TestParallelLinkedEntriesRejectOverflow(t *testing.T) {
	ctx := integration(t)
	f, officer, _, _, _, category, _, headings := setupBudgetActualFixture(t, ctx)
	const ip = "198.51.100.82"
	id, path := linkedOverflowBudget(t, f, ip, officer)
	line := fmt.Sprintf(`{"kind":"cost","headingId":%q,"description":"Gazebo","quantity":1,"unitAmountCents":500000}`, headings["gazebos"])
	if r := f.do("POST", path+"/lines", line, ip, officer, nil); r.Code != 201 {
		t.Fatalf("line: %d %s", r.Code, r.Body)
	}
	lock, err := tdb.Super.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(context.Background())
	if _, err = lock.Exec(ctx, `SELECT id FROM event_budgets WHERE id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":"2026-11-14","amountCents":5000000000000000000,"description":"Huge","eventBudgetId":%q,"headingId":%q}`, category, id, headings["gazebos"])
	type reply struct {
		code int
		body string
	}
	results := make(chan reply, 2)
	for range 2 {
		go func() {
			r := f.do("POST", "/api/finance/entries", body, ip, officer, nil)
			results <- reply{r.Code, strings.TrimSpace(r.Body.String())}
		}()
	}
	waitForLockedQueries(t, ctx, "%SELECT name, archived_at IS NOT NULL, starts_on::text%FOR UPDATE%", 2)
	if err = lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	codes := map[int]int{}
	for range 2 {
		r := <-results
		codes[r.code]++
		if r.code == 422 && r.body != `{"error":"invalid_amount"}` {
			t.Fatalf("body: %s", r.body)
		}
	}
	if codes[201] != 1 || codes[422] != 1 {
		t.Fatalf("statuses: %v", codes)
	}
	var n int
	if err = tdb.Super.QueryRow(ctx, `SELECT count(*) FROM finance_entries WHERE event_budget_id=$1`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("count %d: %v", n, err)
	}
	getOverflowDetail(t, f, path, ip, officer)
}

func TestLinkedEntryAppearsInHeadingComparison(t *testing.T) {
	ctx := integration(t)
	f, officer, _, _, _, category, _, headings := setupBudgetActualFixture(t, ctx)
	const ip = "198.51.100.83"
	id, path := linkedOverflowBudget(t, f, ip, officer)
	line := fmt.Sprintf(`{"kind":"cost","headingId":%q,"description":"Gazebo","quantity":1,"unitAmountCents":500000}`, headings["gazebos"])
	if r := f.do("POST", path+"/lines", line, ip, officer, nil); r.Code != 201 {
		t.Fatalf("line %d %s", r.Code, r.Body)
	}
	body := fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":"2026-11-14","amountCents":100000,"description":"Gazebo","eventBudgetId":%q,"headingId":%q}`, category, id, headings["gazebos"])
	r := f.do("POST", "/api/finance/entries", body, ip, officer, nil)
	if r.Code != 201 {
		t.Fatalf("entry %d %s", r.Code, r.Body)
	}
	var entry linkedEntryDTO
	if err := json.Unmarshal(r.Body.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	rec := f.do("GET", path, "", ip, officer, nil)
	var detail budgetDetailWithActualDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || rec.Code != 200 || len(detail.ByHeading) != 1 || detail.ByHeading[0].ActualExpenseCents != 100000 || detail.ByHeading[0].ExpenseDifferenceCents != 400000 {
		t.Fatalf("detail %d %s %v", rec.Code, rec.Body, err)
	}
	rev := f.do("POST", "/api/finance/entries/"+entry.ID+"/reversal", "", ip, officer, nil)
	if rev.Code != 201 {
		t.Fatalf("reversal %d %s", rev.Code, rev.Body)
	}
	rec = f.do("GET", path, "", ip, officer, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || rec.Code != 200 || len(detail.ByHeading) != 1 || detail.ByHeading[0].ActualExpenseCents != 0 {
		t.Fatalf("reversed detail %d %s %v", rec.Code, rec.Body, err)
	}
}

func TestUnlinkedEntryDoesNotReadBudget(t *testing.T) {
	ctx := integration(t)
	f, officer, _, _, _, category, _, _ := setupBudgetActualFixture(t, ctx)
	const ip = "198.51.100.84"
	id, _ := linkedOverflowBudget(t, f, ip, officer)
	lock, err := tdb.Super.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(context.Background())
	if _, err = lock.Exec(ctx, `SELECT id FROM event_budgets WHERE id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":"2026-11-14","amountCents":100000,"description":"Unlinked"}`, category)
	done := make(chan int, 1)
	go func() { done <- f.do("POST", "/api/finance/entries", body, ip, officer, nil).Code }()
	select {
	case code := <-done:
		if code != 201 {
			t.Fatalf("status %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unlinked entry waited on budget row lock")
	}
}

func TestParallelBudgetLineAndLinkedEntry(t *testing.T) {
	ctx := integration(t)
	f, officer, _, _, _, category, _, headings := setupBudgetActualFixture(t, ctx)
	const ip = "198.51.100.85"
	id, path := linkedOverflowBudget(t, f, ip, officer)
	line := fmt.Sprintf(`{"kind":"cost","headingId":%q,"description":"Gazebo","quantity":1,"unitAmountCents":500000}`, headings["gazebos"])
	if r := f.do("POST", path+"/lines", line, ip, officer, nil); r.Code != 201 {
		t.Fatalf("line %d %s", r.Code, r.Body)
	}
	lock, err := tdb.Super.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(context.Background())
	if _, err = lock.Exec(ctx, `SELECT id FROM event_budgets WHERE id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":"2026-11-14","amountCents":100000,"description":"Gazebo","eventBudgetId":%q,"headingId":%q}`, category, id, headings["gazebos"])
	replies := make(chan int, 2)
	go func() { replies <- f.do("POST", path+"/lines", line, ip, officer, nil).Code }()
	go func() { replies <- f.do("POST", "/api/finance/entries", body, ip, officer, nil).Code }()
	waitForLockedQueries(t, ctx, "%SELECT starts_on::text,ends_on::text,archived_at IS NOT NULL FROM event_budgets%FOR UPDATE%", 1)
	waitForLockedQueries(t, ctx, "%SELECT name, archived_at IS NOT NULL, starts_on::text%FOR UPDATE%", 1)
	if err = lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case code := <-replies:
			if code != 201 {
				t.Fatalf("status %d", code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent operations did not finish")
		}
	}
	detail := getOverflowDetail(t, f, path, ip, officer)
	if len(detail.Lines) != 2 {
		t.Fatalf("lines: %d", len(detail.Lines))
	}
	var n int
	if err = tdb.Super.QueryRow(ctx, `SELECT count(*) FROM finance_entries WHERE event_budget_id=$1`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("entries %d: %v", n, err)
	}
}
