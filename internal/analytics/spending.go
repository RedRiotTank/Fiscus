package analytics

import (
	"sort"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// CategoryShare is a category total and its share of the period spend.
type CategoryShare struct {
	Category string `json:"category"`
	Minor    int64  `json:"minor"`
	ShareBP  int64  `json:"share_bp"`
}

// TrendPoint is one month in the income-versus-expense trend.
type TrendPoint struct {
	Month        string `json:"month"`
	Label        string `json:"label"`
	IncomeMinor  int64  `json:"income_minor"`
	ExpenseMinor int64  `json:"expense_minor"`
	NetMinor     int64  `json:"net_minor"`
}

// WaterfallStep is a named inflow or outflow bucket.
type WaterfallStep struct {
	Label string `json:"label"`
	Minor int64  `json:"minor"`
}

// Waterfall describes the current month cash-flow bridge.
type Waterfall struct {
	OpeningMinor int64           `json:"opening_minor"`
	InflowMinor  int64           `json:"inflow_minor"`
	Outflows     []WaterfallStep `json:"outflows"`
	ClosingMinor int64           `json:"closing_minor"`
}

// Spending is the payload for GET /api/analytics/spending.
type Spending struct {
	Currency   string          `json:"currency"`
	Period     string          `json:"period"`
	From       string          `json:"from"`
	To         string          `json:"to"`
	MinorScale int             `json:"minor_scale"`
	TotalMinor int64           `json:"total_minor"`
	Categories []CategoryShare `json:"categories"`
	Trend      []TrendPoint    `json:"trend"`
	Waterfall  Waterfall       `json:"waterfall"`
}

// ComputeSpending builds the category breakdown, six-month trend, and current
// month waterfall for one currency.
func ComputeSpending(transactions []domain.Transaction, accounts []domain.Account, now time.Time, currency, period string) Spending {
	currency = normalizeCurrency(currency)
	normalized, ok := NormalizePeriod(period)
	if !ok {
		normalized = PeriodMonth
	}
	now = now.UTC()
	from := periodStart(now, normalized)
	end := dayStart(now).AddDate(0, 0, 1)
	spending := spendingTransactions(transactions, currency)

	byCategory := make(map[string]int64)
	var total int64
	for _, tx := range spending {
		if !inRange(tx.BookingDate, from, end) || tx.AmountMinor >= 0 {
			continue
		}
		category := categoryName(tx.Category)
		byCategory[category] += -tx.AmountMinor
		total += -tx.AmountMinor
	}
	categories := make([]CategoryShare, 0, len(byCategory))
	for category, minor := range byCategory {
		share := int64(0)
		if total > 0 {
			share = minor * 10000 / total
		}
		categories = append(categories, CategoryShare{Category: category, Minor: minor, ShareBP: share})
	}
	sort.Slice(categories, func(i, j int) bool {
		if categories[i].Minor == categories[j].Minor {
			return categories[i].Category < categories[j].Category
		}
		return categories[i].Minor > categories[j].Minor
	})

	trend := make([]TrendPoint, 0, 6)
	trendStart := monthStart(now).AddDate(0, -5, 0)
	for i := 0; i < 6; i++ {
		start := trendStart.AddDate(0, i, 0)
		finish := start.AddDate(0, 1, 0)
		point := TrendPoint{Month: start.Format("2006-01"), Label: start.Format("Jan 06")}
		for _, tx := range spending {
			if !inRange(tx.BookingDate, start, finish) {
				continue
			}
			if tx.AmountMinor > 0 {
				point.IncomeMinor += tx.AmountMinor
			} else {
				point.ExpenseMinor += -tx.AmountMinor
			}
		}
		point.NetMinor = point.IncomeMinor - point.ExpenseMinor
		trend = append(trend, point)
	}

	waterfall := currentWaterfall(spending, accounts, now, currency)

	return Spending{
		Currency:   currency,
		Period:     normalized,
		From:       from.Format("2006-01-02"),
		To:         dayStart(now).Format("2006-01-02"),
		MinorScale: money.Scale(currency),
		TotalMinor: total,
		Categories: categories,
		Trend:      trend,
		Waterfall:  waterfall,
	}
}

func currentWaterfall(transactions []domain.Transaction, accounts []domain.Account, now time.Time, currency string) Waterfall {
	start := monthStart(now)
	end := start.AddDate(0, 1, 0)
	var net, inflow int64
	outflows := make(map[string]int64)
	for _, tx := range transactions {
		if !inRange(tx.BookingDate, start, end) {
			continue
		}
		net += tx.AmountMinor
		if tx.AmountMinor > 0 {
			inflow += tx.AmountMinor
		} else {
			outflows[categoryName(tx.Category)] += -tx.AmountMinor
		}
	}
	closing := currencyBalance(accounts, currency)
	steps := make([]WaterfallStep, 0, len(outflows))
	for label, minor := range outflows {
		steps = append(steps, WaterfallStep{Label: label, Minor: minor})
	}
	sort.Slice(steps, func(i, j int) bool {
		if steps[i].Minor == steps[j].Minor {
			return steps[i].Label < steps[j].Label
		}
		return steps[i].Minor > steps[j].Minor
	})
	return Waterfall{
		OpeningMinor: closing - net,
		InflowMinor:  inflow,
		Outflows:     steps,
		ClosingMinor: closing,
	}
}
