package httpapi_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const reportIP = "198.51.100.40"

// reportFixture is a school with seeded Categories and a Treasurer (Carla Ficticia) with a
// two-factor session who records Entries through the API.
type reportFixture struct {
	*fixture
	ctx     context.Context
	officer string
	cat     map[string]string // "income/Ventas" -> id
}

func newReportFixture(t *testing.T, ctx context.Context, now time.Time) *reportFixture {
	t.Helper()
	f := newFixture(t, ctx)
	f.clk.t = now
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, f.tenant); err != nil {
		t.Fatal(err)
	}
	user, token, _, _ := f.signInMFA(t, ctx, "officer")
	member := seedMember(t, ctx, f.tenant, "Carla Ficticia", "30111301")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, member, f.tenant, user); err != nil {
		t.Fatal(err)
	}
	rf := &reportFixture{fixture: f, ctx: ctx, officer: token, cat: map[string]string{}}
	rows, err := tdb.Super.Query(ctx, `SELECT direction||'/'||name, id::text FROM finance_categories WHERE tenant_id=$1`, f.tenant)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, id string
		if err := rows.Scan(&key, &id); err != nil {
			t.Fatal(err)
		}
		rf.cat[key] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return rf
}

// record posts an Entry of the Category "direction/name" and returns its id.
func (rf *reportFixture) record(t *testing.T, category, date string, cents int64, description string) string {
	t.Helper()
	direction, _, _ := strings.Cut(category, "/")
	body := fmt.Sprintf(`{"categoryId":%q,"direction":%q,"date":%q,"amountCents":%d,"description":%q}`, rf.cat[category], direction, date, cents, description)
	r := rf.do(http.MethodPost, "/api/finance/entries", body, reportIP, rf.officer, nil)
	if r.Code != 201 {
		t.Fatalf("record status=%d body=%s", r.Code, r.Body)
	}
	var out struct{ ID string }
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.ID
}

func (rf *reportFixture) get(path, token string) *httptest.ResponseRecorder {
	return rf.do(http.MethodGet, path, "", reportIP, token, nil)
}

// ledgerState is every stored row of the tenant plus the balance the API reports, so a read can
// prove it wrote nothing.
func (rf *reportFixture) ledgerState(t *testing.T, token string) string {
	t.Helper()
	var stored string
	if err := tdb.Super.QueryRow(rf.ctx, `SELECT count(*)::text || ':' || COALESCE(md5(string_agg(id::text || direction || amount_cents || entry_date || description || COALESCE(reverses_id::text, '-'), ',' ORDER BY id)), '') FROM finance_entries WHERE tenant_id=$1`, rf.tenant).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	list := rf.get("/api/finance/entries", token)
	balance := rf.get("/api/finance/balance", token)
	if list.Code != 200 || balance.Code != 200 {
		t.Fatalf("ledger read status=%d/%d", list.Code, balance.Code)
	}
	return stored + "|" + list.Body.String() + "|" + balance.Body.String()
}

type reportTotal struct {
	CategoryID string `json:"categoryId"`
	Name       string `json:"name"`
	Direction  string `json:"direction"`
	TotalCents int64  `json:"totalCents"`
}

type reportBody struct {
	From         string        `json:"from"`
	To           string        `json:"to"`
	OpeningCents int64         `json:"openingCents"`
	ClosingCents int64         `json:"closingCents"`
	IncomeCents  int64         `json:"incomeCents"`
	ExpenseCents int64         `json:"expenseCents"`
	Totals       []reportTotal `json:"totals"`
	Entries      []reportEntry `json:"entries"`
}

// reportEntry is the FinanceEntry shape the report returns, the same one the ledger lists.
type reportEntry struct {
	reversalEntry
	HasReceipt bool      `json:"hasReceipt"`
	RecordedAt time.Time `json:"recordedAt"`
}

