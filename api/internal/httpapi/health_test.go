package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/httpapi"
)

func TestHealthReturnsOK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()

	httpapi.NewRouter(httpapi.Deps{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"status":"ok"}` {
		t.Fatalf("body = %s, want {\"status\":\"ok\"}", body)
	}
}

func TestUnknownRouteReturnsNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/does-not-exist", nil)
	rec := httptest.NewRecorder()

	httpapi.NewRouter(httpapi.Deps{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestRouterWithPDFConverterPreservesHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	httpapi.NewRouter(httpapi.Deps{GotenbergURL: "http://127.0.0.1:1"}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("health status=%d body=%s", rec.Code, rec.Body)
	}
}
