package budgets

import (
	"fmt"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func advisoryTransaction(account, externalID string, amount int64, date time.Time, category string) domain.Transaction {
	return domain.Transaction{
		AccountID: account, ExternalID: externalID, AmountMinor: amount, Currency: "EUR",
		BookingDate: date, Category: category, Status: domain.TransactionStatusBooked,
	}
}

func advisoryFixture(now time.Time, months int) []domain.Transaction {
	transactions := make([]domain.Transaction, 0)
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -(months - 1), 0)
	groceryAmounts := []int64{12_000, 20_000, 28_000, 20_000, 20_000, 20_000}
	for i := 0; i < months; i++ {
		month := start.AddDate(0, i, 0)
		day := func(day int) time.Time {
			return time.Date(month.Year(), month.Month(), day, 10, 0, 0, 0, time.UTC)
		}
		key := month.Format("200601")
		transactions = append(transactions,
			advisoryTransaction("checking", "salary-"+key, 280_000, day(1), "Payroll"),
			advisoryTransaction("checking", "rent-"+key, -100_000, day(2), "Rent"),
			advisoryTransaction("checking", "transfer-out-"+key, -30_000, day(3), "Transfer"),
			advisoryTransaction("savings", "transfer-in-"+key, 30_000, day(3), "Transfer"),
			advisoryTransaction("checking", "groceries-"+key, -groceryAmounts[i], day(5), "Groceries"),
			advisoryTransaction("checking", "restaurants-"+key, -50_000, day(8), "Restaurants"),
			advisoryTransaction("checking", "shopping-"+key, -13_000, day(12), "Shopping"),
			advisoryTransaction("checking", "subscriptions-"+key, -3_000, day(15), "Subscriptions"),
			advisoryTransaction("checking", "utilities-"+key, -15_000, day(20), "Utilities"),
		)
	}
	return transactions
}

func advisoryBudgets() []Budget {
	return []Budget{
		{Category: "Restaurants", LimitMinor: 45_000, Currency: "EUR", NotifyAtPercent: []int{80, 100}},
		{Category: "Groceries", LimitMinor: 24_000, Currency: "EUR", NotifyAtPercent: []int{80, 100}},
		{Category: "Shopping", LimitMinor: 5_000, Currency: "EUR", NotifyAtPercent: []int{80, 100}},
	}
}

func TestComputeAdvisoryNormativeAndSurplus(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	advisories := ComputeAdvisory(advisoryFixture(now, 6), advisoryBudgets(), AdvisoryConfig{
		Enabled: true, Model: AdvisoryModelHybrid,
		NeedsPercent: 50, WantsPercent: 30, SavingsPercent: 20,
		HeuristicWindowMonths: 6, OverbudgetWarningThreshold: 0.15, UnderbudgetNoticeThreshold: 0.30,
	}, nil, now)
	if len(advisories) != 1 {
		t.Fatalf("advisories = %+v", advisories)
	}
	advisory := advisories[0]
	if advisory.Currency != "EUR" || advisory.MonthlyNetIncomeMinor != 280_000 ||
		advisory.NeedsCapacityMinor != 140_000 || advisory.WantsCapacityMinor != 84_000 ||
		advisory.SavingsCapacityMinor != 56_000 {
		t.Fatalf("advisory capacities = %+v", advisory)
	}
	if advisory.NeedsSpentMinor != 135_000 || advisory.WantsSpentMinor != 66_000 ||
		advisory.SavingsSpentMinor != 30_000 || advisory.UnallocatedWantsMinor != 18_000 {
		t.Fatalf("advisory spending = %+v", advisory)
	}
	byCategory := make(map[string]CategoryAdvisory, len(advisory.Categories))
	for _, category := range advisory.Categories {
		byCategory[category.Category] = category
	}
	restaurants := byCategory["Restaurants"]
	if restaurants.Status != StatusOverAllocated || restaurants.RecommendedMinor != 28_000 ||
		restaurants.DeviationPercent != 60 {
		t.Fatalf("restaurants = %+v", restaurants)
	}
	if groceries := byCategory["Groceries"]; groceries.Status != StatusBalanced {
		t.Fatalf("groceries = %+v", groceries)
	}
	if shopping := byCategory["Shopping"]; shopping.Status != StatusUnderBudget || shopping.SurplusMinor <= 0 {
		t.Fatalf("shopping = %+v", shopping)
	}
}

