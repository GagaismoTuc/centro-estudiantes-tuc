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

const (
	latestPath    = "/api/elections/latest"
	exampleReason = "Se detectó un error en el padrón cargado"
)

func annulmentPath(id string) string { return electionsPath + "/" + id + "/annulment" }

func reasonBody(reason any) string { return encode(map[string]any{"reason": reason}) }

func (s *school) annul(token, id, body string) *httptest.ResponseRecorder {
	return s.do(http.MethodPost, annulmentPath(id), body, electionIP, token, nil)
}

type annulledJSON struct {
	electionJSON
	Annulment *struct {
		Reason     string `json:"reason"`
		AnnulledAt string `json:"annulledAt"`
	} `json:"annulment"`
}

func decodeAnnulled(t *testing.T, rec *httptest.ResponseRecorder, status int) annulledJSON {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, status, rec.Body)
	}
	wantNoStore(t, rec)
	var e annulledJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

// soonDraft is a draft whose periods start one hour from now, one hour each: submission at +1h,
// scheduled at +2h, open at +3h, closed at +4h. Sessions last 12 hours, so the clock can move
// through every state without signing in again.
func soonDraft(now time.Time) map[string]any {
	return with(draft(), "submissionStartsAt", now.Add(time.Hour).Format(time.RFC3339),
		"submissionEndsAt", now.Add(2*time.Hour).Format(time.RFC3339), "startsAt", now.Add(3*time.Hour).Format(time.RFC3339),
		"endsAt", now.Add(4*time.Hour).Format(time.RFC3339))
}

// stored reads the election's annulment and its commission's mandate as superuser.
type storedAnnulment struct {
	annulled         bool
	reason           *string
	commissionEnded  bool
	updatedAtChanged bool
}

func readAnnulment(t *testing.T, ctx context.Context, id string) storedAnnulment {
	t.Helper()
	var a storedAnnulment
	if err := tdb.Super.QueryRow(ctx, `
		SELECT e.annulled_at IS NOT NULL, e.annulment_reason, c.ended_at IS NOT NULL, e.updated_at <> e.created_at
		FROM elections e JOIN electoral_commissions c ON c.tenant_id = e.tenant_id AND c.id = e.commission_id
		WHERE e.id = $1`, id).Scan(&a.annulled, &a.reason, &a.commissionEnded, &a.updatedAtChanged); err != nil {
		t.Fatal(err)
	}
	return a
}

// wantNotAnnulled checks through the API and the database that the election was left as it was.
func (s *school) wantNotAnnulled(t *testing.T, ctx context.Context, id string) {
	t.Helper()
	if a := readAnnulment(t, ctx, id); a.annulled || a.reason != nil || a.commissionEnded || a.updatedAtChanged {
		t.Fatalf("stored = %+v, want not annulled, commission current, election untouched", a)
	}
	got := decodeAnnulled(t, s.get(s.reader, electionsPath+"/"+id), http.StatusOK)
	if got.State == "annulled" || got.Annulment != nil {
		t.Fatalf("read back = %+v, want not annulled", got)
	}
}

// R1: only a member of the election's Electoral commission annuls it.
func TestAnnulmentPermissions(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	e := decodeElection(t, s.create(s.member, encode(draft())), http.StatusCreated)
	for _, tc := range []struct{ name, token string }{
		{"student", s.reader},
		{"officer not on the commission", s.officer},
		{"Presidente not on the commission", s.presidente},
		{"school admin", s.admin},
		{"no membership", s.outsider},
		{"no session", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := http.StatusForbidden, forbiddenBody
			if tc.token == "" {
				status, body = http.StatusUnauthorized, unauthBody
			}
			wantResponse(t, s.annul(tc.token, e.ID, reasonBody(exampleReason)), status, body)
			s.wantNotAnnulled(t, ctx, e.ID)
		})
	}
	got := decodeAnnulled(t, s.annul(s.member, e.ID, reasonBody(exampleReason)), http.StatusOK)
	if got.ID != e.ID || got.State != "annulled" || got.Annulment == nil || got.Annulment.Reason != exampleReason {
		t.Fatalf("commission member annulment = %+v", got)
	}
}

