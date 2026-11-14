package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/totp"
)

const changedPassword = "una clave nueva y larga"

func changeBody(current, next string) string {
	return fmt.Sprintf(`{"currentPassword":%q,"newPassword":%q}`, current, next)
}

func (f *fixture) changePassword(token, current, next string) *httptest.ResponseRecorder {
	return f.post("/api/auth/password", changeBody(current, next), "198.51.100.80", token)
}

func passwordHash(t *testing.T, ctx context.Context, user string) string {
	t.Helper()
	var h string
	if err := tdb.Super.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, user).Scan(&h); err != nil {
		t.Fatal(err)
	}
	return h
}

func userEmail(t *testing.T, ctx context.Context, user string) string {
	t.Helper()
	var email string
	if err := tdb.Super.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, user).Scan(&email); err != nil {
		t.Fatal(err)
	}
	return email
}

// unchanged fails unless the password, the session and the emailed link of user are as before.
func unchanged(t *testing.T, ctx context.Context, f *fixture, user, hash, token, link string, sessions int) {
	t.Helper()
	if passwordHash(t, ctx, user) != hash {
		t.Fatal("password hash changed")
	}
	if n := sessionCount(t, ctx, user); n != sessions {
		t.Fatalf("sessions = %d, want %d", n, sessions)
	}
	if rec := f.do(http.MethodGet, "/api/auth/me", "", mfaIP, token, nil); rec.Code != http.StatusOK {
		t.Fatalf("me with the session = %d, want 200", rec.Code)
	}
	if link != "" && emailToken(t, ctx, user, link).Used != nil {
		t.Fatal("pending emailed link was used")
	}
}

// R1 and R4: 204, the other sessions are deleted, this one is rotated with the same absolute expiry
// and the pending emailed links are used.
func TestChangePassword(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	email := uniqueEmail("change")
	user := f.seedUser(t, ctx, email, "student")

	wantResponse(t, f.changePassword("", password, changedPassword), http.StatusUnauthorized, unauthBody)

	start := f.clk.Now()
	tok := f.loginOK(t, email)
	other := f.loginOK(t, email)
	wantResponse(t, f.post("/api/auth/link", emailBody(email), "198.51.100.81", ""), http.StatusAccepted, "")
	signInLink := f.oneLink(t, email, "Tu enlace para ingresar", "/ingresar")
	wantResponse(t, f.post("/api/auth/password/reset-request", emailBody(email), "198.51.100.81", ""), http.StatusAccepted, "")
	resetLink := f.oneLink(t, email, "Restablecer tu contraseña", "/restablecer")
	f.clk.Advance(time.Hour)

	rec := f.changePassword(tok, password, changedPassword)
	wantResponse(t, rec, http.StatusNoContent, "")
	wantNoStore(t, rec)
	// Rotated: a new cookie for the remaining 11 hours of the original session.
	newTok := cookieWithMaxAge(t, rec, 11*3600)
	if newTok == tok {
		t.Fatal("session token was not rotated")
	}
	if n := sessionCount(t, ctx, user); n != 1 {
		t.Fatalf("sessions = %d, want 1 (the rotated one)", n)
	}
	var expires time.Time
	if err := tdb.Super.QueryRow(ctx, `SELECT expires_at FROM sessions WHERE user_id = $1`, user).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if want := start.Add(12 * time.Hour); !expires.Equal(want) {
		t.Fatalf("rotated session expires_at = %s, want %s (the original absolute expiry)", expires, want)
	}
	for name, old := range map[string]string{"old": tok, "other": other} {
		if rec := f.do(http.MethodGet, "/api/auth/me", "", mfaIP, old, nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("me with the %s session = %d, want 401", name, rec.Code)
		}
	}
	if rec := f.do(http.MethodGet, "/api/auth/me", "", mfaIP, newTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("me with the rotated session = %d, want 200", rec.Code)
	}
	for _, link := range []string{signInLink, resetLink} {
		wantTime(t, "used_at", emailToken(t, ctx, user, link).Used, f.clk.Now())
	}
	wantResponse(t, f.post("/api/auth/link/consume", tokenBody(signInLink), "198.51.100.82", ""),
		http.StatusUnprocessableEntity, invalidTokenBody)
	wantResponse(t, f.login(email, password, "198.51.100.82"), http.StatusUnauthorized, invalidBody)
	wantResponse(t, f.login(email, changedPassword, "198.51.100.82"), http.StatusNoContent, "")
}

