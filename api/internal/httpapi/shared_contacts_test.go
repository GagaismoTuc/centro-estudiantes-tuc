package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/contacts"
)

type shareResponseDTO struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
}

type sharedReferencePriceDTO struct {
	Description string `json:"description"`
	AmountCents int64  `json:"amountCents"`
	Date        string `json:"date"`
}

type sharedContactDTO struct {
	ID              string                    `json:"id"`
	HeadingName     string                    `json:"headingName"`
	Name            string                    `json:"name"`
	Phone           string                    `json:"phone"`
	Email           string                    `json:"email"`
	Zone            string                    `json:"zone"`
	ReferencePrices []sharedReferencePriceDTO `json:"referencePrices"`
	CreatedAt       string                    `json:"createdAt"`
}

type sharedContactPageDTO struct {
	Items      []sharedContactDTO `json:"items"`
	Headings   []string           `json:"headings"`
	Page       int                `json:"page"`
	PageSize   int                `json:"pageSize"`
	Total      int                `json:"total"`
	TotalPages int                `json:"totalPages"`
}

type contactWithShareDTO struct {
	contactDTO
	ShareStatus      *string `json:"shareStatus"`
	HasApprovedShare bool    `json:"hasApprovedShare"`
	ShareOutdated    bool    `json:"shareOutdated"`
}

