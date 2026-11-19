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
	"strings"
	"sync"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/httpapi"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/mail"
	"testing"
	"time"
)

const invitationPath = "/api/members/invitations"

// R1 and R2: only an MFA-verified school admin sends an invitation; reissue replaces it.
func TestSchoolInvitationIssuance(t *testing.T) {
	ctx := integration(t)
	s := newSchool(t, ctx)
	_, adminPending := s.signIn(t, ctx, "school_admin")
	_, veedor, _, _ := s.signInMFA(t, ctx, "veedor")
	email := uniqueEmail("invite")
	body := func(mail, role string) string { return fmt.Sprintf(`{"email":%q,"role":%q}`, mail, role) }
	for _, token := range []string{s.officer, s.reader, s.member, veedor} {
		wantResponse(t, s.post(invitationPath, body(email, "veedor"), "198.51.100.88", token), http.StatusForbidden, forbiddenBody)
		s.noMail(t)
	}
	wantResponse(t, s.post(invitationPath, body(email, "veedor"), "198.51.100.88", adminPending), http.StatusForbidden, mfaBody)
	s.noMail(t)
	for _, bad := range []string{"", "bad"} {
		wantResponse(t, s.post(invitationPath, body(bad, "veedor"), "198.51.100.88", s.admin), http.StatusUnprocessableEntity, `{"error":"invalid_email"}`)
	}
	for _, bad := range []string{"officer", "student"} {
		wantResponse(t, s.post(invitationPath, body(email, bad), "198.51.100.88", s.admin), http.StatusUnprocessableEntity, `{"error":"invalid_role"}`)
	}
	s.noMail(t)
	for i, role := range []string{"veedor", "school_admin"} {
		if r := s.post(invitationPath, body(email, role), "198.51.100.88", s.admin); r.Code != http.StatusAccepted {
			t.Fatalf("invite %s: %d %s", role, r.Code, r.Body)
		}
		raw := s.oneLink(t, email, "Invitaci\u00f3n a Centro de Estudiantes", "/invitacion")
		b, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || len(b) != 32 {
			t.Fatalf("invalid token length: %v", err)
		}
		hash := sha256.Sum256(b)
		var gotRole string
		var created, expires time.Time
		var used *time.Time
		if err := tdb.Super.QueryRow(ctx, `SELECT role, created_at, expires_at, used_at FROM school_invitations WHERE token_hash=$1`, hash[:]).Scan(&gotRole, &created, &expires, &used); err != nil {
			t.Fatal(err)
		}
		if gotRole != role || expires.Sub(created) != 48*time.Hour || used != nil {
			t.Fatalf("unexpected invitation: role=%s expires=%v used=%v", gotRole, expires.Sub(created), used)
		}
		if i == 1 {
			var n int
			if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM school_invitations WHERE tenant_id=$1 AND email=$2 AND used_at IS NOT NULL`, s.tenant, email).Scan(&n); err != nil || n != 1 {
				t.Fatalf("previous token: %d %v", n, err)
			}
		}
	}
}

// R3: a valid invitation creates one verified account; bad, used and expired tokens are indistinguishable.
func TestInvitationAcceptanceCreatesAccountOnce(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, admin, _, _ := f.signInMFA(t, ctx, "school_admin")
	email := uniqueEmail("invitee")
	invite := fmt.Sprintf(`{"email":%q,"role":"veedor"}`, email)
	if r := f.post(invitationPath, invite, "198.51.100.81", admin); r.Code != http.StatusAccepted {
		t.Fatalf("invite: %d %s", r.Code, r.Body)
	}
	token := f.oneLink(t, email, "Invitaci\u00f3n a Centro de Estudiantes", "/invitacion")
	accept := func(tok, pw string) *httptest.ResponseRecorder {
		return f.post("/api/auth/invitations/accept", fmt.Sprintf(`{"token":%q,"password":%q}`, tok, pw), "198.51.100.82", "")
	}
	wantResponse(t, accept(token, "short"), http.StatusUnprocessableEntity, `{"error":"weak_password"}`)
	wantResponse(t, accept("missing", password), http.StatusUnprocessableEntity, invalidTokenBody)
	var wg sync.WaitGroup
	results := make([]*httptest.ResponseRecorder, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i] = accept(token, password) }(i)
	}
	wg.Wait()
	created, invalid := 0, 0
	for _, r := range results {
		if r.Code == http.StatusCreated {
			created++
			if setCookieRE.FindStringSubmatch(r.Header().Get("Set-Cookie")) == nil {
				t.Fatalf("missing session cookie")
			}
		} else if r.Code == http.StatusUnprocessableEntity && strings.TrimSpace(r.Body.String()) == invalidTokenBody {
			invalid++
		} else {
			t.Fatalf("accept: %d %s", r.Code, r.Body)
		}
	}
	if created != 1 || invalid != 1 {
		t.Fatalf("created=%d invalid=%d", created, invalid)
	}
	user, hash := userByEmail(t, ctx, email)
	if user == "" {
		t.Fatal("account absent")
	}
	if ok, _ := auth.VerifyPassword(hash, password); !ok {
		t.Fatal("password not stored")
	}
	var count int
	var verified bool
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*), bool_and(u.email_verified_at IS NOT NULL) FROM users u JOIN memberships m ON m.user_id=u.id WHERE u.id=$1 AND m.tenant_id=$2 AND m.role='veedor'`, user, f.tenant).Scan(&count, &verified); err != nil || count != 1 || !verified {
		t.Fatalf("membership=%d verified=%v err=%v", count, verified, err)
	}
	wantResponse(t, accept(token, password), http.StatusUnprocessableEntity, invalidTokenBody)
	email2 := uniqueEmail("expired")
	if r := f.post(invitationPath, fmt.Sprintf(`{"email":%q,"role":"school_admin"}`, email2), "198.51.100.81", admin); r.Code != http.StatusAccepted {
		t.Fatalf("invite expired: %d", r.Code)
	}
	expired := f.oneLink(t, email2, "Invitaci\u00f3n a Centro de Estudiantes", "/invitacion")
	f.clk.Advance(48 * time.Hour)
	wantResponse(t, accept(expired, password), http.StatusUnprocessableEntity, invalidTokenBody)
}

// R13: an existing account keeps its password and gains the role only after login and MFA.
func TestInvitationAcceptanceExistingAccountRequiresLoginAndMFA(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, admin, _, _ := f.signInMFA(t, ctx, "school_admin")
	email := uniqueEmail("existing")
	user := f.seedUser(t, ctx, email, "student")
	_, oldHash := userByEmail(t, ctx, email)
	r := f.post(invitationPath, fmt.Sprintf(`{"email":%q,"role":"school_admin"}`, email), "198.51.100.83", admin)
	if r.Code != http.StatusAccepted {
		t.Fatalf("invite: %d %s", r.Code, r.Body)
	}
	token := f.oneLink(t, email, "Invitaci\u00f3n a Centro de Estudiantes", "/invitacion")
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	sum := sha256.Sum256(raw)
	unchanged := func() {
		t.Helper()
		var roles int
		var used *time.Time
		if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM memberships WHERE user_id=$1 AND tenant_id=$2 AND role='school_admin'`, user, f.tenant).Scan(&roles); err != nil || roles != 0 {
			t.Fatalf("roles=%d err=%v", roles, err)
		}
		if err := tdb.Super.QueryRow(ctx, `SELECT used_at FROM school_invitations WHERE token_hash=$1`, sum[:]).Scan(&used); err != nil || used != nil {
			t.Fatalf("used=%v err=%v", used, err)
		}
	}
	wantResponse(t, f.login(email, "wrong", "198.51.100.84"), http.StatusUnauthorized, invalidBody)
	unchanged()
	body := fmt.Sprintf(`{"token":%q,"password":%q}`, token, "must not overwrite password")
	wantResponse(t, f.post("/api/auth/invitations/accept", body, "198.51.100.84", ""), http.StatusUnauthorized, `{"error":"login_required"}`)
	unchanged()
	pending := f.loginOK(t, email)
	wantResponse(t, f.post("/api/auth/invitations/accept", body, "198.51.100.84", pending), http.StatusForbidden, mfaBody)
	unchanged()
	secret := f.enroll(t, pending)
	verified, _ := f.confirm(t, pending, secret)
	accepted := f.post("/api/auth/invitations/accept", body, "198.51.100.84", verified)
	if accepted.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", accepted.Code, accepted.Body)
	}
	_, afterHash := userByEmail(t, ctx, email)
	if afterHash != oldHash {
		t.Fatal("existing password changed")
	}
	var count int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM memberships WHERE user_id=$1 AND tenant_id=$2 AND role='school_admin'`, user, f.tenant).Scan(&count); err != nil || count != 1 {
		t.Fatalf("roles=%d err=%v", count, err)
	}
	wantResponse(t, f.post("/api/auth/invitations/accept", body, "198.51.100.84", verified), http.StatusUnprocessableEntity, invalidTokenBody)
}

// R3: malformed invitation links use the common invalid-token response without reaching a database.
func TestMalformedInvitationTokenResponse(t *testing.T) {
	h := httpapi.NewRouter(httpapi.Deps{Now: func() time.Time { return time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC) }, AccessCodeKey: testKey, TOTPKey: testTOTPKey, Background: func(fn func()) { fn() }})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/invitations/accept", strings.NewReader(`{"token":"bad","password":"long enough password"}`))
	req.RemoteAddr = "198.51.100.86:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	wantResponse(t, rec, http.StatusUnprocessableEntity, invalidTokenBody)
	for i := 0; i < 50; i++ {
		req = httptest.NewRequest(http.MethodPost, "/api/auth/invitations/accept", strings.NewReader(`{"token":"bad","password":"long enough password"}`))
		req.RemoteAddr = "198.51.100.86:1234"
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if i < 49 {
			wantResponse(t, rec, http.StatusUnprocessableEntity, invalidTokenBody)
		} else {
			wantResponse(t, rec, http.StatusTooManyRequests, tooManyBody)
		}
	}
}

