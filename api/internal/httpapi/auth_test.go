package httpapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/httpapi"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/testdb"
)

var (
	tdb      *testdb.Env
	setupErr error
)

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	var stop func()
	tdb, stop, setupErr = testdb.Start(context.Background())
	code := m.Run()
	stop()
	os.Exit(code)
}

func integration(t *testing.T) context.Context {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker; skipped with -short")
	}
	if setupErr != nil {
		t.Fatalf("setup: %v", setupErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

const (
	cookieName  = "__Host-session"
	password    = "una clave bastante larga"
	unauthBody  = `{"error":"unauthenticated"}`
	invalidBody = `{"error":"invalid_credentials"}`
)

var setCookieRE = regexp.MustCompile(`^__Host-session=([A-Za-z0-9_-]{43}); Path=/; Max-Age=43200; HttpOnly; Secure; SameSite=Lax$`)

type fixture struct {
	clk    *fakeClock
	h      http.Handler
	tenant string
	// tenantName is the name of the fixture's organization (the test's name).
	tenantName string
	mail       *fakeMail
}

func newFixture(t *testing.T, ctx context.Context) *fixture {
	t.Helper()
	return newFixtureWith(t, ctx, func(*httpapi.Deps) {})
}

// newFixtureWith is newFixture with a hook that adjusts the router's dependencies.
func newFixtureWith(t *testing.T, ctx context.Context, opt func(*httpapi.Deps)) *fixture {
	t.Helper()
	f := &fixture{clk: &fakeClock{t: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)}, mail: &fakeMail{}}
	d := httpapi.Deps{Pool: tdb.App, Now: f.clk.Now, AccessCodeKey: testKey, TOTPKey: testTOTPKey,
		Mail: f.mail, BaseURL: baseURL, Background: func(fn func()) { fn() }}
	opt(&d)
	f.h = httpapi.NewRouter(d)
	f.tenantName = t.Name()
	if err := tdb.Super.QueryRow(ctx,
		`INSERT INTO organizations (kind, name) VALUES ('school', $1) RETURNING id::text`, t.Name()).Scan(&f.tenant); err != nil {
		t.Fatal(err)
	}
	return f
}

// seedUser creates a user and, when role is not empty, a membership in the fixture's tenant.
func (f *fixture) seedUser(t *testing.T, ctx context.Context, email, role string) string {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	if err := tdb.Super.QueryRow(ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id::text`, email, hash).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if role != "" {
		if _, err := tdb.Super.Exec(ctx,
			`INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, $3)`, f.tenant, id, role); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func (f *fixture) do(method, path, body, ip, token string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = ip + ":40000"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	if token != "" {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

func (f *fixture) login(email, pw, ip string) *httptest.ResponseRecorder {
	return f.do(http.MethodPost, "/api/auth/login", fmt.Sprintf(`{"email":%q,"password":%q}`, email, pw), ip, "", nil)
}

// loginOK signs in and returns the raw session token from Set-Cookie.
func (f *fixture) loginOK(t *testing.T, email string) string {
	t.Helper()
	rec := f.login(email, password, "198.51.100.1")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login status = %d, body %s; want 204", rec.Code, rec.Body)
	}
	m := setCookieRE.FindStringSubmatch(rec.Header().Get("Set-Cookie"))
	if m == nil {
		t.Fatalf("Set-Cookie = %q, want match for %s", rec.Header().Get("Set-Cookie"), setCookieRE)
	}
	return m[1]
}

func sessionCount(t *testing.T, ctx context.Context, userID string) int {
	t.Helper()
	var n int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func wantResponse(t *testing.T, rec *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, status, rec.Body)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != body {
		t.Fatalf("body = %s, want %s", got, body)
	}
	if body != "" {
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", ct)
		}
	}
}

func TestLoginSetsSessionCookieAndStoresOnlyTokenHash(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	userID := f.seedUser(t, ctx, "ana.login@example.com", "school_admin")

	// Email matching is case-insensitive.
	token := f.loginOK(t, "  Ana.Login@Example.com ")

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		t.Fatalf("token decodes to %d bytes (%v), want 32", len(raw), err)
	}
	var stored []byte
	var tenant string
	var expires time.Time
	if err := tdb.Super.QueryRow(ctx,
		`SELECT token_hash, tenant_id::text, expires_at FROM sessions WHERE user_id = $1`, userID).
		Scan(&stored, &tenant, &expires); err != nil {
		t.Fatalf("read session: %v", err)
	}
	sum := sha256.Sum256(raw)
	if string(stored) != string(sum[:]) {
		t.Fatal("stored token_hash is not SHA-256 of the cookie token")
	}
	var leaks int
	if err := tdb.Super.QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE token_hash = $1 OR position($1 in token_hash) > 0 OR position($2::bytea in token_hash) > 0`,
		raw, []byte(token)).Scan(&leaks); err != nil {
		t.Fatal(err)
	}
	if leaks != 0 {
		t.Fatal("raw token found in the sessions table")
	}
	if tenant != f.tenant {
		t.Fatalf("session tenant = %s, want %s", tenant, f.tenant)
	}
	if want := f.clk.Now().Add(12 * time.Hour); !expires.Equal(want) {
		t.Fatalf("expires_at = %v, want %v", expires, want)
	}
	if n := sessionCount(t, ctx, userID); n != 1 {
		t.Fatalf("sessions = %d, want 1", n)
	}
}

