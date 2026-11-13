package httpapi_test

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
)

// testKey is the fixed ACCESS_CODE_KEY of every router under test.
var testKey = []byte("0123456789abcdef0123456789abcdef")

var codeRE = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}$`)

const csvHeader = "full_name,document_number\n"

// uniqueEmail keeps tests re-runnable against the same database.
func uniqueEmail(prefix string) string {
	return prefix + "-" + strings.ToLower(rand.Text()) + "@example.com"
}

// signIn creates a user with role in the fixture's tenant ("" = no membership) and signs in.
func (f *fixture) signIn(t *testing.T, ctx context.Context, role string) (userID, token string) {
	t.Helper()
	email := uniqueEmail("u")
	userID = f.seedUser(t, ctx, email, role)
	return userID, f.loginOK(t, email)
}

func newTenant(t *testing.T, ctx context.Context) string {
	t.Helper()
	var id string
	if err := tdb.Super.QueryRow(ctx,
		`INSERT INTO organizations (kind, name) VALUES ('school', $1) RETURNING id::text`, t.Name()+" B").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func seedMember(t *testing.T, ctx context.Context, tenant, name, doc string) string {
	t.Helper()
	var id string
	if err := tdb.Super.QueryRow(ctx,
		`INSERT INTO roster_members (tenant_id, full_name, document_number) VALUES ($1, $2, $3) RETURNING id::text`,
		tenant, name, doc).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// seedCode stores an access code; state is "active", "expired", "used" or "revoked".
func seedCode(t *testing.T, ctx context.Context, tenant, member, state string, now time.Time) {
	t.Helper()
	hash := make([]byte, 32)
	_, _ = rand.Read(hash)
	created, expires := now.Add(-time.Hour), now.Add(time.Hour)
	var used, revoked *time.Time
	switch state {
	case "expired":
		created, expires = now.Add(-31*24*time.Hour), now.Add(-24*time.Hour)
	case "used":
		used = &now
	case "revoked":
		revoked = &now
	}
	if _, err := tdb.Super.Exec(ctx, `
		INSERT INTO access_codes (tenant_id, roster_member_id, code_hash, created_at, expires_at, used_at, revoked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, tenant, member, hash, created, expires, used, revoked); err != nil {
		t.Fatal(err)
	}
}

