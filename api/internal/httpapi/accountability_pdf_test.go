package httpapi_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func accountabilityConverter(t *testing.T) (*httptest.Server, chan []byte) {
	t.Helper()
	docs := make(chan []byte, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, _, err := r.FormFile("files")
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			t.Error(err)
			return
		}
		docs <- data
		_, _ = w.Write([]byte("%PDF-1.4\nfixture"))
	}))
	t.Cleanup(srv.Close)
	return srv, docs
}

func TestAccountabilityPDFFrozen(t *testing.T) {
	rf := newAcctFixture(t)
	rf.renameOfficer(t, "Ana Ficticia")
	march := rf.record(t, "income/Ventas", "2026-03-01", 50000, "Kiosco de marzo")
	rep, _ := rf.publishOK(t, rf.officer, "2026-03-01", "2026-11-30", "Gesti\u00f3n 2026")
	converter, docs := accountabilityConverter(t)
	pdfRouter(rf.fixture, converter.URL)
	path := acctPath + "/" + rep.ID + ".pdf"
	first := rf.get(path, rf.officer)
	if first.Code != 200 || first.Header().Get("Content-Type") != "application/pdf" || first.Header().Get("Content-Disposition") != `attachment; filename="rendicion-de-cuentas-gestion-2026-2026-03-01-a-2026-11-30.pdf"` || !bytes.HasPrefix(first.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("PDF: %d %v %s", first.Code, first.Header(), first.Body)
	}
	before := wordText(t, <-docs)
	if !strings.Contains(before, "Publicada por Ana Ficticia") || !strings.Contains(before, "Kiosco de marzo") {
		t.Fatalf("missing frozen data: %s", before)
	}
	if r := rf.do(http.MethodPost, "/api/finance/entries/"+march+"/reversal", "", reportIP, rf.officer, nil); r.Code != 201 {
		t.Fatalf("reversal %d %s", r.Code, r.Body)
	}
	second := rf.get(path, rf.officer)
	if second.Code != 200 || wordText(t, <-docs) != before {
		t.Fatal("reversal changed frozen PDF document")
	}
}

func TestAccountabilityPDFReaders(t *testing.T) {
	rf := newAcctFixture(t)
	rep, _ := rf.publishOK(t, rf.officer, "2026-03-01", "2026-11-30", "Gesti\u00f3n 2026")
	_, student, _ := rf.student(t, rf.ctx, "Beto Ficticio", "30111223")
	_, admin, _, _ := rf.signInMFA(t, rf.ctx, "school_admin")
	_, veedor, _, _ := rf.signInMFA(t, rf.ctx, "veedor")
	converter, docs := accountabilityConverter(t)
	pdfRouter(rf.fixture, converter.URL)
	path := acctPath + "/" + rep.ID + ".pdf"
	for _, token := range []string{rf.officer, student, admin, veedor} {
		if r := rf.get(path, token); r.Code != 200 {
			t.Fatalf("reader %d %s", r.Code, r.Body)
		}
		<-docs
	}
	wantResponse(t, rf.get(path, ""), 401, `{"error":"unauthenticated"}`)
	wantResponse(t, rf.get(acctPath+"/00000000-0000-4000-8000-000000000000.pdf", rf.officer), 404, `{"error":"not_found"}`)
	other := newReportFixture(t, rf.ctx, acctNow)
	pdfRouter(other.fixture, converter.URL)
	wantResponse(t, other.get(path, other.officer), 404, `{"error":"not_found"}`)
}

func TestAccountabilityPDFUnavailable(t *testing.T) {
	rf := newAcctFixture(t)
	rep, _ := rf.publishOK(t, rf.officer, "2026-03-01", "2026-11-30", "Gesti\u00f3n 2026")
	before := rf.acctState(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unavailable", 503) }))
	defer server.Close()
	pdfRouter(rf.fixture, server.URL)
	wantResponse(t, rf.get(acctPath+"/"+rep.ID+".pdf", rf.officer), 503, `{"error":"pdf_unavailable"}`)
	if rf.acctState(t) != before {
		t.Fatal("failed conversion changed report")
	}
}

func TestAccountabilityPDFWithoutDues(t *testing.T) {
	rf := newAcctFixture(t)
	rep, _ := rf.publishOK(t, rf.officer, "2026-03-01", "2026-11-30", "Gesti\u00f3n 2026")
	server, docs := accountabilityConverter(t)
	pdfRouter(rf.fixture, server.URL)
	if r := rf.get(acctPath+"/"+rep.ID+".pdf", rf.officer); r.Code != 200 {
		t.Fatalf("status %d %s", r.Code, r.Body)
	}
	text := wordText(t, <-docs)
	if !strings.Contains(text, "Sin cuotas en el per\u00edodo") || strings.Contains(text, "{{") {
		t.Fatalf("empty dues: %s", text)
	}
}

func TestAccountabilityPDFRejectsDoubleSuffix(t *testing.T) {
	rf := newAcctFixture(t)
	rep, _ := rf.publishOK(t, rf.officer, "2026-03-01", "2026-11-30", "Gestion 2026")
	srv, _ := accountabilityConverter(t)
	pdfRouter(rf.fixture, srv.URL)
	wantResponse(t, rf.get(acctPath+"/"+rep.ID+".csv.pdf", rf.officer), http.StatusNotFound, `{"error":"not_found"}`)
}
