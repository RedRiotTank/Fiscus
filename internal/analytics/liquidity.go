package analytics

import (
	"sort"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// LiquiditySeries is one institution's stacked balance series.
type LiquiditySeries struct {
	Institution string  `json:"institution"`
	Points      []int64 `json:"points"`
}

// Liquidity is the payload for GET /api/analytics/liquidity.
type Liquidity struct {
	Currency   string            `json:"currency"`
	From       string            `json:"from"`
	To         string            `json:"to"`
	MinorScale int               `json:"minor_scale"`
	Labels     []string          `json:"labels"`
	Series     []LiquiditySeries `json:"series"`
}

// ComputeLiquidity reconstructs end-of-day balances per account from current
// balances and transaction history, then groups them by institution.
func ComputeLiquidity(accounts []domain.Account, connections []domain.Connection, transactions []domain.Transaction, now time.Time, currency string, days int) Liquidity {
	currency = normalizeCurrency(currency)
	now = now.UTC()
	if days <= 0 {
		days = 90
	}
	if days < 2 {
		days = 2
	}
	if days > 366 {
		days = 366
	}
	to := dayStart(now)
	from := to.AddDate(0, 0, -(days - 1))
	institutions := make(map[int64]string, len(connections))
	for _, connection := range connections {
		name := strings.TrimSpace(connection.InstitutionName)
		if name == "" {
			name = "Other"
		}
		institutions[connection.ID] = name
	}
	byAccount := make(map[string]map[string]int64)
	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked || !strings.EqualFold(strings.TrimSpace(tx.Currency), currency) {
			continue
		}
		date := dayStart(tx.BookingDate).Format("2006-01-02")
		if byAccount[tx.AccountID] == nil {
			byAccount[tx.AccountID] = make(map[string]int64)
		}
		byAccount[tx.AccountID][date] += tx.AmountMinor
	}
	labels := make([]string, days)
	for i := 0; i < days; i++ {
		labels[i] = from.AddDate(0, 0, i).Format("02 Jan")
	}
	aggregated := make(map[string][]int64)
	for _, account := range accounts {
		if !strings.EqualFold(strings.TrimSpace(account.Currency), currency) {
			continue
		}
		name := institutions[account.ConnectionID]
		if name == "" {
			name = strings.TrimSpace(account.Name)
		}
		if name == "" {
			name = "Other"
		}
		points := make([]int64, days)
		after := int64(0)
		dayTotals := byAccount[account.ID]
		for i := days - 1; i >= 0; i-- {
			date := from.AddDate(0, 0, i)
			points[i] = account.BalanceMinor - after
			after += dayTotals[date.Format("2006-01-02")]
		}
		if aggregated[name] == nil {
			aggregated[name] = make([]int64, days)
		}
		for i := range points {
			aggregated[name][i] += points[i]
		}
	}
	names := make([]string, 0, len(aggregated))
	for name := range aggregated {
		names = append(names, name)
	}
	sort.Strings(names)
	series := make([]LiquiditySeries, 0, len(names))
	for _, name := range names {
		series = append(series, LiquiditySeries{Institution: name, Points: aggregated[name]})
	}
	return Liquidity{
		Currency:   currency,
		From:       from.Format("2006-01-02"),
		To:         to.Format("2006-01-02"),
		MinorScale: money.Scale(currency),
		Labels:     labels,
		Series:     series,
	}
}
