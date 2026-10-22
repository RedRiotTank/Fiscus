package storage

import (
	"context"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/categories"
	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestRecategorizeDryRunAndApply(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	connection := seedConnection(t, s)
	seedAccount(t, s, connection.ID)

	day := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		{AccountID: "acc-1", ExternalID: "power", AmountMinor: -4200, Currency: "EUR", BookingDate: day, Counterparty: "Iberdrola", Category: categories.DefaultCategory, Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "water", AmountMinor: -1800, Currency: "EUR", BookingDate: day.Add(time.Hour), Counterparty: "Aguas", Category: categories.DefaultCategory, Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "already", AmountMinor: -900, Currency: "EUR", BookingDate: day.Add(2 * time.Hour), Counterparty: "Iberdrola", Category: "Utilities", Status: domain.TransactionStatusBooked},
	}
	for i := range transactions {
		if _, _, err := s.UpsertTransaction(ctx, &transactions[i]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}
	classifier := categories.MustNew([]categories.Rule{
		{Category: "Utilities", Pattern: "(?i)IBERDROLA|AGUAS"},
	})

	preview, err := s.Recategorize(ctx, classifier.Classify, false)
	if err != nil {
		t.Fatalf("Recategorize preview: %v", err)
	}
	if preview.Examined != 3 || preview.Changed != 2 || preview.Applied {
		t.Fatalf("preview = %+v", preview)
	}
	if len(preview.Shifts) != 1 || preview.Shifts[0] != (CategoryShift{From: "Uncategorized", To: "Utilities", Count: 2}) {
		t.Fatalf("shifts = %+v", preview.Shifts)
	}
	stored, err := s.ListTransactions(ctx, TransactionFilter{})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	for _, tx := range stored {
		if tx.Category != categories.DefaultCategory && tx.ExternalID != "already" {
			t.Fatalf("dry run wrote category %q to %s", tx.Category, tx.ExternalID)
		}
	}

	applied, err := s.Recategorize(ctx, classifier.Classify, true)
	if err != nil {
		t.Fatalf("Recategorize apply: %v", err)
	}
	if applied.Changed != 2 || !applied.Applied {
		t.Fatalf("applied = %+v", applied)
	}
	stored, err = s.ListTransactions(ctx, TransactionFilter{})
	if err != nil {
		t.Fatalf("ListTransactions after apply: %v", err)
	}
	for _, tx := range stored {
		if tx.Category != "Utilities" {
			t.Fatalf("transaction %s category = %q, want Utilities", tx.ExternalID, tx.Category)
		}
	}

	second, err := s.Recategorize(ctx, classifier.Classify, true)
	if err != nil {
		t.Fatalf("second Recategorize: %v", err)
	}
	if second.Changed != 0 {
		t.Fatalf("second changed = %d, want 0", second.Changed)
	}
}
