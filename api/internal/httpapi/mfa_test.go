package httpapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/totp"
)

// testTOTPKey is the fixed TOTP_ENC_KEY of every router under test.
var testTOTPKey = bytes.Repeat([]byte{0x42}, 32)

const (
	mfaRequiredBody = `{"error":"mfa_required"}`
	invalidCodeMFA  = `{"error":"invalid_code"}`
	mfaIP           = "198.51.100.60"
)

var recoveryRE = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{5}-[0-9A-HJKMNP-TV-Z]{5}$`)

// cookieToken returns the session token set by rec, failing unless the cookie has the login attributes.
func cookieToken(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	m := setCookieRE.FindStringSubmatch(rec.Header().Get("Set-Cookie"))
	if m == nil {
		t.Fatalf("Set-Cookie = %q, want match for %s", rec.Header().Get("Set-Cookie"), setCookieRE)
	}
	return m[1]
}

// enroll starts enrollment and returns the decoded secret.
func (f *fixture) enroll(t *testing.T, token string) []byte {
	t.Helper()
	rec := f.post("/api/auth/mfa/enrollment", "", mfaIP, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("enrollment status = %d, body %s; want 200", rec.Code, rec.Body)
	}
	wantNoStore(t, rec)
	var out struct{ Secret, OtpauthUri string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(out.Secret)
	if err != nil || len(secret) != 20 {
		t.Fatalf("secret %q: %d bytes, %v; want 20", out.Secret, len(secret), err)
	}
	return secret
}

// code returns the TOTP of secret at the fixture's clock shifted by steps.
func (f *fixture) code(secret []byte, steps int64) string {
	return totp.Code(secret, totp.StepAt(f.clk.Now())+steps)
}

func codeBody(code string) string { return fmt.Sprintf(`{"code":%q}`, code) }

// confirm finishes enrollment and returns the rotated token and the recovery codes.
func (f *fixture) confirm(t *testing.T, token string, secret []byte) (string, []string) {
	t.Helper()
	rec := f.post("/api/auth/mfa/enrollment/confirm", codeBody(f.code(secret, 0)), mfaIP, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body %s; want 200", rec.Code, rec.Body)
	}
	var out struct{ RecoveryCodes []string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return cookieToken(t, rec), out.RecoveryCodes
}

// signInMFA signs in a new user with role and completes TOTP enrollment, so the returned session is
// two-factor (state satisfied). The clock moves one step so the confirmation code is not reused.
func (f *fixture) signInMFA(t *testing.T, ctx context.Context, role string) (userID, token string, secret []byte, recovery []string) {
	t.Helper()
	userID, token = f.signIn(t, ctx, role)
	secret = f.enroll(t, token)
	token, recovery = f.confirm(t, token, secret)
	f.clk.Advance(totp.Step)
	return userID, token, secret, recovery
}

func meJSON(t *testing.T, f *fixture, token string) (mfa string, verified bool) {
	t.Helper()
	rec := f.do(http.MethodGet, "/api/auth/me", "", mfaIP, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("me status = %d, want 200", rec.Code)
	}
	var me struct {
		Mfa           string
		EmailVerified bool
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	return me.Mfa, me.EmailVerified
}

type totpRow struct {
	Secret    []byte
	Confirmed *time.Time
	LastStep  *int64
}

func readTOTP(t *testing.T, ctx context.Context, user string) (totpRow, bool) {
	t.Helper()
	var r totpRow
	err := tdb.Super.QueryRow(ctx, `SELECT secret_enc, confirmed_at, last_used_step FROM user_totp WHERE user_id = $1`, user).
		Scan(&r.Secret, &r.Confirmed, &r.LastStep)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return r, true
}

// recoveryHashes returns "hex(sha256)|used" for every recovery code of user.
func recoveryHashes(t *testing.T, ctx context.Context, user string) map[string]*time.Time {
	t.Helper()
	rows, err := tdb.Super.Query(ctx, `SELECT code_hash, used_at FROM recovery_codes WHERE user_id = $1`, user)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]*time.Time{}
	for rows.Next() {
		var h []byte
		var used *time.Time
		if err := rows.Scan(&h, &used); err != nil {
			t.Fatal(err)
		}
		out[string(h)] = used
	}
	return out
}

func recoveryHash(code string) string {
	sum := sha256.Sum256([]byte(strings.ReplaceAll(code, "-", "")))
	return string(sum[:])
}

// sessionMFAAt returns mfa_at of the session for token and whether the row exists.
func sessionMFAAt(t *testing.T, ctx context.Context, token string) (*time.Time, bool) {
	t.Helper()
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	sum := sha256.Sum256(raw)
	var at *time.Time
	err := tdb.Super.QueryRow(ctx, `SELECT mfa_at FROM sessions WHERE token_hash = $1`, sum[:]).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return at, true
}

// wantRotated checks that rec set a new two-factor session for user and deleted the old one.
func wantRotated(t *testing.T, ctx context.Context, f *fixture, rec *httptest.ResponseRecorder, old string) string {
	t.Helper()
	tok := cookieToken(t, rec)
	if tok == old {
		t.Fatal("session token was not rotated")
	}
	if _, ok := sessionMFAAt(t, ctx, old); ok {
		t.Fatal("old session row still exists")
	}
	at, ok := sessionMFAAt(t, ctx, tok)
	if !ok {
		t.Fatal("new session row missing")
	}
	wantTime(t, "mfa_at", at, f.clk.Now())
	wantResponse(t, f.do(http.MethodGet, "/api/auth/me", "", mfaIP, old, nil), http.StatusUnauthorized, unauthBody)
	if sessionTenant(t, ctx, tok) != f.tenant {
		t.Fatal("rotated session lost its tenant")
	}
	return tok
}

func TestEnrollmentConfirmAndRecoveryCodes(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	user, token := f.signIn(t, ctx, "school_admin")
	if mfa, _ := meJSON(t, f, token); mfa != "enrollment_required" {
		t.Fatalf("mfa = %s, want enrollment_required", mfa)
	}

	first := f.enroll(t, token)
	row, ok := readTOTP(t, ctx, user)
	if !ok || row.Confirmed != nil || row.LastStep != nil {
		t.Fatalf("user_totp = %+v (exists %v), want one unconfirmed row", row, ok)
	}
	// Stored only encrypted (AES-256-GCM bound to the user), never in plaintext or base32.
	if bytes.Contains(row.Secret, first) || bytes.Contains(row.Secret, []byte(totp.EncodeSecret(first))) {
		t.Fatal("secret stored in plaintext")
	}
	if got, err := totp.Open(testTOTPKey, user, row.Secret); err != nil || !bytes.Equal(got, first) {
		t.Fatalf("stored secret does not decrypt to the shown one: %v", err)
	}
	if mfa, _ := meJSON(t, f, token); mfa != "enrollment_required" {
		t.Fatalf("mfa = %s, want enrollment_required before confirming", mfa)
	}

	// Enrolling again replaces the pending secret: codes of the first one stop working.
	secret := f.enroll(t, token)
	if bytes.Equal(secret, first) {
		t.Fatal("second enrollment returned the same secret")
	}
	rec := f.post("/api/auth/mfa/enrollment/confirm", codeBody(f.code(first, 0)), mfaIP, token)
	wantResponse(t, rec, http.StatusUnprocessableEntity, invalidCodeMFA)
	wantNoStore(t, rec)
	if rec.Header().Get("Set-Cookie") != "" || len(recoveryHashes(t, ctx, user)) != 0 {
		t.Fatal("a wrong code set a cookie or stored recovery codes")
	}
	if row, _ := readTOTP(t, ctx, user); row.Confirmed != nil {
		t.Fatal("a wrong code confirmed the enrollment")
	}

	rec = f.post("/api/auth/mfa/enrollment/confirm", codeBody(f.code(secret, 0)), mfaIP, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body %s", rec.Code, rec.Body)
	}
	wantNoStore(t, rec)
	var out struct {
		RecoveryCodes []string `json:"recoveryCodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	hashes := recoveryHashes(t, ctx, user)
	if len(out.RecoveryCodes) != 10 || len(hashes) != 10 {
		t.Fatalf("recovery codes = %d shown, %d stored; want 10 and 10", len(out.RecoveryCodes), len(hashes))
	}
	for _, c := range out.RecoveryCodes {
		if !recoveryRE.MatchString(c) {
			t.Fatalf("recovery code %q does not match %s", c, recoveryRE)
		}
		if used, ok := hashes[recoveryHash(c)]; !ok || used != nil {
			t.Fatalf("recovery code %s: stored %v, used %v; want only its unused SHA-256", c, ok, used)
		}
	}
	newToken := wantRotated(t, ctx, f, rec, token)
	row, _ = readTOTP(t, ctx, user)
	wantTime(t, "confirmed_at", row.Confirmed, f.clk.Now())
	if row.LastStep == nil || *row.LastStep != totp.StepAt(f.clk.Now()) {
		t.Fatalf("last_used_step = %v, want %d", row.LastStep, totp.StepAt(f.clk.Now()))
	}
	if mfa, _ := meJSON(t, f, newToken); mfa != "satisfied" {
		t.Fatalf("mfa = %s, want satisfied", mfa)
	}
	if rec := f.do(http.MethodGet, "/api/roster", "", mfaIP, newToken, nil); rec.Code != http.StatusOK {
		t.Fatalf("roster with a two-factor session: status = %d, want 200", rec.Code)
	}

	// Shown once: a confirmed user can neither enroll again nor confirm again.
	rec = f.post("/api/auth/mfa/enrollment", "", mfaIP, newToken)
	wantResponse(t, rec, http.StatusConflict, `{"error":"already_enrolled"}`)
	wantNoStore(t, rec)
	f.clk.Advance(totp.Step)
	wantResponse(t, f.post("/api/auth/mfa/enrollment/confirm", codeBody(f.code(secret, 0)), mfaIP, newToken),
		http.StatusConflict, `{"error":"already_enrolled"}`)
	if after, _ := readTOTP(t, ctx, user); !bytes.Equal(after.Secret, row.Secret) || len(recoveryHashes(t, ctx, user)) != 10 {
		t.Fatal("already_enrolled changed the secret or the recovery codes")
	}
}

