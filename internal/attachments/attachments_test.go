package attachments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

type fixture struct {
	store   *storage.Store
	service *Service
	dir     string
	account domain.Account
	tx      domain.Transaction
	now     time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "attachments.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	connection := domain.Connection{RequisitionID: "req-1", InstitutionName: "Test Bank", Status: "LN"}
	if err := store.UpsertConnection(ctx, &connection); err != nil {
		t.Fatalf("UpsertConnection: %v", err)
	}
	account := domain.Account{
		ID: "acc-1", ConnectionID: connection.ID, Name: "Business", Currency: "EUR",
		Type: domain.AccountTypeBusiness,
	}
	if err := store.UpsertAccount(ctx, &account); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	tx := domain.Transaction{
		AccountID: "acc-1", ExternalID: "tx-1", AmountMinor: -1234, Currency: "EUR",
		BookingDate:  time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC),
		Counterparty: "ACME", Description: "Invoice", Category: "Supplies",
		Status: domain.TransactionStatusBooked,
	}
	if _, _, err := store.UpsertTransaction(ctx, &tx); err != nil {
		t.Fatalf("UpsertTransaction: %v", err)
	}
	dir := t.TempDir()
	service, err := New(store, dir, 64<<10, func() time.Time { return now })
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &fixture{store: store, service: service, dir: dir, account: account, tx: tx, now: now}
}

func TestSaveAutoPairsExactAmountAndDate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	content := []byte("invoice contents")
	att, err := f.service.SaveWeb(context.Background(), SaveInput{
		Reader:         bytes.NewReader(content),
		Filename:       "factura-2026-03-05-total-12,34.pdf",
		ContentType:    "application/pdf",
		ExtractionText: "ACME invoice",
	})
	if err != nil {
		t.Fatalf("SaveWeb: %v", err)
	}
	if !att.Paired() || att.TransactionID == nil || *att.TransactionID != f.tx.ID {
		t.Fatalf("attachment was not paired: %+v", att)
	}
	if att.SizeBytes != int64(len(content)) || att.SHA256 != hashHex(content) {
		t.Fatalf("stored integrity metadata = %+v", att)
	}
	if att.ExtractedDate == nil || att.ExtractedDate.Format("2006-01-02") != "2026-03-05" {
		t.Fatalf("extracted date = %v", att.ExtractedDate)
	}
	if att.ExtractedAmountMinor == nil || *att.ExtractedAmountMinor != 1234 {
		t.Fatalf("extracted amount = %v", att.ExtractedAmountMinor)
	}
	path := filepath.Join(f.dir, att.FilePath)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Fatal("stored file content mismatch")
	}
	opened, file, err := f.service.Open(context.Background(), att.ID)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = file.Close() }()
	if opened.Filename != "factura-2026-03-05-total-12,34.pdf" {
		t.Fatalf("opened = %+v", opened)
	}
}

func TestSaveWithAmbiguousCandidatesStaysUnpaired(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	second := domain.Transaction{
		AccountID: "acc-1", ExternalID: "tx-2", AmountMinor: -1234, Currency: "EUR",
		BookingDate: f.tx.BookingDate, Counterparty: "Other", Status: domain.TransactionStatusBooked,
	}
	if _, _, err := f.store.UpsertTransaction(context.Background(), &second); err != nil {
		t.Fatalf("UpsertTransaction: %v", err)
	}
	att, err := f.service.SaveWeb(context.Background(), SaveInput{
		Reader: strings.NewReader("receipt"), Filename: "2026-03-05 total 12.34.pdf",
	})
	if err != nil {
		t.Fatalf("SaveWeb: %v", err)
	}
	if att.Paired() {
		t.Fatalf("ambiguous attachment should remain unpaired: %+v", att)
	}
	unpaired, err := f.service.ListUnpaired(context.Background())
	if err != nil {
		t.Fatalf("ListUnpaired: %v", err)
	}
	if len(unpaired) != 1 || unpaired[0].ID != att.ID {
		t.Fatalf("unpaired = %+v", unpaired)
	}
}