func decodeReport(t *testing.T, r *httptest.ResponseRecorder) reportBody {
	t.Helper()
	if r.Code != 200 {
		t.Fatalf("report status=%d body=%s", r.Code, r.Body)
	}
	if ct := r.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type=%q", ct)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(r.Body.Bytes(), &keys); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 8 {
		t.Fatalf("report keys=%d body=%s", len(keys), r.Body)
	}
	var out reportBody
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// readCSV checks the BOM and the CSV headers, and parses the body with `;` as separator.
func readCSV(t *testing.T, r *httptest.ResponseRecorder, filename string) [][]string {
	t.Helper()
	if r.Code != 200 {
		t.Fatalf("csv status=%d body=%s", r.Code, r.Body)
	}
	if ct := r.Header().Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Fatalf("Content-Type=%q", ct)
	}
	if cd := r.Header().Get("Content-Disposition"); cd != `attachment; filename="`+filename+`"` {
		t.Fatalf("Content-Disposition=%q", cd)
	}
	body := r.Body.Bytes()
	if len(body) < 3 || body[0] != 0xEF || body[1] != 0xBB || body[2] != 0xBF {
		t.Fatalf("missing UTF-8 BOM: % x", body[:min(len(body), 3)])
	}
	cr := csv.NewReader(strings.NewReader(string(body[3:])))
	cr.Comma = ';'
	cr.FieldsPerRecord = -1
	rows, err := cr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

const ledgerHeader = "fecha;tipo;Categoría;descripción;monto;registrado por;revertido;con comprobante"

// S1 (R1): the issue's example, with exact cents.
func TestFinanceReportExample(t *testing.T) {
	ctx := integration(t)
	rf := newReportFixture(t, ctx, time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC))
	rf.record(t, "income/Ventas", "2026-02-28", 1000000, "Saldo anterior")
	rf.record(t, "income/Ventas", "2026-03-01", 200000, "Venta de tortas")
	rf.record(t, "expense/Alquileres", "2026-11-30", 1500000, "Alquiler del salón")
	rf.record(t, "income/Ventas", "2026-12-01", 99, "Fuera del rango")
	got := decodeReport(t, rf.get("/api/finance/report?from=2026-03-01&to=2026-11-30", rf.officer))
	if got.From != "2026-03-01" || got.To != "2026-11-30" || got.OpeningCents != 1000000 || got.ClosingCents != -300000 || got.IncomeCents != 200000 || got.ExpenseCents != -1500000 {
		t.Fatalf("report=%+v", got)
	}
	want := []reportTotal{
		{CategoryID: rf.cat["income/Ventas"], Name: "Ventas", Direction: "income", TotalCents: 200000},
		{CategoryID: rf.cat["expense/Alquileres"], Name: "Alquileres", Direction: "expense", TotalCents: -1500000},
	}
	if fmt.Sprint(got.Totals) != fmt.Sprint(want) {
		t.Fatalf("totals=%+v want %+v", got.Totals, want)
	}
	if len(got.Entries) != 2 || got.Entries[0].Description != "Venta de tortas" || got.Entries[0].AmountCents != 200000 || got.Entries[0].RecordedBy != "Carla Ficticia" ||
		got.Entries[1].Description != "Alquiler del salón" || got.Entries[1].Direction != "expense" || got.Entries[1].Date != "2026-11-30" {
		t.Fatalf("entries=%+v", got.Entries)
	}
}

