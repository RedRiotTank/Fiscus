package web

import (
	"encoding/json"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/budgets"
)

// BudgetDonutView carries serialized macro-donut and grouped-bar chart data.
type BudgetDonutView struct {
	Data string
}

type budgetPillarSlice struct {
	Label     string `json:"label"`
	Allocated int64  `json:"allocated"`
	Spent     int64  `json:"spent"`
	Target    int64  `json:"target"`
	Color     string `json:"color"`
}

type budgetBarCategory struct {
	Label  string `json:"label"`
	Budget int64  `json:"budget"`
	Spent  int64  `json:"spent"`
	Color  string `json:"color"`
	Pillar string `json:"pillar"`
}

type budgetDonutPayload struct {
	Currency         string              `json:"currency"`
	Labels           []string            `json:"labels"`
	Budgets          []int64             `json:"budgets"`
	Spent            []int64             `json:"spent"`
	Colors           []string            `json:"colors"`
	Pillars          []budgetPillarSlice `json:"pillars"`
	Categories       []budgetBarCategory `json:"categories"`
	BaselineMinor    int64               `json:"baseline_minor"`
	AllocatedMinor   int64               `json:"allocated_minor"`
	UnallocatedMinor int64               `json:"unallocated_minor"`
	DayOfMonth       int                 `json:"day_of_month"`
	DaysInMonth      int                 `json:"days_in_month"`
	PacePercent      int                 `json:"pace_percent"`
}

var pillarChartColors = map[budgets.Pillar]string{
	budgets.PillarNeeds:   "#60a5fa",
	budgets.PillarWants:   "#a78bfa",
	budgets.PillarSavings: "#34d399",
}

func buildBudgetDonut(plan budgets.Plan) *BudgetDonutView {
	return buildBudgetDonutAt(plan, time.Now().UTC())
}

func buildBudgetDonutAt(plan budgets.Plan, now time.Time) *BudgetDonutView {
	if plan.BaselineMinor <= 0 || len(plan.Categories) == 0 {
		return nil
	}
	now = now.UTC()
	daysInMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	payload := budgetDonutPayload{
		Currency:         plan.Currency,
		BaselineMinor:    plan.BaselineMinor,
		AllocatedMinor:   plan.AllocatedMinor,
		UnallocatedMinor: plan.UnallocatedMinor,
		DayOfMonth:       now.Day(),
		DaysInMonth:      daysInMonth,
	}
	for _, category := range plan.Categories {
		color := categoryColor(category.Category)
		payload.Labels = append(payload.Labels, category.Category)
		payload.Budgets = append(payload.Budgets, category.LimitMinor)
		payload.Spent = append(payload.Spent, category.SpentMinor)
		payload.Colors = append(payload.Colors, color)
		payload.Categories = append(payload.Categories, budgetBarCategory{
			Label: category.Category, Budget: category.LimitMinor,
			Spent: category.SpentMinor, Color: color, Pillar: string(category.Pillar),
		})
	}
	if plan.UnallocatedMinor > 0 {
		payload.Labels = append(payload.Labels, "Unallocated")
		payload.Budgets = append(payload.Budgets, plan.UnallocatedMinor)
		payload.Spent = append(payload.Spent, 0)
		payload.Colors = append(payload.Colors, "#475569")
	}
	for _, pillar := range plan.Pillars {
		color := pillarChartColors[pillar.Pillar]
		if color == "" {
			color = categoryColor(string(pillar.Pillar))
		}
		payload.Pillars = append(payload.Pillars, budgetPillarSlice{
			Label: pillar.Label, Allocated: pillar.AllocatedMinor,
			Spent: pillar.SpentMinor, Target: pillar.TargetMinor, Color: color,
		})
	}
	if len(payload.Pillars) == 0 {
		// Keep a valid macro ring even when callers build a raw plan without
		// pre-aggregated pillars.
		for _, pillar := range []budgets.Pillar{budgets.PillarNeeds, budgets.PillarWants, budgets.PillarSavings} {
			payload.Pillars = append(payload.Pillars, budgetPillarSlice{
				Label: budgets.PillarLabel(pillar), Color: pillarChartColors[pillar],
			})
		}
	}
	if daysInMonth > 0 {
		payload.PacePercent = now.Day() * 100 / daysInMonth
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
