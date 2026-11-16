package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	commissionPath = "/api/electoral-commission"
	candidatesPath = "/api/electoral-commission/candidates"
	commissionIP   = "198.51.100.40"
	forbiddenBody  = `{"error":"forbidden"}`
	mfaBody        = `{"error":"mfa_required"}`
)

type commissionJSON struct {
	ID        string `json:"id"`
	CreatedAt string `json:"createdAt"`
	Members   []struct {
		RosterMemberID string `json:"rosterMemberId"`
		FullName       string `json:"fullName"`
	} `json:"members"`
}

type commissionStatusJSON struct {
	Commission   *commissionJSON `json:"commission"`
	IsMember     bool            `json:"isMember"`
	CanDesignate bool            `json:"canDesignate"`
}

// student creates a roster member of tenant with a student account linked to it and signs in.
func (f *fixture) student(t *testing.T, ctx context.Context, name, doc string) (userID, token, member string) {
	t.Helper()
	member = seedMember(t, ctx, f.tenant, name, doc)
	email := uniqueEmail("student")
	userID = f.seedUser(t, ctx, email, "student")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id = $1 WHERE tenant_id = $2 AND user_id = $3`,
		member, f.tenant, userID); err != nil {
		t.Fatal(err)
	}
	return userID, f.loginOK(t, email), member
}

func designateBody(ids ...string) string {
	b, _ := json.Marshal(map[string][]string{"rosterMemberIds": ids})
	return string(b)
}

func (f *fixture) designate(token string, ids ...string) *httptest.ResponseRecorder {
	return f.do(http.MethodPut, commissionPath, designateBody(ids...), commissionIP, token, nil)
}

// storedMembers reads the members of the tenant's current commission as superuser, sorted; nil when
// the tenant has no commission.
func storedMembers(t *testing.T, ctx context.Context, tenant string) []string {
	t.Helper()
	var n int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM electoral_commissions WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		return nil
	}
	var ids []string
	if err := tdb.Super.QueryRow(ctx, `
		SELECT coalesce(array_agg(cm.roster_member_id::text ORDER BY cm.roster_member_id::text), '{}')
		FROM electoral_commission_members cm JOIN electoral_commissions c ON c.tenant_id = cm.tenant_id AND c.id = cm.commission_id
		WHERE c.tenant_id = $1 AND c.ended_at IS NULL`, tenant).Scan(&ids); err != nil {
		t.Fatal(err)
	}
	return ids
}

func wantStoredMembers(t *testing.T, ctx context.Context, tenant string, want ...string) {
	t.Helper()
	got := storedMembers(t, ctx, tenant)
	if want == nil {
		if got != nil {
			t.Fatalf("stored commission members = %v, want no commission", got)
		}
		return
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("stored commission members = %v, want %v", got, want)
	}
}

func decodeCommission(t *testing.T, rec *httptest.ResponseRecorder) commissionJSON {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body)
	}
	wantNoStore(t, rec)
	var c commissionJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func memberIDsOf(c commissionJSON) []string {
	ids := []string{}
	for _, m := range c.Members {
		ids = append(ids, m.RosterMemberID)
	}
	return ids
}

// S1: who designates. Officers when the school has them; the school admin only when it has none.
// Every rejection writes nothing.
func TestCommissionDesignationPermissions(t *testing.T) {
	ctx := integration(t)

	t.Run("school without officers", func(t *testing.T) {
		f := newFixture(t, ctx)
		_, _, ana := f.student(t, ctx, "Ana Ficticia", "30111222")
		_, admin, _, _ := f.signInMFA(t, ctx, "school_admin")
		_, student := f.signIn(t, ctx, "student")
		_, outsider := f.signIn(t, ctx, "")
		wantResponse(t, f.designate("", ana), http.StatusUnauthorized, unauthBody)
		wantResponse(t, f.designate(student, ana), http.StatusForbidden, forbiddenBody)
		wantResponse(t, f.designate(outsider, ana), http.StatusForbidden, forbiddenBody)
		wantStoredMembers(t, ctx, f.tenant)
		c := decodeCommission(t, f.designate(admin, ana))
		if !slices.Equal(memberIDsOf(c), []string{ana}) || c.Members[0].FullName != "Ana Ficticia" || c.ID == "" ||
			c.CreatedAt != f.clk.Now().UTC().Format(time.RFC3339) {
			t.Fatalf("commission = %+v", c)
		}
		wantStoredMembers(t, ctx, f.tenant, ana)
	})

	t.Run("school with officers", func(t *testing.T) {
		f := newFixture(t, ctx)
		_, _, ana := f.student(t, ctx, "Ana Ficticia", "30111222")
		_, admin, _, _ := f.signInMFA(t, ctx, "school_admin")
		_, officer, _, _ := f.signInMFA(t, ctx, "officer")
		_, student := f.signIn(t, ctx, "student")
		wantResponse(t, f.designate(admin, ana), http.StatusForbidden, forbiddenBody)
		wantResponse(t, f.designate(student, ana), http.StatusForbidden, forbiddenBody)
		wantStoredMembers(t, ctx, f.tenant)
		decodeCommission(t, f.designate(officer, ana))
		wantStoredMembers(t, ctx, f.tenant, ana)
	})

	t.Run("an officer position alone counts as officers present", func(t *testing.T) {
		f := newFixture(t, ctx)
		_, _, ana := f.student(t, ctx, "Ana Ficticia", "30111222")
		unclaimed := seedMember(t, ctx, f.tenant, "Beto Ficticio", "30111223")
		if _, err := tdb.Super.Exec(ctx, `INSERT INTO officer_positions (tenant_id, roster_member_id, position) VALUES ($1, $2, 'Presidente')`,
			f.tenant, unclaimed); err != nil {
			t.Fatal(err)
		}
		_, admin, _, _ := f.signInMFA(t, ctx, "school_admin")
		wantResponse(t, f.designate(admin, ana), http.StatusForbidden, forbiddenBody)
		wantStoredMembers(t, ctx, f.tenant)
	})

	t.Run("a student holding an officer position designates", func(t *testing.T) {
		f := newFixture(t, ctx)
		_, _, ana := f.student(t, ctx, "Ana Ficticia", "30111222")
		_, token, presidente := f.student(t, ctx, "Carla Ficticia", "30111224")
		if _, err := tdb.Super.Exec(ctx, `INSERT INTO officer_positions (tenant_id, roster_member_id, position) VALUES ($1, $2, 'Presidente')`,
			f.tenant, presidente); err != nil {
			t.Fatal(err)
		}
		wantResponse(t, f.designate(token, ana), http.StatusForbidden, mfaBody)
		secret := f.enroll(t, token)
		token, _ = f.confirm(t, token, secret)
		decodeCommission(t, f.designate(token, ana))
		wantStoredMembers(t, ctx, f.tenant, ana)
	})

	t.Run("a commission member who is not an officer cannot replace it", func(t *testing.T) {
		f := newFixture(t, ctx)
		_, admin, _, _ := f.signInMFA(t, ctx, "school_admin")
		_, member, ana := f.student(t, ctx, "Ana Ficticia", "30111222")
		_, _, beto := f.student(t, ctx, "Beto Ficticio", "30111223")
		decodeCommission(t, f.designate(admin, ana))
		secret := f.enroll(t, member) // commission members are privileged: give them a second factor
		member, _ = f.confirm(t, member, secret)
		wantResponse(t, f.designate(member, beto), http.StatusForbidden, forbiddenBody)
		wantStoredMembers(t, ctx, f.tenant, ana)
	})
}

// S2: members are roster members of this school with an account; repeats are stored once; nothing is
// written on a rejection.
func TestCommissionDesignationMembers(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	_, _, ana := f.student(t, ctx, "Ana Ficticia", "30111222")
	_, _, beto := f.student(t, ctx, "Beto Ficticio", "30111223")
	noAccount := seedMember(t, ctx, f.tenant, "Carla Ficticia", "30111224")
	other := newTenant(t, ctx)
	foreign := seedMember(t, ctx, other, "Dora Ficticia", "30111225")
	linkAccount(t, ctx, other, foreign)

	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"empty list", designateBody(), `{"error":"empty_commission"}`, http.StatusUnprocessableEntity},
		{"null list", `{"rosterMemberIds":null}`, `{"error":"empty_commission"}`, http.StatusUnprocessableEntity},
		{"missing list", `{}`, `{"error":"empty_commission"}`, http.StatusUnprocessableEntity},
		{"roster member without account", designateBody(ana, noAccount), `{"error":"no_account"}`, http.StatusUnprocessableEntity},
		{"roster member of another school", designateBody(ana, foreign), `{"error":"not_found"}`, http.StatusNotFound},
		{"unknown id", designateBody("00000000-0000-4000-8000-000000000000"), `{"error":"not_found"}`, http.StatusNotFound},
		{"not a uuid", designateBody("nope"), `{"error":"not_found"}`, http.StatusNotFound},
		{"unknown field", `{"rosterMemberIds":[],"extra":1}`, `{"error":"bad_request"}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(http.MethodPut, commissionPath, tc.body, commissionIP, officer, nil)
			wantResponse(t, rec, tc.status, tc.want)
			wantNoStore(t, rec)
			wantStoredMembers(t, ctx, f.tenant)
			wantStoredMembers(t, ctx, other)
		})
	}

	t.Run("a repeated member is stored once", func(t *testing.T) {
		c := decodeCommission(t, f.designate(officer, beto, ana, beto))
		names := []string{c.Members[0].FullName, c.Members[1].FullName}
		if len(c.Members) != 2 || !slices.Equal(names, []string{"Ana Ficticia", "Beto Ficticio"}) {
			t.Fatalf("members = %+v, want Ana and Beto once, by name", c.Members)
		}
		wantStoredMembers(t, ctx, f.tenant, ana, beto)
	})
}

