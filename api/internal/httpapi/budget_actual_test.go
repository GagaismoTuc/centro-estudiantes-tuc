package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type linkedEntryDTO struct {
	ID              string `json:"id"`
	CategoryID      string `json:"categoryId"`
	Direction       string `json:"direction"`
	Date            string `json:"date"`
	AmountCents     int64  `json:"amountCents"`
	Description     string `json:"description"`
	RecordedBy      string `json:"recordedBy"`
	ReversesID      string `json:"reversesId"`
	EventBudgetID   string `json:"eventBudgetId"`
	EventBudgetName string `json:"eventBudgetName"`
	HeadingID       string `json:"headingId"`
	HeadingName     string `json:"headingName"`
}

func setupBudgetActualFixture(t *testing.T, ctx context.Context) (f *fixture, officerToken, studentToken, adminToken, officerNoMFAToken string, alquileresCat, ventasCat string, headings map[string]string) {
	t.Helper()
	f = newFixture(t, ctx)
	f.clk.t = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, f.tenant); err != nil {
		t.Fatal(err)
	}
	headings = seedHeadingsForTenant(t, ctx, f.tenant)
	officerUser, officer, _, _ := f.signInMFA(t, ctx, "officer")
	member := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, member, f.tenant, officerUser); err != nil {
		t.Fatal(err)
	}
	_, student := f.signIn(t, ctx, "student")
	_, admin, _, _ := f.signInMFA(t, ctx, "school_admin")
	_, noMFA := f.signIn(t, ctx, "officer")

	cat := func(name, dir string) string {
		t.Helper()
		var id string
		if err := tdb.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name=$2 AND direction=$3`, f.tenant, name, dir).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	return f, officer, student, admin, noMFA, cat("Alquileres", "expense"), cat("Ventas", "income"), headings
}

func TestBudgetEntryLinkingAndDeleteProtection(t *testing.T) {
	ctx := integration(t)
	f, officer, student, admin, officerNoMFA, alquileres, ventas, headings := setupBudgetActualFixture(t, ctx)
	other := newTenant(t, ctx)
	foreignHeadings := seedHeadingsForTenant(t, ctx, other)
	var foreignBudget string
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO event_budgets(tenant_id,name,starts_on,ends_on) VALUES ($1,'Otra escuela','2026-09-21','2026-09-25') RETURNING id::text`, other).Scan(&foreignBudget); err != nil {
		t.Fatal(err)
	}
	const ip = "198.51.100.81"

	// Create "Semana del Estudiante 2026" with a "mesas y sillas" cost line ($100.000).
	rec := f.do(http.MethodPost, "/api/event-budgets", `{"name":"Semana del Estudiante 2026","startsOn":"2026-09-21","endsOn":"2026-09-25"}`, ip, officer, nil)
	if rec.Code != 201 {
		t.Fatalf("create budget = %d %s", rec.Code, rec.Body)
	}
	var budget struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &budget); err != nil {
		t.Fatal(err)
	}
	lineBody := fmt.Sprintf(`{"kind":"cost","headingId":%q,"description":"Alquiler mesas y sillas","quantity":1,"unitAmountCents":10000000}`, headings["mesas y sillas"])
	if rec := f.do(http.MethodPost, "/api/event-budgets/"+budget.ID+"/lines", lineBody, ip, officer, nil); rec.Code != 201 {
		t.Fatalf("add line = %d %s", rec.Code, rec.Body)
	}

	snapshot := func() string {
		t.Helper()
		var n int
		var balance string
		if err := tdb.Super.QueryRow(ctx, `SELECT count(*), COALESCE(sum(CASE direction WHEN 'income' THEN amount_cents ELSE -amount_cents END),0)::text FROM finance_entries WHERE tenant_id=$1`, f.tenant).Scan(&n, &balance); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%d:%s", n, balance)
	}

	t.Run("R2 rejects invalid link combinations without writing anything", func(t *testing.T) {
		before := snapshot()
		cases := []struct {
			name   string
			body   string
			status int
			code   string
		}{
			{
				name:   "heading without lines in budget",
				body:   fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":"2026-11-14","amountCents":11000000,"description":"Banda","eventBudgetId":%q,"headingId":%q}`, alquileres, budget.ID, headings["artistas"]),
				status: 422,
				code:   "heading_not_in_budget",
			},
			{
				name:   "only eventBudgetId",
				body:   fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":"2026-11-14","amountCents":11000000,"description":"Sillas","eventBudgetId":%q}`, alquileres, budget.ID),
				status: 422,
				code:   "invalid_event_link",
			},
			{
				name:   "only headingId",
				body:   fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":"2026-11-14","amountCents":11000000,"description":"Sillas","headingId":%q}`, alquileres, headings["mesas y sillas"]),
				status: 422,
				code:   "invalid_event_link",
			},
			{
				name:   "foreign event budget",
				body:   fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":"2026-11-14","amountCents":11000000,"description":"Sillas","eventBudgetId":%q,"headingId":%q}`, alquileres, foreignBudget, headings["mesas y sillas"]),
				status: 404,
				code:   "not_found",
			},
			{
				name:   "foreign heading",
				body:   fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":"2026-11-14","amountCents":11000000,"description":"Sillas","eventBudgetId":%q,"headingId":%q}`, alquileres, budget.ID, foreignHeadings["mesas y sillas"]),
				status: 404,
				code:   "not_found",
			},
		}
		for _, tc := range cases {
			rec := f.do(http.MethodPost, "/api/finance/entries", tc.body, ip, officer, nil)
			wantResponse(t, rec, tc.status, `{"error":"`+tc.code+`"}`)
			if got := snapshot(); got != before {
				t.Fatalf("%s mutated ledger: before=%s after=%s", tc.name, before, got)
			}
		}
	})

	var linked linkedEntryDTO
	t.Run("R1 Ana Ficticia links $110.000 Alquiler de sillas to Semana del Estudiante 2026 / mesas y sillas", func(t *testing.T) {
		body := fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":"2026-11-14","amountCents":11000000,"description":"Alquiler de sillas","eventBudgetId":%q,"headingId":%q}`, alquileres, budget.ID, headings["mesas y sillas"])
		rec := f.do(http.MethodPost, "/api/finance/entries", body, ip, officer, nil)
		if rec.Code != 201 {
			t.Fatalf("post linked entry = %d %s", rec.Code, rec.Body)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &linked); err != nil {
			t.Fatal(err)
		}
		if linked.EventBudgetID != budget.ID || linked.EventBudgetName != "Semana del Estudiante 2026" || linked.HeadingID != headings["mesas y sillas"] || linked.HeadingName != "mesas y sillas" {
			t.Fatalf("unexpected linked entry response: %+v", linked)
		}
	})

	t.Run("R5 DELETE with linked entry returns 409 budget_has_entries and keeps budget", func(t *testing.T) {
		rec := f.do(http.MethodDelete, "/api/event-budgets/"+budget.ID, "", ip, officer, nil)
		wantResponse(t, rec, 409, `{"error":"budget_has_entries"}`)
		if get := f.do(http.MethodGet, "/api/event-budgets/"+budget.ID, "", ip, officer, nil); get.Code != 200 {
			t.Fatalf("budget disappeared after 409 delete: %d %s", get.Code, get.Body)
		}
	})

	t.Run("R5 archived budget rejects new linked entries with 422 invalid_event_link without writing", func(t *testing.T) {
		if rec := f.do(http.MethodPost, "/api/event-budgets/"+budget.ID+"/archive", "", ip, officer, nil); rec.Code != 200 {
			t.Fatalf("archive = %d %s", rec.Code, rec.Body)
		}
		before := snapshot()
		body := fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":"2026-11-14","amountCents":500000,"description":"Extra sillas","eventBudgetId":%q,"headingId":%q}`, alquileres, budget.ID, headings["mesas y sillas"])
		rec := f.do(http.MethodPost, "/api/finance/entries", body, ip, officer, nil)
		wantResponse(t, rec, 422, `{"error":"invalid_event_link"}`)
		if got := snapshot(); got != before {
			t.Fatalf("archived budget rejection mutated ledger: before=%s after=%s", before, got)
		}
	})

	t.Run("R4 reversal inherits event budget and heading even when budget is archived", func(t *testing.T) {
		rec := f.do(http.MethodPost, "/api/finance/entries/"+linked.ID+"/reversal", "", ip, officer, nil)
		if rec.Code != 201 {
			t.Fatalf("reverse = %d %s", rec.Code, rec.Body)
		}
		var rev linkedEntryDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &rev); err != nil {
			t.Fatal(err)
		}
		if rev.ReversesID != linked.ID || rev.EventBudgetID != budget.ID || rev.EventBudgetName != "Semana del Estudiante 2026" || rev.HeadingID != headings["mesas y sillas"] || rev.HeadingName != "mesas y sillas" {
			t.Fatalf("reversal did not inherit link: %+v", rev)
		}
		// Read back via GET to verify the inherited link was persisted, not only populated in memory.
		persistedRec := f.do(http.MethodGet, "/api/finance/entries/"+rev.ID, "", ip, officer, nil)
		if persistedRec.Code != 200 {
			t.Fatalf("get persisted reversal = %d %s", persistedRec.Code, persistedRec.Body)
		}
		var persistedRev linkedEntryDTO
		if err := json.Unmarshal(persistedRec.Body.Bytes(), &persistedRev); err != nil {
			t.Fatal(err)
		}
		if persistedRev.ReversesID != linked.ID || persistedRev.EventBudgetID != budget.ID || persistedRev.EventBudgetName != "Semana del Estudiante 2026" || persistedRev.HeadingID != headings["mesas y sillas"] || persistedRev.HeadingName != "mesas y sillas" {
			t.Fatalf("persisted reversal missing inherited link: %+v", persistedRev)
		}
	})

	t.Run("R6 authorization and student ledger visibility", func(t *testing.T) {
		before := snapshot()
		linkBody := fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":"2026-11-14","amountCents":50000,"description":"Intento","eventBudgetId":%q,"headingId":%q}`, alquileres, budget.ID, headings["mesas y sillas"])
		wantResponse(t, f.do(http.MethodPost, "/api/finance/entries", linkBody, ip, officerNoMFA, nil), 403, `{"error":"mfa_required"}`)
		wantResponse(t, f.do(http.MethodPost, "/api/finance/entries", linkBody, ip, student, nil), 403, `{"error":"forbidden"}`)
		wantResponse(t, f.do(http.MethodPost, "/api/finance/entries", linkBody, ip, admin, nil), 403, `{"error":"forbidden"}`)
		if got := snapshot(); got != before {
			t.Fatalf("unauthorized post mutated ledger: before=%s after=%s", before, got)
		}

		// Students cannot read GET /event-budgets/{id}; school admin with MFA can.
		wantResponse(t, f.do(http.MethodGet, "/api/event-budgets/"+budget.ID, "", ip, student, nil), 403, `{"error":"forbidden"}`)
		if rec := f.do(http.MethodGet, "/api/event-budgets/"+budget.ID, "", ip, admin, nil); rec.Code != 200 {
			t.Fatalf("school_admin get budget = %d %s", rec.Code, rec.Body)
		}

		// Students see the Event name on linked entries in the ledger list and single-entry view.
		listRec := f.do(http.MethodGet, "/api/finance/entries", "", ip, student, nil)
		if listRec.Code != 200 {
			t.Fatalf("student list entries = %d %s", listRec.Code, listRec.Body)
		}
		var list []linkedEntryDTO
		if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range list {
			if item.ID == linked.ID {
				found = true
				if item.EventBudgetName != "Semana del Estudiante 2026" || item.HeadingName != "mesas y sillas" {
					t.Fatalf("student list entry missing event name: %+v", item)
				}
			}
		}
		if !found {
			t.Fatalf("linked entry %s missing from student list", linked.ID)
		}
		getRec := f.do(http.MethodGet, "/api/finance/entries/"+linked.ID, "", ip, student, nil)
		if getRec.Code != 200 {
			t.Fatalf("student get entry = %d %s", getRec.Code, getRec.Body)
		}
		var gotEntry linkedEntryDTO
		if err := json.Unmarshal(getRec.Body.Bytes(), &gotEntry); err != nil {
			t.Fatal(err)
		}
		if gotEntry.EventBudgetName != "Semana del Estudiante 2026" || gotEntry.HeadingName != "mesas y sillas" {
			t.Fatalf("student get entry missing event name: %+v", gotEntry)
		}
	})

	t.Run("PRESERVE unlinked entries, reversals, report, csv and deleting unlinked budget", func(t *testing.T) {
		unlinkedBody := fmt.Sprintf(`{"categoryId":%q,"direction":"income","date":"2026-11-14","amountCents":250000,"description":"Rifa escolar"}`, ventas)
		rec := f.do(http.MethodPost, "/api/finance/entries", unlinkedBody, ip, officer, nil)
		if rec.Code != 201 {
			t.Fatalf("post unlinked entry = %d %s", rec.Code, rec.Body)
		}
		var unlinked linkedEntryDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &unlinked); err != nil {
			t.Fatal(err)
		}
		if unlinked.EventBudgetID != "" || unlinked.EventBudgetName != "" || unlinked.HeadingID != "" || unlinked.HeadingName != "" {
			t.Fatalf("unlinked entry has event fields: %+v", unlinked)
		}
		getUnlinked := f.do(http.MethodGet, "/api/finance/entries/"+unlinked.ID, "", ip, officer, nil)
		if getUnlinked.Code != 200 {
			t.Fatalf("get unlinked entry = %d %s", getUnlinked.Code, getUnlinked.Body)
		}
		var gotUnlinked linkedEntryDTO
		if err := json.Unmarshal(getUnlinked.Body.Bytes(), &gotUnlinked); err != nil {
			t.Fatal(err)
		}
		if gotUnlinked.ID != unlinked.ID || gotUnlinked.EventBudgetID != "" || gotUnlinked.HeadingID != "" {
			t.Fatalf("unexpected get unlinked entry: %+v", gotUnlinked)
		}
		listRec := f.do(http.MethodGet, "/api/finance/entries", "", ip, officer, nil)
		if listRec.Code != 200 {
			t.Fatalf("list entries = %d %s", listRec.Code, listRec.Body)
		}
		var list []linkedEntryDTO
		if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		foundUnlinked := false
		for _, item := range list {
			if item.ID == unlinked.ID {
				foundUnlinked = true
				if item.EventBudgetID != "" || item.HeadingID != "" {
					t.Fatalf("unlinked entry in list has event fields: %+v", item)
				}
			}
		}
		if !foundUnlinked {
			t.Fatalf("unlinked entry %s missing from list", unlinked.ID)
		}
		revRec := f.do(http.MethodPost, "/api/finance/entries/"+unlinked.ID+"/reversal", "", ip, officer, nil)
		if revRec.Code != 201 {
			t.Fatalf("reverse unlinked entry = %d %s", revRec.Code, revRec.Body)
		}
		var rev linkedEntryDTO
		if err := json.Unmarshal(revRec.Body.Bytes(), &rev); err != nil {
			t.Fatal(err)
		}
		if rev.EventBudgetID != "" || rev.HeadingID != "" {
			t.Fatalf("unlinked reversal has event fields: %+v", rev)
		}
		if rep := f.do(http.MethodGet, "/api/finance/report?from=2026-11-01&to=2026-11-30", "", ip, student, nil); rep.Code != 200 || !strings.Contains(rep.Body.String(), "Alquiler de sillas") || !strings.Contains(rep.Body.String(), "Rifa escolar") {
			t.Fatalf("finance report = %d %s", rep.Code, rep.Body)
		}
		if repCSV := f.do(http.MethodGet, "/api/finance/report.csv?from=2026-11-01&to=2026-11-30", "", ip, student, nil); repCSV.Code != 200 || !strings.Contains(repCSV.Body.String(), "Alquiler de sillas") || !strings.Contains(repCSV.Body.String(), "Rifa escolar") {
			t.Fatalf("finance report csv = %d %s", repCSV.Code, repCSV.Body)
		}
		if csvRec := f.do(http.MethodGet, "/api/finance/entries.csv", "", ip, student, nil); csvRec.Code != 200 || !strings.Contains(csvRec.Body.String(), "Alquiler de sillas") || !strings.Contains(csvRec.Body.String(), "Rifa escolar") {
			t.Fatalf("finance csv = %d %s", csvRec.Code, csvRec.Body)
		}
		fresh := f.do(http.MethodPost, "/api/event-budgets", `{"name":"Peña sin gastos","startsOn":"2026-10-01","endsOn":"2026-10-02"}`, ip, officer, nil)
		if fresh.Code != 201 {
			t.Fatalf("create fresh budget = %d %s", fresh.Code, fresh.Body)
		}
		var emptyBudget struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(fresh.Body.Bytes(), &emptyBudget); err != nil {
			t.Fatal(err)
		}
		if del := f.do(http.MethodDelete, "/api/event-budgets/"+emptyBudget.ID, "", ip, officer, nil); del.Code != 204 {
			t.Fatalf("delete unlinked budget = %d %s", del.Code, del.Body)
		}
	})
}

