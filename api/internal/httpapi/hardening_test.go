package httpapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/httpapi"
)

const internalBody = `{"error":"internal"}`

var maxAgeRE = regexp.MustCompile(`^__Host-session=([A-Za-z0-9_-]{43}); Path=/; Max-Age=(\d+); HttpOnly; Secure; SameSite=Lax$`)

// cookieWithMaxAge returns the session token set by rec, failing unless Max-Age is maxAge seconds.
func cookieWithMaxAge(t *testing.T, rec *httptest.ResponseRecorder, maxAge int) string {
	t.Helper()
	sc := rec.Header().Get("Set-Cookie")
	m := maxAgeRE.FindStringSubmatch(sc)
	if m == nil || m[2] != strconv.Itoa(maxAge) {
		t.Fatalf("Set-Cookie = %q, want a session cookie with Max-Age=%d", sc, maxAge)
	}
	return m[1]
}

func sessionExpiry(t *testing.T, ctx context.Context, token string) time.Time {
	t.Helper()
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	sum := sha256.Sum256(raw)
	var exp time.Time
	if err := tdb.Super.QueryRow(ctx, `SELECT expires_at FROM sessions WHERE token_hash = $1`, sum[:]).Scan(&exp); err != nil {
		t.Fatalf("session expiry: %v", err)
	}
	return exp
}

// The per-IP key comes from X-Forwarded-For only when the TCP peer is a trusted proxy. Each case
// spends the 50 invalid-token budget of one client and checks who is limited afterwards.
func TestClientIPBehindTrustedProxy(t *testing.T) {
	ctx := integration(t)
	bogus := tokenBody(base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
	verify := func(f *fixture, peer, xff string) int {
		var h map[string]string
		if xff != "" {
			h = map[string]string{"X-Forwarded-For": xff}
		}
		return f.do(http.MethodPost, "/api/auth/email/verify", bogus, peer, "", h).Code
	}
	type try struct {
		peer, xff string
		want      int
	}
	for _, tc := range []struct {
		name          string
		spendPeer     string
		spendXFF      func(i int) string
		then          []try
		trustedRanges string
	}{
		{"untrusted peer: X-Forwarded-For is ignored", "203.0.113.1",
			func(i int) string { return fmt.Sprintf("10.9.0.%d", i) },
			[]try{{"203.0.113.1", "10.9.0.200", 429}, {"203.0.113.2", "", 422}}, ""},
		{"trusted proxy: right-most untrusted address", "10.0.0.1",
			func(i int) string { return fmt.Sprintf("192.0.2.%d, 198.18.0.1", i) }, // left part is spoofable
			[]try{
				{"10.0.0.1", "198.18.0.1", 429},
				{"10.0.0.1", "1.2.3.4, 198.18.0.1, 10.0.0.2", 429}, // second trusted hop skipped
				{"10.0.0.1", "198.18.0.1, 198.18.0.2", 422},        // another student behind the proxy
				{"10.0.0.1", "", 422},                              // the proxy itself
				{"10.0.0.1", "not-an-ip", 422},                     // falls back to the peer
			}, "10.0.0.0/8, 192.168.1.1/32"},
		{"IPv6 clients share their /64", "[2001:db8:1:2::1]",
			func(int) string { return "" },
			[]try{{"[2001:db8:1:2:ffff::9]", "", 429}, {"[2001:db8:1:3::1]", "", 422}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixtureWith(t, ctx, func(d *httpapi.Deps) {
				for _, s := range strings.Split(tc.trustedRanges, ",") {
					if s = strings.TrimSpace(s); s != "" {
						d.TrustedProxies = append(d.TrustedProxies, netip.MustParsePrefix(s))
					}
				}
			})
			for i := range 50 {
				if got := verify(f, tc.spendPeer, tc.spendXFF(i)); got != http.StatusUnprocessableEntity {
					t.Fatalf("attempt %d: status = %d, want 422", i+1, got)
				}
			}
			for _, tr := range tc.then {
				if got := verify(f, tr.peer, tr.xff); got != tr.want {
					t.Errorf("peer %s, X-Forwarded-For %q: status = %d, want %d", tr.peer, tr.xff, got, tr.want)
				}
			}
		})
	}
}

// failEmailTokensFor makes every email_tokens insert for user fail until the test ends.
func failEmailTokensFor(t *testing.T, ctx context.Context, user string) {
	t.Helper()
	name := "t7_fail_" + strings.ReplaceAll(user, "-", "")
	for _, q := range []string{
		`CREATE FUNCTION ` + name + `() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected'; END $$`,
		`CREATE TRIGGER ` + name + ` BEFORE INSERT ON email_tokens FOR EACH ROW WHEN (NEW.user_id = '` + user + `') EXECUTE FUNCTION ` + name + `()`,
	} {
		if _, err := tdb.Super.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = tdb.Super.Exec(context.Background(), `DROP TRIGGER IF EXISTS `+name+` ON email_tokens`)
		_, _ = tdb.Super.Exec(context.Background(), `DROP FUNCTION IF EXISTS `+name+`()`)
	})
}

