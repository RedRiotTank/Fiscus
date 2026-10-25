package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestTransactionQuickFilterQueryBuilder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		filter    TransactionFilter
		wantSQL   string
		wantArgs  int
		rejectSQL string
	}{
		{name: "inflows", filter: TransactionFilter{QuickFilter: QuickFilterInflows}, wantSQL: "amount_minor > 0"},
		{name: "outflows", filter: TransactionFilter{QuickFilter: QuickFilterOutflows}, wantSQL: "amount_minor < 0"},
		{name: "p2p", filter: TransactionFilter{QuickFilter: QuickFilterP2P}, wantSQL: "LOWER(TRIM(category)) IN", wantArgs: 3},
		{name: "needs", filter: TransactionFilter{QuickFilter: QuickFilterNeeds, Categories: []string{"Utilities & Telecom", "Groceries"}}, wantSQL: "LOWER(TRIM(category)) IN", wantArgs: 2},
		{name: "wants empty", filter: TransactionFilter{QuickFilter: QuickFilterWants}, wantSQL: "1 = 0"},
		{name: "all", filter: TransactionFilter{QuickFilter: QuickFilterAll}, rejectSQL: "amount_minor"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			where, args := transactionWhere(test.filter)
			clause := strings.Join(where, " AND ")
			if test.wantSQL != "" && !strings.Contains(clause, test.wantSQL) {
				t.Fatalf("clause = %q, want %q", clause, test.wantSQL)
			}
			if test.rejectSQL != "" && strings.Contains(clause, test.rejectSQL) {
				t.Fatalf("clause = %q should not contain %q", clause, test.rejectSQL)
			}
			if len(args) != test.wantArgs {
				t.Fatalf("args = %+v, want %d", args, test.wantArgs)
			}
		})
	}
}

func TestListTransactionsQuickFilters(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()
	connection := seedConnection(t, store)
	account := seedAccount(t, store, connection.ID)
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		{AccountID: account.ID, ExternalID: "salary", AmountMinor: 300000, Currency: "EUR", BookingDate: now, Counterparty: "Employer", Category: "Payroll", Status: domain.TransactionStatusBooked},
		{AccountID: account.ID, ExternalID: "coffee", AmountMinor: -1250, Currency: "EUR", BookingDate: now, Counterparty: "Coffee Shop", Category: "Dining", Status: domain.TransactionStatusBooked},
		{AccountID: account.ID, ExternalID: "bizum", AmountMinor: -1600, Currency: "EUR", BookingDate: now, Counterparty: "Alberto", Category: "Transfers / P2P", Status: domain.TransactionStatusBooked},
		{AccountID: account.ID, ExternalID: "fiber", AmountMinor: -5000, Currency: "EUR", BookingDate: now, Counterparty: "Vodafone", Category: "Utilities & Telecom", Status: domain.TransactionStatusBooked},
	}
	for i := range transactions {
		if _, _, err := store.UpsertTransaction(ctx, &transactions[i]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}

	tests := []struct {
		name   string
		filter TransactionFilter
		want   []string
	}{
		{name: "inflows", filter: TransactionFilter{QuickFilter: QuickFilterInflows}, want: []string{"salary"}},
		{name: "outflows", filter: TransactionFilter{QuickFilter: QuickFilterOutflows}, want: []string{"coffee", "bizum", "fiber"}},
		{name: "p2p", filter: TransactionFilter{QuickFilter: QuickFilterP2P}, want: []string{"bizum"}},
		{name: "needs", filter: TransactionFilter{QuickFilter: QuickFilterNeeds, Categories: []string{"Utilities & Telecom", "Groceries"}}, want: []string{"fiber"}},
		{name: "wants", filter: TransactionFilter{QuickFilter: QuickFilterWants, Categories: []string{"Dining"}}, want: []string{"coffee"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			listed, err := store.ListTransactions(ctx, test.filter)
			if err != nil {
				t.Fatalf("ListTransactions: %v", err)
			}
			got := make(map[string]bool, len(listed))
			for _, tx := range listed {
				got[tx.ExternalID] = true
			}
			if len(listed) != len(test.want) {
				t.Fatalf("transactions = %+v, want %v", got, test.want)
			}
			for _, id := range test.want {
				if !got[id] {
					t.Fatalf("missing %s in %+v", id, got)
				}
			}
			count, err := store.CountTransactionsMatching(ctx, test.filter)
			if err != nil || count != len(test.want) {
				t.Fatalf("count = %d err=%v, want %d", count, err, len(test.want))
			}
		})
	}
}
