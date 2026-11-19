package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

type calculatorSummaryJSON struct {
	RosterCount int         `json:"rosterCount"`
	Balance     json.Number `json:"balance"`
	OwedOnDues  json.Number `json:"owedOnDues"`
}

func decodeCalculatorSummary(t *testing.T, body []byte) (calculatorSummaryJSON, map[string]any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var summary calculatorSummaryJSON
	if err := dec.Decode(&summary); err != nil {
		t.Fatalf("decode summary %s: %v", body, err)
	}
	dec2 := json.NewDecoder(bytes.NewReader(body))
	dec2.UseNumber()
	var raw map[string]any
	if err := dec2.Decode(&raw); err != nil {
		t.Fatalf("decode raw summary %s: %v", body, err)
	}
	return summary, raw
}

func TestCalculatorSummaryR1TotalsAndExemptions(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, f.tenant); err != nil {
		t.Fatal(err)
	}
	user, officer, _, _ := f.signInMFA(t, ctx, "officer")

	// Empty school returns zeros.
	emptyRes := f.do(http.MethodGet, "/api/finance/calculator-summary", "", "198.51.100.10", officer, nil)
	if emptyRes.Code != http.StatusOK {
		t.Fatalf("empty summary: status=%d body=%s", emptyRes.Code, emptyRes.Body)
	}
	gotEmpty, rawEmpty := decodeCalculatorSummary(t, emptyRes.Body.Bytes())
	if gotEmpty.RosterCount != 0 || gotEmpty.Balance != "0" || gotEmpty.OwedOnDues != "0" || len(rawEmpty) != 3 {
		t.Fatalf("unexpected empty summary: %+v raw=%v", gotEmpty, rawEmpty)
	}

	// Seed 5 roster members: Ana (officer), Beto, Carla, Diego, Elena.
	anaID := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, anaID, f.tenant, user); err != nil {
		t.Fatal(err)
	}
	betoID := seedMember(t, ctx, f.tenant, "Beto Ficticio", "30111223")
	carlaID := seedMember(t, ctx, f.tenant, "Carla Ficticia", "30111224")
	diegoID := seedMember(t, ctx, f.tenant, "Diego Ficticio", "30111225")
	_ = seedMember(t, ctx, f.tenant, "Elena Ficticia", "30111226")

	var incomeCat, expenseCat string
	if err := tdb.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name='Ventas' AND direction='income'`, f.tenant).Scan(&incomeCat); err != nil {
		t.Fatal(err)
	}
	if err := tdb.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name='Viajes' AND direction='expense'`, f.tenant).Scan(&expenseCat); err != nil {
		t.Fatal(err)
	}

	// Record initial income ($160.000) and expense ($12.500) => balance = $147.500 before due payments.
	for _, body := range []string{
		fmt.Sprintf(`{"categoryId":%q,"direction":"income","date":%q,"amountCents":16000000,"description":"Venta inicial"}`, incomeCat, payDate),
		fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":%q,"amountCents":1250000,"description":"Gasto viaje"}`, expenseCat, payDate),
	} {
		if r := f.do(http.MethodPost, "/api/finance/entries", body, "198.51.100.10", officer, nil); r.Code != http.StatusCreated {
			t.Fatalf("entry: status=%d body=%s", r.Code, r.Body)
		}
	}

	// Create Due 1: "Cuota de noviembre" ($2.000 = 200000 cents) for all 5 current members.
	due1Res := f.do(http.MethodPost, "/api/finance/dues", `{"name":"Cuota de noviembre","amountCents":200000,"dueDate":"2026-11-30"}`, "198.51.100.10", officer, nil)
	if due1Res.Code != http.StatusCreated {
		t.Fatalf("due1: status=%d body=%s", due1Res.Code, due1Res.Body)
	}
	var due1 dueDetailJSON
	if err := json.Unmarshal(due1Res.Body.Bytes(), &due1); err != nil {
		t.Fatal(err)
	}

	// Ana pays in full ($2.000) => owes $0; balance +$2.000.
	if r := f.do(http.MethodPost, "/api/finance/dues/"+due1.ID+"/payments", fmt.Sprintf(`{"rosterMemberId":%q,"amountCents":200000,"date":%q}`, anaID, payDate), "198.51.100.10", officer, nil); r.Code != http.StatusCreated {
		t.Fatalf("ana pay: status=%d body=%s", r.Code, r.Body)
	}
	// Beto pays partially ($500) => owes $1.500; balance +$500.
	if r := f.do(http.MethodPost, "/api/finance/dues/"+due1.ID+"/payments", fmt.Sprintf(`{"rosterMemberId":%q,"amountCents":50000,"date":%q}`, betoID, payDate), "198.51.100.10", officer, nil); r.Code != http.StatusCreated {
		t.Fatalf("beto pay: status=%d body=%s", r.Code, r.Body)
	}
	// Carla is exempt => owes $0 (excluded per A13).
	if r := f.do(http.MethodPost, "/api/finance/dues/"+due1.ID+"/exemptions", fmt.Sprintf(`{"rosterMemberId":%q,"reason":"Beca escolar"}`, carlaID), "198.51.100.10", officer, nil); r.Code != http.StatusCreated {
		t.Fatalf("carla exempt: status=%d body=%s", r.Code, r.Body)
	}
	// Diego pays $2.000 and the payment entry is reversed => owes $2.000 again; net balance impact $0.
	diegoPay := f.do(http.MethodPost, "/api/finance/dues/"+due1.ID+"/payments", fmt.Sprintf(`{"rosterMemberId":%q,"amountCents":200000,"date":%q}`, diegoID, payDate), "198.51.100.10", officer, nil)
	if diegoPay.Code != http.StatusCreated {
		t.Fatalf("diego pay: status=%d body=%s", diegoPay.Code, diegoPay.Body)
	}
	var dp paymentJSON
	if err := json.Unmarshal(diegoPay.Body.Bytes(), &dp); err != nil {
		t.Fatal(err)
	}
	if r := f.do(http.MethodPost, "/api/finance/entries/"+dp.EntryID+"/reversal", "", "198.51.100.10", officer, nil); r.Code != http.StatusCreated {
		t.Fatalf("reverse diego pay: status=%d body=%s", r.Code, r.Body)
	}
	// Elena pays nothing => owes $2.000.

	// Create Due 2: "Cuota de diciembre" ($1.000 = 100000 cents) for all 5 current members (none paid => $5.000).
	if r := f.do(http.MethodPost, "/api/finance/dues", `{"name":"Cuota de diciembre","amountCents":100000,"dueDate":"2026-12-15"}`, "198.51.100.10", officer, nil); r.Code != http.StatusCreated {
		t.Fatalf("due2: status=%d body=%s", r.Code, r.Body)
	}

	// Import Franco after both dues: rosterCount becomes 6, owedOnDues stays unaffected by Franco.
	_ = seedMember(t, ctx, f.tenant, "Franco Ficticio", "30111227")

	// Expected:
	// rosterCount = 6
	// balance = 16000000 - 1250000 + 200000 + 50000 + 200000 - 200000 = 15000000 ($150.000)
	// owedOnDues = Due1 (0 + 150000 + 0 + 200000 + 200000 = 550000) + Due2 (5 * 100000 = 500000) = 1050000 ($10.500)
	res := f.do(http.MethodGet, "/api/finance/calculator-summary", "", "198.51.100.10", officer, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("summary: status=%d body=%s", res.Code, res.Body)
	}
	got, raw := decodeCalculatorSummary(t, res.Body.Bytes())
	if got.RosterCount != 6 || got.Balance != "15000000" || got.OwedOnDues != "1050000" {
		t.Fatalf("unexpected summary: got=%+v want rosterCount=6 balance=15000000 owedOnDues=1050000", got)
	}
	if len(raw) != 3 {
		t.Fatalf("summary returned unexpected keys (only totals allowed): %v", raw)
	}

	// Negative balance is returned as negative integer cents.
	if r := f.do(http.MethodPost, "/api/finance/entries", fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":%q,"amountCents":17000000,"description":"Gasto grande"}`, expenseCat, payDate), "198.51.100.10", officer, nil); r.Code != http.StatusCreated {
		t.Fatalf("expense for negative balance: status=%d body=%s", r.Code, r.Body)
	}
	negRes := f.do(http.MethodGet, "/api/finance/calculator-summary", "", "198.51.100.10", officer, nil)
	if negRes.Code != http.StatusOK {
		t.Fatalf("negative balance summary: status=%d body=%s", negRes.Code, negRes.Body)
	}
	gotNeg, _ := decodeCalculatorSummary(t, negRes.Body.Bytes())
	if gotNeg.Balance != "-2000000" || gotNeg.OwedOnDues != "1050000" || gotNeg.RosterCount != 6 {
		t.Fatalf("unexpected negative balance summary: %+v", gotNeg)
	}
}

