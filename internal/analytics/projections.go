package analytics

import (
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// BalancePoint is an observed historical balance.
type BalancePoint struct {
	Month        string `json:"month"`
	Label        string `json:"label"`
	BalanceMinor int64  `json:"balance_minor"`
}

// HorizonProjection holds one forward-looking horizon.
type HorizonProjection struct {
	Months       int      `json:"months"`
	Labels       []string `json:"labels"`
	Baseline     []int64  `json:"baseline"`
	Optimistic   []int64  `json:"optimistic"`
	Conservative []int64  `json:"conservative"`
}

// Projections is the payload for GET /api/analytics/projections.
type Projections struct {
	Currency            string                       `json:"currency"`
	CurrentBalanceMinor int64                        `json:"current_balance_minor"`
	MinorScale          int                          `json:"minor_scale"`
	MuMinor             int64                        `json:"mu_minor"`
	SigmaMinor          int64                        `json:"sigma_minor"`
	ExtraMonthlyMinor   int64                        `json:"extra_monthly_minor"`
	EffectiveMuMinor    int64                        `json:"effective_mu_minor"`
	AnnualYieldBP       int64                        `json:"annual_yield_bp"`
	History             []BalancePoint               `json:"history"`
	Horizons            map[string]HorizonProjection `json:"horizons"`
}

// ComputeProjections derives historical monthly net savings, their sample mean
// and standard deviation, and forward curves for 1, 5, 10, and 15 years.
func ComputeProjections(transactions []domain.Transaction, accounts []domain.Account, now time.Time, currency string, extraMonthly, annualYieldBP int64) Projections {
	currency = normalizeCurrency(currency)
	now = now.UTC()
	current := currencyBalance(accounts, currency)
	spending := spendingTransactions(transactions, currency)
	month := monthStart(now)

	savings := make([]int64, 0, 12)
	for i := 12; i >= 1; i-- {
		start := month.AddDate(0, -i, 0)
		finish := start.AddDate(0, 1, 0)
		var inflow, outflow int64
		for _, tx := range spending {
			if !inRange(tx.BookingDate, start, finish) {
				continue
			}
			if tx.AmountMinor > 0 {
				inflow += tx.AmountMinor
			} else {
				outflow += -tx.AmountMinor
			}
		}
		savings = append(savings, inflow-outflow)
	}
	var mean int64
	for _, saving := range savings {
		mean += saving
	}
	if len(savings) > 0 {
		mean /= int64(len(savings))
	}
	var sigma int64
	if len(savings) > 1 {
		var sumSquares int64
		for _, saving := range savings {
			delta := saving - mean
			sumSquares += delta * delta
		}
		sigma = isqrt(sumSquares / int64(len(savings)-1))
	}

	history := historicalBalances(accounts, transactions, now, currency)
	effective := mean + extraMonthly
	horizons := make(map[string]HorizonProjection, 4)
	for _, spec := range []struct {
		key    string
		months int
	}{
		{"1y", 12}, {"5y", 60}, {"10y", 120}, {"15y", 180},
	} {
		horizons[spec.key] = projectHorizon(now, current, effective, sigma, annualYieldBP, spec.months)
	}
	return Projections{
		Currency:            currency,
		CurrentBalanceMinor: current,
		MinorScale:          money.Scale(currency),
		MuMinor:             mean,
		SigmaMinor:          sigma,
		ExtraMonthlyMinor:   extraMonthly,
		EffectiveMuMinor:    effective,
		AnnualYieldBP:       annualYieldBP,
		History:             history,
		Horizons:            horizons,
	}
}

func projectHorizon(now time.Time, current, monthly, sigma, annualYieldBP int64, months int) HorizonProjection {
	labels := make([]string, 0, months+1)
	baseline := make([]int64, 0, months+1)
	optimistic := make([]int64, 0, months+1)
	conservative := make([]int64, 0, months+1)
	labels = append(labels, "Now")
	baseline = append(baseline, current)
	optimistic = append(optimistic, current)
	conservative = append(conservative, current)
	balance := current
	for i := 1; i <= months; i++ {
		interest := balance * annualYieldBP / 120000
		balance += interest + monthly
		band := sigma * scaledSqrt(int64(i)) / 1000
		labels = append(labels, monthStart(now).AddDate(0, i, 0).Format("Jan 06"))
		baseline = append(baseline, balance)
		optimistic = append(optimistic, balance+band)
		conservative = append(conservative, balance-band)
	}
	return HorizonProjection{
		Months:       months,
		Labels:       labels,
		Baseline:     baseline,
		Optimistic:   optimistic,
		Conservative: conservative,
	}
}
func historicalBalances(accounts []domain.Account, transactions []domain.Transaction, now time.Time, currency string) []BalancePoint {
	current := currencyBalance(accounts, currency)
	month := monthStart(now)
	booked := bookedInCurrency(transactions, currency)
	history := make([]BalancePoint, 0, 13)
	for i := 12; i >= 1; i-- {
		cutoff := month.AddDate(0, -i+1, 0)
		var after int64
		for _, tx := range booked {
			if !tx.BookingDate.UTC().Before(cutoff) {
				after += tx.AmountMinor
			}
		}
		label := cutoff.AddDate(0, -1, 0)
		history = append(history, BalancePoint{
			Month:        label.Format("2006-01"),
			Label:        label.Format("Jan 06"),
			BalanceMinor: current - after,
		})
	}
	history = append(history, BalancePoint{Month: month.Format("2006-01"), Label: "Now", BalanceMinor: current})
	return history
}

func isqrt(value int64) int64 {
	if value <= 0 {
		return 0
	}
	x := value
	y := (x + 1) / 2
	for y < x {
		x = y
		y = (x + value/x) / 2
	}
	return x
}

// scaledSqrt returns sqrt(value) in thousandths, e.g. 12 -> 3464.
func scaledSqrt(value int64) int64 {
	if value <= 0 {
		return 0
	}
	return isqrt(value * 1000000)
}
