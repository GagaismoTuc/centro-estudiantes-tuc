package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// R1, R4, R10: student presents "Lista Azul"; school_admin without roster member and non-member
// get 403 forbidden before body is read; candidates may lack accounts; student needs no 2FA.
func TestSubmitElectionListPermissionsAndExample(t *testing.T) {
	ctx := integration(t)
	ls := newListSchool(t, ctx)

	// R1 negative checks before body is read.
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
	if ls.countLists(t, ctx) != 0 {
		t.Fatal("expected 0 lists")
	}

	// R1 + R4 + R10: Beto (student, no MFA) submits Lista Azul with Dana (account), Ciro & Eli (no account).
	body := listBody("  Lista Azul  ", candidatesPayload(ls.danaID, ls.ciroID, ls.fitoID, ls.eliID))
	l := decodeList(t, ls.do(http.MethodPost, ls.listsPath(), body, electionIP, ls.betoTok, nil), http.StatusCreated)
	if l.Name != "Lista Azul" || l.Status != "pending" || l.RepresentativeRosterMemberID != ls.betoID ||
		l.RepresentativeFullName != "Beto Alumno" || l.RejectionReason != nil || len(l.Candidates) != 4 ||
		l.Candidates[0].Position != "Presidente" || l.Candidates[0].RosterMemberID != ls.danaID ||
		l.Candidates[1].FullName != "Ciro Inventado" {
		t.Fatalf("created list = %+v", l)
	}
}

// R2, R10: commission loads a list on behalf of a roster member (even without an account); requires MFA
// checked before reading the body.
func TestCommissionLoadListAndMFARequirement(t *testing.T) {
	ctx := integration(t)
	ls := newListSchool(t, ctx)

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

	body := encode(map[string]any{
		"name":                         "Lista Naranja",
		"representativeRosterMemberId": ls.ciroID, // roster member without account
		"candidates":                   candidatesPayload(ls.danaID, ls.eliID, ls.fitoID, ls.gabiID),
	})
	l := decodeList(t, ls.do(http.MethodPost, ls.commissionListsPath(), body, electionIP, ls.commTok, nil), http.StatusCreated)
	if l.RepresentativeRosterMemberID != ls.ciroID || l.RepresentativeFullName != "Ciro Inventado" || l.Status != "pending" {
		t.Fatalf("commission loaded list = %+v", l)
	}
}

// R3, R4, R5, R6: validation codes, 404 on foreign/unknown candidates, duplicate names/candidates, concurrency race.
func TestSubmitListValidationAndConcurrency(t *testing.T) {
	ctx := integration(t)
	ls := newListSchool(t, ctx)
	other := newFixture(t, ctx)
	foreignID := seedMember(t, ctx, other.tenant, "Ajeno", "30999001")

	for _, tc := range []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"blank name", listBody("   ", candidatesPayload(ls.danaID, ls.ciroID, ls.eliID, ls.fitoID)), http.StatusUnprocessableEntity, "invalid_name"},
		{"name 81 chars", listBody(strings.Repeat("a", 81), candidatesPayload(ls.danaID, ls.ciroID, ls.eliID, ls.fitoID)), http.StatusUnprocessableEntity, "invalid_name"},
		{"missing position", listBody("Lista", candidatesPayload(ls.danaID, ls.ciroID, ls.eliID, ls.fitoID)[:3]), http.StatusUnprocessableEntity, "invalid_candidates"},
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
			if ls.countLists(t, ctx) != 0 {
				t.Fatalf("list stored after %s", tc.name)
			}
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
	for _, rec := range recs {
		if rec.Code == http.StatusConflict {
			wantResponse(t, rec, http.StatusConflict, `{"error":"candidate_on_other_list"}`)
		}
	}
	if ls.countLists(t, ctx) != 1 {
		t.Fatalf("lists = %d, want 1", ls.countLists(t, ctx))
	}
}

