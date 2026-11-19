package finance_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/finance"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/testdb"
)

// TestPublishReadsOneSnapshot covers R6 of #83: a Due payment committed while a report is being
// published is wholly out of the copy (Entries, balance and Due totals), never half in.
func TestPublishReadsOneSnapshot(t *testing.T) {
	ctx := context.Background()
	env, stop, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	store := finance.NewStore(env.App, func() time.Time { return now })
	school := newReportSchool(t, ctx, env, "Escuela Rendición", "r6@example.invalid")
	var beto string
	if err := env.Super.QueryRow(ctx, `INSERT INTO roster_members(tenant_id,full_name,document_number) VALUES ($1,'Beto Ficticio','30111401') RETURNING id::text`, school.sess.TenantID).Scan(&beto); err != nil {
		t.Fatal(err)
	}
	due, err := store.CreateDue(ctx, school.sess, finance.NewDue{Name: "Cuota de noviembre", AmountCents: 100000, DueDate: "2026-11-15"})
	if err != nil {
		t.Fatal(err)
	}
	school.record(t, ctx, store, "Ventas", "2026-11-02", 50000)

	var paid finance.DuePayment
	finance.SetAfterReportRead(store, func() {
		var e error
		if paid, e = store.RecordDuePayment(ctx, school.sess, due.ID, finance.NewDuePayment{RosterMemberID: beto, Date: "2026-11-10", AmountCents: 100000}); e != nil {
			t.Errorf("payment during publication: %v", e)
		}
	})
	rep, err := store.PublishAccountabilityReport(ctx, school.sess, finance.NewAccountabilityReport{From: "2026-11-01", To: "2026-11-19", Term: "Gestión 2026"})
	finance.SetAfterReportRead(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if paid.EntryID == "" {
		t.Fatal("the payment during publication was not committed")
	}
	for _, e := range rep.Copy.Entries {
		if e.ID == paid.EntryID || strings.HasPrefix(e.Description, "Pago de cuota") {
			t.Fatalf("payment Entry is in the copy: %+v", e)
		}
	}
	if len(rep.Copy.Entries) != 1 || rep.Copy.ClosingCents != 50000 {
		t.Fatalf("entries=%d closing=%d, want 1 and 50000", len(rep.Copy.Entries), rep.Copy.ClosingCents)
	}
	if len(rep.Copy.Dues) != 1 || rep.Copy.Dues[0].PaidCount != 0 || rep.Copy.Dues[0].TotalCount != 2 {
		t.Fatalf("dues=%+v, want 0 of 2 paid", rep.Copy.Dues)
	}

	// Published after the payment, a new report has it wholly in.
	after, err := store.PublishAccountabilityReport(ctx, school.sess, finance.NewAccountabilityReport{From: "2026-11-01", To: "2026-11-19", Term: "Gestión 2026"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range after.Copy.Entries {
		found = found || e.ID == paid.EntryID
	}
	if !found || after.Copy.ClosingCents != 150000 || after.Copy.Dues[0].PaidCount != 1 {
		t.Fatalf("after: payment in=%v closing=%d dues=%+v", found, after.Copy.ClosingCents, after.Copy.Dues)
	}
}
