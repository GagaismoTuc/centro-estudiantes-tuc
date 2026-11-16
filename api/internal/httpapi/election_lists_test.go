package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type listCandidateJSON struct {
	Position       string `json:"position"`
	RosterMemberID string `json:"rosterMemberId"`
	FullName       string `json:"fullName"`
}

type electionListJSON struct {
	ID                           string              `json:"id"`
	ElectionID                   string              `json:"electionId"`
	Name                         string              `json:"name"`
	Status                       string              `json:"status"`
	RejectionReason              *string             `json:"rejectionReason"`
	RepresentativeRosterMemberID string              `json:"representativeRosterMemberId"`
	RepresentativeFullName       string              `json:"representativeFullName"`
	Candidates                   []listCandidateJSON `json:"candidates"`
	ReviewedAt                   *string             `json:"reviewedAt"`
	CreatedAt                    string              `json:"createdAt"`
	UpdatedAt                    string              `json:"updatedAt"`
}

type electionListsResponseJSON struct {
	RosterMemberID *string            `json:"rosterMemberId"`
	Lists          []electionListJSON `json:"lists"`
}

type listSchool struct {
	*school
	subStart   time.Time
	subEnd     time.Time
	electionID string
	betoTok    string
	betoID     string
	ciroID     string
	danaTok    string
	danaID     string
	eliID      string
	fitoID     string
	gabiID     string
	hugoID     string
	irisID     string
	commNoMFA  string
	commTok    string
	commID     string
}

func newListSchool(t *testing.T, ctx context.Context) *listSchool {
	t.Helper()
	s := newSchool(t, ctx)
	ls := &listSchool{school: s}

	_, ls.betoTok, ls.betoID = s.student(t, ctx, "Beto Alumno", "30222101")
	ls.ciroID = seedMember(t, ctx, s.tenant, "Ciro Inventado", "30222102") // no account
	_, ls.danaTok, ls.danaID = s.student(t, ctx, "Dana Ejemplo", "30222103")
	ls.eliID = seedMember(t, ctx, s.tenant, "Eli Supuesto", "30222104") // no account
	ls.fitoID = seedMember(t, ctx, s.tenant, "Fito Cuarto", "30222105")
	ls.gabiID = seedMember(t, ctx, s.tenant, "Gabi Quinta", "30222106")
	ls.hugoID = seedMember(t, ctx, s.tenant, "Hugo Sexto", "30222107")
	ls.irisID = seedMember(t, ctx, s.tenant, "Iris Septima", "30222110")

	_, unverifiedComm, commID := s.student(t, ctx, "Comision Dos", "30222108")
	ls.commID = commID
	decodeCommission(t, s.designate(s.officer, s.ana, commID))
	ls.commNoMFA = unverifiedComm
	ls.commTok = s.member

	base := s.clk.Now().UTC().Truncate(time.Minute)
	ls.subStart = base.Add(time.Hour)
	ls.subEnd = base.Add(2 * time.Hour)
	d := with(draft(),
		"submissionStartsAt", ls.subStart.Format(time.RFC3339),
		"submissionEndsAt", ls.subEnd.Format(time.RFC3339),
		"startsAt", base.Add(3*time.Hour).Format(time.RFC3339),
		"endsAt", base.Add(4*time.Hour).Format(time.RFC3339),
	)
	e := decodeElection(t, s.create(s.member, encode(d)), http.StatusCreated)
	ls.electionID = e.ID
	s.clk.t = ls.subStart
	return ls
}

func candidatesPayload(a, b, c, d string) []map[string]string {
	return []map[string]string{
		{"position": "Presidente", "rosterMemberId": a},
		{"position": "Vicepresidente", "rosterMemberId": b},
		{"position": "Secretario", "rosterMemberId": c},
		{"position": "Tesorero", "rosterMemberId": d},
	}
}

func listBody(name string, cands []map[string]string) string {
	return encode(map[string]any{"name": name, "candidates": cands})
}

func (ls *listSchool) listsPath() string {
	return fmt.Sprintf("%s/%s/lists", electionsPath, ls.electionID)
}

func (ls *listSchool) commissionListsPath() string {
	return fmt.Sprintf("%s/%s/commission/lists", electionsPath, ls.electionID)
}

func (ls *listSchool) listPath(id string) string {
	return fmt.Sprintf("%s/%s", ls.listsPath(), id)
}

func decodeList(t *testing.T, rec *httptest.ResponseRecorder, status int) electionListJSON {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, status, rec.Body)
	}
	wantNoStore(t, rec)
	var l electionListJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	return l
}

