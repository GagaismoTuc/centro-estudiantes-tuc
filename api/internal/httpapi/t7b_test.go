package httpapi_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// failOn makes every row of table matching the trigger condition when (for example
// "NEW.user_id = '…'") fail at timing ("BEFORE INSERT", "BEFORE DELETE", …) until the returned
// function is called or the test ends.
func failOn(t *testing.T, ctx context.Context, timing, table, when string) (stop func()) {
	t.Helper()
	name := "t7b_fail_" + strings.ToLower(rand.Text())
	for _, q := range []string{
		`CREATE FUNCTION ` + name + `() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected'; END $$`,
		`CREATE TRIGGER ` + name + ` ` + timing + ` ON ` + table + ` FOR EACH ROW WHEN (` + when + `) EXECUTE FUNCTION ` + name + `()`,
	} {
		if _, err := tdb.Super.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	stop = func() {
		_, _ = tdb.Super.Exec(context.Background(), `DROP TRIGGER IF EXISTS `+name+` ON `+table)
		_, _ = tdb.Super.Exec(context.Background(), `DROP FUNCTION IF EXISTS `+name+`()`)
	}
	t.Cleanup(stop)
	return stop
}

// Redeem adds the membership, consumes the code and rotates the session in one transaction: when
// any step fails nothing changes and the old session keeps working. Server errors never count in
// the per-IP limit (51 failures > 50).
func TestRedeemIsAtomicAndServerErrorsDoNotCount(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	userID, token := f.signIn(t, ctx, "officer")
	other := newTenant(t, ctx)
	member := seedMember(t, ctx, other, "Ana Acosta", "30111290")
	code := seedCode(t, ctx, other, member, "active", f.clk.Now())
	stop := failOn(t, ctx, "BEFORE INSERT", "sessions", fmt.Sprintf("NEW.user_id = '%s'", userID))

	for i := range 51 {
		rec := f.redeem(code, token)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("attempt %d: status = %d (%s), want 500", i+1, rec.Code, rec.Body)
		}
		wantResponse(t, rec, http.StatusInternalServerError, internalBody)
	}
	if got := memberAccounts(t, ctx, member); len(got) != 0 {
		t.Fatalf("memberships = %q, want none", got)
	}
	wantCodeUsed(t, ctx, member, nil)
	if got := sessionTenant(t, ctx, token); got != f.tenant || sessionCount(t, ctx, userID) != 1 {
		t.Fatalf("session tenant = %s, want %s (old session kept)", got, f.tenant)
	}
	if got := f.do(http.MethodGet, "/api/auth/me", "", "192.0.2.90", token, nil).Code; got != http.StatusOK {
		t.Fatalf("old session: /auth/me status = %d, want 200", got)
	}

	stop()
	rec := f.redeem(code, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("after the failure is gone: status = %d (%s), want 200", rec.Code, rec.Body)
	}
	if len(memberAccounts(t, ctx, member)) != 1 {
		t.Fatal("membership not added")
	}
}

// Dropping the session the claim request arrived with is part of the claim: if it fails, no account
// is created, the code stays usable and the answer is 500 (never counted by the limiter).
func TestClaimFailsWhenThePreviousSessionCannotBeDropped(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	prevUser, old := f.signIn(t, ctx, "officer")
	member := seedMember(t, ctx, f.tenant, "Ana Acosta", "30111291")
	code := seedCode(t, ctx, f.tenant, member, "active", f.clk.Now())
	stop := failOn(t, ctx, "BEFORE DELETE", "sessions", fmt.Sprintf("OLD.user_id = '%s'", prevUser))
	email := uniqueEmail("claim-drop")
	body := fmt.Sprintf(`{"code":%q,"email":%q,"password":%q}`, code, email, password)

	for i := range 51 {
		rec := f.do(http.MethodPost, "/api/auth/claim", body, "192.0.2.150", old, nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("attempt %d: status = %d (%s), want 500", i+1, rec.Code, rec.Body)
		}
		wantResponse(t, rec, http.StatusInternalServerError, internalBody)
		if sc := rec.Header().Get("Set-Cookie"); sc != "" {
			t.Fatalf("Set-Cookie = %q, want none", sc)
		}
	}
	f.noMail(t)
	if id, _ := userByEmail(t, ctx, email); id != "" {
		t.Fatal("user created")
	}
	if got := memberAccounts(t, ctx, member); len(got) != 0 {
		t.Fatalf("memberships = %q, want none", got)
	}
	wantCodeUsed(t, ctx, member, nil)
	if _, ok := sessionMFAAt(t, ctx, old); !ok {
		t.Fatal("previous session deleted")
	}

	stop()
	rec := f.do(http.MethodPost, "/api/auth/claim", body, "192.0.2.150", old, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("after the failure is gone: status = %d (%s), want 201", rec.Code, rec.Body)
	}
	f.mail.take()
	if _, ok := sessionMFAAt(t, ctx, old); ok {
		t.Fatal("previous session still exists")
	}
}