func TestCalculatorSummaryR4ExactSpendableExample(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, f.tenant); err != nil {
		t.Fatal(err)
	}
	user, officer, _, _ := f.signInMFA(t, ctx, "officer")

	// Seed 480 roster members matching the R2/R4 example (470 non-exempt, 10 exempt, 312 paid, 158 owe $2.000).
	memberIDs := make([]string, 0, 480)
	for i := 0; i < 480; i++ {
		id := seedMember(t, ctx, f.tenant, fmt.Sprintf("Alumno Ficticio %03d", i), fmt.Sprintf("40000%03d", i))
		memberIDs = append(memberIDs, id)
	}
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, memberIDs[0], f.tenant, user); err != nil {
		t.Fatal(err)
	}

	dueRes := f.do(http.MethodPost, "/api/finance/dues", `{"name":"Cuota de noviembre","amountCents":200000,"dueDate":"2026-11-30"}`, "198.51.100.10", officer, nil)
	if dueRes.Code != http.StatusCreated {
		t.Fatalf("create due: status=%d body=%s", dueRes.Code, dueRes.Body)
	}
	var due dueDetailJSON
	if err := json.Unmarshal(dueRes.Body.Bytes(), &due); err != nil {
		t.Fatal(err)
	}

	// Exempt 10 members (470 non-exempt remain), mark 312 as fully paid ($2.000 each -> $624.000),
	// and record a $474.000 expense so current balance is exactly $150.000 (15_000_000 cents).
	var cuotasCat, expenseCat string
	if err := tdb.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name='Cuotas' AND direction='income'`, f.tenant).Scan(&cuotasCat); err != nil {
		t.Fatal(err)
	}
	if err := tdb.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name='Viajes' AND direction='expense'`, f.tenant).Scan(&expenseCat); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		if _, err := tdb.Super.Exec(ctx,
			`INSERT INTO finance_due_exemptions (tenant_id, due_id, roster_member_id, reason, created_by) VALUES ($1,$2,$3,'Beca',$4)`,
			f.tenant, due.ID, memberIDs[i], user); err != nil {
			t.Fatal(err)
		}
	}
	for i := 10; i < 322; i++ {
		var entryID string
		if err := tdb.Super.QueryRow(ctx,
			`INSERT INTO finance_entries (tenant_id, category_id, direction, entry_date, amount_cents, description, recorded_by, recorded_by_name)
			 VALUES ($1,$2,'income',$3,200000,'Pago de cuota: Cuota de noviembre',$4,'Alumno Ficticio 000') RETURNING id::text`,
			f.tenant, cuotasCat, payDate, user).Scan(&entryID); err != nil {
			t.Fatal(err)
		}
		if _, err := tdb.Super.Exec(ctx,
			`INSERT INTO finance_due_payments (tenant_id, due_id, roster_member_id, entry_id, amount_cents, payment_date, recorded_by)
			 VALUES ($1,$2,$3,$4,200000,$5,$6)`,
			f.tenant, due.ID, memberIDs[i], entryID, payDate, user); err != nil {
			t.Fatal(err)
		}
	}
	// Balance after 312 * $2.000 = $624.000; subtract $474.000 expense => balance = $150.000 (15_000_000).
	if r := f.do(http.MethodPost, "/api/finance/entries", fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":%q,"amountCents":47400000,"description":"Gasto evento"}`, expenseCat, payDate), "198.51.100.10", officer, nil); r.Code != http.StatusCreated {
		t.Fatalf("expense: status=%d body=%s", r.Code, r.Body)
	}

	res := f.do(http.MethodGet, "/api/finance/calculator-summary", "", "198.51.100.10", officer, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("summary: status=%d body=%s", res.Code, res.Body)
	}
	got, _ := decodeCalculatorSummary(t, res.Body.Bytes())
	// 158 * $2.000 = $316.000 = 31600000 cents; balance $150.000 = 15000000 cents; total = $466.000.
	if got.RosterCount != 480 || got.Balance != "15000000" || got.OwedOnDues != "31600000" {
		t.Fatalf("R4 example mismatch: got=%+v want rosterCount=480 balance=15000000 owedOnDues=31600000", got)
	}
}