// linkAccount gives a roster member a student account.
func linkAccount(t *testing.T, ctx context.Context, tenant, member string) {
	t.Helper()
	var user string
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id::text`,
		uniqueEmail("student")).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if _, err := tdb.Super.Exec(ctx,
		`INSERT INTO memberships (tenant_id, user_id, role, roster_member_id) VALUES ($1, $2, 'student', $3)`,
		tenant, user, member); err != nil {
		t.Fatal(err)
	}
}

// rosterRows reads a tenant's roster as superuser: "name|document" ordered by document.
func rosterRows(t *testing.T, ctx context.Context, tenant string) []string {
	t.Helper()
	var rows []string
	if err := tdb.Super.QueryRow(ctx, `
		SELECT coalesce(array_agg(full_name || '|' || document_number ORDER BY document_number), '{}')
		FROM roster_members WHERE tenant_id = $1`, tenant).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

type codeRow struct {
	Hash             []byte
	Created, Expires time.Time
	Used, Revoked    *time.Time
}

func codeRows(t *testing.T, ctx context.Context, member string) []codeRow {
	t.Helper()
	rows, err := tdb.Super.Query(ctx, `
		SELECT code_hash, created_at, expires_at, used_at, revoked_at FROM access_codes
		WHERE roster_member_id = $1 ORDER BY created_at`, member)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []codeRow
	for rows.Next() {
		var r codeRow
		if err := rows.Scan(&r.Hash, &r.Created, &r.Expires, &r.Used, &r.Revoked); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func tenantCodeCount(t *testing.T, ctx context.Context, tenant string) int {
	t.Helper()
	var n int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM access_codes WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *fixture) importCSV(token, body string) *httptest.ResponseRecorder {
	return f.do(http.MethodPost, "/api/roster/import", body, "198.51.100.10", token, map[string]string{"Content-Type": "text/csv"})
}

func wantNoStore(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
}

// wantCodeHash checks that the only trace of code in the database is HMAC-SHA256(testKey, normalized code).
func wantCodeHash(t *testing.T, ctx context.Context, stored []byte, code string) {
	t.Helper()
	normalized := strings.ReplaceAll(code, "-", "")
	m := hmac.New(sha256.New, testKey)
	m.Write([]byte(normalized))
	if !hmac.Equal(stored, m.Sum(nil)) {
		t.Fatalf("code_hash is not HMAC-SHA256(key, %q)", normalized)
	}
	var leaks int
	if err := tdb.Super.QueryRow(ctx, `
		SELECT count(*) FROM access_codes a
		WHERE row_to_json(a)::text LIKE '%' || $1 || '%' OR row_to_json(a)::text LIKE '%' || $2 || '%'
		   OR position(convert_to($1, 'UTF8') in a.code_hash) > 0`, normalized, code).Scan(&leaks); err != nil {
		t.Fatal(err)
	}
	if leaks != 0 {
		t.Fatal("plaintext code found in access_codes")
	}
}

func TestRosterImportInsertsAndSkipsExisting(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, token := f.signIn(t, ctx, "school_admin")
	seedMember(t, ctx, f.tenant, "Carla Castro", "30123456")

	body := "\uFEFF Full_Name , DOCUMENT_NUMBER \r\n" +
		"Ana Acosta,40.111.222\r\n" +
		"  Beto Bravo  , 9 876 543\n" +
		"\"Pérez, Juan\",22333444\n" +
		"\n" +
		"Otra Carla,30123456\n"
	rec := f.importCSV(token, body)
	wantResponse(t, rec, http.StatusOK, `{"inserted":3,"skipped":1}`)
	wantNoStore(t, rec)
	want := []string{"Pérez, Juan|22333444", "Carla Castro|30123456", "Ana Acosta|40111222", "Beto Bravo|9876543"}
	if got := rosterRows(t, ctx, f.tenant); !slices.Equal(got, want) {
		t.Fatalf("roster = %q, want %q", got, want)
	}

	t.Run("5000 rows is the limit", func(t *testing.T) {
		g := newFixture(t, ctx)
		_, token := g.signIn(t, ctx, "school_admin")
		var b strings.Builder
		b.WriteString(csvHeader)
		for i := range 5000 {
			fmt.Fprintf(&b, "Alumno %d,%d\n", i, 10000000+i)
		}
		wantResponse(t, g.importCSV(token, b.String()), http.StatusOK, `{"inserted":5000,"skipped":0}`)
		if n := len(rosterRows(t, ctx, g.tenant)); n != 5000 {
			t.Fatalf("rows = %d, want 5000", n)
		}
	})
}

func TestRosterImportRejectsInvalidFilesAtomically(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, token := f.signIn(t, ctx, "school_admin")
	seedMember(t, ctx, f.tenant, "Carla Castro", "30123456")
	before := rosterRows(t, ctx, f.tenant)

	var tooMany strings.Builder
	tooMany.WriteString(csvHeader)
	for i := range 5001 {
		fmt.Fprintf(&tooMany, "Alumno %d,%d\n", i, 10000000+i)
	}
	for _, tc := range []struct {
		name, contentType, body string
		status                  int
		want                    string
	}{
		{"invalid rows", "text/csv",
			csvHeader + "Ana,123\n,40111222\nBeto,40.111.22a\nCarla,30111222,extra\nDani,30111223\nEva,1234567890\n",
			http.StatusUnprocessableEntity, `{"error":"invalid_rows","rows":[` +
				`{"line":2,"reason":"document_number must have 7 to 9 digits"},` +
				`{"line":3,"reason":"full_name is required"},` +
				`{"line":4,"reason":"document_number must have 7 to 9 digits"},` +
				`{"line":5,"reason":"expected 2 fields"},` +
				`{"line":7,"reason":"document_number must have 7 to 9 digits"}]}`},
		{"duplicate in file", "text/csv", csvHeader + "Ana,30111222\nBeto,40111222\nAna bis,30.111.222\n",
			http.StatusUnprocessableEntity,
			`{"error":"invalid_rows","rows":[{"line":4,"reason":"duplicate document_number (line 2)"}]}`},
		{"missing header", "text/csv", "Ana,30111222\n", http.StatusUnprocessableEntity,
			`{"error":"invalid_rows","rows":[{"line":1,"reason":"header must be full_name,document_number"}]}`},
		{"empty body", "text/csv", "", http.StatusUnprocessableEntity,
			`{"error":"invalid_rows","rows":[{"line":1,"reason":"header must be full_name,document_number"}]}`},
		{"malformed CSV", "text/csv", csvHeader + "Ana,30111222\n\"Beto,40111222\n", http.StatusUnprocessableEntity,
			`{"error":"invalid_rows","rows":[{"line":3,"reason":"malformed CSV"}]}`},
		{"too many rows", "text/csv", tooMany.String(), http.StatusUnprocessableEntity, `{"error":"too_many_rows"}`},
		{"body too large", "text/csv", csvHeader + strings.Repeat("a", 1<<20) + ",30111222\n",
			http.StatusRequestEntityTooLarge, `{"error":"body_too_large"}`},
		{"not CSV", "application/json", csvHeader + "Ana,30111222\n",
			http.StatusUnsupportedMediaType, `{"error":"unsupported_media_type"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(http.MethodPost, "/api/roster/import", tc.body, "198.51.100.11", token,
				map[string]string{"Content-Type": tc.contentType})
			wantResponse(t, rec, tc.status, tc.want)
			if got := rosterRows(t, ctx, f.tenant); !slices.Equal(got, before) {
				t.Fatalf("roster = %q, want unchanged %q", got, before)
			}
		})
	}
}