func TestComputeAdvisoryModelsAndTaxProvision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	transactions := advisoryFixture(now, 6)
	base := AdvisoryConfig{
		Enabled: true, NeedsPercent: 50, WantsPercent: 30, SavingsPercent: 20,
		HeuristicWindowMonths: 6, OverbudgetWarningThreshold: 0.15, UnderbudgetNoticeThreshold: 0.30,
	}
	normative := base
	normative.Model = AdvisoryModelNormative
	heuristic := base
	heuristic.Model = AdvisoryModelHeuristic
	normativeAdvisories := ComputeAdvisory(transactions, advisoryBudgets(), normative, nil, now)
	heuristicAdvisories := ComputeAdvisory(transactions, advisoryBudgets(), heuristic, nil, now)
	normativeRestaurants := categoryByName(normativeAdvisories[0], "Restaurants")
	heuristicRestaurants := categoryByName(heuristicAdvisories[0], "Restaurants")
	if normativeRestaurants.RecommendedMinor != 28_000 || heuristicRestaurants.RecommendedMinor != 50_000 {
		t.Fatalf("model recommendations = %d/%d", normativeRestaurants.RecommendedMinor, heuristicRestaurants.RecommendedMinor)
	}
	taxed := ComputeAdvisory(transactions, advisoryBudgets(), base, map[string]int64{"EUR": 50_000}, now)
	if taxed[0].MonthlyNetIncomeMinor != 230_000 || taxed[0].TaxProvisionMinor != 50_000 {
		t.Fatalf("tax provision = %+v", taxed[0])
	}
}

func TestComputeAdvisorySparseHistory(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	advisories := ComputeAdvisory(advisoryFixture(now, 2), advisoryBudgets(), AdvisoryConfig{
		Enabled: true, Model: AdvisoryModelHybrid,
		NeedsPercent: 50, WantsPercent: 30, SavingsPercent: 20,
		HeuristicWindowMonths: 6, OverbudgetWarningThreshold: 0.15, UnderbudgetNoticeThreshold: 0.30,
	}, nil, now)
	if len(advisories) != 1 {
		t.Fatalf("advisories = %+v", advisories)
	}
	if restaurants := categoryByName(advisories[0], "Restaurants"); restaurants.MonthsObserved != 2 {
		t.Fatalf("sparse months = %+v", restaurants)
	}
	if advisories[0].MonthlyNetIncomeMinor != 280_000 {
		t.Fatalf("sparse income = %+v", advisories[0])
	}
}

func TestComputeAdvisoryIrregularIncome(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	amounts := []int64{280_000, 0, 350_000, 0, 280_000, 350_000}
	transactions := make([]domain.Transaction, 0)
	for i, amount := range amounts {
		if amount == 0 {
			continue
		}
		month := start.AddDate(0, i, 0)
		transactions = append(transactions, advisoryTransaction("checking",
			fmt.Sprintf("irregular-%d", i), amount, time.Date(month.Year(), month.Month(), 5, 10, 0, 0, 0, time.UTC), "Sales"))
	}
	advisories := ComputeAdvisory(transactions, nil, AdvisoryConfig{
		Enabled: true, Model: AdvisoryModelHybrid,
		NeedsPercent: 50, WantsPercent: 30, SavingsPercent: 20,
		HeuristicWindowMonths: 6, OverbudgetWarningThreshold: 0.15, UnderbudgetNoticeThreshold: 0.30,
	}, nil, now)
	if len(advisories) != 1 || advisories[0].MonthlyNetIncomeMinor != 210_000 {
		t.Fatalf("irregular income = %+v", advisories)
	}
}

