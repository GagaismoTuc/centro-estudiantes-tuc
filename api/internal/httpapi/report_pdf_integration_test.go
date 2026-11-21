package httpapi_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.yaml.in/yaml/v3"
)

func TestFinanceReportPDFWithGotenberg(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	compose, err := os.ReadFile("../../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Services map[string]struct {
			Image string `yaml:"image"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(compose, &config); err != nil {
		t.Fatal(err)
	}
	image := config.Services["gotenberg"].Image
	if image == "" {
		t.Fatal("missing converter image")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{Started: true,
		ContainerRequest: testcontainers.ContainerRequest{Image: image, ExposedPorts: []string{"3000/tcp"}, WaitingFor: wait.ForHTTP("/health").WithPort("3000/tcp").WithStartupTimeout(2 * time.Minute)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "3000/tcp")
	if err != nil {
		t.Fatal(err)
	}
	dbctx := integration(t)
	rf := newReportFixture(t, dbctx, time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC))
	rf.ctx = ctx
	rf.record(t, "income/Ventas", "2026-02-28", 1000000, "Saldo anterior")
	rf.record(t, "income/Ventas", "2026-03-01", 200000, "Venta ficticia")
	rf.record(t, "expense/Alquileres", "2026-11-30", 1500000, "Alquiler ficticio")
	other := newTenant(t, ctx)
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, other); err != nil {
		t.Fatal(err)
	}
	var user string
	if err := tdb.Super.QueryRow(ctx, `SELECT recorded_by::text FROM finance_entries WHERE tenant_id=$1 LIMIT 1`, rf.tenant).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name)
		SELECT $1,id,'income','2026-03-20',987654,'Other school only',$2,'Fictitious' FROM finance_categories WHERE tenant_id=$1 AND name='Ventas' AND direction='income'`, other, user); err != nil {
		t.Fatal(err)
	}
	pdfRouter(rf.fixture, fmt.Sprintf("http://%s:%s", host, port.Port()))
	path := "/api/finance/report.pdf?from=2026-03-01&to=2026-11-30"
	before := rf.ledgerState(t, rf.officer)
	response := rf.get(path, rf.officer)
	if response.Code != http.StatusOK {
		t.Fatalf("real converter: %d %s", response.Code, response.Body)
	}
	text, err := converterText(response.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"10.000,00", "Ventas", "2.000,00", "Alquileres", "-15.000,00", "-3.000,00", "Venta ficticia"} {
		if !strings.Contains(text, want) {
			t.Errorf("PDF missing %q in %q", want, text)
		}
	}
	if strings.Contains(text, "Other school only") || strings.Contains(text, "9.876,54") {
		t.Fatalf("PDF contains another school's data: %q", text)
	}
	pay := newPayFixture(t)
	pay.payOK(t, 100000)
	pdfRouter(pay.fixture, fmt.Sprintf("http://%s:%s", host, port.Port()))
	payPath := "/api/finance/report.pdf?from=2026-11-01&to=2026-11-30"
	for _, reader := range []struct {
		token     string
		seesPayer bool
	}{{pay.beto, false}, {pay.officer, true}} {
		res := pay.do(http.MethodGet, payPath, "", reportIP, reader.token, nil)
		if res.Code != http.StatusOK {
			t.Fatalf("payment report: %d %s", res.Code, res.Body)
		}
		text, err := converterText(res.Body.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "Beto Ficticio") != reader.seesPayer {
			t.Fatalf("PDF payer visibility incorrect: %q", text)
		}
	}
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name)
		SELECT e.tenant_id,e.category_id,'income','2026-03-15',100,'Large entry '||n,e.recorded_by,e.recorded_by_name
		FROM finance_entries e CROSS JOIN generate_series(1,4998) n WHERE e.tenant_id=$1 AND e.description='Venta ficticia'`, rf.tenant); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewUnstartedServer(rf.h)
	api.Config.ReadHeaderTimeout, api.Config.ReadTimeout, api.Config.WriteTimeout = 5*time.Second, 15*time.Second, 15*time.Second
	api.Start()
	defer api.Close()
	req, err := http.NewRequest(http.MethodGet, api.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: cookieName, Value: rf.officer})
	started := time.Now()
	large, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(large.Body)
	_ = large.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if large.StatusCode != http.StatusOK || !bytes.HasPrefix(body, []byte("%PDF-")) {
		t.Fatalf("large report: %d %s after %s", large.StatusCode, body, time.Since(started))
	}
	t.Logf("5000 movement report returned in %s", time.Since(started))
	text, err = converterText(body)
	if err != nil {
		t.Fatal(err)
	}
	matches := regexp.MustCompile(`Large entry \d+`).FindAllString(text, -1)
	unique := make(map[string]bool, len(matches))
	for _, match := range matches {
		unique[match] = true
	}
	if len(matches) != 4998 || len(unique) != 4998 || !unique["Large entry 1"] || !unique["Large entry 4998"] {
		t.Fatalf("large PDF contains %d entries (%d unique), want 4998", len(matches), len(unique))
	}
	for _, entry := range []string{"Venta ficticia", "Alquiler ficticio"} {
		if !strings.Contains(text, entry) {
			t.Fatalf("large PDF omitted %q", entry)
		}
	}

	before = rf.ledgerState(t, rf.officer)
	if err := container.Stop(ctx, nil); err != nil {
		t.Fatal(err)
	}
	wantResponse(t, rf.get(path, rf.officer), http.StatusServiceUnavailable, `{"error":"pdf_unavailable"}`)
	if after := rf.ledgerState(t, rf.officer); before != after {
		t.Fatal("conversion changed ledger or balance")
	}
}

func accountabilityGotenberg(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	compose, err := os.ReadFile("../../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Services map[string]struct {
			Image string `yaml:"image"`
		} `yaml:"services"`
	}
	if err = yaml.Unmarshal(compose, &config); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	t.Cleanup(cancel)
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{Started: true, ContainerRequest: testcontainers.ContainerRequest{Image: config.Services["gotenberg"].Image, ExposedPorts: []string{"3000/tcp"}, WaitingFor: wait.ForHTTP("/health").WithPort("3000/tcp").WithStartupTimeout(2 * time.Minute)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "3000/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("http://%s:%s", host, port.Port())
}

// TestAccountabilityPDFPrivacyWithGotenberg extracts actual PDF text.
func TestAccountabilityPDFPrivacyWithGotenberg(t *testing.T) {
	url := accountabilityGotenberg(t)
	rf := newAcctFixture(t)
	march := rf.record(t, "income/Ventas", "2026-03-01", 50000, "Kiosco de marzo")
	_, student, beto := rf.student(t, rf.ctx, "Beto Ficticio", "30111223")
	_, _, eva := rf.student(t, rf.ctx, "Eva Ficticia", "30111224")
	empty, _ := rf.publishOK(t, rf.officer, "2026-03-01", "2026-11-30", "Gesti\u00f3n 2026")
	due := rf.createDue(t, "Cuota de noviembre", "2026-11-15", 200000)
	rf.payDue(t, due, beto, "2026-11-10", 200000)
	exempt := rf.do(http.MethodPost, "/api/finance/dues/"+due+"/exemptions", fmt.Sprintf(`{"rosterMemberId":%q,"reason":"Beca"}`, eva), reportIP, rf.officer, nil)
	if exempt.Code != http.StatusCreated {
		t.Fatalf("exemption: %d %s", exempt.Code, exempt.Body)
	}
	rep, _ := rf.publishOK(t, rf.officer, "2026-03-01", "2026-11-30", "Gesti\u00f3n 2026")
	pdfRouter(rf.fixture, url)
	emptyPDF := rf.get(acctPath+"/"+empty.ID+".pdf", student)
	if emptyPDF.Code != 200 {
		t.Fatalf("empty PDF: %d %s", emptyPDF.Code, emptyPDF.Body)
	}
	emptyText, err := converterText(emptyPDF.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(emptyText, "Sin cuotas en el per\u00edodo") || strings.Contains(emptyText, "{{") {
		t.Fatalf("empty PDF: %q", emptyText)
	}
	response := rf.get(acctPath+"/"+rep.ID+".pdf", student)
	if response.Code != 200 {
		t.Fatalf("PDF: %d %s", response.Code, response.Body)
	}
	text, err := converterText(response.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Cuotas - ingreso", "Cuota de noviembre - 1 de 2 pagaron"} {
		if !strings.Contains(text, want) {
			t.Errorf("PDF missing spaced text %q: %q", want, text)
		}
	}
	if strings.Contains(text, "Beto Ficticio") || strings.Contains(text, "Eva Ficticia") || !strings.Contains(text, "Cuota de noviembre") || !strings.Contains(text, "1 de 2 pagaron") {
		t.Fatalf("PDF privacy or totals: %q", text)
	}
	reversal := rf.do(http.MethodPost, "/api/finance/entries/"+march+"/reversal", "", reportIP, rf.officer, nil)
	if reversal.Code != http.StatusCreated {
		t.Fatalf("reversal: %d %s", reversal.Code, reversal.Body)
	}
	after := rf.get(acctPath+"/"+rep.ID+".pdf", student)
	if after.Code != http.StatusOK {
		t.Fatalf("PDF after reversal: %d %s", after.Code, after.Body)
	}
	afterText, err := converterText(after.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if afterText != text {
		t.Fatal("reversal changed rendered PDF text")
	}
}
