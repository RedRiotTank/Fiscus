package analysis

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func booked(account, merchant, currency string, amount int64, date string) domain.Transaction {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(err)
	}
	return domain.Transaction{
		AccountID: account, ExternalID: merchant + date, AmountMinor: amount, Currency: currency,
		BookingDate: parsed, Counterparty: merchant, Description: merchant,
		Status: domain.TransactionStatusBooked,
	}
}

func TestAnalyzeSubscriptionsAndCosts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		booked("acc-1", "Streaming", "EUR", -999, "2026-01-05"),
		booked("acc-1", "Streaming", "EUR", -999, "2026-02-04"),
		booked("acc-1", "Streaming", "EUR", -999, "2026-03-05"),
		booked("acc-1", "Annual Cloud", "EUR", -12000, "2025-03-01"),
		booked("acc-1", "Annual Cloud", "EUR", -12000, "2026-03-01"),
		booked("acc-1", "Irregular Shop", "EUR", -1000, "2025-12-01"),
		booked("acc-1", "Irregular Shop", "EUR", -1000, "2026-01-15"),
		booked("acc-1", "Old Gym", "EUR", -3000, "2025-08-01"),
		booked("acc-1", "Old Gym", "EUR", -3000, "2025-09-01"),
	}
	snapshot := Analyze(transactions, now)
	if len(snapshot.Subscriptions) != 3 {
		t.Fatalf("subscriptions = %+v, want 3", snapshot.Subscriptions)
	}
	var streaming, annual Subscription
	for _, subscription := range snapshot.Subscriptions {
		switch subscription.Counterparty {
		case "Streaming":
			streaming = subscription
		case "Annual Cloud":
			annual = subscription
		}
	}
	if streaming.Frequency != Monthly || streaming.Occurrences != 3 || !streaming.Active {
		t.Fatalf("streaming = %+v", streaming)
	}
	if streaming.MonthlyMinor() != 999 || streaming.AnnualMinor() != 11988 {
		t.Fatalf("streaming costs = %d/%d", streaming.MonthlyMinor(), streaming.AnnualMinor())
	}
	if annual.Frequency != Yearly || !annual.Active || annual.MonthlyMinor() != 1000 || annual.AnnualMinor() != 12000 {
		t.Fatalf("annual = %+v", annual)
	}
	if len(snapshot.MonthlyCosts) != 1 || snapshot.MonthlyCosts[0].Minor != 1999 {
		t.Fatalf("monthly costs = %+v", snapshot.MonthlyCosts)
	}
	if len(snapshot.AnnualCosts) != 1 || snapshot.AnnualCosts[0].Minor != 23988 {
		t.Fatalf("annual costs = %+v", snapshot.AnnualCosts)
	}
}

func TestAnalyzeDetectsHistoricalPriceIncrease(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		booked("acc-1", "Streaming", "EUR", -999, "2026-01-05"),
		booked("acc-1", "Streaming", "EUR", -1099, "2026-02-05"),
		booked("acc-1", "Streaming", "EUR", -1099, "2026-03-05"),
	}
	for i := range transactions {
		transactions[i].Category = "Subscriptions"
	}
	snapshot := Analyze(transactions, now)
	if len(snapshot.Subscriptions) != 1 {
		t.Fatalf("subscriptions = %+v", snapshot.Subscriptions)
	}
	subscription := snapshot.Subscriptions[0]
	if subscription.PriceIncreaseMinor != 100 || subscription.PreviousAmountMinor != 999 {
		t.Fatalf("price increase = %+v", subscription)
	}
	if subscription.PriceChangedAt.Format("2006-01-02") != "2026-02-05" {
		t.Fatalf("price changed at = %v", subscription.PriceChangedAt)
	}
}

func TestAnalyzeIgnoresNonSubscriptionCategories(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	makeTx := func(merchant, category, date string) domain.Transaction {
		tx := booked("acc-1", merchant, "EUR", -2500, date)
		tx.Category = category
		return tx
	}
	transactions := []domain.Transaction{
		makeTx("Taverna", "Restaurants", "2026-01-05"),
		makeTx("Taverna", "Restaurants", "2026-02-05"),
		makeTx("Mercadona", "Groceries", "2026-01-08"),
		makeTx("Mercadona", "Groceries", "2026-02-08"),
		makeTx("Landlord", "Rent", "2026-01-01"),
		makeTx("Landlord", "Rent", "2026-02-01"),
		makeTx("Streaming", "Subscriptions", "2026-01-10"),
		makeTx("Streaming", "Subscriptions", "2026-02-10"),
	}
	snapshot := Analyze(transactions, now)
	if len(snapshot.Subscriptions) != 1 || snapshot.Subscriptions[0].Counterparty != "Streaming" {
		t.Fatalf("subscriptions = %+v", snapshot.Subscriptions)
	}
	if snapshot.Subscriptions[0].Category != "Subscriptions" {
		t.Fatalf("category = %q", snapshot.Subscriptions[0].Category)
	}
}

