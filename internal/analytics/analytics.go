// Package analytics computes spending, pacing, projections, and liquidity
// series from Fiscus transaction history. All monetary values stay in integer
// minor units; percentages and yields are expressed in basis points.
package analytics

import (
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/treasury"
	"github.com/RedRiotTank/Fiscus/pkg/transfers"
)

const (
	// PeriodMonth is the current calendar month.
	PeriodMonth = "month"
	// PeriodQuarter is the current calendar quarter.
	PeriodQuarter = "quarter"
	// PeriodYTD is the current year to date.
	PeriodYTD = "ytd"
)

// NormalizePeriod validates a requested reporting period.
func NormalizePeriod(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", PeriodMonth:
		return PeriodMonth, true
	case PeriodQuarter:
		return PeriodQuarter, true
	case PeriodYTD, "year":
		return PeriodYTD, true
	default:
		return "", false
	}
}

func periodStart(now time.Time, period string) time.Time {
	month := monthStart(now)
	switch period {
	case PeriodQuarter:
		quarterMonth := time.Month((int(now.Month())-1)/3*3 + 1)
		return time.Date(now.Year(), quarterMonth, 1, 0, 0, 0, 0, time.UTC)
	case PeriodYTD:
		return time.Date(now.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	default:
		return month
	}
}

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func dayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func inRange(t, from, to time.Time) bool {
	t = t.UTC()
	return !t.Before(from) && t.Before(to)
}

func normalizeCurrency(currency string) string {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return "EUR"
	}
	return currency
}

func bookedInCurrency(transactions []domain.Transaction, currency string) []domain.Transaction {
	out := make([]domain.Transaction, 0, len(transactions))
	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(tx.Currency), currency) {
			continue
		}
		out = append(out, tx)
	}
	return out
}

func spendingTransactions(transactions []domain.Transaction, currency string) []domain.Transaction {
	booked := bookedInCurrency(transactions, currency)
	filtered := make([]domain.Transaction, 0, len(booked))
	for _, tx := range booked {
		if transfers.IsExcluded(tx) {
			continue
		}
		filtered = append(filtered, tx)
	}
	pairs := treasury.PairTransfers(filtered, 3)
	return treasury.ExcludeTransfers(filtered, pairs)
}

func currencyBalance(accounts []domain.Account, currency string) int64 {
	var total int64
	for _, account := range accounts {
		if strings.EqualFold(strings.TrimSpace(account.Currency), currency) {
			total += account.BalanceMinor
		}
	}
	return total
}

func categoryName(category string) string {
	category = strings.TrimSpace(category)
	if category == "" {
		return "Uncategorized"
	}
	return category
}