func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	userID := f.seedUser(t, ctx, "ana.fail@example.com", "student")
	for _, tc := range []struct{ name, email, pw string }{
		{"unknown email", "nadie@example.com", password},
		{"wrong password", "ana.fail@example.com", password + "x"},
		{"empty password", "ana.fail@example.com", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.login(tc.email, tc.pw, "198.51.100.2")
			wantResponse(t, rec, http.StatusUnauthorized, invalidBody)
			if sc := rec.Header().Get("Set-Cookie"); sc != "" {
				t.Fatalf("Set-Cookie = %q, want none", sc)
			}
		})
	}
	if n := sessionCount(t, ctx, userID); n != 0 {
		t.Fatalf("sessions = %d, want 0", n)
	}
}

func TestLoginRejectsMalformedBody(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	rec := f.do(http.MethodPost, "/api/auth/login", `{"email":`, "198.51.100.3", "", nil)
	wantResponse(t, rec, http.StatusBadRequest, `{"error":"bad_request"}`)
}

func TestLoginIsRateLimitedPerEmailAndPerIP(t *testing.T) {
	ctx := integration(t)
	for _, tc := range []struct {
		name      string
		failures  int                            // the limit: 5 per email, 50 per IP (school NAT)
		attempt   func(i int) (email, ip string) // the failing attempts
		lastEmail string
		lastIP    string
	}{
		{"same email from many IPs", 5, func(i int) (string, string) {
			return "rate.email@example.com", fmt.Sprintf("203.0.113.%d", i+1)
		}, "rate.email@example.com", "203.0.113.99"},
		{"same IP for many emails", 50, func(i int) (string, string) {
			return fmt.Sprintf("nobody%d@example.com", i), "203.0.113.200"
		}, "rate.ip@example.com", "203.0.113.200"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, ctx)
			userID := f.seedUser(t, ctx, tc.lastEmail, "student")
			for i := range tc.failures {
				email, ip := tc.attempt(i)
				wantResponse(t, f.login(email, "wrong", ip), http.StatusUnauthorized, invalidBody)
			}
			// The next attempt within the window is refused even with the right password.
			rec := f.login(tc.lastEmail, password, tc.lastIP)
			wantResponse(t, rec, http.StatusTooManyRequests, `{"error":"too_many_attempts"}`)
			if sc := rec.Header().Get("Set-Cookie"); sc != "" {
				t.Fatalf("Set-Cookie = %q, want none", sc)
			}
			if n := sessionCount(t, ctx, userID); n != 0 {
				t.Fatalf("sessions = %d, want 0", n)
			}
			f.clk.Advance(15 * time.Minute)
			if rec := f.login(tc.lastEmail, password, tc.lastIP); rec.Code != http.StatusNoContent {
				t.Fatalf("after the window: status = %d, want 204", rec.Code)
			}
		})
	}
}

