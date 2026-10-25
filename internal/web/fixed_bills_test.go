package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/analysis"
	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestFilterFixedBillSubscriptions(t *testing.T) {
	t.Parallel()
	subscriptions := []analysis.Subscription{
		{Counterparty: "Iberdrola", Category: "Utilities & Telecom", Currency: "EUR", Frequency: analysis.Monthly, AmountMinor: 6500, Active: true},
		{Counterparty: "Mapfre", Category: "Insurance", Currency: "EUR", Frequency: analysis.Monthly, AmountMinor: 4500, Active: true},
		{Counterparty: "Alberto Avidad Fernández", Category: "Internet", Currency: "EUR", Frequency: analysis.Monthly, AmountMinor: 1600, Active: true},
		{Counterparty: "Streaming", Category: "Subscriptions", Currency: "EUR", Frequency: analysis.Monthly, AmountMinor: 999, Active: true},
	}
	rules := []domain.RecurringBillRule{{
		Workspace: "personal", Currency: "EUR", Counterparty: "Alberto Avidad Fernández",
		Category: "Utilities & Telecom", Pillar: "needs", GrossMinor: 1600, PersonalShareMinor: 533, Active: true,
	}}
	fixedNeeds := map[string]struct{}{"insurance": {}, "utilities & telecom": {}}
	filtered := filterFixedBillSubscriptions(subscriptions, rules, nil, fixedNeeds)
	if len(filtered) != 1 || filtered[0].Counterparty != "Streaming" {
		t.Fatalf("filtered subscriptions = %+v", filtered)
	}
}

func TestSubscriptionsPageShowsFixedBillsAndConsumerSubscriptions(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	ctx := context.Background()
	now := f.server.now().UTC()
	transactions := []domain.Transaction{
		{AccountID: "acc-1", ExternalID: "vodafone-1", AmountMinor: -4990, Currency: "EUR", BookingDate: time.Date(now.Year(), now.Month()-2, 5, 10, 0, 0, 0, time.UTC), Counterparty: "Vodafone", Description: "Fibra + Movil", Category: "Utilities & Telecom", Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "vodafone-2", AmountMinor: -4990, Currency: "EUR", BookingDate: time.Date(now.Year(), now.Month()-1, 5, 10, 0, 0, 0, time.UTC), Counterparty: "Vodafone", Description: "Fibra + Movil", Category: "Utilities & Telecom", Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "vodafone-3", AmountMinor: -4990, Currency: "EUR", BookingDate: time.Date(now.Year(), now.Month(), 5, 10, 0, 0, 0, time.UTC), Counterparty: "Vodafone", Description: "Fibra + Movil", Category: "Utilities & Telecom", Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "netflix-1", AmountMinor: -1799, Currency: "EUR", BookingDate: time.Date(now.Year(), now.Month()-2, 3, 10, 0, 0, 0, time.UTC), Counterparty: "Netflix", Description: "Netflix", Category: "Subscriptions", Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "netflix-2", AmountMinor: -1799, Currency: "EUR", BookingDate: time.Date(now.Year(), now.Month()-1, 3, 10, 0, 0, 0, time.UTC), Counterparty: "Netflix", Description: "Netflix", Category: "Subscriptions", Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "netflix-3", AmountMinor: -1799, Currency: "EUR", BookingDate: time.Date(now.Year(), now.Month(), 3, 10, 0, 0, 0, time.UTC), Counterparty: "Netflix", Description: "Netflix", Category: "Subscriptions", Status: domain.TransactionStatusBooked},
	}
	for i := range transactions {
		if _, _, err := f.store.UpsertTransaction(ctx, &transactions[i]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}

	page := f.request(t, http.MethodGet, "/subscriptions?workspace=personal", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("subscriptions status = %d body=%s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	for _, want := range []string{
		"Fixed Bills &amp; Utilities", "Consumer subscriptions", "Vodafone", "Tag as household bill",
		"Netflix", "Netflix", "Active",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("subscriptions page missing %q", want)
		}
	}
	if !strings.Contains(body, "Telecom") {
		t.Error("subscriptions page does not show the fixed utility category")
	}
}