// S2 (R2): an income of March reversed in November adds in March and subtracts in November.
func TestFinanceReportReversal(t *testing.T) {
	ctx := integration(t)
	rf := newReportFixture(t, ctx, time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC))
	sale := rf.record(t, "income/Ventas", "2026-03-10", 500000, "Venta de rifas")
	r := rf.do(http.MethodPost, "/api/finance/entries/"+sale+"/reversal", "", reportIP, rf.officer, nil)
	if r.Code != 201 {
		t.Fatalf("reversal status=%d body=%s", r.Code, r.Body)
	}
	ventas := rf.cat["income/Ventas"]
	march := decodeReport(t, rf.get("/api/finance/report?from=2026-03-01&to=2026-03-31", rf.officer))
	if march.OpeningCents != 0 || march.ClosingCents != 500000 || march.IncomeCents != 500000 || march.ExpenseCents != 0 ||
		fmt.Sprint(march.Totals) != fmt.Sprint([]reportTotal{{ventas, "Ventas", "income", 500000}}) ||
		len(march.Entries) != 1 || march.Entries[0].ID != sale || march.Entries[0].ReversalID == "" {
		t.Fatalf("march=%+v", march)
	}
	november := decodeReport(t, rf.get("/api/finance/report?from=2026-11-01&to=2026-11-30", rf.officer))
	if november.OpeningCents != 500000 || november.ClosingCents != 0 || november.IncomeCents != -500000 || november.ExpenseCents != 0 ||
		fmt.Sprint(november.Totals) != fmt.Sprint([]reportTotal{{ventas, "Ventas", "income", -500000}}) ||
		len(november.Entries) != 1 || november.Entries[0].ReversesID != sale || november.Entries[0].Date != "2026-11-20" {
		t.Fatalf("november=%+v", november)
	}
}

// S3 (R3): invalid ranges answer 422 invalid_range; an empty range answers zeros.
func TestFinanceReportInvalidRange(t *testing.T) {
	ctx := integration(t)
	rf := newReportFixture(t, ctx, time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC))
	rf.record(t, "income/Ventas", "2026-02-10", 700000, "Kermesse")
	before := rf.ledgerState(t, rf.officer)
	invalid := []string{"from=2026-11-30&to=2026-03-01", "from=2026-02-30&to=2026-03-01", "from=abc&to=2026-03-01", "from=2026-03-01&to=xyz",
		"from=2026-3-01&to=2026-03-31", "to=2026-03-01", "from=2026-03-01", "from=&to=", ""}
	for _, path := range []string{"/api/finance/report", "/api/finance/report.csv"} {
		for _, q := range invalid {
			t.Run(path+"?"+q, func(t *testing.T) {
				wantResponse(t, rf.get(path+"?"+q, rf.officer), 422, `{"error":"invalid_range"}`)
			})
		}
	}
	for _, q := range []string{"from=2026-03-01", "to=2026-03-01", "from=2026-11-30&to=2026-03-01", "from=abc&to=2026-03-01", "from=2026-02-30&to=2026-03-01"} {
		t.Run("entries.csv?"+q, func(t *testing.T) {
			wantResponse(t, rf.get("/api/finance/entries.csv?"+q, rf.officer), 422, `{"error":"invalid_range"}`)
		})
	}
	t.Run("empty range", func(t *testing.T) {
		got := decodeReport(t, rf.get("/api/finance/report?from=2026-06-01&to=2026-06-30", rf.officer))
		if got.OpeningCents != 700000 || got.ClosingCents != 700000 || got.IncomeCents != 0 || got.ExpenseCents != 0 || got.Totals == nil || len(got.Totals) != 0 || got.Entries == nil || len(got.Entries) != 0 {
			t.Fatalf("report=%+v", got)
		}
		rows := readCSV(t, rf.get("/api/finance/report.csv?from=2026-06-01&to=2026-06-30", rf.officer), "informe-de-caja-2026-06-01-a-2026-06-30.csv")
		if len(rows) != 4 || strings.Join(rows[1], ";") != "Saldo inicial;;7000,00" || strings.Join(rows[2], ";") != "Saldo final;;7000,00" {
			t.Fatalf("rows=%q", rows)
		}
	})
	if rf.ledgerState(t, rf.officer) != before {
		t.Fatal("reading the report changed the ledger")
	}
}

