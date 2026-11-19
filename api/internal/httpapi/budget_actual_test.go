package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	f, officer, _, _, _, alquileres, _, headings := setupBudgetActualFixture(t, ctx)
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
	})
}