func TestAnalyzeIgnoresIrregularAndInactiveForCosts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		booked("acc-1", "Irregular", "EUR", -1000, "2026-01-01"),
		booked("acc-1", "Irregular", "EUR", -1000, "2026-02-10"),
		booked("acc-1", "Old", "EUR", -500, "2025-01-01"),
		booked("acc-1", "Old", "EUR", -500, "2025-02-01"),
	}
	snapshot := Analyze(transactions, now)
	if len(snapshot.Subscriptions) != 1 || snapshot.Subscriptions[0].Counterparty != "Old" {
		t.Fatalf("subscriptions = %+v", snapshot.Subscriptions)
	}
	if snapshot.Subscriptions[0].Active {
		t.Fatal("old subscription should be inactive")
	}
	if len(snapshot.MonthlyCosts) != 0 || len(snapshot.AnnualCosts) != 0 {
		t.Fatalf("inactive costs = %+v/%+v", snapshot.MonthlyCosts, snapshot.AnnualCosts)
	}
}

func TestDetectFees(t *testing.T) {
	t.Parallel()
	transactions := []domain.Transaction{
		booked("acc-1", "Comisi\u00f3n mantenimiento", "EUR", -1500, "2026-03-01"),
		booked("acc-1", "Gastos descubierto", "EUR", -2000, "2026-03-02"),
		booked("acc-1", "Reclamaci\u00f3n", "EUR", -500, "2026-03-03"),
		booked("acc-1", "Coffee", "EUR", -300, "2026-03-04"),
		booked("acc-1", "Refund", "EUR", 1000, "2026-03-05"),
	}
	snapshot := Analyze(transactions, time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC))
	if len(snapshot.Fees) != 3 {
		t.Fatalf("fees = %+v, want 3", snapshot.Fees)
	}
	if len(snapshot.FeeTotals) != 1 || snapshot.FeeTotals[0].Minor != 4000 {
		t.Fatalf("fee totals = %+v", snapshot.FeeTotals)
	}
	for _, fee := range snapshot.Fees {
		if fee.Label == "" {
			t.Fatalf("fee has empty label: %+v", fee)
		}
	}
}

type staticProvider struct {
	snapshot Snapshot
	err      error
}

func (s staticProvider) Snapshot(context.Context) (Snapshot, error) { return s.snapshot, s.err }

func TestSubscriptionRulePriceIncrease(t *testing.T) {
	t.Parallel()
	last := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	provider := staticProvider{snapshot: Snapshot{Subscriptions: []Subscription{
		{
			AccountID: "acc-1", Counterparty: "Streaming", Currency: "EUR", Active: true,
			AmountMinor: 1299, PreviousAmountMinor: 999, PriceIncreaseMinor: 300, LastCharge: last,
		},
		{
			AccountID: "acc-1", Counterparty: "Stable", Currency: "EUR", Active: true,
			AmountMinor: 999, PreviousAmountMinor: 999, PriceIncreaseMinor: 0, LastCharge: last,
		},
	}}}
	rule, err := NewSubscriptionRule(provider)
	if err != nil {
		t.Fatalf("NewSubscriptionRule: %v", err)
	}
	alerts, err := rule.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("alerts = %+v, want 1", alerts)
	}
	alert := alerts[0]
	if alert.Rule != RuleSubscriptionPriceIncrease || alert.AccountID != "acc-1" {
		t.Fatalf("alert = %+v", alert)
	}
	if !strings.Contains(alert.Message, "9.99 EUR") || !strings.Contains(alert.Message, "12.99 EUR") || !strings.Contains(alert.Message, "+30%") {
		t.Fatalf("message = %q", alert.Message)
	}
	if alert.DedupKey == "" {
		t.Fatal("missing dedup key")
	}
}

func TestSubscriptionRuleSkipsInactiveAndDecreases(t *testing.T) {
	t.Parallel()
	provider := staticProvider{snapshot: Snapshot{Subscriptions: []Subscription{
		{AccountID: "acc-1", Counterparty: "Inactive", Active: false, AmountMinor: 1200, PreviousAmountMinor: 1000, PriceIncreaseMinor: 200},
		{AccountID: "acc-1", Counterparty: "Decrease", Active: true, AmountMinor: 800, PreviousAmountMinor: 1000, PriceIncreaseMinor: -200},
	}}}
	rule, _ := NewSubscriptionRule(provider)
	alerts, err := rule.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(alerts) != 0 {
		t.Fatalf("alerts = %+v, want none", alerts)
	}
}