func TestConfirmWithoutPendingSecretIsInvalid(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	user, token := f.signIn(t, ctx, "officer")
	wantResponse(t, f.post("/api/auth/mfa/enrollment/confirm", codeBody("123456"), mfaIP, token),
		http.StatusUnprocessableEntity, invalidCodeMFA)
	if _, ok := readTOTP(t, ctx, user); ok {
		t.Fatal("user_totp row created")
	}
}

func TestPrivilegedSessionWithoutMFAIsRefusedOnRosterEndpoints(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	member := seedMember(t, ctx, f.tenant, "Ana Acosta", "30111300")
	_, noTOTP := f.signIn(t, ctx, "school_admin")
	user, _, secret, _ := f.signInMFA(t, ctx, "school_admin")
	var email string
	_ = tdb.Super.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, user).Scan(&email)
	pending := f.loginOK(t, email)
	if mfa, _ := meJSON(t, f, pending); mfa != "pending" {
		t.Fatalf("mfa = %s, want pending for a new session of an enrolled admin", mfa)
	}

	for name, token := range map[string]string{"no TOTP": noTOTP, "pending": pending} {
		t.Run(name, func(t *testing.T) {
			for _, req := range []struct{ method, path, body, ct string }{
				{http.MethodGet, "/api/roster", "", ""},
				{http.MethodPost, "/api/roster/import", csvHeader + "Beto Bravo,30111301\n", "text/csv"},
				{http.MethodPost, "/api/roster/" + member + "/access-code", "", ""},
				{http.MethodPost, "/api/roster/access-codes", "", ""},
			} {
				var h map[string]string
				if req.ct != "" {
					h = map[string]string{"Content-Type": req.ct}
				}
				rec := f.do(req.method, req.path, req.body, mfaIP, token, h)
				wantResponse(t, rec, http.StatusForbidden, mfaRequiredBody)
				wantNoStore(t, rec)
			}
			if rows := rosterRows(t, ctx, f.tenant); len(rows) != 1 || tenantCodeCount(t, ctx, f.tenant) != 0 {
				t.Fatalf("roster = %v, codes = %d; want nothing written", rows, tenantCodeCount(t, ctx, f.tenant))
			}
		})
	}

	// After verifying, the same admin gets through.
	rec := f.post("/api/auth/mfa/verify", codeBody(f.code(secret, 0)), mfaIP, pending)
	wantResponse(t, rec, http.StatusNoContent, "")
	verified := wantRotated(t, ctx, f, rec, pending)
	if rec := f.do(http.MethodGet, "/api/roster", "", mfaIP, verified, nil); rec.Code != http.StatusOK {
		t.Fatalf("roster after verify: status = %d, want 200", rec.Code)
	}
}