// S3: the designator may replace the members until the election opens; the rule is evaluated on each
// request.
func TestCommissionReplacement(t *testing.T) {
	ctx := integration(t)

	t.Run("replaced until the election opens, then frozen", func(t *testing.T) {
		f := newFixture(t, ctx)
		_, officer, _, _ := f.signInMFA(t, ctx, "officer")
		_, _, ana := f.student(t, ctx, "Ana Ficticia", "30111222")
		_, _, beto := f.student(t, ctx, "Beto Ficticio", "30111223")
		first := decodeCommission(t, f.designate(officer, ana))
		second := decodeCommission(t, f.designate(officer, beto))
		if second.ID != first.ID || !slices.Equal(memberIDsOf(second), []string{beto}) {
			t.Fatalf("replaced commission = %+v, want same id %s with only beto", second, first.ID)
		}
		wantStoredMembers(t, ctx, f.tenant, beto)
		// An election of this commission that already opened at the fixture's clock.
		now := f.clk.Now()
		if _, err := tdb.Super.Exec(ctx, `
			INSERT INTO elections (tenant_id, commission_id, name, mode, submission_starts_at, submission_ends_at, starts_at, ends_at)
			VALUES ($1, $2, 'Elección 2027', 'secret', $3, $4, $5, $6)`,
			f.tenant, first.ID, now.Add(-72*3600e9), now.Add(-48*3600e9), now.Add(-3600e9), now.Add(3600e9)); err != nil {
			t.Fatal(err)
		}
		wantResponse(t, f.designate(officer, ana), http.StatusConflict, `{"error":"election_frozen"}`)
		wantStoredMembers(t, ctx, f.tenant, beto)
	})

	t.Run("a school admin loses the right once the school has officers", func(t *testing.T) {
		f := newFixture(t, ctx)
		_, admin, _, _ := f.signInMFA(t, ctx, "school_admin")
		_, _, ana := f.student(t, ctx, "Ana Ficticia", "30111222")
		_, _, beto := f.student(t, ctx, "Beto Ficticio", "30111223")
		decodeCommission(t, f.designate(admin, ana))
		if _, err := tdb.Super.Exec(ctx, `INSERT INTO officer_positions (tenant_id, roster_member_id, position) VALUES ($1, $2, 'Presidente')`,
			f.tenant, beto); err != nil {
			t.Fatal(err)
		}
		wantResponse(t, f.designate(admin, beto), http.StatusForbidden, forbiddenBody)
		wantStoredMembers(t, ctx, f.tenant, ana)
	})
}

