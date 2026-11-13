package httpapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/mail"
)

const (
	baseURL          = "https://ce.example"
	invalidTokenBody = `{"error":"invalid_token"}`
	tooManyBody      = `{"error":"too_many_attempts"}`
	newPassword      = "otra clave bien larga"
)

// fakeMail records every message, even when it is set to fail.
type fakeMail struct {
	mu   sync.Mutex
	msgs []mail.Message
	err  error
}

func (m *fakeMail) Send(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, msg)
	return m.err
}

// take returns and forgets the recorded messages.
func (m *fakeMail) take() []mail.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.msgs
	m.msgs = nil
	return out
}

// oneLink checks that exactly one message went to `to` with a link to path and returns its token.
func (f *fixture) oneLink(t *testing.T, to, subject, path string) string {
	t.Helper()
	msgs := f.mail.take()
	if len(msgs) != 1 || msgs[0].To != to || msgs[0].Subject != subject {
		t.Fatalf("messages = %+v, want one to %s with subject %q", msgs, to, subject)
	}
	re := regexp.MustCompile(regexp.QuoteMeta(baseURL+path+"#token=") + `([A-Za-z0-9_-]{43})\n`)
	m := re.FindStringSubmatch(msgs[0].Text)
	if m == nil {
		t.Fatalf("text = %q, want a %s link", msgs[0].Text, path)
	}
	return m[1]
}

func (f *fixture) noMail(t *testing.T) {
	t.Helper()
	if msgs := f.mail.take(); len(msgs) != 0 {
		t.Fatalf("messages = %+v, want none", msgs)
	}
}

func (f *fixture) post(path, body, ip, token string) *httptest.ResponseRecorder {
	return f.do(http.MethodPost, path, body, ip, token, nil)
}

type tokenRow struct {
	Purpose          string
	Created, Expires time.Time
	Used             *time.Time
}

// emailToken reads the row of a raw token; it fails unless only SHA-256 of the token is stored.
func emailToken(t *testing.T, ctx context.Context, user, raw string) tokenRow {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(b) != 32 {
		t.Fatalf("token %q is not 32 bytes of base64url", raw)
	}
	sum := sha256.Sum256(b)
	var r tokenRow
	if err := tdb.Super.QueryRow(ctx, `SELECT purpose, created_at, expires_at, used_at FROM email_tokens
		WHERE token_hash = $1 AND user_id = $2`, sum[:], user).Scan(&r.Purpose, &r.Created, &r.Expires, &r.Used); err != nil {
		t.Fatalf("token row: %v", err)
	}
	var leaks int
	_ = tdb.Super.QueryRow(ctx, `SELECT count(*) FROM email_tokens WHERE position($1::bytea in token_hash) > 0
		OR position($2::bytea in token_hash) > 0`, b, []byte(raw)).Scan(&leaks)
	if leaks != 0 {
		t.Fatal("raw token stored in email_tokens")
	}
	return r
}

func tokenCount(t *testing.T, ctx context.Context, user string) int {
	t.Helper()
	var n int
	_ = tdb.Super.QueryRow(ctx, `SELECT count(*) FROM email_tokens WHERE user_id = $1`, user).Scan(&n)
	return n
}

