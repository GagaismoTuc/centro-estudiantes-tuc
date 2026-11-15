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
	electionsPath = "/api/elections"
	currentPath   = "/api/elections/current"
	electionIP    = "198.51.100.41"
)

// art is Argentina time (UTC-3).
var art = time.FixedZone("ART", -3*60*60)

// The S6 example: submission 1 to 5 March 2027 (ends at the start of 6 March), voting 10 March
// 08:00 to 18:00, Argentina time.
var (
	exSubStart = time.Date(2027, time.March, 1, 0, 0, 0, 0, art)
	exSubEnd   = time.Date(2027, time.March, 6, 0, 0, 0, 0, art)
	exStart    = time.Date(2027, time.March, 10, 8, 0, 0, 0, art)
	exEnd      = time.Date(2027, time.March, 10, 18, 0, 0, 0, art)
)

var defaultPositions = []string{"Presidente", "Vicepresidente", "Secretario", "Tesorero"}

// draft is the S6 example as a request body; tests edit or delete keys before encoding.
func draft() map[string]any {
	return map[string]any{
		"name":               "Elección 2027",
		"submissionStartsAt": exSubStart.Format(time.RFC3339),
		"submissionEndsAt":   exSubEnd.Format(time.RFC3339),
		"startsAt":           exStart.Format(time.RFC3339),
		"endsAt":             exEnd.Format(time.RFC3339),
	}
}

func encode(m map[string]any) string {
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func with(m map[string]any, kv ...any) map[string]any {
	for i := 0; i < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func without(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		delete(m, k)
	}
	return m
}

type electionJSON struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Mode               string   `json:"mode"`
	SubmissionStartsAt string   `json:"submissionStartsAt"`
	SubmissionEndsAt   string   `json:"submissionEndsAt"`
	StartsAt           string   `json:"startsAt"`
	EndsAt             string   `json:"endsAt"`
	Positions          []string `json:"positions"`
	State              string   `json:"state"`
	Commission         []struct {
		RosterMemberID string `json:"rosterMemberId"`
		FullName       string `json:"fullName"`
	} `json:"commission"`
}

func decodeElection(t *testing.T, rec *httptest.ResponseRecorder, status int) electionJSON {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, status, rec.Body)
	}
	wantNoStore(t, rec)
	var e electionJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

// school is a fixture with a designated commission: member (Ana) is on it with a two-factor
// session; presidente (Beto) holds the Presidente position but is not on it.
type school struct {
	*fixture
	member, ana string
	presidente  string
	officer     string
	admin       string
	reader      string // a student without a role on the commission
	outsider    string
}

