package elections_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/elections"
)

type listSchool struct {
	school
	dana, eli, fito  string
	gabi, hugo, iris string
	betoUser         string
	danaUser         string
	commUser         string
	commRoster       string
	adminUser        string
	electionID       string
}

// setupListSchool creates a school with:
// - commRoster/commUser on the Electoral commission (MFA verified)
// - ana/anaUser, beto/betoUser, dana/danaUser (students with accounts)
// - carla, eli, fito (roster members without accounts)
// - adminUser (school_admin without a roster entry)
// - an election in StatusSubmission (clock at exSubStart).
func setupListSchool(t *testing.T, ctx context.Context) (listSchool, *fakeClock, *elections.Store) {
	t.Helper()
	s := newSchool(t, ctx)
	ls := listSchool{school: s}
	su := tdb.Super

	addMember := func(name, doc string, withAccount bool) (rosterID, userID string) {
		if err := su.QueryRow(ctx,
			`INSERT INTO roster_members (tenant_id, full_name, document_number) VALUES ($1, $2, $3) RETURNING id::text`,
			s.tenant, name, doc).Scan(&rosterID); err != nil {
			t.Fatal(err)
		}
		if withAccount {
			if err := su.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id::text`,
				fmt.Sprintf("ls-%s-%s@example.com", s.tenant[:8], doc)).Scan(&userID); err != nil {
				t.Fatal(err)
			}
			if _, err := su.Exec(ctx,
				`INSERT INTO memberships (tenant_id, user_id, role, roster_member_id) VALUES ($1, $2, 'student', $3)`,
				s.tenant, userID, rosterID); err != nil {
				t.Fatal(err)
			}
		}
		return rosterID, userID
	}

	if err := su.QueryRow(ctx,
		`SELECT user_id::text FROM memberships WHERE tenant_id = $1 AND roster_member_id = $2`,
		s.tenant, s.beto).Scan(&ls.betoUser); err != nil {
		t.Fatal(err)
	}
	ls.dana, ls.danaUser = addMember("Dana Ejemplo", "30000004", true)
	ls.eli, _ = addMember("Eli Supuesto", "30000005", false)
	ls.fito, _ = addMember("Fito Extra", "30000006", false)
	ls.gabi, _ = addMember("Gabi Nueva", "30000008", false)
	ls.hugo, _ = addMember("Hugo Nuevo", "30000009", false)
	ls.iris, _ = addMember("Iris Nueva", "30000010", false)
	ls.commRoster, ls.commUser = addMember("Gabi Junta", "30000007", true)

	if err := su.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id::text`,
		fmt.Sprintf("admin-%s@example.com", s.tenant[:8])).Scan(&ls.adminUser); err != nil {
		t.Fatal(err)
	}
	if _, err := su.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'school_admin')`,
		s.tenant, ls.adminUser); err != nil {
		t.Fatal(err)
	}

	clk := today()
	st := newStore(clk)
	adminSess := auth.Session{UserID: ls.adminUser, TenantID: s.tenant, EmailVerified: true, MFAEnrolled: true, MFAVerified: true}
	if _, err := st.Designate(ctx, adminSess, []string{ls.commRoster}); err != nil {
		t.Fatal(err)
	}
	commSess := auth.Session{UserID: ls.commUser, TenantID: s.tenant, EmailVerified: true, MFAEnrolled: true, MFAVerified: true}
	e, err := st.Create(ctx, commSess, draft2027())
	if err != nil {
		t.Fatal(err)
	}
	ls.electionID = e.ID
	// Move clock into submission period.
	clk.t = example2027.SubmissionStartsAt
	return ls, clk, st
}

func fullCandidates(ana, ciro, dana, eli string) []elections.CandidateInput {
	return []elections.CandidateInput{
		{Position: "Presidente", RosterMemberID: ana},
		{Position: "Vicepresidente", RosterMemberID: ciro},
		{Position: "Secretario", RosterMemberID: dana},
		{Position: "Tesorero", RosterMemberID: eli},
	}
}

type storeListRow struct {
	ID               string
	ElectionID       string
	Name             string
	RepresentativeID string
	Status           string
	IsActive         bool
	RejectionReason  *string
	ReviewedAt       *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type storeCandidateRow struct {
	ListID        string
	PositionOrder int
	CandidateID   string
	IsActive      bool
}

type storeListSnapshot struct {
	Lists      []storeListRow
	Candidates []storeCandidateRow
}

func snapshotLists(t *testing.T, ctx context.Context, tenant string) storeListSnapshot {
	t.Helper()
	var snap storeListSnapshot
	rows, err := tdb.Super.Query(ctx, `
		SELECT id::text, election_id::text, name, representative_id::text, status, is_active,
		       rejection_reason, reviewed_at, created_at, updated_at
		FROM election_lists
		WHERE tenant_id = $1
		ORDER BY id`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var r storeListRow
		var reviewedAt *time.Time
		if err := rows.Scan(&r.ID, &r.ElectionID, &r.Name, &r.RepresentativeID, &r.Status, &r.IsActive,
			&r.RejectionReason, &reviewedAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
			t.Fatal(err)
		}
		r.CreatedAt = r.CreatedAt.UTC()
		r.UpdatedAt = r.UpdatedAt.UTC()
		if reviewedAt != nil {
			utc := reviewedAt.UTC()
			r.ReviewedAt = &utc
		}
		snap.Lists = append(snap.Lists, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	cRows, err := tdb.Super.Query(ctx, `
		SELECT list_id::text, position_order, candidate_id::text, is_active
		FROM election_list_candidates
		WHERE tenant_id = $1
		ORDER BY list_id, position_order`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	defer cRows.Close()
	for cRows.Next() {
		var c storeCandidateRow
		if err := cRows.Scan(&c.ListID, &c.PositionOrder, &c.CandidateID, &c.IsActive); err != nil {
			t.Fatal(err)
		}
		snap.Candidates = append(snap.Candidates, c)
	}
	if err := cRows.Err(); err != nil {
		t.Fatal(err)
	}
	return snap
}

func assertListsUnchanged(t *testing.T, ctx context.Context, tenant string, before storeListSnapshot, label string) {
	t.Helper()
	after := snapshotLists(t, ctx, tenant)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("%s: persisted election lists mutated:\nbefore: %+v\nafter:  %+v", label, before, after)
	}
}

func TestStoreSubmitAndEditListRules(t *testing.T) {
	ctx := integration(t)
	ls, clk, st := setupListSchool(t, ctx)

	betoSess := auth.Session{UserID: ls.betoUser, TenantID: ls.tenant} // student: no MFA required
	commSess := auth.Session{UserID: ls.commUser, TenantID: ls.tenant, EmailVerified: true, MFAEnrolled: true, MFAVerified: true}
	adminSess := auth.Session{UserID: ls.adminUser, TenantID: ls.tenant, EmailVerified: true, MFAEnrolled: true, MFAVerified: true}
	outsiderSess := auth.Session{UserID: ls.anaUser, TenantID: ""}

	// R1: school_admin without roster member and no-membership get ErrForbidden.
	if _, err := st.SubmitList(ctx, adminSess, ls.electionID, elections.ListInput{
		Name: "Lista Admin", Candidates: fullCandidates(ls.ana, ls.carla, ls.dana, ls.eli),
	}); !errors.Is(err, elections.ErrForbidden) {
		t.Fatalf("admin submit: err = %v, want ErrForbidden", err)
	}
	if _, err := st.SubmitList(ctx, outsiderSess, ls.electionID, elections.ListInput{
		Name: "Lista Fuera", Candidates: fullCandidates(ls.ana, ls.carla, ls.dana, ls.eli),
	}); !errors.Is(err, elections.ErrForbidden) {
		t.Fatalf("outsider submit: err = %v, want ErrForbidden", err)
	}

	// R7: before submission period -> ErrSubmissionClosed.
	clk.t = example2027.SubmissionStartsAt.Add(-time.Second)
	if _, err := st.SubmitList(ctx, betoSess, ls.electionID, elections.ListInput{
		Name: "Lista Azul", Candidates: fullCandidates(ls.ana, ls.carla, ls.dana, ls.eli),
	}); !errors.Is(err, elections.ErrSubmissionClosed) {
		t.Fatalf("before submission: err = %v, want ErrSubmissionClosed", err)
	}
	clk.t = example2027.SubmissionStartsAt

	// R3, R4, R6 validation errors write nothing.
	other := newSchool(t, ctx)
	for _, tc := range []struct {
		name string
		in   elections.ListInput
		want error
	}{
		{"blank name", elections.ListInput{Name: "   ", Candidates: fullCandidates(ls.ana, ls.carla, ls.dana, ls.eli)}, elections.ErrInvalidName},
		{"81 char name", elections.ListInput{Name: strings.Repeat("ñ", 81), Candidates: fullCandidates(ls.ana, ls.carla, ls.dana, ls.eli)}, elections.ErrInvalidName},
		{"missing position", elections.ListInput{Name: "Lista", Candidates: fullCandidates(ls.ana, ls.carla, ls.dana, ls.eli)[:3]}, elections.ErrInvalidCandidates},
		{"unknown position", elections.ListInput{Name: "Lista", Candidates: []elections.CandidateInput{
			{Position: "Presidente", RosterMemberID: ls.ana}, {Position: "Vicepresidente", RosterMemberID: ls.carla},
			{Position: "Secretario", RosterMemberID: ls.dana}, {Position: "Vocal", RosterMemberID: ls.eli},
		}}, elections.ErrInvalidCandidates},
		{"same student twice", elections.ListInput{Name: "Lista", Candidates: fullCandidates(ls.ana, ls.ana, ls.dana, ls.eli)}, elections.ErrInvalidCandidates},
		{"unknown candidate uuid", elections.ListInput{Name: "Lista", Candidates: fullCandidates(ls.ana, ls.carla, ls.dana, "00000000-0000-4000-8000-000000000000")}, elections.ErrNotFound},
		{"candidate from other school", elections.ListInput{Name: "Lista", Candidates: fullCandidates(ls.ana, ls.carla, ls.dana, other.ana)}, elections.ErrNotFound},
		{"commission member as candidate", elections.ListInput{Name: "Lista", Candidates: fullCandidates(ls.ana, ls.carla, ls.dana, ls.commRoster)}, elections.ErrCandidateInCommission},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := st.SubmitList(ctx, betoSess, ls.electionID, tc.in); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if n := superCount(t, ctx, "election_lists", ls.tenant); n != 0 {
		t.Fatalf("stored lists = %d, want 0", n)
	}

	// R1 + R4: Beto submits "Lista Azul" with Ana, Carla (no account), Dana, Eli (no account).
	created, err := st.SubmitList(ctx, betoSess, ls.electionID, elections.ListInput{
		Name:       "  Lista Azul  ",
		Candidates: fullCandidates(ls.ana, ls.carla, ls.dana, ls.eli),
	})
	if err != nil {
		t.Fatalf("submit Lista Azul: %v", err)
	}
	if created.Name != "Lista Azul" || created.Status != elections.ListPending ||
		created.RepresentativeRosterMemberID != ls.beto || len(created.Candidates) != 4 {
		t.Fatalf("created = %+v", created)
	}

	// R3: duplicate name ignoring case -> ErrListNameTaken.
	if _, err := st.CommissionSubmitList(ctx, commSess, ls.electionID, ls.dana, elections.ListInput{
		Name: "lista azul", Candidates: fullCandidates(ls.fito, ls.beto, ls.gabi, ls.hugo),
	}); !errors.Is(err, elections.ErrListNameTaken) {
		t.Fatalf("duplicate name: err = %v, want ErrListNameTaken", err)
	}

	// R5: candidate already on pending list -> ErrCandidateOnOtherList, nothing saved.
	if _, err := st.SubmitList(ctx, auth.Session{UserID: ls.danaUser, TenantID: ls.tenant}, ls.electionID, elections.ListInput{
		Name: "Lista Verde", Candidates: fullCandidates(ls.ana, ls.beto, ls.fito, ls.eli),
	}); !errors.Is(err, elections.ErrCandidateOnOtherList) {
		t.Fatalf("overlapping candidate: err = %v, want ErrCandidateOnOtherList", err)
	}
	if n := superCount(t, ctx, "election_lists", ls.tenant); n != 1 {
		t.Fatalf("stored lists = %d, want 1", n)
	}

	// R9: Beto and commission can edit pending list (keeping same name, swapping candidates).
	edited, err := st.EditList(ctx, betoSess, ls.electionID, created.ID, elections.ListInput{
		Name:       "Lista Azul",
		Candidates: fullCandidates(ls.dana, ls.carla, ls.ana, ls.eli),
	})
	if err != nil {
		t.Fatalf("rep edit: %v", err)
	}
	if edited.Candidates[0].RosterMemberID != ls.dana {
		t.Fatalf("edited first candidate = %s, want dana", edited.Candidates[0].RosterMemberID)
	}
	editedByComm, err := st.EditList(ctx, commSess, ls.electionID, created.ID, elections.ListInput{
		Name:       "Lista Azul Renovada",
		Candidates: fullCandidates(ls.ana, ls.carla, ls.dana, ls.eli),
	})
	if err != nil {
		t.Fatalf("comm edit: %v", err)
	}
	if editedByComm.Name != "Lista Azul Renovada" {
		t.Fatalf("comm edited name = %q", editedByComm.Name)
	}
}

func TestStoreConcurrentCandidateUniqueness(t *testing.T) {
	ctx := integration(t)
	ls, _, st := setupListSchool(t, ctx)

	s1 := auth.Session{UserID: ls.anaUser, TenantID: ls.tenant}
	s2 := auth.Session{UserID: ls.betoUser, TenantID: ls.tenant}
	// Both lists share ls.carla as Vicepresidente.
	in1 := elections.ListInput{Name: "Lista Uno", Candidates: fullCandidates(ls.ana, ls.carla, ls.dana, ls.eli)}
	in2 := elections.ListInput{Name: "Lista Dos", Candidates: fullCandidates(ls.beto, ls.carla, ls.fito, ls.dana)}

	errs := make([]error, 2)
	var start, done sync.WaitGroup
	start.Add(1)
	for i, tc := range []struct {
		sess auth.Session
		in   elections.ListInput
	}{{s1, in1}, {s2, in2}} {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			_, errs[i] = st.SubmitList(ctx, tc.sess, ls.electionID, tc.in)
		}()
	}
	start.Done()
	done.Wait()

	var okCount, conflictCount int
	for _, err := range errs {
		switch {
		case err == nil:
			okCount++
		case errors.Is(err, elections.ErrCandidateOnOtherList):
			conflictCount++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if okCount != 1 || conflictCount != 1 {
		t.Fatalf("results = %v, want 1 nil and 1 ErrCandidateOnOtherList", errs)
	}
	if n := superCount(t, ctx, "election_lists", ls.tenant); n != 1 {
		t.Fatalf("stored lists = %d, want 1", n)
	}
}

func TestStoreApproveRejectAndVisibilityRules(t *testing.T) {
	ctx := integration(t)
	ls, clk, st := setupListSchool(t, ctx)

	betoSess := auth.Session{UserID: ls.betoUser, TenantID: ls.tenant}
	anaSess := auth.Session{UserID: ls.anaUser, TenantID: ls.tenant}
	danaSess := auth.Session{UserID: ls.danaUser, TenantID: ls.tenant}
	commSess := auth.Session{UserID: ls.commUser, TenantID: ls.tenant, EmailVerified: true, MFAEnrolled: true, MFAVerified: true}
	commNoMFA := auth.Session{UserID: ls.commUser, TenantID: ls.tenant, EmailVerified: true}

	l1, err := st.SubmitList(ctx, betoSess, ls.electionID, elections.ListInput{
		Name: "Lista Azul", Candidates: fullCandidates(ls.ana, ls.carla, ls.dana, ls.eli),
	})
	if err != nil {
		t.Fatal(err)
	}

	// R10: commission actions without MFA -> ErrMFARequired.
	if _, err := st.ApproveList(ctx, commNoMFA, ls.electionID, l1.ID); !errors.Is(err, auth.ErrMFARequired) {
		t.Fatalf("approve without MFA: %v", err)
	}
	if _, err := st.RejectList(ctx, commNoMFA, ls.electionID, l1.ID, "Motivo"); !errors.Is(err, auth.ErrMFARequired) {
		t.Fatalf("reject without MFA: %v", err)
	}
	if _, err := st.CommissionSubmitList(ctx, commNoMFA, ls.electionID, ls.fito, elections.ListInput{
		Name: "Lista Roja", Candidates: fullCandidates(ls.ana, ls.carla, ls.dana, ls.eli),
	}); !errors.Is(err, auth.ErrMFARequired) {
		t.Fatalf("commission submit without MFA: %v", err)
	}

	// R8: non-commission cannot approve/reject -> ErrForbidden.
	if _, err := st.ApproveList(ctx, betoSess, ls.electionID, l1.ID); !errors.Is(err, elections.ErrForbidden) {
		t.Fatalf("rep approve: %v", err)
	}
	if _, err := st.RejectList(ctx, betoSess, ls.electionID, l1.ID, "Motivo"); !errors.Is(err, elections.ErrForbidden) {
		t.Fatalf("rep reject: %v", err)
	}

	// R11: pending list visible to rep and commission, hidden (ErrNotFound) from another student.
	if _, err := st.GetList(ctx, betoSess, ls.electionID, l1.ID); err != nil {
		t.Fatalf("rep GetList pending: %v", err)
	}
	if _, err := st.GetList(ctx, commSess, ls.electionID, l1.ID); err != nil {
		t.Fatalf("comm GetList pending: %v", err)
	}
	if _, err := st.GetList(ctx, anaSess, ls.electionID, l1.ID); !errors.Is(err, elections.ErrNotFound) {
		t.Fatalf("other student GetList pending: %v, want ErrNotFound", err)
	}

	// R8: blank or >500 reason -> ErrReasonRequired.
	for _, r := range []string{"", "   ", strings.Repeat("x", 501)} {
		if _, err := st.RejectList(ctx, commSess, ls.electionID, l1.ID, r); !errors.Is(err, elections.ErrReasonRequired) {
			t.Fatalf("reason %d chars: err = %v, want ErrReasonRequired", len(r), err)
		}
	}

	// Reject l1 with valid reason: candidates become free (R5), cannot be edited/approved/rejected again (R9).
	rejected, err := st.RejectList(ctx, commSess, ls.electionID, l1.ID, "Falta confirmar suplentes")
	if err != nil {
		t.Fatalf("reject l1: %v", err)
	}
	if rejected.Status != elections.ListRejected || rejected.RejectionReason == nil || *rejected.RejectionReason != "Falta confirmar suplentes" {
		t.Fatalf("rejected = %+v", rejected)
	}
	if _, err := st.EditList(ctx, betoSess, ls.electionID, l1.ID, elections.ListInput{
		Name: "Lista Azul", Candidates: fullCandidates(ls.ana, ls.carla, ls.dana, ls.eli),
	}); !errors.Is(err, elections.ErrListRejected) {
		t.Fatalf("edit rejected: %v", err)
	}
	if _, err := st.ApproveList(ctx, commSess, ls.electionID, l1.ID); !errors.Is(err, elections.ErrListRejected) {
		t.Fatalf("approve rejected: %v", err)
	}

	// Another student still gets ErrNotFound on rejected list, while rep sees reason.
	if _, err := st.GetList(ctx, anaSess, ls.electionID, l1.ID); !errors.Is(err, elections.ErrNotFound) {
		t.Fatalf("other student GetList rejected: %v, want ErrNotFound", err)
	}
	viewRep, err := st.ListLists(ctx, betoSess, ls.electionID)
	if err != nil || len(viewRep.Lists) != 1 || viewRep.Lists[0].RejectionReason == nil {
		t.Fatalf("rep ListLists: %+v, err=%v", viewRep, err)
	}

	// R2 + R5: commission loads a new list for Dana reusing Ana/Carla/Dana/Eli and even the name "Lista Azul".
	l2, err := st.CommissionSubmitList(ctx, commSess, ls.electionID, ls.dana, elections.ListInput{
		Name: "Lista Azul", Candidates: fullCandidates(ls.ana, ls.carla, ls.dana, ls.eli),
	})
	if err != nil {
		t.Fatalf("commission submit reusing rejected name/candidates: %v", err)
	}
	if l2.RepresentativeRosterMemberID != ls.dana {
		t.Fatalf("rep = %s, want dana", l2.RepresentativeRosterMemberID)
	}

	// Approve l2 -> frozen (R9), visible to everyone (R11).
	approved, err := st.ApproveList(ctx, commSess, ls.electionID, l2.ID)
	if err != nil || approved.Status != elections.ListApproved {
		t.Fatalf("approve l2: %+v, err=%v", approved, err)
	}
	for _, fn := range []func() error{
		func() error { _, err := st.ApproveList(ctx, commSess, ls.electionID, l2.ID); return err },
		func() error { _, err := st.RejectList(ctx, commSess, ls.electionID, l2.ID, "Tarde"); return err },
		func() error {
			_, err := st.EditList(ctx, danaSess, ls.electionID, l2.ID, elections.ListInput{
				Name: "Otra", Candidates: fullCandidates(ls.ana, ls.carla, ls.dana, ls.eli),
			})
			return err
		},
	} {
		if err := fn(); !errors.Is(err, elections.ErrListFrozen) {
			t.Fatalf("frozen list action: err = %v, want ErrListFrozen", err)
		}
	}

	viewAna, err := st.ListLists(ctx, anaSess, ls.electionID)
	if err != nil || len(viewAna.Lists) != 1 || viewAna.Lists[0].ID != l2.ID {
		t.Fatalf("ana ListLists = %+v, err=%v (want only approved l2)", viewAna, err)
	}

	// R7: before and after submission period, submit/commission-submit/edit/approve/reject on a pending
	// list all return ErrSubmissionClosed with zero persisted changes.
	pending, err := st.SubmitList(ctx, betoSess, ls.electionID, elections.ListInput{
		Name: "Lista Pendiente R7", Candidates: fullCandidates(ls.beto, ls.gabi, ls.hugo, ls.iris),
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeClosed := snapshotLists(t, ctx, ls.tenant)
	for _, tc := range []struct {
		name string
		at   time.Time
	}{
		{"before submissionStartsAt", example2027.SubmissionStartsAt.Add(-time.Second)},
		{"at submissionEndsAt", example2027.SubmissionEndsAt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk.t = tc.at
			if _, err := st.SubmitList(ctx, betoSess, ls.electionID, elections.ListInput{
				Name: "Lista Fuera", Candidates: fullCandidates(ls.beto, ls.fito, ls.ana, ls.eli),
			}); !errors.Is(err, elections.ErrSubmissionClosed) {
				t.Fatalf("submit: %v, want ErrSubmissionClosed", err)
			}
			if _, err := st.CommissionSubmitList(ctx, commSess, ls.electionID, ls.fito, elections.ListInput{
				Name: "Lista Fuera Comm", Candidates: fullCandidates(ls.beto, ls.fito, ls.ana, ls.eli),
			}); !errors.Is(err, elections.ErrSubmissionClosed) {
				t.Fatalf("commission submit: %v, want ErrSubmissionClosed", err)
			}
			if _, err := st.EditList(ctx, betoSess, ls.electionID, pending.ID, elections.ListInput{
				Name: "Editada Fuera Rep", Candidates: fullCandidates(ls.beto, ls.gabi, ls.hugo, ls.iris),
			}); !errors.Is(err, elections.ErrSubmissionClosed) {
				t.Fatalf("rep edit: %v, want ErrSubmissionClosed", err)
			}
			if _, err := st.EditList(ctx, commSess, ls.electionID, pending.ID, elections.ListInput{
				Name: "Editada Fuera Comm", Candidates: fullCandidates(ls.beto, ls.gabi, ls.hugo, ls.iris),
			}); !errors.Is(err, elections.ErrSubmissionClosed) {
				t.Fatalf("comm edit: %v, want ErrSubmissionClosed", err)
			}
			if _, err := st.ApproveList(ctx, commSess, ls.electionID, pending.ID); !errors.Is(err, elections.ErrSubmissionClosed) {
				t.Fatalf("approve: %v, want ErrSubmissionClosed", err)
			}
			if _, err := st.RejectList(ctx, commSess, ls.electionID, pending.ID, "Fuera"); !errors.Is(err, elections.ErrSubmissionClosed) {
				t.Fatalf("reject: %v, want ErrSubmissionClosed", err)
			}
			assertListsUnchanged(t, ctx, ls.tenant, beforeClosed, "R7 "+tc.name)
		})
	}

	// R12: cross-tenant requests return ErrNotFound.
	other := newSchool(t, ctx)
	otherSess := auth.Session{UserID: other.anaUser, TenantID: other.tenant}
	if _, err := st.ListLists(ctx, otherSess, ls.electionID); !errors.Is(err, elections.ErrNotFound) {
		t.Fatalf("other tenant ListLists: %v", err)
	}
	if _, err := st.GetList(ctx, otherSess, ls.electionID, l2.ID); !errors.Is(err, elections.ErrNotFound) {
		t.Fatalf("other tenant GetList: %v", err)
	}
	if got, err := st.ListRosterCandidates(ctx, anaSess, ls.electionID); err != nil || len(got) != 10 {
		t.Fatalf("ListRosterCandidates len = %d, err = %v, want 10", len(got), err)
	}
	// Verify ListLists returns sorted slices and no panic.
	_ = slices.IsSortedFunc(viewAna.Lists, func(a, b elections.ElectionList) int { return strings.Compare(a.ID, b.ID) })
}
