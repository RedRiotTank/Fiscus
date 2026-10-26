package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestTransactionFacetQueryBuilderCombinesFlowAndCategory(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		filter    TransactionFilter
		wantSQL   []string
		wantArgs  int
		rejectSQL string
	}{
		{name: "inflows", filter: TransactionFilter{Flow: FlowInflows}, wantSQL: []string{"amount_minor > 0"}},
		{name: "outflows", filter: TransactionFilter{Flow: FlowOutflows}, wantSQL: []string{"amount_minor < 0"}},
		{name: "p2p", filter: TransactionFilter{Category: CategoryFacetP2P}, wantSQL: []string{"LOWER(TRIM(category)) IN"}, wantArgs: 8},
		{name: "needs", filter: TransactionFilter{Category: CategoryFacetNeeds, Categories: []string{"Utilities & Telecom", "Groceries"}}, wantSQL: []string{"LOWER(TRIM(category)) IN"}, wantArgs: 2},
		{name: "wants empty", filter: TransactionFilter{Category: CategoryFacetWants}, wantSQL: []string{"1 = 0"}},
		{name: "wants p2p aliases", filter: TransactionFilter{Category: CategoryFacetWants, Categories: []string{"Transfers / P2P"}}, wantSQL: []string{"LOWER(TRIM(category)) IN"}, wantArgs: 8},
		{name: "exact p2p aliases", filter: TransactionFilter{CategoryName: "Transfers / P2P"}, wantSQL: []string{"LOWER(TRIM(category)) IN"}, wantArgs: 8},
		{name: "outflows housing", filter: TransactionFilter{Flow: FlowOutflows, Category: CategoryFacetHousing}, wantSQL: []string{"amount_minor < 0", "LOWER(TRIM(category)) IN"}, wantArgs: 7},
		{name: "inflows p2p", filter: TransactionFilter{Flow: FlowInflows, Category: CategoryFacetP2P}, wantSQL: []string{"amount_minor > 0", "LOWER(TRIM(category)) IN"}, wantArgs: 8},
		{name: "all", filter: TransactionFilter{Flow: FlowAll, Category: CategoryFacetAll}, rejectSQL: "amount_minor"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			where, args := transactionWhere(test.filter)
			clause := strings.Join(where, " AND ")
			for _, want := range test.wantSQL {
				if !strings.Contains(clause, want) {
					t.Fatalf("clause = %q, want %q", clause, want)
				}
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

func TestListTransactionsFacetedFilters(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()
	connection := seedConnection(t, store)
	account := seedAccount(t, store, connection.ID)
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		{AccountID: account.ID, ExternalID: "salary", AmountMinor: 300000, Currency: "EUR", BookingDate: now, Counterparty: "Employer", Category: "Payroll", Status: domain.TransactionStatusBooked},
		{AccountID: account.ID, ExternalID: "coffee", AmountMinor: -1250, Currency: "EUR", BookingDate: now, Counterparty: "Coffee Shop", Category: "Dining", Status: domain.TransactionStatusBooked},
		{AccountID: account.ID, ExternalID: "bizum-out", AmountMinor: -1600, Currency: "EUR", BookingDate: now, Counterparty: "Alberto", Category: "Transfers / P2P", Status: domain.TransactionStatusBooked},
		{AccountID: account.ID, ExternalID: "bizum-in", AmountMinor: 1600, Currency: "EUR", BookingDate: now, Counterparty: "Ana", Category: "Transfers / P2P", Status: domain.TransactionStatusBooked},
		{AccountID: account.ID, ExternalID: "rent", AmountMinor: -85000, Currency: "EUR", BookingDate: now, Counterparty: "Inmobiliaria", Category: "Housing / Rent", Status: domain.TransactionStatusBooked},
		{AccountID: account.ID, ExternalID: "fiber", AmountMinor: -5000, Currency: "EUR", BookingDate: now, Counterparty: "Vodafone", Category: "Utilities & Telecom", Status: domain.TransactionStatusBooked},
		{AccountID: account.ID, ExternalID: "groceries", AmountMinor: -4200, Currency: "EUR", BookingDate: now, Counterparty: "Mercadona", Category: "Groceries", Status: domain.TransactionStatusBooked},
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
		{name: "inflows", filter: TransactionFilter{Flow: FlowInflows}, want: []string{"salary", "bizum-in"}},
		{name: "outflows", filter: TransactionFilter{Flow: FlowOutflows}, want: []string{"coffee", "bizum-out", "rent", "fiber", "groceries"}},
		{name: "outflows housing", filter: TransactionFilter{Flow: FlowOutflows, Category: CategoryFacetHousing}, want: []string{"rent"}},
		{name: "inflows p2p", filter: TransactionFilter{Flow: FlowInflows, Category: CategoryFacetP2P}, want: []string{"bizum-in"}},
		{name: "utilities", filter: TransactionFilter{Category: CategoryFacetUtilities}, want: []string{"fiber"}},
		{name: "groceries out", filter: TransactionFilter{Flow: FlowOutflows, Category: CategoryFacetGroceries}, want: []string{"groceries"}},
		{name: "needs", filter: TransactionFilter{Category: CategoryFacetNeeds, Categories: []string{"Utilities & Telecom", "Groceries"}}, want: []string{"fiber", "groceries"}},
		{name: "wants", filter: TransactionFilter{Category: CategoryFacetWants, Categories: []string{"Dining"}}, want: []string{"coffee"}},
		{name: "wants p2p", filter: TransactionFilter{Category: CategoryFacetWants, Categories: []string{"Transfers / P2P"}}, want: []string{"bizum-out", "bizum-in"}},
		{name: "exact p2p", filter: TransactionFilter{CategoryName: "Transfers / P2P"}, want: []string{"bizum-out", "bizum-in"}},
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
