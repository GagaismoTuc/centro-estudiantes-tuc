package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/finance"
)

func TestAccountabilityHandlerRejectsDoubleSuffixWithFake(t *testing.T) {
	var got string
	lookup := func(id string) (finance.AccountabilityReport, error) {
		got = id
		return finance.AccountabilityReport{}, finance.ErrNotFound
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/finance/accountability-reports/report.csv.pdf", nil)
	(&server{}).getAccountabilityReport(w, r, "report.csv.pdf", lookup)
	if got != "report.csv" || w.Code != http.StatusNotFound || w.Body.String() != "{\"error\":\"not_found\"}\n" {
		t.Fatalf("lookup %q, response %d %q", got, w.Code, w.Body.String())
	}
}