func TestVerifyAcceptsOneStepOfDriftAndRejectsReplays(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	user, _, secret, _ := f.signInMFA(t, ctx, "officer")
	var email string
	_ = tdb.Super.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, user).Scan(&email)
	f.clk.Advance(5 * time.Minute)

	verify := func(t *testing.T, code string) (*httptest.ResponseRecorder, string) {
		t.Helper()
		tok := f.loginOK(t, email)
		return f.post("/api/auth/mfa/verify", codeBody(code), mfaIP, tok), tok
	}
	for _, tc := range []struct {
		name  string
		steps int64 // relative to now; the clock advances 3 steps after each case
		ok    bool
	}{
		{"two steps back", -2, false},
		{"two steps ahead", 2, false},
		{"previous step", -1, true},
		{"current step", 0, true},
		{"next step", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := f.code(secret, tc.steps)
			rec, tok := verify(t, code)
			wantNoStore(t, rec)
			if !tc.ok {
				wantResponse(t, rec, http.StatusUnprocessableEntity, invalidCodeMFA)
				if rec.Header().Get("Set-Cookie") != "" {
					t.Fatal("failed verify set a cookie")
				}
				if at, ok := sessionMFAAt(t, ctx, tok); !ok || at != nil {
					t.Fatal("failed verify changed the session")
				}
				f.clk.Advance(3 * totp.Step)
				return
			}
			wantResponse(t, rec, http.StatusNoContent, "")
			newTok := wantRotated(t, ctx, f, rec, tok)
			if mfa, _ := meJSON(t, f, newTok); mfa != "satisfied" {
				t.Fatalf("mfa = %s, want satisfied", mfa)
			}
			if row, _ := readTOTP(t, ctx, user); row.LastStep == nil || *row.LastStep != totp.StepAt(f.clk.Now())+tc.steps {
				t.Fatalf("last_used_step = %v, want %d", row.LastStep, totp.StepAt(f.clk.Now())+tc.steps)
			}
			// The same code again, in another session within its window, is a replay.
			rec, _ = verify(t, code)
			wantResponse(t, rec, http.StatusUnprocessableEntity, invalidCodeMFA)
			f.clk.Advance(3 * totp.Step)
		})
	}
}

