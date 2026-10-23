package search

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

type searchFixture struct {
	store   *storage.Store
	service *Service
	txs     map[string]domain.Transaction
}

func newSearchFixture(t *testing.T) *searchFixture {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	connection := domain.Connection{RequisitionID: "req-search", InstitutionName: "Bank", Status: "LN"}
	if err := store.UpsertConnection(ctx, &connection); err != nil {
		t.Fatalf("UpsertConnection: %v", err)
	}
	accounts := []domain.Account{
		{ID: "biz", ConnectionID: connection.ID, Name: "Business", Currency: "EUR", Type: domain.AccountTypeBusiness},
		{ID: "personal", ConnectionID: connection.ID, Name: "Personal", Currency: "EUR", Type: domain.AccountTypePersonal},
	}
	for i := range accounts {
		if err := store.UpsertAccount(ctx, &accounts[i]); err != nil {
			t.Fatalf("UpsertAccount: %v", err)
		}
	}
	day := time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		{AccountID: "biz", ExternalID: "coffee-biz", AmountMinor: -1250, Currency: "EUR", BookingDate: day, Counterparty: "Coffee Shop Roasters", Description: "Flat white", Category: "Dining", Notes: "meeting with client", Status: domain.TransactionStatusBooked},
		{AccountID: "biz", ExternalID: "consulting", AmountMinor: 150000, Currency: "EUR", BookingDate: day.Add(-time.Hour), Counterparty: "Acme Invoice", Description: "Consulting work", Category: "Sales", Notes: "paid in full", Status: domain.TransactionStatusBooked},
		{AccountID: "personal", ExternalID: "coffee-personal", AmountMinor: -500, Currency: "EUR", BookingDate: day.Add(-2 * time.Hour), Counterparty: "Coffee Corner", Description: "Espresso", Category: "Dining", Status: domain.TransactionStatusBooked},
		{AccountID: "biz", ExternalID: "pending-coffee", AmountMinor: -2000, Currency: "EUR", BookingDate: day.Add(-3 * time.Hour), Counterparty: "Coffee Pending", Description: "Pending", Category: "Dining", Status: domain.TransactionStatusPending},
	}
	txs := make(map[string]domain.Transaction, len(transactions))
	for i := range transactions {
		tx := transactions[i]
		if _, _, err := store.UpsertTransaction(ctx, &tx); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
		txs[tx.ExternalID] = tx
	}
	txID := txs["coffee-biz"].ID
	if err := store.CreateAttachment(ctx, &domain.Attachment{
		TransactionID: &txID, Filename: "receipt.pdf", FilePath: "receipt.pdf",
		ContentType: "application/pdf", SizeBytes: 10, UploadedAt: day, Source: "web",
	}); err != nil {
		t.Fatalf("CreateAttachment: %v", err)
	}
	return &searchFixture{store: store, service: New(store), txs: txs}
}

func externalIDs(transactions []domain.Transaction) map[string]bool {
	out := make(map[string]bool, len(transactions))
	for _, tx := range transactions {
		out[tx.ExternalID] = true
	}
	return out
}

func TestParseStructuredQuery(t *testing.T) {
	t.Parallel()
	query, err := Parse("coffee amount:>10 category:Dining status:booked has:receipt type:business")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if query.Match != `"coffee"*` || query.AmountOp != ">" || query.AmountMinor != 1000 ||
		query.Category != "Dining" || query.Status != "booked" || query.AccountType != "business" ||
		query.HasReceipt == nil || !*query.HasReceipt {
		t.Fatalf("query = %+v", query)
	}
}

func TestParseRejectsInvalidFilters(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"amount:>", "amount:abc", "status:done", "type:other", "has:maybe", "category:"} {
		if _, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q) expected error", raw)
		}
	}
}

func TestFTSSearchAndFilters(t *testing.T) {
	t.Parallel()
	f := newSearchFixture(t)
	ctx := context.Background()
	tests := []struct {
		query string
		want  []string
	}{
		{query: "coffee", want: []string{"coffee-biz", "coffee-personal", "pending-coffee"}},
		{query: "coff", want: []string{"coffee-biz", "coffee-personal", "pending-coffee"}},
		{query: "client", want: []string{"coffee-biz"}},
		{query: "coffee type:personal", want: []string{"coffee-personal"}},
		{query: "coffee type:business status:booked", want: []string{"coffee-biz"}},
		{query: "coffee status:pending", want: []string{"pending-coffee"}},
		{query: "coffee has:receipt", want: []string{"coffee-biz"}},
		{query: "coffee has:no-receipt", want: []string{"coffee-personal", "pending-coffee"}},
		{query: "amount:>10 category:Dining has:receipt", want: []string{"coffee-biz"}},
		{query: "acme", want: []string{"consulting"}},
	}
	for _, test := range tests {
		results, err := f.service.Search(ctx, test.query, 50)
		if err != nil {
			t.Errorf("Search(%q): %v", test.query, err)
			continue
		}
		got := externalIDs(results)
		if len(got) != len(test.want) {
			t.Errorf("Search(%q) = %v, want %v", test.query, got, test.want)
			continue
		}
		for _, id := range test.want {
			if !got[id] {
				t.Errorf("Search(%q) missing %s: %v", test.query, id, got)
			}
		}
	}
}

func TestFTSIndexUpdatesOnTransactionAndAccountChanges(t *testing.T) {
	t.Parallel()
	f := newSearchFixture(t)
	ctx := context.Background()
	tx := f.txs["coffee-biz"]
	tx.Notes = "urgent tax document"
	tx.Category = "Taxes"
	if _, _, err := f.store.UpsertTransaction(ctx, &tx); err != nil {
		t.Fatalf("UpsertTransaction update: %v", err)
	}
	results, err := f.service.Search(ctx, "urgent", 10)
	if err != nil {
		t.Fatalf("Search urgent: %v", err)
	}
	if len(results) != 1 || results[0].ID != tx.ID {
		t.Fatalf("urgent results = %+v", results)
	}
	if err := f.store.SetAccountType(ctx, "biz", domain.AccountTypePersonal); err != nil {
		t.Fatalf("SetAccountType: %v", err)
	}
	results, err = f.service.Search(ctx, "roasters type:business", 10)
	if err != nil {
		t.Fatalf("Search business: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("business results after account change = %+v", results)
	}
	results, err = f.service.Search(ctx, "roasters type:personal", 10)
	if err != nil {
		t.Fatalf("Search personal: %v", err)
	}
	if len(results) != 1 || results[0].ExternalID != "coffee-biz" {
		t.Fatalf("personal results = %+v", results)
	}
}

func TestSearchLimit(t *testing.T) {
	t.Parallel()
	f := newSearchFixture(t)
	results, err := f.service.Search(context.Background(), "coffee", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
}
