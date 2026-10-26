// Package budgets computes month-to-date category spending against configured
// caps and produces pacing alerts. All monetary values are integer minor units.
package budgets

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/money"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

// Budget is one category cap.
type Budget struct {
	Category        string
	LimitMinor      int64
	Currency        string
	NotifyAtPercent []int
}

// Status is the month-to-date consumption of one budget.
type Status struct {
	Category            string
	Currency            string
	LimitMinor          int64
	SpentMinor          int64
	RemainingMinor      int64
	Percent             int64
	BarWidth            int64
	Tone                string
	MonthElapsedPercent int64
}

// Tone classes used by the dashboard progress bars.
const (
	ToneHealthy  = "ok"
	ToneWarning  = "warning"
	ToneCritical = "critical"
)

// Normalize returns a budget with defaults applied and thresholds sorted and
// deduplicated.
func Normalize(budget Budget) Budget {
	category := strings.TrimSpace(budget.Category)
	if category == "" {
		category = "Uncategorized"
	}
	currency := strings.ToUpper(strings.TrimSpace(budget.Currency))
	if currency == "" {
		currency = "EUR"
	}
	seen := make(map[int]struct{}, len(budget.NotifyAtPercent))
	thresholds := make([]int, 0, len(budget.NotifyAtPercent)+1)
	for _, threshold := range budget.NotifyAtPercent {
		if threshold <= 0 || threshold > 100 {
			continue
		}
		if _, ok := seen[threshold]; ok {
			continue
		}
		seen[threshold] = struct{}{}
		thresholds = append(thresholds, threshold)
	}
	if len(thresholds) == 0 {
		thresholds = append(thresholds, 80)
	}
	if _, ok := seen[100]; !ok {
		thresholds = append(thresholds, 100)
	}
	sort.Ints(thresholds)
	return Budget{
		Category:        category,
		LimitMinor:      budget.LimitMinor,
		Currency:        currency,
		NotifyAtPercent: thresholds,
	}
}

// Compute maps category spending onto configured budgets for now's calendar
// month. The returned order matches the configured budget order.
func Compute(spending []storage.CategorySpending, budgets []Budget, now time.Time) []Status {
	now = now.UTC()
	byKey := make(map[string]int64, len(spending))
	for _, item := range spending {
		if isExcludedCategory(item.Category) {
			continue
		}
		category := canonicalSpendingCategory(item.Category)
		currency := strings.ToUpper(strings.TrimSpace(item.Currency))
		if currency == "" {
			currency = "EUR"
		}
		byKey[strings.ToLower(category)+"\x00"+currency] += item.Minor
	}
	elapsed := MonthElapsedPercent(now)
	out := make([]Status, 0, len(budgets))
	for _, configured := range budgets {
		budget := Normalize(configured)
		if budget.LimitMinor <= 0 {
			continue
		}
		spent := byKey[strings.ToLower(canonicalSpendingCategory(budget.Category))+"\x00"+budget.Currency]
		percent := spent * 100 / budget.LimitMinor
		tone := ToneHealthy
		switch {
		case percent >= 100:
			tone = ToneCritical
		case percent >= 80:
			tone = ToneWarning
		}
		out = append(out, Status{
			Category:            budget.Category,
			Currency:            budget.Currency,
			LimitMinor:          budget.LimitMinor,
			SpentMinor:          spent,
			RemainingMinor:      budget.LimitMinor - spent,
			Percent:             percent,
			BarWidth:            min(percent, 100),
			Tone:                tone,
			MonthElapsedPercent: elapsed,
		})
	}
	return out
}

// MonthElapsedPercent reports how much of the calendar month has elapsed.
func MonthElapsedPercent(now time.Time) int64 {
	now = now.UTC()
	total := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if total <= 0 {
		return 0
	}
	return int64(now.Day()) * 100 / int64(total)
}

// Description renders a compact status line for notifications.
func (s Status) Description() string {
	return fmt.Sprintf("%s %s of %s %s (%d%%, %d%% of month elapsed)",
		money.FormatGrouped(s.SpentMinor, s.Currency), s.Currency,
		money.FormatGrouped(s.LimitMinor, s.Currency), s.Currency,
		s.Percent, s.MonthElapsedPercent,
	)
}
