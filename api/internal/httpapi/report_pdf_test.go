package httpapi_test

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/httpapi"
)

func pdfRouter(f *fixture, url string) {
	f.h = httpapi.NewRouter(httpapi.Deps{Pool: tdb.App, Now: f.clk.Now, AccessCodeKey: testKey,
		TOTPKey: testTOTPKey, Mail: f.mail, BaseURL: baseURL, Background: func(fn func()) { fn() }, GotenbergURL: url})
}

func wordText(t *testing.T, data []byte) string {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range z.File {
		if f.Name != "word/document.xml" {
			continue
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		dec := xml.NewDecoder(r)
		var text strings.Builder
		inside := false
		for {
			tok, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			switch v := tok.(type) {
			case xml.StartElement:
				if v.Name.Local == "t" {
					inside = true
				}
			case xml.EndElement:
				if v.Name.Local == "t" {
					inside = false
					text.WriteByte('\n')
				}
			case xml.CharData:
				if inside {
					text.Write(v)
				}
			}
		}
		return text.String()
	}
	t.Fatal("missing document.xml")
	return ""
}

func TestFinanceReportPDFExample(t *testing.T) {
	ctx := integration(t)
	rf := newReportFixture(t, ctx, time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC))
	rf.record(t, "income/Ventas", "2026-02-28", 1000000, "Saldo anterior")
	rf.record(t, "income/Ventas", "2026-03-01", 200000, "Venta de tortas")
	rf.record(t, "expense/Alquileres", "2026-11-30", 1500000, "Alquiler del salon")
	documents := make(chan []byte, 1)
	converter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, _, err := r.FormFile("files")
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		document, err := io.ReadAll(file)
		if err != nil {
			t.Error(err)
			return
		}
		documents <- document
		_, _ = w.Write([]byte("%PDF-1.4\nfixture"))
	}))
	defer converter.Close()
	pdfRouter(rf.fixture, converter.URL)
	response := rf.get("/api/finance/report.pdf?from=2026-03-01&to=2026-11-30", rf.officer)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/pdf" || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Disposition") != `attachment; filename="informe-de-caja-2026-03-01-a-2026-11-30.pdf"` || !bytes.HasPrefix(response.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("response: %d %v %q", response.Code, response.Header(), response.Body.String())
	}
	text := wordText(t, <-documents)
	for _, want := range []string{"10.000,00", "Ventas", "2.000,00", "Alquileres", "15.000,00", "3.000,00", "Venta de tortas"} {
		if !strings.Contains(text, want) {
			t.Errorf("report missing %q: %s", want, text)
		}
	}
}

func TestFinanceReportPDFReadersAndPrivacy(t *testing.T) {
	p := newPayFixture(t)
	p.payOK(t, 100000)
	_, veedor, _, _ := p.signInMFA(t, p.ctx, "veedor")
	_, veedorNoMFA := p.signIn(t, p.ctx, "veedor")
	documents := make(chan []byte, 4)
	converter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, _, err := r.FormFile("files")
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		doc, err := io.ReadAll(file)
		if err != nil {
			t.Error(err)
			return
		}
		documents <- doc
		_, _ = w.Write([]byte("%PDF-1.4\nfixture"))
	}))
	defer converter.Close()
	pdfRouter(p.fixture, converter.URL)
	path := "/api/finance/report.pdf?from=2026-11-01&to=2026-11-30"
	for _, reader := range []struct {
		token     string
		seesPayer bool
	}{{p.beto, false}, {p.officer, true}, {p.adminMFA, true}, {veedor, true}} {
		r := p.do(http.MethodGet, path, "", reportIP, reader.token, nil)
		if r.Code != http.StatusOK {
			t.Fatalf("status %d: %s", r.Code, r.Body)
		}
		text := wordText(t, <-documents)
		if strings.Contains(text, "Beto Ficticio") != reader.seesPayer {
			t.Fatalf("payer visibility incorrect: %q", text)
		}
	}
	wantResponse(t, p.do(http.MethodGet, path, "", reportIP, veedorNoMFA, nil), 403, `{"error":"mfa_required"}`)
	wantResponse(t, p.do(http.MethodGet, path, "", reportIP, "", nil), 401, unauthBody)
	wantResponse(t, p.do(http.MethodGet, "/api/finance/report.pdf?from=2026-11-30&to=2026-11-01", "", reportIP, p.beto, nil), 422, `{"error":"invalid_range"}`)
}

