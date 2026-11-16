package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestEventBudgetCreateValidation(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	const path = "/api/event-budgets"
	const ip = "198.51.100.67"
	for _, tc := range []struct{ body, code string }{
		{`{"name":" ","startsOn":"2026-09-21","endsOn":"2026-09-25"}`, "name_required"},
		{`{"name":"Week","startsOn":"2026-09-25","endsOn":"2026-09-21"}`, "invalid_range"},
		{`{"name":"Week","startsOn":"2026-09-01","endsOn":"2026-10-02"}`, "invalid_range"},
	} {
		wantResponse(t, f.do(http.MethodPost, path, tc.body, ip, officer, nil), 422, `{"error":"`+tc.code+`"}`)
	}
	var count int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM event_budgets WHERE tenant_id=$1`, f.tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected creates persisted %d rows: %v", count, err)
	}
	rec := f.do(http.MethodPost, path, `{"name":"Week","startsOn":"2026-09-21","endsOn":"2026-09-25"}`, ip, officer, nil)
	if rec.Code != 201 {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	var result struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result.ID == "" || result.Name != "Week" {
		t.Fatalf("result %+v, err %v", result, err)
	}
}

func TestEventBudgetCostLineValidation(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	headings := seedHeadingsForTenant(t, ctx, f.tenant)
	other := newTenant(t, ctx)
	foreign := seedHeadingsForTenant(t, ctx, other)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	const ip = "198.51.100.68"
	created := f.do("POST", "/api/event-budgets", `{"name":"Week","startsOn":"2026-09-21","endsOn":"2026-09-25"}`, ip, officer, nil)
	var budget struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &budget); err != nil || created.Code != 201 {
		t.Fatalf("budget: %d %s %v", created.Code, created.Body, err)
	}
	path := "/api/event-budgets/" + budget.ID + "/lines"
	cases := []struct {
		body, code string
		status     int
	}{
		{`{"kind":"cost","headingId":"` + headings["mesas y sillas"] + `","description":"Chairs","quantity":0,"unitAmountCents":50000}`, "invalid_quantity", 422},
		{`{"kind":"cost","headingId":"` + headings["mesas y sillas"] + `","description":"Chairs","quantity":1,"unitAmountCents":0}`, "invalid_amount", 422},
		{`{"kind":"cost","headingId":"` + headings["mesas y sillas"] + `","description":"Chairs","quantity":1,"unitAmountCents":50000,"day":"2026-09-26"}`, "invalid_day", 422},
		{`{"kind":"cost","headingId":"` + foreign["gazebos"] + `","description":"Chairs","quantity":1,"unitAmountCents":50000}`, "not_found", 404},
	}
	for _, tc := range cases {
		wantResponse(t, f.do("POST", path, tc.body, ip, officer, nil), tc.status, `{"error":"`+tc.code+`"}`)
	}
	var count int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM event_budget_lines WHERE budget_id=$1`, budget.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejections changed %d lines: %v", count, err)
	}
	rec := f.do("POST", path, `{"kind":"cost","headingId":"`+headings["mesas y sillas"]+`","description":"Chairs","quantity":200,"unitAmountCents":50000,"day":"2026-09-22"}`, ip, officer, nil)
	if rec.Code != 201 {
		t.Fatalf("create line: %d %s", rec.Code, rec.Body)
	}
}