func TestRosterEndpointsRequireSchoolAdmin(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	member := seedMember(t, ctx, f.tenant, "Ana Acosta", "30111222")
	_, student := f.signIn(t, ctx, "student")
	_, officer := f.signIn(t, ctx, "officer")
	_, outsider := f.signIn(t, ctx, "")
	removedID, removed := f.signIn(t, ctx, "school_admin")
	// The session still names the tenant, but the membership is gone: checked at request time.
	if _, err := tdb.Super.Exec(ctx, `DELETE FROM memberships WHERE user_id = $1`, removedID); err != nil {
		t.Fatal(err)
	}
	before := rosterRows(t, ctx, f.tenant)

	for _, ep := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/roster", ""},
		{http.MethodPost, "/api/roster/import", csvHeader + "Beto,40111222\n"},
		{http.MethodPost, "/api/roster/" + member + "/access-code", ""},
		{http.MethodPost, "/api/roster/access-codes", ""},
	} {
		for _, who := range []struct {
			name, token string
			status      int
			body        string
		}{
			{"no session", "", http.StatusUnauthorized, unauthBody},
			{"student", student, http.StatusForbidden, `{"error":"forbidden"}`},
			{"officer", officer, http.StatusForbidden, `{"error":"forbidden"}`},
			{"no membership", outsider, http.StatusForbidden, `{"error":"forbidden"}`},
			{"membership removed", removed, http.StatusForbidden, `{"error":"forbidden"}`},
		} {
			t.Run(ep.method+" "+ep.path+"/"+who.name, func(t *testing.T) {
				rec := f.do(ep.method, ep.path, ep.body, "198.51.100.12", who.token,
					map[string]string{"Content-Type": "text/csv"})
				wantResponse(t, rec, who.status, who.body)
				if got := rosterRows(t, ctx, f.tenant); !slices.Equal(got, before) {
					t.Fatalf("roster = %q, want unchanged %q", got, before)
				}
				if n := tenantCodeCount(t, ctx, f.tenant); n != 0 {
					t.Fatalf("access codes = %d, want 0", n)
				}
			})
		}
	}
}