func TestMe(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	adminID := f.seedUser(t, ctx, "ana.me@example.com", "school_admin")
	loneID := f.seedUser(t, ctx, "solo.me@example.com", "")
	adminToken := f.loginOK(t, "ana.me@example.com")
	loneToken := f.loginOK(t, "solo.me@example.com")
	// The name is read on every request, as text: quotes and accents come back JSON-encoded.
	if _, err := tdb.Super.Exec(ctx, `UPDATE organizations SET name = $2 WHERE id = $1`,
		f.tenant, `Escuela "Demo" Tucumán`); err != nil {
		t.Fatal(err)
	}

	t.Run("valid session", func(t *testing.T) {
		rec := f.do(http.MethodGet, "/api/auth/me", "", "198.51.100.4", adminToken, nil)
		wantResponse(t, rec, http.StatusOK, fmt.Sprintf(
			`{"email":"ana.me@example.com","emailVerified":false,"mfa":"enrollment_required","role":"school_admin","tenantId":"%s","tenantName":"Escuela \"Demo\" Tucumán","userId":"%s"}`, f.tenant, adminID))
	})
	t.Run("user without membership", func(t *testing.T) {
		rec := f.do(http.MethodGet, "/api/auth/me", "", "198.51.100.4", loneToken, nil)
		wantResponse(t, rec, http.StatusOK, fmt.Sprintf(
			`{"email":"solo.me@example.com","emailVerified":false,"mfa":"not_required","role":null,"tenantId":null,"tenantName":null,"userId":"%s"}`, loneID))
	})
	t.Run("membership removed after sign-in", func(t *testing.T) {
		email := uniqueEmail("gone.me")
		goneID := f.seedUser(t, ctx, email, "student")
		token := f.loginOK(t, email)
		if _, err := tdb.Super.Exec(ctx, `DELETE FROM memberships WHERE user_id = $1`, goneID); err != nil {
			t.Fatal(err)
		}
		// The session still names the tenant, but without a role it must not be reported.
		rec := f.do(http.MethodGet, "/api/auth/me", "", "198.51.100.4", token, nil)
		wantResponse(t, rec, http.StatusOK, fmt.Sprintf(
			`{"email":"%s","emailVerified":false,"mfa":"not_required","role":null,"tenantId":null,"tenantName":null,"userId":"%s"}`, email, goneID))
	})
	t.Run("missing cookie", func(t *testing.T) {
		wantResponse(t, f.do(http.MethodGet, "/api/auth/me", "", "198.51.100.4", "", nil), http.StatusUnauthorized, unauthBody)
	})
	t.Run("unknown token", func(t *testing.T) {
		bogus := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
		for _, tok := range []string{bogus, "not base64!"} {
			wantResponse(t, f.do(http.MethodGet, "/api/auth/me", "", "198.51.100.4", tok, nil), http.StatusUnauthorized, unauthBody)
		}
	})
	t.Run("expired session", func(t *testing.T) {
		f.clk.Advance(12 * time.Hour)
		wantResponse(t, f.do(http.MethodGet, "/api/auth/me", "", "198.51.100.4", adminToken, nil), http.StatusUnauthorized, unauthBody)
	})
}

func TestLogoutDeletesSessionAndExpiresCookie(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	userID := f.seedUser(t, ctx, "ana.logout@example.com", "officer")
	token := f.loginOK(t, "ana.logout@example.com")
	other := f.loginOK(t, "ana.logout@example.com")

	const expired = "__Host-session=; Path=/; Max-Age=0; HttpOnly; Secure; SameSite=Lax"
	rec := f.do(http.MethodPost, "/api/auth/logout", "", "198.51.100.5", token, nil)
	wantResponse(t, rec, http.StatusNoContent, "")
	if sc := rec.Header().Get("Set-Cookie"); sc != expired {
		t.Fatalf("Set-Cookie = %q, want %q", sc, expired)
	}
	if n := sessionCount(t, ctx, userID); n != 1 {
		t.Fatalf("sessions = %d, want 1 (only the other session left)", n)
	}
	wantResponse(t, f.do(http.MethodGet, "/api/auth/me", "", "198.51.100.5", token, nil), http.StatusUnauthorized, unauthBody)
	if rec := f.do(http.MethodGet, "/api/auth/me", "", "198.51.100.5", other, nil); rec.Code != http.StatusOK {
		t.Fatalf("other session: status = %d, want 200", rec.Code)
	}

	// Without a cookie logout is still a no-op success that expires the cookie.
	rec = f.do(http.MethodPost, "/api/auth/logout", "", "198.51.100.5", "", nil)
	wantResponse(t, rec, http.StatusNoContent, "")
	if sc := rec.Header().Get("Set-Cookie"); sc != expired {
		t.Fatalf("Set-Cookie = %q, want %q", sc, expired)
	}
}

func TestCrossOriginLoginIsRejected(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	userID := f.seedUser(t, ctx, "ana.csrf@example.com", "student")
	body := fmt.Sprintf(`{"email":"ana.csrf@example.com","password":%q}`, password)

	rec := f.do(http.MethodPost, "/api/auth/login", body, "198.51.100.6", "",
		map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d, want 403", rec.Code)
	}
	if sc := rec.Header().Get("Set-Cookie"); sc != "" {
		t.Fatalf("Set-Cookie = %q, want none", sc)
	}
	if n := sessionCount(t, ctx, userID); n != 0 {
		t.Fatalf("sessions = %d, want 0", n)
	}

	rec = f.do(http.MethodPost, "/api/auth/login", body, "198.51.100.6", "",
		map[string]string{"Origin": "https://example.com", "Sec-Fetch-Site": "same-origin"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("same-origin status = %d, want 204", rec.Code)
	}
}
