package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestTransactionsFacetedFiltersAndHTMXSwap(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	seedPersonalBudgetPlan(t, f)
	ctx := context.Background()
	bookedAt := time.Date(2026, 3, 9, 9, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		{
			AccountID: "acc-1", ExternalID: "bizum-out-filter", AmountMinor: -1600, Currency: "EUR",
			BookingDate: bookedAt, Counterparty: "Bizum Alberto", Description: "Bizum enviado",
			Category: "Transfers / P2P", Status: domain.TransactionStatusBooked,
		},
		{
			AccountID: "acc-1", ExternalID: "bizum-in-filter", AmountMinor: 1600, Currency: "EUR",
			BookingDate: bookedAt, Counterparty: "Bizum Ana", Description: "Bizum recibido",
			Category: "Transfers / P2P", Status: domain.TransactionStatusBooked,
		},
		{
			AccountID: "acc-1", ExternalID: "rent-filter", AmountMinor: -85000, Currency: "EUR",
			BookingDate: bookedAt, Counterparty: "Inmobiliaria Centro", Description: "Alquiler",
			Category: "Housing / Rent", Status: domain.TransactionStatusBooked,
		},
		{
			AccountID: "acc-1", ExternalID: "fiber-filter", AmountMinor: -5000, Currency: "EUR",
			BookingDate: bookedAt, Counterparty: "Vodafone", Description: "Fibra",
			Category: "Utilities & Telecom", Status: domain.TransactionStatusBooked,
		},
		{
			AccountID: "acc-1", ExternalID: "cash-filter", AmountMinor: -6000, Currency: "EUR",
			BookingDate: bookedAt, Counterparty: "Cash at La Caixa 6277", Description: "Cash at La Caixa 6277",
			Category: "Cash / ATM", Status: domain.TransactionStatusBooked,
		},
		{
			AccountID: "acc-1", ExternalID: "feb-rent-filter", AmountMinor: -80000, Currency: "EUR",
			BookingDate: time.Date(2026, 2, 9, 9, 0, 0, 0, time.UTC), Counterparty: "Feb Inmobiliaria", Description: "Alquiler febrero",
			Category: "Housing / Rent", Status: domain.TransactionStatusBooked,
		},
	}
	for i := range transactions {
		if _, _, err := f.store.UpsertTransaction(ctx, &transactions[i]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}

	page := f.request(t, http.MethodGet, "/transactions", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("transactions status = %d", page.Code)
	}
	body := page.Body.String()
	for _, want := range []string{
		`transaction-facet-bar my-6 space-y-4`, `transaction-facet-divider h-6 w-px bg-zinc-700/50`, `Flow`, `Category`, `All Flow`, `Money In`, `Money Out`,
		`All Categories`, `Transfers / P2P`, `Housing / Rent`, `Utilities &amp; Telecom`, `Cash / ATM`,
		`flow=inflows`, `facet=needs`, `data-search-toggle`, `transition-all duration-200`, `<input type="month" name="month"`, `All Time`,
		`id="transactions-history"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("transactions page missing %q", want)
		}
	}

	tests := []struct {
		name   string
		query  string
		want   []string
		absent []string
	}{
		{name: "inflows", query: "flow=inflows", want: []string{"Employer", "Bizum Ana"}, absent: []string{"Coffee Shop", "Bizum Alberto"}},
		{name: "outflows", query: "flow=outflows", want: []string{"Coffee Shop", "Bizum Alberto", "Inmobiliaria Centro"}, absent: []string{"Employer", "Bizum Ana"}},
		{name: "p2p", query: "facet=p2p", want: []string{"Bizum Alberto", "Bizum Ana"}, absent: []string{"Coffee Shop", "Employer"}},
		{name: "wants", query: "facet=wants", want: []string{"Coffee Shop", "Bizum Alberto", "Bizum Ana"}, absent: []string{"Employer", "Inmobiliaria Centro"}},
		{name: "outflows housing", query: "flow=outflows&facet=housing", want: []string{"Inmobiliaria Centro"}, absent: []string{"Coffee Shop", "Bizum Alberto", "Employer"}},
		{name: "inflows p2p", query: "flow=inflows&facet=p2p", want: []string{"Bizum Ana"}, absent: []string{"Bizum Alberto", "Coffee Shop", "Employer"}},
		{name: "outflows utilities", query: "flow=outflows&facet=utilities", want: []string{"Vodafone"}, absent: []string{"Coffee Shop", "Inmobiliaria Centro", "Employer"}},
		{name: "cash", query: "facet=cash", want: []string{"Cash at La Caixa 6277"}, absent: []string{"Coffee Shop", "Inmobiliaria Centro", "Employer"}},
		{name: "march outflows housing", query: "month=2026-03&flow=outflows&facet=housing", want: []string{"Inmobiliaria Centro"}, absent: []string{"Feb Inmobiliaria", "Coffee Shop", "Employer"}},
		{name: "february outflows housing", query: "month=2026-02&flow=outflows&facet=housing", want: []string{"Feb Inmobiliaria"}, absent: []string{"Inmobiliaria Centro", "Coffee Shop", "Employer"}},
		{name: "exact category drilldown", query: "month=2026-03&flow=outflows&category=Housing+%2F+Rent", want: []string{"Inmobiliaria Centro", "filter-chip active"}, absent: []string{"Feb Inmobiliaria", "Cash at La Caixa 6277", "Employer"}},
	}
	for _, test := range tests {
		response := f.request(t, http.MethodGet, "/transactions?workspace=personal&"+test.query, true, map[string]string{"HX-Request": "true"})
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", test.name, response.Code, response.Body.String())
		}
		partial := response.Body.String()
		if strings.Contains(partial, "<html") || !strings.Contains(partial, `id="transactions-history"`) {
			t.Fatalf("%s did not return the history partial: %s", test.name, partial)
		}
		for _, want := range test.want {
			if !strings.Contains(partial, want) {
				t.Errorf("%s filter missing %q", test.name, want)
			}
		}
		for _, absent := range test.absent {
			if strings.Contains(partial, absent) {
				t.Errorf("%s filter unexpectedly contains %q", test.name, absent)
			}
		}
		if !strings.Contains(partial, `filter-chip active`) {
			t.Errorf("%s filter did not mark an active chip", test.name)
		}
	}
}
