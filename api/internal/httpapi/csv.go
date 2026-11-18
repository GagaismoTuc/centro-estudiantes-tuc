package httpapi

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/finance"
)

// csvText escapes a text cell so a spreadsheet never runs it as a formula (CSV injection): a
// leading =, +, - or @ gets a ' in front. Every cell except a csvAmount goes through it.
func csvText(v string) string {
	if v != "" && strings.ContainsRune("=+-@", rune(v[0])) {
		return "'" + v
	}
	return v
}

// csvAmount writes cents for Excel in Spanish: decimal comma, no thousands separator. It is a
// number we generate, never a formula, so it is not escaped.
func csvAmount(cents int64) string {
	sign, abs := "", uint64(cents)
	if cents < 0 {
		sign, abs = "-", uint64(-cents)
	}
	return fmt.Sprintf("%s%d,%02d", sign, abs/100, abs%100)
}

func csvKind(direction string) string {
	if direction == "income" {
		return "ingreso"
	}
	return "gasto"
}

func csvYesNo(v bool) string {
	if v {
		return "sí"
	}
	return "no"
}

// writeLedgerCSV writes the ledger section. The "alumno" column exists only for readers who may see
// who paid a Due (ADR 0004); Students never get it.
// shortcut: "con comprobante" is always "no" until Receipts (#73), and "alumno" stays empty until
// Due payments (#78) link an Entry to a Roster member.
func writeLedgerCSV(cw *csv.Writer, entries []finance.Entry, categories map[string]string, seesPayers bool) {
	header := []string{"fecha", "tipo", "Categoría", "descripción", "monto", "registrado por", "revertido", "con comprobante"}
	if seesPayers {
		header = append(header, "alumno")
	}
	_ = cw.Write(header)
	for _, e := range entries {
		amount := e.AmountCents
		if e.Direction != "income" {
			amount = -amount
		}
		row := []string{csvText(e.Date), csvKind(e.Direction), csvText(categories[e.CategoryID]), csvText(e.Description), csvAmount(amount),
			csvText(e.RecordedBy), csvYesNo(e.ReversalID != ""), csvYesNo(false)}
		if seesPayers {
			row = append(row, "")
		}
		_ = cw.Write(row)
	}
}

// writeCSV sends a CSV attachment for Excel in Spanish: UTF-8 with BOM, `;` and CRLF. The body is
// built in memory so an error never leaves a half-written download.
func writeCSV(w http.ResponseWriter, filename string, fill func(*csv.Writer)) {
	var buf bytes.Buffer
	buf.WriteRune(0xFEFF) // UTF-8 BOM, so Excel reads accents
	cw := csv.NewWriter(&buf)
	cw.Comma = ';'
	cw.UseCRLF = true
	fill(cw)
	cw.Flush()
	if cw.Error() != nil {
		slog.Error("csv export failed")
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}