// S4 (R4): BOM, `;`, the ledger columns in order and amounts with decimal comma.
func TestFinanceCSVFormat(t *testing.T) {
	ctx := integration(t)
	rf := newReportFixture(t, ctx, time.Date(2026, 11, 30, 15, 0, 0, 0, time.UTC))
	_, student := rf.signIn(t, ctx, "student")
	rf.record(t, "income/Ventas", "2026-02-28", 1000000, "Saldo anterior")
	sale := rf.record(t, "income/Ventas", "2026-03-15", 200005, "Venta de tortas; con glaseado")
	rf.record(t, "expense/Alquileres", "2026-11-20", 1500000, "Alquiler del salón")
	rf.clk.Advance(time.Minute)
	if r := rf.do(http.MethodPost, "/api/finance/entries/"+sale+"/reversal", "", reportIP, rf.officer, nil); r.Code != 201 {
		t.Fatalf("reversal status=%d body=%s", r.Code, r.Body)
	}
	bom := string(rune(0xFEFF))
	ledger := ledgerHeader + "\r\n" +
		"2026-03-15;ingreso;Ventas;\"Venta de tortas; con glaseado\";2000,05;Carla Ficticia;sí;no\r\n" +
		"2026-11-20;gasto;Alquileres;Alquiler del salón;-15000,00;Carla Ficticia;no;no\r\n" +
		"2026-11-30;gasto;Ventas;\"Reversión: Venta de tortas; con glaseado\";-2000,05;Carla Ficticia;no;no\r\n"
	t.Run("entries.csv whole ledger", func(t *testing.T) {
		r := rf.get("/api/finance/entries.csv", student)
		readCSV(t, r, "libro-de-caja.csv")
		want := bom + ledgerHeader + "\r\n" + "2026-02-28;ingreso;Ventas;Saldo anterior;10000,00;Carla Ficticia;no;no\r\n" + strings.TrimPrefix(ledger, ledgerHeader+"\r\n")
		if r.Body.String() != want {
			t.Fatalf("body=\n%q\nwant\n%q", r.Body.String(), want)
		}
	})
	t.Run("entries.csv range", func(t *testing.T) {
		r := rf.get("/api/finance/entries.csv?from=2026-03-01&to=2026-11-30", student)
		readCSV(t, r, "libro-de-caja-2026-03-01-a-2026-11-30.csv")
		if r.Body.String() != bom+ledger {
			t.Fatalf("body=\n%q\nwant\n%q", r.Body.String(), bom+ledger)
		}
	})
	t.Run("report.csv", func(t *testing.T) {
		r := rf.get("/api/finance/report.csv?from=2026-03-01&to=2026-11-30", student)
		readCSV(t, r, "informe-de-caja-2026-03-01-a-2026-11-30.csv")
		want := bom + "concepto;tipo;monto\r\n" +
			"Saldo inicial;;10000,00\r\n" +
			"Ventas;ingreso;0,00\r\n" +
			"Alquileres;gasto;-15000,00\r\n" +
			"Saldo final;;-5000,00\r\n" +
			"\r\n" + ledger
		if r.Body.String() != want {
			t.Fatalf("body=\n%q\nwant\n%q", r.Body.String(), want)
		}
	})
}

