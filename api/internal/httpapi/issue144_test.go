package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestIssue144DueFreezeMatchesUpdate(t *testing.T) {
	for _, kind := range []string{"partial", "exemption", "reversed", "none"} {
		t.Run(kind, func(t *testing.T) {
			p := newPayFixture(t)
			initial := p.do(http.MethodPatch, "/api/finance/dues/"+p.due, `{"amountCents":125050}`, "198.51.100.10", p.officer, nil)
			if initial.Code != http.StatusOK {
				t.Fatalf("initial update: %d %s", initial.Code, initial.Body)
			}
			if kind == "partial" || kind == "reversed" {
				payment := p.payOK(t, 100000)
				if kind == "reversed" {
					rec := p.do(http.MethodPost, "/api/finance/entries/"+payment.EntryID+"/reversal", "", "198.51.100.10", p.officer, nil)
					if rec.Code != http.StatusCreated {
						t.Fatalf("reversal: %d %s", rec.Code, rec.Body)
					}
				}
			}
			if kind == "exemption" {
				rec := p.do(http.MethodPost, "/api/finance/dues/"+p.due+"/exemptions", exemptBody(p.betoID, "test"), "198.51.100.10", p.officer, nil)
				if rec.Code != http.StatusCreated {
					t.Fatalf("exemption: %d %s", rec.Code, rec.Body)
				}
			}
			rec := p.do(http.MethodGet, "/api/finance/dues", "", "198.51.100.10", p.officer, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("list: %d %s", rec.Code, rec.Body)
			}
			var dues []struct {
				ID     string `json:"id"`
				Frozen *bool  `json:"frozen"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &dues); err != nil {
				t.Fatal(err)
			}
			if len(dues) != 1 || dues[0].ID != p.due || dues[0].Frozen == nil {
				t.Fatalf("missing frozen summary: %s", rec.Body)
			}
			frozen := kind != "none"
			if *dues[0].Frozen != frozen {
				t.Fatalf("frozen = %v, want %v", *dues[0].Frozen, frozen)
			}
			for _, body := range []string{`{"amountCents":150000}`, `{"dueDate":"2026-12-15"}`} {
				patch := p.do(http.MethodPatch, "/api/finance/dues/"+p.due, body, "198.51.100.10", p.officer, nil)
				if frozen {
					wantResponse(t, patch, http.StatusConflict, `{"error":"due_frozen"}`)
				} else if patch.Code != http.StatusOK {
					t.Fatalf("update: %d %s", patch.Code, patch.Body)
				}
			}
			readback := p.do(http.MethodGet, "/api/finance/dues/"+p.due, "", "198.51.100.10", p.officer, nil)
			if readback.Code != http.StatusOK {
				t.Fatalf("readback: %d %s", readback.Code, readback.Body)
			}
			var updated struct {
				AmountCents int64  `json:"amountCents"`
				DueDate     string `json:"dueDate"`
			}
			if err := json.Unmarshal(readback.Body.Bytes(), &updated); err != nil {
				t.Fatal(err)
			}
			wantAmount, wantDate := int64(150000), "2026-12-15"
			if frozen {
				wantAmount, wantDate = 125050, "2026-11-30"
			}
			if updated.AmountCents != wantAmount || updated.DueDate != wantDate {
				t.Fatalf("updated: %+v", updated)
			}
		})
	}
}

func TestIssue144DeletePreservesFreeze(t *testing.T) {
	for _, frozen := range []bool{false, true} {
		t.Run(fmt.Sprint(frozen), func(t *testing.T) {
			p := newPayFixture(t)
			if frozen {
				p.payOK(t, 100000)
			}
			rec := p.do(http.MethodDelete, "/api/finance/dues/"+p.due, "", "198.51.100.10", p.officer, nil)
			if frozen {
				wantResponse(t, rec, http.StatusConflict, `{"error":"due_frozen"}`)
			} else if rec.Code != http.StatusNoContent {
				t.Fatalf("delete: %d %s", rec.Code, rec.Body)
			}
			readback := p.do(http.MethodGet, "/api/finance/dues/"+p.due, "", "198.51.100.10", p.officer, nil)
			want := http.StatusNotFound
			if frozen {
				want = http.StatusOK
			}
			if readback.Code != want {
				t.Fatalf("readback: %d %s", readback.Code, readback.Body)
			}
		})
	}
}