func TestComputeAdvisoryDisabled(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	if advisories := ComputeAdvisory(advisoryFixture(now, 6), advisoryBudgets(), AdvisoryConfig{Enabled: false}, nil, now); len(advisories) != 0 {
		t.Fatalf("disabled advisories = %+v", advisories)
	}
}

func TestAdvisoryCategoryBuckets(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"Rent": "needs", "Groceries": "needs", "Utilities": "needs",
		"Restaurants": "wants", "Subscriptions": "wants", "Travel": "wants", "Bizum": "wants",
		"Investments": "savings", "Mystery": "unallocated",
	}
	for category, want := range tests {
		if got := BucketForCategory(category); got != want {
			t.Errorf("BucketForCategory(%q) = %q, want %q", category, got, want)
		}
	}
}

func categoryByName(advisory Advisory, name string) CategoryAdvisory {
	for _, category := range advisory.Categories {
		if category.Category == name {
			return category
		}
	}
	return CategoryAdvisory{}
}

func TestAdvisoryConfigNormalization(t *testing.T) {
	t.Parallel()
	cfg := NormalizeAdvisoryConfig(AdvisoryConfig{})
	if cfg.Model != AdvisoryModelHybrid || cfg.NeedsPercent != 50 || cfg.WantsPercent != 30 ||
		cfg.SavingsPercent != 20 || cfg.HeuristicWindowMonths != 6 {
		t.Fatalf("normalized config = %+v", cfg)
	}
	cfg = NormalizeAdvisoryConfig(AdvisoryConfig{Model: "bogus", HeuristicWindowMonths: 1})
	if cfg.Model != AdvisoryModelHybrid || cfg.HeuristicWindowMonths != 3 {
		t.Fatalf("clamped config = %+v", cfg)
	}
}

func TestComputeAdvisoryNetsFlatmateReimbursements(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	transactions := advisoryFixture(now, 6)
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -5, 0)
	for i := 0; i < 6; i++ {
		month := start.AddDate(0, i, 0)
		key := month.Format("200601")
		for index, roommate := range []string{"Ismael Díaz", "Ana Ruiz"} {
			tx := advisoryTransaction("checking", "flatmate-"+key+"-"+roommate, 35_000,
				time.Date(month.Year(), month.Month(), 3+index, 10, 0, 0, 0, time.UTC), "Uncategorized")
			tx.Counterparty = roommate
			tx.Description = "Piso " + month.Format("January") + " | From " + roommate
			transactions = append(transactions, tx)
		}
	}
	advisories := ComputeAdvisory(transactions, advisoryBudgets(), AdvisoryConfig{
		Enabled: true, Model: AdvisoryModelHybrid,
		NeedsPercent: 50, WantsPercent: 30, SavingsPercent: 20,
		HeuristicWindowMonths: 6, OverbudgetWarningThreshold: 0.15, UnderbudgetNoticeThreshold: 0.30,
	}, nil, now)
	if len(advisories) != 1 {
		t.Fatalf("advisories = %+v", advisories)
	}
	advisory := advisories[0]
	if advisory.MonthlyNetIncomeMinor != 280_000 || advisory.MonthlyInflowMinor != 280_000 {
		t.Fatalf("flatmate reimbursements inflated income: %+v", advisory)
	}
	if advisory.NeedsSpentMinor != 65_000 {
		t.Fatalf("net needs spending = %d, want 65000", advisory.NeedsSpentMinor)
	}
	housingAdvisory := categoryByName(advisory, "Housing / Rent")
	if housingAdvisory.ActualSpendMinor != 30_000 {
		t.Fatalf("net housing spend = %+v", housingAdvisory)
	}
}
