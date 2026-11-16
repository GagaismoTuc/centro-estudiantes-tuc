package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCategoryManagementR1(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, f.tenant); err != nil {
		t.Fatal(err)
	}
	user, officer, _, _ := f.signInMFA(t, ctx, "officer")
	member := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, member, f.tenant, user); err != nil {
		t.Fatal(err)
	}
	post := func(name, direction string) *httptest.ResponseRecorder {
		return f.do(http.MethodPost, "/api/finance/categories", fmt.Sprintf(`{"name":%q,"direction":%q}`, name, direction), "198.51.100.12", officer, nil)
	}
	var before int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM finance_categories WHERE tenant_id=$1`, f.tenant).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "   "} {
		wantResponse(t, post(name, "expense"), 422, `{"error":"name_required"}`)
	}
	wantResponse(t, post("  ventas ", "income"), 409, `{"error":"category_exists"}`)
	var after int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM finance_categories WHERE tenant_id=$1`, f.tenant).Scan(&after); err != nil || after != before {
		t.Fatalf("rejection wrote categories: %d -> %d: %v", before, after, err)
	}
	r := post("Buzos de egresados", "expense")
	if r.Code != 201 {
		t.Fatalf("create: %d %s", r.Code, r.Body)
	}
	var cat struct {
		ID, Name, Direction string
		Active              bool
	}
	if err := json.Unmarshal(r.Body.Bytes(), &cat); err != nil || cat.Name != "Buzos de egresados" || cat.Direction != "expense" || !cat.Active {
		t.Fatalf("category=%+v err=%v", cat, err)
	}
	list := f.do("GET", "/api/finance/categories", "", "198.51.100.12", officer, nil)
	var cats []struct {
		ID     string
		Active bool
	}
	if err := json.Unmarshal(list.Body.Bytes(), &cats); err != nil {
		t.Fatal(err)
	}
	for _, c := range cats {
		if c.ID == cat.ID && c.Active {
			return
		}
	}
	t.Fatal("created category not available")
}