func TestCalculatorSummaryR5OfficerAndMFAOnly(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, f.tenant); err != nil {
		t.Fatal(err)
	}
	officerUser, officerMFA, _, _ := f.signInMFA(t, ctx, "officer")
	anaID := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, anaID, f.tenant, officerUser); err != nil {
		t.Fatal(err)
	}
	_, officerNoMFA := f.signIn(t, ctx, "officer")
	_, adminMFA, _, _ := f.signInMFA(t, ctx, "school_admin")
	_, studentToken, _ := f.student(t, ctx, "Beto Ficticio", "30111223")

	// Multi-role student with an officer_positions row (with MFA and without MFA).
	posMFAUser, posMFA, _, _ := f.signInMFA(t, ctx, "student")
	carlaID := seedMember(t, ctx, f.tenant, "Carla Ficticia", "30111224")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, carlaID, f.tenant, posMFAUser); err != nil {
		t.Fatal(err)
	}
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO officer_positions(tenant_id,roster_member_id,position) VALUES($1,$2,'Tesorero')`, f.tenant, carlaID); err != nil {
		t.Fatal(err)
	}
	posNoMFAUser, posNoMFA := f.signIn(t, ctx, "student")
	diegoID := seedMember(t, ctx, f.tenant, "Diego Ficticio", "30111225")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, diegoID, f.tenant, posNoMFAUser); err != nil {
		t.Fatal(err)
	}
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO officer_positions(tenant_id,roster_member_id,position) VALUES($1,$2,'Secretario')`, f.tenant, diegoID); err != nil {
		t.Fatal(err)
	}

	if r := f.do(http.MethodPost, "/api/finance/dues", `{"name":"Cuota privacidad","amountCents":200000,"dueDate":"2026-11-30"}`, "198.51.100.10", officerMFA, nil); r.Code != http.StatusCreated {
		t.Fatalf("create due: %d %s", r.Code, r.Body)
	}

	// Veedor arrives with #28 (memberships.role CHECK currently allows school_admin, officer, student).
	for _, tc := range []struct {
		name   string
		token  string
		status int
		code   string
	}{
		{"no session", "", http.StatusUnauthorized, "unauthenticated"},
		{"student", studentToken, http.StatusForbidden, "forbidden"},
		{"school_admin with MFA", adminMFA, http.StatusForbidden, "forbidden"},
		{"officer without MFA", officerNoMFA, http.StatusForbidden, "mfa_required"},
		{"position holder without MFA", posNoMFA, http.StatusForbidden, "mfa_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(http.MethodGet, "/api/finance/calculator-summary", "", "198.51.100.10", tc.token, nil)
			wantResponse(t, rec, tc.status, fmt.Sprintf(`{"error":%q}`, tc.code))
		})
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	for _, allowed := range []struct {
		name  string
		token string
	}{
		{"officer with MFA", officerMFA},
		{"position holder with MFA", posMFA},
	} {
		t.Run(allowed.name, func(t *testing.T) {
			rec := f.do(http.MethodGet, "/api/finance/calculator-summary", "", "198.51.100.10", allowed.token, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
			body := rec.Body.String()
			got, raw := decodeCalculatorSummary(t, rec.Body.Bytes())
			if len(raw) != 3 || got.RosterCount != 4 || got.Balance != "0" || got.OwedOnDues != "800000" {
				t.Fatalf("unexpected response: got=%+v raw=%v", got, raw)
			}
			for _, forbidden := range []string{"Ana Ficticia", "Beto Ficticio", "Carla Ficticia", "Diego Ficticio", "30111222", "30111223"} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("summary leaked student data %q: %s", forbidden, body)
				}
			}
		})
	}

	logs := buf.String()
	for _, forbidden := range []string{"Ana Ficticia", "Beto Ficticio", "30111222"} {
		if strings.Contains(logs, forbidden) {
			t.Fatalf("logs leaked %q: %s", forbidden, logs)
		}
	}
}