func newSchool(t *testing.T, ctx context.Context) *school {
	t.Helper()
	f := newFixture(t, ctx)
	s := &school{fixture: f}
	_, s.officer, _, _ = f.signInMFA(t, ctx, "officer")
	_, s.admin, _, _ = f.signInMFA(t, ctx, "school_admin")
	_, presidente, beto := f.student(t, ctx, "Beto Ficticio", "30111223")
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO officer_positions (tenant_id, roster_member_id, position) VALUES ($1, $2, 'Presidente')`,
		f.tenant, beto); err != nil {
		t.Fatal(err)
	}
	secret := f.enroll(t, presidente)
	s.presidente, _ = f.confirm(t, presidente, secret)
	_, member, ana := f.student(t, ctx, "Ana Ficticia", "30111222")
	s.ana = ana
	decodeCommission(t, f.designate(s.officer, ana))
	secret = f.enroll(t, member)
	s.member, _ = f.confirm(t, member, secret)
	_, s.reader = f.signIn(t, ctx, "student")
	_, s.outsider = f.signIn(t, ctx, "")
	return s
}

func (s *school) create(token, body string) *httptest.ResponseRecorder {
	return s.do(http.MethodPost, electionsPath, body, electionIP, token, nil)
}

func (s *school) edit(token, id, body string) *httptest.ResponseRecorder {
	return s.do(http.MethodPut, electionsPath+"/"+id, body, electionIP, token, nil)
}

func (s *school) get(token, path string) *httptest.ResponseRecorder {
	return s.do(http.MethodGet, path, "", electionIP, token, nil)
}

// wantNoElection checks through the public API and the database that the school has no election.
func (s *school) wantNoElection(t *testing.T, ctx context.Context) {
	t.Helper()
	wantResponse(t, s.get(s.reader, currentPath), http.StatusNotFound, `{"error":"not_found"}`)
	var n int
	if err := tdb.Super.QueryRow(ctx, `SELECT (SELECT count(*) FROM elections WHERE tenant_id = $1) + (SELECT count(*) FROM election_positions WHERE tenant_id = $1)`,
		s.tenant).Scan(&n); err != nil || n != 0 {
		t.Fatalf("stored election rows = %d (%v), want 0", n, err)
	}
}

func utc(tm time.Time) string { return tm.UTC().Format(time.RFC3339) }

// S4: only a member of the current Electoral commission creates and edits the draft.
func TestElectionDraftPermissions(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	body := encode(draft())
	for _, tc := range []struct{ name, token, want string }{
		{"student", s.reader, forbiddenBody},
		{"officer not on the commission", s.officer, forbiddenBody},
		{"Presidente not on the commission", s.presidente, forbiddenBody},
		{"school admin", s.admin, forbiddenBody},
		{"no membership", s.outsider, forbiddenBody},
		{"no session", "", unauthBody},
	} {
		t.Run("create as "+tc.name, func(t *testing.T) {
			status := http.StatusForbidden
			if tc.token == "" {
				status = http.StatusUnauthorized
			}
			wantResponse(t, s.create(tc.token, body), status, tc.want)
			s.wantNoElection(t, ctx)
		})
	}
	e := decodeElection(t, s.create(s.member, body), http.StatusCreated)
	edited := encode(with(draft(), "name", "Hackeada"))
	for _, tc := range []struct{ name, token string }{
		{"student", s.reader}, {"officer not on the commission", s.officer},
		{"Presidente not on the commission", s.presidente}, {"school admin", s.admin}, {"no membership", s.outsider},
	} {
		t.Run("edit as "+tc.name, func(t *testing.T) {
			wantResponse(t, s.edit(tc.token, e.ID, edited), http.StatusForbidden, forbiddenBody)
			if got := decodeElection(t, s.get(s.reader, currentPath), http.StatusOK); got.Name != "Elección 2027" {
				t.Fatalf("name = %q, want unchanged", got.Name)
			}
		})
	}
	if got := decodeElection(t, s.edit(s.member, e.ID, edited), http.StatusOK); got.Name != "Hackeada" {
		t.Fatalf("commission member edit: name = %q", got.Name)
	}
}

// S5: creating and editing need a verified second factor, checked before the body is read.
func TestElectionDraftRequiresMFA(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	e := decodeElection(t, s.create(s.member, encode(draft())), http.StatusCreated)
	// A fresh session of a second commission member, without the second factor.
	_, unverified, bea := s.student(t, ctx, "Bea Ficticia", "30111226")
	decodeCommission(t, s.designate(s.officer, s.ana, bea))
	for _, tc := range []struct{ method, path string }{{http.MethodPost, electionsPath}, {http.MethodPut, electionsPath + "/" + e.ID}} {
		t.Run(tc.method, func(t *testing.T) {
			probe := &readProbe{}
			req := httptest.NewRequest(tc.method, tc.path, probe)
			req.AddCookie(&http.Cookie{Name: cookieName, Value: unverified})
			rec := httptest.NewRecorder()
			s.h.ServeHTTP(rec, req)
			wantResponse(t, rec, http.StatusForbidden, mfaBody)
			if probe.read {
				t.Fatal("request body read before the permission checks")
			}
			wantResponse(t, s.do(tc.method, tc.path, encode(with(draft(), "name", "Otra")), electionIP, unverified, nil), http.StatusForbidden, mfaBody)
		})
	}
	var n int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM elections WHERE tenant_id = $1 AND name = 'Elección 2027'`, s.tenant).Scan(&n); err != nil || n != 1 {
		t.Fatalf("elections = %d (%v), want only the unchanged one", n, err)
	}
}