func TestSubscriptionStaleThresholds(t *testing.T) {
	t.Parallel()
	transactions := []domain.Transaction{
		booked("acc-1", "Monthly Service", "EUR", -1000, "2026-01-01"),
		booked("acc-1", "Monthly Service", "EUR", -1000, "2026-02-01"),
		booked("acc-1", "Monthly Service", "EUR", -1000, "2026-03-01"),
		booked("acc-1", "Yearly Service", "EUR", -12000, "2025-03-01"),
		booked("acc-1", "Yearly Service", "EUR", -12000, "2026-03-01"),
	}
	find := func(snapshot Snapshot, name string) Subscription {
		t.Helper()
		for _, subscription := range snapshot.Subscriptions {
			if subscription.Counterparty == name {
				return subscription
			}
		}
		t.Fatalf("subscription %q not found", name)
		return Subscription{}
	}

	monthlyAtFortyFive := Analyze(transactions, time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	if !find(monthlyAtFortyFive, "Monthly Service").Active {
		t.Fatal("monthly subscription aged 45 days should remain active")
	}
	monthlyAtFortySix := Analyze(transactions, time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC))
	if find(monthlyAtFortySix, "Monthly Service").Active {
		t.Fatal("monthly subscription aged 46 days should be inactive")
	}

	yearlyAtThreeNinety := Analyze(transactions, time.Date(2027, 3, 26, 12, 0, 0, 0, time.UTC))
	if !find(yearlyAtThreeNinety, "Yearly Service").Active {
		t.Fatal("yearly subscription aged 390 days should remain active")
	}
	yearlyAtThreeNinetyOne := Analyze(transactions, time.Date(2027, 3, 27, 12, 0, 0, 0, time.UTC))
	if find(yearlyAtThreeNinetyOne, "Yearly Service").Active {
		t.Fatal("yearly subscription aged 391 days should be inactive")
	}
}

func TestAnalyzeDirtyMerchantRootsAndFallback(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		booked("acc-1", "Amazon Prime*nk9ae7fn4", "EUR", -499, "2026-01-05"),
		booked("acc-1", "Amazon Prime*nj7t039f4", "EUR", -499, "2026-02-04"),
		booked("acc-1", "Amazon Prime*zz8aa111x", "EUR", -499, "2026-03-06"),
		booked("acc-1", "Google G1ai101m", "EUR", -2199, "2026-01-10"),
		booked("acc-1", "Google One", "EUR", -2199, "2026-02-09"),
		booked("acc-1", "Google G1zz220n", "EUR", -2199, "2026-03-11"),
		booked("acc-1", "Google *temporary Hold", "EUR", 0, "2026-02-15"),
		booked("acc-1", "Amazon Prime Refund", "EUR", -499, "2026-02-20"),
	}
	for i := range transactions {
		transactions[i].Category = "Uncategorized"
		if transactions[i].Description == "" {
			transactions[i].Description = transactions[i].Counterparty
		}
	}
	transactions[7].Description = "Refund for Amazon Prime"
	snapshot := Analyze(transactions, now)
	if len(snapshot.Subscriptions) != 2 {
		t.Fatalf("subscriptions = %+v", snapshot.Subscriptions)
	}
	byName := make(map[string]Subscription, len(snapshot.Subscriptions))
	for _, subscription := range snapshot.Subscriptions {
		byName[subscription.Counterparty] = subscription
	}
	if amazon, ok := byName["Amazon Prime"]; !ok || amazon.Category != "Subscriptions" || amazon.AmountMinor != 499 {
		t.Fatalf("amazon prime = %+v", byName)
	}
	if google, ok := byName["Google"]; !ok || google.Category != "Subscriptions" || google.AmountMinor != 2199 {
		t.Fatalf("google = %+v", byName)
	}
}

func TestAnalyzeFallbackRequiresStableAmount(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		booked("acc-1", "Unknown Service", "EUR", -999, "2026-01-05"),
		booked("acc-1", "Unknown Service", "EUR", -1099, "2026-02-04"),
		booked("acc-1", "Unknown Service", "EUR", -1099, "2026-03-06"),
	}
	for i := range transactions {
		transactions[i].Category = "Uncategorized"
	}
	if snapshot := Analyze(transactions, now); len(snapshot.Subscriptions) != 0 {
		t.Fatalf("uncategorized price change should not be a fallback subscription: %+v", snapshot.Subscriptions)
	}
}