func TestCalculatorSummaryR6TenantIsolation(t *testing.T) {
	ctx := integration(t)
	fA := newFixture(t, ctx)
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, fA.tenant); err != nil {
		t.Fatal(err)
	}
	uA, offA, _, _ := fA.signInMFA(t, ctx, "officer")
	mA1 := seedMember(t, ctx, fA.tenant, "Ana Escuela A", "31000001")
	_ = seedMember(t, ctx, fA.tenant, "Beto Escuela A", "31000002")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, mA1, fA.tenant, uA); err != nil {
		t.Fatal(err)
	}
	var catA string
	if err := tdb.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name='Ventas' AND direction='income'`, fA.tenant).Scan(&catA); err != nil {
		t.Fatal(err)
	}
	if r := fA.do(http.MethodPost, "/api/finance/entries", fmt.Sprintf(`{"categoryId":%q,"direction":"income","date":%q,"amountCents":500000,"description":"Ingreso A"}`, catA, payDate), "198.51.100.10", offA, nil); r.Code != http.StatusCreated {
		t.Fatalf("entry A: %d %s", r.Code, r.Body)
	}
	if r := fA.do(http.MethodPost, "/api/finance/dues", `{"name":"Cuota A","amountCents":100000,"dueDate":"2026-11-30"}`, "198.51.100.10", offA, nil); r.Code != http.StatusCreated {
		t.Fatalf("due A: %d %s", r.Code, r.Body)
	}

	fB := newFixture(t, ctx)
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, fB.tenant); err != nil {
		t.Fatal(err)
	}
	uB, offB, _, _ := fB.signInMFA(t, ctx, "officer")
	mB1 := seedMember(t, ctx, fB.tenant, "Carla Escuela B", "32000001")
	_ = seedMember(t, ctx, fB.tenant, "Diego Escuela B", "32000002")
	_ = seedMember(t, ctx, fB.tenant, "Elena Escuela B", "32000003")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, mB1, fB.tenant, uB); err != nil {
		t.Fatal(err)
	}
	var catB string
	if err := tdb.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name='Ventas' AND direction='income'`, fB.tenant).Scan(&catB); err != nil {
		t.Fatal(err)
	}
	if r := fB.do(http.MethodPost, "/api/finance/entries", fmt.Sprintf(`{"categoryId":%q,"direction":"income","date":%q,"amountCents":900000,"description":"Ingreso B"}`, catB, payDate), "198.51.100.10", offB, nil); r.Code != http.StatusCreated {
		t.Fatalf("entry B: %d %s", r.Code, r.Body)
	}
	if r := fB.do(http.MethodPost, "/api/finance/dues", `{"name":"Cuota B","amountCents":300000,"dueDate":"2026-11-30"}`, "198.51.100.10", offB, nil); r.Code != http.StatusCreated {
		t.Fatalf("due B: %d %s", r.Code, r.Body)
	}

	resA := fA.do(http.MethodGet, "/api/finance/calculator-summary", "", "198.51.100.10", offA, nil)
	if resA.Code != http.StatusOK {
		t.Fatalf("summary A: %d %s", resA.Code, resA.Body)
	}
	gotA, _ := decodeCalculatorSummary(t, resA.Body.Bytes())
	if gotA.RosterCount != 2 || gotA.Balance != "500000" || gotA.OwedOnDues != "200000" {
		t.Fatalf("school A leaked school B: got=%+v", gotA)
	}

	resB := fB.do(http.MethodGet, "/api/finance/calculator-summary", "", "198.51.100.10", offB, nil)
	if resB.Code != http.StatusOK {
		t.Fatalf("summary B: %d %s", resB.Code, resB.Body)
	}
	gotB, _ := decodeCalculatorSummary(t, resB.Body.Bytes())
	if gotB.RosterCount != 3 || gotB.Balance != "900000" || gotB.OwedOnDues != "900000" {
		t.Fatalf("school B leaked school A: got=%+v", gotB)
	}
}

