package httpapi

import (
	"encoding/csv"
	"errors"
	"net/http"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/finance"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
)

func queryValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func writeReportError(w http.ResponseWriter, err error) {
	if errors.Is(err, finance.ErrInvalidRange) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_range")
		return
	}
	writeFinanceError(w, err)
}

// GetFinanceReport answers the Fund report (Informe de caja) for any member of the school.
func (s *server) GetFinanceReport(w http.ResponseWriter, r *http.Request, params oapi.GetFinanceReportParams) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	rep, err := s.finance.Report(r.Context(), sess, queryValue(params.From), queryValue(params.To))
	if err != nil {
		writeReportError(w, err)
		return
	}
	out := oapi.FinanceReport{From: rep.From, To: rep.To, OpeningCents: rep.OpeningCents, ClosingCents: rep.ClosingCents,
		Totals: make([]oapi.FinanceCategoryTotal, 0, len(rep.Totals)), Entries: make([]oapi.FinanceEntry, 0, len(rep.Entries))}
	for _, c := range rep.Totals {
		out.Totals = append(out.Totals, oapi.FinanceCategoryTotal{CategoryId: c.CategoryID, Name: c.Name, Direction: oapi.FinanceCategoryTotalDirection(c.Direction), TotalCents: c.TotalCents})
		if c.Direction == "income" {
			out.IncomeCents += c.TotalCents
		} else {
			out.ExpenseCents += c.TotalCents
		}
	}
	for _, e := range rep.Entries {
		out.Entries = append(out.Entries, financeEntry(e))
	}
	writeJSON(w, http.StatusOK, out)
}

// GetFinanceReportCsv downloads the Fund report: the summary, an empty line and the range's ledger.
func (s *server) GetFinanceReportCsv(w http.ResponseWriter, r *http.Request, params oapi.GetFinanceReportCsvParams) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	rep, err := s.finance.Report(r.Context(), sess, queryValue(params.From), queryValue(params.To))
	if err != nil {
		writeReportError(w, err)
		return
	}
	// Every Entry in the range has its Category among the Totals.
	names := make(map[string]string, len(rep.Totals))
	for _, c := range rep.Totals {
		names[c.CategoryID] = c.Name
	}
	writeCSV(w, "informe-de-caja-"+rep.From+"-a-"+rep.To+".csv", func(cw *csv.Writer) {
		_ = cw.Write([]string{"concepto", "tipo", "monto"})
		_ = cw.Write([]string{"Saldo inicial", "", csvAmount(rep.OpeningCents)})
		for _, c := range rep.Totals {
			_ = cw.Write([]string{csvText(c.Name), csvKind(c.Direction), csvAmount(c.TotalCents)})
		}
		_ = cw.Write([]string{"Saldo final", "", csvAmount(rep.ClosingCents)})
		_ = cw.Write([]string{})
		writeLedgerCSV(cw, rep.Entries, names, rep.SeesPayers)
	})
}

// GetFinanceEntriesCsv downloads the ledger, whole or for a range.
func (s *server) GetFinanceEntriesCsv(w http.ResponseWriter, r *http.Request, params oapi.GetFinanceEntriesCsvParams) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	from, to := queryValue(params.From), queryValue(params.To)
	ledger, err := s.finance.Ledger(r.Context(), sess, from, to)
	if err != nil {
		writeReportError(w, err)
		return
	}
	filename := "libro-de-caja.csv"
	if from != "" || to != "" {
		filename = "libro-de-caja-" + from + "-a-" + to + ".csv"
	}
	writeCSV(w, filename, func(cw *csv.Writer) {
		writeLedgerCSV(cw, ledger.Entries, ledger.Categories, ledger.SeesPayers)
	})
}