func TestSharedContactsLifecycleAndPrivacy(t *testing.T) {
	ctx := integration(t)
	fA := newFixture(t, ctx)
	fB := newFixture(t, ctx)
	headingsA := seedHeadingsForTenant(t, ctx, fA.tenant)
	_, officerA, _, _ := fA.signInMFA(t, ctx, "officer")
	_, officerB, _, _ := fB.signInMFA(t, ctx, "officer")
	const ip = "198.51.100.40"

	// Escuela A creates "Gazebos Ficticios SRL" with sensitive notes and a reference price.
	rec := fA.do(http.MethodPost, "/api/contacts", `{
		"name":"Gazebos Ficticios SRL",
		"headingId":"`+headingsA["gazebos"]+`",
		"phone":"381 555-0000",
		"email":"ventas@gazebos-ficticios.example",
		"zone":"Yerba Buena",
		"notes":"Pagan tarde, pedir seña"
	}`, ip, officerA, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create contact = %d body=%s", rec.Code, rec.Body)
	}
	var cA contactWithShareDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &cA)

	rec = fA.do(http.MethodPost, "/api/contacts/"+cA.ID+"/reference-prices", `{"description":"Gazebo 3×3","amountCents":2500000,"date":"2026-09-01"}`, ip, officerA, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add price = %d body=%s", rec.Code, rec.Body)
	}

	// R1: Share -> 201 pending, without notes, with its own id.
	rec = fA.do(http.MethodPost, "/api/contacts/"+cA.ID+"/share", "", ip, officerA, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("share = %d body=%s", rec.Code, rec.Body)
	}
	var share1 shareResponseDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &share1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = tdb.Super.Exec(context.Background(), `DELETE FROM shared_contacts WHERE id = $1`, share1.ID)
	})
	if share1.Status != "pending" || share1.ID == "" || share1.ID == cA.ID {
		t.Fatalf("share1 = %+v, contactID=%s", share1, cA.ID)
	}

	// R2: Pending is invisible in GET /shared-contacts for both A and B, while A sees shareStatus=pending on its Contact.
	for _, tc := range []struct {
		name string
		f    *fixture
		tok  string
	}{{"A", fA, officerA}, {"B", fB, officerB}} {
		r := tc.f.do(http.MethodGet, "/api/shared-contacts", "", ip, tc.tok, nil)
		if r.Code != http.StatusOK {
			t.Fatalf("search (%s) = %d body=%s", tc.name, r.Code, r.Body)
		}
		var page sharedContactPageDTO
		_ = json.Unmarshal(r.Body.Bytes(), &page)
		if len(page.Items) != 0 {
			t.Fatalf("pending visible to %s: %+v", tc.name, page.Items)
		}
	}

	rec = fA.do(http.MethodGet, "/api/contacts", "", ip, officerA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list contacts = %d body=%s", rec.Code, rec.Body)
	}
	var listA []contactWithShareDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &listA)
	if len(listA) != 1 || listA[0].ShareStatus == nil || *listA[0].ShareStatus != "pending" {
		t.Fatalf("listA = %+v, want shareStatus=pending", listA)
	}

	// R1 negative: sharing again while pending -> 409 already_pending; sharing another school's contact -> 404 not_found.
	wantResponse(t, fA.do(http.MethodPost, "/api/contacts/"+cA.ID+"/share", "", ip, officerA, nil), http.StatusConflict, `{"error":"already_pending"}`)
	var linkCount int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM shared_contact_links WHERE tenant_id = $1 AND contact_id = $2`, fA.tenant, cA.ID).Scan(&linkCount); err != nil || linkCount != 1 {
		t.Fatalf("links after 409 already_pending = %d (err=%v), want 1", linkCount, err)
	}
	wantResponse(t, fB.do(http.MethodPost, "/api/contacts/"+cA.ID+"/share", "", ip, officerB, nil), http.StatusNotFound, `{"error":"not_found"}`)

	// R3 + R4: Reject via admin CLI function -> invisible in search, A sees rejected, A can share again.
	if err := contacts.RejectShared(ctx, tdb.Super, share1.ID, time.Now()); err != nil {
		t.Fatalf("reject: %v", err)
	}
	rec = fA.do(http.MethodGet, "/api/contacts", "", ip, officerA, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &listA)
	if len(listA) != 1 || listA[0].ShareStatus == nil || *listA[0].ShareStatus != "rejected" {
		t.Fatalf("after reject listA = %+v, want shareStatus=rejected", listA)
	}
	rec = fB.do(http.MethodGet, "/api/shared-contacts", "", ip, officerB, nil)
	var page sharedContactPageDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Items) != 0 {
		t.Fatalf("rejected visible to B: %+v", page.Items)
	}

	// Share again after rejection under the same frozen clock -> 201 pending with a new id, and
	// shareStatus deterministically switches back to pending (no timestamp-tie flakiness).
	rec = fA.do(http.MethodPost, "/api/contacts/"+cA.ID+"/share", "", ip, officerA, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("re-share = %d body=%s", rec.Code, rec.Body)
	}
	var share2 shareResponseDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &share2)
	t.Cleanup(func() {
		_, _ = tdb.Super.Exec(context.Background(), `DELETE FROM shared_contacts WHERE id = $1`, share2.ID)
	})
	if share2.ID == share1.ID || share2.Status != "pending" {
		t.Fatalf("share2 = %+v vs share1 = %+v", share2, share1)
	}
	rec = fA.do(http.MethodGet, "/api/contacts", "", ip, officerA, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &listA)
	if len(listA) != 1 || listA[0].ShareStatus == nil || *listA[0].ShareStatus != "pending" {
		t.Fatalf("after re-share listA = %+v, want shareStatus=pending", listA)
	}
	wantResponse(t, fA.do(http.MethodPost, "/api/contacts/"+cA.ID+"/share", "", ip, officerA, nil), http.StatusConflict, `{"error":"already_pending"}`)
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM shared_contact_links WHERE tenant_id = $1 AND contact_id = $2`, fA.tenant, cA.ID).Scan(&linkCount); err != nil || linkCount != 2 {
		t.Fatalf("links after 409 already_pending = %d (err=%v), want 2", linkCount, err)
	}

	// Approve share2 -> visible to B (without notes or school); re-sharing while approved -> 201 new pending (issue #82 R5).
	if err := contacts.ApproveShared(ctx, tdb.Super, share2.ID, time.Now()); err != nil {
		t.Fatalf("approve: %v", err)
	}

	rec = fB.do(http.MethodGet, "/api/shared-contacts", "", ip, officerB, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search B = %d body=%s", rec.Code, rec.Body)
	}
	bodyStr := rec.Body.String()
	if strings.Contains(bodyStr, "Pagan tarde") || strings.Contains(bodyStr, fA.tenant) || strings.Contains(bodyStr, cA.ID) || strings.Contains(bodyStr, fA.tenantName) {
		t.Fatalf("response leaked notes, tenant, contact id, or school name: %s", bodyStr)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Items) != 1 {
		t.Fatalf("approved items = %d, want 1 (%s)", len(page.Items), bodyStr)
	}
	got := page.Items[0]
	if got.ID != share2.ID || got.Name != "Gazebos Ficticios SRL" || got.HeadingName != "gazebos" ||
		got.Phone != "381 555-0000" || got.Email != "ventas@gazebos-ficticios.example" || got.Zone != "Yerba Buena" ||
		len(got.ReferencePrices) != 1 || got.ReferencePrices[0].Description != "Gazebo 3×3" || got.ReferencePrices[0].AmountCents != 2500000 {
		t.Fatalf("shared item = %+v", got)
	}

	// PRESERVE: PATCH own contact keeps shareStatus and does not mutate shared snapshot.
	rec = fA.do(http.MethodPatch, "/api/contacts/"+cA.ID, `{"zone":"Tafí Viejo","notes":"Nota nueva"}`, ip, officerA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch contact = %d body=%s", rec.Code, rec.Body)
	}
	var patched contactWithShareDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if patched.ShareStatus == nil || *patched.ShareStatus != "approved" {
		t.Fatalf("patched shareStatus = %v, want approved", patched.ShareStatus)
	}
	rec = fB.do(http.MethodGet, "/api/shared-contacts", "", ip, officerB, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0].Zone != "Yerba Buena" {
		t.Fatalf("shared snapshot mutated after own patch: %+v", page.Items)
	}

	// R9: remove -> disappears from search for every school, while own contact remains intact.
	if err := contacts.RemoveShared(ctx, tdb.Super, share2.ID, time.Now()); err != nil {
		t.Fatalf("remove: %v", err)
	}
	rec = fB.do(http.MethodGet, "/api/shared-contacts", "", ip, officerB, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Items) != 0 {
		t.Fatalf("removed still visible: %+v", page.Items)
	}
	rec = fA.do(http.MethodGet, "/api/contacts", "", ip, officerA, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &listA)
	if len(listA) != 1 || listA[0].Notes != "Nota nueva" || len(listA[0].ReferencePrices) != 1 {
		t.Fatalf("own contact altered after remove: %+v", listA)
	}

	// PRESERVE: DELETE own contact on a previously shared contact succeeds cleanly.
	wantResponse(t, fA.do(http.MethodDelete, "/api/contacts/"+cA.ID, "", ip, officerA, nil), http.StatusNoContent, "")
}