func decodeListsResponse(t *testing.T, rec *httptest.ResponseRecorder, status int) electionListsResponseJSON {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, status, rec.Body)
	}
	wantNoStore(t, rec)
	var out electionListsResponseJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (ls *listSchool) countLists(t *testing.T, ctx context.Context) int {
	t.Helper()
	var n int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM election_lists WHERE tenant_id = $1`, ls.tenant).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type dbCandidateRow struct {
	ListID        string
	PositionOrder int
	CandidateID   string
	IsActive      bool
}

type dbListRow struct {
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

type listDBSnapshot struct {
	Lists      []dbListRow
	Candidates []dbCandidateRow
}

func (ls *listSchool) snapshot(t *testing.T, ctx context.Context) listDBSnapshot {
	t.Helper()
	var snap listDBSnapshot

	rows, err := tdb.Super.Query(ctx, `
		SELECT id::text, election_id::text, name, representative_id::text, status, is_active,
		       rejection_reason, reviewed_at, created_at, updated_at
		FROM election_lists
		WHERE tenant_id = $1
		ORDER BY id`, ls.tenant)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var r dbListRow
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
		ORDER BY list_id, position_order`, ls.tenant)
	if err != nil {
		t.Fatal(err)
	}
	defer cRows.Close()
	for cRows.Next() {
		var c dbCandidateRow
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

func (ls *listSchool) assertSnapshotEqual(t *testing.T, ctx context.Context, before listDBSnapshot, label string) {
	t.Helper()
	after := ls.snapshot(t, ctx)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("%s: persisted election lists mutated:\nbefore: %+v\nafter:  %+v", label, before, after)
	}
}

// R1, R4, R10: student presents "Lista Azul"; school_admin without roster member and non-member
// get 403 forbidden before body is read; candidates may lack accounts; student needs no 2FA.
func TestSubmitElectionListPermissionsAndExample(t *testing.T) {
	ctx := integration(t)
	ls := newListSchool(t, ctx)

	// R1 negative checks before body is read.
	emptySnap := ls.snapshot(t, ctx)
	for _, tc := range []struct {
		name, token, want string
		status            int
	}{
		{"school admin without roster", ls.admin, forbiddenBody, http.StatusForbidden},
		{"no membership", ls.outsider, forbiddenBody, http.StatusForbidden},
		{"no session", "", unauthBody, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &readProbe{}
			req := httptest.NewRequest(http.MethodPost, ls.listsPath(), probe)
			if tc.token != "" {
				req.AddCookie(&http.Cookie{Name: cookieName, Value: tc.token})
			}
			rec := httptest.NewRecorder()
			ls.h.ServeHTTP(rec, req)
			wantResponse(t, rec, tc.status, tc.want)
			if probe.read {
				t.Fatal("request body read before permission check")
			}
		})
	}
	ls.assertSnapshotEqual(t, ctx, emptySnap, "R1 unauthorized submit")

	// R1 + R4 + R10: Beto (student, no MFA) submits Lista Azul with Dana (account), Ciro & Eli (no account).
	body := listBody("  Lista Azul  ", candidatesPayload(ls.danaID, ls.ciroID, ls.fitoID, ls.eliID))
	l := decodeList(t, ls.do(http.MethodPost, ls.listsPath(), body, electionIP, ls.betoTok, nil), http.StatusCreated)
	if l.Name != "Lista Azul" || l.Status != "pending" || l.RepresentativeRosterMemberID != ls.betoID ||
		l.RepresentativeFullName != "Beto Alumno" || l.RejectionReason != nil || len(l.Candidates) != 4 ||
		l.Candidates[0].Position != "Presidente" || l.Candidates[0].RosterMemberID != ls.danaID ||
		l.Candidates[1].FullName != "Ciro Inventado" {
		t.Fatalf("created list = %+v", l)
	}
	afterSubmit := ls.snapshot(t, ctx)
	wantResponse(t, ls.do(http.MethodPost, ls.listsPath(), listBody("Lista Admin", candidatesPayload(ls.gabiID, ls.hugoID, ls.irisID, ls.betoID)), electionIP, ls.admin, nil), http.StatusForbidden, forbiddenBody)
	wantResponse(t, ls.do(http.MethodPost, ls.listsPath(), listBody("Lista Fuera", candidatesPayload(ls.gabiID, ls.hugoID, ls.irisID, ls.betoID)), electionIP, ls.outsider, nil), http.StatusForbidden, forbiddenBody)
	ls.assertSnapshotEqual(t, ctx, afterSubmit, "R1 negative submit with existing list")
}

