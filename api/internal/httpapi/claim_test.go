package httpapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/httpapi"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/roster"
)

const invalidCodeBody = `{"error":"invalid_code"}`

func (f *fixture) claim(code, email, pw, ip string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"code": code, "email": email, "password": pw})
	return f.do(http.MethodPost, "/api/auth/claim", string(body), ip, "", nil)
}

func (f *fixture) redeem(code, token string) *httptest.ResponseRecorder {
	return f.do(http.MethodPost, "/api/auth/redeem", fmt.Sprintf(`{"code":%q}`, code), "192.0.2.90", token, nil)
}

func meBody(user, email, tenant, tenantName, role, mfa string, verified bool) string {
	name, _ := json.Marshal(tenantName)
	return fmt.Sprintf(`{"email":"%s","emailVerified":%t,"mfa":"%s","role":"%s","tenantId":"%s","tenantName":%s,"userId":"%s"}`,
		email, verified, mfa, role, tenant, name, user)
}

func userByEmail(t *testing.T, ctx context.Context, email string) (id, hash string) {
	t.Helper()
	_ = tdb.Super.QueryRow(ctx, `SELECT id::text, password_hash FROM users WHERE lower(email) = lower($1)`, email).Scan(&id, &hash)
	return id, hash
}

// memberAccounts returns "user|role|tenant" for every membership linked to the roster member.
func memberAccounts(t *testing.T, ctx context.Context, member string) []string {
	t.Helper()
	var out []string
	if err := tdb.Super.QueryRow(ctx, `SELECT coalesce(array_agg(user_id || '|' || role || '|' || tenant_id), '{}')
		FROM memberships WHERE roster_member_id = $1`, member).Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func sessionTenant(t *testing.T, ctx context.Context, token string) string {
	t.Helper()
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	sum := sha256.Sum256(raw)
	var tenant *string
	if err := tdb.Super.QueryRow(ctx, `SELECT tenant_id::text FROM sessions WHERE token_hash = $1`, sum[:]).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if tenant == nil {
		return ""
	}
	return *tenant
}

// fmtRows prints code rows by value (codeRow holds pointers).
func fmtRows(rows []codeRow) string {
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%x %v %v ", r.Hash, r.Used != nil && !r.Used.IsZero(), r.Revoked != nil)
		if r.Used != nil {
			b.WriteString(r.Used.String())
		}
	}
	return b.String()
}

// wantCodeUsed fails unless the member's only code has the given used state.
func wantCodeUsed(t *testing.T, ctx context.Context, member string, used *time.Time) {
	t.Helper()
	rows := codeRows(t, ctx, member)
	if len(rows) != 1 || (used == nil) != (rows[0].Used == nil) || (used != nil && !rows[0].Used.Equal(*used)) {
		t.Fatalf("code rows = %+v, want one with used_at = %v", rows, used)
	}
}