func TestRecoveryCodeIsSingleUse(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	user, _, _, recovery := f.signInMFA(t, ctx, "school_admin")
	var email string
	_ = tdb.Super.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, user).Scan(&email)

	tok := f.loginOK(t, email)
	for _, body := range []string{`{}`, fmt.Sprintf(`{"code":"123456","recoveryCode":%q}`, recovery[0])} {
		wantResponse(t, f.post("/api/auth/mfa/verify", body, mfaIP, tok), http.StatusBadRequest, `{"error":"bad_request"}`)
	}
	// What a person types: lowercase, with spaces instead of the hyphen.
	typed := strings.ToLower(strings.ReplaceAll(recovery[0], "-", " "))
	rec := f.post("/api/auth/mfa/verify", fmt.Sprintf(`{"recoveryCode":%q}`, typed), mfaIP, tok)
	wantResponse(t, rec, http.StatusNoContent, "")
	wantRotated(t, ctx, f, rec, tok)
	hashes := recoveryHashes(t, ctx, user)
	wantTime(t, "recovery used_at", hashes[recoveryHash(recovery[0])], f.clk.Now())
	for _, c := range recovery[1:] {
		if hashes[recoveryHash(c)] != nil {
			t.Fatalf("recovery code %s marked used", c)
		}
	}

	tok = f.loginOK(t, email)
	rec = f.post("/api/auth/mfa/verify", fmt.Sprintf(`{"recoveryCode":%q}`, recovery[0]), mfaIP, tok)
	wantResponse(t, rec, http.StatusUnprocessableEntity, invalidCodeMFA)
	if at, ok := sessionMFAAt(t, ctx, tok); !ok || at != nil {
		t.Fatal("used recovery code changed the session")
	}
	wantResponse(t, f.post("/api/auth/mfa/verify", `{"recoveryCode":"00000-00000"}`, mfaIP, tok),
		http.StatusUnprocessableEntity, invalidCodeMFA)
}