func TestRosterIsTenantIsolated(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, token := f.signIn(t, ctx, "school_admin")
	mine := seedMember(t, ctx, f.tenant, "Ana Acosta", "30111222")
	other := newTenant(t, ctx)
	theirs := seedMember(t, ctx, other, "Beto Bravo", "40111222")

	t.Run("list shows only own members", func(t *testing.T) {
		rec := f.do(http.MethodGet, "/api/roster", "", "198.51.100.13", token, nil)
		wantResponse(t, rec, http.StatusOK, fmt.Sprintf(
			`[{"documentNumber":"30111222","fullName":"Ana Acosta","hasAccount":false,"hasActiveCode":false,"id":"%s"}]`, mine))
		wantNoStore(t, rec)
	})
	t.Run("issuing for another tenant's member is not found", func(t *testing.T) {
		for _, id := range []string{theirs, "not-a-uuid"} {
			rec := f.do(http.MethodPost, "/api/roster/"+id+"/access-code", "", "198.51.100.13", token, nil)
			wantResponse(t, rec, http.StatusNotFound, `{"error":"not_found"}`)
		}
		if n := tenantCodeCount(t, ctx, other); n != 0 {
			t.Fatalf("other tenant's codes = %d, want 0", n)
		}
	})
	t.Run("bulk issue covers only own members", func(t *testing.T) {
		rec := f.do(http.MethodPost, "/api/roster/access-codes", "", "198.51.100.13", token, nil)
		var got []oapi.IssuedMemberCode
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, body %s (%v); want 201", rec.Code, rec.Body, err)
		}
		if len(got) != 1 || got[0].MemberId != mine {
			t.Fatalf("issued = %+v, want only %s", got, mine)
		}
		if n := tenantCodeCount(t, ctx, other); n != 0 {
			t.Fatalf("other tenant's codes = %d, want 0", n)
		}
	})
	t.Run("existing document numbers are matched within the tenant", func(t *testing.T) {
		wantResponse(t, f.importCSV(token, csvHeader+"Beto Propio,40111222\n"), http.StatusOK, `{"inserted":1,"skipped":0}`)
		if got, want := rosterRows(t, ctx, other), []string{"Beto Bravo|40111222"}; !slices.Equal(got, want) {
			t.Fatalf("other tenant's roster = %q, want %q", got, want)
		}
	})
}