func TestEventBudgetResult(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	headings := seedHeadingsForTenant(t, ctx, f.tenant)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	const ip = "198.51.100.69"
	created := f.do("POST", "/api/event-budgets", `{"name":"Week","startsOn":"2026-09-21","endsOn":"2026-09-25"}`, ip, officer, nil)
	var b struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(created.Body.Bytes(), &b)
	if created.Code != 201 {
		t.Fatal(created.Body.String())
	}
	base := "/api/event-budgets/" + b.ID
	lines := []string{`{"kind":"cost","headingId":"` + headings["mesas y sillas"] + `","description":"Chairs","quantity":200,"unitAmountCents":50000}`, `{"kind":"cost","headingId":"` + headings["gazebos"] + `","description":"Gazebos","quantity":8,"unitAmountCents":2500000}`, `{"kind":"sponsor","description":"Sponsor","quantity":1,"unitAmountCents":5000000}`, `{"kind":"ticket","description":"Tickets","quantity":500,"unitAmountCents":80000}`}
	for _, body := range lines {
		r := f.do("POST", base+"/lines", body, ip, officer, nil)
		if r.Code != 201 {
			t.Fatalf("line %d %s", r.Code, r.Body)
		}
	}
	rec := f.do("GET", base, "", ip, officer, nil)
	if rec.Code != 200 {
		t.Fatalf("get %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Totals struct {
			CostCents, IncomeCents, ResultCents int64
			BreakEvenCents                      *int64
		} `json:"totals"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Totals.CostCents != 30000000 || got.Totals.IncomeCents != 45000000 || got.Totals.ResultCents != 15000000 || got.Totals.BreakEvenCents == nil || *got.Totals.BreakEvenCents != 50000 {
		t.Fatalf("totals %+v", got.Totals)
	}
	for _, tc := range []struct {
		name, ticket, sponsor string
		want                  *int64
	}{
		{"no attendance", `{"kind":"ticket","description":"Tickets"}`, "", nil},
		{"zero attendance", `{"kind":"ticket","description":"Tickets","quantity":0}`, "", nil},
		{"round up", `{"kind":"ticket","description":"Tickets","quantity":1}`, "", int64Pointer(20000)},
		{"covered costs", `{"kind":"ticket","description":"Tickets","quantity":1}`, `{"kind":"sponsor","description":"Sponsor","quantity":1,"unitAmountCents":10001}`, int64Pointer(0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := f.do("POST", "/api/event-budgets", `{"name":"Edge","startsOn":"2026-09-21","endsOn":"2026-09-21"}`, ip, officer, nil)
			var x struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(r.Body.Bytes(), &x)
			p := "/api/event-budgets/" + x.ID
			cost := `{"kind":"cost","headingId":"` + headings["gazebos"] + `","description":"Cost","quantity":1,"unitAmountCents":10001}`
			for _, body := range []string{cost, tc.ticket, tc.sponsor} {
				if body == "" {
					continue
				}
				r = f.do("POST", p+"/lines", body, ip, officer, nil)
				if r.Code != 201 {
					t.Fatalf("line %d %s", r.Code, r.Body)
				}
			}
			r = f.do("GET", p, "", ip, officer, nil)
			var d struct {
				Totals struct {
					BreakEvenCents *int64 `json:"breakEvenCents"`
				} `json:"totals"`
			}
			if err := json.Unmarshal(r.Body.Bytes(), &d); err != nil || r.Code != 200 {
				t.Fatalf("get %d %s %v", r.Code, r.Body, err)
			}
			if (d.Totals.BreakEvenCents == nil) != (tc.want == nil) || (tc.want != nil && *d.Totals.BreakEvenCents != *tc.want) {
				t.Fatalf("break even got %v want %v", d.Totals.BreakEvenCents, tc.want)
			}
		})
	}

}

func TestEventBudgetDeleteAndArchive(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	headings := seedHeadingsForTenant(t, ctx, f.tenant)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	const ip = "198.51.100.70"
	create := func() string {
		r := f.do("POST", "/api/event-budgets", `{"name":"Week","startsOn":"2026-09-21","endsOn":"2026-09-25"}`, ip, officer, nil)
		var b struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &b); err != nil || r.Code != 201 {
			t.Fatalf("create %d %s %v", r.Code, r.Body, err)
		}
		return b.ID
	}
	id := create()
	path := "/api/event-budgets/" + id
	rec := f.do("POST", path+"/lines", `{"kind":"cost","headingId":"`+headings["gazebos"]+`","description":"Gazebo","quantity":1,"unitAmountCents":100}`, ip, officer, nil)
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	wantResponse(t, f.do("DELETE", path, "", ip, officer, nil), 204, "")
	var n int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM event_budget_lines WHERE budget_id=$1`, id).Scan(&n); err != nil || n != 0 {
		t.Fatalf("orphan lines %d %v", n, err)
	}
	id = create()
	path = "/api/event-budgets/" + id
	rec = f.do("POST", path+"/archive", "", ip, officer, nil)
	if rec.Code != 200 {
		t.Fatalf("archive %d %s", rec.Code, rec.Body)
	}
	rec = f.do("GET", path, "", ip, officer, nil)
	var b struct {
		Archived bool `json:"archived"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &b) != nil || !b.Archived {
		t.Fatalf("archived get %d %s", rec.Code, rec.Body)
	}
	wantResponse(t, f.do("POST", path+"/lines", `{"kind":"cost","headingId":"`+headings["gazebos"]+`","description":"Gazebo","quantity":1,"unitAmountCents":100}`, ip, officer, nil), 409, `{"error":"budget_archived"}`)
}

func TestEventBudgetCopiesReferencePrice(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	headings := seedHeadingsForTenant(t, ctx, f.tenant)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	const ip = "198.51.100.71"
	r := f.do("POST", "/api/contacts", `{"name":"Fictional gazebos","headingId":"`+headings["gazebos"]+`"}`, ip, officer, nil)
	var contact contactDTO
	if err := json.Unmarshal(r.Body.Bytes(), &contact); err != nil || r.Code != 201 {
		t.Fatalf("contact %d %s %v", r.Code, r.Body, err)
	}
	r = f.do("POST", "/api/contacts/"+contact.ID+"/reference-prices", `{"description":"Gazebo 3x3","amountCents":2500000,"date":"2026-09-01"}`, ip, officer, nil)
	var price referencePriceDTO
	if err := json.Unmarshal(r.Body.Bytes(), &price); err != nil || r.Code != 201 {
		t.Fatalf("price %d %s %v", r.Code, r.Body, err)
	}
	r = f.do("POST", "/api/event-budgets", `{"name":"Week","startsOn":"2026-09-21","endsOn":"2026-09-25"}`, ip, officer, nil)
	var budget struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &budget)
	if r.Code != 201 {
		t.Fatal(r.Body.String())
	}
	path := "/api/event-budgets/" + budget.ID
	r = f.do("POST", path+"/lines", `{"kind":"cost","headingId":"`+headings["gazebos"]+`","contactId":"`+contact.ID+`","referencePriceId":"`+price.ID+`","description":"Gazebo","quantity":1}`, ip, officer, nil)
	var line struct {
		ID              string `json:"id"`
		UnitAmountCents int64  `json:"unitAmountCents"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &line); err != nil || r.Code != 201 || line.UnitAmountCents != 2500000 {
		t.Fatalf("line %d %s %v", r.Code, r.Body, err)
	}
	r = f.do("PUT", path+"/lines/"+line.ID, `{"kind":"cost","headingId":"`+headings["gazebos"]+`","contactId":"`+contact.ID+`","description":"Gazebo","quantity":1,"unitAmountCents":2700000}`, ip, officer, nil)
	if r.Code != 200 {
		t.Fatalf("edit %d %s", r.Code, r.Body)
	}
	r = f.do("GET", "/api/contacts", "", ip, officer, nil)
	var contacts []contactDTO
	if err := json.Unmarshal(r.Body.Bytes(), &contacts); err != nil || len(contacts) != 1 || len(contacts[0].ReferencePrices) != 1 || contacts[0].ReferencePrices[0].AmountCents != 2500000 {
		t.Fatalf("contact prices %s %v", r.Body, err)
	}
	wantResponse(t, f.do("DELETE", "/api/contacts/"+contact.ID+"/reference-prices/"+price.ID, "", ip, officer, nil), 204, "")
	wantResponse(t, f.do("DELETE", "/api/contacts/"+contact.ID, "", ip, officer, nil), 204, "")
	r = f.do("GET", path, "", ip, officer, nil)
	var detail struct {
		Lines []struct {
			UnitAmountCents int64 `json:"unitAmountCents"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &detail); err != nil || r.Code != 200 || len(detail.Lines) != 1 || detail.Lines[0].UnitAmountCents != 2700000 {
		t.Fatalf("retained line %d %s %v", r.Code, r.Body, err)
	}
}

func TestEventBudgetAuthorization(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	_, pending := f.signIn(t, ctx, "officer")
	_, admin, _, _ := f.signInMFA(t, ctx, "school_admin")
	_, student := f.signIn(t, ctx, "student")
	const ip = "198.51.100.72"
	body := `{"name":"Week","startsOn":"2026-09-21","endsOn":"2026-09-25"}`
	for _, tc := range []struct {
		token  string
		status int
		body   string
	}{{"", 401, unauthBody}, {student, 403, `{"error":"forbidden"}`}, {admin, 403, `{"error":"forbidden"}`}, {pending, 403, `{"error":"mfa_required"}`}} {
		wantResponse(t, f.do("POST", "/api/event-budgets", body, ip, tc.token, nil), tc.status, tc.body)
	}
	var n int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM event_budgets WHERE tenant_id=$1`, f.tenant).Scan(&n); err != nil || n != 0 {
		t.Fatalf("writes after rejects %d %v", n, err)
	}
	r := f.do("POST", "/api/event-budgets", body, ip, officer, nil)
	if r.Code != 201 {
		t.Fatal(r.Body.String())
	}
	for _, tc := range []struct {
		token  string
		status int
		body   string
	}{{"", 401, unauthBody}, {student, 403, `{"error":"forbidden"}`}, {pending, 403, `{"error":"mfa_required"}`}, {admin, 200, ""}, {officer, 200, ""}} {
		r := f.do("GET", "/api/event-budgets", "", ip, tc.token, nil)
		if tc.status == 200 {
			if r.Code != 200 {
				t.Fatalf("read %d %s", r.Code, r.Body)
			}
		} else {
			wantResponse(t, r, tc.status, tc.body)
		}
	}
}

func TestEventBudgetEditingAndConcurrentLines(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	headings := seedHeadingsForTenant(t, ctx, f.tenant)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	_, other, _, _ := f.signInMFA(t, ctx, "officer")
	const ip = "198.51.100.73"
	r := f.do("POST", "/api/event-budgets", `{"name":"Week","startsOn":"2026-09-21","endsOn":"2026-09-25"}`, ip, officer, nil)
	var b struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &b)
	if r.Code != 201 {
		t.Fatal(r.Body.String())
	}
	path := "/api/event-budgets/" + b.ID
	if r := f.do("PUT", path, `{"name":"New week","startsOn":"2026-09-21","endsOn":"2026-09-25"}`, ip, other, nil); r.Code != 200 {
		t.Fatalf("edit budget %d %s", r.Code, r.Body)
	}
	bodies := []string{`{"kind":"cost","headingId":"` + headings["gazebos"] + `","description":"A","quantity":1,"unitAmountCents":100}`, `{"kind":"cost","headingId":"` + headings["gazebos"] + `","description":"B","quantity":1,"unitAmountCents":100}`}
	results := make(chan int, 2)
	for i, token := range []string{officer, other} {
		go func(body, token string) { results <- f.do("POST", path+"/lines", body, ip, token, nil).Code }(bodies[i], token)
	}
	for range bodies {
		if got := <-results; got != 201 {
			t.Fatalf("parallel add status %d", got)
		}
	}
	r = f.do("GET", path, "", ip, officer, nil)
	var detail struct {
		Name  string `json:"name"`
		Lines []struct {
			ID          string `json:"id"`
			Description string `json:"description"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &detail); err != nil || r.Code != 200 || detail.Name != "New week" || len(detail.Lines) != 2 {
		t.Fatalf("detail %d %s %v", r.Code, r.Body, err)
	}
	line := detail.Lines[0].ID
	for _, v := range []struct{ token, name string }{{officer, "First"}, {other, "Last"}} {
		body := `{"kind":"cost","headingId":"` + headings["gazebos"] + `","description":"` + v.name + `","quantity":1,"unitAmountCents":100}`
		if r := f.do("PUT", path+"/lines/"+line, body, ip, v.token, nil); r.Code != 200 {
			t.Fatalf("edit %d %s", r.Code, r.Body)
		}
	}
	var got string
	if err := tdb.Super.QueryRow(ctx, `SELECT description FROM event_budget_lines WHERE id=$1`, line).Scan(&got); err != nil || got != "Last" {
		t.Fatalf("last write %q %v", got, err)
	}
	wantResponse(t, f.do("DELETE", path+"/lines/"+line, "", ip, officer, nil), 204, "")
	var remaining int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM event_budget_lines WHERE budget_id=$1`, b.ID).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("remaining %d %v", remaining, err)
	}
}

