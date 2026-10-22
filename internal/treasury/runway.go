package treasury

import (
	"sort"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// DefaultBurnWindowDays is the rolling window used for monthly burn.
const DefaultBurnWindowDays = 90

// Runway summarizes liquid funds, burn, and remaining months for a currency.
type Runway struct {
	Currency         string
	LiquidMinor      int64
	MonthlyBurnMinor int64
	RunwayTenths     int64
	RunwayKnown      bool
}

// ComputeRunways calculates average monthly operational burn and runway per
// currency. Internal transfers between own accounts are paired and excluded.
func ComputeRunways(accounts []domain.Account, transactions []domain.Transaction, now time.Time, windowDays int) []Runway {
	if windowDays <= 0 {
		windowDays = DefaultBurnWindowDays
	}
	now = now.UTC()
	from := now.Add(-time.Duration(windowDays) * 24 * time.Hour)

	liquid := make(map[string]int64)
	for _, account := range accounts {
		currency := strings.ToUpper(account.Currency)
		if currency == "" {
			currency = "EUR"
		}
		liquid[currency] += account.BalanceMinor
	}

	windowTransactions := make([]domain.Transaction, 0, len(transactions))
	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked {
			continue
		}
		if tx.BookingDate.Before(from) || tx.BookingDate.After(now) {
			continue
		}
		windowTransactions = append(windowTransactions, tx)
	}
	windowTransactions = ExcludeTransfers(windowTransactions, PairTransfers(windowTransactions, 3))

	burn := make(map[string]int64)
	for _, tx := range windowTransactions {
		if tx.AmountMinor >= 0 {
			continue
		}
		currency := strings.ToUpper(tx.Currency)
		if currency == "" {
			currency = "EUR"
		}
		burn[currency] += -tx.AmountMinor
	}

	currencies := make(map[string]struct{})
	for currency := range liquid {
		currencies[currency] = struct{}{}
	}
	for currency := range burn {
		currencies[currency] = struct{}{}
	}
	ordered := make([]string, 0, len(currencies))
	for currency := range currencies {
		ordered = append(ordered, currency)
	}
	sort.Strings(ordered)

	out := make([]Runway, 0, len(ordered))
	for _, currency := range ordered {
		monthlyBurn := (burn[currency]*30 + int64(windowDays)/2) / int64(windowDays)
		item := Runway{
			Currency:         currency,
			LiquidMinor:      liquid[currency],
			MonthlyBurnMinor: monthlyBurn,
		}
		switch {
		case monthlyBurn <= 0:
			item.RunwayKnown = false
		case item.LiquidMinor <= 0:
			item.RunwayKnown = true
		default:
			item.RunwayTenths = item.LiquidMinor * 10 / monthlyBurn
			item.RunwayKnown = true
		}
		out = append(out, item)
	}
	return out
}
