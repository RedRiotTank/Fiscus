package budgets

import (
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

func TestNetRecurringBillSpendingUsesPersonalShare(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	spending := []storage.CategorySpending{
		{Category: "Transfers / P2P", Currency: "EUR", Minor: 1600},
		{Category: "Groceries", Currency: "EUR", Minor: 5000},
	}
	transactions := []domain.Transaction{{
		AccountID: "acc-1", AmountMinor: -1600, Currency: "EUR", BookingDate: now,
		Counterparty: "Alberto Avidad Fernández", Description: "Bizum fibra",
		Category: "Transfers / P2P", Status: domain.TransactionStatusBooked,
	}}
	rules := []domain.RecurringBillRule{{
		Workspace: "personal", Currency: "EUR",
		Counterparty: "Alberto Avidad Fernández", Category: "Utilities & Telecom",
		Pillar: "needs", SplitMode: domain.RecurringSplitShared, SplitPeople: 3,
		GrossMinor: 1600, PersonalShareMinor: 533, Active: true,
	}}
	netted := NetRecurringBillSpending(spending, transactions, rules)
	byCategory := make(map[string]int64, len(netted))
	for _, item := range netted {
		byCategory[item.Category] = item.Minor
	}
	if byCategory["Utilities & Telecom"] != 533 {
		t.Fatalf("utilities spend = %d, want 533 (%+v)", byCategory["Utilities & Telecom"], netted)
	}
	if byCategory["Transfers / P2P"] != 0 {
		t.Fatalf("gross P2P transfer was not removed: %+v", netted)
	}
	if byCategory["Groceries"] != 5000 {
		t.Fatalf("unrelated spending changed: %+v", netted)
	}
}

func TestSubscriptionsBudgetExcludesFixedBillShare(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	spending := []storage.CategorySpending{
		{Category: "Subscriptions", Currency: "EUR", Minor: 2_698},
		{Category: "Utilities & Telecom", Currency: "EUR", Minor: 599},
	}
	transactions := []domain.Transaction{{
		AmountMinor: -599, Currency: "EUR", BookingDate: now,
		Counterparty: "Alberto Avidad Fernández", Description: "Bizum internet",
		Category: "Utilities & Telecom", Status: domain.TransactionStatusBooked,
	}}
	rules := []domain.RecurringBillRule{{
		Workspace: "personal", Currency: "EUR", Counterparty: "Alberto Avidad Fernández",
		Category: "Utilities & Telecom", Pillar: "needs",
		SplitMode: domain.RecurringSplitShared, SplitPeople: 3,
		GrossMinor: 599, PersonalShareMinor: 533, Active: true,
	}}
	netted := NetRecurringBillSpending(spending, transactions, rules)
	plan := BuildPlan(Profile{
		Workspace: "personal", Currency: "EUR", NeedsBPS: 5000, WantsBPS: 3000, SavingsBPS: 2000,
	}, []Allocation{
		{Category: "Subscriptions", Pillar: PillarWants, PercentBPS: 500},
		{Category: "Utilities & Telecom", Pillar: PillarNeeds, PercentBPS: 800},
	}, netted, 500_000, 0, 500_000, now)
	byCategory := make(map[string]CategoryPlan, len(plan.Categories))
	for _, category := range plan.Categories {
		byCategory[category.Category] = category
	}
	if byCategory["Subscriptions"].SpentMinor != 2_698 {
		t.Fatalf("subscriptions spend = %d, want 2698", byCategory["Subscriptions"].SpentMinor)
	}
	if byCategory["Utilities & Telecom"].SpentMinor != 533 {
		t.Fatalf("utilities spend = %d, want 533", byCategory["Utilities & Telecom"].SpentMinor)
	}
}

func TestNetRecurringBillSpendingSoloKeepsGross(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	spending := []storage.CategorySpending{{Category: "Transfers / P2P", Currency: "EUR", Minor: 1600}}
	transactions := []domain.Transaction{{
		AmountMinor: -1600, Currency: "EUR", BookingDate: now,
		Counterparty: "Alberto Avidad Fernández", Category: "Transfers / P2P",
		Status: domain.TransactionStatusBooked,
	}}
	rules := []domain.RecurringBillRule{{
		Workspace: "personal", Currency: "EUR", Counterparty: "Alberto Avidad Fernández",
		Category: "Utilities & Telecom", Pillar: "needs",
		SplitMode: domain.RecurringSplitSolo, GrossMinor: 1600, PersonalShareMinor: 1600, Active: true,
	}}
	netted := NetRecurringBillSpending(spending, transactions, rules)
	byCategory := make(map[string]int64, len(netted))
	for _, item := range netted {
		byCategory[item.Category] = item.Minor
	}
	if byCategory["Utilities & Telecom"] != 1600 {
		t.Fatalf("utilities spend = %d, want 1600", byCategory["Utilities & Telecom"])
	}
}