// R2, R10: commission loads a list on behalf of a roster member (with or without an account); requires MFA
// checked before reading the body; persisted state stays unchanged on rejections.
func TestCommissionLoadListAndMFARequirement(t *testing.T) {
	ctx := integration(t)
	ls := newListSchool(t, ctx)
	emptySnap := ls.snapshot(t, ctx)

	// Non-commission gets 403 forbidden before reading body; commission without MFA gets 403 mfa_required before reading body.
	for _, tc := range []struct{ name, token, want string }{
		{"student", ls.betoTok, forbiddenBody},
		{"officer not on commission", ls.officer, forbiddenBody},
		{"school admin", ls.admin, forbiddenBody},
		{"commission without MFA", ls.commNoMFA, mfaBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &readProbe{}
			req := httptest.NewRequest(http.MethodPost, ls.commissionListsPath(), probe)
			req.AddCookie(&http.Cookie{Name: cookieName, Value: tc.token})
			rec := httptest.NewRecorder()
			ls.h.ServeHTTP(rec, req)
			wantResponse(t, rec, http.StatusForbidden, tc.want)
			if probe.read {
				t.Fatal("request body read before permission/MFA check")
			}
		})
	}
	ls.assertSnapshotEqual(t, ctx, emptySnap, "R2/R10 unauthorized commission load")

	// Unknown or foreign representative -> 404 not_found, no changes.
	other := newFixture(t, ctx)
	foreignRep := seedMember(t, ctx, other.tenant, "Ajeno Rep", "30999002")
	for _, repID := range []string{"00000000-0000-4000-8000-000000000000", foreignRep} {
		badBody := encode(map[string]any{
			"name":                         "Lista Invalida",
			"representativeRosterMemberId": repID,
			"candidates":                   candidatesPayload(ls.danaID, ls.eliID, ls.fitoID, ls.gabiID),
		})
		wantResponse(t, ls.do(http.MethodPost, ls.commissionListsPath(), badBody, electionIP, ls.commTok, nil), http.StatusNotFound, `{"error":"not_found"}`)
	}
	ls.assertSnapshotEqual(t, ctx, emptySnap, "R2/R4 unknown or foreign representative")

	body := encode(map[string]any{
		"name":                         "Lista Naranja",
		"representativeRosterMemberId": ls.ciroID, // roster member without account
		"candidates":                   candidatesPayload(ls.danaID, ls.eliID, ls.fitoID, ls.gabiID),
	})
	l := decodeList(t, ls.do(http.MethodPost, ls.commissionListsPath(), body, electionIP, ls.commTok, nil), http.StatusCreated)
	if l.RepresentativeRosterMemberID != ls.ciroID || l.RepresentativeFullName != "Ciro Inventado" || l.Status != "pending" {
		t.Fatalf("commission loaded list = %+v", l)
	}

	// Commission loads on behalf of Dana (with account); Dana sees and edits it like self-submitted.
	danaBody := encode(map[string]any{
		"name":                         "Lista Violeta",
		"representativeRosterMemberId": ls.danaID,
		"candidates":                   candidatesPayload(ls.betoID, ls.ciroID, ls.hugoID, ls.irisID),
	})
	lDana := decodeList(t, ls.do(http.MethodPost, ls.commissionListsPath(), danaBody, electionIP, ls.commTok, nil), http.StatusCreated)
	gotDana := decodeList(t, ls.do(http.MethodGet, ls.listPath(lDana.ID), "", electionIP, ls.danaTok, nil), http.StatusOK)
	if gotDana.ID != lDana.ID || gotDana.RepresentativeRosterMemberID != ls.danaID {
		t.Fatalf("dana get commission-loaded list = %+v", gotDana)
	}
	editedDana := decodeList(t, ls.do(http.MethodPut, ls.listPath(lDana.ID), listBody("Lista Violeta Editada", candidatesPayload(ls.betoID, ls.ciroID, ls.hugoID, ls.irisID)), electionIP, ls.danaTok, nil), http.StatusOK)
	if editedDana.Name != "Lista Violeta Editada" {
		t.Fatalf("dana edited name = %q", editedDana.Name)
	}
}