// S5: designating requires a verified second factor, checked after the role and before the body is
// read; nothing is written.
func TestCommissionDesignationRequiresMFA(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, _, ana := f.student(t, ctx, "Ana Ficticia", "30111222")
	_, officer := f.signIn(t, ctx, "officer")
	_, admin := f.signIn(t, ctx, "school_admin")
	for _, tc := range []struct{ name, token, want string }{
		{"officer", officer, mfaBody},
		// With officers present the school admin is refused by role before the second factor.
		{"school admin", admin, forbiddenBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &readProbe{}
			req := httptest.NewRequest(http.MethodPut, commissionPath, probe)
			req.AddCookie(&http.Cookie{Name: cookieName, Value: tc.token})
			rec := httptest.NewRecorder()
			f.h.ServeHTTP(rec, req)
			wantResponse(t, rec, http.StatusForbidden, tc.want)
			if probe.read {
				t.Fatal("request body read before the permission checks")
			}
			wantResponse(t, f.designate(tc.token, ana), http.StatusForbidden, tc.want)
			wantStoredMembers(t, ctx, f.tenant)
		})
	}
	t.Run("school admin without officers", func(t *testing.T) {
		g := newFixture(t, ctx)
		_, _, carla := g.student(t, ctx, "Carla Ficticia", "30111224")
		_, admin := g.signIn(t, ctx, "school_admin")
		wantResponse(t, g.designate(admin, carla), http.StatusForbidden, mfaBody)
		wantStoredMembers(t, ctx, g.tenant)
	})
	t.Run("candidates", func(t *testing.T) {
		wantResponse(t, f.do(http.MethodGet, candidatesPath, "", commissionIP, officer, nil), http.StatusForbidden, mfaBody)
	})
}

