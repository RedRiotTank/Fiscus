package web

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/attachments"
	"github.com/RedRiotTank/Fiscus/internal/money"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

const maxUploadRequestBytes = 100 << 20

var attachmentDownloadTypes = map[string]string{
	"application/pdf": "application/pdf",
	"image/jpeg":      "image/jpeg",
	"image/png":       "image/png",
	"image/webp":      "image/webp",
}

func (s *Server) loadAttachmentData(ctx context.Context, data *PageData) error {
	data.AttachmentsEnabled = s.attachments != nil
	if s.attachments == nil {
		return nil
	}
	if len(data.Transactions) > 0 {
		ids := make([]int64, 0, len(data.Transactions))
		for _, tx := range data.Transactions {
			ids = append(ids, tx.ID)
		}
		attached, err := s.store.AttachmentIDsForTransactions(ctx, ids)
		if err != nil {
			return err
		}
		data.AttachmentIDs = attached
	}
	unpaired, err := s.attachments.ListUnpaired(ctx)
	if err != nil {
		return err
	}
	data.UnpairedAttachments = unpaired
	return nil
}

func attachmentNotice(code string) string {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "paired":
		return "Receipt uploaded and paired automatically."
	case "unpaired":
		return "Receipt stored in the unpaired inbox. Link it to a transaction manually."
	case "linked":
		return "Receipt linked to the selected transaction."
	case "error":
		return "The receipt could not be stored. Check the file type and size."
	default:
		return ""
	}
}

func (s *Server) handleAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	if !validSyncRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if s.attachments == nil {
		http.Error(w, "Receipt storage is disabled", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequestBytes)
	// #nosec G120 -- the request body is bounded by MaxBytesReader above.
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		s.redirectAttachment(w, r, "error")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	file, header, err := r.FormFile("file")
	if err != nil {
		s.redirectAttachment(w, r, "error")
		return
	}
	defer func() { _ = file.Close() }()

	input := attachments.SaveInput{
		Reader:         file,
		Filename:       header.Filename,
		ContentType:    header.Header.Get("Content-Type"),
		ExtractionText: r.FormValue("caption"),
		Currency:       strings.ToUpper(strings.TrimSpace(r.FormValue("currency"))),
	}
	if input.Currency == "" {
		input.Currency = "EUR"
	}
	if raw := strings.TrimSpace(r.FormValue("transaction_id")); raw != "" {
		id, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || id <= 0 {
			s.redirectAttachment(w, r, "error")
			return
		}
		input.TransactionID = id
	}
	if raw := strings.TrimSpace(r.FormValue("date")); raw != "" {
		date, parseErr := time.Parse("2006-01-02", raw)
		if parseErr != nil {
			s.redirectAttachment(w, r, "error")
			return
		}
		input.Date = &date
	}
	if raw := strings.TrimSpace(r.FormValue("amount")); raw != "" {
		amount, parseErr := parseMajorAmount(raw, input.Currency)
		if parseErr != nil {
			s.redirectAttachment(w, r, "error")
			return
		}
		input.AmountMinor = &amount
	}

	att, err := s.attachments.SaveWeb(r.Context(), input)
	if err != nil {
		s.logger.Error("receipt upload failed", "error", err)
		s.redirectAttachment(w, r, "error")
		return
	}
	if att.Paired() {
		s.redirectAttachment(w, r, "paired")
		return
	}
	s.redirectAttachment(w, r, "unpaired")
}

func (s *Server) redirectAttachment(w http.ResponseWriter, r *http.Request, code string) {
	target := "/transactions?attachment=" + code
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) handleAttachmentPair(w http.ResponseWriter, r *http.Request) {
	if !validSyncRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if s.attachments == nil {
		http.Error(w, "Receipt storage is disabled", http.StatusServiceUnavailable)
		return
	}
	attachmentID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || attachmentID <= 0 {
		http.NotFound(w, r)
		return
	}
	transactionID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("transaction_id")), 10, 64)
	if err != nil || transactionID <= 0 {
		s.redirectAttachment(w, r, "error")
		return
	}
	if err := s.attachments.Attach(r.Context(), attachmentID, transactionID); err != nil {
		s.logger.Error("receipt pairing failed", "attachment_id", attachmentID, "transaction_id", transactionID, "error", err)
		s.redirectAttachment(w, r, "error")
		return
	}
	s.redirectAttachment(w, r, "linked")
}

func (s *Server) handleAttachmentDownload(w http.ResponseWriter, r *http.Request) {
	if s.attachments == nil {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	att, file, err := s.attachments.Open(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.Error("receipt download failed", "attachment_id", id, "error", err)
		http.Error(w, "Receipt unavailable", http.StatusInternalServerError)
		return
	}
	defer func() { _ = file.Close() }()
	filename := attachmentDownloadFilename(att.Filename)
	w.Header().Set("Content-Type", attachmentDownloadContentType(att.ContentType, filename))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, filename, att.UploadedAt, file)
}

func attachmentDownloadFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, name)
	name = strings.Trim(name, ". ")
	if name == "" || name == "." || name == ".." {
		return "attachment"
	}
	return name
}

func attachmentDownloadContentType(stored, filename string) string {
	if mediaType, _, err := mime.ParseMediaType(stored); err == nil {
		if allowed, ok := attachmentDownloadTypes[strings.ToLower(mediaType)]; ok {
			return allowed
		}
	}
	if detected := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))); detected != "" {
		if mediaType, _, err := mime.ParseMediaType(detected); err == nil {
			if allowed, ok := attachmentDownloadTypes[strings.ToLower(mediaType)]; ok {
				return allowed
			}
		}
	}
	return "application/octet-stream"
}

type attachmentResponse struct {
	ID                   int64      `json:"id"`
	TransactionID        *int64     `json:"transaction_id,omitempty"`
	Filename             string     `json:"filename"`
	ContentType          string     `json:"content_type"`
	SizeBytes            int64      `json:"size_bytes"`
	UploadedAt           time.Time  `json:"uploaded_at"`
	Source               string     `json:"source"`
	ExtractedDate        *time.Time `json:"extracted_date,omitempty"`
	ExtractedAmountMinor *int64     `json:"extracted_amount_minor,omitempty"`
	ExtractedCurrency    string     `json:"extracted_currency,omitempty"`
}

func (s *Server) handleUnpairedAttachments(w http.ResponseWriter, r *http.Request) {
	if s.attachments == nil {
		s.writeJSON(w, http.StatusOK, []attachmentResponse{})
		return
	}
	attachments, err := s.attachments.ListUnpaired(r.Context())
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list receipts"})
		return
	}
	out := make([]attachmentResponse, 0, len(attachments))
	for _, att := range attachments {
		out = append(out, attachmentResponse{
			ID: att.ID, TransactionID: att.TransactionID, Filename: att.Filename,
			ContentType: att.ContentType, SizeBytes: att.SizeBytes, UploadedAt: att.UploadedAt,
			Source: att.Source, ExtractedDate: att.ExtractedDate,
			ExtractedAmountMinor: att.ExtractedAmountMinor, ExtractedCurrency: att.ExtractedCurrency,
		})
	}
	s.writeJSON(w, http.StatusOK, out)
}

func parseMajorAmount(raw, currency string) (int64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, errors.New("empty amount")
	}
	if strings.Contains(value, ",") && !strings.Contains(value, ".") {
		value = strings.ReplaceAll(value, ",", ".")
	}
	minor, err := money.Parse(value, currency)
	if err != nil || minor <= 0 {
		return 0, errors.New("invalid amount")
	}
	return minor, nil
}