// R3, R4, R5, R6: validation codes, 404 on foreign/unknown candidates, duplicate names/candidates, concurrency race.
func TestSubmitListValidationAndConcurrency(t *testing.T) {
	ctx := integration(t)
	ls := newListSchool(t, ctx)
	other := newFixture(t, ctx)
	foreignID := seedMember(t, ctx, other.tenant, "Ajeno", "30999001")

	emptySnap := ls.snapshot(t, ctx)
	extraCand := append(candidatesPayload(ls.danaID, ls.ciroID, ls.eliID, ls.fitoID), map[string]string{"position": "Vocal", "rosterMemberId": ls.gabiID})
	dupPos := []map[string]string{
		{"position": "Presidente", "rosterMemberId": ls.danaID},
		{"position": "Presidente", "rosterMemberId": ls.ciroID},
		{"position": "Secretario", "rosterMemberId": ls.eliID},
		{"position": "Tesorero", "rosterMemberId": ls.fitoID},
	}

	for _, tc := range []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"empty name", listBody("", candidatesPayload(ls.danaID, ls.ciroID, ls.eliID, ls.fitoID)), http.StatusUnprocessableEntity, "invalid_name"},
		{"blank name", listBody("   ", candidatesPayload(ls.danaID, ls.ciroID, ls.eliID, ls.fitoID)), http.StatusUnprocessableEntity, "invalid_name"},
		{"name 81 chars", listBody(strings.Repeat("a", 81), candidatesPayload(ls.danaID, ls.ciroID, ls.eliID, ls.fitoID)), http.StatusUnprocessableEntity, "invalid_name"},
		{"missing position", listBody("Lista", candidatesPayload(ls.danaID, ls.ciroID, ls.eliID, ls.fitoID)[:3]), http.StatusUnprocessableEntity, "invalid_candidates"},
		{"extra position", listBody("Lista", extraCand), http.StatusUnprocessableEntity, "invalid_candidates"},
		{"duplicate position", listBody("Lista", dupPos), http.StatusUnprocessableEntity, "invalid_candidates"},
		{"unknown position", listBody("Lista", []map[string]string{
			{"position": "Presidente", "rosterMemberId": ls.danaID},
			{"position": "Vicepresidente", "rosterMemberId": ls.ciroID},
			{"position": "Secretario", "rosterMemberId": ls.eliID},
			{"position": "Vocal", "rosterMemberId": ls.fitoID},
		}), http.StatusUnprocessableEntity, "invalid_candidates"},
		{"same candidate twice", listBody("Lista", candidatesPayload(ls.danaID, ls.danaID, ls.eliID, ls.fitoID)), http.StatusUnprocessableEntity, "invalid_candidates"},
		{"unknown candidate", listBody("Lista", candidatesPayload(ls.danaID, ls.ciroID, ls.eliID, "00000000-0000-4000-8000-000000000000")), http.StatusNotFound, "not_found"},
		{"other school candidate", listBody("Lista", candidatesPayload(ls.danaID, ls.ciroID, ls.eliID, foreignID)), http.StatusNotFound, "not_found"},
		{"commission member candidate", listBody("Lista", candidatesPayload(ls.danaID, ls.ciroID, ls.eliID, ls.commID)), http.StatusUnprocessableEntity, "candidate_in_commission"},
		{"unknown field", `{"name":"Lista","candidates":[],"extra":1}`, http.StatusBadRequest, "bad_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantResponse(t, ls.do(http.MethodPost, ls.listsPath(), tc.body, electionIP, ls.betoTok, nil), tc.status, fmt.Sprintf(`{"error":%q}`, tc.code))
			ls.assertSnapshotEqual(t, ctx, emptySnap, "R3/R4/R6 submit "+tc.name)
		})
	}

	// R5 concurrency race: two simultaneous requests sharing ls.ciroID -> one 201, one 409 candidate_on_other_list.
	b1 := listBody("Lista Uno", candidatesPayload(ls.betoID, ls.ciroID, ls.eliID, ls.fitoID))
	b2 := listBody("Lista Dos", candidatesPayload(ls.danaID, ls.ciroID, ls.gabiID, ls.hugoID))
	recs := make([]*httptest.ResponseRecorder, 2)
	var start, done sync.WaitGroup
	start.Add(1)
	for i, tc := range []struct{ tok, body string }{{ls.betoTok, b1}, {ls.danaTok, b2}} {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			recs[i] = ls.do(http.MethodPost, ls.listsPath(), tc.body, electionIP, tc.tok, nil)
		}()
	}
	start.Done()
	done.Wait()
	statuses := []int{recs[0].Code, recs[1].Code}
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusCreated, http.StatusConflict}) {
		t.Fatalf("concurrent statuses = %v, want [201 409]", statuses)
	}
	var winnerID string
	for _, rec := range recs {
		switch rec.Code {
		case http.StatusCreated:
			winnerID = decodeList(t, rec, http.StatusCreated).ID
		case http.StatusConflict:
			wantResponse(t, rec, http.StatusConflict, `{"error":"candidate_on_other_list"}`)
		}
	}
	if ls.countLists(t, ctx) != 1 {
		t.Fatalf("lists = %d, want 1", ls.countLists(t, ctx))
	}

	// R3: duplicate name ignoring case against non-rejected list -> 409 list_name_taken.
	winner := decodeList(t, ls.do(http.MethodGet, ls.listPath(winnerID), "", electionIP, ls.commTok, nil), http.StatusOK)
	dupName := strings.ToLower(winner.Name)
	var freeCand1, freeCand2, freeCand3 string
	var otherCand1, otherCand2, otherCand3 string
	if winner.Name == "Lista Uno" {
		freeCand1, freeCand2, freeCand3 = ls.danaID, ls.gabiID, ls.hugoID
		otherCand1, otherCand2, otherCand3 = ls.betoID, ls.eliID, ls.fitoID
	} else {
		freeCand1, freeCand2, freeCand3 = ls.betoID, ls.eliID, ls.fitoID
		otherCand1, otherCand2, otherCand3 = ls.danaID, ls.gabiID, ls.hugoID
	}
	wantResponse(t, ls.do(http.MethodPost, ls.listsPath(), listBody(dupName, candidatesPayload(freeCand1, freeCand2, freeCand3, ls.commID)), electionIP, ls.danaTok, nil),
		http.StatusUnprocessableEntity, `{"error":"candidate_in_commission"}`)
	// Add one more roster member so R3 duplicate-name check hits list_name_taken with 4 clean candidates.
	extraID := seedMember(t, ctx, ls.tenant, "Ivan Extra", "30222109")
	beforeConflicts := ls.snapshot(t, ctx)
	wantResponse(t, ls.do(http.MethodPost, ls.listsPath(), listBody(dupName, candidatesPayload(freeCand1, freeCand2, freeCand3, extraID)), electionIP, ls.danaTok, nil),
		http.StatusConflict, `{"error":"list_name_taken"}`)
	ls.assertSnapshotEqual(t, ctx, beforeConflicts, "R3 list_name_taken on POST")

	// R5 sequential blocking: reusing ls.ciroID while winner is pending -> 409 candidate_on_other_list, nothing saved.
	wantResponse(t, ls.do(http.MethodPost, ls.listsPath(), listBody("Lista Tres", candidatesPayload(freeCand1, ls.ciroID, freeCand2, freeCand3)), electionIP, ls.danaTok, nil),
		http.StatusConflict, `{"error":"candidate_on_other_list"}`)
	ls.assertSnapshotEqual(t, ctx, beforeConflicts, "R5 candidate_on_other_list while pending")

	// R5 post-rejection freedom: rejecting winner frees ls.ciroID and winner's name for a new list.
	decodeList(t, ls.do(http.MethodPost, ls.listPath(winnerID)+"/reject", `{"reason":"Rehacer"}`, electionIP, ls.commTok, nil), http.StatusOK)
	maxName := strings.Repeat("a", 80) // R3: exactly 80 chars accepted
	reused := decodeList(t, ls.do(http.MethodPost, ls.listsPath(), listBody(winner.Name, candidatesPayload(freeCand1, ls.ciroID, freeCand2, freeCand3)), electionIP, ls.danaTok, nil), http.StatusCreated)
	otherList := decodeList(t, ls.do(http.MethodPost, ls.listsPath(), listBody(maxName, candidatesPayload(otherCand1, otherCand2, otherCand3, extraID)), electionIP, ls.betoTok, nil), http.StatusCreated)
	_ = decodeList(t, ls.do(http.MethodPost, ls.listPath(reused.ID)+"/approve", "", electionIP, ls.commTok, nil), http.StatusOK)

	// R5 against approved list + R3/R5 validation on PUT leave persisted state unchanged.
	afterApprove := ls.snapshot(t, ctx)
	wantResponse(t, ls.do(http.MethodPost, ls.listsPath(), listBody("Lista Cuatro", candidatesPayload(freeCand1, ls.ciroID, freeCand2, freeCand3)), electionIP, ls.commTok, nil),
		http.StatusConflict, `{"error":"candidate_on_other_list"}`)
	wantResponse(t, ls.do(http.MethodPut, ls.listPath(otherList.ID), listBody("  ", candidatesPayload(otherCand1, otherCand2, otherCand3, extraID)), electionIP, ls.betoTok, nil),
		http.StatusUnprocessableEntity, `{"error":"invalid_name"}`)
	wantResponse(t, ls.do(http.MethodPut, ls.listPath(otherList.ID), listBody(maxName, candidatesPayload(otherCand1, otherCand2, otherCand3, extraID)[:3]), electionIP, ls.betoTok, nil),
		http.StatusUnprocessableEntity, `{"error":"invalid_candidates"}`)
	wantResponse(t, ls.do(http.MethodPut, ls.listPath(otherList.ID), listBody(strings.ToUpper(winner.Name), candidatesPayload(otherCand1, otherCand2, otherCand3, extraID)), electionIP, ls.betoTok, nil),
		http.StatusConflict, `{"error":"list_name_taken"}`)
	wantResponse(t, ls.do(http.MethodPut, ls.listPath(otherList.ID), listBody(maxName, candidatesPayload(otherCand1, ls.ciroID, otherCand3, extraID)), electionIP, ls.betoTok, nil),
		http.StatusConflict, `{"error":"candidate_on_other_list"}`)
	ls.assertSnapshotEqual(t, ctx, afterApprove, "R3/R5 approved conflict and PUT validation rejections")
}