func TestMFAFailuresAreRateLimitedPerUser(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	user, _, secret, _ := f.signInMFA(t, ctx, "school_admin")
	var email string
	_ = tdb.Super.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, user).Scan(&email)
	tok := f.loginOK(t, email)
	wrong := codeBody(fmt.Sprintf("%06d", (mustAtoi(f.code(secret, 0))+500000)%1000000))

	for range 4 {
		wantResponse(t, f.post("/api/auth/mfa/verify", wrong, mfaIP, tok), http.StatusUnprocessableEntity, invalidCodeMFA)
	}
	// A success does not count and does not reset the failures.
	rec := f.post("/api/auth/mfa/verify", codeBody(f.code(secret, 0)), mfaIP, tok)
	wantResponse(t, rec, http.StatusNoContent, "")
	f.clk.Advance(totp.Step)
	tok = f.loginOK(t, email)
	wantResponse(t, f.post("/api/auth/mfa/verify", wrong, mfaIP, tok), http.StatusUnprocessableEntity, invalidCodeMFA)
	// Five failures: even the right code is refused, from any IP, until the window passes.
	rec = f.post("/api/auth/mfa/verify", codeBody(f.code(secret, 0)), "203.0.113.250", tok)
	wantResponse(t, rec, http.StatusTooManyRequests, tooManyBody)
	if at, _ := sessionMFAAt(t, ctx, tok); at != nil || rec.Header().Get("Set-Cookie") != "" {
		t.Fatal("rate-limited verify changed the session")
	}
	f.clk.Advance(15 * time.Minute)
	wantResponse(t, f.post("/api/auth/mfa/verify", codeBody(f.code(secret, 0)), mfaIP, tok), http.StatusNoContent, "")

	t.Run("confirm shares the limit", func(t *testing.T) {
		_, token := f.signIn(t, ctx, "officer")
		secret := f.enroll(t, token)
		bad := codeBody(fmt.Sprintf("%06d", (mustAtoi(f.code(secret, 0))+500000)%1000000))
		for range 5 {
			wantResponse(t, f.post("/api/auth/mfa/enrollment/confirm", bad, mfaIP, token), http.StatusUnprocessableEntity, invalidCodeMFA)
		}
		wantResponse(t, f.post("/api/auth/mfa/verify", bad, mfaIP, token), http.StatusTooManyRequests, tooManyBody)
		wantResponse(t, f.post("/api/auth/mfa/enrollment/confirm", codeBody(f.code(secret, 0)), mfaIP, token),
			http.StatusTooManyRequests, tooManyBody)
	})
}

func mustAtoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

func TestStudentIsNotRequiredAndUnaffected(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, token := f.signIn(t, ctx, "student")
	if mfa, verified := meJSON(t, f, token); mfa != "not_required" || verified {
		t.Fatalf("me = %s/%v, want not_required/false", mfa, verified)
	}
	wantResponse(t, f.do(http.MethodGet, "/api/roster", "", mfaIP, token, nil), http.StatusForbidden, `{"error":"forbidden"}`)
	_, lone := f.signIn(t, ctx, "")
	if mfa, _ := meJSON(t, f, lone); mfa != "not_required" {
		t.Fatalf("mfa = %s, want not_required without membership", mfa)
	}
}