func TestSharedContactsConcurrentShareRace(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	headings := seedHeadingsForTenant(t, ctx, f.tenant)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	const ip = "198.51.100.41"

	rec := f.do(http.MethodPost, "/api/contacts", `{"name":"Carrera Ficticia","headingId":"`+headings["gazebos"]+`"}`, ip, officer, nil)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	var created contactDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	t.Cleanup(func() {
		_, _ = tdb.Super.Exec(context.Background(), `
			DELETE FROM shared_contacts
			WHERE id IN (SELECT shared_contact_id FROM shared_contact_links WHERE tenant_id = $1)`, f.tenant)
	})

	start := make(chan struct{})
	codes := make([]int, 2)
	bodies := make([]string, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			r := f.do(http.MethodPost, "/api/contacts/"+created.ID+"/share", "", ip, officer, nil)
			codes[idx] = r.Code
			bodies[idx] = r.Body.String()
		}(i)
	}
	close(start)
	wg.Wait()

	createdCount, conflictCount := 0, 0
	for i, code := range codes {
		switch code {
		case http.StatusCreated:
			createdCount++
		case http.StatusConflict:
			if strings.TrimSpace(bodies[i]) != `{"error":"already_pending"}` {
				t.Fatalf("conflict body = %s", bodies[i])
			}
			conflictCount++
		default:
			t.Fatalf("unexpected status %d body=%s", code, bodies[i])
		}
	}
	if createdCount != 1 || conflictCount != 1 {
		t.Fatalf("codes = %v, want one 201 and one 409", codes)
	}

	var links int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM shared_contact_links WHERE tenant_id = $1 AND contact_id = $2`, f.tenant, created.ID).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if links != 1 {
		t.Fatalf("links = %d, want 1", links)
	}
}

func TestSharedContactsSearchFiltersAndPagination(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	_, officer, _, _ := f.signInMFA(t, ctx, "officer")
	const ip = "198.51.100.42"

	var ids []string
	t.Cleanup(func() {
		if len(ids) > 0 {
			_, _ = tdb.Super.Exec(context.Background(), `DELETE FROM shared_contacts WHERE id = ANY($1::uuid[])`, ids)
		}
	})

	insert := func(status, heading, name, zone string) {
		var id string
		if err := tdb.Super.QueryRow(ctx, `
			INSERT INTO shared_contacts (status, heading_name, name, zone)
			VALUES ($1, $2, $3, $4) RETURNING id::text`,
			status, heading, name, zone).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	// 55 approved entries + non-approved noise.
	insert("approved", "gazebos", "A01 Gazebos YB", "Yerba Buena")
	insert("approved", "artistas", "A02 Banda Capital", "San Miguel de Tucumán")
	insert("approved", "gazebos", "A03 Gazebos Sur", "Concepcion")
	for i := 4; i <= 55; i++ {
		insert("approved", "otros", fmt.Sprintf("Z%02d Proveedor", i), "Banda del Río Salí")
	}
	insert("pending", "gazebos", "Pendiente Oculto", "Yerba Buena")
	insert("rejected", "gazebos", "Rechazado Oculto", "Yerba Buena")
	insert("removed", "gazebos", "Baja Oculto", "Yerba Buena")

	// Page 1 (default): 50 items out of 55.
	rec := f.do(http.MethodGet, "/api/shared-contacts", "", ip, officer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("page 1 = %d body=%s", rec.Code, rec.Body)
	}
	var p1 sharedContactPageDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &p1)
	if len(p1.Items) != 50 || p1.Page != 1 || p1.PageSize != 50 || p1.Total != 55 || p1.TotalPages != 2 {
		t.Fatalf("page 1 meta = items:%d page:%d size:%d total:%d pages:%d", len(p1.Items), p1.Page, p1.PageSize, p1.Total, p1.TotalPages)
	}

	// Page 2: remaining 5 items.
	rec = f.do(http.MethodGet, "/api/shared-contacts?page=2", "", ip, officer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("page 2 = %d body=%s", rec.Code, rec.Body)
	}
	var p2 sharedContactPageDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &p2)
	if len(p2.Items) != 5 || p2.Page != 2 || p2.Total != 55 || p2.TotalPages != 2 {
		t.Fatalf("page 2 = items:%d page:%d total:%d pages:%d", len(p2.Items), p2.Page, p2.Total, p2.TotalPages)
	}

	// Invalid page -> 400.
	wantResponse(t, f.do(http.MethodGet, "/api/shared-contacts?page=0", "", ip, officer, nil), http.StatusBadRequest, `{"error":"bad_request"}`)

	// Filter by heading (case-insensitive) and zone (partial, case- & accent-insensitive).
	rec = f.do(http.MethodGet, "/api/shared-contacts?heading=GAZEBOS&zone=yerba%20buena", "", ip, officer, nil)
	var filtered sharedContactPageDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &filtered)
	if len(filtered.Items) != 1 || filtered.Items[0].Name != "A01 Gazebos YB" {
		t.Fatalf("heading+zone filter = %+v", filtered.Items)
	}

	rec = f.do(http.MethodGet, "/api/shared-contacts?zone=tucuman", "", ip, officer, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &filtered)
	if len(filtered.Items) != 1 || filtered.Items[0].Name != "A02 Banda Capital" {
		t.Fatalf("accent-insensitive zone filter (tucuman) = %+v", filtered.Items)
	}

	rec = f.do(http.MethodGet, "/api/shared-contacts?zone=CONCEPCI%C3%93N", "", ip, officer, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &filtered)
	if len(filtered.Items) != 1 || filtered.Items[0].Name != "A03 Gazebos Sur" {
		t.Fatalf("accented query against unaccented zone = %+v", filtered.Items)
	}
}

func TestSharedContactsAuthorizationMatrix(t *testing.T) {
	ctx := integration(t)
	f := newFixture(t, ctx)
	headings := seedHeadingsForTenant(t, ctx, f.tenant)
	_, officerMFA, _, _ := f.signInMFA(t, ctx, "officer")
	_, officerNoMFA := f.signIn(t, ctx, "officer")
	_, adminMFA, _, _ := f.signInMFA(t, ctx, "school_admin")
	_, adminNoMFA := f.signIn(t, ctx, "school_admin")
	_, student := f.signIn(t, ctx, "student")
	_, outsider := f.signIn(t, ctx, "")
	const ip = "198.51.100.43"

	rec := f.do(http.MethodPost, "/api/contacts", `{"name":"Permisos Ficticios","headingId":"`+headings["gazebos"]+`"}`, ip, officerMFA, nil)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	var c contactDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &c)

	// Search authorization: officer+MFA, school_admin+MFA -> 200; officer/admin without MFA -> 403 mfa_required; student/outsider -> 403 forbidden; unauth -> 401.
	for _, tc := range []struct {
		name   string
		token  string
		status int
		body   string
	}{
		{"unauthenticated", "", http.StatusUnauthorized, unauthBody},
		{"student", student, http.StatusForbidden, `{"error":"forbidden"}`},
		{"outsider", outsider, http.StatusForbidden, `{"error":"forbidden"}`},
		{"officer without MFA", officerNoMFA, http.StatusForbidden, `{"error":"mfa_required"}`},
		{"admin without MFA", adminNoMFA, http.StatusForbidden, `{"error":"mfa_required"}`},
		{"admin with MFA", adminMFA, http.StatusOK, ""},
		{"officer with MFA", officerMFA, http.StatusOK, ""},
	} {
		t.Run("search/"+tc.name, func(t *testing.T) {
			r := f.do(http.MethodGet, "/api/shared-contacts", "", ip, tc.token, nil)
			if tc.status == http.StatusOK {
				if r.Code != http.StatusOK {
					t.Fatalf("GET /api/shared-contacts = %d body=%s", r.Code, r.Body)
				}
			} else {
				wantResponse(t, r, tc.status, tc.body)
			}
		})
	}

	// Share authorization: only officer with MFA may share; nothing written for unauthorized callers.
	for _, tc := range []struct {
		name   string
		token  string
		status int
		body   string
	}{
		{"unauthenticated", "", http.StatusUnauthorized, unauthBody},
		{"student", student, http.StatusForbidden, `{"error":"forbidden"}`},
		{"outsider", outsider, http.StatusForbidden, `{"error":"forbidden"}`},
		{"admin with MFA", adminMFA, http.StatusForbidden, `{"error":"forbidden"}`},
		{"admin without MFA", adminNoMFA, http.StatusForbidden, `{"error":"forbidden"}`},
		{"officer without MFA", officerNoMFA, http.StatusForbidden, `{"error":"mfa_required"}`},
	} {
		t.Run("share/"+tc.name, func(t *testing.T) {
			wantResponse(t, f.do(http.MethodPost, "/api/contacts/"+c.ID+"/share", "", ip, tc.token, nil), tc.status, tc.body)
		})
	}

	var links int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM shared_contact_links WHERE tenant_id = $1`, f.tenant).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if links != 0 {
		t.Fatalf("unauthorized share wrote %d links", links)
	}

	// Copy and Withdraw authorization matrix (R6): only officer with MFA; nothing created or withdrawn.
	var pendingID, approvedID string
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO shared_contacts (status, heading_name, name) VALUES ('pending', 'gazebos', 'Auth Pendiente') RETURNING id::text`).Scan(&pendingID); err != nil {
		t.Fatal(err)
	}
	if err := tdb.Super.QueryRow(ctx, `INSERT INTO shared_contacts (status, heading_name, name) VALUES ('approved', 'gazebos', 'Auth Aprobado') RETURNING id::text`).Scan(&approvedID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = tdb.Super.Exec(context.Background(), `DELETE FROM shared_contacts WHERE id IN ($1, $2)`, pendingID, approvedID)
	})
	if _, err := tdb.Super.Exec(ctx, `INSERT INTO shared_contact_links (tenant_id, contact_id, shared_contact_id) VALUES ($1, $2, $3)`, f.tenant, c.ID, pendingID); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		token  string
		status int
		body   string
	}{
		{"unauthenticated", "", http.StatusUnauthorized, unauthBody},
		{"student", student, http.StatusForbidden, `{"error":"forbidden"}`},
		{"outsider", outsider, http.StatusForbidden, `{"error":"forbidden"}`},
		{"admin with MFA", adminMFA, http.StatusForbidden, `{"error":"forbidden"}`},
		{"admin without MFA", adminNoMFA, http.StatusForbidden, `{"error":"forbidden"}`},
		{"officer without MFA", officerNoMFA, http.StatusForbidden, `{"error":"mfa_required"}`},
	} {
		t.Run("copy/"+tc.name, func(t *testing.T) {
			wantResponse(t, f.do(http.MethodPost, "/api/shared-contacts/"+approvedID+"/copy", "", ip, tc.token, nil), tc.status, tc.body)
		})
		t.Run("withdraw/"+tc.name, func(t *testing.T) {
			wantResponse(t, f.do(http.MethodDelete, "/api/contacts/"+c.ID+"/share", "", ip, tc.token, nil), tc.status, tc.body)
		})
	}

	var contactCount int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM contacts WHERE tenant_id = $1`, f.tenant).Scan(&contactCount); err != nil || contactCount != 1 {
		t.Fatalf("unauthorized copy altered contacts count=%d err=%v", contactCount, err)
	}
	var st string
	if err := tdb.Super.QueryRow(ctx, `SELECT status FROM shared_contacts WHERE id = $1`, pendingID).Scan(&st); err != nil || st != "pending" {
		t.Fatalf("unauthorized withdraw altered status=%q err=%v", st, err)
	}
}

