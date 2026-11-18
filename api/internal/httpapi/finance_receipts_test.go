package httpapi_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/finance"
)

// Minimal generated fixtures (R9: never real ticket photos).
var (
	// 1x1 transparent PNG.
	tinyPNG = mustBase64("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=")
	tinyPDF = []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n")
)

func mustBase64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func makeMinimalJPG(totalLen int) []byte {
	b := make([]byte, totalLen)
	copy(b, []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00})
	return b
}

// trackingReader yields up to total bytes of pattern and records total bytes read by the server.
type trackingReader struct {
	prefix    []byte
	total     int64
	readSoFar int64
}

func (r *trackingReader) Read(p []byte) (int, error) {
	if r.readSoFar >= r.total {
		return 0, io.EOF
	}
	n := len(p)
	if rem := r.total - r.readSoFar; int64(n) > rem {
		n = int(rem)
	}
	for i := 0; i < n; i++ {
		idx := r.readSoFar + int64(i)
		if idx < int64(len(r.prefix)) {
			p[i] = r.prefix[idx]
		} else {
			p[i] = 'x'
		}
	}
	r.readSoFar += int64(n)
	return n, nil
}

func TestFinanceReceipts(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	f.clk.t = time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC)
	if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, f.tenant); err != nil {
		t.Fatal(err)
	}

	anaUser, ana, _, _ := f.signInMFA(t, ctx, "officer")
	anaMember := seedMember(t, ctx, f.tenant, "Ana Ficticia", "30222111")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, anaMember, f.tenant, anaUser); err != nil {
		t.Fatal(err)
	}
	betoUser, beto, _, _ := f.signInMFA(t, ctx, "officer")
	betoMember := seedMember(t, ctx, f.tenant, "Beto Ficticio", "30222112")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, betoMember, f.tenant, betoUser); err != nil {
		t.Fatal(err)
	}
	posUser, posToken, _, _ := f.signInMFA(t, ctx, "student")
	posMember := seedMember(t, ctx, f.tenant, "Ciro Ficticio", "30222113")
	if _, err := tdb.Super.Exec(ctx, `UPDATE memberships SET roster_member_id=$1 WHERE tenant_id=$2 AND user_id=$3`, posMember, f.tenant, posUser); err != nil {
		t.Fatal(err)
	}
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO officer_positions(tenant_id,roster_member_id,position) VALUES($1,$2,'Tesorero')`, f.tenant, posMember); err != nil {
		t.Fatal(err)
	}

	_, officerNoMFA := f.signIn(t, ctx, "officer")
	_, adminMFA, _, _ := f.signInMFA(t, ctx, "school_admin")
	_, adminNoMFA := f.signIn(t, ctx, "school_admin")
	_, student := f.signIn(t, ctx, "student")

	var alquileres string
	if err := tdb.Super.QueryRow(ctx, `SELECT id::text FROM finance_categories WHERE tenant_id=$1 AND name='Alquileres' AND direction='expense'`, f.tenant).Scan(&alquileres); err != nil {
		t.Fatal(err)
	}

	createEntry := func(desc string) string {
		t.Helper()
		body := fmt.Sprintf(`{"categoryId":%q,"direction":"expense","date":"2026-11-15","amountCents":1500000,"description":%q}`, alquileres, desc)
		r := f.do(http.MethodPost, "/api/finance/entries", body, "198.51.100.20", ana, nil)
		if r.Code != 201 {
			t.Fatalf("create entry: %d %s", r.Code, r.Body)
		}
		var out struct {
			ID         string `json:"id"`
			HasReceipt bool   `json:"hasReceipt"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.HasReceipt {
			t.Fatal("new entry should start with hasReceipt=false")
		}
		return out.ID
	}

	upload := func(entryID string, data []byte, token string, hdr map[string]string) *httptest.ResponseRecorder {
		h := map[string]string{"Content-Type": "application/octet-stream"}
		for k, v := range hdr {
			h[k] = v
		}
		return f.do(http.MethodPost, "/api/finance/entries/"+entryID+"/receipts", string(data), "198.51.100.20", token, h)
	}

	receiptCount := func(entryID string) int {
		t.Helper()
		var n int
		if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM finance_receipts WHERE entry_id=$1`, entryID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	entryHasReceipt := func(entryID, token string) bool {
		t.Helper()
		r := f.do(http.MethodGet, "/api/finance/entries/"+entryID, "", "198.51.100.20", token, nil)
		if r.Code != 200 {
			t.Fatalf("get entry %s: %d %s", entryID, r.Code, r.Body)
		}
		var out struct {
			HasReceipt bool `json:"hasReceipt"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.HasReceipt
	}

	type receiptResp struct {
		ID          string    `json:"id"`
		ContentType string    `json:"contentType"`
		SizeBytes   int       `json:"sizeBytes"`
		UploadedAt  time.Time `json:"uploadedAt"`
	}

	chairs := createEntry("Alquiler de sillas")
	secondEntry := createEntry("Equipo de sonido")

	var jpgID, pngID, pdfID string

	t.Run("R1 Ana uploads 300 KB JPG and multiple receipts per entry work", func(t *testing.T) {
		jpg300 := makeMinimalJPG(300 * 1024)
		r := upload(chairs, jpg300, ana, nil)
		if r.Code != 201 {
			t.Fatalf("upload jpg: %d %s", r.Code, r.Body)
		}
		var out receiptResp
		if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		jpgID = out.ID
		if out.ID == "" || out.ContentType != "image/jpeg" || out.SizeBytes != len(jpg300) {
			t.Fatalf("receipt=%+v", out)
		}
		if !entryHasReceipt(chairs, student) {
			t.Fatal("student should see hasReceipt=true after upload")
		}

		f.clk.t = f.clk.t.Add(time.Second)
		r2 := upload(chairs, tinyPNG, ana, nil)
		if r2.Code != 201 {
			t.Fatalf("upload png: %d %s", r2.Code, r2.Body)
		}
		var out2 receiptResp
		if err := json.Unmarshal(r2.Body.Bytes(), &out2); err != nil {
			t.Fatal(err)
		}
		pngID = out2.ID

		f.clk.t = f.clk.t.Add(time.Second)
		r3 := upload(chairs, tinyPDF, beto, nil)
		if r3.Code != 201 {
			t.Fatalf("upload pdf by other officer: %d %s", r3.Code, r3.Body)
		}
		var out3 receiptResp
		if err := json.Unmarshal(r3.Body.Bytes(), &out3); err != nil {
			t.Fatal(err)
		}
		pdfID = out3.ID

		listRec := f.do(http.MethodGet, "/api/finance/entries/"+chairs+"/receipts", "", "198.51.100.20", ana, nil)
		if listRec.Code != 200 {
			t.Fatalf("list receipts: %d %s", listRec.Code, listRec.Body)
		}
		var listed []receiptResp
		if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
			t.Fatal(err)
		}
		if len(listed) != 3 || listed[0].ID != jpgID || listed[1].ID != pngID || listed[2].ID != pdfID {
			t.Fatalf("listed=%+v", listed)
		}
	})

	t.Run("R2 rejects spoofed executable, GIF, and DOCX by file signature without writing", func(t *testing.T) {
		cases := []struct {
			name string
			data []byte
			ct   string
		}{
			{"exe_as_pdf", []byte("MZ\x90\x00\x03\x00\x00\x00"), "application/pdf"},
			{"gif_as_png", []byte("GIF89a\x01\x00\x01\x00"), "image/png"},
			{"docx_zip", []byte{'P', 'K', 0x03, 0x04, 0x14, 0x00}, "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				r := upload(secondEntry, tc.data, ana, map[string]string{"Content-Type": tc.ct})
				wantResponse(t, r, 415, `{"error":"unsupported_media_type"}`)
				if receiptCount(secondEntry) != 0 || entryHasReceipt(secondEntry, ana) {
					t.Fatal("rejected upload mutated entry receipts")
				}
			})
		}
	})

	t.Run("R3 enforces 10 MiB inclusive, rejects 10 MiB+1 early with file_too_large, rejects empty with empty_file", func(t *testing.T) {
		exact := makeMinimalJPG(finance.MaxReceiptBytes)
		rExact := upload(chairs, exact, ana, nil)
		if rExact.Code != 201 {
			t.Fatalf("exact 10 MiB: %d %s", rExact.Code, rExact.Body)
		}

		// 10 MiB + 1 byte streamed: server must reject 413 file_too_large without draining a 20 MiB stream.
		stream := &trackingReader{prefix: []byte{0xFF, 0xD8, 0xFF}, total: 20 << 20}
		req := httptest.NewRequest(http.MethodPost, "/api/finance/entries/"+secondEntry+"/receipts", stream)
		req.RemoteAddr = "198.51.100.20:40000"
		req.Header.Set("Content-Type", "application/octet-stream")
		req.AddCookie(&http.Cookie{Name: cookieName, Value: ana})
		rec := httptest.NewRecorder()
		f.h.ServeHTTP(rec, req)
		wantResponse(t, rec, 413, `{"error":"file_too_large"}`)
		if stream.readSoFar > int64(finance.MaxReceiptBytes)+64*1024 {
			t.Fatalf("server kept reading past limit: readSoFar=%d", stream.readSoFar)
		}
		if receiptCount(secondEntry) != 0 {
			t.Fatal("oversized upload saved rows")
		}

		rEmpty := upload(secondEntry, []byte{}, ana, nil)
		wantResponse(t, rEmpty, 422, `{"error":"empty_file"}`)
		if receiptCount(secondEntry) != 0 {
			t.Fatal("empty upload saved rows")
		}
	})

	t.Run("R4 PUT and DELETE on receipts return 405", func(t *testing.T) {
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			for _, path := range []string{
				"/api/finance/entries/" + chairs + "/receipts/" + jpgID,
				"/api/finance/entries/" + chairs + "/receipts",
			} {
				r := f.do(method, path, "", "198.51.100.20", ana, nil)
				if r.Code != http.StatusMethodNotAllowed {
					t.Fatalf("%s %s: status=%d want 405", method, path, r.Code)
				}
			}
		}
	})

	t.Run("R5 failed receipt transaction leaves entry intact without partial receipt", func(t *testing.T) {
		if _, err := tdb.Super.Exec(ctx, `CREATE FUNCTION finance_receipt_probe() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'probe'; END $$; CREATE TRIGGER finance_receipt_probe AFTER INSERT ON finance_receipts FOR EACH ROW EXECUTE FUNCTION finance_receipt_probe()`); err != nil {
			t.Fatal(err)
		}
		dropped := false
		drop := func() {
			if !dropped {
				dropped = true
				_, _ = tdb.Super.Exec(context.Background(), `DROP TRIGGER IF EXISTS finance_receipt_probe ON finance_receipts; DROP FUNCTION IF EXISTS finance_receipt_probe()`)
			}
		}
		t.Cleanup(drop)

		r := upload(secondEntry, tinyPNG, ana, nil)
		wantResponse(t, r, 500, `{"error":"internal"}`)
		if receiptCount(secondEntry) != 0 || entryHasReceipt(secondEntry, ana) {
			t.Fatal("failed upload left partial receipt")
		}
		drop()
		if rOK := upload(secondEntry, tinyPNG, ana, nil); rOK.Code != 201 {
			t.Fatalf("retry after failure: %d %s", rOK.Code, rOK.Body)
		}
		if !entryHasReceipt(secondEntry, ana) {
			t.Fatal("retry should mark hasReceipt=true")
		}
	})

	t.Run("R6 download returns validated content type, attachment filename with receiptId, nosniff, and no original filename in headers or logs", func(t *testing.T) {
		var logBuf bytes.Buffer
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
		t.Cleanup(func() { slog.SetDefault(prev) })

		secretName := "ticket-secreto-juan-perez.png"
		up := upload(secondEntry, tinyPNG, ana, map[string]string{
			"Content-Disposition": fmt.Sprintf(`attachment; filename="%s"`, secretName),
			"X-Filename":          secretName,
		})
		if up.Code != 201 {
			t.Fatalf("upload: %d %s", up.Code, up.Body)
		}
		var upOut receiptResp
		if err := json.Unmarshal(up.Body.Bytes(), &upOut); err != nil {
			t.Fatal(err)
		}

		dl := f.do(http.MethodGet, "/api/finance/entries/"+secondEntry+"/receipts/"+upOut.ID, "", "198.51.100.20", adminMFA, nil)
		if dl.Code != 200 {
			t.Fatalf("download: %d %s", dl.Code, dl.Body)
		}
		if !bytes.Equal(dl.Body.Bytes(), tinyPNG) {
			t.Fatal("downloaded bytes mismatch")
		}
		if got := dl.Header().Get("Content-Type"); got != "image/png" {
			t.Fatalf("Content-Type=%q", got)
		}
		wantDisp := fmt.Sprintf(`attachment; filename="comprobante-%s.png"`, upOut.ID)
		if got := dl.Header().Get("Content-Disposition"); got != wantDisp {
			t.Fatalf("Content-Disposition=%q want %q", got, wantDisp)
		}
		if got := dl.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("X-Content-Type-Options=%q", got)
		}
		if got := dl.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("Cache-Control=%q", got)
		}
		if strings.Contains(logBuf.String(), secretName) {
			t.Fatalf("original filename leaked to logs: %s", logBuf.String())
		}
		for k, vals := range dl.Header() {
			for _, v := range vals {
				if strings.Contains(v, secretName) {
					t.Fatalf("original filename leaked to header %s: %s", k, v)
				}
			}
		}

		// Also check PDF download headers.
		dlPDF := f.do(http.MethodGet, "/api/finance/entries/"+chairs+"/receipts/"+pdfID, "", "198.51.100.20", ana, nil)
		if dlPDF.Code != 200 || dlPDF.Header().Get("Content-Type") != "application/pdf" ||
			dlPDF.Header().Get("Content-Disposition") != fmt.Sprintf(`attachment; filename="comprobante-%s.pdf"`, pdfID) {
			t.Fatalf("pdf download: status=%d headers=%v", dlPDF.Code, dlPDF.Header())
		}
	})

	t.Run("R7 role and MFA matrix for upload and download", func(t *testing.T) {
		before := receiptCount(chairs)
		// Upload rejections.
		for _, tc := range []struct {
			name   string
			token  string
			status int
			code   string
		}{
			{"no session", "", 401, "unauthenticated"},
			{"student", student, 403, "forbidden"},
			{"school_admin with MFA", adminMFA, 403, "forbidden"},
			{"school_admin without MFA", adminNoMFA, 403, "forbidden"},
			{"officer without MFA", officerNoMFA, 403, "mfa_required"},
		} {
			t.Run("upload/"+tc.name, func(t *testing.T) {
				r := upload(chairs, tinyPNG, tc.token, nil)
				wantResponse(t, r, tc.status, fmt.Sprintf(`{"error":%q}`, tc.code))
			})
		}
		if receiptCount(chairs) != before {
			t.Fatal("unauthorized upload wrote rows")
		}
		// Officer via officer_positions with MFA can upload too.
		if r := upload(chairs, tinyPNG, posToken, nil); r.Code != 201 {
			t.Fatalf("position officer upload: %d %s", r.Code, r.Body)
		}

		// Download & list matrix (veedor arrives with #28; memberships.role CHECK currently allows school_admin, officer, student).
		for _, tc := range []struct {
			name   string
			token  string
			status int
			code   string
		}{
			{"no session", "", 401, "unauthenticated"},
			{"student", student, 403, "forbidden"},
			{"officer without MFA", officerNoMFA, 403, "mfa_required"},
			{"school_admin without MFA", adminNoMFA, 403, "mfa_required"},
		} {
			t.Run("download/"+tc.name, func(t *testing.T) {
				rDL := f.do(http.MethodGet, "/api/finance/entries/"+chairs+"/receipts/"+jpgID, "", "198.51.100.20", tc.token, nil)
				wantResponse(t, rDL, tc.status, fmt.Sprintf(`{"error":%q}`, tc.code))
				rList := f.do(http.MethodGet, "/api/finance/entries/"+chairs+"/receipts", "", "198.51.100.20", tc.token, nil)
				wantResponse(t, rList, tc.status, fmt.Sprintf(`{"error":%q}`, tc.code))
			})
		}
		for _, tok := range []string{ana, beto, posToken, adminMFA} {
			if r := f.do(http.MethodGet, "/api/finance/entries/"+chairs+"/receipts/"+pngID, "", "198.51.100.20", tok, nil); r.Code != 200 {
				t.Fatalf("authorized download failed: %d %s", r.Code, r.Body)
			}
		}
	})

	t.Run("R8 cross-tenant and mismatched entryId/receiptId return 404", func(t *testing.T) {
		other := newTenant(t, ctx)
		if _, err := tdb.Super.Exec(ctx, `SELECT seed_finance_categories($1)`, other); err != nil {
			t.Fatal(err)
		}
		var otherEntry string
		if err := tdb.Super.QueryRow(ctx, `INSERT INTO finance_entries(tenant_id,category_id,direction,entry_date,amount_cents,description,recorded_by,recorded_by_name) SELECT $1,c.id,'income','2026-11-15',1,'Fixture',u.id,'Ciro Ficticio' FROM finance_categories c, users u WHERE c.tenant_id=$1 AND c.name='Ventas' LIMIT 1 RETURNING id::text`, other).Scan(&otherEntry); err != nil {
			t.Fatal(err)
		}
		var otherReceipt string
		if err := tdb.Super.QueryRow(ctx, `INSERT INTO finance_receipts(tenant_id,entry_id,content_type,size_bytes,data,uploaded_by) SELECT $1,$2,'image/png',$3,$4,u.id FROM users u LIMIT 1 RETURNING id::text`, other, otherEntry, len(tinyPNG), tinyPNG).Scan(&otherReceipt); err != nil {
			t.Fatal(err)
		}

		// Upload to other school's entry or non-UUID -> 404.
		wantResponse(t, upload(otherEntry, tinyPNG, ana, nil), 404, `{"error":"not_found"}`)
		wantResponse(t, upload("not-a-uuid", tinyPNG, ana, nil), 404, `{"error":"not_found"}`)
		wantResponse(t, upload("00000000-0000-4000-8000-000000000000", tinyPNG, ana, nil), 404, `{"error":"not_found"}`)

		// List other school's entry -> 404.
		wantResponse(t, f.do(http.MethodGet, "/api/finance/entries/"+otherEntry+"/receipts", "", "198.51.100.20", ana, nil), 404, `{"error":"not_found"}`)

		// Download other school's receipt, or receiptId belonging to a different entry in the same school -> 404.
		wantResponse(t, f.do(http.MethodGet, "/api/finance/entries/"+otherEntry+"/receipts/"+otherReceipt, "", "198.51.100.20", ana, nil), 404, `{"error":"not_found"}`)
		wantResponse(t, f.do(http.MethodGet, "/api/finance/entries/"+secondEntry+"/receipts/"+jpgID, "", "198.51.100.20", ana, nil), 404, `{"error":"not_found"}`)
		wantResponse(t, f.do(http.MethodGet, "/api/finance/entries/"+chairs+"/receipts/not-a-uuid", "", "198.51.100.20", ana, nil), 404, `{"error":"not_found"}`)
	})
}
