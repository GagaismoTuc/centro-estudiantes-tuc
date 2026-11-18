package finance_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/finance"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/testdb"
)

func TestStoreExemptions(t *testing.T) {
	ctx := context.Background()
	env, stop, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	seed := func(dst *string, q string, args ...any) {
		t.Helper()
		if err := env.Super.QueryRow(ctx, q, args...).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := env.Super.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	var tenant, otherTenant string
	seed(&tenant, `INSERT INTO organizations(kind,name) VALUES ('school','Escuela Demo') RETURNING id::text`)
	seed(&otherTenant, `INSERT INTO organizations(kind,name) VALUES ('school','Otra Escuela') RETURNING id::text`)
	var anaUser, betoUser, carlaUser, adminUser, otherUser string
	for _, u := range []struct {
		email string
		dst   *string
	}{
		{"ana@example.invalid", &anaUser},
		{"beto@example.invalid", &betoUser},
		{"carla@example.invalid", &carlaUser},
		{"admin@example.invalid", &adminUser},
		{"otra@example.invalid", &otherUser},
	} {
		seed(u.dst, `INSERT INTO users(email,password_hash) VALUES ($1,'x') RETURNING id::text`, u.email)
	}
	var ana, beto, carla, dani, otherMember string
	seed(&ana, `INSERT INTO roster_members(tenant_id,full_name,document_number) VALUES ($1,'Ana Ficticia','30111222') RETURNING id::text`, tenant)
	seed(&beto, `INSERT INTO roster_members(tenant_id,full_name,document_number) VALUES ($1,'Beto Ficticio','30111223') RETURNING id::text`, tenant)
	seed(&carla, `INSERT INTO roster_members(tenant_id,full_name,document_number) VALUES ($1,'Carla Ficticia','30111224') RETURNING id::text`, tenant)
	seed(&otherMember, `INSERT INTO roster_members(tenant_id,full_name,document_number) VALUES ($1,'Eva Ficticia','30111226') RETURNING id::text`, otherTenant)
	exec(`INSERT INTO memberships(tenant_id,user_id,role,roster_member_id) VALUES ($1,$2,'officer',$3)`, tenant, anaUser, ana)
	exec(`INSERT INTO memberships(tenant_id,user_id,role,roster_member_id) VALUES ($1,$2,'student',$3)`, tenant, betoUser, beto)
	exec(`INSERT INTO memberships(tenant_id,user_id,role,roster_member_id) VALUES ($1,$2,'student',$3)`, tenant, carlaUser, carla)
	exec(`INSERT INTO memberships(tenant_id,user_id,role) VALUES ($1,$2,'school_admin')`, tenant, adminUser)
	exec(`INSERT INTO memberships(tenant_id,user_id,role,roster_member_id) VALUES ($1,$2,'officer',$3)`, otherTenant, otherUser, otherMember)

	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	store := finance.NewStore(env.App, func() time.Time { return now })
	anaSess := auth.Session{TenantID: tenant, UserID: anaUser, MFAVerified: true}
	betoSess := auth.Session{TenantID: tenant, UserID: betoUser, MFAVerified: true}
	carlaSess := auth.Session{TenantID: tenant, UserID: carlaUser, MFAVerified: true}
	adminSess := auth.Session{TenantID: tenant, UserID: adminUser, MFAVerified: true}
	otherSess := auth.Session{TenantID: otherTenant, UserID: otherUser, MFAVerified: true}

	newDue := func(sess auth.Session, name string) finance.DueDetail {
		t.Helper()
		d, err := store.CreateDue(ctx, sess, finance.NewDue{Name: name, AmountCents: 200000, DueDate: "2026-11-30"})
		if err != nil {
			t.Fatalf("create due %q: %v", name, err)
		}
		return d
	}
	due := newDue(anaSess, "Cuota de noviembre")
	raceDue := newDue(anaSess, "Cuota de diciembre")
	plainDue := newDue(anaSess, "Cuota de octubre")
	otherDue := newDue(otherSess, "Cuota ajena")
	// Dani joins the Roster after the Dues were created, so Dani does not owe them.
	seed(&dani, `INSERT INTO roster_members(tenant_id,full_name,document_number) VALUES ($1,'Dani Ficticio','30111225') RETURNING id::text`, tenant)

	countExemptions := func() int {
		t.Helper()
		var n int
		if err := env.Super.QueryRow(ctx, `SELECT count(*) FROM finance_due_exemptions`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	expectRejected := func(name string, want error, sess auth.Session, dueID, member, reason string) {
		t.Helper()
		before := countExemptions()
		_, err := store.CreateExemption(ctx, sess, dueID, member, reason)
		if !errors.Is(err, want) {
			t.Fatalf("%s: got %v, want %v", name, err, want)
		}
		if r := strings.TrimSpace(reason); r != "" && strings.Contains(err.Error(), r) {
			t.Fatalf("%s: error message carries the reason", name)
		}
		if after := countExemptions(); after != before {
			t.Fatalf("%s: exemptions %d -> %d, want nothing written", name, before, after)
		}
	}

	// S7: only an Officer with verified MFA writes an Exemption.
	expectRejected("student", finance.ErrForbidden, carlaSess, due.ID, beto, "Beca")
	expectRejected("school admin", finance.ErrForbidden, adminSess, due.ID, beto, "Beca")
	expectRejected("no session", finance.ErrForbidden, auth.Session{}, due.ID, beto, "Beca")
	expectRejected("officer without MFA", auth.ErrMFARequired, auth.Session{TenantID: tenant, UserID: anaUser}, due.ID, beto, "Beca")

	// S2: the reason is required and at most 500 characters.
	expectRejected("empty reason", finance.ErrReasonRequired, anaSess, due.ID, beto, "")
	expectRejected("blank reason", finance.ErrReasonRequired, anaSess, due.ID, beto, "   ")
	expectRejected("whitespace reason", finance.ErrReasonRequired, anaSess, due.ID, beto, "\t\n ")
	expectRejected("reason too long", finance.ErrReasonTooLong, anaSess, due.ID, beto, strings.Repeat("á", 501))

	// S8: ids of another school or unknown ids are not found; a member outside the snapshot is not owed.
	expectRejected("other tenant due", finance.ErrNotFound, anaSess, otherDue.ID, beto, "Beca")
	expectRejected("other tenant member", finance.ErrNotFound, anaSess, due.ID, otherMember, "Beca")
	expectRejected("unknown due", finance.ErrNotFound, anaSess, "00000000-0000-0000-0000-000000000000", beto, "Beca")
	expectRejected("malformed due", finance.ErrNotFound, anaSess, "nope", beto, "Beca")
	expectRejected("malformed member", finance.ErrNotFound, anaSess, due.ID, "nope", "Beca")
	expectRejected("not owed", finance.ErrNotOwed, anaSess, due.ID, dani, "Beca")

	// S1: Ana exempts Beto from "Cuota de noviembre" with reason "Beca"; Beto shows as exempt.
	detail, err := store.CreateExemption(ctx, anaSess, due.ID, beto, "  Beca  ")
	if err != nil {
		t.Fatalf("create exemption: %v", err)
	}
	var reason, createdBy string
	if err := env.Super.QueryRow(ctx, `SELECT reason, created_by::text FROM finance_due_exemptions WHERE tenant_id=$1 AND due_id=$2 AND roster_member_id=$3`, tenant, due.ID, beto).Scan(&reason, &createdBy); err != nil {
		t.Fatalf("stored exemption: %v", err)
	}
	if reason != "Beca" || createdBy != anaUser {
		t.Fatalf("stored reason=%q created_by=%s, want trimmed Beca by Ana", reason, createdBy)
	}

	// S4: Beto leaves the denominator: 0 of 2 paid, Ana and Carla owe.
	checkDetail := func(name string, d finance.DueDetail) {
		t.Helper()
		if d.TotalCount != 2 || d.OwingCount != 2 || len(d.Members) != 3 {
			t.Fatalf("%s counts: total=%d owing=%d members=%d, want 2/2/3", name, d.TotalCount, d.OwingCount, len(d.Members))
		}
		for _, m := range d.Members {
			wantStatus, wantReason := finance.DueStatusOwing, ""
			if m.RosterMemberID == beto {
				wantStatus, wantReason = finance.DueStatusExempt, "Beca"
			}
			if m.Status != wantStatus || m.ExemptionReason != wantReason {
				t.Fatalf("%s member %s: status=%q reason=%q, want %q/%q", name, m.FullName, m.Status, m.ExemptionReason, wantStatus, wantReason)
			}
		}
	}
	checkDetail("create response", detail)
	for _, sess := range []auth.Session{anaSess, adminSess} {
		got, err := store.GetDue(ctx, sess, due.ID)
		if err != nil {
			t.Fatalf("get due: %v", err)
		}
		checkDetail("get due", got)
	}
	list, err := store.ListDues(ctx, anaSess)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range list {
		if d.ID == due.ID && (d.TotalCount != 2 || d.OwingCount != 2) {
			t.Fatalf("list counts total=%d owing=%d, want 2/2", d.TotalCount, d.OwingCount)
		}
	}

	// S6: the exempt Student sees only "exempt"; other Students see nothing of it.
	betoMine, err := store.ListMyDues(ctx, betoSess)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range betoMine {
		want := finance.DueStatusOwing
		if d.ID == due.ID {
			want = finance.DueStatusExempt
		}
		if d.Status != want {
			t.Fatalf("beto mine %s: status=%q, want %q", d.Name, d.Status, want)
		}
	}
	carlaMine, err := store.ListMyDues(ctx, carlaSess)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range carlaMine {
		if d.Status != finance.DueStatusOwing {
			t.Fatalf("carla mine %s: status=%q, want owing", d.Name, d.Status)
		}
	}
	if _, err := store.GetDue(ctx, carlaSess, due.ID); !errors.Is(err, finance.ErrForbidden) {
		t.Fatalf("student GetDue after exemption: got %v, want ErrForbidden", err)
	}

	// S3: exempting the same Student again is rejected.
	expectRejected("already exempt", finance.ErrAlreadyExempt, anaSess, due.ID, beto, "Otra beca")

	// S5: an Exemption freezes amount, due date and deletion; nothing changes.
	snapshot := func() (int64, string, int) {
		t.Helper()
		var amount int64
		var date string
		var members int
		if err := env.Super.QueryRow(ctx, `SELECT d.amount_cents, to_char(d.due_date,'YYYY-MM-DD'), (SELECT count(*) FROM finance_due_members m WHERE m.tenant_id=d.tenant_id AND m.due_id=d.id)::int FROM finance_dues d WHERE d.id=$1`, due.ID).Scan(&amount, &date, &members); err != nil {
			t.Fatalf("due row: %v", err)
		}
		return amount, date, members
	}
	newAmount := int64(250000)
	newDate := "2026-12-15"
	for _, tc := range []struct {
		name string
		in   finance.UpdateDue
	}{
		{"amount", finance.UpdateDue{AmountCents: &newAmount}},
		{"due date", finance.UpdateDue{DueDate: &newDate}},
	} {
		if _, err := store.UpdateDue(ctx, anaSess, due.ID, tc.in); !errors.Is(err, finance.ErrDueFrozen) {
			t.Fatalf("frozen update %s: got %v, want ErrDueFrozen", tc.name, err)
		}
		if amount, date, members := snapshot(); amount != 200000 || date != "2026-11-30" || members != 3 {
			t.Fatalf("frozen update %s changed the due: %d %s %d", tc.name, amount, date, members)
		}
	}
	if err := store.DeleteDue(ctx, anaSess, due.ID); !errors.Is(err, finance.ErrDueFrozen) {
		t.Fatalf("frozen delete: got %v, want ErrDueFrozen", err)
	}
	if amount, date, members := snapshot(); amount != 200000 || date != "2026-11-30" || members != 3 || countExemptions() != 1 {
		t.Fatalf("frozen delete changed the due: %d %s %d", amount, date, members)
	}
	// Renaming stays allowed on a frozen Due.
	newName := "Cuota de noviembre 2026"
	if renamed, err := store.UpdateDue(ctx, anaSess, due.ID, finance.UpdateDue{Name: &newName}); err != nil || renamed.Name != newName {
		t.Fatalf("rename frozen due: %+v %v", renamed, err)
	}

	// S10/S3: two simultaneous requests for the same Student: one succeeds, the other is already exempt.
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = store.CreateExemption(ctx, anaSess, raceDue.ID, carla, "Beca")
		}(i)
	}
	close(start)
	wg.Wait()
	ok, already := 0, 0
	for _, e := range errs {
		switch {
		case e == nil:
			ok++
		case errors.Is(e, finance.ErrAlreadyExempt):
			already++
		default:
			t.Fatalf("race: unexpected error %v", e)
		}
	}
	var raceRows int
	if err := env.Super.QueryRow(ctx, `SELECT count(*) FROM finance_due_exemptions WHERE due_id=$1 AND roster_member_id=$2`, raceDue.ID, carla).Scan(&raceRows); err != nil {
		t.Fatal(err)
	}
	if ok != 1 || already != 1 || raceRows != 1 {
		t.Fatalf("race: ok=%d already=%d rows=%d, want 1/1/1", ok, already, raceRows)
	}

	// A Due without Exemptions still updates and deletes as before.
	if _, err := store.UpdateDue(ctx, anaSess, plainDue.ID, finance.UpdateDue{AmountCents: &newAmount}); err != nil {
		t.Fatalf("update unfrozen due: %v", err)
	}
	if err := store.DeleteDue(ctx, anaSess, plainDue.ID); err != nil {
		t.Fatalf("delete unfrozen due: %v", err)
	}
}