// R2: a wrong current password changes nothing and counts as a failed sign-in of the user's email.
func TestChangePasswordWithWrongCurrentPasswordCountsAsFailedLogin(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	email := uniqueEmail("change-wrong")
	user := f.seedUser(t, ctx, email, "student")
	tok := f.loginOK(t, email)
	wantResponse(t, f.post("/api/auth/link", emailBody(email), "198.51.100.83", ""), http.StatusAccepted, "")
	link := f.oneLink(t, email, "Tu enlace para ingresar", "/ingresar")
	hash := passwordHash(t, ctx, user)

	// Four wrong current passwords and one failed sign-in: five failures of this email.
	for range 4 {
		rec := f.changePassword(tok, "no es la clave", changedPassword)
		wantResponse(t, rec, http.StatusUnprocessableEntity, invalidBody)
		wantNoStore(t, rec)
		unchanged(t, ctx, f, user, hash, tok, link, 1)
	}
	wantResponse(t, f.login(email, "tampoco es la clave", "198.51.100.84"), http.StatusUnauthorized, invalidBody)

	// The sixth attempt is refused even with the right password, here and on sign-in.
	rec := f.changePassword(tok, password, changedPassword)
	wantResponse(t, rec, http.StatusTooManyRequests, tooManyBody)
	wantNoStore(t, rec)
	unchanged(t, ctx, f, user, hash, tok, link, 1)
	wantResponse(t, f.login(email, password, "198.51.100.85"), http.StatusTooManyRequests, tooManyBody)

	f.clk.Advance(15 * time.Minute)
	wantResponse(t, f.changePassword(tok, password, changedPassword), http.StatusNoContent, "")
}

// R3: the new password follows auth.ValidatePassword and must differ from the current one.
func TestChangePasswordRejectsWeakOrSamePassword(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	email := uniqueEmail("change-weak")
	user := f.seedUser(t, ctx, email, "student")
	tok := f.loginOK(t, email)
	wantResponse(t, f.post("/api/auth/link", emailBody(email), "198.51.100.86", ""), http.StatusAccepted, "")
	link := f.oneLink(t, email, "Tu enlace para ingresar", "/ingresar")
	hash := passwordHash(t, ctx, user)

	for _, tc := range []struct{ next, body string }{
		{"corta", `{"error":"weak_password"}`},
		{strings.Repeat("ñ", 9), `{"error":"weak_password"}`},
		{strings.Repeat("a", 129), `{"error":"weak_password"}`},
		{password, `{"error":"same_password"}`},
	} {
		rec := f.changePassword(tok, password, tc.next)
		wantResponse(t, rec, http.StatusUnprocessableEntity, tc.body)
		wantNoStore(t, rec)
		unchanged(t, ctx, f, user, hash, tok, link, 1)
	}
	// Edges of 10 to 128 characters are accepted.
	tok = cookieToken(t, f.changePassword(tok, password, strings.Repeat("ñ", 10)))
	wantResponse(t, f.changePassword(tok, strings.Repeat("ñ", 10), strings.Repeat("a", 128)), http.StatusNoContent, "")
}

// R5: a school admin or officer needs a session that completed two-factor authentication.
func TestChangePasswordNeedsTwoFactorForPrivilegedRoles(t *testing.T) {
	ctx := integration(t)
	for _, role := range []string{"school_admin", "officer"} {
		t.Run(role, func(t *testing.T) {
			f := newFixture(t, ctx)

			// Enrollment required: no TOTP yet.
			user, tok := f.signIn(t, ctx, role)
			hash := passwordHash(t, ctx, user)
			rec := f.changePassword(tok, password, changedPassword)
			wantResponse(t, rec, http.StatusForbidden, mfaRequiredBody)
			wantNoStore(t, rec)
			unchanged(t, ctx, f, user, hash, tok, "", 1)

			// Pending: TOTP confirmed but this session has not verified it.
			user, mfaTok, _, _ := f.signInMFA(t, ctx, role)
			hash = passwordHash(t, ctx, user)
			pending := f.loginOK(t, userEmail(t, ctx, user))
			if state, _ := meJSON(t, f, pending); state != "pending" {
				t.Fatalf("mfa = %q, want pending", state)
			}
			wantResponse(t, f.changePassword(pending, password, changedPassword), http.StatusForbidden, mfaRequiredBody)
			unchanged(t, ctx, f, user, hash, pending, "", 2)

			// Satisfied: allowed, and the rotated session keeps the second factor.
			rec = f.changePassword(mfaTok, password, changedPassword)
			wantResponse(t, rec, http.StatusNoContent, "")
			// signInMFA moved the clock one TOTP step after the session started.
			if state, _ := meJSON(t, f, cookieWithMaxAge(t, rec, 12*3600-int(totp.Step/time.Second))); state != "satisfied" {
				t.Fatalf("mfa after the change = %q, want satisfied", state)
			}
		})
	}
}