// R2: annulled in every state before the end; from the end on (result published) it is refused.
func TestAnnulmentBeforeTheEndOnly(t *testing.T) {
	ctx := integration(t)
	for _, tc := range []struct {
		name    string
		advance time.Duration
		state   string
	}{
		{"draft", 0, "draft"},
		{"submission", time.Hour, "submission"},
		{"scheduled", 2 * time.Hour, "scheduled"},
		{"open", 3 * time.Hour, "open"},
		{"open one minute before the end", 4*time.Hour - time.Minute, "open"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSchool(t, ctx)
			e := decodeElection(t, s.create(s.member, encode(soonDraft(s.clk.Now()))), http.StatusCreated)
			s.clk.Advance(tc.advance)
			if got := decodeElection(t, s.get(s.reader, electionsPath+"/"+e.ID), http.StatusOK); got.State != tc.state {
				t.Fatalf("state before annulling = %q, want %q", got.State, tc.state)
			}
			if got := decodeAnnulled(t, s.annul(s.member, e.ID, reasonBody(exampleReason)), http.StatusOK); got.State != "annulled" {
				t.Fatalf("state = %q, want annulled", got.State)
			}
			if a := readAnnulment(t, ctx, e.ID); !a.annulled || a.reason == nil || *a.reason != exampleReason {
				t.Fatalf("stored = %+v, want annulled with the reason", a)
			}
		})
	}
	t.Run("at the end", func(t *testing.T) {
		s := newSchool(t, ctx)
		e := decodeElection(t, s.create(s.member, encode(soonDraft(s.clk.Now()))), http.StatusCreated)
		s.clk.Advance(4 * time.Hour)
		wantResponse(t, s.annul(s.member, e.ID, reasonBody(exampleReason)), http.StatusConflict, `{"error":"election_published"}`)
		s.wantNotAnnulled(t, ctx, e.ID)
		if got := decodeElection(t, s.get(s.reader, electionsPath+"/"+e.ID), http.StatusOK); got.State != "closed" {
			t.Fatalf("state = %q, want closed", got.State)
		}
	})
	t.Run("result published", func(t *testing.T) {
		s := newSchool(t, ctx)
		e := decodeElection(t, s.create(s.member, encode(soonDraft(s.clk.Now()))), http.StatusCreated)
		if _, err := tdb.Super.Exec(ctx, `UPDATE elections SET published_at = now() WHERE id = $1`, e.ID); err != nil {
			t.Fatal(err)
		}
		wantResponse(t, s.annul(s.member, e.ID, reasonBody(exampleReason)), http.StatusConflict, `{"error":"election_published"}`)
		s.wantNotAnnulled(t, ctx, e.ID)
	})
}

// R3: a reason of 1 to 500 characters is required, and every member of the school reads it.
func TestAnnulmentReason(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	e := decodeElection(t, s.create(s.member, encode(draft())), http.StatusCreated)
	for _, tc := range []struct{ name, body, code string }{
		{"missing", `{}`, "reason_required"},
		{"null", reasonBody(nil), "reason_required"},
		{"empty", reasonBody(""), "reason_required"},
		{"only spaces", reasonBody("   "), "reason_required"},
		{"501 characters", reasonBody(strings.Repeat("ñ", 501)), "reason_too_long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantResponse(t, s.annul(s.member, e.ID, tc.body), http.StatusUnprocessableEntity, fmt.Sprintf(`{"error":%q}`, tc.code))
			s.wantNotAnnulled(t, ctx, e.ID)
		})
	}
	t.Run("500 characters", func(t *testing.T) {
		g := newSchool(t, ctx)
		ge := decodeElection(t, g.create(g.member, encode(draft())), http.StatusCreated)
		long := strings.Repeat("ñ", 500)
		if got := decodeAnnulled(t, g.annul(g.member, ge.ID, reasonBody(long)), http.StatusOK); got.Annulment == nil || got.Annulment.Reason != long {
			t.Fatalf("annulment = %+v, want the 500-character reason", got.Annulment)
		}
	})

	decodeAnnulled(t, s.annul(s.member, e.ID, reasonBody(exampleReason)), http.StatusOK)
	_, unverifiedOfficer := s.signIn(t, ctx, "officer")
	want := fmt.Sprintf(`{"annulment":{"annulledAt":%q,"reason":%q},"commission":[{"fullName":"Ana Ficticia","rosterMemberId":"%s"}],"endsAt":"2027-03-10T21:00:00Z","id":"%s","mode":"secret","name":"Elección 2027","positions":["Presidente","Vicepresidente","Secretario","Tesorero"],"startsAt":"2027-03-10T11:00:00Z","state":"annulled","submissionEndsAt":"2027-03-06T03:00:00Z","submissionStartsAt":"2027-03-01T03:00:00Z"}`,
		utc(s.clk.Now()), exampleReason, s.ana, e.ID)
	for _, tc := range []struct{ name, token string }{
		{"student", s.reader}, {"officer", unverifiedOfficer}, {"school admin", s.admin}, {"commission member", s.member},
	} {
		t.Run("read by "+tc.name, func(t *testing.T) {
			for _, path := range []string{electionsPath + "/" + e.ID, latestPath} {
				rec := s.get(tc.token, path)
				if rec.Code != http.StatusOK {
					t.Fatalf("%s: status %d, body %s", path, rec.Code, rec.Body)
				}
				if got := strings.TrimSpace(rec.Body.String()); got != want {
					t.Fatalf("%s = %s\nwant %s", path, got, want)
				}
			}
		})
	}
}