func TestFinanceReportPDFUnavailableDoesNotWrite(t *testing.T) {
	ctx := integration(t)
	rf := newReportFixture(t, ctx, time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC))
	rf.record(t, "income/Ventas", "2026-03-15", 200000, "Private description")
	before := rf.ledgerState(t, rf.officer)
	converter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Private description", http.StatusServiceUnavailable)
	}))
	defer converter.Close()
	pdfRouter(rf.fixture, converter.URL)
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(old)
	r := rf.get("/api/finance/report.pdf?from=2026-01-01&to=2026-11-30", rf.officer)
	wantResponse(t, r, http.StatusServiceUnavailable, `{"error":"pdf_unavailable"}`)
	if strings.Contains(logs.String(), "Private description") || !strings.Contains(logs.String(), "503") {
		t.Fatalf("unsafe or missing technical log: %q", logs.String())
	}
	if after := rf.ledgerState(t, rf.officer); before != after {
		t.Fatal("unavailable conversion changed ledger or balance")
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer slow.Close()
	pdfRouter(rf.fixture, slow.URL)
	api := httptest.NewUnstartedServer(rf.h)
	api.Config.WriteTimeout = 15 * time.Second
	api.Start()
	defer api.Close()
	req, err := http.NewRequest(http.MethodGet, api.URL+"/api/finance/report.pdf?from=2026-01-01&to=2026-11-30", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: cookieName, Value: rf.officer})
	response, err := (&http.Client{Timeout: 45 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("HTTP response after converter timeout: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusServiceUnavailable || strings.TrimSpace(string(body)) != `{"error":"pdf_unavailable"}` {
		t.Fatalf("timeout response: %d %s", response.StatusCode, body)
	}
	if !strings.Contains(logs.String(), "converter timeout") || strings.Contains(logs.String(), "Private description") {
		t.Fatalf("unsafe or missing timeout log: %q", logs.String())
	}
	if after := rf.ledgerState(t, rf.officer); before != after {
		t.Fatal("timed out conversion changed ledger or balance")
	}
}

func TestFinanceReportPDFIsolation(t *testing.T) {
	ctx := integration(t)
	rf := newReportFixture(t, ctx, time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC))
	rf.record(t, "income/Ventas", "2026-03-15", 200000, "School A only")
	other := newTenant(t, ctx)
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, other); err != nil {
		t.Fatal(err)
	}
	var user string
	if err := tdb.Super.QueryRow(ctx, `SELECT recorded_by::text FROM finance_entries WHERE tenant_id=$1`, rf.tenant).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name)
		SELECT $1,id,'income','2026-03-20',987654,'School B only',$2,'Fictitious' FROM finance_categories WHERE tenant_id=$1 AND name='Ventas' AND direction='income'`, other, user); err != nil {
		t.Fatal(err)
	}
	documents := make(chan []byte, 1)
	converter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, _, err := r.FormFile("files")
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		doc, err := io.ReadAll(file)
		if err != nil {
			t.Error(err)
			return
		}
		documents <- doc
		_, _ = w.Write([]byte("%PDF-1.4\nfixture"))
	}))
	defer converter.Close()
	pdfRouter(rf.fixture, converter.URL)
	r := rf.get("/api/finance/report.pdf?from=2026-01-01&to=2026-11-30", rf.officer)
	if r.Code != http.StatusOK {
		t.Fatalf("status %d: %s", r.Code, r.Body)
	}
	text := wordText(t, <-documents)
	if !strings.Contains(text, "School A only") || strings.Contains(text, "School B only") || strings.Contains(text, "9.876,54") {
		t.Fatalf("cross-school report: %q", text)
	}
}

func TestFinanceReportPDF5000Entries(t *testing.T) {
	ctx := integration(t)
	rf := newReportFixture(t, ctx, time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC))
	rf.record(t, "income/Ventas", "2026-03-15", 100, "Movement 1")
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name)
		SELECT e.tenant_id,e.category_id,'income','2026-03-15',100,'Movement '||n,e.recorded_by,e.recorded_by_name
		FROM finance_entries e CROSS JOIN generate_series(2,5000) n WHERE e.tenant_id=$1`, rf.tenant); err != nil {
		t.Fatal(err)
	}
	documents := make(chan []byte, 1)
	converter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, _, err := r.FormFile("files")
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		doc, err := io.ReadAll(file)
		if err != nil {
			t.Error(err)
			return
		}
		documents <- doc
		_, _ = w.Write([]byte("%PDF-1.4\nfixture"))
	}))
	defer converter.Close()
	pdfRouter(rf.fixture, converter.URL)
	r := rf.get("/api/finance/report.pdf?from=2026-01-01&to=2026-11-30", rf.officer)
	if r.Code != http.StatusOK {
		t.Fatalf("status %d: %s", r.Code, r.Body)
	}
	if count := strings.Count(wordText(t, <-documents), "Movement "); count != 5000 {
		t.Fatalf("entries = %d, want 5000", count)
	}
}
