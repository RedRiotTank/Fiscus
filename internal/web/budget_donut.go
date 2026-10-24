package web

import (
	"encoding/json"

	"github.com/RedRiotTank/Fiscus/internal/budgets"
)

// BudgetDonutSlice is one category in the dual-ring budget chart.
type BudgetDonutSlice struct {
	Category    string
	Currency    string
	BudgetMinor int64
	SpentMinor  int64
	Percent     int64
	Color       string
	Over        bool
}

// BudgetDonutView carries the rendered chart data and legend.
type BudgetDonutView struct {
	Currency         string
	TotalBudgetMinor int64
	TotalSpentMinor  int64
	Percent          int64
	Data             string
	Slices           []BudgetDonutSlice
}

type budgetDonutPayload struct {
	Currency string   `json:"currency"`
	Labels   []string `json:"labels"`
	Budgets  []int64  `json:"budgets"`
	Spent    []int64  `json:"spent"`
	Colors   []string `json:"colors"`
}

func buildBudgetDonut(statuses []budgets.Status) *BudgetDonutView {
	if len(statuses) == 0 {
		return nil
	}
	totals := make(map[string]int64)
	for _, status := range statuses {
		totals[status.Currency] += status.LimitMinor
	}
	currency := ""
	var largest int64
	for candidate, total := range totals {
		if total > largest || (total == largest && (currency == "" || candidate < currency)) {
			currency = candidate
			largest = total
		}
	}
	if currency == "" || largest <= 0 {
		return nil
	}
	view := &BudgetDonutView{Currency: currency}
	payload := budgetDonutPayload{Currency: currency}
	for _, status := range statuses {
		if status.Currency != currency {
			continue
		}
		percent := int64(0)
		if status.LimitMinor > 0 {
			percent = (status.SpentMinor*100 + status.LimitMinor/2) / status.LimitMinor
		}
		view.Slices = append(view.Slices, BudgetDonutSlice{
			Category:    status.Category,
			Currency:    currency,
			BudgetMinor: status.LimitMinor,
			SpentMinor:  status.SpentMinor,
			Percent:     percent,
			Color:       categoryColor(status.Category),
			Over:        status.SpentMinor > status.LimitMinor,
		})
		view.TotalBudgetMinor += status.LimitMinor
		view.TotalSpentMinor += status.SpentMinor
		payload.Labels = append(payload.Labels, status.Category)
		payload.Budgets = append(payload.Budgets, status.LimitMinor)
		payload.Spent = append(payload.Spent, status.SpentMinor)
		payload.Colors = append(payload.Colors, categoryColor(status.Category))
	}
	if view.TotalBudgetMinor <= 0 || len(view.Slices) == 0 {
		return nil
	}
	view.Percent = (view.TotalSpentMinor*100 + view.TotalBudgetMinor/2) / view.TotalBudgetMinor
	data, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	view.Data = string(data)
	return view
}
