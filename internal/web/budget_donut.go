package web

import (
	"encoding/json"

	"github.com/RedRiotTank/Fiscus/internal/budgets"
)

// BudgetDonutView carries the serialized dual-ring chart data.
type BudgetDonutView struct {
	Data string
}

type budgetDonutPayload struct {
	Currency string   `json:"currency"`
	Labels   []string `json:"labels"`
	Budgets  []int64  `json:"budgets"`
	Spent    []int64  `json:"spent"`
	Colors   []string `json:"colors"`
}

func buildBudgetDonut(plan budgets.Plan) *BudgetDonutView {
	if plan.BaselineMinor <= 0 || len(plan.Categories) == 0 {
		return nil
	}
	payload := budgetDonutPayload{Currency: plan.Currency}
	for _, category := range plan.Categories {
		payload.Labels = append(payload.Labels, category.Category)
		payload.Budgets = append(payload.Budgets, category.LimitMinor)
		payload.Spent = append(payload.Spent, category.SpentMinor)
		payload.Colors = append(payload.Colors, categoryColor(category.Category))
	}
	if plan.UnallocatedMinor > 0 {
		payload.Labels = append(payload.Labels, "Unallocated")
		payload.Budgets = append(payload.Budgets, plan.UnallocatedMinor)
		payload.Spent = append(payload.Spent, 0)
		payload.Colors = append(payload.Colors, "#475569")
	}
	if len(payload.Labels) == 0 {
		return nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return &BudgetDonutView{Data: string(data)}
}