// A server error while consuming a sign-in link does not spend the per-IP budget nor the token.
func TestLinkConsumeServerErrorsDoNotCount(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	email := uniqueEmail("err-consume")
	user := f.seedUser(t, ctx, email, "student")
	f.post("/api/auth/link", emailBody(email), "203.0.113.232", "")
	tok := f.oneLink(t, email, "Tu enlace para ingresar", "/ingresar")
	stop := failOn(t, ctx, "BEFORE UPDATE", "users", fmt.Sprintf("NEW.id = '%s'", user))
	for range 51 {
		wantResponse(t, f.post("/api/auth/link/consume", tokenBody(tok), "203.0.113.232", ""),
			http.StatusInternalServerError, internalBody)
	}
	stop()
	wantResponse(t, f.post("/api/auth/link/consume", tokenBody(tok), "203.0.113.232", ""), http.StatusNoContent, "")
}

// A server error while verifying a second factor does not spend the per-user budget (5).
func TestMFAVerifyServerErrorsDoNotCount(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	user, _, secret, _ := f.signInMFA(t, ctx, "officer")
	var email string
	_ = tdb.Super.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, user).Scan(&email)
	pending := f.loginOK(t, email)
	stop := failOn(t, ctx, "BEFORE INSERT", "sessions", fmt.Sprintf("NEW.user_id = '%s'", user))
	code := f.code(secret, 0)
	for range 6 {
		wantResponse(t, f.post("/api/auth/mfa/verify", codeBody(code), mfaIP, pending), http.StatusInternalServerError, internalBody)
	}
	if at, ok := sessionMFAAt(t, ctx, pending); !ok || at != nil {
		t.Fatal("failed verify changed the pending session")
	}
	stop()
	wantResponse(t, f.post("/api/auth/mfa/verify", codeBody(code), mfaIP, pending), http.StatusNoContent, "")
}

// countingReader counts the bytes read from it.
type countingReader struct {
	r io.Reader
	n atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// The import is authorized (session, school_admin, second factor) before its body is read.
func TestRosterImportAuthorizesBeforeReadingTheBody(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, student := f.signIn(t, ctx, "student")
	_, pendingAdmin := f.signIn(t, ctx, "school_admin")                      // no second factor yet
	big := csvHeader + strings.Repeat("Ana Acosta,40111222\n", (1<<20)/20+1) // over 1 MiB
	for _, tc := range []struct {
		name, token string
		status      int
		body        string
	}{
		{"no session", "", http.StatusUnauthorized, unauthBody},
		{"student", student, http.StatusForbidden, `{"error":"forbidden"}`},
		{"admin without second factor", pendingAdmin, http.StatusForbidden, mfaRequiredBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &countingReader{r: strings.NewReader(big)}
			req := httptest.NewRequest(http.MethodPost, "/api/roster/import", body)
			req.RemoteAddr = "198.51.100.13:40000"
			req.Header.Set("Content-Type", "text/csv")
			if tc.token != "" {
				req.AddCookie(&http.Cookie{Name: cookieName, Value: tc.token})
			}
			rec := httptest.NewRecorder()
			f.h.ServeHTTP(rec, req)
			wantResponse(t, rec, tc.status, tc.body)
			if n := body.n.Load(); n != 0 {
				t.Fatalf("read %d body bytes before authorizing, want 0", n)
			}
		})
	}
	if got := rosterRows(t, ctx, f.tenant); len(got) != 0 {
		t.Fatalf("roster = %q, want empty", got)
	}
}