func TestEventBudgetIncomeLines(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	const ip = "198.51.100.74"
	r := f.do("POST", "/api/event-budgets", `{"name":"Week","startsOn":"2026-09-21","endsOn":"2026-09-25"}`, ip, officer, nil)
	var b struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &b)
	path := "/api/event-budgets/" + b.ID
	for _, body := range []string{`{"kind":"ticket","description":"Tickets","quantity":500}`, `{"kind":"foodtruck","description":"Stall","quantity":1,"unitAmountCents":10000}`, `{"kind":"sponsor","description":"Sponsor","quantity":1,"unitAmountCents":20000}`, `{"kind":"due","description":"Dues","quantity":1,"unitAmountCents":30000}`} {
		r = f.do("POST", path+"/lines", body, ip, officer, nil)
		if r.Code != 201 {
			t.Fatalf("line %d %s", r.Code, r.Body)
		}
	}
	r = f.do("GET", path, "", ip, officer, nil)
	var d struct {
		Lines []struct {
			Kind string `json:"kind"`
		} `json:"lines"`
		Totals struct {
			IncomeCents int64 `json:"incomeCents"`
		} `json:"totals"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &d); err != nil || r.Code != 200 || len(d.Lines) != 4 || d.Totals.IncomeCents != 60000 {
		t.Fatalf("incomes %d %s %v", r.Code, r.Body, err)
	}
}

func TestEventBudgetTotalsByDay(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	headings := seedHeadingsForTenant(t, ctx, f.tenant)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	const ip = "198.51.100.75"
	r := f.do("POST", "/api/event-budgets", `{"name":"Week","startsOn":"2026-09-21","endsOn":"2026-09-25"}`, ip, officer, nil)
	var b struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &b)
	path := "/api/event-budgets/" + b.ID
	for _, body := range []string{`{"kind":"cost","headingId":"` + headings["mesas y sillas"] + `","description":"Chairs","quantity":200,"unitAmountCents":50000,"day":"2026-09-22"}`, `{"kind":"sponsor","description":"Undated","quantity":1,"unitAmountCents":5000000}`} {
		if r = f.do("POST", path+"/lines", body, ip, officer, nil); r.Code != 201 {
			t.Fatal(r.Body.String())
		}
	}
	r = f.do("GET", path, "", ip, officer, nil)
	var d struct {
		Totals struct{ CostCents, IncomeCents int64 } `json:"totals"`
		Days   []struct {
			Day    string                                 `json:"day"`
			Totals struct{ CostCents, IncomeCents int64 } `json:"totals"`
		} `json:"days"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &d); err != nil || r.Code != 200 || len(d.Days) != 5 || d.Totals.CostCents != 10000000 || d.Totals.IncomeCents != 5000000 || d.Days[1].Day != "2026-09-22" || d.Days[1].Totals.CostCents != 10000000 || d.Days[1].Totals.IncomeCents != 0 {
		t.Fatalf("days %d %s %v", r.Code, r.Body, err)
	}
}