// R4: an annulled election refuses every change, says "annulled" with its reason when asked, and
// nothing reopens it.
func TestAnnulledElectionNeverReopens(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	e := decodeElection(t, s.create(s.member, encode(soonDraft(s.clk.Now()))), http.StatusCreated)
	decodeAnnulled(t, s.annul(s.member, e.ID, reasonBody(exampleReason)), http.StatusOK)

	// A new commission, whose member may edit drafts, still cannot touch the annulled election.
	_, newMember, carla := s.student(t, ctx, "Carla Ficticia", "30111227")
	decodeCommission(t, s.designate(s.officer, carla))
	newMember, _ = s.confirm(t, newMember, s.enroll(t, newMember))
	wantResponse(t, s.edit(newMember, e.ID, encode(with(soonDraft(s.clk.Now()), "name", "Reabierta"))), http.StatusConflict, `{"error":"election_annulled"}`)
	wantResponse(t, s.edit(newMember, e.ID, encode(with(soonDraft(s.clk.Now()), "positions", []string{"Vocal"}))), http.StatusConflict, `{"error":"election_annulled"}`)

	// Another election, and the clock past the annulled one's end, leave it annulled.
	decodeElection(t, s.create(newMember, encode(soonDraft(s.clk.Now()))), http.StatusCreated)
	s.clk.Advance(5 * time.Hour)
	got := decodeAnnulled(t, s.get(s.reader, electionsPath+"/"+e.ID), http.StatusOK)
	if got.State != "annulled" || got.Annulment == nil || got.Annulment.Reason != exampleReason || got.Name != "Elección 2027" ||
		!slices.Equal(got.Positions, defaultPositions) {
		t.Fatalf("annulled election = %+v, want unchanged and annulled", got)
	}
	if a := readAnnulment(t, ctx, e.ID); !a.annulled || a.reason == nil || *a.reason != exampleReason {
		t.Fatalf("stored = %+v", a)
	}
}

// R5: a second annulment, sequential or simultaneous, is refused and keeps the first reason.
func TestAnnulmentTwiceOrConcurrently(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	e := decodeElection(t, s.create(s.member, encode(draft())), http.StatusCreated)
	decodeAnnulled(t, s.annul(s.member, e.ID, reasonBody(exampleReason)), http.StatusOK)
	wantResponse(t, s.annul(s.member, e.ID, reasonBody("Otro motivo")), http.StatusConflict, `{"error":"election_annulled"}`)
	if got := decodeAnnulled(t, s.get(s.reader, electionsPath+"/"+e.ID), http.StatusOK); got.Annulment == nil || got.Annulment.Reason != exampleReason {
		t.Fatalf("annulment = %+v, want the original reason", got.Annulment)
	}

	t.Run("two simultaneous requests", func(t *testing.T) {
		g := newSchool(t, ctx)
		ge := decodeElection(t, g.create(g.member, encode(draft())), http.StatusCreated)
		reasons := []string{"Motivo uno", "Motivo dos"}
		recs := make([]*httptest.ResponseRecorder, 2)
		var start, done sync.WaitGroup
		start.Add(1)
		for i := range recs {
			done.Add(1)
			go func() {
				defer done.Done()
				start.Wait()
				recs[i] = g.annul(g.member, ge.ID, reasonBody(reasons[i]))
			}()
		}
		start.Done()
		done.Wait()
		codes := []int{recs[0].Code, recs[1].Code}
		slices.Sort(codes)
		if !slices.Equal(codes, []int{http.StatusOK, http.StatusConflict}) {
			t.Fatalf("statuses = %v, want one 200 and one 409", codes)
		}
		winner := reasons[0]
		for i, r := range recs {
			if r.Code == http.StatusConflict {
				wantResponse(t, r, http.StatusConflict, `{"error":"election_annulled"}`)
			} else {
				winner = reasons[i]
			}
		}
		if a := readAnnulment(t, ctx, ge.ID); !a.annulled || a.reason == nil || *a.reason != winner {
			t.Fatalf("stored = %+v, want the winner's reason %q", a, winner)
		}
	})
}

