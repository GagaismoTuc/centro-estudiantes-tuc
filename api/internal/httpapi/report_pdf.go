package httpapi

import (
	_ "embed"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/docx"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/finance"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
)

//go:embed templates/report.docx
var reportTemplate []byte

// GetFinanceReportPdf downloads the on-demand report for the active school.
func (s *server) GetFinanceReportPdf(w http.ResponseWriter, r *http.Request, params oapi.GetFinanceReportPdfParams) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	// The server's normal 15-second write deadline is shorter than conversion's 30 seconds.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(2 * time.Minute))
	report, err := s.finance.Report(r.Context(), sess, queryValue(params.From), queryValue(params.To))
	if err != nil {
		writeReportError(w, err)
		return
	}
	fields, rows := reportFields(report)
	document, err := docx.Fill(reportTemplate, fields, rows)
	if err != nil {
		slog.Error("pdf generation failed", "cause", "template")
		writeError(w, http.StatusServiceUnavailable, "pdf_unavailable")
		return
	}
	pdf, err := s.pdf.Convert(r.Context(), document)
	if err != nil {
		slog.Error("pdf generation failed", "cause", err.Error())
		writeError(w, http.StatusServiceUnavailable, "pdf_unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="informe-de-caja-`+report.From+`-a-`+report.To+`.pdf"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdf)
}

func reportFields(report finance.Report) (map[string]string, map[string][]map[string]string) {
	var income, expenses int64
	names := make(map[string]string, len(report.Totals))
	totals := make([]map[string]string, 0, len(report.Totals))
	for _, total := range report.Totals {
		names[total.CategoryID] = total.Name
		if total.Direction == "income" {
			income += total.TotalCents
		} else {
			expenses += total.TotalCents
		}
		totals = append(totals, map[string]string{"categoria": total.Name, "tipo": csvKind(total.Direction), "monto": pdfAmount(total.TotalCents)})
	}
	entries := make([]map[string]string, 0, len(report.Entries))
	for _, entry := range report.Entries {
		amount := entry.AmountCents
		if entry.Direction != "income" {
			amount = -amount
		}
		entries = append(entries, map[string]string{"fecha": entry.Date, "categoria": names[entry.CategoryID], "descripcion": entry.Description,
			"monto": pdfAmount(amount), "registrado_por": entry.RecordedBy, "alumno": entry.RosterMemberName})
	}
	return map[string]string{"desde": report.From, "hasta": report.To, "saldo_inicial": pdfAmount(report.OpeningCents),
			"ingresos": pdfAmount(income), "gastos": pdfAmount(expenses), "saldo_final": pdfAmount(report.ClosingCents)},
		map[string][]map[string]string{"totales": totals, "movimientos": entries}
}

func pdfAmount(cents int64) string {
	sign := ""
	var abs uint64
	if cents < 0 {
		sign = "-"
		abs = uint64(-(cents + 1)) + 1
	} else {
		abs = uint64(cents)
	}
	whole := strconv.FormatUint(abs/100, 10)
	var grouped strings.Builder
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			grouped.WriteByte('.')
		}
		grouped.WriteRune(c)
	}
	return fmt.Sprintf("$ %s%s,%02d", sign, grouped.String(), abs%100)
}