func TestClaimCreatesLinkedStudentAccount(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	member := seedMember(t, ctx, f.tenant, "Ana Acosta", "30111222")
	code := seedCode(t, ctx, f.tenant, member, "active", f.clk.Now())
	email := uniqueEmail("claim")
	const pw = "ñandú12345" // 10 characters, 12 bytes

	// What a person types: lowercase, spaces instead of hyphens, mixed-case email.
	rec := f.claim(strings.ToLower(strings.ReplaceAll(code, "-", " ")), " "+strings.ToUpper(email)+" ", pw, "192.0.2.1")

	userID, hash := userByEmail(t, ctx, email)
	wantResponse(t, rec, http.StatusCreated, meBody(userID, email, f.tenant, f.tenantName, "student", "not_required", false))
	wantNoStore(t, rec)
	m := setCookieRE.FindStringSubmatch(rec.Header().Get("Set-Cookie"))
	if m == nil {
		t.Fatalf("Set-Cookie = %q, want match for %s", rec.Header().Get("Set-Cookie"), setCookieRE)
	}
	var stored string
	_ = tdb.Super.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&stored)
	if ok, err := auth.VerifyPassword(hash, pw); !ok || err != nil || stored != email {
		t.Fatalf("stored email %q, password verifies = %v (%v); want %q and true", stored, ok, err, email)
	}
	if got := memberAccounts(t, ctx, member); len(got) != 1 || got[0] != userID+"|student|"+f.tenant {
		t.Fatalf("memberships = %q, want one student membership for %s", got, userID)
	}
	now := f.clk.Now()
	wantCodeUsed(t, ctx, member, &now)
	if n := sessionCount(t, ctx, userID); n != 1 || sessionTenant(t, ctx, m[1]) != f.tenant {
		t.Fatalf("sessions = %d, want 1 in the code's tenant", n)
	}
	wantResponse(t, f.do(http.MethodGet, "/api/auth/me", "", "192.0.2.1", m[1], nil),
		http.StatusOK, meBody(userID, email, f.tenant, f.tenantName, "student", "not_required", false))

	t.Run("the same code again is dead", func(t *testing.T) {
		other := uniqueEmail("claim-again")
		rec := f.claim(code, other, password, "192.0.2.1")
		wantResponse(t, rec, http.StatusUnprocessableEntity, invalidCodeBody)
		wantNoStore(t, rec)
		if id, _ := userByEmail(t, ctx, other); id != "" {
			t.Fatal("a second user was created")
		}
		if got := memberAccounts(t, ctx, member); len(got) != 1 {
			t.Fatalf("memberships = %q, want still one", got)
		}
	})
}

func TestClaimRejectsDeadCodesIdentically(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	now := f.clk.Now()
	for i, state := range []string{"unknown", "used", "revoked", "expired", "member already linked"} {
		t.Run(state, func(t *testing.T) {
			member := seedMember(t, ctx, f.tenant, "Alumno "+state, fmt.Sprint(32000000+i))
			var code string
			switch state {
			case "unknown":
				code = "ABCD-EFGH-JKMN"
			case "member already linked":
				code = seedCode(t, ctx, f.tenant, member, "active", now)
				linkAccount(t, ctx, f.tenant, member)
			default:
				code = seedCode(t, ctx, f.tenant, member, state, now)
			}
			before := codeRows(t, ctx, member)
			accounts := memberAccounts(t, ctx, member)
			email := uniqueEmail("dead")

			rec := f.claim(code, email, password, fmt.Sprintf("192.0.2.%d", 10+i))

			wantResponse(t, rec, http.StatusUnprocessableEntity, invalidCodeBody)
			if sc := rec.Header().Get("Set-Cookie"); sc != "" {
				t.Fatalf("Set-Cookie = %q, want none", sc)
			}
			if id, _ := userByEmail(t, ctx, email); id != "" {
				t.Fatal("user created")
			}
			if after := codeRows(t, ctx, member); fmtRows(after) != fmtRows(before) {
				t.Fatalf("code rows changed: %+v -> %+v", before, after)
			}
			if got := memberAccounts(t, ctx, member); fmt.Sprint(got) != fmt.Sprint(accounts) {
				t.Fatalf("memberships changed: %q -> %q", accounts, got)
			}
		})
	}
}

func TestClaimWithRegisteredEmailKeepsCodeUsable(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	member := seedMember(t, ctx, f.tenant, "Ana Acosta", "30111223")
	code := seedCode(t, ctx, f.tenant, member, "active", f.clk.Now())
	taken := uniqueEmail("taken")
	takenID := f.seedUser(t, ctx, taken, "")
	_, takenHash := userByEmail(t, ctx, taken)

	rec := f.claim(code, strings.ToUpper(taken), password, "192.0.2.20")

	wantResponse(t, rec, http.StatusConflict, `{"error":"email_in_use"}`)
	wantNoStore(t, rec)
	if sc := rec.Header().Get("Set-Cookie"); sc != "" {
		t.Fatalf("Set-Cookie = %q, want none", sc)
	}
	if _, h := userByEmail(t, ctx, taken); h != takenHash || sessionCount(t, ctx, takenID) != 0 {
		t.Fatal("existing user changed or got a session")
	}
	if got := memberAccounts(t, ctx, member); len(got) != 0 {
		t.Fatalf("memberships = %q, want none", got)
	}
	wantCodeUsed(t, ctx, member, nil)

	// The code still works with another email, and 128 characters is the longest password.
	fresh := uniqueEmail("fresh")
	rec = f.claim(code, fresh, strings.Repeat("ñ", 128), "192.0.2.20")
	freshID, _ := userByEmail(t, ctx, fresh)
	wantResponse(t, rec, http.StatusCreated, meBody(freshID, fresh, f.tenant, f.tenantName, "student", "not_required", false))
}