// S5 (R5): a Student's CSV never names other students and has no "alumno" column; the Board's
// and the School admin's have it.
func TestFinanceCSVStudentPrivacy(t *testing.T) {
	ctx := integration(t)
	rf := newReportFixture(t, ctx, time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC))
	seedMember(t, ctx, rf.tenant, "Ana Ficticia", "30111302")
	_, beto, _ := rf.student(t, ctx, "Beto Ficticio", "30111303")
	if r := rf.do(http.MethodPost, "/api/finance/dues", `{"name":"Cuota de marzo","amountCents":150000,"dueDate":"2026-12-10"}`, reportIP, rf.officer, nil); r.Code != 201 {
		t.Fatalf("due status=%d body=%s", r.Code, r.Body)
	}
	rf.record(t, "income/Ventas", "2026-03-15", 200000, "Venta de tortas")
	_, admin, _, _ := rf.signInMFA(t, ctx, "school_admin")
	paths := map[string]string{
		"/api/finance/entries.csv":                              "libro-de-caja.csv",
		"/api/finance/report.csv?from=2026-01-01&to=2026-11-30": "informe-de-caja-2026-01-01-a-2026-11-30.csv",
	}
	for path, file := range paths {
		t.Run("student "+path, func(t *testing.T) {
			rows := readCSV(t, rf.get(path, beto), file)
			sawHeader := false
			for _, row := range rows {
				for _, cell := range row {
					if strings.Contains(cell, "Ana Ficticia") || cell == "alumno" {
						t.Fatalf("student CSV leaks %q in row %q", cell, row)
					}
				}
				if strings.Join(row, ";") == ledgerHeader {
					sawHeader = true
				}
			}
			if !sawHeader {
				t.Fatalf("no ledger header in %q", rows)
			}
		})
		for name, token := range map[string]string{"officer": rf.officer, "school admin": admin} {
			t.Run(name+" "+path, func(t *testing.T) {
				rows := readCSV(t, rf.get(path, token), file)
				saw := false
				for _, row := range rows {
					if strings.Join(row, ";") == ledgerHeader+";alumno" {
						saw = true
					}
					if row[0] == "2026-03-15" && (len(row) != 9 || row[8] != "") {
						t.Fatalf("row=%q", row)
					}
				}
				if !saw {
					t.Fatalf("no alumno column in %q", rows)
				}
			})
		}
	}
}

// S6 (R6): who reads, and reading writes nothing.
func TestFinanceReportReaders(t *testing.T) {
	ctx := integration(t)
	rf := newReportFixture(t, ctx, time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC))
	rf.record(t, "income/Ventas", "2026-03-15", 200000, "Venta de tortas")
	_, student := rf.signIn(t, ctx, "student")
	_, admin := rf.signIn(t, ctx, "school_admin")
	_, outsider := rf.signIn(t, ctx, "")
	before := rf.ledgerState(t, student)
	paths := []string{"/api/finance/report?from=2026-03-01&to=2026-11-30", "/api/finance/report.csv?from=2026-03-01&to=2026-11-30", "/api/finance/entries.csv"}
	for _, path := range paths {
		for name, token := range map[string]string{"officer": rf.officer, "student": student, "school admin": admin} {
			t.Run(name+" "+path, func(t *testing.T) {
				r := rf.get(path, token)
				if r.Code != 200 {
					t.Fatalf("status=%d body=%s", r.Code, r.Body)
				}
				if cc := r.Header().Get("Cache-Control"); cc != "no-store" {
					t.Fatalf("Cache-Control=%q", cc)
				}
			})
		}
		t.Run("no session "+path, func(t *testing.T) {
			wantResponse(t, rf.get(path, ""), 401, unauthBody)
		})
		t.Run("no membership "+path, func(t *testing.T) {
			wantResponse(t, rf.get(path, outsider), 403, `{"error":"forbidden"}`)
		})
	}
	if rf.ledgerState(t, student) != before {
		t.Fatal("reading the report changed the ledger or the balance")
	}
}