func TestBudgetDeleteVsEntryLinkRace(t *testing.T) {
	ctx := integration(t)
	f, officer, _, _, _, alquileres, _, headings := setupBudgetActualFixture(t, ctx)
	const ip = "198.51.100.83"

	makeBudgetWithLine := func(name string) string {
		t.Helper()
		created := f.do(http.MethodPost, "/api/event-budgets", fmt.Sprintf(`{"name":%q,"startsOn":"2026-09-21","endsOn":"2026-09-22"}`, name), ip, officer, nil)
		if created.Code != 201 {
			t.Fatalf("create budget %s = %d %s", name, created.Code, created.Body)
		}
		var b struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(created.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		lineBody := fmt.Sprintf(`{"kind":"cost","headingId":%q,"description":"Sillas","quantity":1,"unitAmountCents":100000}`, headings["mesas y sillas"])
		if r := f.do(http.MethodPost, "/api/event-budgets/"+b.ID+"/lines", lineBody, ip, officer, nil); r.Code != 201 {
			t.Fatalf("add line %s = %d %s", name, r.Code, r.Body)
		}
		return b.ID
	}

	// Deterministic branch 1: DELETE commits while POST waits on FOR SHARE -> POST gets 404 not_found.
	t.Run("delete wins under row lock yields 404 not_found on link", func(t *testing.T) {
		bid := makeBudgetWithLine("Carrera deterministica delete")
		blocker, err := tdb.Super.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer blocker.Rollback(ctx)
		if _, err := blocker.Exec(ctx, `DELETE FROM event_budgets WHERE id=$1`, bid); err != nil {
			t.Fatal(err)
		}
		entryBody := fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":"2026-11-14","amountCents":100000,"description":"Sillas","eventBudgetId":%q,"headingId":%q}`, alquileres, bid, headings["mesas y sillas"])
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			done <- f.do(http.MethodPost, "/api/finance/entries", entryBody, ip, officer, nil)
		}()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var waiting int
			if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%event_budgets%FOR SHARE%'`).Scan(&waiting); err == nil && waiting > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := blocker.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		wantResponse(t, <-done, 404, `{"error":"not_found"}`)
		var count int
		if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM finance_entries WHERE event_budget_id=$1`, bid).Scan(&count); err != nil || count != 0 {
			t.Fatalf("entry count = %d err=%v, want 0", count, err)
		}
	})

	// Deterministic branch 2: link commits while DELETE waits on FOR UPDATE -> DELETE gets 409 budget_has_entries.
	t.Run("link wins under row lock yields 409 budget_has_entries on delete", func(t *testing.T) {
		bid := makeBudgetWithLine("Carrera deterministica link")
		blocker, err := tdb.Super.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer blocker.Rollback(ctx)
		if _, err := blocker.Exec(ctx, `SELECT 1 FROM event_budgets WHERE id=$1 FOR SHARE`, bid); err != nil {
			t.Fatal(err)
		}
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			done <- f.do(http.MethodDelete, "/api/event-budgets/"+bid, "", ip, officer, nil)
		}()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var waiting int
			if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%event_budgets%FOR UPDATE%'`).Scan(&waiting); err == nil && waiting > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		entryBody := fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":"2026-11-14","amountCents":100000,"description":"Sillas","eventBudgetId":%q,"headingId":%q}`, alquileres, bid, headings["mesas y sillas"])
		if postRec := f.do(http.MethodPost, "/api/finance/entries", entryBody, ip, officer, nil); postRec.Code != 201 {
			t.Fatalf("post while FOR SHARE held = %d %s", postRec.Code, postRec.Body)
		}
		if err := blocker.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		wantResponse(t, <-done, 409, `{"error":"budget_has_entries"}`)
		var exists bool
		if err := tdb.Super.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM event_budgets WHERE id=$1)`, bid).Scan(&exists); err != nil || !exists {
			t.Fatalf("budget missing after 409 delete: exists=%v err=%v", exists, err)
		}
	})

	for round := range 8 {
		bid := makeBudgetWithLine(fmt.Sprintf("Carrera %d", round))
		entryBody := fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":"2026-11-14","amountCents":100000,"description":"Sillas %d","eventBudgetId":%q,"headingId":%q}`, alquileres, round, bid, headings["mesas y sillas"])

		start := make(chan struct{})
		var delRec, postRec *httptest.ResponseRecorder
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			delRec = f.do(http.MethodDelete, "/api/event-budgets/"+bid, "", ip, officer, nil)
		}()
		go func() {
			defer wg.Done()
			<-start
			postRec = f.do(http.MethodPost, "/api/finance/entries", entryBody, ip, officer, nil)
		}()
		close(start)
		wg.Wait()

		var budgetExists bool
		var entryCount int
		if err := tdb.Super.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM event_budgets WHERE id=$1)`, bid).Scan(&budgetExists); err != nil {
			t.Fatal(err)
		}
		if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM finance_entries WHERE event_budget_id=$1`, bid).Scan(&entryCount); err != nil {
			t.Fatal(err)
		}
		switch {
		case delRec.Code == 204 && postRec.Code == 404:
			wantResponse(t, postRec, 404, `{"error":"not_found"}`)
			if budgetExists || entryCount != 0 {
				t.Fatalf("round %d delete won but budgetExists=%v entryCount=%d", round, budgetExists, entryCount)
			}
		case postRec.Code == 201 && delRec.Code == 409:
			wantResponse(t, delRec, 409, `{"error":"budget_has_entries"}`)
			if !budgetExists || entryCount != 1 {
				t.Fatalf("round %d link won but budgetExists=%v entryCount=%d", round, budgetExists, entryCount)
			}
		default:
			t.Fatalf("round %d invalid race outcome: delete=%d(%s) post=%d(%s) budgetExists=%v entryCount=%d",
				round, delRec.Code, delRec.Body, postRec.Code, postRec.Body, budgetExists, entryCount)
		}
	}
}