// A server error is not the client's fault: it never spends a per-IP or per-email attempt.
func TestServerErrorsDoNotCountInLimiters(t *testing.T) {
	ctx := integration(t)
	t.Run("login", func(t *testing.T) {
		f := newFixture(t, ctx)
		email := uniqueEmail("err-login")
		user := f.seedUser(t, ctx, email, "student")
		if _, err := tdb.Super.Exec(ctx, `UPDATE users SET password_hash = 'broken' WHERE id = $1`, user); err != nil {
			t.Fatal(err)
		}
		for range 51 { // over both the per-email (5) and the per-IP (50) limits
			wantResponse(t, f.login(email, password, "203.0.113.230"), http.StatusInternalServerError, internalBody)
		}
		hash, _ := auth.HashPassword(password)
		if _, err := tdb.Super.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, user, hash); err != nil {
			t.Fatal(err)
		}
		wantResponse(t, f.login(email, password, "203.0.113.230"), http.StatusNoContent, "")
	})
	t.Run("mail", func(t *testing.T) {
		f := newFixture(t, ctx)
		email := uniqueEmail("err-mail")
		user := f.seedUser(t, ctx, email, "")
		failEmailTokensFor(t, ctx, user)
		for range 51 {
			wantResponse(t, f.post("/api/auth/link", emailBody(email), "203.0.113.231", ""), http.StatusInternalServerError, internalBody)
		}
		f.noMail(t)
		_, _ = tdb.Super.Exec(ctx, `DROP TRIGGER t7_fail_`+strings.ReplaceAll(user, "-", "")+` ON email_tokens`)
		wantResponse(t, f.post("/api/auth/link", emailBody(email), "203.0.113.231", ""), http.StatusAccepted, "")
		f.oneLink(t, email, "Tu enlace para ingresar", "/ingresar")
	})
}