type invitationTestSender struct {
	messages []mail.Message
	fail     bool
}

func (s *invitationTestSender) Send(_ context.Context, msg mail.Message) error {
	s.messages = append(s.messages, msg)
	if s.fail {
		return errors.New(msg.Text + msg.To)
	}
	return nil
}

// R2: an SMTP failure does not roll back an invitation or leak its link, and it can be reissued.
func TestInvitationMailFailureAndResend(t *testing.T) {
	ctx := integration(t)
	sender := &invitationTestSender{fail: true}
	f := newFixtureWith(t, ctx, func(d *httpapi.Deps) { d.Mail = sender })
	_, admin, _, _ := f.signInMFA(t, ctx, "school_admin")
	email := uniqueEmail("send-fail")
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	body := fmt.Sprintf(`{"email":%q,"role":"veedor"}`, email)
	if r := f.post(invitationPath, body, "198.51.100.91", admin); r.Code != http.StatusAccepted {
		t.Fatalf("invite: %d", r.Code)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("messages=%d", len(sender.messages))
	}
	parts := strings.Split(sender.messages[0].Text, "#token=")
	if len(parts) != 2 {
		t.Fatal("missing fragment token")
	}
	token := strings.Fields(parts[1])[0]
	if strings.Contains(logs.String(), token) || strings.Contains(logs.String(), email) {
		t.Fatal("invitation data leaked to log")
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		t.Fatal("invalid token")
	}
	hash := sha256.Sum256(raw)
	var used *time.Time
	if err := tdb.Super.QueryRow(ctx, `SELECT used_at FROM school_invitations WHERE token_hash=$1`, hash[:]).Scan(&used); err != nil || used != nil {
		t.Fatalf("invitation lost or used: %v %v", used, err)
	}
	sender.fail = false
	if r := f.post(invitationPath, body, "198.51.100.91", admin); r.Code != http.StatusAccepted {
		t.Fatalf("resend: %d", r.Code)
	}
	if len(sender.messages) != 2 {
		t.Fatalf("messages=%d", len(sender.messages))
	}
	if err := tdb.Super.QueryRow(ctx, `SELECT used_at FROM school_invitations WHERE token_hash=$1`, hash[:]).Scan(&used); err != nil || used == nil {
		t.Fatalf("previous invitation not invalidated: %v %v", used, err)
	}
}