func TestEventBudgetTenantIsolation(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	headings := seedHeadingsForTenant(t, ctx, f.tenant)
	other := newTenant(t, ctx)
	foreignHeadings := seedHeadingsForTenant(t, ctx, other)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	const ip = "198.51.100.76"
	var foreignBudget, foreignLine, foreignContact string
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO event_budgets(tenant_id,name,starts_on,ends_on) VALUES ($1,'Foreign','2026-09-21','2026-09-25') RETURNING id::text`, other).Scan(&foreignBudget); err != nil {
		t.Fatal(err)
	}
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO event_budget_lines(tenant_id,budget_id,kind,description,quantity,unit_amount_cents) VALUES ($1,$2,'sponsor','Foreign',1,100) RETURNING id::text`, other, foreignBudget).Scan(&foreignLine); err != nil {
		t.Fatal(err)
	}
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO contacts(tenant_id,heading_id,name) VALUES($1,$2,'Foreign') RETURNING id::text`, other, foreignHeadings["gazebos"]).Scan(&foreignContact); err != nil {
		t.Fatal(err)
	}
	rec := f.do("POST", "/api/event-budgets", `{"name":"Local","startsOn":"2026-09-21","endsOn":"2026-09-25"}`, ip, officer, nil)
	var b struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	local := "/api/event-budgets/" + b.ID
	foreign := "/api/event-budgets/" + foreignBudget
	for _, tc := range []struct{ method, path, body string }{{"GET", foreign, ""}, {"DELETE", foreign, ""}, {"POST", foreign + "/archive", ""}, {"POST", foreign + "/lines", `{"kind":"sponsor","description":"x","quantity":1,"unitAmountCents":100}`}, {"PUT", local + "/lines/" + foreignLine, `{"kind":"sponsor","description":"x","quantity":1,"unitAmountCents":100}`}, {"POST", local + "/lines", `{"kind":"cost","headingId":"` + headings["gazebos"] + `","contactId":"` + foreignContact + `","description":"x","quantity":1,"unitAmountCents":100}`}} {
		wantResponse(t, f.do(tc.method, tc.path, tc.body, ip, officer, nil), 404, `{"error":"not_found"}`)
	}
	var name string
	var n int
	if err := tdb.Super.QueryRow(ctx, `SELECT name,(SELECT count(*) FROM event_budget_lines WHERE budget_id=$1) FROM event_budgets WHERE id=$1`, foreignBudget).Scan(&name, &n); err != nil || name != "Foreign" || n != 1 {
		t.Fatalf("foreign changed %q %d %v", name, n, err)
	}
}

func int64Pointer(v int64) *int64 { return &v }