// The S6 example times plus a fraction of 10^-10 s: below the nanosecond time.Parse keeps, so it
// must not be read as a whole minute.
const (
	subNanoStart    = "2027-03-10T08:00:00.0000000001-03:00"
	subNanoEnd      = "2027-03-10T18:00:00.0000000001-03:00"
	subNanoSubStart = "2027-03-01T00:00:00.0000000001-03:00"
	subNanoSubEnd   = "2027-03-06T00:00:00.0000000001-03:00"
)

// S6: the S6 example is stored as given; each invalid draft is refused with its field's code and
// nothing is stored.
func TestElectionDraftValidation(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	now := s.clk.Now()
	long := strings.Repeat("ñ", 121)
	for _, tc := range []struct {
		name string
		body map[string]any
		code string
	}{
		{"empty name", with(draft(), "name", ""), "invalid_name"},
		{"blank name", with(draft(), "name", "   "), "invalid_name"},
		{"name of 121 characters", with(draft(), "name", long), "invalid_name"},
		{"missing name", without(draft(), "name"), "invalid_name"},
		{"null name", with(draft(), "name", nil), "invalid_name"},
		{"end equal to start", with(draft(), "endsAt", exStart.Format(time.RFC3339)), "invalid_window"},
		{"end before start", with(draft(), "endsAt", exStart.Add(-time.Minute).Format(time.RFC3339)), "invalid_window"},
		{"start in the past", with(draft(), "submissionStartsAt", now.Add(-72*time.Hour).Format(time.RFC3339),
			"submissionEndsAt", now.Add(-48*time.Hour).Format(time.RFC3339), "startsAt", now.Add(-time.Minute).Format(time.RFC3339)), "invalid_window"},
		{"start with seconds", with(draft(), "startsAt", "2027-03-10T08:00:30-03:00"), "invalid_window"},
		{"end with a fraction of a second", with(draft(), "endsAt", "2027-03-10T18:00:00.5-03:00"), "invalid_window"},
		{"submission start with seconds", with(draft(), "submissionStartsAt", "2027-03-01T00:00:30-03:00"), "invalid_submission_period"},
		{"submission end with a fraction of a second", with(draft(), "submissionEndsAt", "2027-03-06T00:00:00.5-03:00"), "invalid_submission_period"},
		{"start with a fraction below a nanosecond", with(draft(), "startsAt", subNanoStart), "invalid_window"},
		{"end with a fraction below a nanosecond", with(draft(), "endsAt", subNanoEnd), "invalid_window"},
		{"submission start with a fraction below a nanosecond", with(draft(), "submissionStartsAt", subNanoSubStart), "invalid_submission_period"},
		{"submission end with a fraction below a nanosecond", with(draft(), "submissionEndsAt", subNanoSubEnd), "invalid_submission_period"},
		{"missing start", without(draft(), "startsAt"), "invalid_window"},
		{"null end", with(draft(), "endsAt", nil), "invalid_window"},
		{"start not a date-time", with(draft(), "startsAt", "10/03/2027 08:00"), "invalid_window"},
		{"submission ends after start", with(draft(), "submissionEndsAt", exStart.Add(time.Minute).Format(time.RFC3339)), "invalid_submission_period"},
		{"submission ends at the voting start", with(draft(), "submissionEndsAt", exStart.Format(time.RFC3339)), "invalid_submission_period"},
		{"submission ends when it starts", with(draft(), "submissionEndsAt", exSubStart.Format(time.RFC3339)), "invalid_submission_period"},
		{"missing submission start", without(draft(), "submissionStartsAt"), "invalid_submission_period"},
		{"null submission end", with(draft(), "submissionEndsAt", nil), "invalid_submission_period"},
		{"submission start not a date-time", with(draft(), "submissionStartsAt", "mañana"), "invalid_submission_period"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantResponse(t, s.create(s.member, encode(tc.body)), http.StatusUnprocessableEntity, fmt.Sprintf(`{"error":%q}`, tc.code))
			s.wantNoElection(t, ctx)
		})
	}
	t.Run("unknown field", func(t *testing.T) {
		wantResponse(t, s.create(s.member, encode(with(draft(), "extra", 1))), http.StatusBadRequest, `{"error":"bad_request"}`)
		s.wantNoElection(t, ctx)
	})

	rec := s.create(s.member, encode(with(draft(), "name", "  Elección 2027  ")))
	e := decodeElection(t, rec, http.StatusCreated)
	want := fmt.Sprintf(`{"commission":[{"fullName":"Ana Ficticia","rosterMemberId":"%s"}],"endsAt":"2027-03-10T21:00:00Z","id":"%s","mode":"secret","name":"Elección 2027","positions":["Presidente","Vicepresidente","Secretario","Tesorero"],"startsAt":"2027-03-10T11:00:00Z","state":"draft","submissionEndsAt":"2027-03-06T03:00:00Z","submissionStartsAt":"2027-03-01T03:00:00Z"}`,
		s.ana, e.ID)
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("created = %s\nwant      %s", got, want)
	}
	if got := strings.TrimSpace(s.get(s.reader, currentPath).Body.String()); got != want {
		t.Fatalf("read back = %s\nwant        %s", got, want)
	}

	t.Run("edits are validated the same way", func(t *testing.T) {
		wantResponse(t, s.edit(s.member, e.ID, encode(with(draft(), "name", " "))), http.StatusUnprocessableEntity, `{"error":"invalid_name"}`)
		wantResponse(t, s.edit(s.member, e.ID, encode(with(draft(), "endsAt", nil))), http.StatusUnprocessableEntity, `{"error":"invalid_window"}`)
		wantResponse(t, s.edit(s.member, e.ID, encode(with(draft(), "startsAt", "2027-03-10T08:00:30-03:00"))), http.StatusUnprocessableEntity, `{"error":"invalid_window"}`)
		for _, tc := range []struct{ field, value, code string }{
			{"startsAt", subNanoStart, "invalid_window"},
			{"endsAt", subNanoEnd, "invalid_window"},
			{"submissionStartsAt", subNanoSubStart, "invalid_submission_period"},
			{"submissionEndsAt", subNanoSubEnd, "invalid_submission_period"},
		} {
			wantResponse(t, s.edit(s.member, e.ID, encode(with(draft(), tc.field, tc.value))), http.StatusUnprocessableEntity, fmt.Sprintf(`{"error":%q}`, tc.code))
		}
		if got := strings.TrimSpace(s.get(s.reader, currentPath).Body.String()); got != want {
			t.Fatalf("after rejected edits = %s, want unchanged", got)
		}
		// Ten zero fraction digits are no fraction: still a whole minute.
		if rec := s.edit(s.member, e.ID, encode(with(draft(), "startsAt", "2027-03-10T08:00:00.0000000000-03:00"))); rec.Code != http.StatusOK {
			t.Fatalf("zero fraction: status %d, body %s; want 200", rec.Code, rec.Body)
		}
		if got := strings.TrimSpace(s.get(s.reader, currentPath).Body.String()); got != want {
			t.Fatalf("after zero-fraction edit = %s, want unchanged", got)
		}
		name := strings.Repeat("ñ", 120)
		if got := decodeElection(t, s.edit(s.member, e.ID, encode(with(draft(), "name", name))), http.StatusOK); got.Name != name {
			t.Fatalf("a name of 120 characters is valid; got %q", got.Name)
		}
	})
}

