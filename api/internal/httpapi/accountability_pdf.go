package httpapi

import (
	_ "embed"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/docx"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/finance"
)

//go:embed templates/accountability.docx
var accountabilityTemplate []byte

func (s *server) writeAccountabilityPDF(w http.ResponseWriter, r *http.Request, rep finance.AccountabilityReport) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(2 * time.Minute))
	totals := make([]map[string]string, 0, len(rep.Copy.Totals))
	for _, t := range rep.Copy.Totals {
		totals = append(totals, map[string]string{"categoria": t.Name, "tipo": csvKind(t.Direction), "monto": pdfAmount(t.TotalCents)})
	}
	dues := make([]map[string]string, 0, len(rep.Copy.Dues))
	for _, d := range rep.Copy.Dues {
		dues = append(dues, map[string]string{"nombre": d.Name, "pagaron": strconv.Itoa(d.PaidCount), "total": strconv.Itoa(d.TotalCount)})
	}
	entries := make([]map[string]string, 0, len(rep.Copy.Entries))
	for _, e := range rep.Copy.Entries {
		amount := e.AmountCents
		if e.Direction != "income" {
			amount = -amount
		}
		entries = append(entries, map[string]string{"fecha": e.Date, "categoria": e.Category, "descripcion": e.Description, "monto": pdfAmount(amount), "registrado_por": e.RecordedBy})
	}
	empty := ""
	if len(dues) == 0 {
		empty = "Sin cuotas en el per\u00edodo"
	}
	fields := map[string]string{"gestion": rep.Term, "desde": rep.From, "hasta": rep.To, "publicada_por": rep.PublishedBy, "saldo_inicial": pdfAmount(rep.Copy.OpeningCents), "saldo_final": pdfAmount(rep.Copy.ClosingCents), "sin_cuotas": empty}
	document, err := docx.Fill(accountabilityTemplate, fields, map[string][]map[string]string{"totales": totals, "cuotas": dues, "movimientos": entries})
	if err != nil {
		slog.Error("pdf generation failed", "cause", "template")
		writeError(w, 503, "pdf_unavailable")
		return
	}
	pdf, err := s.pdf.Convert(r.Context(), document)
	if err != nil {
		slog.Error("pdf generation failed", "cause", err.Error())
		writeError(w, 503, "pdf_unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="rendicion-de-cuentas-%s-%s-a-%s.pdf"`, accountabilitySlug(rep.Term), rep.From, rep.To))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdf)
}

func accountabilitySlug(term string) string {
	var b strings.Builder
	sep := false
	for _, c := range strings.ToLower(term) {
		switch c {
		case '\u00e1', '\u00e0', '\u00e4':
			c = 'a'
		case '\u00e9', '\u00e8', '\u00eb':
			c = 'e'
		case '\u00ed', '\u00ec', '\u00ef':
			c = 'i'
		case '\u00f3', '\u00f2', '\u00f6':
			c = 'o'
		case '\u00fa', '\u00f9', '\u00fc':
			c = 'u'
		case '\u00f1':
			c = 'n'
		}
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			if sep && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(c)
			sep = false
		} else if unicode.IsSpace(c) || c == '-' || c == '_' {
			sep = true
		}
	}
	if b.Len() == 0 {
		return "gestion"
	}
	return b.String()
}