// R6: annulling frees the school for another election and ends the commission's mandate.
func TestAnnulmentFreesTheSchool(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	e := decodeElection(t, s.create(s.member, encode(draft())), http.StatusCreated)
	wantResponse(t, s.create(s.member, encode(draft())), http.StatusConflict, `{"error":"election_in_progress"}`)
	decodeAnnulled(t, s.annul(s.member, e.ID, reasonBody(exampleReason)), http.StatusOK)

	wantResponse(t, s.create(s.member, encode(draft())), http.StatusForbidden, forbiddenBody)
	var st commissionStatusJSON
	if rec := s.get(s.member, commissionPath); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &st) != nil || st.Commission != nil || st.IsMember {
		t.Fatalf("commission after annulment: %d %s, want none and not a member", rec.Code, rec.Body)
	}
	if a := readAnnulment(t, ctx, e.ID); !a.commissionEnded {
		t.Fatal("the annulled election's commission did not end")
	}

	_, newMember, carla := s.student(t, ctx, "Carla Ficticia", "30111227")
	decodeCommission(t, s.designate(s.officer, carla))
	newMember, _ = s.confirm(t, newMember, s.enroll(t, newMember))
	next := decodeElection(t, s.create(newMember, encode(with(draft(), "name", "Elección 2027 bis"))), http.StatusCreated)
	if got := decodeElection(t, s.get(s.reader, currentPath), http.StatusOK); got.ID != next.ID {
		t.Fatalf("current = %s, want the new election %s", got.ID, next.ID)
	}
	if got := decodeElection(t, s.get(s.reader, latestPath), http.StatusOK); got.ID != next.ID {
		t.Fatalf("latest = %s, want the new election %s", got.ID, next.ID)
	}
}

// officersOf lists the tenant's officer positions and officer memberships as superuser.
func officersOf(t *testing.T, ctx context.Context, tenant string) []string {
	t.Helper()
	rows, err := tdb.Super.Query(ctx, `
		SELECT 'position:' || roster_member_id::text || ':' || position FROM officer_positions WHERE tenant_id = $1
		UNION ALL SELECT 'role:' || user_id::text || ':' || role FROM memberships WHERE tenant_id = $1
		ORDER BY 1`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// R7: annulling changes neither the Officers nor what the school admin may do.
func TestAnnulmentLeavesTheBoard(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	e := decodeElection(t, s.create(s.member, encode(draft())), http.StatusCreated)
	before := officersOf(t, ctx, s.tenant)
	decodeAnnulled(t, s.annul(s.member, e.ID, reasonBody(exampleReason)), http.StatusOK)
	if after := officersOf(t, ctx, s.tenant); !slices.Equal(after, before) {
		t.Fatalf("officers after = %v, want %v", after, before)
	}
	var st commissionStatusJSON
	if rec := s.get(s.admin, commissionPath); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &st) != nil || st.CanDesignate {
		t.Fatalf("school admin status: %d %s, want canDesignate false while there are officers", rec.Code, rec.Body)
	}
	_, _, someone := s.student(t, ctx, "Dora Ficticia", "30111228")
	wantResponse(t, s.designate(s.admin, someone), http.StatusForbidden, forbiddenBody)
}

// R8: no response about an annulled election carries vote counts, for anyone.
func TestAnnulledElectionShowsNoCounts(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	e := decodeElection(t, s.create(s.member, encode(soonDraft(s.clk.Now()))), http.StatusCreated)
	s.clk.Advance(3 * time.Hour) // open: votes could have been cast
	keys := []string{"annulment", "commission", "endsAt", "id", "mode", "name", "positions", "startsAt", "state", "submissionEndsAt", "submissionStartsAt"}
	wantKeys := func(t *testing.T, what string, rec *httptest.ResponseRecorder) {
		t.Helper()
		var m map[string]json.RawMessage
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &m) != nil {
			t.Fatalf("%s: %d %s", what, rec.Code, rec.Body)
		}
		var got []string
		for k := range m {
			got = append(got, k)
		}
		slices.Sort(got)
		if !slices.Equal(got, keys) {
			t.Fatalf("%s keys = %v, want exactly %v (no counts)", what, got, keys)
		}
		var a struct {
			Reason     string `json:"reason"`
			AnnulledAt string `json:"annulledAt"`
		}
		var am map[string]json.RawMessage
		if json.Unmarshal(m["annulment"], &am) != nil || len(am) != 2 || json.Unmarshal(m["annulment"], &a) != nil || a.Reason != exampleReason {
			t.Fatalf("%s annulment = %s, want only reason and annulledAt", what, m["annulment"])
		}
	}
	wantKeys(t, "annulment response", s.annul(s.member, e.ID, reasonBody(exampleReason)))
	for _, tc := range []struct{ name, token string }{
		{"student", s.reader}, {"officer", s.officer}, {"school admin", s.admin}, {"commission member", s.member},
	} {
		for _, path := range []string{electionsPath + "/" + e.ID, latestPath} {
			wantKeys(t, tc.name+" "+path, s.get(tc.token, path))
		}
	}
}