func TestClaimValidatesInputBeforeTouchingTheDatabase(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	member := seedMember(t, ctx, f.tenant, "Ana Acosta", "30111224")
	code := seedCode(t, ctx, f.tenant, member, "active", f.clk.Now())
	email := uniqueEmail("valid")
	long := strings.Repeat("a", 64) + "@" + strings.Repeat("b", 186) + ".com" // 255 characters
	enc := func(c, e, p string) string {
		b, _ := json.Marshal(map[string]string{"code": c, "email": e, "password": p})
		return string(b)
	}
	for _, tc := range []struct{ name, body, want string }{
		{"email without @", enc(code, "ana.example.com", password), `{"error":"invalid_email"}`},
		{"email with display name", enc(code, "Ana <"+email+">", password), `{"error":"invalid_email"}`},
		{"email over 254 characters", enc(code, long, password), `{"error":"invalid_email"}`},
		{"missing email", fmt.Sprintf(`{"code":%q,"password":%q}`, code, password), `{"error":"invalid_email"}`},
		{"9-character password", enc(code, email, "123456789"), `{"error":"weak_password"}`},
		{"9 multibyte characters", enc(code, email, strings.Repeat("ñ", 9)), `{"error":"weak_password"}`},
		{"129-character password", enc(code, email, strings.Repeat("a", 129)), `{"error":"weak_password"}`},
		{"short code", enc(code[:13], email, password), invalidCodeBody},
		{"long code", enc(code+"0", email, password), invalidCodeBody},
		{"code outside the alphabet", enc("UUUU-UUUU-UUUU", email, password), invalidCodeBody},
		{"unknown field", strings.TrimSuffix(enc(code, email, password), "}") + `,"role":"school_admin"}`, `{"error":"bad_request"}`},
		{"trailing data", enc(code, email, password) + `{}`, `{"error":"bad_request"}`},
		{"not JSON", `code=` + code, `{"error":"bad_request"}`},
		{"body over 4 KiB", enc(code, email, strings.Repeat("a", 4<<10)), `{"error":"bad_request"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(http.MethodPost, "/api/auth/claim", tc.body, "192.0.2.30", "", nil)
			status := http.StatusUnprocessableEntity
			if strings.Contains(tc.want, "bad_request") {
				status = http.StatusBadRequest
			}
			wantResponse(t, rec, status, tc.want)
			wantNoStore(t, rec)
			if id, _ := userByEmail(t, ctx, email); id != "" {
				t.Fatal("user created")
			}
			wantCodeUsed(t, ctx, member, nil)
		})
	}
}

func TestClaimIsRateLimitedPerIP(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	member := seedMember(t, ctx, f.tenant, "Ana Acosta", "30111225")
	code := seedCode(t, ctx, f.tenant, member, "active", f.clk.Now())
	taken := uniqueEmail("rate-taken")
	f.seedUser(t, ctx, taken, "")
	const ip = "192.0.2.40"

	// Input-validation failures do not count; email_in_use does (it would otherwise allow email probing).
	for range 3 {
		wantResponse(t, f.claim(code, taken, password, ip), http.StatusConflict, `{"error":"email_in_use"}`)
		wantResponse(t, f.claim(code, "nope", password, ip), http.StatusUnprocessableEntity, `{"error":"invalid_email"}`)
	}
	for i := range 47 { // 3 email_in_use + 47 invalid codes = the per-IP limit of 50 (school NAT)
		bad := "ABCD-EFGH-JKMN"
		if i%2 == 1 {
			bad = "short" // malformed codes count too
		}
		wantResponse(t, f.claim(bad, uniqueEmail("rate"), password, ip), http.StatusUnprocessableEntity, invalidCodeBody)
	}
	email := uniqueEmail("rate-ok")
	rec := f.claim(code, email, password, ip)
	wantResponse(t, rec, http.StatusTooManyRequests, `{"error":"too_many_attempts"}`)
	wantNoStore(t, rec)
	if id, _ := userByEmail(t, ctx, email); id != "" {
		t.Fatal("user created while limited")
	}
	wantCodeUsed(t, ctx, member, nil)

	// Another IP is not affected; the limited IP recovers after the window.
	wantResponse(t, f.claim("ABCD-EFGH-JKMN", email, password, "192.0.2.41"), http.StatusUnprocessableEntity, invalidCodeBody)
	f.clk.Advance(15 * time.Minute)
	if rec := f.claim(code, email, password, ip); rec.Code != http.StatusCreated {
		t.Fatalf("after the window: status = %d (%s), want 201", rec.Code, rec.Body)
	}
}

func TestConcurrentClaims(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	race := func(n int, req func(i int) *httptest.ResponseRecorder) map[int]int {
		var wg sync.WaitGroup
		recs := make([]*httptest.ResponseRecorder, n)
		for i := range n {
			wg.Go(func() { recs[i] = req(i) })
		}
		wg.Wait()
		out := map[int]int{}
		for _, r := range recs {
			out[r.Code]++
		}
		return out
	}
	for iter := range 5 {
		t.Run(fmt.Sprintf("same code %d", iter), func(t *testing.T) {
			member := seedMember(t, ctx, f.tenant, "Alumno", fmt.Sprint(33000000+iter))
			code := seedCode(t, ctx, f.tenant, member, "active", f.clk.Now())
			got := race(4, func(i int) *httptest.ResponseRecorder {
				return f.claim(code, uniqueEmail("race"), password, fmt.Sprintf("192.0.2.%d", 100+i))
			})
			if got[http.StatusCreated] != 1 || got[http.StatusUnprocessableEntity] != 3 {
				t.Fatalf("statuses = %v, want one 201 and three 422", got)
			}
			if accounts := memberAccounts(t, ctx, member); len(accounts) != 1 {
				t.Fatalf("memberships = %q, want one", accounts)
			}
		})
		t.Run(fmt.Sprintf("same email %d", iter), func(t *testing.T) {
			members := []string{seedMember(t, ctx, f.tenant, "Uno", fmt.Sprint(34000000+iter)),
				seedMember(t, ctx, f.tenant, "Dos", fmt.Sprint(35000000+iter))}
			codes := []string{seedCode(t, ctx, f.tenant, members[0], "active", f.clk.Now()),
				seedCode(t, ctx, f.tenant, members[1], "active", f.clk.Now())}
			email := uniqueEmail("race-email")
			got := race(2, func(i int) *httptest.ResponseRecorder {
				return f.claim(codes[i], email, password, fmt.Sprintf("192.0.2.%d", 110+i))
			})
			if got[http.StatusCreated] != 1 || got[http.StatusConflict] != 1 {
				t.Fatalf("statuses = %v, want one 201 and one 409", got)
			}
			if n := len(memberAccounts(t, ctx, members[0])) + len(memberAccounts(t, ctx, members[1])); n != 1 {
				t.Fatalf("linked members = %d, want 1", n)
			}
		})
	}
}

func TestRedeemAddsMembershipAndSwitchesTenant(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	userID, token := f.signIn(t, ctx, "officer") // already in f.tenant
	var email string
	_ = tdb.Super.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&email)
	oldExpiry := sessionExpiry(t, ctx, token)
	f.clk.Advance(2 * time.Hour)
	other := newTenant(t, ctx)
	member := seedMember(t, ctx, other, "Ana Acosta", "30111226")
	code := seedCode(t, ctx, other, member, "active", f.clk.Now())

	rec := f.redeem(strings.ToLower(code), token)

	wantResponse(t, rec, http.StatusOK, meBody(userID, email, other, "TestRedeemAddsMembershipAndSwitchesTenant B", "student", "not_required", false))
	wantNoStore(t, rec)
	// The session rotates: new token, old row gone, same absolute expiry (10 h left).
	old := token
	token = cookieWithMaxAge(t, rec, 10*3600)
	if token == old {
		t.Fatal("session token was not rotated")
	}
	wantResponse(t, f.do(http.MethodGet, "/api/auth/me", "", "192.0.2.90", old, nil), http.StatusUnauthorized, unauthBody)
	if got := sessionExpiry(t, ctx, token); !got.Equal(oldExpiry) {
		t.Fatalf("rotated session expires_at = %s, want %s", got, oldExpiry)
	}
	if got := memberAccounts(t, ctx, member); len(got) != 1 || got[0] != userID+"|student|"+other {
		t.Fatalf("memberships = %q, want one student membership for %s", got, userID)
	}
	now := f.clk.Now()
	wantCodeUsed(t, ctx, member, &now)
	if got := sessionTenant(t, ctx, token); got != other || sessionCount(t, ctx, userID) != 1 {
		t.Fatalf("session tenant = %s, want %s (one session)", got, other)
	}
	wantResponse(t, f.do(http.MethodGet, "/api/auth/me", "", "192.0.2.90", token, nil),
		http.StatusOK, meBody(userID, email, other, "TestRedeemAddsMembershipAndSwitchesTenant B", "student", "not_required", false))

	t.Run("already a member keeps the code usable", func(t *testing.T) {
		second := seedMember(t, ctx, other, "Beto Bravo", "30111227")
		code := seedCode(t, ctx, other, second, "active", f.clk.Now())
		rec := f.redeem(code, token)
		wantResponse(t, rec, http.StatusConflict, `{"error":"already_member"}`)
		wantNoStore(t, rec)
		wantCodeUsed(t, ctx, second, nil)
		if got := memberAccounts(t, ctx, second); len(got) != 0 {
			t.Fatalf("memberships = %q, want none", got)
		}
	})
	t.Run("dead code", func(t *testing.T) {
		wantResponse(t, f.redeem(code, token), http.StatusUnprocessableEntity, invalidCodeBody)
	})
	t.Run("strict JSON", func(t *testing.T) {
		rec := f.do(http.MethodPost, "/api/auth/redeem", `{"code":"x","tenant":"y"}`, "192.0.2.90", token, nil)
		wantResponse(t, rec, http.StatusBadRequest, `{"error":"bad_request"}`)
	})
}

func TestRedeemRequiresSession(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	member := seedMember(t, ctx, f.tenant, "Ana Acosta", "30111228")
	code := seedCode(t, ctx, f.tenant, member, "active", f.clk.Now())

	rec := f.redeem(code, "")

	wantResponse(t, rec, http.StatusUnauthorized, unauthBody)
	wantNoStore(t, rec)
	wantCodeUsed(t, ctx, member, nil)
	if got := memberAccounts(t, ctx, member); len(got) != 0 {
		t.Fatalf("memberships = %q, want none", got)
	}
}

func TestCrossOriginClaimIsRejected(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	member := seedMember(t, ctx, f.tenant, "Ana Acosta", "30111229")
	code := seedCode(t, ctx, f.tenant, member, "active", f.clk.Now())
	body, _ := json.Marshal(map[string]string{"code": code, "email": uniqueEmail("csrf"), "password": password})
	for _, path := range []string{"/api/auth/claim", "/api/auth/redeem"} {
		rec := f.do(http.MethodPost, path, string(body), "192.0.2.95", "",
			map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403", path, rec.Code)
		}
	}
	wantCodeUsed(t, ctx, member, nil)
}

// Holding one valid code must not allow unlimited probing of which emails already have an account.
func TestClaimEmailProbesAreLimited(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	member := seedMember(t, ctx, f.tenant, "Ana", "39111222")
	code := seedCode(t, ctx, f.tenant, member, "active", f.clk.Now())
	taken := uniqueEmail("enum")
	f.seedUser(t, ctx, taken, "")
	limited := false
	for range 60 {
		if f.claim(code, taken, password, "192.0.2.200").Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("60 email_in_use probes from one IP were never limited")
	}
}

func TestRedeemSessionVanishedAfterAuthentication(t *testing.T) {
	ctx := integration(t)
	var armed bool
	var calls int
	var user string
	var deleteErr error
	f := newFixtureWith(t, ctx, func(d *httpapi.Deps) {
		base := d.Now
		d.Now = func() time.Time {
			if armed {
				calls++
				if calls == 3 {
					_, deleteErr = tdb.Super.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, user)
				}
			}
			return base()
		}
	})
	user, token := f.signIn(t, ctx, "officer")
	other := newTenant(t, ctx)
	member := seedMember(t, ctx, other, "Student", "39111001")
	code := seedCode(t, ctx, other, member, "active", f.clk.Now())
	armed = true
	rec := f.redeem(code, token)
	if deleteErr != nil || calls < 3 {
		t.Fatalf("session deletion: calls=%d err=%v", calls, deleteErr)
	}
	wantResponse(t, rec, http.StatusUnauthorized, unauthBody)
	if got := memberAccounts(t, ctx, member); len(got) != 0 {
		t.Fatalf("memberships = %v, want none", got)
	}
	wantCodeUsed(t, ctx, member, nil)
}

func TestConcurrentRedeemsOfSameSession(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, token := f.signIn(t, ctx, "officer")
	members := []string{seedMember(t, ctx, newTenant(t, ctx), "One", "39111002"), seedMember(t, ctx, newTenant(t, ctx), "Two", "39111003")}
	var codes [2]string
	for i, member := range members {
		var tenant string
		if err := tdb.Super.QueryRow(ctx, `SELECT tenant_id::text FROM roster_members WHERE id = $1`, member).Scan(&tenant); err != nil {
			t.Fatal(err)
		}
		codes[i] = seedCode(t, ctx, tenant, member, "active", f.clk.Now())
	}
	var recs [2]*httptest.ResponseRecorder
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range codes {
		wg.Go(func() { <-start; recs[i] = f.redeem(codes[i], token) })
	}
	close(start)
	wg.Wait()
	success := 0
	for _, rec := range recs {
		if rec.Code == http.StatusOK {
			success++
		} else {
			wantResponse(t, rec, http.StatusUnauthorized, unauthBody)
		}
	}
	if success < 1 {
		t.Fatal("no successful redeem")
	}
}

func TestClaimUsesOneInstant(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	now := f.clk.Now()
	calls := 0
	store := roster.NewStore(tdb.App, testKey, func() time.Time { t := now.Add(time.Duration(calls) * time.Second); calls++; return t })
	expiredMember := seedMember(t, ctx, f.tenant, "Expired", "39111004")
	expired := seedCode(t, ctx, f.tenant, expiredMember, "active", now)
	if _, err := tdb.Super.Exec(ctx, `UPDATE access_codes SET expires_at = $1 WHERE roster_member_id = $2`, now, expiredMember); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(ctx, expired, uniqueEmail("expired-clock"), password, ""); !errors.Is(err, roster.ErrInvalidCode) {
		t.Fatalf("expired code error = %v", err)
	}
	validMember := seedMember(t, ctx, f.tenant, "Valid", "39111005")
	valid := seedCode(t, ctx, f.tenant, validMember, "active", now)
	calls = 0
	got, err := store.Claim(ctx, valid, uniqueEmail("valid-clock"), password, "")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("clock reads = %d, want 1", calls)
	}
	if want := now.Add(auth.SessionLifetime); !got.Expires.Equal(want) || !sessionExpiry(t, ctx, got.Token).Equal(want) {
		t.Fatalf("session expiry = %v, want %v", got.Expires, want)
	}
}