// R7: submitting (student & commission), editing (rep & commission), approving, and rejecting
// outside the submission period (before start and at/after end) all return 409 submission_closed
// with zero persisted changes.
func TestElectionListSubmissionPeriod(t *testing.T) {
	ctx := integration(t)
	ls := newListSchool(t, ctx)

	// Before submission period with no lists yet: student and commission submits return 409 submission_closed.
	emptySnap := ls.snapshot(t, ctx)
	ls.clk.t = ls.subStart.Add(-time.Second)
	b1 := listBody("Lista Pendiente", candidatesPayload(ls.betoID, ls.ciroID, ls.eliID, ls.fitoID))
	commLoadBody := encode(map[string]any{
		"name":                         "Lista Comision",
		"representativeRosterMemberId": ls.danaID,
		"candidates":                   candidatesPayload(ls.danaID, ls.gabiID, ls.hugoID, ls.irisID),
	})
	wantResponse(t, ls.do(http.MethodPost, ls.listsPath(), b1, electionIP, ls.betoTok, nil), http.StatusConflict, `{"error":"submission_closed"}`)
	wantResponse(t, ls.do(http.MethodPost, ls.commissionListsPath(), commLoadBody, electionIP, ls.commTok, nil), http.StatusConflict, `{"error":"submission_closed"}`)
	ls.assertSnapshotEqual(t, ctx, emptySnap, "R7 submit before period with no lists")

	// Create a pending list inside the submission period, then snapshot its DB state.
	ls.clk.t = ls.subStart
	pending := decodeList(t, ls.do(http.MethodPost, ls.listsPath(), b1, electionIP, ls.betoTok, nil), http.StatusCreated)
	before := ls.snapshot(t, ctx)

	submitOtherBody := listBody("Lista Tarde", candidatesPayload(ls.danaID, ls.gabiID, ls.hugoID, ls.irisID))
	editRepBody := listBody("Editada Rep", candidatesPayload(ls.betoID, ls.eliID, ls.ciroID, ls.fitoID))
	editCommBody := listBody("Editada Comm", candidatesPayload(ls.betoID, ls.fitoID, ls.ciroID, ls.eliID))

	assertAllClosed := func(label string) {
		t.Helper()
		wantResponse(t, ls.do(http.MethodPost, ls.listsPath(), submitOtherBody, electionIP, ls.danaTok, nil), http.StatusConflict, `{"error":"submission_closed"}`)
		wantResponse(t, ls.do(http.MethodPost, ls.commissionListsPath(), commLoadBody, electionIP, ls.commTok, nil), http.StatusConflict, `{"error":"submission_closed"}`)
		wantResponse(t, ls.do(http.MethodPut, ls.listPath(pending.ID), editRepBody, electionIP, ls.betoTok, nil), http.StatusConflict, `{"error":"submission_closed"}`)
		wantResponse(t, ls.do(http.MethodPut, ls.listPath(pending.ID), editCommBody, electionIP, ls.commTok, nil), http.StatusConflict, `{"error":"submission_closed"}`)
		wantResponse(t, ls.do(http.MethodPost, ls.listPath(pending.ID)+"/approve", "", electionIP, ls.commTok, nil), http.StatusConflict, `{"error":"submission_closed"}`)
		wantResponse(t, ls.do(http.MethodPost, ls.listPath(pending.ID)+"/reject", `{"reason":"Fuera de fecha"}`, electionIP, ls.commTok, nil), http.StatusConflict, `{"error":"submission_closed"}`)
		ls.assertSnapshotEqual(t, ctx, before, label)
	}

	// Before submissionStartsAt (with a pending list present): all 6 actions return 409 submission_closed, no DB changes.
	ls.clk.t = ls.subStart.Add(-time.Second)
	assertAllClosed("R7 before submissionStartsAt")

	// At exact submissionEndsAt boundary: half-open window excludes end; all 6 actions return 409 submission_closed, no DB changes.
	ls.clk.t = ls.subEnd
	assertAllClosed("R7 at submissionEndsAt")

	// Well after submissionEndsAt (during scheduled/open): all 6 actions return 409 submission_closed, no DB changes.
	ls.clk.t = ls.subEnd.Add(time.Hour)
	assertAllClosed("R7 after submissionEndsAt")

	// Sanity check: 1s before submissionEndsAt is still inside the period and succeeds.
	ls.clk.t = ls.subEnd.Add(-time.Second)
	updated := decodeList(t, ls.do(http.MethodPut, ls.listPath(pending.ID), editRepBody, electionIP, ls.betoTok, nil), http.StatusOK)
	if updated.Name != "Editada Rep" {
		t.Fatalf("updated name = %q, want Editada Rep", updated.Name)
	}
}