func TestSaveExplicitTransactionAndPathSanitization(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	att, err := f.service.SaveWeb(context.Background(), SaveInput{
		Reader: strings.NewReader("x"), Filename: "../../evil receipt.pdf", TransactionID: f.tx.ID,
	})
	if err != nil {
		t.Fatalf("SaveWeb: %v", err)
	}
	if att.Filename != "evil receipt.pdf" {
		t.Fatalf("sanitized filename = %q", att.Filename)
	}
	if strings.Contains(att.FilePath, "..") || strings.Contains(att.FilePath, "/") {
		t.Fatalf("stored path is unsafe: %q", att.FilePath)
	}
	if !att.Paired() || *att.TransactionID != f.tx.ID {
		t.Fatalf("explicit pairing failed: %+v", att)
	}
}

func TestSaveRejectsOversizedFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "attachments.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	dir := t.TempDir()
	service, err := New(store, dir, 4, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := service.SaveWeb(ctx, SaveInput{Reader: strings.NewReader("12345"), Filename: "large.pdf"}); err == nil {
		t.Fatal("expected oversize error")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("oversize upload left files behind: %+v", entries)
	}
}

func TestExtractMetadata(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		text     string
		currency string
		date     string
		amount   int64
		wantCurr string
	}{
		{name: "labeled", text: "Factura 2026-03-05 total 1.234,56", currency: "EUR", date: "2026-03-05", amount: 123456, wantCurr: "EUR"},
		{name: "european date", text: "receipt 05/03/2026 amount 99.99 USD", currency: "EUR", date: "2026-03-05", amount: 9999, wantCurr: "USD"},
		{name: "symbol", text: "invoice €42,10", currency: "EUR", date: "", amount: 4210, wantCurr: "EUR"},
		{name: "bare", text: "2026-03-05_receipt_12.34.pdf", currency: "GBP", date: "2026-03-05", amount: 1234, wantCurr: "GBP"},
		{name: "invalid date", text: "2026-02-31 total 10.00", currency: "EUR", date: "", amount: 1000, wantCurr: "EUR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			meta := ExtractMetadata(test.text, test.currency)
			if test.date == "" {
				if meta.Date != nil {
					t.Fatalf("date = %v, want nil", meta.Date)
				}
			} else if meta.Date == nil || meta.Date.Format("2006-01-02") != test.date {
				t.Fatalf("date = %v, want %s", meta.Date, test.date)
			}
			if meta.AmountMinor == nil || *meta.AmountMinor != test.amount {
				t.Fatalf("amount = %v, want %d", meta.AmountMinor, test.amount)
			}
			if meta.Currency != test.wantCurr {
				t.Fatalf("currency = %q, want %q", meta.Currency, test.wantCurr)
			}
		})
	}
}

func TestMissingReceiptRule(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	personal := domain.Account{
		ID: "acc-personal", ConnectionID: f.account.ConnectionID, Name: "Personal", Currency: "EUR",
		Type: domain.AccountTypePersonal,
	}
	if err := f.store.UpsertAccount(ctx, &personal); err != nil {
		t.Fatalf("UpsertAccount personal: %v", err)
	}
	others := []domain.Transaction{
		{
			AccountID: "acc-personal", ExternalID: "tx-personal", AmountMinor: -9000, Currency: "EUR",
			BookingDate: time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC), Counterparty: "Personal expense",
			Status: domain.TransactionStatusBooked,
		},
		{
			AccountID: "acc-1", ExternalID: "tx-small", AmountMinor: -500, Currency: "EUR",
			BookingDate: time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC), Counterparty: "Small expense",
			Status: domain.TransactionStatusBooked,
		},
	}
	for i := range others {
		if _, _, err := f.store.UpsertTransaction(ctx, &others[i]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}
	// The fixture transaction is old enough and above the threshold.
	rule := NewMissingReceiptRule(f.store, 1000, 5, func() time.Time { return f.now })
	alerts, err := rule.Evaluate(ctx)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(alerts) != 1 || alerts[0].Rule != "missing_receipt" {
		t.Fatalf("alerts = %+v", alerts)
	}
	// Attach a receipt and the warning disappears.
	if _, err := f.service.SaveWeb(ctx, SaveInput{
		Reader: strings.NewReader("receipt"), Filename: "attached.pdf", TransactionID: f.tx.ID,
	}); err != nil {
		t.Fatalf("SaveWeb: %v", err)
	}
	alerts, err = rule.Evaluate(ctx)
	if err != nil {
		t.Fatalf("Evaluate after attach: %v", err)
	}
	if len(alerts) != 0 {
		t.Fatalf("alerts after attach = %+v", alerts)
	}
}

func hashHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
