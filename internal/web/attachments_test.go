package web

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/attachments"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

func uploadReceipt(t *testing.T, f *webFixture, filename, content string, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("WriteField: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/attachments", &body)
	req.SetBasicAuth("admin", "secret")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set(syncRequestHeader, "1")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func webTransactionID(t *testing.T, f *webFixture, externalID string) int64 {
	t.Helper()
	transactions, err := f.store.ListTransactions(context.Background(), storage.TransactionFilter{AccountID: "acc-1", Limit: 20})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	for _, tx := range transactions {
		if tx.ExternalID == externalID {
			return tx.ID
		}
	}
	t.Fatalf("transaction %s not found", externalID)
	return 0
}

func TestReceiptUploadAutoPairAndDownload(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	service, err := attachments.New(f.store, t.TempDir(), 1<<20, f.server.now)
	if err != nil {
		t.Fatalf("attachments.New: %v", err)
	}
	f.server.attachments = service

	content := "invoice-pdf-content"
	resp := uploadReceipt(t, f, "factura-2026-03-10-total-12,50.pdf", content, nil)
	if resp.Code != http.StatusSeeOther {
		t.Fatalf("upload status = %d body=%s", resp.Code, resp.Body.String())
	}
	if location := resp.Header().Get("Location"); !strings.Contains(location, "attachment=paired") {
		t.Fatalf("redirect = %q", location)
	}
	txID := webTransactionID(t, f, "tx-1")
	attached, err := service.ListForTransaction(context.Background(), txID)
	if err != nil {
		t.Fatalf("ListForTransaction: %v", err)
	}
	if len(attached) != 1 || attached[0].SizeBytes != int64(len(content)) {
		t.Fatalf("attached = %+v", attached)
	}
	download := f.request(t, http.MethodGet, "/attachments/"+strconv.FormatInt(attached[0].ID, 10), true, nil)
	if download.Code != http.StatusOK || download.Body.String() != content {
		t.Fatalf("download status=%d body=%q", download.Code, download.Body.String())
	}
	if got := download.Header().Get("Content-Type"); !strings.Contains(got, "pdf") {
		t.Fatalf("content type = %q", got)
	}

	page := f.request(t, http.MethodGet, "/transactions", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("transactions status = %d", page.Code)
	}
	for _, want := range []string{"Receipt inbox", "Attached", "Upload receipt"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("transactions page missing %q", want)
		}
	}
}

func TestReceiptUploadUnpairedAndManualLink(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	service, err := attachments.New(f.store, t.TempDir(), 1<<20, f.server.now)
	if err != nil {
		t.Fatalf("attachments.New: %v", err)
	}
	f.server.attachments = service

	resp := uploadReceipt(t, f, "scanned-receipt.pdf", "scan", nil)
	if resp.Code != http.StatusSeeOther || !strings.Contains(resp.Header().Get("Location"), "attachment=unpaired") {
		t.Fatalf("upload = %d %q", resp.Code, resp.Header().Get("Location"))
	}
	unpaired, err := service.ListUnpaired(context.Background())
	if err != nil {
		t.Fatalf("ListUnpaired: %v", err)
	}
	if len(unpaired) != 1 {
		t.Fatalf("unpaired = %+v", unpaired)
	}
	txID := webTransactionID(t, f, "tx-1")
	body := strings.NewReader("transaction_id=" + strconv.FormatInt(txID, 10))
	req := httptest.NewRequest(http.MethodPost, "/attachments/"+strconv.FormatInt(unpaired[0].ID, 10)+"/pair", body)
	req.SetBasicAuth("admin", "secret")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(syncRequestHeader, "1")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "attachment=linked") {
		t.Fatalf("pair = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	unpaired, err = service.ListUnpaired(context.Background())
	if err != nil {
		t.Fatalf("ListUnpaired after pair: %v", err)
	}
	if len(unpaired) != 0 {
		t.Fatalf("unpaired after pair = %+v", unpaired)
	}
	apiResp := f.request(t, http.MethodGet, "/api/attachments/unpaired", true, nil)
	if apiResp.Code != http.StatusOK || !strings.Contains(apiResp.Body.String(), "[]") {
		t.Fatalf("unpaired API = %d %s", apiResp.Code, apiResp.Body.String())
	}
}

func TestAttachmentMutationsRequireRequestHeader(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	service, err := attachments.New(f.store, t.TempDir(), 1<<20, f.server.now)
	if err != nil {
		t.Fatalf("attachments.New: %v", err)
	}
	f.server.attachments = service

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "receipt.pdf")
	_, _ = part.Write([]byte("receipt"))
	_ = writer.Close()
	req := httptest.NewRequest(http.MethodPost, "/attachments", &body)
	req.SetBasicAuth("admin", "secret")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unprotected upload = %d, want 403", rec.Code)
	}

	att, err := service.Save(context.Background(), attachments.SaveInput{
		Reader: strings.NewReader("receipt"), Filename: "receipt.pdf", ContentType: "application/pdf",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	pair := strings.NewReader("transaction_id=" + strconv.FormatInt(webTransactionID(t, f, "tx-1"), 10))
	req = httptest.NewRequest(http.MethodPost, "/attachments/"+strconv.FormatInt(att.ID, 10)+"/pair", pair)
	req.SetBasicAuth("admin", "secret")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unprotected pair = %d, want 403", rec.Code)
	}
}

func TestReceiptDownloadForcesSafeHeaders(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	service, err := attachments.New(f.store, t.TempDir(), 1<<20, f.server.now)
	if err != nil {
		t.Fatalf("attachments.New: %v", err)
	}
	f.server.attachments = service

	att, err := service.Save(context.Background(), attachments.SaveInput{
		Reader:      strings.NewReader("<html><script>alert(1)</script></html>"),
		Filename:    `evil"name.html`,
		ContentType: "text/html",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	download := f.request(t, http.MethodGet, "/attachments/"+strconv.FormatInt(att.ID, 10), true, nil)
	if download.Code != http.StatusOK {
		t.Fatalf("download status = %d", download.Code)
	}
	if got := download.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("content type = %q, want application/octet-stream", got)
	}
	if got := download.Header().Get("Content-Disposition"); got != `attachment; filename="evilname.html"` {
		t.Fatalf("content disposition = %q", got)
	}
	if got := download.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("nosniff = %q", got)
	}
}

func TestReceiptUploadRequiresAuthentication(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	service, err := attachments.New(f.store, t.TempDir(), 1<<20, f.server.now)
	if err != nil {
		t.Fatalf("attachments.New: %v", err)
	}
	f.server.attachments = service
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "receipt.pdf")
	_, _ = part.Write([]byte("x"))
	_ = writer.Close()
	req := httptest.NewRequest(http.MethodPost, "/attachments", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated upload = %d", rec.Code)
	}
}
