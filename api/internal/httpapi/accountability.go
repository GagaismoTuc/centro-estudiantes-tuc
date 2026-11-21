package httpapi

import (
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/finance"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
)

// argentinaTime is the zone the CSV shows publication times in.
var argentinaTime = time.FixedZone("Argentina", -3*60*60)

func writeAccountabilityError(w http.ResponseWriter, err error) {
	if errors.Is(err, finance.ErrTermRequired) {
		writeError(w, http.StatusUnprocessableEntity, "term_required")
		return
	}
	writeReportError(w, err)
}

func accountabilitySummary(r finance.AccountabilityReport) oapi.AccountabilityReportSummary {
	return oapi.AccountabilityReportSummary{Id: r.ID, From: r.From, To: r.To, Term: r.Term, PublishedBy: r.PublishedBy, PublishedAt: r.PublishedAt}
}

func accountabilityReport(r finance.AccountabilityReport) oapi.AccountabilityReport {
	c := r.Copy
	out := oapi.AccountabilityReport{Id: r.ID, From: r.From, To: r.To, Term: r.Term, PublishedBy: r.PublishedBy, PublishedAt: r.PublishedAt,
		OpeningCents: c.OpeningCents, ClosingCents: c.ClosingCents, Totals: make([]oapi.FinanceCategoryTotal, 0, len(c.Totals)),
		Dues: make([]oapi.AccountabilityDueTotal, 0, len(c.Dues)), Entries: make([]oapi.AccountabilityEntry, 0, len(c.Entries))}
	for _, t := range c.Totals {
		out.Totals = append(out.Totals, oapi.FinanceCategoryTotal{CategoryId: t.CategoryID, Name: t.Name, Direction: oapi.FinanceCategoryTotalDirection(t.Direction), TotalCents: t.TotalCents})
	}
	for _, d := range c.Dues {
		out.Dues = append(out.Dues, oapi.AccountabilityDueTotal{DueId: d.DueID, Name: d.Name, DueDate: d.DueDate, AmountCents: d.AmountCents, PaidCount: d.PaidCount, TotalCount: d.TotalCount})
	}
	for _, e := range c.Entries {
		entry := oapi.AccountabilityEntry{Id: e.ID, Date: e.Date, Direction: oapi.AccountabilityEntryDirection(e.Direction), CategoryId: e.CategoryID, Category: e.Category,
			Description: e.Description, AmountCents: e.AmountCents, RecordedBy: e.RecordedBy, Reversed: e.Reversed}
		if e.ReversesID != "" {
			entry.ReversesId = &e.ReversesID
		}
		out.Entries = append(out.Entries, entry)
	}
	return out
}

// GetFinanceAccountabilityReports lists the published Financial accountability reports.
func (s *server) GetFinanceAccountabilityReports(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	list, err := s.finance.AccountabilityReports(r.Context(), sess)
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	out := make([]oapi.AccountabilityReportSummary, 0, len(list))
	for _, rep := range list {
		out = append(out, accountabilitySummary(rep))
	}
	writeJSON(w, http.StatusOK, out)
}

// PostFinanceAccountabilityReports publishes a Financial accountability report. The Officer and
// two-factor checks run before the body is read.
func (s *server) PostFinanceAccountabilityReports(w http.ResponseWriter, r *http.Request) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	if err := s.finance.Authorize(r.Context(), sess); err != nil {
		writeFinanceError(w, err)
		return
	}
	var req oapi.NewAccountabilityReport
	if !decodeStrict(w, r, &req) {
		return
	}
	rep, err := s.finance.PublishAccountabilityReport(r.Context(), sess, finance.NewAccountabilityReport{From: req.From, To: req.To, Term: req.Term})
	if err != nil {
		writeAccountabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, accountabilityReport(rep))
}

// GetFinanceAccountabilityReport answers one report, or its CSV when the id ends in ".csv": Go's
// ServeMux cannot register the `{id}.csv` path of the contract, so it lands here.
func (s *server) GetFinanceAccountabilityReport(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	s.getAccountabilityReport(w, r, id, func(id string) (finance.AccountabilityReport, error) {
		return s.finance.AccountabilityReport(r.Context(), sess, id)
	})
}

func (s *server) getAccountabilityReport(w http.ResponseWriter, r *http.Request, id string, lookup func(string) (finance.AccountabilityReport, error)) {
	id, asPDF := strings.CutSuffix(id, ".pdf")
	var asCSV bool
	if !asPDF {
		id, asCSV = strings.CutSuffix(id, ".csv")
	}
	rep, err := lookup(id)
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	if asPDF {
		s.writeAccountabilityPDF(w, r, rep)
		return
	}
	if !asCSV {
		writeJSON(w, http.StatusOK, accountabilityReport(rep))
		return
	}
	c := rep.Copy
	names := make(map[string]string, len(c.Totals))
	entries := make([]finance.Entry, 0, len(c.Entries))
	for _, e := range c.Entries {
		names[e.CategoryID] = e.Category
		// writeLedgerCSV marks an Entry "revertido" when it has a ReversalID.
		reversal := ""
		if e.Reversed {
			reversal = "published"
		}
		entries = append(entries, finance.Entry{ID: e.ID, CategoryID: e.CategoryID, Direction: e.Direction, Date: e.Date, Description: e.Description,
			RecordedBy: e.RecordedBy, AmountCents: e.AmountCents, ReversesID: e.ReversesID, ReversalID: reversal})
	}
	writeCSV(w, "rendicion-de-cuentas-"+rep.From+"-a-"+rep.To+".csv", func(cw *csv.Writer) {
		_ = cw.Write([]string{"concepto", "valor"})
		_ = cw.Write([]string{"Gestión", csvText(rep.Term)})
		_ = cw.Write([]string{"Período", rep.From + " a " + rep.To})
		_ = cw.Write([]string{"Publicada por", csvText(rep.PublishedBy)})
		_ = cw.Write([]string{"Publicada el", rep.PublishedAt.In(argentinaTime).Format("2006-01-02 15:04")})
		_ = cw.Write([]string{})
		_ = cw.Write([]string{"concepto", "tipo", "monto"})
		_ = cw.Write([]string{"Saldo inicial", "", csvAmount(c.OpeningCents)})
		for _, t := range c.Totals {
			_ = cw.Write([]string{csvText(t.Name), csvKind(t.Direction), csvAmount(t.TotalCents)})
		}
		_ = cw.Write([]string{"Saldo final", "", csvAmount(c.ClosingCents)})
		_ = cw.Write([]string{})
		_ = cw.Write([]string{"cuota", "vencimiento", "pagaron", "de"})
		for _, d := range c.Dues {
			_ = cw.Write([]string{csvText(d.Name), d.DueDate, strconv.Itoa(d.PaidCount), strconv.Itoa(d.TotalCount)})
		}
		_ = cw.Write([]string{})
		// The copy never names who paid, so no reader gets the "alumno" column.
		writeLedgerCSV(cw, entries, names, false)
	})
}