// S5: a member of the current Electoral commission is privileged: the public auth flow asks them for a
// second factor, and stops when they leave the commission.
func TestCommissionMemberMustUseSecondFactor(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	_, member, ana := f.student(t, ctx, "Ana Ficticia", "30111222")
	_, _, beto := f.student(t, ctx, "Beto Ficticio", "30111223")
	if mfa, _ := meJSON(t, f, member); mfa != "not_required" {
		t.Fatalf("before designation mfa = %q, want not_required", mfa)
	}
	decodeCommission(t, f.designate(officer, ana))
	if mfa, _ := meJSON(t, f, member); mfa != "enrollment_required" {
		t.Fatalf("commission member mfa = %q, want enrollment_required", mfa)
	}
	wantResponse(t, f.changePassword(member, password, "otra clave bastante larga"), http.StatusForbidden, mfaBody)
	secret := f.enroll(t, member)
	member, _ = f.confirm(t, member, secret)
	if mfa, _ := meJSON(t, f, member); mfa != "satisfied" {
		t.Fatalf("verified mfa = %q, want satisfied", mfa)
	}
	decodeCommission(t, f.designate(officer, beto))
	if mfa, _ := meJSON(t, f, member); mfa != "not_required" {
		t.Fatalf("after leaving the commission mfa = %q, want not_required", mfa)
	}
}

// PRESERVE: the two-factor state that /auth/me reports for existing roles is unchanged.
func TestMeMFAStateOfExistingRolesIsUnchanged(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	for role, want := range map[string]string{"student": "not_required", "officer": "enrollment_required",
		"school_admin": "enrollment_required", "": "not_required"} {
		_, token := f.signIn(t, ctx, role)
		if mfa, _ := meJSON(t, f, token); mfa != want {
			t.Fatalf("role %q: mfa = %q, want %q", role, mfa, want)
		}
	}
}