// R7, R8, R9, R10, R11, R12: approval, rejection with reason, freeze, edit permissions, clock window,
// visibility by role, and cross-tenant 404.
func TestApproveRejectEditVisibilityAndPeriod(t *testing.T) {
	ctx := integration(t)
	ls := newListSchool(t, ctx)

	// R7: before submission period -> 409 submission_closed.
	ls.clk.t = ls.subStart.Add(-time.Second)
	b1 := listBody("Lista Azul", candidatesPayload(ls.betoID, ls.ciroID, ls.eliID, ls.fitoID))
	wantResponse(t, ls.do(http.MethodPost, ls.listsPath(), b1, electionIP, ls.betoTok, nil), http.StatusConflict, `{"error":"submission_closed"}`)
	ls.clk.t = ls.subStart

	l1 := decodeList(t, ls.do(http.MethodPost, ls.listsPath(), b1, electionIP, ls.betoTok, nil), http.StatusCreated)

	// R8 + R10: approve/reject permissions & MFA checked before body read.
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

	// R8: reject validation (missing, blank, >500 chars -> 422 reason_required).
	for _, rBody := range []string{`{}`, `{"reason":null}`, `{"reason":"   "}`, fmt.Sprintf(`{"reason":%q}`, strings.Repeat("r", 501))} {
		wantResponse(t, ls.do(http.MethodPost, ls.listPath(l1.ID)+"/reject", rBody, electionIP, ls.commTok, nil), http.StatusUnprocessableEntity, `{"error":"reason_required"}`)
	}
	// Reject with valid reason -> candidates & name freed (R5), cannot edit/approve/reject (R9).
	rej := decodeList(t, ls.do(http.MethodPost, ls.listPath(l1.ID)+"/reject", `{"reason":"Falta aval de curso"}`, electionIP, ls.commTok, nil), http.StatusOK)
	if rej.Status != "rejected" || rej.RejectionReason == nil || *rej.RejectionReason != "Falta aval de curso" {
		t.Fatalf("rejected = %+v", rej)
	}
	wantResponse(t, ls.do(http.MethodPut, ls.listPath(l1.ID), b1, electionIP, ls.betoTok, nil), http.StatusConflict, `{"error":"list_rejected"}`)
	wantResponse(t, ls.do(http.MethodGet, ls.listPath(l1.ID), "", electionIP, ls.danaTok, nil), http.StatusNotFound, `{"error":"not_found"}`)

	// Beto presents a new list reusing same name & candidates (R5, R9), edits it (R9), commission approves it.
	l2 := decodeList(t, ls.do(http.MethodPost, ls.listsPath(), b1, electionIP, ls.betoTok, nil), http.StatusCreated)
	editedBody := listBody("Lista Azul Editada", candidatesPayload(ls.betoID, ls.eliID, ls.ciroID, ls.fitoID))
	l2 = decodeList(t, ls.do(http.MethodPut, ls.listPath(l2.ID), editedBody, electionIP, ls.betoTok, nil), http.StatusOK)
	if l2.Name != "Lista Azul Editada" {
		t.Fatalf("edited name = %q", l2.Name)
	}

	// Commission approves l2 -> frozen (R9), visible to Dana (R11).
	app := decodeList(t, ls.do(http.MethodPost, ls.listPath(l2.ID)+"/approve", "", electionIP, ls.commTok, nil), http.StatusOK)
	if app.Status != "approved" {
		t.Fatalf("approved = %+v", app)
	}
	wantResponse(t, ls.do(http.MethodPost, ls.listPath(l2.ID)+"/approve", "", electionIP, ls.commTok, nil), http.StatusConflict, `{"error":"list_frozen"}`)
	wantResponse(t, ls.do(http.MethodPost, ls.listPath(l2.ID)+"/reject", `{"reason":"Tarde"}`, electionIP, ls.commTok, nil), http.StatusConflict, `{"error":"list_frozen"}`)
	wantResponse(t, ls.do(http.MethodPut, ls.listPath(l2.ID), editedBody, electionIP, ls.betoTok, nil), http.StatusConflict, `{"error":"list_frozen"}`)

	danaView = decodeListsResponse(t, ls.do(http.MethodGet, ls.listsPath(), "", electionIP, ls.danaTok, nil), http.StatusOK)
	if len(danaView.Lists) != 1 || danaView.Lists[0].ID != l2.ID || danaView.Lists[0].Status != "approved" {
		t.Fatalf("dana approved view = %+v", danaView)
	}
	_ = decodeList(t, ls.do(http.MethodGet, ls.listPath(l2.ID), "", electionIP, ls.danaTok, nil), http.StatusOK)

	// R7: after submission ends, approve/reject/edit/submit return 409 submission_closed.
	ls.clk.t = ls.subEnd
	wantResponse(t, ls.do(http.MethodPost, ls.listsPath(), listBody("Tarde", candidatesPayload(ls.danaID, ls.gabiID, ls.hugoID, ls.ciroID)), electionIP, ls.danaTok, nil), http.StatusConflict, `{"error":"submission_closed"}`)
	wantResponse(t, ls.do(http.MethodPost, ls.listPath(l2.ID)+"/approve", "", electionIP, ls.commTok, nil), http.StatusConflict, `{"error":"submission_closed"}`)

	// R12: another school receives 404 not_found on election, lists, list, and candidates.
	other := newSchool(t, ctx)
	wantResponse(t, other.do(http.MethodGet, ls.listsPath(), "", electionIP, other.reader, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, other.do(http.MethodGet, ls.listPath(l2.ID), "", electionIP, other.reader, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, other.do(http.MethodGet, fmt.Sprintf("%s/%s/candidates", electionsPath, ls.electionID), "", electionIP, other.reader, nil), http.StatusNotFound, `{"error":"not_found"}`)

	// Candidates endpoint returns roster names without DNIs.
	cRec := ls.do(http.MethodGet, fmt.Sprintf("%s/%s/candidates", electionsPath, ls.electionID), "", electionIP, ls.danaTok, nil)
	if cRec.Code != http.StatusOK || strings.Contains(cRec.Body.String(), "30222") {
		t.Fatalf("candidates status=%d body=%s (must not contain DNI)", cRec.Code, cRec.Body)
	}
}
