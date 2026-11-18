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
		_, unlinked, _, _ := f.signInMFA(t, ctx, "officer")
		for _, c := range []struct {
			token  string
			status int
			code   string
		}{{"", 401, "unauthenticated"}, {student, 403, "forbidden"}, {admin, 403, "forbidden"}, {unverified, 403, "mfa_required"}, {unlinked, 403, "forbidden"}} {
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
	t.Run("R12 other tenant entry is not found", func(t *testing.T) {
		other := newTenant(t, ctx)
		var id string
		if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, other); err != nil {
			t.Fatal(err)
		}
		if err := tdb.Super.QueryRow(ctx, `INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name) SELECT $1,id,'income','2026-11-10',1,'Fixture',$2,'Beto Ficticio' FROM finance_categories WHERE tenant_id=$1 AND name='Ventas' RETURNING id::text`, other, user).Scan(&id); err != nil {
			t.Fatal(err)
		}
		r := f.do("GET", "/api/finance/entries/"+id, "", "198.51.100.12", officer, nil)
		wantResponse(t, r, 404, `{"error":"not_found"}`)
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
	user, token, _, _ := f.signInMFA(t, ctx, "officer")
	member := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111224")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, member, f.tenant, user); err != nil {
		t.Fatal(err)
	}
	_, err := tdb.Super.Exec(ctx, `CREATE FUNCTION finance_privacy_probe() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION '% % %', NEW.description, NEW.amount_cents, NEW.recorded_by_name; END $$; CREATE TRIGGER finance_privacy_probe BEFORE INSERT ON finance_entries FOR EACH ROW EXECUTE FUNCTION finance_privacy_probe()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = tdb.Super.Exec(context.Background(), `DROP TRIGGER IF EXISTS finance_privacy_probe ON finance_entries; DROP FUNCTION IF EXISTS finance_privacy_probe()`)
	})
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

// reversalEntry is the FinanceEntry shape a reversal test reads back.
type reversalEntry struct {
	ID, CategoryID, Direction, Date, Description, RecordedBy string
	AmountCents                                              int64
	ReversesID, ReversalID, ReversedBy, ReversedOn           string
}

func TestFinanceReversals(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	f.clk.t = time.Date(2026, 11, 14, 16, 0, 0, 0, time.UTC)
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, f.tenant); err != nil {
		t.Fatal(err)
	}
	officerAs := func(name, doc string) string {
		t.Helper()
		user, token, _, _ := f.signInMFA(t, ctx, "officer")
		member := seedMember(t, ctx, f.tenant, name, doc)
		if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, member, f.tenant, user); err != nil {
			t.Fatal(err)
		}
		return token
	}
	ana := officerAs("Ana Ficticia", "30111231")
	beto := officerAs("Beto Ficticio", "30111232")
	_, student := f.signIn(t, ctx, "student")
	_, admin := f.signIn(t, ctx, "school_admin")
	_, unverified := f.signIn(t, ctx, "officer")
	var ventas, viajes string
	for name, dst := range map[string]*string{"Ventas": &ventas, "Viajes": &viajes} {
		if err := tdb.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name=$2`, f.tenant, name).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	decode := func(t *testing.T, body []byte) reversalEntry {
		t.Helper()
		var e reversalEntry
		if err := json.Unmarshal(body, &e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	record := func(t *testing.T, category, dir, date string, amount int64, description string) reversalEntry {
		t.Helper()
		body := fmt.Sprintf(`{"categoryId":%q,"direction":%q,"date":%q,"amountCents":%d,"description":%q}`, category, dir, date, amount, description)
		r := f.do(http.MethodPost, "/api/finance/entries", body, "198.51.100.13", ana, nil)
		if r.Code != 201 {
			t.Fatalf("record status=%d body=%s", r.Code, r.Body)
		}
		return decode(t, r.Body.Bytes())
	}
	reverse := func(id, token string) *httptest.ResponseRecorder {
		return f.do(http.MethodPost, "/api/finance/entries/"+id+"/reversal", "", "198.51.100.13", token, nil)
	}
	get := func(t *testing.T, id string) reversalEntry {
		t.Helper()
		r := f.do(http.MethodGet, "/api/finance/entries/"+id, "", "198.51.100.13", student, nil)
		if r.Code != 200 {
			t.Fatalf("get status=%d body=%s", r.Code, r.Body)
		}
		return decode(t, r.Body.Bytes())
	}
	balance := func(t *testing.T) int64 {
		t.Helper()
		r := f.do(http.MethodGet, "/api/finance/balance", "", "198.51.100.13", student, nil)
		var b struct{ BalanceCents int64 }
		if err := json.Unmarshal(r.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		return b.BalanceCents
	}
	// ledger is every stored row of the tenant, including the reversal link, so a rejection can
	// prove the balance and the ledger stayed exactly as they were.
	ledger := func(t *testing.T) string {
		t.Helper()
		var out string
		if err := tdb.Super.QueryRow(ctx, `SELECT count(*)::text || ':' || COALESCE(md5(string_agg(id::text || direction || amount_cents || entry_date || description || COALESCE(reverses_id::text, '-'), ',' ORDER BY id)), '') FROM finance_entries WHERE tenant_id=$1`, f.tenant).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	rejected := func(t *testing.T, id, token string, status int, code string) {
		t.Helper()
		beforeLedger, beforeBalance := ledger(t), balance(t)
		wantResponse(t, reverse(id, token), status, fmt.Sprintf(`{"error":%q}`, code))
		if ledger(t) != beforeLedger || balance(t) != beforeBalance {
			t.Fatal("rejected reversal changed the ledger or the balance")
		}
	}

	tortas := record(t, ventas, "income", "2026-11-10", 200000, "Venta de tortas")
	var reversal reversalEntry
	t.Run("S1 reversal is an opposite entry of the same category dated today", func(t *testing.T) {
		before := balance(t)
		r := reverse(tortas.ID, beto)
		if r.Code != 201 {
			t.Fatalf("status=%d body=%s", r.Code, r.Body)
		}
		reversal = decode(t, r.Body.Bytes())
		want := reversalEntry{ID: reversal.ID, CategoryID: ventas, Direction: "expense", Date: "2026-11-14", Description: "Reversión: Venta de tortas", RecordedBy: "Beto Ficticio", AmountCents: 200000, ReversesID: tortas.ID}
		if reversal != want || reversal.ID == tortas.ID {
			t.Fatalf("reversal=%+v want %+v", reversal, want)
		}
		if got := balance(t); got != before-200000 {
			t.Fatalf("balance=%d want %d", got, before-200000)
		}
		original := get(t, tortas.ID)
		if original.ReversalID != reversal.ID || original.ReversedBy != "Beto Ficticio" || original.ReversedOn != "2026-11-14" || original.ReversesID != "" {
			t.Fatalf("original=%+v", original)
		}
		if stored := get(t, reversal.ID); stored != reversal {
			t.Fatalf("stored reversal=%+v", stored)
		}
	})
	t.Run("S2 an entry is reversed only once", func(t *testing.T) {
		rejected(t, tortas.ID, beto, 409, "already_reversed")
		rejected(t, tortas.ID, ana, 409, "already_reversed")
	})
	t.Run("S3 a reversal cannot be reversed", func(t *testing.T) {
		rejected(t, reversal.ID, beto, 409, "cannot_reverse_reversal")
		if got := get(t, reversal.ID); got.ReversalID != "" || got.ReversedBy != "" {
			t.Fatalf("reversal marked as reversed: %+v", got)
		}
	})
	t.Run("S4 parallel reversals apply once", func(t *testing.T) {
		target := record(t, ventas, "income", "2026-11-14", 70000, "Fixture")
		before := balance(t)
		var wg sync.WaitGroup
		results := make([]*httptest.ResponseRecorder, 2)
		for i, token := range []string{ana, beto} {
			wg.Add(1)
			go func(i int, token string) {
				defer wg.Done()
				results[i] = reverse(target.ID, token)
			}(i, token)
		}
		wg.Wait()
		created, conflicts := 0, 0
		for _, r := range results {
			switch {
			case r.Code == 201:
				created++
			case r.Code == 409 && strings.TrimSpace(r.Body.String()) == `{"error":"already_reversed"}`:
				conflicts++
			default:
				t.Fatalf("status=%d body=%s", r.Code, r.Body)
			}
		}
		if created != 1 || conflicts != 1 {
			t.Fatalf("created=%d conflicts=%d", created, conflicts)
		}
		if got := balance(t); got != before-70000 {
			t.Fatalf("balance=%d want %d", got, before-70000)
		}
		var links int
		if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM finance_entries WHERE reverses_id=$1`, target.ID).Scan(&links); err != nil || links != 1 {
			t.Fatalf("links=%d err=%v", links, err)
		}

		// Deterministic loser: an uncommitted reversal holds the unique key, so the request passes
		// its read check, blocks on INSERT and must map the unique violation to already_reversed.
		late := record(t, ventas, "income", "2026-11-14", 80000, "Fixture")
		before = balance(t)
		blocker, err := tdb.Super.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer blocker.Rollback(context.Background())
		if _, err := blocker.Exec(ctx, `INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name,reverses_id) SELECT tenant_id,category_id,'expense','2026-11-14',amount_cents,'Reversión: Fixture',recorded_by,'Beto Ficticio',id FROM finance_entries WHERE id=$1`, late.ID); err != nil {
			t.Fatal(err)
		}
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- reverse(late.ID, beto) }()
		for waiting := 0; waiting == 0; {
			select {
			case r := <-done:
				t.Fatalf("request did not block on the unique key: status=%d body=%s", r.Code, r.Body)
			case <-time.After(10 * time.Millisecond):
			}
			if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'INSERT INTO finance_entries%'`).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
		}
		if err := blocker.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		wantResponse(t, <-done, 409, `{"error":"already_reversed"}`)
		if got := balance(t); got != before-80000 {
			t.Fatalf("balance=%d want %d", got, before-80000)
		}
		if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM finance_entries WHERE reverses_id=$1`, late.ID).Scan(&links); err != nil || links != 1 {
			t.Fatalf("links=%d err=%v", links, err)
		}
	})
	t.Run("S5 an old entry is reversed with today's date", func(t *testing.T) {
		old := record(t, viajes, "expense", "2026-03-10", 45000, "Fixture")
		before := balance(t)
		r := reverse(old.ID, beto)
		if r.Code != 201 {
			t.Fatalf("status=%d body=%s", r.Code, r.Body)
		}
		got := decode(t, r.Body.Bytes())
		if got.Date != "2026-11-14" || got.Direction != "income" || got.CategoryID != viajes || got.AmountCents != 45000 {
			t.Fatalf("reversal=%+v", got)
		}
		if balance(t) != before+45000 {
			t.Fatal("balance did not move by the reversal")
		}
		if get(t, old.ID).Date != "2026-03-10" {
			t.Fatal("original date changed")
		}
		// Argentina is UTC-3: at 02:30 UTC on 2026-11-15 it is still 2026-11-14 there. The clock
		// stays there, inside the session lifetime, for the remaining subtests.
		f.clk.t = time.Date(2026, 11, 15, 2, 30, 0, 0, time.UTC)
		late := record(t, viajes, "expense", "2026-11-14", 1, "Fixture")
		r = reverse(late.ID, beto)
		if r.Code != 201 || decode(t, r.Body.Bytes()).Date != "2026-11-14" {
			t.Fatalf("argentina date: status=%d body=%s", r.Code, r.Body)
		}
	})
	t.Run("S6 a failed reversal leaves no half state", func(t *testing.T) {
		target := record(t, ventas, "income", "2026-11-14", 30000, "Fixture")
		// The probe fails after the reversal row was inserted, inside the same transaction.
		if _, err := tdb.Super.Exec(ctx, `CREATE FUNCTION finance_reversal_probe() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'probe'; END $$; CREATE TRIGGER finance_reversal_probe AFTER INSERT ON finance_entries FOR EACH ROW WHEN (NEW.reverses_id IS NOT NULL) EXECUTE FUNCTION finance_reversal_probe()`); err != nil {
			t.Fatal(err)
		}
		dropped := false
		drop := func() {
			if !dropped {
				dropped = true
				_, _ = tdb.Super.Exec(context.Background(), `DROP TRIGGER IF EXISTS finance_reversal_probe ON finance_entries; DROP FUNCTION IF EXISTS finance_reversal_probe()`)
			}
		}
		t.Cleanup(drop)
		rejected(t, target.ID, beto, 500, "internal")
		if got := get(t, target.ID); got.ReversalID != "" || got.ReversedBy != "" || got.ReversedOn != "" {
			t.Fatalf("original marked without a reversal: %+v", got)
		}
		drop()
		if r := reverse(target.ID, beto); r.Code != 201 {
			t.Fatalf("retry status=%d body=%s", r.Code, r.Body)
		}
	})
	t.Run("S7 any officer with MFA reverses; others are rejected", func(t *testing.T) {
		target := record(t, ventas, "income", "2026-11-14", 10000, "Fixture")
		for _, c := range []struct {
			name, token string
			status      int
			code        string
		}{{"no session", "", 401, "unauthenticated"}, {"student", student, 403, "forbidden"}, {"school admin", admin, 403, "forbidden"}, {"officer without MFA", unverified, 403, "mfa_required"}} {
			t.Run(c.name, func(t *testing.T) { rejected(t, target.ID, c.token, c.status, c.code) })
		}
		// Veedor: the memberships role CHECK only allows school_admin, officer and student (00002),
		// so its case is tested when #28 adds the role.
		r := reverse(target.ID, beto)
		if r.Code != 201 || decode(t, r.Body.Bytes()).RecordedBy != "Beto Ficticio" {
			t.Fatalf("other officer: status=%d body=%s", r.Code, r.Body)
		}
		for _, token := range []string{ana, student, admin} {
			r := f.do(http.MethodGet, "/api/finance/entries", "", "198.51.100.13", token, nil)
			if r.Code != 200 {
				t.Fatalf("list status=%d", r.Code)
			}
			var rows []reversalEntry
			if err := json.Unmarshal(r.Body.Bytes(), &rows); err != nil {
				t.Fatal(err)
			}
			seenOriginal, seenReversal := false, false
			for _, row := range rows {
				seenOriginal = seenOriginal || (row.ID == tortas.ID && row.ReversalID == reversal.ID && row.ReversedBy == "Beto Ficticio" && row.ReversedOn == "2026-11-14")
				seenReversal = seenReversal || (row.ID == reversal.ID && row.ReversesID == tortas.ID)
			}
			if !seenOriginal || !seenReversal {
				t.Fatalf("reader does not see the reversal: original=%v reversal=%v", seenOriginal, seenReversal)
			}
		}
	})
	t.Run("S8 unknown and other-school entries are not found", func(t *testing.T) {
		other := newTenant(t, ctx)
		if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, other); err != nil {
			t.Fatal(err)
		}
		var foreign string
		if err := tdb.Super.QueryRow(ctx, `INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name) SELECT $1,c.id,'income','2026-11-10',1,'Fixture',u.id,'Ciro Ficticio' FROM finance_categories c, users u WHERE c.tenant_id=$1 AND c.name='Ventas' LIMIT 1 RETURNING id::text`, other).Scan(&foreign); err != nil {
			t.Fatal(err)
		}
		foreignCount := func() int {
			var n int
			if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM finance_entries WHERE tenant_id=$1`, other).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		for _, id := range []string{"00000000-0000-4000-8000-000000000000", "not-a-uuid", foreign} {
			rejected(t, id, beto, 404, "not_found")
		}
		if foreignCount() != 1 {
			t.Fatal("other school's ledger changed")
		}
	})
	t.Run("PRESERVE entries that are not reversals keep their shape", func(t *testing.T) {
		plain := []string{"amountCents", "categoryId", "date", "description", "direction", "hasReceipt", "id", "recordedAt", "recordedBy"}
		keys := func(t *testing.T, raw []byte) string {
			t.Helper()
			var m map[string]json.RawMessage
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, k := range plain {
				if _, ok := m[k]; ok {
					out = append(out, k)
				}
			}
			if len(m) != len(out) {
				return "extra keys"
			}
			return strings.Join(out, ",")
		}
		want := strings.Join(plain, ",")
		body := fmt.Sprintf(`{"categoryId":%q,"direction":"income","date":"2026-11-14","amountCents":5,"description":"Fixture"}`, ventas)
		r := f.do(http.MethodPost, "/api/finance/entries", body, "198.51.100.13", ana, nil)
		if r.Code != 201 || keys(t, r.Body.Bytes()) != want {
			t.Fatalf("POST status=%d body=%s", r.Code, r.Body)
		}
		id := decode(t, r.Body.Bytes()).ID
		if r = f.do(http.MethodGet, "/api/finance/entries/"+id, "", "198.51.100.13", student, nil); r.Code != 200 || keys(t, r.Body.Bytes()) != want {
			t.Fatalf("GET one status=%d body=%s", r.Code, r.Body)
		}
		r = f.do(http.MethodGet, "/api/finance/entries", "", "198.51.100.13", student, nil)
		var rows []json.RawMessage
		if err := json.Unmarshal(r.Body.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range rows {
			if decode(t, row).ID == id {
				found = true
				if keys(t, row) != want {
					t.Fatalf("GET list row=%s", row)
				}
			}
		}
		if !found {
			t.Fatal("plain entry missing from list")
		}
	})
}