func TestSharedContactsCopyAndIndependence(t *testing.T) {
	ctx := integration(t)
	fA := newFixture(t, ctx)
	fB := newFixture(t, ctx)
	headingsA := seedHeadingsForTenant(t, ctx, fA.tenant)
	headingsB := seedHeadingsForTenant(t, ctx, fB.tenant)
	_, officerA, _, _ := fA.signInMFA(t, ctx, "officer")
	_, officerB, _, _ := fB.signInMFA(t, ctx, "officer")
	const ip = "198.51.100.44"

	// Remove "gazebos" from School B so R1 falls back to "otros".
	wantResponse(t, fB.do(http.MethodDelete, "/api/contacts/headings/"+headingsB["gazebos"], "", ip, officerB, nil), http.StatusNoContent, "")

	rec := fA.do(http.MethodPost, "/api/contacts", `{
		"name":"Gazebos Ficticios SRL",
		"headingId":"`+headingsA["gazebos"]+`",
		"phone":"381 555-0000",
		"email":"ventas@gazebos-ficticios.example",
		"zone":"Yerba Buena",
		"notes":"Nota confidencial A"
	}`, ip, officerA, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create A = %d body=%s", rec.Code, rec.Body)
	}
	var cA contactWithShareDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &cA)
	rec = fA.do(http.MethodPost, "/api/contacts/"+cA.ID+"/reference-prices", `{"description":"Gazebo 3×3","amountCents":2500000,"date":"2026-09-01"}`, ip, officerA, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add price = %d body=%s", rec.Code, rec.Body)
	}

	rec = fA.do(http.MethodPost, "/api/contacts/"+cA.ID+"/share", "", ip, officerA, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("share = %d body=%s", rec.Code, rec.Body)
	}
	var sh shareResponseDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &sh)
	t.Cleanup(func() {
		_, _ = tdb.Super.Exec(context.Background(), `DELETE FROM shared_contacts WHERE id = $1`, sh.ID)
	})

	// R1 negative: pending or nonexistent shared contact returns 404 not_found and creates nothing.
	wantResponse(t, fB.do(http.MethodPost, "/api/shared-contacts/"+sh.ID+"/copy", "", ip, officerB, nil), http.StatusNotFound, `{"error":"not_found"}`)
	wantResponse(t, fB.do(http.MethodPost, "/api/shared-contacts/00000000-0000-0000-0000-000000000000/copy", "", ip, officerB, nil), http.StatusNotFound, `{"error":"not_found"}`)
	rec = fB.do(http.MethodGet, "/api/contacts", "", ip, officerB, nil)
	var emptyB []contactWithShareDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &emptyB)
	if len(emptyB) != 0 {
		t.Fatalf("rejected copies created %d contacts in B, want 0", len(emptyB))
	}

	if err := contacts.ApproveShared(ctx, tdb.Super, sh.ID, time.Now()); err != nil {
		t.Fatal(err)
	}

	// R1 + R8: Beto Ficticio (Escuela B) copies "Gazebos Ficticios SRL" -> enters "otros" without notes or school.
	rec = fB.do(http.MethodPost, "/api/shared-contacts/"+sh.ID+"/copy", "", ip, officerB, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("copy = %d body=%s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), fA.tenant) || strings.Contains(rec.Body.String(), fA.tenantName) || strings.Contains(rec.Body.String(), "Nota confidencial A") {
		t.Fatalf("copy response leaked source school or notes: %s", rec.Body.String())
	}
	var copy1 contactWithShareDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &copy1)
	if copy1.HeadingName != "otros" || copy1.HeadingID != headingsB["otros"] ||
		copy1.Name != "Gazebos Ficticios SRL" || copy1.Phone != "381 555-0000" ||
		copy1.Email != "ventas@gazebos-ficticios.example" || copy1.Zone != "Yerba Buena" ||
		copy1.Notes != "" || copy1.ShareStatus != nil || len(copy1.ReferencePrices) != 1 ||
		copy1.ReferencePrices[0].Description != "Gazebo 3×3" || copy1.ReferencePrices[0].AmountCents != 2500000 {
		t.Fatalf("copy1 = %+v", copy1)
	}

	// R2: Copying twice creates two independent contacts.
	rec = fB.do(http.MethodPost, "/api/shared-contacts/"+sh.ID+"/copy", "", ip, officerB, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("second copy = %d body=%s", rec.Code, rec.Body)
	}
	var copy2 contactWithShareDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &copy2)
	if copy2.ID == copy1.ID {
		t.Fatalf("duplicate copy reused id %s", copy1.ID)
	}

	// R2: Editing B's copy does not alter shared entry or A's contact.
	rec = fB.do(http.MethodPatch, "/api/contacts/"+copy1.ID, `{"phone":"381 999-9999"}`, ip, officerB, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch copy = %d body=%s", rec.Code, rec.Body)
	}
	rec = fB.do(http.MethodGet, "/api/shared-contacts", "", ip, officerB, nil)
	var page sharedContactPageDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0].Phone != "381 555-0000" {
		t.Fatalf("shared entry altered by copy edit: %+v", page.Items)
	}
	rec = fA.do(http.MethodGet, "/api/contacts", "", ip, officerA, nil)
	var listA []contactWithShareDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &listA)
	if len(listA) != 1 || listA[0].Phone != "381 555-0000" {
		t.Fatalf("A contact altered by B copy edit: %+v", listA)
	}

	// R2 + R3: A withdraws -> leaves shared directory, copy in B remains, and copying withdrawn returns 404.
	wantResponse(t, fA.do(http.MethodDelete, "/api/contacts/"+cA.ID+"/share", "", ip, officerA, nil), http.StatusNoContent, "")
	wantResponse(t, fB.do(http.MethodPost, "/api/shared-contacts/"+sh.ID+"/copy", "", ip, officerB, nil), http.StatusNotFound, `{"error":"not_found"}`)

	rec = fB.do(http.MethodGet, "/api/contacts", "", ip, officerB, nil)
	var listB []contactWithShareDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &listB)
	if len(listB) != 2 {
		t.Fatalf("B contacts after A withdraw = %d, want 2", len(listB))
	}
}