// R9: annulling needs a verified second factor, checked before the body is read.
func TestAnnulmentRequiresMFA(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	e := decodeElection(t, s.create(s.member, encode(draft())), http.StatusCreated)
	_, unverified, bea := s.student(t, ctx, "Bea Ficticia", "30111226")
	decodeCommission(t, s.designate(s.officer, s.ana, bea))
	probe := &readProbe{}
	req := httptest.NewRequest(http.MethodPost, annulmentPath(e.ID), probe)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: unverified})
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	wantResponse(t, rec, http.StatusForbidden, mfaBody)
	if probe.read {
		t.Fatal("request body read before the permission checks")
	}
	wantResponse(t, s.annul(unverified, e.ID, reasonBody(exampleReason)), http.StatusForbidden, mfaBody)
	s.wantNotAnnulled(t, ctx, e.ID)
}

// R10: an election of another school is not found, and stays as it was.
func TestAnnulmentAcrossSchools(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	e := decodeElection(t, s.create(s.member, encode(draft())), http.StatusCreated)
	g := newSchool(t, ctx)
	decodeElection(t, g.create(g.member, encode(draft())), http.StatusCreated)
	for _, id := range []string{e.ID, "00000000-0000-4000-8000-000000000000", "nope"} {
		t.Run(id, func(t *testing.T) {
			wantResponse(t, g.annul(g.member, id, reasonBody(exampleReason)), http.StatusNotFound, `{"error":"not_found"}`)
			s.wantNotAnnulled(t, ctx, e.ID)
		})
	}
}

// R7 and R11: a Veedor can annul only its school's election, with a public reason and identity.
func TestVeedorAnnulment(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	e := decodeElection(t, s.create(s.member, encode(draft())), http.StatusCreated)
	_, pending := s.signIn(t, ctx, "veedor")
	_, veedor, _, _ := s.signInMFA(t, ctx, "veedor")
	wantResponse(t, s.annul(pending, e.ID, reasonBody(exampleReason)), http.StatusForbidden, mfaBody)
	s.wantNotAnnulled(t, ctx, e.ID)
	wantResponse(t, s.annul(veedor, e.ID, reasonBody("")), http.StatusUnprocessableEntity, `{"error":"reason_required"}`)
	s.wantNotAnnulled(t, ctx, e.ID)
	other := newSchool(t, ctx)
	foreign := decodeElection(t, other.create(other.member, encode(soonDraft(other.clk.Now()))), http.StatusCreated)
	_, otherVeedor, _, _ := other.signInMFA(t, ctx, "veedor")
	wantResponse(t, s.annul(veedor, foreign.ID, reasonBody(exampleReason)), http.StatusNotFound, `{"error":"not_found"}`)
	other.clk.t = other.clk.t.Add(4 * time.Hour)
	wantResponse(t, other.annul(otherVeedor, foreign.ID, reasonBody(exampleReason)), http.StatusConflict, `{"error":"election_published"}`)
	other.wantNotAnnulled(t, ctx, foreign.ID)
	result := s.annul(veedor, e.ID, reasonBody(exampleReason))
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"annulledBy":"veedor"`) {
		t.Fatalf("annulment = %d %s", result.Code, result.Body)
	}
	for _, token := range []string{s.reader, veedor} {
		r := s.get(token, electionsPath+"/"+e.ID)
		if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"annulledBy":"veedor"`) || !strings.Contains(r.Body.String(), exampleReason) {
			t.Fatalf("public annulment = %d %s", r.Code, r.Body)
		}
	}
	wantResponse(t, s.annul(veedor, e.ID, reasonBody("second")), http.StatusConflict, `{"error":"election_annulled"}`)
}