// Signing in with a browser that already holds a valid session replaces that session.
func TestSignInDeletesTheRequestsPreviousSession(t *testing.T) {
	ctx := integration(t)
	for _, tc := range []struct {
		name string
		// signIn signs in (as anyone) sending the old cookie and returns the response.
		signIn func(t *testing.T, f *fixture, old string) *httptest.ResponseRecorder
	}{
		{"password login", func(t *testing.T, f *fixture, old string) *httptest.ResponseRecorder {
			email := uniqueEmail("rot-login")
			f.seedUser(t, ctx, email, "student")
			body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)
			// A failed login keeps the old session.
			bad := f.do(http.MethodPost, "/api/auth/login", strings.Replace(body, password, "wrong", 1), "198.51.100.70", old, nil)
			wantResponse(t, bad, http.StatusUnauthorized, invalidBody)
			if _, ok := sessionMFAAt(t, ctx, old); !ok {
				t.Fatal("failed login deleted the old session")
			}
			return f.do(http.MethodPost, "/api/auth/login", body, "198.51.100.70", old, nil)
		}},
		{"sign-in link", func(t *testing.T, f *fixture, old string) *httptest.ResponseRecorder {
			email := uniqueEmail("rot-link")
			f.seedUser(t, ctx, email, "student")
			f.post("/api/auth/link", emailBody(email), "198.51.100.71", "")
			tok := f.oneLink(t, email, "Tu enlace para ingresar", "/ingresar")
			return f.post("/api/auth/link/consume", tokenBody(tok), "198.51.100.71", old)
		}},
		{"claim", func(t *testing.T, f *fixture, old string) *httptest.ResponseRecorder {
			member := seedMember(t, ctx, f.tenant, "Ana Acosta", "30111260")
			code := seedCode(t, ctx, f.tenant, member, "active", f.clk.Now())
			rec := f.do(http.MethodPost, "/api/auth/claim", fmt.Sprintf(`{"code":%q,"email":%q,"password":%q}`,
				code, uniqueEmail("rot-claim"), password), "198.51.100.72", old, nil)
			f.mail.take()
			return rec
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, ctx)
			prevUser, old := f.signIn(t, ctx, "officer")
			rec := tc.signIn(t, f, old)
			if rec.Code != http.StatusNoContent && rec.Code != http.StatusCreated {
				t.Fatalf("status = %d (%s), want success", rec.Code, rec.Body)
			}
			tok := cookieWithMaxAge(t, rec, 12*3600)
			if _, ok := sessionMFAAt(t, ctx, old); ok {
				t.Fatal("previous session row still exists")
			}
			if n := sessionCount(t, ctx, prevUser); n != 0 {
				t.Fatalf("previous user's sessions = %d, want 0", n)
			}
			wantResponse(t, f.do(http.MethodGet, "/api/auth/me", "", "198.51.100.73", old, nil), http.StatusUnauthorized, unauthBody)
			if got := f.do(http.MethodGet, "/api/auth/me", "", "198.51.100.73", tok, nil).Code; got != http.StatusOK {
				t.Fatalf("new session: /auth/me status = %d, want 200", got)
			}
		})
	}
}

func TestEnrollmentConfirmCookieKeepsRemainingLifetime(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, token := f.signIn(t, ctx, "school_admin")
	f.clk.Advance(2 * time.Hour)
	secret := f.enroll(t, token)
	rec := f.post("/api/auth/mfa/enrollment/confirm", codeBody(f.code(secret, 0)), mfaIP, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm status = %d (%s), want 200", rec.Code, rec.Body)
	}
	cookieWithMaxAge(t, rec, 10*3600)
}

// insertSession stores a session for user expiring at exp and returns its token.
func insertSession(t *testing.T, ctx context.Context, user string, exp time.Time) string {
	t.Helper()
	tok, err := auth.InsertSession(ctx, tdb.Super, user, nil, exp.Add(-auth.SessionLifetime))
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestDeleteExpiredSessionsInBatches(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	user := f.seedUser(t, ctx, uniqueEmail("cleanup"), "")
	now := f.clk.Now()
	expired := []string{
		insertSession(t, ctx, user, now.Add(-time.Hour)),
		insertSession(t, ctx, user, now.Add(-time.Minute)),
		insertSession(t, ctx, user, now.Add(-time.Microsecond)),
	}
	live := insertSession(t, ctx, user, now.Add(time.Minute))

	n, err := auth.NewStore(tdb.App, f.clk.Now).DeleteExpiredSessions(ctx, 2)
	if err != nil || n < 3 {
		t.Fatalf("DeleteExpiredSessions = %d, %v; want at least 3", n, err)
	}
	for _, tok := range expired {
		if _, ok := sessionMFAAt(t, ctx, tok); ok {
			t.Fatal("expired session still stored")
		}
	}
	if _, ok := sessionMFAAt(t, ctx, live); !ok {
		t.Fatal("live session deleted")
	}
}

func TestSessionCleanupLoopRunsUntilCancelled(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	user := f.seedUser(t, ctx, uniqueEmail("cleanup-loop"), "")
	tok := insertSession(t, ctx, user, f.clk.Now().Add(-time.Minute))

	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		auth.NewStore(tdb.App, f.clk.Now).CleanSessionsEvery(loopCtx, 10*time.Millisecond)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := sessionMFAAt(t, ctx, tok); !ok {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("cleanup loop did not delete the expired session")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup loop did not stop after cancel")
	}
}