// Argentine Excel saves "CSV" with semicolons; the delimiter is taken from the header line.
func TestRosterImportAcceptsSemicolonCSV(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, token, _, _ := f.signInMFA(t, ctx, "school_admin")
	body := "\uFEFFfull_name;document_number\r\n" +
		"Pérez, Juan;40.111.222\r\n" +
		"Beto Bravo;9.876.543\r\n" +
		"\"Ana; Acosta\";30111222\r\n"
	wantResponse(t, f.importCSV(token, body), http.StatusOK, `{"inserted":3,"skipped":0}`)
	want := []string{"Ana; Acosta|30111222", "Pérez, Juan|40111222", "Beto Bravo|9876543"}
	if got := rosterRows(t, ctx, f.tenant); !slices.Equal(got, want) {
		t.Fatalf("roster = %q, want %q", got, want)
	}
	// A semicolon file is still validated row by row.
	rec := f.importCSV(token, "full_name;document_number\nEva;123\n")
	wantResponse(t, rec, http.StatusUnprocessableEntity,
		`{"error":"invalid_rows","rows":[{"line":2,"reason":"document_number must have 7 to 9 digits"}]}`)
}

// Names may not hide text direction overrides or zero-width characters (Unicode category Cf), and
// document numbers accept dots only as thousands separators.
func TestRosterImportRejectsInvisibleCharactersAndMisplacedDots(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, token, _, _ := f.signInMFA(t, ctx, "school_admin")
	const invisible = "full_name contains invisible characters"
	const dni = "document_number must have 7 to 9 digits"
	rows := []struct{ name, doc, reason string }{
		{"Ana \u202Eatsoca", "30111301", invisible}, // right-to-left override
		{"Beto\u2066 Bravo", "30111302", invisible}, // left-to-right isolate
		{"Car\u200Bla", "30111303", invisible},      // zero-width space
		{"Dani\u202A", "30111304", invisible},       // left-to-right embedding
		{"Eva\u2069", "30111305", invisible},        // pop directional isolate
		{"Fede", "1.2.3.4.5.6.7", dni},
		{"Gabi", "40.111222", dni},
		{"Hugo", "4011.1222", dni},
		{"Ines", "40.111.2222", dni},
		{"Juan", "9 876 543", dni},
		{"Karina", "40.111.222", ""},
		{"Lucas", "9.876.543", ""},
		{"Mora", "123.456.789", ""},
		{"Nico", "40111223", ""},
	}
	var body strings.Builder
	body.WriteString(csvHeader)
	var want []string
	for i, r := range rows {
		fmt.Fprintf(&body, "%s,%s\n", r.name, r.doc)
		if r.reason != "" {
			want = append(want, fmt.Sprintf(`{"line":%d,"reason":%q}`, i+2, r.reason))
		}
	}
	wantResponse(t, f.importCSV(token, body.String()), http.StatusUnprocessableEntity,
		`{"error":"invalid_rows","rows":[`+strings.Join(want, ",")+`]}`)
	if got := rosterRows(t, ctx, f.tenant); len(got) != 0 {
		t.Fatalf("roster = %q, want empty", got)
	}
	var ok strings.Builder
	ok.WriteString(csvHeader)
	for _, r := range rows {
		if r.reason == "" {
			fmt.Fprintf(&ok, "%s,%s\n", r.name, r.doc)
		}
	}
	wantResponse(t, f.importCSV(token, ok.String()), http.StatusOK, `{"inserted":4,"skipped":0}`)
}
