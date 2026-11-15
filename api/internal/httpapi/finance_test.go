package httpapi_test

import (
	"bytes"
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

func TestFinanceRules(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	f.clk.t = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, f.tenant); err != nil {
		t.Fatal(err)
	}
	user, officer, _, _ := f.signInMFA(t, ctx, "officer")
	member := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, member, f.tenant, user); err != nil {
		t.Fatal(err)
	}
	_, student := f.signIn(t, ctx, "student")
	_, admin := f.signIn(t, ctx, "school_admin")
	_, unverified := f.signIn(t, ctx, "officer")
	cat := func(name, dir string) string {
		t.Helper()
		var id string
		if err := tdb.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name=$2 AND direction=$3`, f.tenant, name, dir).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	income, expense, reserved := cat("Ventas", "income"), cat("Viajes", "expense"), cat("Cuotas", "income")
	makeBody := func(id, dir, date, amount, description string) string {
		return fmt.Sprintf(`{"categoryId":%q,"direction":%q,"date":%q,"amountCents":%s,"description":%q}`, id, dir, date, amount, description)
	}
	post := func(body, token string) int {
		return f.do(http.MethodPost, "/api/finance/entries", body, "198.51.100.12", token, nil).Code
	}
	count := func() int {
		t.Helper()
		var n int
		if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM finance_entries WHERE tenant_id=$1`, f.tenant).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
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
	checkReject := func(t *testing.T, body string, status int, code string) {
		t.Helper()
		before := snapshot()
		r := f.do(http.MethodPost, "/api/finance/entries", body, "198.51.100.12", officer, nil)
		wantResponse(t, r, status, fmt.Sprintf(`{"error":%q}`, code))
		if snapshot() != before {
			t.Fatal("rejected entry was written")
		}
	}
	var created string
	t.Run("R1 records income and officer", func(t *testing.T) {
		r := f.do(http.MethodPost, "/api/finance/entries", makeBody(income, "income", "2026-11-10", "200000", "Venta de tortas"), "198.51.100.12", officer, nil)
		if r.Code != 201 {
			t.Fatalf("status=%d body=%s", r.Code, r.Body)
		}
		var out struct {
			ID, RecordedBy, Date string
			AmountCents          int64
			RecordedAt           time.Time
		}
		if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		created = out.ID
		if out.RecordedBy != "Ana Ficticia" || out.Date != "2026-11-10" || out.AmountCents != 200000 || !out.RecordedAt.Equal(f.clk.Now()) {
			t.Fatalf("entry=%+v", out)
		}
		var stored time.Time
		if err := tdb.Super.QueryRow(ctx, `SELECT recorded_at FROM finance_entries WHERE id=$1`, created).Scan(&stored); err != nil || !stored.Equal(out.RecordedAt) {
			t.Fatalf("stored time=%v err=%v", stored, err)
		}
	})
	t.Run("R2 rejects invalid amounts without writes", func(t *testing.T) {
		for _, amount := range []string{"0", "-50000", "1250.5", "null", "9223372036854775808"} {
			checkReject(t, makeBody(income, "income", "2026-11-10", amount, "Fixture"), 422, "invalid_amount")
		}
		checkReject(t, fmt.Sprintf(`{"categoryId":%q,"direction":"income","date":"2026-11-10","description":"Fixture"}`, income), 422, "invalid_amount")
	})
	t.Run("R4 validates descriptions", func(t *testing.T) {
		for _, d := range []string{"", "   "} {
			checkReject(t, makeBody(income, "income", "2026-11-10", "1", d), 422, "description_required")
		}
		checkReject(t, makeBody(income, "income", "2026-11-10", "1", strings.Repeat("x", 501)), 422, "description_too_long")
	})
	t.Run("R5 validates category ownership direction and reservation", func(t *testing.T) {
		checkReject(t, makeBody(income, "expense", "2026-11-10", "1", "Fixture"), 422, "invalid_category")
		checkReject(t, makeBody(reserved, "income", "2026-11-10", "1", "Fixture"), 422, "reserved_category")
		checkReject(t, makeBody("00000000-0000-4000-8000-000000000000", "income", "2026-11-10", "1", "Fixture"), 404, "not_found")
		other := newTenant(t, ctx)
		if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, other); err != nil {
			t.Fatal(err)
		}
		var foreign string
		if err := tdb.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name='Ventas'`, other).Scan(&foreign); err != nil {
			t.Fatal(err)
		}
		checkReject(t, makeBody(foreign, "income", "2026-11-10", "1", "Fixture"), 404, "not_found")
		if _, err := tdb.Super.Exec(ctx, `UPDATE finance_categories SET active=false WHERE id=$1`, income); err != nil {
			t.Fatal(err)
		}
		checkReject(t, makeBody(income, "income", "2026-11-10", "1", "Fixture"), 422, "invalid_category")
		if _, err := tdb.Super.Exec(ctx, `UPDATE finance_categories SET active=true WHERE id=$1`, income); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("R6 lists default categories", func(t *testing.T) {
		r := f.do(http.MethodGet, "/api/finance/categories", "", "198.51.100.12", student, nil)
		if r.Code != 200 {
			t.Fatalf("status=%d %s", r.Code, r.Body)
		}
		var rows []struct{ Direction, Name string }
		if err := json.Unmarshal(r.Body.Bytes(), &rows); err != nil || len(rows) != 10 {
			t.Fatalf("categories=%v err=%v", rows, err)
		}
		want := map[string]bool{"income:Cuotas": true, "income:Eventos": true, "income:Ventas": true, "income:Donaciones y sponsors": true, "income:Otros ingresos": true, "expense:Eventos": true, "expense:Materiales e impresiones": true, "expense:Alquileres": true, "expense:Viajes": true, "expense:Otros gastos": true}
		for _, row := range rows {
			delete(want, row.Direction+":"+row.Name)
		}
		if len(want) != 0 {
			t.Fatalf("missing categories %v", want)
		}
	})
	t.Run("R7 expenses affect balance including negative", func(t *testing.T) {
		if post(makeBody(expense, "expense", "2026-11-10", "50000", "Fixture"), officer) != 201 {
			t.Fatal("expense rejected")
		}
		r := f.do("GET", "/api/finance/balance", "", "198.51.100.12", student, nil)
		var b struct{ BalanceCents int64 }
		if err := json.Unmarshal(r.Body.Bytes(), &b); err != nil || b.BalanceCents != 150000 {
			t.Fatalf("balance=%+v err=%v", b, err)
		}
		if post(makeBody(expense, "expense", "2026-11-10", "200000", "Fixture"), officer) != 201 {
			t.Fatal("negative expense rejected")
		}
		r = f.do("GET", "/api/finance/balance", "", "198.51.100.12", student, nil)
		_ = json.Unmarshal(r.Body.Bytes(), &b)
		if b.BalanceCents != -50000 {
			t.Fatalf("balance=%+v", b)
		}
		for i := 0; i < 2; i++ {
			if post(makeBody(income, "income", "2026-11-10", "9223372036854775807", "Fixture"), officer) != 201 {
				t.Fatal("large income rejected")
			}
		}
		r = f.do("GET", "/api/finance/balance", "", "198.51.100.12", student, nil)
		if r.Code != 200 || !strings.Contains(r.Body.String(), "18446744073709501614") {
			t.Fatalf("wide balance: %d %s", r.Code, r.Body)
		}
		for i := 0; i < 2; i++ {
			if post(makeBody(expense, "expense", "2026-11-10", "9223372036854775807", "Fixture"), officer) != 201 {
				t.Fatal("large expense rejected")
			}
		}
	})
	t.Run("R3 validates Argentina date", func(t *testing.T) {
		for _, date := range []string{"2026-11-14", "2025-03-01"} {
			if got := post(makeBody(income, "income", date, "1", "Fixture"), officer); got != 201 {
				t.Fatalf("date %s status=%d", date, got)
			}
		}
		checkReject(t, makeBody(income, "income", "2026-11-15", "1", "Fixture"), 422, "future_date")
		for _, date := range []string{"", "not-a-date"} {
			checkReject(t, makeBody(income, "income", date, "1", "Fixture"), 422, "invalid_date")
		}
		checkReject(t, fmt.Sprintf(`{"categoryId":%q,"direction":"income","amountCents":1,"description":"Fixture"}`, income), 422, "invalid_date")
		f.clk.t = time.Date(2026, 11, 14, 2, 0, 0, 0, time.UTC)
		checkReject(t, makeBody(income, "income", "2026-11-14", "1", "Fixture"), 422, "future_date")
		f.clk.t = time.Date(2026, 11, 14, 12, 1, 0, 0, time.UTC)
	})
	t.Run("R8 edit and delete are unsupported", func(t *testing.T) {
		for _, method := range []string{"PUT", "DELETE"} {
			before := snapshot()
			r := f.do(method, "/api/finance/entries/"+created, "", "198.51.100.12", officer, nil)
			if r.Code != 405 {
				t.Fatalf("%s status=%d", method, r.Code)
			}
			if snapshot() != before {
				t.Fatal("entry changed")
			}
		}
	})
	t.Run("R9 only officers with MFA write", func(t *testing.T) {
		body := makeBody(income, "income", "2026-11-10", "1", "Fixture")
		for _, c := range []struct {
			token  string
			status int
			code   string
		}{{"", 401, "unauthenticated"}, {student, 403, "forbidden"}, {admin, 403, "forbidden"}, {unverified, 403, "mfa_required"}} {
			before := snapshot()
			r := f.do("POST", "/api/finance/entries", body, "198.51.100.12", c.token, nil)
			wantResponse(t, r, c.status, fmt.Sprintf(`{"error":%q}`, c.code))
			if snapshot() != before {
				t.Fatal("unauthorized write")
			}
		}
		probe := &readProbe{}
		req := httptest.NewRequest("POST", "/api/finance/entries", probe)
		req.AddCookie(&http.Cookie{Name: cookieName, Value: unverified})
		rec := httptest.NewRecorder()
		f.h.ServeHTTP(rec, req)
		wantResponse(t, rec, 403, `{"error":"mfa_required"}`)
		if probe.read {
			t.Fatal("unverified officer body was read")
		}
	})
	t.Run("R10 authorized members read ledger and balance", func(t *testing.T) {
		for _, token := range []string{officer, student, admin} {
			for _, path := range []string{"/api/finance/entries", "/api/finance/balance"} {
				r := f.do("GET", path, "", "198.51.100.12", token, nil)
				if r.Code != 200 {
					t.Fatalf("%s status=%d", path, r.Code)
				}
			}
		}
		r := f.do("GET", "/api/finance/entries", "", "198.51.100.12", student, nil)
		if !strings.Contains(r.Body.String(), "Ana Ficticia") {
			t.Fatal("student cannot see officer")
		}
	})
	t.Run("R11 headless fund stays readable", func(t *testing.T) {
		if _, err := tdb.Super.Exec(ctx, `DELETE FROM memberships WHERE tenant_id=$1 AND role='officer'`, f.tenant); err != nil {
			t.Fatal(err)
		}
		before := snapshot()
		for _, token := range []string{student, admin} {
			r := f.do("POST", "/api/finance/entries", makeBody(income, "income", "2026-11-10", "1", "Fixture"), "198.51.100.12", token, nil)
			wantResponse(t, r, 403, `{"error":"forbidden"}`)
		}
		if snapshot() != before {
			t.Fatal("headless write")
		}
		r := f.do("GET", "/api/finance/entries", "", "198.51.100.12", student, nil)
		if r.Code != 200 {
			t.Fatalf("entries %d", r.Code)
		}
		if r = f.do("GET", "/api/finance/balance", "", "198.51.100.12", student, nil); r.Code != 200 {
			t.Fatalf("balance %d", r.Code)
		}
	})
	t.Run("R12 other tenant entry is not found", func(t *testing.T) {
		other := newTenant(t, ctx)
		var id string
		if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, other); err != nil {
			t.Fatal(err)
		}
		if err := tdb.Super.QueryRow(ctx, `INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name) SELECT $1,id,'income','2026-11-10',1,'Fixture',$2,'Beto Ficticio' FROM finance_categories WHERE tenant_id=$1 AND name='Ventas' RETURNING id::text`, other, user).Scan(&id); err != nil {
			t.Fatal(err)
		}
		r := f.do("GET", "/api/finance/entries/"+id, "", "198.51.100.12", student, nil)
		wantResponse(t, r, 404, `{"error":"not_found"}`)
	})
	t.Run("R13 duplicate parallel submissions both persist", func(t *testing.T) {
		user2, token, _, _ := f.signInMFA(t, ctx, "officer")
		m := seedMember(t, ctx, f.tenant, "Otra Ficticia", "30111223")
		if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE user_id=$2 AND tenant_id=$3`, m, user2, f.tenant); err != nil {
			t.Fatal(err)
		}
		before := count()
		var wg sync.WaitGroup
		statuses := make([]int, 2)
		for i := range statuses {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				statuses[i] = post(makeBody(income, "income", "2026-11-10", "200000", "Fixture"), token)
			}(i)
		}
		wg.Wait()
		if statuses[0] != 201 || statuses[1] != 201 || count() != before+2 {
			t.Fatalf("statuses=%v count=%d", statuses, count())
		}
		r := f.do("GET", "/api/finance/balance", "", "198.51.100.12", token, nil)
		var balance struct{ BalanceCents int64 }
		if err := json.Unmarshal(r.Body.Bytes(), &balance); err != nil || balance.BalanceCents != 350002 {
			t.Fatalf("balance=%+v err=%v", balance, err)
		}
	})
}

func TestFinancePrivacy(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, f.tenant); err != nil {
		t.Fatal(err)
	}
	_, token, _, _ := f.signInMFA(t, ctx, "officer")
	var id string
	if err := tdb.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name='Ventas'`, f.tenant).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	body := fmt.Sprintf(`{"categoryId":%q,"direction":"income","date":"2026-11-10","amountCents":87361,"description":"Private fixture text"}`, id)
	r := f.do("POST", "/api/finance/entries", body, "198.51.100.12", token, nil)
	wantResponse(t, r, 500, `{"error":"internal"}`)
	for _, value := range []string{"Private fixture text", "87361", "Ana Ficticia"} {
		if strings.Contains(logs.String(), value) || strings.Contains(r.Body.String(), value) {
			t.Fatalf("private value in output")
		}
	}
}