// S7: default positions, custom positions and their validation; the commission adds, removes and
// renames positions while the election is a draft.
func TestElectionPositions(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	for _, tc := range []struct {
		name      string
		positions any
	}{
		{"empty list", []string{}},
		{"null list", nil},
		{"blank name", []string{"Presidente", "  "}},
		{"repeated name ignoring case", []string{"Presidente", "presidente"}},
		{"name of 121 characters", []string{strings.Repeat("a", 121)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantResponse(t, s.create(s.member, encode(with(draft(), "positions", tc.positions))), http.StatusUnprocessableEntity, `{"error":"invalid_positions"}`)
			s.wantNoElection(t, ctx)
		})
	}
	e := decodeElection(t, s.create(s.member, encode(draft())), http.StatusCreated)
	if !slices.Equal(e.Positions, defaultPositions) {
		t.Fatalf("positions = %v, want the defaults in order", e.Positions)
	}
	changed := []string{"Presidenta", "Tesorero", "Vocal"} // rename, remove, reorder and add
	if got := decodeElection(t, s.edit(s.member, e.ID, encode(with(draft(), "positions", changed))), http.StatusOK); !slices.Equal(got.Positions, changed) {
		t.Fatalf("edited positions = %v, want %v", got.Positions, changed)
	}
	if got := decodeElection(t, s.get(s.reader, electionsPath+"/"+e.ID), http.StatusOK); !slices.Equal(got.Positions, changed) {
		t.Fatalf("read positions = %v, want %v", got.Positions, changed)
	}
	wantResponse(t, s.edit(s.member, e.ID, encode(with(draft(), "positions", []string{"Vocal", "VOCAL"}))), http.StatusUnprocessableEntity, `{"error":"invalid_positions"}`)
	if got := decodeElection(t, s.edit(s.member, e.ID, encode(draft())), http.StatusOK); !slices.Equal(got.Positions, defaultPositions) {
		t.Fatalf("edit without positions = %v, want the defaults", got.Positions)
	}

	t.Run("only while a draft", func(t *testing.T) {
		g := newSchool(t, ctx)
		now := g.clk.Now()
		soon := with(draft(), "submissionStartsAt", now.Add(time.Hour).Format(time.RFC3339),
			"submissionEndsAt", now.Add(2*time.Hour).Format(time.RFC3339), "startsAt", now.Add(3*time.Hour).Format(time.RFC3339),
			"endsAt", now.Add(4*time.Hour).Format(time.RFC3339))
		e := decodeElection(t, g.create(g.member, encode(soon)), http.StatusCreated)
		g.clk.Advance(time.Hour) // submission starts
		wantResponse(t, g.edit(g.member, e.ID, encode(with(soon, "positions", changed))), http.StatusConflict, `{"error":"election_not_draft"}`)
		got := decodeElection(t, g.get(g.reader, currentPath), http.StatusOK)
		if got.State != "submission" || !slices.Equal(got.Positions, defaultPositions) {
			t.Fatalf("election = %+v, want unchanged in submission", got)
		}
	})
}