func TestIssueAccessCode(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, token := f.signIn(t, ctx, "school_admin")
	member := seedMember(t, ctx, f.tenant, "Ana Acosta", "30111222")
	path := "/api/roster/" + member + "/access-code"

	issue := func(t *testing.T) string {
		t.Helper()
		rec := f.do(http.MethodPost, path, "", "198.51.100.14", token, nil)
		var got oapi.IssuedCode
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("status %d, body %s: %v", rec.Code, rec.Body, err)
		}
		if !codeRE.MatchString(got.Code) {
			t.Fatalf("code = %q, want match for %s", got.Code, codeRE)
		}
		wantResponse(t, rec, http.StatusCreated, fmt.Sprintf(`{"code":"%s","expiresAt":"%s"}`,
			got.Code, f.clk.Now().Add(30*24*time.Hour).Format(time.RFC3339)))
		wantNoStore(t, rec)
		return got.Code
	}

	first := issue(t)
	rows := codeRows(t, ctx, member)
	if len(rows) != 1 || rows[0].Used != nil || rows[0].Revoked != nil ||
		!rows[0].Created.Equal(f.clk.Now()) || !rows[0].Expires.Equal(f.clk.Now().Add(30*24*time.Hour)) {
		t.Fatalf("rows = %+v, want one active code created now, expiring in 30 days", rows)
	}
	wantCodeHash(t, ctx, rows[0].Hash, first)

	t.Run("re-issuing revokes the previous code", func(t *testing.T) {
		f.clk.Advance(time.Minute)
		second := issue(t)
		if second == first {
			t.Fatal("re-issued the same code")
		}
		rows := codeRows(t, ctx, member)
		if len(rows) != 2 || rows[0].Revoked == nil || !rows[0].Revoked.Equal(f.clk.Now()) ||
			rows[1].Revoked != nil || rows[1].Used != nil {
			t.Fatalf("rows = %+v, want the first revoked now and the second active", rows)
		}
		wantCodeHash(t, ctx, rows[1].Hash, second)
	})
	t.Run("member with an account gets no code", func(t *testing.T) {
		claimed := seedMember(t, ctx, f.tenant, "Beto Bravo", "40111222")
		linkAccount(t, ctx, f.tenant, claimed)
		rec := f.do(http.MethodPost, "/api/roster/"+claimed+"/access-code", "", "198.51.100.14", token, nil)
		wantResponse(t, rec, http.StatusConflict, `{"error":"member_has_account"}`)
		if rows := codeRows(t, ctx, claimed); len(rows) != 0 {
			t.Fatalf("rows = %+v, want none", rows)
		}
	})
}

func TestRosterListReportsCodeAndAccountState(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, token := f.signIn(t, ctx, "school_admin")
	now := f.clk.Now()
	ids := map[string]string{}
	for i, name := range []string{"Ana", "Beto", "Carla", "Dani", "Eva", "Fede"} {
		ids[name] = seedMember(t, ctx, f.tenant, name, fmt.Sprint(30000000+i))
	}
	seedCode(t, ctx, f.tenant, ids["Beto"], "active", now)
	linkAccount(t, ctx, f.tenant, ids["Carla"])
	seedCode(t, ctx, f.tenant, ids["Dani"], "expired", now)
	seedCode(t, ctx, f.tenant, ids["Eva"], "revoked", now)
	seedCode(t, ctx, f.tenant, ids["Fede"], "used", now)

	var want strings.Builder
	want.WriteString("[")
	for i, m := range []struct {
		name          string
		account, code bool
	}{{"Ana", false, false}, {"Beto", false, true}, {"Carla", true, false}, {"Dani", false, false}, {"Eva", false, false}, {"Fede", false, false}} {
		if i > 0 {
			want.WriteString(",")
		}
		fmt.Fprintf(&want, `{"documentNumber":"%d","fullName":"%s","hasAccount":%t,"hasActiveCode":%t,"id":"%s"}`,
			30000000+i, m.name, m.account, m.code, ids[m.name])
	}
	want.WriteString("]")
	rec := f.do(http.MethodGet, "/api/roster", "", "198.51.100.15", token, nil)
	wantResponse(t, rec, http.StatusOK, want.String())
	wantNoStore(t, rec)
}

