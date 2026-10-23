package analytics

import (
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func scenarioFixture() ([]domain.Transaction, []domain.Account, time.Time) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	accounts := []domain.Account{{ID: "acc-1", Currency: "EUR", BalanceMinor: 100000}}
	var transactions []domain.Transaction
	id := int64(1)
	for month := 1; month <= 12; month++ {
		start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).AddDate(0, -month, 0)
		transactions = append(transactions,
			testTx(id, "acc-1", 10000, start.AddDate(0, 0, 1), "Payroll", domain.TransactionStatusBooked))
		id++
		transactions = append(transactions,
			testTx(id, "acc-1", -5000, start.AddDate(0, 0, 2), "Living", domain.TransactionStatusBooked))
		id++
	}
	return transactions, accounts, now
}

func TestComputeScenarioAppliesDeltasAndOneOff(t *testing.T) {
	t.Parallel()
	transactions, accounts, now := scenarioFixture()
	scenario := ComputeScenario(transactions, accounts, now, "EUR", ScenarioInput{
		Months:            12,
		IncomeDeltaMinor:  1000,
		ExpenseDeltaMinor: 500,
		OneOffMinor:       -20000,
		OneOffMonth:       3,
	})
	if scenario.BaselineMonthlyMinor != 5000 || scenario.ScenarioMonthlyMinor != 5500 {
		t.Fatalf("monthly = %d/%d", scenario.BaselineMonthlyMinor, scenario.ScenarioMonthlyMinor)
	}
	if len(scenario.Labels) != 13 || len(scenario.Baseline) != 13 || len(scenario.Simulated) != 13 {
		t.Fatalf("series lengths = %d/%d/%d", len(scenario.Labels), len(scenario.Baseline), len(scenario.Simulated))
	}
	if scenario.Baseline[12] != 160000 {
		t.Fatalf("baseline end = %d", scenario.Baseline[12])
	}
	if scenario.Simulated[2] != 111000 || scenario.Simulated[3] != 96500 || scenario.Simulated[12] != 146000 {
		t.Fatalf("simulated = %#v", scenario.Simulated)
	}
	if scenario.EndingDeltaMinor != -14000 || scenario.EndingScenarioMinor-scenario.EndingBaselineMinor != -14000 {
		t.Fatalf("ending = %+v", scenario)
	}
}

func TestComputeScenarioNoChangesMatchesBaseline(t *testing.T) {
	t.Parallel()
	transactions, accounts, now := scenarioFixture()
	scenario := ComputeScenario(transactions, accounts, now, "EUR", ScenarioInput{Months: 24, AnnualYieldBP: 300})
	if len(scenario.Baseline) != len(scenario.Simulated) {
		t.Fatalf("series lengths differ: %d/%d", len(scenario.Baseline), len(scenario.Simulated))
	}
	for i := range scenario.Baseline {
		if scenario.Baseline[i] != scenario.Simulated[i] {
			t.Fatalf("point %d differs: %d/%d", i, scenario.Baseline[i], scenario.Simulated[i])
		}
	}
	if scenario.OneOffMonth != 1 || scenario.Months != 24 {
		t.Fatalf("normalization = %+v", scenario)
	}
}

func TestComputeScenarioClampsHorizonAndOneOffMonth(t *testing.T) {
	t.Parallel()
	transactions, accounts, now := scenarioFixture()
	scenario := ComputeScenario(transactions, accounts, now, "EUR", ScenarioInput{
		Months: 500, OneOffMinor: 1000, OneOffMonth: 999,
	})
	if scenario.Months != 180 || scenario.OneOffMonth != 180 {
		t.Fatalf("clamp = months %d one-off %d", scenario.Months, scenario.OneOffMonth)
	}
	zero := ComputeScenario(transactions, accounts, now, "EUR", ScenarioInput{Months: 0})
	if zero.Months != 12 {
		t.Fatalf("default months = %d", zero.Months)
	}
}
