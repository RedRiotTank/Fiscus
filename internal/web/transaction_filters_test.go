package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestTransactionsQuickFiltersAndHTMXSwap(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	seedPersonalBudgetPlan(t, f)
	ctx := context.Background()
	bizum := domain.Transaction{
		AccountID: "acc-1", ExternalID: "bizum-filter", AmountMinor: -1600, Currency: "EUR",
		BookingDate:  time.Date(2026, 3, 9, 9, 0, 0, 0, time.UTC),
		Counterparty: "Bizum Alberto", Description: "Bizum enviado",
		Category: "Transfers / P2P", Status: domain.TransactionStatusBooked,
	}
	if _, _, err := f.store.UpsertTransaction(ctx, &bizum); err != nil {
		t.Fatalf("UpsertTransaction: %v", err)
	}

	page := f.request(t, http.MethodGet, "/transactions", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("transactions status = %d", page.Code)
	}
	body := page.Body.String()
	for _, want := range []string{
		`transaction-quick-filters`, `>All<`, `Money In`, `Money Out`, `Transfers / P2P`,
		`filter=inflows`, `filter=needs`, `data-search-toggle`, `transition-all duration-200`,
		`id="transactions-history"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("transactions page missing %q", want)
		}
	}

	tests := []struct {
		filter string
		want   []string
		absent []string
	}{
		{filter: "inflows", want: []string{"Employer"}, absent: []string{"Coffee Shop", "Alberto"}},
		{filter: "outflows", want: []string{"Coffee Shop", "Alberto"}, absent: []string{"Employer"}},
		{filter: "p2p", want: []string{"Alberto"}, absent: []string{"Coffee Shop", "Employer"}},
		{filter: "wants", want: []string{"Coffee Shop"}, absent: []string{"Employer", "Alberto"}},
	}
	for _, test := range tests {
		response := f.request(t, http.MethodGet, "/transactions?workspace=personal&filter="+test.filter, true, map[string]string{"HX-Request": "true"})
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", test.filter, response.Code, response.Body.String())
		}
		partial := response.Body.String()
		if strings.Contains(partial, "<html") || !strings.Contains(partial, `id="transactions-history"`) {
			t.Fatalf("%s did not return the history partial: %s", test.filter, partial)
		}
		for _, want := range test.want {
			if !strings.Contains(partial, want) {
				t.Errorf("%s filter missing %q", test.filter, want)
			}
		}
		for _, absent := range test.absent {
			if strings.Contains(partial, absent) {
				t.Errorf("%s filter unexpectedly contains %q", test.filter, absent)
			}
		}
		if !strings.Contains(partial, `filter-chip active`) {
			t.Errorf("%s filter did not mark an active chip", test.filter)
		}
	}
}