func TestBulkIssueSkipsMembersWithAccountOrActiveCode(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, token := f.signIn(t, ctx, "school_admin")
	now := f.clk.Now()
	ana := seedMember(t, ctx, f.tenant, "Ana", "30000001")
	beto := seedMember(t, ctx, f.tenant, "Beto", "30000002")
	carla := seedMember(t, ctx, f.tenant, "Carla", "30000003")
	dani := seedMember(t, ctx, f.tenant, "Dani", "30000004")
	seedCode(t, ctx, f.tenant, beto, "active", now)
	linkAccount(t, ctx, f.tenant, carla)
	seedCode(t, ctx, f.tenant, dani, "expired", now)
	betoBefore := codeRows(t, ctx, beto)

	rec := f.do(http.MethodPost, "/api/roster/access-codes", "", "198.51.100.16", token, nil)
	var got []oapi.IssuedMemberCode
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("status %d, body %s: %v", rec.Code, rec.Body, err)
	}
	if len(got) != 2 {
		t.Fatalf("issued %d codes, want 2: %s", len(got), rec.Body)
	}
	exp := now.Add(30 * 24 * time.Hour).Format(time.RFC3339)
	wantResponse(t, rec, http.StatusCreated, fmt.Sprintf(
		`[{"code":"%s","documentNumber":"30000001","expiresAt":"%s","fullName":"Ana","memberId":"%s"},`+
			`{"code":"%s","documentNumber":"30000004","expiresAt":"%s","fullName":"Dani","memberId":"%s"}]`,
		got[0].Code, exp, ana, got[1].Code, exp, dani))
	wantNoStore(t, rec)
	for i, m := range []string{ana, dani} {
		if !codeRE.MatchString(got[i].Code) {
			t.Fatalf("code = %q, want match for %s", got[i].Code, codeRE)
		}
		rows := codeRows(t, ctx, m)
		last := rows[len(rows)-1]
		if last.Revoked != nil || last.Used != nil {
			t.Fatalf("new code for %s is not active: %+v", m, last)
		}
		wantCodeHash(t, ctx, last.Hash, got[i].Code)
	}
	if rows := codeRows(t, ctx, dani); len(rows) != 2 || rows[0].Revoked == nil {
		t.Fatalf("dani rows = %+v, want the expired code revoked", rows)
	}
	if rows := codeRows(t, ctx, beto); len(rows) != 1 || rows[0].Revoked != nil || string(rows[0].Hash) != string(betoBefore[0].Hash) {
		t.Fatalf("beto rows = %+v, want the active code untouched", rows)
	}
	if rows := codeRows(t, ctx, carla); len(rows) != 0 {
		t.Fatalf("carla rows = %+v, want none", rows)
	}

	rec = f.do(http.MethodPost, "/api/roster/access-codes", "", "198.51.100.16", token, nil)
	wantResponse(t, rec, http.StatusCreated, `[]`)
}

func TestRosterImportRejectsNonUTF8(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, token := f.signIn(t, ctx, "school_admin")
	before := rosterRows(t, ctx, f.tenant)

	rec := f.importCSV(token, csvHeader+"Ana,30111221\nMu\xf1oz Ana,30111222\n")

	wantResponse(t, rec, http.StatusUnprocessableEntity,
		`{"error":"invalid_rows","rows":[{"line":3,"reason":"file must be saved as CSV UTF-8"}]}`)
	if got := rosterRows(t, ctx, f.tenant); !slices.Equal(got, before) {
		t.Fatalf("roster = %q, want unchanged %q", got, before)
	}
}

// Two admins pressing "issue codes" at the same time must not revoke codes the first one already handed out.
func TestConcurrentBulkIssueDoesNotRevokeIssuedCodes(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, token := f.signIn(t, ctx, "school_admin")
	for i := range 8 {
		seedMember(t, ctx, f.tenant, fmt.Sprintf("Alumno %d", i), fmt.Sprintf("3100000%d", i))
	}

	var wg sync.WaitGroup
	recs := make([]*httptest.ResponseRecorder, 2)
	for i := range recs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recs[i] = f.do(http.MethodPost, "/api/roster/access-codes", "", "198.51.100.30", token, nil)
		}()
	}
	wg.Wait()

	issued := 0
	for _, rec := range recs {
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
		}
		var got []oapi.IssuedMemberCode
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		issued += len(got)
	}
	if issued != 8 {
		t.Fatalf("codes handed out = %d, want 8 (one per member, across both calls)", issued)
	}
	var total, revoked int
	if err := tdb.Super.QueryRow(ctx,
		`SELECT count(*), count(revoked_at) FROM access_codes WHERE tenant_id = $1`, f.tenant).Scan(&total, &revoked); err != nil {
		t.Fatal(err)
	}
	if total != 8 || revoked != 0 {
		t.Fatalf("access_codes total=%d revoked=%d, want 8 and 0", total, revoked)
	}
}
