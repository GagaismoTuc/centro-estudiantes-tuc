package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/finance"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/oapi"
)

const receiptTransferTimeout = 2 * time.Minute

func financeReceipt(r finance.Receipt) oapi.FinanceReceipt {
	return oapi.FinanceReceipt{
		Id:          r.ID,
		ContentType: oapi.FinanceReceiptContentType(r.ContentType),
		SizeBytes:   r.SizeBytes,
		UploadedAt:  r.UploadedAt,
	}
}

func (s *server) GetFinanceEntryReceipts(w http.ResponseWriter, r *http.Request, entryID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	list, err := s.finance.ListReceipts(r.Context(), sess, entryID)
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	out := make([]oapi.FinanceReceipt, 0, len(list))
	for _, rec := range list {
		out = append(out, financeReceipt(rec))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) PostFinanceEntryReceipts(w http.ResponseWriter, r *http.Request, entryID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	// Check officer + MFA before reading up to 10 MiB from the body (R7).
	if err := s.finance.Authorize(r.Context(), sess); err != nil {
		writeFinanceError(w, err)
		return
	}
	// Extend the server's 15s read deadline for slow 10 MiB uploads.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(receiptTransferTimeout))
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, finance.MaxReceiptBytes))
	if tooLarge := (*http.MaxBytesError)(nil); errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "file_too_large")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	rec, err := s.finance.AddReceipt(r.Context(), sess, entryID, data)
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, financeReceipt(rec))
}

func (s *server) GetFinanceEntryReceipt(w http.ResponseWriter, r *http.Request, entryID string, receiptID string) {
	sess, ok := rosterSession(w, r)
	if !ok {
		return
	}
	rec, err := s.finance.GetReceipt(r.Context(), sess, entryID, receiptID)
	if err != nil {
		writeFinanceError(w, err)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(receiptTransferTimeout))
	ext := finance.ReceiptExt(rec.ContentType)
	w.Header().Set("Content-Type", rec.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="comprobante-%s.%s"`, rec.ID, ext))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(rec.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(rec.Data)
}