func TestCalculatorSummaryR7ReadOnly(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, f.tenant); err != nil {
		t.Fatal(err)
	}
	user, officer, _, _ := f.signInMFA(t, ctx, "officer")
	anaID := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30111222")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, anaID, f.tenant, user); err != nil {
		t.Fatal(err)
	}
	var catID string
	if err := tdb.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name='Ventas' AND direction='income'`, f.tenant).Scan(&catID); err != nil {
		t.Fatal(err)
	}
	if r := f.do(http.MethodPost, "/api/finance/entries", fmt.Sprintf(`{"categoryId":%q,"direction":"income","date":%q,"amountCents":300000,"description":"Venta"}`, catID, payDate), "198.51.100.10", officer, nil); r.Code != http.StatusCreated {
		t.Fatalf("entry: %d %s", r.Code, r.Body)
	}
	if r := f.do(http.MethodPost, "/api/finance/dues", `{"name":"Cuota noviembre","amountCents":200000,"dueDate":"2026-11-30"}`, "198.51.100.10", officer, nil); r.Code != http.StatusCreated {
		t.Fatalf("due: %d %s", r.Code, r.Body)
	}

	balanceBefore := f.do(http.MethodGet, "/api/finance/balance", "", "198.51.100.10", officer, nil).Body.String()
	entriesBefore := f.do(http.MethodGet, "/api/finance/entries", "", "198.51.100.10", officer, nil).Body.String()
	duesBefore := f.do(http.MethodGet, "/api/finance/dues", "", "198.51.100.10", officer, nil).Body.String()

	res := f.do(http.MethodGet, "/api/finance/calculator-summary", "", "198.51.100.10", officer, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("summary: %d %s", res.Code, res.Body)
	}

	if got := f.do(http.MethodGet, "/api/finance/balance", "", "198.51.100.10", officer, nil).Body.String(); got != balanceBefore {
		t.Fatalf("balance changed: before=%s after=%s", balanceBefore, got)
	}
	if got := f.do(http.MethodGet, "/api/finance/entries", "", "198.51.100.10", officer, nil).Body.String(); got != entriesBefore {
		t.Fatalf("entries changed: before=%s after=%s", entriesBefore, got)
	}
	if got := f.do(http.MethodGet, "/api/finance/dues", "", "198.51.100.10", officer, nil).Body.String(); got != duesBefore {
		t.Fatalf("dues changed: before=%s after=%s", duesBefore, got)
	}
}