func verifiedAt(t *testing.T, ctx context.Context, user string) *time.Time {
	t.Helper()
	var v *time.Time
	if err := tdb.Super.QueryRow(ctx, `SELECT email_verified_at FROM users WHERE id = $1`, user).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func wantTime(t *testing.T, what string, got *time.Time, want time.Time) {
	t.Helper()
	if got == nil || !got.Equal(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func tokenBody(tok string) string { return fmt.Sprintf(`{"token":%q}`, tok) }

func emailBody(email string) string { return fmt.Sprintf(`{"email":%q}`, email) }

func TestEmailVerification(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	email := uniqueEmail("verify")
	user := f.seedUser(t, ctx, email, "student")
	session := f.loginOK(t, email)

	wantResponse(t, f.post("/api/auth/email/verification", "", "198.51.100.20", ""), http.StatusUnauthorized, unauthBody)
	f.noMail(t)

	rec := f.post("/api/auth/email/verification", "", "198.51.100.20", session)
	wantResponse(t, rec, http.StatusAccepted, "")
	wantNoStore(t, rec)
	tok := f.oneLink(t, email, "Confirmá tu correo", "/verificar")
	now := f.clk.Now()
	row := emailToken(t, ctx, user, tok)
	if row.Purpose != "verify_email" || !row.Created.Equal(now) || !row.Expires.Equal(now.Add(48*time.Hour)) || row.Used != nil {
		t.Fatalf("token row = %+v, want unused verify_email expiring in 48h", row)
	}
	if verifiedAt(t, ctx, user) != nil {
		t.Fatal("email verified before the link was used")
	}

	f.clk.Advance(time.Minute)
	rec = f.post("/api/auth/email/verify", tokenBody(tok), "198.51.100.21", "")
	wantResponse(t, rec, http.StatusNoContent, "")
	wantNoStore(t, rec)
	wantTime(t, "email_verified_at", verifiedAt(t, ctx, user), f.clk.Now())
	wantTime(t, "used_at", emailToken(t, ctx, user, tok).Used, f.clk.Now())

	t.Run("already verified sends nothing", func(t *testing.T) {
		rec := f.post("/api/auth/email/verification", "", "198.51.100.20", session)
		wantResponse(t, rec, http.StatusAccepted, "")
		f.noMail(t)
		if n := tokenCount(t, ctx, user); n != 1 {
			t.Fatalf("tokens = %d, want 1", n)
		}
	})
}

func TestIssuingATokenInvalidatesThePreviousOne(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	email := uniqueEmail("reissue")
	user := f.seedUser(t, ctx, email, "")
	session := f.loginOK(t, email)

	f.post("/api/auth/email/verification", "", "198.51.100.22", session)
	first := f.oneLink(t, email, "Confirmá tu correo", "/verificar")
	f.post("/api/auth/link", emailBody(email), "198.51.100.22", "") // another purpose: stays valid
	link := f.oneLink(t, email, "Tu enlace para ingresar", "/ingresar")
	f.post("/api/auth/email/verification", "", "198.51.100.22", session)
	second := f.oneLink(t, email, "Confirmá tu correo", "/verificar")

	wantTime(t, "first used_at", emailToken(t, ctx, user, first).Used, f.clk.Now())
	wantResponse(t, f.post("/api/auth/email/verify", tokenBody(first), "198.51.100.22", ""), http.StatusUnprocessableEntity, invalidTokenBody)
	if verifiedAt(t, ctx, user) != nil {
		t.Fatal("invalidated token verified the email")
	}
	wantResponse(t, f.post("/api/auth/email/verify", tokenBody(second), "198.51.100.22", ""), http.StatusNoContent, "")
	wantResponse(t, f.post("/api/auth/link/consume", tokenBody(link), "198.51.100.22", ""), http.StatusNoContent, "")
}

func TestSignInLink(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	email := uniqueEmail("link")
	user := f.seedUser(t, ctx, email, "officer")

	unknown := f.post("/api/auth/link", emailBody(uniqueEmail("nobody")), "198.51.100.30", "")
	f.noMail(t)
	known := f.post("/api/auth/link", emailBody(" "+strings.ToUpper(email)+" "), "198.51.100.31", "")
	for _, rec := range []*httptest.ResponseRecorder{unknown, known} {
		wantResponse(t, rec, http.StatusAccepted, "")
		wantNoStore(t, rec)
	}
	if fmt.Sprint(unknown.Header()) != fmt.Sprint(known.Header()) {
		t.Fatalf("headers differ: %v vs %v", unknown.Header(), known.Header())
	}
	tok := f.oneLink(t, email, "Tu enlace para ingresar", "/ingresar")
	if row := emailToken(t, ctx, user, tok); row.Purpose != "sign_in" || !row.Expires.Equal(f.clk.Now().Add(15*time.Minute)) {
		t.Fatalf("token row = %+v, want sign_in expiring in 15m", row)
	}

	rec := f.post("/api/auth/link/consume", tokenBody(tok), "198.51.100.32", "")
	wantResponse(t, rec, http.StatusNoContent, "")
	wantNoStore(t, rec)
	m := setCookieRE.FindStringSubmatch(rec.Header().Get("Set-Cookie"))
	if m == nil {
		t.Fatalf("Set-Cookie = %q, want match for %s", rec.Header().Get("Set-Cookie"), setCookieRE)
	}
	if sessionTenant(t, ctx, m[1]) != f.tenant || sessionCount(t, ctx, user) != 1 {
		t.Fatal("want one session in the user's tenant")
	}
	wantTime(t, "email_verified_at", verifiedAt(t, ctx, user), f.clk.Now())
	wantTime(t, "used_at", emailToken(t, ctx, user, tok).Used, f.clk.Now())
	wantResponse(t, f.do(http.MethodGet, "/api/auth/me", "", "198.51.100.32", m[1], nil), http.StatusOK,
		meBody(user, email, f.tenant, "officer"))
}

func TestDeadTokensAreRejectedIdentically(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	email := uniqueEmail("dead-token")
	user := f.seedUser(t, ctx, email, "student")
	issue := func(path string) string {
		f.post(path, emailBody(email), "198.51.100.40", "")
		msgs := f.mail.take()
		return regexp.MustCompile(`#token=([A-Za-z0-9_-]+)`).FindStringSubmatch(msgs[len(msgs)-1].Text)[1]
	}
	used := issue("/api/auth/link")
	wantResponse(t, f.post("/api/auth/link/consume", tokenBody(used), "198.51.100.40", ""), http.StatusNoContent, "")
	_, _ = tdb.Super.Exec(ctx, `UPDATE users SET email_verified_at = NULL WHERE id = $1`, user)
	_, _ = tdb.Super.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, user)
	expired := issue("/api/auth/password/reset-request")
	f.clk.Advance(30 * time.Minute)
	signIn := issue("/api/auth/link")

	for _, tc := range []struct{ name, path, body string }{
		{"reused", "/api/auth/link/consume", tokenBody(used)},
		{"expired", "/api/auth/password/reset", fmt.Sprintf(`{"token":%q,"password":%q}`, expired, newPassword)},
		{"sign-in token used to verify", "/api/auth/email/verify", tokenBody(signIn)},
		{"sign-in token used to reset", "/api/auth/password/reset", fmt.Sprintf(`{"token":%q,"password":%q}`, signIn, newPassword)},
		{"unknown", "/api/auth/email/verify", tokenBody(base64.RawURLEncoding.EncodeToString(make([]byte, 32)))},
		{"malformed", "/api/auth/link/consume", tokenBody("not a token")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.post(tc.path, tc.body, "198.51.100.41", "")
			wantResponse(t, rec, http.StatusUnprocessableEntity, invalidTokenBody)
			wantNoStore(t, rec)
			if sc := rec.Header().Get("Set-Cookie"); sc != "" {
				t.Fatalf("Set-Cookie = %q, want none", sc)
			}
		})
	}
	if verifiedAt(t, ctx, user) != nil || sessionCount(t, ctx, user) != 0 {
		t.Fatal("a dead token verified the email or created a session")
	}
	if emailToken(t, ctx, user, signIn).Used != nil || emailToken(t, ctx, user, expired).Used != nil {
		t.Fatal("a rejected token was consumed")
	}
	_, hash := userByEmail(t, ctx, email)
	if ok, _ := auth.VerifyPassword(hash, password); !ok {
		t.Fatal("password changed")
	}
}

func TestPasswordReset(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	email := uniqueEmail("reset")
	user := f.seedUser(t, ctx, email, "student")
	f.loginOK(t, email)
	f.loginOK(t, email)

	unknown := f.post("/api/auth/password/reset-request", emailBody(uniqueEmail("nobody")), "198.51.100.50", "")
	f.noMail(t)
	known := f.post("/api/auth/password/reset-request", emailBody(email), "198.51.100.50", "")
	for _, rec := range []*httptest.ResponseRecorder{unknown, known} {
		wantResponse(t, rec, http.StatusAccepted, "")
		wantNoStore(t, rec)
	}
	tok := f.oneLink(t, email, "Restablecer tu contraseña", "/restablecer")
	if row := emailToken(t, ctx, user, tok); row.Purpose != "reset_password" || !row.Expires.Equal(f.clk.Now().Add(30*time.Minute)) {
		t.Fatalf("token row = %+v, want reset_password expiring in 30m", row)
	}
	reset := func(pw string) *httptest.ResponseRecorder {
		return f.post("/api/auth/password/reset", fmt.Sprintf(`{"token":%q,"password":%q}`, tok, pw), "198.51.100.51", "")
	}

	for _, weak := range []string{"123456789", strings.Repeat("ñ", 9), strings.Repeat("a", 129)} {
		rec := reset(weak)
		wantResponse(t, rec, http.StatusUnprocessableEntity, `{"error":"weak_password"}`)
		wantNoStore(t, rec)
	}
	if emailToken(t, ctx, user, tok).Used != nil || sessionCount(t, ctx, user) != 2 {
		t.Fatal("weak password consumed the token or revoked sessions")
	}

	rec := reset(newPassword)
	wantResponse(t, rec, http.StatusNoContent, "")
	wantNoStore(t, rec)
	if sc := rec.Header().Get("Set-Cookie"); sc != "" {
		t.Fatalf("Set-Cookie = %q, want none", sc)
	}
	if n := sessionCount(t, ctx, user); n != 0 {
		t.Fatalf("sessions = %d, want 0 after reset", n)
	}
	wantTime(t, "used_at", emailToken(t, ctx, user, tok).Used, f.clk.Now())
	wantResponse(t, f.login(email, password, "198.51.100.52"), http.StatusUnauthorized, invalidBody)
	wantResponse(t, f.login(email, newPassword, "198.51.100.52"), http.StatusNoContent, "")
	wantResponse(t, reset(newPassword+"x"), http.StatusUnprocessableEntity, invalidTokenBody)
}

func TestEmailRequestsAreRateLimited(t *testing.T) {
	ctx := integration(t)
	t.Run("per email", func(t *testing.T) {
		f := newFixture(t, ctx)
		email := uniqueEmail("rate-mail")
		f.seedUser(t, ctx, email, "")
		for i := range 5 {
			path := []string{"/api/auth/link", "/api/auth/password/reset-request"}[i%2]
			wantResponse(t, f.post(path, emailBody(email), fmt.Sprintf("203.0.113.%d", 10+i), ""), http.StatusAccepted, "")
		}
		f.mail.take()
		rec := f.post("/api/auth/link", emailBody(strings.ToUpper(email)), "203.0.113.99", "")
		wantResponse(t, rec, http.StatusTooManyRequests, tooManyBody)
		wantNoStore(t, rec)
		f.noMail(t)
		f.clk.Advance(15 * time.Minute)
		wantResponse(t, f.post("/api/auth/link", emailBody(email), "203.0.113.99", ""), http.StatusAccepted, "")
		f.oneLink(t, email, "Tu enlace para ingresar", "/ingresar")
	})
	t.Run("per IP", func(t *testing.T) {
		f := newFixture(t, ctx)
		email := uniqueEmail("rate-ip")
		f.seedUser(t, ctx, email, "")
		for range 5 {
			wantResponse(t, f.post("/api/auth/password/reset-request", emailBody(uniqueEmail("x")), "203.0.113.150", ""), http.StatusAccepted, "")
		}
		wantResponse(t, f.post("/api/auth/link", emailBody(email), "203.0.113.150", ""), http.StatusTooManyRequests, tooManyBody)
		f.noMail(t)
		wantResponse(t, f.post("/api/auth/link", emailBody(email), "203.0.113.151", ""), http.StatusAccepted, "")
		f.oneLink(t, email, "Tu enlace para ingresar", "/ingresar")
	})
}

func TestInvalidTokensAreRateLimitedPerIP(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	email := uniqueEmail("rate-token")
	user := f.seedUser(t, ctx, email, "")
	f.post("/api/auth/link", emailBody(email), "203.0.113.160", "")
	tok := f.oneLink(t, email, "Tu enlace para ingresar", "/ingresar")
	bogus := tokenBody(base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
	const ip = "203.0.113.161"
	for i := range 10 {
		path := []string{"/api/auth/email/verify", "/api/auth/link/consume"}[i%2]
		wantResponse(t, f.post(path, bogus, ip, ""), http.StatusUnprocessableEntity, invalidTokenBody)
	}
	rec := f.post("/api/auth/link/consume", tokenBody(tok), ip, "")
	wantResponse(t, rec, http.StatusTooManyRequests, tooManyBody)
	wantNoStore(t, rec)
	if emailToken(t, ctx, user, tok).Used != nil || sessionCount(t, ctx, user) != 0 {
		t.Fatal("limited request consumed the token")
	}
	wantResponse(t, f.post("/api/auth/link/consume", tokenBody(tok), "203.0.113.162", ""), http.StatusNoContent, "")
}

// A failed send is logged without the token and never undoes the change that triggered it.
func TestSendFailureKeepsTheChangeAndNeverLogsTheToken(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	f.mail.err = errors.New("smtp: 421 service not available")
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	member := seedMember(t, ctx, f.tenant, "Ana Acosta", "30111240")
	code := seedCode(t, ctx, f.tenant, member, "active", f.clk.Now())
	email := uniqueEmail("send-fail")
	rec := f.claim(code, email, password, "203.0.113.170")
	user, _ := userByEmail(t, ctx, email)
	wantResponse(t, rec, http.StatusCreated, meBody(user, email, f.tenant, "student"))
	verifyTok := f.oneLink(t, email, "Confirmá tu correo", "/verificar")
	wantResponse(t, f.post("/api/auth/password/reset-request", emailBody(email), "203.0.113.170", ""), http.StatusAccepted, "")
	resetTok := f.oneLink(t, email, "Restablecer tu contraseña", "/restablecer")

	for _, tok := range []string{verifyTok, resetTok} {
		if emailToken(t, ctx, user, tok).Used != nil {
			t.Fatal("token not stored as usable")
		}
		if strings.Contains(logs.String(), tok) {
			t.Fatalf("log contains the token: %s", logs.String())
		}
	}
	if n := strings.Count(logs.String(), "send mail"); n != 2 {
		t.Fatalf("log = %s, want 2 send mail errors", logs.String())
	}
	wantResponse(t, f.post("/api/auth/email/verify", tokenBody(verifyTok), "203.0.113.170", ""), http.StatusNoContent, "")
}

func TestClaimSendsVerificationMail(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	member := seedMember(t, ctx, f.tenant, "Ana Acosta", "30111241")
	code := seedCode(t, ctx, f.tenant, member, "active", f.clk.Now())
	email := uniqueEmail("claim-mail")
	if rec := f.claim(code, email, password, "203.0.113.180"); rec.Code != http.StatusCreated {
		t.Fatalf("claim status = %d, want 201", rec.Code)
	}
	user, _ := userByEmail(t, ctx, email)
	tok := f.oneLink(t, email, "Confirmá tu correo", "/verificar")
	if row := emailToken(t, ctx, user, tok); row.Purpose != "verify_email" || row.Used != nil {
		t.Fatalf("token row = %+v, want unused verify_email", row)
	}
	// A failed claim sends nothing.
	f.claim(code, uniqueEmail("again"), password, "203.0.113.180")
	f.noMail(t)
}

func TestJSONBodiesAreStrict(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, session := f.signIn(t, ctx, "student")
	for _, path := range []string{"/api/auth/login", "/api/auth/claim", "/api/auth/redeem", "/api/auth/email/verify",
		"/api/auth/link", "/api/auth/link/consume", "/api/auth/password/reset-request", "/api/auth/password/reset"} {
		for _, body := range []string{`{"email":`, `{"unknown":1}`, `{"token":"x"} {}`, `{"email":"` + strings.Repeat("a", 4<<10) + `"}`} {
			rec := f.post(path, body, "203.0.113.190", session)
			wantResponse(t, rec, http.StatusBadRequest, `{"error":"bad_request"}`)
		}
	}
	f.noMail(t)
}

// ce_app may only mark tokens used and change a user's verification and password.
func TestEmailTokenGrantsAreLeastPrivilege(t *testing.T) {
	ctx := integration(t)
	for _, q := range []string{
		`UPDATE users SET email = 'x' WHERE false`,
		`UPDATE email_tokens SET purpose = 'sign_in' WHERE false`,
		`UPDATE email_tokens SET expires_at = now() WHERE false`,
		`DELETE FROM email_tokens WHERE false`,
	} {
		_, err := tdb.App.Exec(ctx, q)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("%s: err = %v, want permission denied", q, err)
		}
	}
	for _, q := range []string{
		`UPDATE users SET email_verified_at = now(), password_hash = 'x' WHERE false`,
		`UPDATE email_tokens SET used_at = now() WHERE false`,
	} {
		if _, err := tdb.App.Exec(ctx, q); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
}