func TestCategoryManagementRulesR2ToR7(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, f.tenant); err != nil {
		t.Fatal(err)
	}
	user, officer, _, _ := f.signInMFA(t, ctx, "officer")
	member := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111223")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, member, f.tenant, user); err != nil {
		t.Fatal(err)
	}
	_, student := f.signIn(t, ctx, "student")
	_, admin := f.signIn(t, ctx, "school_admin")
	_, unverified := f.signIn(t, ctx, "officer")
	req := func(method, path, body, token string) *httptest.ResponseRecorder {
		return f.do(method, "/api/finance/categories"+path, body, "198.51.100.12", token, nil)
	}
	idOf := func(name, dir string) string {
		var id string
		if err := tdb.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name=$2 AND direction=$3`, f.tenant, name, dir).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	entry := func(id string) *httptest.ResponseRecorder {
		return f.do("POST", "/api/finance/entries", fmt.Sprintf(`{"categoryId":%q,"direction":"income","date":"2026-11-10","amountCents":100,"description":"Fixture"}`, id), "198.51.100.12", officer, nil)
	}
	snapshot := func() string {
		r := req("GET", "", "", student)
		if r.Code != 200 {
			t.Fatalf("list: %d", r.Code)
		}
		return r.Body.String()
	}
	reject := func(t *testing.T, method, path, body, token string, status int, code string) {
		t.Helper()
		before := snapshot()
		wantResponse(t, req(method, path, body, token), status, fmt.Sprintf(`{"error":%q}`, code))
		if snapshot() != before {
			t.Fatalf("%s %s changed list", method, path)
		}
	}
	used := idOf("Ventas", "income")
	reserved := idOf("Cuotas", "income")
	if r := entry(used); r.Code != 201 {
		t.Fatalf("seed entry: %d %s", r.Code, r.Body)
	}
	t.Run("R2 rename preserves references and validates names", func(t *testing.T) {
		for _, name := range []string{"", "   "} {
			reject(t, "PATCH", "/"+used, fmt.Sprintf(`{"name":%q}`, name), officer, 422, "name_required")
		}
		reject(t, "PATCH", "/"+used, `{"name":"  eventos "}`, officer, 409, "category_exists")
		r := req("PATCH", "/"+used, `{"name":"Kiosco"}`, officer)
		if r.Code != 200 {
			t.Fatalf("rename: %d %s", r.Code, r.Body)
		}
		before := snapshot()
		r = req("PATCH", "/"+used, `{"name":"Kiosco"}`, officer)
		if r.Code != 200 || snapshot() != before {
			t.Fatal("same name not idempotent")
		}
		var name string
		if err := tdb.Super.QueryRow(ctx, `SELECT c.name FROM finance_entries e JOIN finance_categories c ON c.tenant_id=e.tenant_id AND c.id=e.category_id WHERE e.tenant_id=$1 AND e.category_id=$2`, f.tenant, used).Scan(&name); err != nil || name != "Kiosco" {
			t.Fatalf("entry name=%q err=%v", name, err)
		}
	})
	t.Run("R3 archive preserves used categories and delete rejects usage", func(t *testing.T) {
		reject(t, "DELETE", "/"+used, "", officer, 409, "category_in_use")
		r := req("POST", "/"+used+"/archive", "", officer)
		if r.Code != 200 {
			t.Fatalf("archive %d %s", r.Code, r.Body)
		}
		wantResponse(t, entry(used), 422, `{"error":"invalid_category"}`)
		if !strings.Contains(snapshot(), `"name":"Kiosco"`) || !strings.Contains(snapshot(), `"active":false`) {
			t.Fatal("archived category missing from list")
		}
		entries := f.do("GET", "/api/finance/entries", "", "198.51.100.12", student, nil)
		if entries.Code != 200 || !strings.Contains(entries.Body.String(), used) {
			t.Fatal("old entry lost")
		}
		unused := idOf("Viajes", "expense")
		r = req("DELETE", "/"+unused, "", officer)
		if r.Code != 204 {
			t.Fatalf("delete %d %s", r.Code, r.Body)
		}
		if strings.Contains(snapshot(), unused) {
			t.Fatal("unused category remains")
		}
	})
	t.Run("R4 reserved dues category cannot be changed", func(t *testing.T) {
		reject(t, "PATCH", "/"+reserved, `{"name":"Other"}`, officer, 409, "reserved_category")
		reject(t, "POST", "/"+reserved+"/archive", "", officer, 409, "reserved_category")
		reject(t, "DELETE", "/"+reserved, "", officer, 409, "reserved_category")
		reject(t, "POST", "", `{"name":"cuotas","direction":"income"}`, officer, 409, "category_exists")
	})
	t.Run("R5 concurrent delete and entry preserve composite foreign key", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			var id string
			if err := tdb.Super.QueryRow(ctx, `INSERT INTO finance_categories(tenant_id,name,direction) VALUES($1,$2,'income') RETURNING id::text`, f.tenant, fmt.Sprintf("Race %d", i)).Scan(&id); err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			results := make(chan *httptest.ResponseRecorder, 2)
			go func() { <-start; results <- entry(id) }()
			go func() { <-start; results <- req("DELETE", "/"+id, "", officer) }()
			close(start)
			a, b := <-results, <-results
			if !((a.Code == 201 && b.Code == 409) || (a.Code == 409 && b.Code == 201) || (a.Code == 404 && b.Code == 204) || (a.Code == 204 && b.Code == 404)) {
				t.Fatalf("race: %d %s / %d %s", a.Code, a.Body, b.Code, b.Body)
			}
			for _, result := range []*httptest.ResponseRecorder{a, b} {
				if result.Code == 404 {
					wantResponse(t, result, 404, `{"error":"not_found"}`)
				}
				if result.Code == 409 {
					wantResponse(t, result, 409, `{"error":"category_in_use"}`)
				}
			}
			var categories, entries int
			if err := tdb.Super.QueryRow(ctx, `SELECT (SELECT count(*) FROM finance_categories WHERE tenant_id=$1 AND id=$2), (SELECT count(*) FROM finance_entries WHERE tenant_id=$1 AND category_id=$2)`, f.tenant, id).Scan(&categories, &entries); err != nil {
				t.Fatal(err)
			}
			if a.Code == 201 || b.Code == 201 {
				if categories != 1 || entries != 1 {
					t.Fatalf("entry won but category=%d entries=%d", categories, entries)
				}
			} else if categories != 0 || entries != 0 {
				t.Fatalf("delete won but category=%d entries=%d", categories, entries)
			}
			var orphans int
			if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM finance_entries e LEFT JOIN finance_categories c ON c.tenant_id=e.tenant_id AND c.id=e.category_id WHERE e.tenant_id=$1 AND c.id IS NULL`, f.tenant).Scan(&orphans); err != nil || orphans != 0 {
				t.Fatalf("orphans=%d err=%v", orphans, err)
			}
		}
	})
	t.Run("R6 only officers with MFA mutate and all members read", func(t *testing.T) {
		for _, token := range []string{student, admin, unverified} {
			status, code := 403, "forbidden"
			if token == unverified {
				code = "mfa_required"
			}
			for _, v := range []struct{ method, path, body string }{{"POST", "", `{"name":"Blocked","direction":"income"}`}, {"PATCH", "/" + used, `{"name":"Blocked"}`}, {"POST", "/" + used + "/archive", ""}, {"DELETE", "/" + used, ""}} {
				reject(t, v.method, v.path, v.body, token, status, code)
			}
		}
		for _, token := range []string{student, admin, officer} {
			if r := req("GET", "", "", token); r.Code != 200 || !strings.Contains(r.Body.String(), `"active":false`) {
				t.Fatalf("read: %d %s", r.Code, r.Body)
			}
		}
		probe := &readProbe{}
		request := httptest.NewRequest("POST", "/api/finance/categories", probe)
		request.AddCookie(&http.Cookie{Name: cookieName, Value: unverified})
		rec := httptest.NewRecorder()
		f.h.ServeHTTP(rec, request)
		wantResponse(t, rec, 403, `{"error":"mfa_required"}`)
		if probe.read {
			t.Fatal("MFA failure read body")
		}
	})
	t.Run("R7 foreign ids are hidden and names are tenant scoped", func(t *testing.T) {
		other := newTenant(t, ctx)
		var foreign string
		if err := tdb.Super.QueryRow(ctx, `INSERT INTO finance_categories(tenant_id,name,direction) VALUES($1,'Buzos','expense') RETURNING id::text`, other).Scan(&foreign); err != nil {
			t.Fatal(err)
		}
		for _, v := range []struct{ method, path, body string }{{"PATCH", "/" + foreign, `{"name":"Other"}`}, {"POST", "/" + foreign + "/archive", ""}, {"DELETE", "/" + foreign, ""}} {
			reject(t, v.method, v.path, v.body, officer, 404, "not_found")
		}
		if r := req("POST", "", `{"name":"Buzos","direction":"expense"}`, officer); r.Code != 201 {
			t.Fatalf("tenant name: %d %s", r.Code, r.Body)
		}
	})
}