// S11: every member of the school reads the commission; the caller's relation comes with it.
func TestReadCommission(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, student, ana := f.student(t, ctx, "Ana Ficticia", "30111222")
	_, _, beto := f.student(t, ctx, "Beto Ficticio", "30111223")
	_, officer := f.signIn(t, ctx, "officer") // no second factor needed to read
	_, officerMFA, _, _ := f.signInMFA(t, ctx, "officer")
	_, admin := f.signIn(t, ctx, "school_admin")
	_, outsider := f.signIn(t, ctx, "")

	read := func(token string) commissionStatusJSON {
		t.Helper()
		rec := f.do(http.MethodGet, commissionPath, "", commissionIP, token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body)
		}
		wantNoStore(t, rec)
		var s commissionStatusJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if got := strings.TrimSpace(f.do(http.MethodGet, commissionPath, "", commissionIP, student, nil).Body.String()); got != `{"canDesignate":false,"commission":null,"isMember":false}` {
		t.Fatalf("before designation = %s", got)
	}
	decodeCommission(t, f.designate(officerMFA, ana, beto))
	for _, tc := range []struct {
		name               string
		token              string
		isMember, designer bool
	}{
		{"commission member", student, true, false},
		{"officer", officer, false, true},
		{"school admin while officers exist", admin, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := read(tc.token)
			if s.Commission == nil || !slices.Equal(memberIDsOf(*s.Commission), []string{ana, beto}) ||
				s.IsMember != tc.isMember || s.CanDesignate != tc.designer {
				t.Fatalf("status = %+v, want members ana and beto, isMember %v, canDesignate %v", s, tc.isMember, tc.designer)
			}
		})
	}
	t.Run("no membership", func(t *testing.T) {
		wantResponse(t, f.do(http.MethodGet, commissionPath, "", commissionIP, outsider, nil), http.StatusForbidden, forbiddenBody)
	})
	t.Run("no session", func(t *testing.T) {
		wantResponse(t, f.do(http.MethodGet, commissionPath, "", commissionIP, "", nil), http.StatusUnauthorized, unauthBody)
	})
	t.Run("another school sees its own (none)", func(t *testing.T) {
		g := newFixture(t, ctx)
		_, other := g.signIn(t, ctx, "student")
		if got := strings.TrimSpace(g.do(http.MethodGet, commissionPath, "", commissionIP, other, nil).Body.String()); got != `{"canDesignate":false,"commission":null,"isMember":false}` {
			t.Fatalf("other school = %s", got)
		}
	})
	t.Run("school admin without officers can designate", func(t *testing.T) {
		g := newFixture(t, ctx)
		_, a := g.signIn(t, ctx, "school_admin")
		if got := strings.TrimSpace(g.do(http.MethodGet, commissionPath, "", commissionIP, a, nil).Body.String()); got != `{"canDesignate":true,"commission":null,"isMember":false}` {
			t.Fatalf("admin without officers = %s", got)
		}
	})
}

// The candidates a designator can pick: roster members of this school with an account, by name.
func TestCommissionCandidates(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	_, student, ana := f.student(t, ctx, "Ana Ficticia", "30111222")
	seedMember(t, ctx, f.tenant, "Carla Ficticia", "30111224") // no account
	_, _, beto := f.student(t, ctx, "Beto Ficticio", "30111223")
	other := newTenant(t, ctx)
	linkAccount(t, ctx, other, seedMember(t, ctx, other, "Dora Ficticia", "30111225"))

	rec := f.do(http.MethodGet, candidatesPath, "", commissionIP, officer, nil)
	wantNoStore(t, rec)
	wantResponse(t, rec, http.StatusOK, fmt.Sprintf(`[{"fullName":"Ana Ficticia","rosterMemberId":"%s"},{"fullName":"Beto Ficticio","rosterMemberId":"%s"}]`, ana, beto))
	wantResponse(t, f.do(http.MethodGet, candidatesPath, "", commissionIP, student, nil), http.StatusForbidden, forbiddenBody)
	wantResponse(t, f.do(http.MethodGet, candidatesPath, "", commissionIP, "", nil), http.StatusUnauthorized, unauthBody)
}
