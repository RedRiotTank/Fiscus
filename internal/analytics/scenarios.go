package analytics

import (
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// ScenarioInput describes a what-if change to monthly cash flow.
type ScenarioInput struct {
	Months            int
	IncomeDeltaMinor  int64
	ExpenseDeltaMinor int64
	OneOffMinor       int64
	OneOffMonth       int
	ExtraMonthlyMinor int64
	AnnualYieldBP     int64
}

// Scenario is the base-vs-simulated projection payload returned by the
// scenario API. Arrays share the same labels and include the current point.
type Scenario struct {
	Currency             string   `json:"currency"`
	MinorScale           int      `json:"minor_scale"`
	Months               int      `json:"months"`
	Labels               []string `json:"labels"`
	Baseline             []int64  `json:"baseline"`
	Simulated            []int64  `json:"simulated"`
	CurrentBalanceMinor  int64    `json:"current_balance_minor"`
	BaselineMonthlyMinor int64    `json:"baseline_monthly_minor"`
	ScenarioMonthlyMinor int64    `json:"scenario_monthly_minor"`
	IncomeDeltaMinor     int64    `json:"income_delta_minor"`
	ExpenseDeltaMinor    int64    `json:"expense_delta_minor"`
	OneOffMinor          int64    `json:"one_off_minor"`
	OneOffMonth          int      `json:"one_off_month"`
	AnnualYieldBP        int64    `json:"annual_yield_bp"`
	EndingBaselineMinor  int64    `json:"ending_baseline_minor"`
	EndingScenarioMinor  int64    `json:"ending_scenario_minor"`
	EndingDeltaMinor     int64    `json:"ending_delta_minor"`
}

// ComputeScenario projects a baseline balance and a modified cash-flow path.
// Positive income deltas add cash, positive expense deltas consume cash, and a
// one-off event is applied at the start of its 1-based month.
func ComputeScenario(transactions []domain.Transaction, accounts []domain.Account, now time.Time, currency string, input ScenarioInput) Scenario {
	currency = normalizeCurrency(currency)
	now = now.UTC()
	months := input.Months
	if months <= 0 {
		months = 12
	}
	if months > 180 {
		months = 180
	}
	oneOffMonth := input.OneOffMonth
	if oneOffMonth <= 0 {
		oneOffMonth = 1
	}
	if oneOffMonth > months {
		oneOffMonth = months
	}
	projections := ComputeProjections(transactions, accounts, now, currency, input.ExtraMonthlyMinor, input.AnnualYieldBP)
	baseline := projectHorizon(now, projections.CurrentBalanceMinor, projections.EffectiveMuMinor, 0, input.AnnualYieldBP, months)
	scenarioMonthly := projections.EffectiveMuMinor + input.IncomeDeltaMinor - input.ExpenseDeltaMinor
	simulated := make([]int64, 0, months+1)
	simulated = append(simulated, projections.CurrentBalanceMinor)
	balance := projections.CurrentBalanceMinor
	for month := 1; month <= months; month++ {
		if month == oneOffMonth {
			balance += input.OneOffMinor
		}
		interest := balance * input.AnnualYieldBP / 120000
		balance += interest + scenarioMonthly
		simulated = append(simulated, balance)
	}
	endingBaseline := baseline.Baseline[len(baseline.Baseline)-1]
	endingScenario := simulated[len(simulated)-1]
	return Scenario{
		Currency:             currency,
		MinorScale:           money.Scale(currency),
		Months:               months,
		Labels:               baseline.Labels,
		Baseline:             baseline.Baseline,
		Simulated:            simulated,
		CurrentBalanceMinor:  projections.CurrentBalanceMinor,
		BaselineMonthlyMinor: projections.EffectiveMuMinor,
		ScenarioMonthlyMinor: scenarioMonthly,
		IncomeDeltaMinor:     input.IncomeDeltaMinor,
		ExpenseDeltaMinor:    input.ExpenseDeltaMinor,
		OneOffMinor:          input.OneOffMinor,
		OneOffMonth:          oneOffMonth,
		AnnualYieldBP:        input.AnnualYieldBP,
		EndingBaselineMinor:  endingBaseline,
		EndingScenarioMinor:  endingScenario,
		EndingDeltaMinor:     endingScenario - endingBaseline,
	}
}