// S7 (R7): school A's report never adds school B's Entries.
func TestFinanceReportIsolation(t *testing.T) {
	ctx := integration(t)
	rf := newReportFixture(t, ctx, time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC))
	rf.record(t, "income/Ventas", "2026-03-15", 200000, "Venta de A")
	other := newTenant(t, ctx)
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, other); err != nil {
		t.Fatal(err)
	}
	var officerUser string
	if err := tdb.Super.QueryRow(ctx, `SELECT recorded_by::text FROM finance_entries WHERE tenant_id=$1`, rf.tenant).Scan(&officerUser); err != nil {
		t.Fatal(err)
	}
	for _, e := range []struct{ date, dir, name string }{{"2026-01-10", "income", "Ventas"}, {"2026-03-20", "income", "Ventas"}, {"2026-04-02", "expense", "Viajes"}} {
		if _, err := tdb.Super.Exec(ctx, `INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name)
			SELECT $1,id,$2,$3::date,987654,'Solo de B',$4,'Beto Ficticio' FROM finance_categories WHERE tenant_id=$1 AND name=$5 AND direction=$2`, other, e.dir, e.date, officerUser, e.name); err != nil {
			t.Fatal(err)
		}
	}
	got := decodeReport(t, rf.get("/api/finance/report?from=2026-03-01&to=2026-11-30", rf.officer))
	if got.OpeningCents != 0 || got.ClosingCents != 200000 || got.IncomeCents != 200000 || got.ExpenseCents != 0 || len(got.Totals) != 1 || got.Totals[0].CategoryID != rf.cat["income/Ventas"] || len(got.Entries) != 1 {
		t.Fatalf("report=%+v", got)
	}
	for path, file := range map[string]string{"/api/finance/entries.csv": "libro-de-caja.csv", "/api/finance/report.csv?from=2026-01-01&to=2026-11-30": "informe-de-caja-2026-01-01-a-2026-11-30.csv"} {
		r := rf.get(path, rf.officer)
		readCSV(t, r, file)
		if body := r.Body.String(); strings.Contains(body, "Solo de B") || strings.Contains(body, "9876,54") || strings.Contains(body, "Viajes") {
			t.Fatalf("%s leaks school B: %q", path, body)
		}
	}
}

// S8 (R8): a cell starting with =, +, - or @ gets a leading '; amounts stay numbers.
func TestFinanceCSVInjection(t *testing.T) {
	ctx := integration(t)
	rf := newReportFixture(t, ctx, time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC))
	var rifa string
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO finance_categories(tenant_id,direction,name) VALUES ($1,'income','@Rifa') RETURNING id::text`, rf.tenant).Scan(&rifa); err != nil {
		t.Fatal(err)
	}
	rf.cat["income/@Rifa"] = rifa
	rf.record(t, "income/Ventas", "2026-03-01", 100, `=HYPERLINK("http://x","y")`)
	rf.record(t, "income/Ventas", "2026-03-02", 100, "+1+1")
	rf.record(t, "expense/Viajes", "2026-03-03", 1500, "-2+3")
	rf.record(t, "income/@Rifa", "2026-03-04", 100, "@SUM(A1)")
	want := map[string][2]string{ // date -> {Categoría, descripción}
		"2026-03-01": {"Ventas", `'=HYPERLINK("http://x","y")`},
		"2026-03-02": {"Ventas", "'+1+1"},
		"2026-03-03": {"Viajes", "'-2+3"},
		"2026-03-04": {"'@Rifa", "'@SUM(A1)"},
	}
	for path, file := range map[string]string{"/api/finance/entries.csv": "libro-de-caja.csv", "/api/finance/report.csv?from=2026-03-01&to=2026-03-31": "informe-de-caja-2026-03-01-a-2026-03-31.csv"} {
		t.Run(path, func(t *testing.T) {
			rows := readCSV(t, rf.get(path, rf.officer), file)
			seen := 0
			for _, row := range rows {
				if w, ok := want[row[0]]; ok {
					seen++
					if row[2] != w[0] || row[3] != w[1] {
						t.Fatalf("row=%q want %q", row, w)
					}
					if row[0] == "2026-03-03" && row[4] != "-15,00" {
						t.Fatalf("amount=%q, want -15,00 without prefix", row[4])
					}
				}
				if row[0] == "'@Rifa" && (row[1] != "ingreso" || row[2] != "1,00") {
					t.Fatalf("summary row=%q", row)
				}
			}
			if seen != 4 {
				t.Fatalf("rows=%q", rows)
			}
		})
	}
	// The JSON report keeps the text as recorded; only the CSV escapes it.
	got := decodeReport(t, rf.get("/api/finance/report?from=2026-03-01&to=2026-03-31", rf.officer))
	if len(got.Entries) != 4 || got.Entries[0].Description != `=HYPERLINK("http://x","y")` {
		t.Fatalf("entries=%+v", got.Entries)
	}
}
