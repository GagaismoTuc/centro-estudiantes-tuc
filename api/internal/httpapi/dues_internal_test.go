package httpapi

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/finance"
)

func TestWriteDueErrorMappingsAndSanitizedLogs(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	rec := httptest.NewRecorder()
	writeDueError(rec, finance.ErrDueFrozen)
	if rec.Code != http.StatusConflict || strings.TrimSpace(rec.Body.String()) != `{"error":"due_frozen"}` {
		t.Fatalf("due_frozen: code=%d body=%s", rec.Code, rec.Body)
	}

	rec500 := httptest.NewRecorder()
	writeDueError(rec500, errors.New("pq: duplicate Ana Ficticia 30111222"))
	if rec500.Code != http.StatusInternalServerError || strings.TrimSpace(rec500.Body.String()) != `{"error":"internal"}` {
		t.Fatalf("internal: code=%d body=%s", rec500.Code, rec500.Body)
	}
	if logs := buf.String(); strings.Contains(logs, "Ana Ficticia") || strings.Contains(logs, "30111222") {
		t.Fatalf("500 log leaked personal data: %s", logs)
	}
}