func TestSharedContactsWithdrawAndReshareHTTP(t *testing.T) {
	ctx := integration(t)
	fA := newFixture(t, ctx)
	fB := newFixture(t, ctx)
	headingsA := seedHeadingsForTenant(t, ctx, fA.tenant)
	_, officerA, _, _ := fA.signInMFA(t, ctx, "officer")
	_, officerB, _, _ := fB.signInMFA(t, ctx, "officer")
	const ip = "198.51.100.45"

	rec := fA.do(http.MethodPost, "/api/contacts", `{
		"name":"Proveedor Ana",
		"headingId":"`+headingsA["gazebos"]+`",
		"phone":"381 100-0001"
	}`, ip, officerA, nil)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	var cA contactWithShareDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &cA)

	t.Cleanup(func() {
		_, _ = tdb.Super.Exec(context.Background(), `
			DELETE FROM shared_contacts
			WHERE id IN (SELECT shared_contact_id FROM shared_contact_links WHERE tenant_id = $1)`, fA.tenant)
	})

	// R3 negative: unshared own contact -> 404 not_shared; other school's contact -> 404 not_found.
	wantResponse(t, fA.do(http.MethodDelete, "/api/contacts/"+cA.ID+"/share", "", ip, officerA, nil), http.StatusNotFound, `{"error":"not_shared"}`)
	wantResponse(t, fB.do(http.MethodDelete, "/api/contacts/"+cA.ID+"/share", "", ip, officerB, nil), http.StatusNotFound, `{"error":"not_found"}`)

	// Share pending, then withdraw pending -> 204, shareStatus cleared.
	rec = fA.do(http.MethodPost, "/api/contacts/"+cA.ID+"/share", "", ip, officerA, nil)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	wantResponse(t, fA.do(http.MethodDelete, "/api/contacts/"+cA.ID+"/share", "", ip, officerA, nil), http.StatusNoContent, "")
	wantResponse(t, fA.do(http.MethodDelete, "/api/contacts/"+cA.ID+"/share", "", ip, officerA, nil), http.StatusNotFound, `{"error":"not_shared"}`)

	// Re-share and approve v1.
	rec = fA.do(http.MethodPost, "/api/contacts/"+cA.ID+"/share", "", ip, officerA, nil)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	var v1 shareResponseDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &v1)
	if err := contacts.ApproveShared(ctx, tdb.Super, v1.ID, time.Now()); err != nil {
		t.Fatal(err)
	}

	// R3 negative cross-tenant: School B cannot withdraw School A's approved shared contact.
	wantResponse(t, fB.do(http.MethodDelete, "/api/contacts/"+cA.ID+"/share", "", ip, officerB, nil), http.StatusNotFound, `{"error":"not_found"}`)

	// R4: Ana edits phone -> shared search keeps old phone; own contact reports shareOutdated=true.
	rec = fA.do(http.MethodPatch, "/api/contacts/"+cA.ID, `{"phone":"381 200-0002"}`, ip, officerA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d body=%s", rec.Code, rec.Body)
	}
	var patched contactWithShareDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if !patched.ShareOutdated || !patched.HasApprovedShare || patched.ShareStatus == nil || *patched.ShareStatus != "approved" {
		t.Fatalf("patched = %+v, want shareOutdated=true hasApprovedShare=true shareStatus=approved", patched)
	}
	rec = fB.do(http.MethodGet, "/api/shared-contacts", "", ip, officerB, nil)
	var page sharedContactPageDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0].Phone != "381 100-0001" {
		t.Fatalf("shared search after edit = %+v, want old phone", page.Items)
	}

	// R5: Re-share while approved -> 201 pending v2; v1 stays visible; second re-share -> 409 already_pending.
	rec = fA.do(http.MethodPost, "/api/contacts/"+cA.ID+"/share", "", ip, officerA, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("re-share = %d body=%s", rec.Code, rec.Body)
	}
	var v2 shareResponseDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &v2)
	if v2.ID == v1.ID || v2.Status != "pending" {
		t.Fatalf("v2 = %+v", v2)
	}
	wantResponse(t, fA.do(http.MethodPost, "/api/contacts/"+cA.ID+"/share", "", ip, officerA, nil), http.StatusConflict, `{"error":"already_pending"}`)
	var linkCount int
	if err := tdb.Super.QueryRow(ctx, `SELECT count(*) FROM shared_contact_links WHERE tenant_id = $1 AND contact_id = $2`, fA.tenant, cA.ID).Scan(&linkCount); err != nil || linkCount != 3 {
		t.Fatalf("links after 409 already_pending = %d (err=%v), want 3", linkCount, err)
	}

	rec = fB.do(http.MethodGet, "/api/shared-contacts", "", ip, officerB, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0].ID != v1.ID {
		t.Fatalf("shared search while v2 pending = %+v, want v1", page.Items)
	}

	// R5: Approving v2 replaces v1 in shared search and clears shareOutdated.
	if err := contacts.ApproveShared(ctx, tdb.Super, v2.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	rec = fB.do(http.MethodGet, "/api/shared-contacts", "", ip, officerB, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0].ID != v2.ID || page.Items[0].Phone != "381 200-0002" {
		t.Fatalf("shared search after approve v2 = %+v, want v2", page.Items)
	}
	rec = fA.do(http.MethodGet, "/api/contacts", "", ip, officerA, nil)
	var listA []contactWithShareDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &listA)
	if len(listA) != 1 || listA[0].ShareOutdated || listA[0].ShareStatus == nil || *listA[0].ShareStatus != "approved" {
		t.Fatalf("listA after approve v2 = %+v", listA)
	}

	// R3: Withdraw approved -> 204 and disappears from search for all schools.
	wantResponse(t, fA.do(http.MethodDelete, "/api/contacts/"+cA.ID+"/share", "", ip, officerA, nil), http.StatusNoContent, "")
	for _, tok := range []string{officerA, officerB} {
		rec = fA.do(http.MethodGet, "/api/shared-contacts", "", ip, tok, nil)
		if tok == officerB {
			rec = fB.do(http.MethodGet, "/api/shared-contacts", "", ip, tok, nil)
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &page)
		if len(page.Items) != 0 {
			t.Fatalf("withdrawn still visible: %+v", page.Items)
		}
	}
}