// R7, R8, R9, R10, R11, R12: approval, rejection with reason, freeze, edit permissions, clock window,
// visibility by role, and cross-tenant 404.
func TestApproveRejectEditVisibilityAndPeriod(t *testing.T) {
	ctx := integration(t)
	ls := newListSchool(t, ctx)

	// R7: before submission period -> 409 submission_closed, no changes.
	emptySnap := ls.snapshot(t, ctx)
	ls.clk.t = ls.subStart.Add(-time.Second)
	b1 := listBody("Lista Azul", candidatesPayload(ls.betoID, ls.ciroID, ls.eliID, ls.fitoID))
	wantResponse(t, ls.do(http.MethodPost, ls.listsPath(), b1, electionIP, ls.betoTok, nil), http.StatusConflict, `{"error":"submission_closed"}`)
	ls.assertSnapshotEqual(t, ctx, emptySnap, "R7 submit before period")
	ls.clk.t = ls.subStart

	l1 := decodeList(t, ls.do(http.MethodPost, ls.listsPath(), b1, electionIP, ls.betoTok, nil), http.StatusCreated)
	afterL1 := ls.snapshot(t, ctx)

	// R8 + R10: approve/reject permissions & MFA checked before body read; persisted state stays unchanged.
	for _, tc := range []struct {
		name, token, suffix, want string
	}{
		{"rep approve", ls.betoTok, "/approve", forbiddenBody},
		{"officer approve", ls.officer, "/approve", forbiddenBody},
		{"admin approve", ls.admin, "/approve", forbiddenBody},
		{"comm no MFA approve", ls.commNoMFA, "/approve", mfaBody},
		{"rep reject", ls.betoTok, "/reject", forbiddenBody},
		{"officer reject", ls.officer, "/reject", forbiddenBody},
		{"admin reject", ls.admin, "/reject", forbiddenBody},
		{"comm no MFA reject", ls.commNoMFA, "/reject", mfaBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &readProbe{}
			req := httptest.NewRequest(http.MethodPost, ls.listPath(l1.ID)+tc.suffix, probe)
			req.AddCookie(&http.Cookie{Name: cookieName, Value: tc.token})
			rec := httptest.NewRecorder()
			ls.h.ServeHTTP(rec, req)
			wantResponse(t, rec, http.StatusForbidden, tc.want)
			if probe.read {
				t.Fatal("request body read before permission/MFA check")
			}
		})
	}
	ls.assertSnapshotEqual(t, ctx, afterL1, "R8/R10 unauthorized approve/reject")

	// R11: Dana (another student) gets 404 on pending l1 by id, and empty list in GET /lists.
	wantResponse(t, ls.do(http.MethodGet, ls.listPath(l1.ID), "", electionIP, ls.danaTok, nil), http.StatusNotFound, `{"error":"not_found"}`)
	danaView := decodeListsResponse(t, ls.do(http.MethodGet, ls.listsPath(), "", electionIP, ls.danaTok, nil), http.StatusOK)
	if len(danaView.Lists) != 0 || danaView.RosterMemberID == nil || *danaView.RosterMemberID != ls.danaID {
		t.Fatalf("dana view = %+v", danaView)
	}
	// Beto (apoderado) and commission see pending l1.
	betoView := decodeListsResponse(t, ls.do(http.MethodGet, ls.listsPath(), "", electionIP, ls.betoTok, nil), http.StatusOK)
	if len(betoView.Lists) != 1 || betoView.Lists[0].ID != l1.ID {
		t.Fatalf("beto view = %+v", betoView)
	}
	commView := decodeListsResponse(t, ls.do(http.MethodGet, ls.listsPath(), "", electionIP, ls.commTok, nil), http.StatusOK)
	if len(commView.Lists) != 1 || commView.Lists[0].ID != l1.ID {
		t.Fatalf("commission view pending = %+v", commView)
	}

	// R8: reject validation (missing, blank, >500 chars -> 422 reason_required), no changes.
	for _, rBody := range []string{`{}`, `{"reason":null}`, `{"reason":"   "}`, fmt.Sprintf(`{"reason":%q}`, strings.Repeat("r", 501))} {
		wantResponse(t, ls.do(http.MethodPost, ls.listPath(l1.ID)+"/reject", rBody, electionIP, ls.commTok, nil), http.StatusUnprocessableEntity, `{"error":"reason_required"}`)
	}
	ls.assertSnapshotEqual(t, ctx, afterL1, "R8 invalid rejection reasons")

	// Reject with valid reason -> candidates & name freed (R5), cannot edit/approve/reject (R9).
	rej := decodeList(t, ls.do(http.MethodPost, ls.listPath(l1.ID)+"/reject", `{"reason":"Falta aval de curso"}`, electionIP, ls.commTok, nil), http.StatusOK)
	if rej.Status != "rejected" || rej.RejectionReason == nil || *rej.RejectionReason != "Falta aval de curso" {
		t.Fatalf("rejected = %+v", rej)
	}
	afterReject := ls.snapshot(t, ctx)
	wantResponse(t, ls.do(http.MethodPut, ls.listPath(l1.ID), b1, electionIP, ls.betoTok, nil), http.StatusConflict, `{"error":"list_rejected"}`)
	wantResponse(t, ls.do(http.MethodPut, ls.listPath(l1.ID), b1, electionIP, ls.commTok, nil), http.StatusConflict, `{"error":"list_rejected"}`)
	wantResponse(t, ls.do(http.MethodPost, ls.listPath(l1.ID)+"/approve", "", electionIP, ls.commTok, nil), http.StatusConflict, `{"error":"list_rejected"}`)
	wantResponse(t, ls.do(http.MethodPost, ls.listPath(l1.ID)+"/reject", `{"reason":"Otro"}`, electionIP, ls.commTok, nil), http.StatusConflict, `{"error":"list_rejected"}`)
	ls.assertSnapshotEqual(t, ctx, afterReject, "R9 rejected list actions")

	wantResponse(t, ls.do(http.MethodGet, ls.listPath(l1.ID), "", electionIP, ls.danaTok, nil), http.StatusNotFound, `{"error":"not_found"}`)
	commRejView := decodeListsResponse(t, ls.do(http.MethodGet, ls.listsPath(), "", electionIP, ls.commTok, nil), http.StatusOK)
	if len(commRejView.Lists) != 1 || commRejView.Lists[0].RejectionReason == nil {
		t.Fatalf("commission view rejected = %+v", commRejView)
	}

	// Beto presents a new list reusing same name & candidates (R5, R9), edited by rep and by commission (R9), commission approves it.
	l2 := decodeList(t, ls.do(http.MethodPost, ls.listsPath(), b1, electionIP, ls.betoTok, nil), http.StatusCreated)
	editedBody := listBody("Lista Azul Editada", candidatesPayload(ls.betoID, ls.eliID, ls.ciroID, ls.fitoID))
	l2 = decodeList(t, ls.do(http.MethodPut, ls.listPath(l2.ID), editedBody, electionIP, ls.betoTok, nil), http.StatusOK)
	if l2.Name != "Lista Azul Editada" {
		t.Fatalf("edited name = %q", l2.Name)
	}
	commEditedBody := listBody("Lista Azul Comision", candidatesPayload(ls.betoID, ls.eliID, ls.ciroID, ls.fitoID))
	l2 = decodeList(t, ls.do(http.MethodPut, ls.listPath(l2.ID), commEditedBody, electionIP, ls.commTok, nil), http.StatusOK)
	if l2.Name != "Lista Azul Comision" {
		t.Fatalf("commission edited name = %q", l2.Name)
	}

	// Commission approves l2 -> frozen (R9), visible to Dana (R11).
	app := decodeList(t, ls.do(http.MethodPost, ls.listPath(l2.ID)+"/approve", "", electionIP, ls.commTok, nil), http.StatusOK)
	if app.Status != "approved" {
		t.Fatalf("approved = %+v", app)
	}
	afterFreeze := ls.snapshot(t, ctx)
	wantResponse(t, ls.do(http.MethodPost, ls.listPath(l2.ID)+"/approve", "", electionIP, ls.commTok, nil), http.StatusConflict, `{"error":"list_frozen"}`)
	wantResponse(t, ls.do(http.MethodPost, ls.listPath(l2.ID)+"/reject", `{"reason":"Tarde"}`, electionIP, ls.commTok, nil), http.StatusConflict, `{"error":"list_frozen"}`)
	wantResponse(t, ls.do(http.MethodPut, ls.listPath(l2.ID), editedBody, electionIP, ls.betoTok, nil), http.StatusConflict, `{"error":"list_frozen"}`)
	wantResponse(t, ls.do(http.MethodPut, ls.listPath(l2.ID), editedBody, electionIP, ls.commTok, nil), http.StatusConflict, `{"error":"list_frozen"}`)
	ls.assertSnapshotEqual(t, ctx, afterFreeze, "R9 frozen list actions")

	danaView = decodeListsResponse(t, ls.do(http.MethodGet, ls.listsPath(), "", electionIP, ls.danaTok, nil), http.StatusOK)
	if len(danaView.Lists) != 1 || danaView.Lists[0].ID != l2.ID || danaView.Lists[0].Status != "approved" {
		t.Fatalf("dana approved view = %+v", danaView)
	}
	_ = decodeList(t, ls.do(http.MethodGet, ls.listPath(l2.ID), "", electionIP, ls.danaTok, nil), http.StatusOK)

	// R7: after submission ends, submit/edit/approve/reject on pending list all return 409 submission_closed without persisted changes.
	l3 := decodeList(t, ls.do(http.MethodPost, ls.listsPath(), listBody("Lista Pendiente Fin", candidatesPayload(ls.danaID, ls.gabiID, ls.hugoID, ls.irisID)), electionIP, ls.danaTok, nil), http.StatusCreated)
	beforeEnd := ls.snapshot(t, ctx)
	ls.clk.t = ls.subEnd
	wantResponse(t, ls.do(http.MethodPost, ls.listsPath(), listBody("Tarde", candidatesPayload(ls.danaID, ls.gabiID, ls.hugoID, ls.ciroID)), electionIP, ls.danaTok, nil), http.StatusConflict, `{"error":"submission_closed"}`)
	wantResponse(t, ls.do(http.MethodPost, ls.commissionListsPath(), encode(map[string]any{
		"name":                         "Tarde Comision",
		"representativeRosterMemberId": ls.danaID,
		"candidates":                   candidatesPayload(ls.danaID, ls.gabiID, ls.hugoID, ls.irisID),
	}), electionIP, ls.commTok, nil), http.StatusConflict, `{"error":"submission_closed"}`)
	wantResponse(t, ls.do(http.MethodPut, ls.listPath(l3.ID), editedBody, electionIP, ls.danaTok, nil), http.StatusConflict, `{"error":"submission_closed"}`)
	wantResponse(t, ls.do(http.MethodPut, ls.listPath(l3.ID), editedBody, electionIP, ls.commTok, nil), http.StatusConflict, `{"error":"submission_closed"}`)
	wantResponse(t, ls.do(http.MethodPost, ls.listPath(l3.ID)+"/approve", "", electionIP, ls.commTok, nil), http.StatusConflict, `{"error":"submission_closed"}`)
	wantResponse(t, ls.do(http.MethodPost, ls.listPath(l3.ID)+"/reject", `{"reason":"Tarde"}`, electionIP, ls.commTok, nil), http.StatusConflict, `{"error":"submission_closed"}`)
	ls.assertSnapshotEqual(t, ctx, beforeEnd, "R7 after submission end")

	// R12: another school receives 404 not_found on election, lists, list, candidates, and write endpoints, with no changes.
	other := newSchool(t, ctx)
	wantResponse(t, other.do(http.MethodGet, ls.listsPath(), "", electionIP, other.reader, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, other.do(http.MethodGet, ls.listPath(l2.ID), "", electionIP, other.reader, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, other.do(http.MethodGet, fmt.Sprintf("%s/%s/candidates", electionsPath, ls.electionID), "", electionIP, other.reader, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, other.do(http.MethodPost, ls.listsPath(), b1, electionIP, other.member, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, other.do(http.MethodPost, ls.commissionListsPath(), encode(map[string]any{
		"name":                         "Intrusa",
		"representativeRosterMemberId": other.ana,
		"candidates":                   candidatesPayload(other.ana, ls.betoID, ls.ciroID, ls.eliID),
	}), electionIP, other.member, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, other.do(http.MethodPut, ls.listPath(l3.ID), editedBody, electionIP, other.member, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, other.do(http.MethodPost, ls.listPath(l3.ID)+"/approve", "", electionIP, other.member, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, other.do(http.MethodPost, ls.listPath(l3.ID)+"/reject", `{"reason":"Intrusa"}`, electionIP, other.member, nil), http.StatusNotFound, `{"error":"not_found"}`)
	ls.assertSnapshotEqual(t, ctx, beforeEnd, "R12 cross-tenant writes")

	// Candidates endpoint returns roster names without DNIs.
	cRec := ls.do(http.MethodGet, fmt.Sprintf("%s/%s/candidates", electionsPath, ls.electionID), "", electionIP, ls.danaTok, nil)
	if cRec.Code != http.StatusOK || strings.Contains(cRec.Body.String(), "30222") {
		t.Fatalf("candidates status=%d body=%s (must not contain DNI)", cRec.Code, cRec.Body)
	}
}