func TestMFAEndpointsRequireSession(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	for path, body := range map[string]string{
		"/api/auth/mfa/enrollment":         "",
		"/api/auth/mfa/enrollment/confirm": codeBody("123456"),
		"/api/auth/mfa/verify":             codeBody("123456"),
	} {
		rec := f.post(path, body, mfaIP, "")
		wantResponse(t, rec, http.StatusUnauthorized, unauthBody)
		wantNoStore(t, rec)
	}
	_, token := f.signIn(t, ctx, "officer")
	for _, path := range []string{"/api/auth/mfa/enrollment/confirm", "/api/auth/mfa/verify"} {
		for _, body := range []string{`{"code":`, `{"code":"1","x":1}`, `{"code":"1"} {}`, `{"code":"` + strings.Repeat("1", 4<<10) + `"}`} {
			wantResponse(t, f.post(path, body, mfaIP, token), http.StatusBadRequest, `{"error":"bad_request"}`)
		}
	}
}

// ce_app can only replace a pending secret, confirm it, record the last step and mark recovery codes
// used; it can never rewrite owners or hashes, nor upgrade an existing session in place.
func TestMFAGrantsAreLeastPrivilege(t *testing.T) {
	ctx := integration(t)
	for _, q := range []string{
		`UPDATE user_totp SET user_id = user_id WHERE false`,
		`UPDATE recovery_codes SET code_hash = code_hash WHERE false`,
		`UPDATE recovery_codes SET user_id = user_id WHERE false`,
		`UPDATE sessions SET mfa_at = now() WHERE false`,
	} {
		_, err := tdb.App.Exec(ctx, q)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("%s: err = %v, want permission denied", q, err)
		}
	}
	for _, q := range []string{
		`UPDATE user_totp SET secret_enc = secret_enc, confirmed_at = now(), last_used_step = 1 WHERE false`,
		`UPDATE recovery_codes SET used_at = now() WHERE false`,
		`DELETE FROM user_totp WHERE false`,
		`DELETE FROM recovery_codes WHERE false`,
	} {
		if _, err := tdb.App.Exec(ctx, q); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
}

// Rotating the session on two-factor verification must not extend its absolute lifetime.
func TestMFAVerifyKeepsAbsoluteSessionLifetime(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	user, _, secret, _ := f.signInMFA(t, ctx, "school_admin")
	var email string
	if err := tdb.Super.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, user).Scan(&email); err != nil {
		t.Fatal(err)
	}
	f.clk.Advance(time.Minute)
	start := f.clk.Now()
	tok := f.loginOK(t, email)
	f.clk.Advance(11 * time.Hour)

	rec := f.post("/api/auth/mfa/verify", codeBody(f.code(secret, 0)), mfaIP, tok)
	wantResponse(t, rec, http.StatusNoContent, "")

	var expires time.Time
	if err := tdb.Super.QueryRow(ctx,
		`SELECT expires_at FROM sessions WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1`, user).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if want := start.Add(12 * time.Hour); !expires.Equal(want) {
		t.Fatalf("rotated session expires_at = %s, want %s (the original absolute expiry)", expires, want)
	}
}

// Two verifications racing with the same TOTP code: exactly one may succeed (replay protection).
func TestConcurrentSameTOTPCodeSucceedsOnce(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	user, _, secret, _ := f.signInMFA(t, ctx, "school_admin")
	var email string
	if err := tdb.Super.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, user).Scan(&email); err != nil {
		t.Fatal(err)
	}
	f.clk.Advance(time.Minute)
	toks := []string{f.loginOK(t, email), f.loginOK(t, email)}
	code := codeBody(f.code(secret, 0))

	// Hold the row lock so both verifications are in flight together.
	tx, err := tdb.Super.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM user_totp WHERE user_id = $1 FOR UPDATE`, user); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	codes := make([]int, len(toks))
	for i, tok := range toks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = f.post("/api/auth/mfa/verify", code, mfaIP, tok).Code
		}()
	}
	time.Sleep(500 * time.Millisecond)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	ok := 0
	for _, c := range codes {
		if c == http.StatusNoContent {
			ok++
		} else if c != http.StatusUnprocessableEntity {
			t.Fatalf("statuses %v, want one 204 and one 422", codes)
		}
	}
	if ok != 1 {
		t.Fatalf("statuses %v, want exactly one 204", codes)
	}
}