// S8: one election in progress per school, also under concurrent requests.
func TestElectionInProgress(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	first := decodeElection(t, s.create(s.member, encode(draft())), http.StatusCreated)
	wantResponse(t, s.create(s.member, encode(with(draft(), "name", "Otra"))), http.StatusConflict, `{"error":"election_in_progress"}`)
	if got := decodeElection(t, s.get(s.reader, currentPath), http.StatusOK); got.ID != first.ID || got.Name != "Elección 2027" {
		t.Fatalf("current = %+v, want the first", got)
	}

	t.Run("two simultaneous requests", func(t *testing.T) {
		g := newSchool(t, ctx)
		recs := make([]*httptest.ResponseRecorder, 2)
		var start, done sync.WaitGroup
		start.Add(1)
		for i := range recs {
			done.Add(1)
			go func() {
				defer done.Done()
				start.Wait()
				recs[i] = g.create(g.member, encode(draft()))
			}()
		}
		start.Done()
		done.Wait()
		codes := []int{recs[0].Code, recs[1].Code}
		slices.Sort(codes)
		if !slices.Equal(codes, []int{http.StatusCreated, http.StatusConflict}) {
			t.Fatalf("statuses = %v, want one 201 and one 409", codes)
		}
		for _, r := range recs {
			if r.Code == http.StatusConflict {
				wantResponse(t, r, http.StatusConflict, `{"error":"election_in_progress"}`)
			}
		}
		var n int
		if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM elections WHERE tenant_id = $1`, g.tenant).Scan(&n); err != nil || n != 1 {
			t.Fatalf("elections = %d (%v), want 1", n, err)
		}
	})
}

// S9: only secret voting.
func TestElectionModeIsSecretOnly(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	for _, mode := range []any{"nominal", nil, "Secret", ""} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			wantResponse(t, s.create(s.member, encode(with(draft(), "mode", mode))), http.StatusUnprocessableEntity, `{"error":"mode_not_supported"}`)
			s.wantNoElection(t, ctx)
		})
	}
	e := decodeElection(t, s.create(s.member, encode(with(draft(), "mode", "secret"))), http.StatusCreated)
	if e.Mode != "secret" {
		t.Fatalf("mode = %q, want secret", e.Mode)
	}
	wantResponse(t, s.edit(s.member, e.ID, encode(with(draft(), "mode", "nominal"))), http.StatusUnprocessableEntity, `{"error":"mode_not_supported"}`)
}

// S10: the state comes from the injected clock against the dates, at request time.
func TestElectionStateFollowsTheClock(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	e := decodeElection(t, s.create(s.member, encode(draft())), http.StatusCreated)
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{exSubStart.Add(-time.Second), "draft"},
		{exSubStart, "submission"},
		{exSubEnd, "scheduled"},
		{time.Date(2027, time.March, 7, 12, 0, 0, 0, art), "scheduled"},
		{exStart, "open"},
		{exEnd.Add(-time.Second), "open"},
		{exEnd, "closed"},
	} {
		t.Run(tc.at.Format(time.RFC3339), func(t *testing.T) {
			s.clk.t = tc.at
			_, token := s.signIn(t, ctx, "student") // sessions last 12 hours: sign in at the new time
			for _, path := range []string{currentPath, electionsPath + "/" + e.ID} {
				if got := decodeElection(t, s.get(token, path), http.StatusOK); got.State != tc.want {
					t.Fatalf("%s state = %q, want %q", path, got.State, tc.want)
				}
			}
		})
	}
}

// S11: every member of the school reads the election; others get 403 or 404.
func TestReadElection(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	wantResponse(t, s.get(s.reader, currentPath), http.StatusNotFound, `{"error":"not_found"}`)
	e := decodeElection(t, s.create(s.member, encode(draft())), http.StatusCreated)
	_, unverifiedOfficer := s.signIn(t, ctx, "officer") // reading needs no second factor
	for _, tc := range []struct{ name, token string }{
		{"student", s.reader}, {"officer", unverifiedOfficer}, {"school admin", s.admin}, {"commission member", s.member},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, path := range []string{currentPath, electionsPath + "/" + e.ID} {
				got := decodeElection(t, s.get(tc.token, path), http.StatusOK)
				if got.ID != e.ID || got.Name != "Elección 2027" || got.Mode != "secret" || got.State != "draft" ||
					got.StartsAt != utc(exStart) || got.EndsAt != utc(exEnd) || got.SubmissionStartsAt != utc(exSubStart) ||
					got.SubmissionEndsAt != utc(exSubEnd) || !slices.Equal(got.Positions, defaultPositions) ||
					len(got.Commission) != 1 || got.Commission[0].RosterMemberID != s.ana || got.Commission[0].FullName != "Ana Ficticia" {
					t.Fatalf("%s = %+v", path, got)
				}
			}
		})
	}
	t.Run("no membership", func(t *testing.T) {
		for _, path := range []string{currentPath, electionsPath + "/" + e.ID} {
			wantResponse(t, s.get(s.outsider, path), http.StatusForbidden, forbiddenBody)
		}
	})
	t.Run("no session", func(t *testing.T) {
		wantResponse(t, s.get("", electionsPath+"/"+e.ID), http.StatusUnauthorized, unauthBody)
	})
	t.Run("another school", func(t *testing.T) {
		g := newFixture(t, ctx)
		_, other := g.signIn(t, ctx, "student")
		for _, path := range []string{currentPath, electionsPath + "/" + e.ID, electionsPath + "/00000000-0000-4000-8000-000000000000", electionsPath + "/nope"} {
			wantResponse(t, g.do(http.MethodGet, path, "", electionIP, other, nil), http.StatusNotFound, `{"error":"not_found"}`)
		}
	})
	t.Run("edit an election of another school", func(t *testing.T) {
		g := newSchool(t, ctx)
		wantResponse(t, g.edit(g.member, e.ID, encode(with(draft(), "name", "Hackeada"))), http.StatusNotFound, `{"error":"not_found"}`)
		if got := decodeElection(t, s.get(s.reader, currentPath), http.StatusOK); got.Name != "Elección 2027" {
			t.Fatalf("name = %q, want unchanged", got.Name)
		}
	})
}
