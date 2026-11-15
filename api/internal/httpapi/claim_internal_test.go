package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/roster"
)

func TestWriteClaimMeLogsLookupFailure(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	s := server{store: auth.NewStore(nil, time.Now)}
	w := httptest.NewRecorder()
	s.writeClaimMe(w, httptest.NewRequest(http.MethodPost, "/api/auth/claim", nil), roster.Claimed{Token: "invalid-secret"}, http.StatusCreated)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), `"internal"`) {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(logs.String(), "claim session lookup") || strings.Contains(logs.String(), "invalid-secret") {
		t.Fatalf("unexpected log: %s", logs.String())
	}
}
