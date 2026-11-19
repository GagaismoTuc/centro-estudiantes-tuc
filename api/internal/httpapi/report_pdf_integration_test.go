package httpapi_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
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
		ContainerRequest: testcontainers.ContainerRequest{Image: image, Env: map[string]string{"API_TIMEOUT": "60s"}, ExposedPorts: []string{"3000/tcp"}, WaitingFor: wait.ForHTTP("/health").WithPort("3000/tcp").WithStartupTimeout(2 * time.Minute)}})
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
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, other); err != nil { t.Fatal(err) }
	var user string
	if err := tdb.Super.QueryRow(ctx, `SELECT recorded_by::text FROM finance_entries WHERE tenant_id=$1 LIMIT 1`, rf.tenant).Scan(&user); err != nil { t.Fatal(err) }
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name)
		SELECT $1,id,'income','2026-03-20',987654,'Other school only',$2,'Fictitious' FROM finance_categories WHERE tenant_id=$1 AND name='Ventas' AND direction='income'`, other, user); err != nil { t.Fatal(err) }
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
	if strings.Contains(text, "Other school only") || strings.Contains(text, "9.876,54") { t.Fatalf("PDF contains another school's data: %q", text) }
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
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	large := rf.get(path, rf.officer)
	slog.SetDefault(old)
	if large.Code == http.StatusServiceUnavailable {
		if !strings.Contains(logs.String(), "converter timeout") {
			t.Fatalf("large report failed for a reason other than timeout: %s %s", large.Body, logs.String())
		}
	} else if large.Code == http.StatusOK {
		text, err := converterText(large.Body.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "Large entry 4998") {
			t.Fatal("large PDF omitted the last entry")
		}
	} else {
		t.Fatalf("large report: %d %s", large.Code, large.Body)
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
