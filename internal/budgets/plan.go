package budgets

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/money"
	"github.com/RedRiotTank/Fiscus/internal/storage"
	"github.com/RedRiotTank/Fiscus/pkg/housing"
)

// Pillar is one macro allocation bucket.
type Pillar string

const (
	// PillarNeeds is fixed and essential spending.
	PillarNeeds Pillar = "needs"
	// PillarWants is discretionary and lifestyle spending.
	PillarWants Pillar = "wants"
	// PillarSavings is savings, investments, and future capital.
	PillarSavings Pillar = "savings"
)

const (
	// MessageWarning marks a budget advisory warning.
	MessageWarning = "warning"
	// MessageSuccess marks positive reinforcement.
	MessageSuccess = "success"
	// MessageInfo marks contextual information.
	MessageInfo = "info"
)

// Profile stores income-allocation targets for one workspace and currency.
// Percentages are basis points (10000 = 100%).
type Profile struct {
	Workspace       string
	Currency        string
	NeedsBPS        int
	WantsBPS        int
	SavingsBPS      int
	AdvisoryEnabled bool
}

// Allocation assigns a category to a pillar with a share of income.
type Allocation struct {
	Category        string
	Pillar          Pillar
	PercentBPS      int
	NotifyAtPercent []int
	SortOrder       int
	Custom          bool
}

// CategoryPlan is the calculated status for one category allocation.
type CategoryPlan struct {
	Category        string
	Pillar          Pillar
	PercentBPS      int
	LimitMinor      int64
	SpentMinor      int64
	RemainingMinor  int64
	Percent         int64
	Tone            string
	NotifyAtPercent []int
	Custom          bool
}

// PillarPlan aggregates categories into a macro pillar.
type PillarPlan struct {
	Pillar         Pillar
	Label          string
	TargetBPS      int
	AllocatedBPS   int
	TargetMinor    int64
	AllocatedMinor int64
	SpentMinor     int64
	RemainingMinor int64
	Percent        int64
	Tone           string
	AllocationTone string
	Categories     []CategoryPlan
}

// AdvisoryMessage is a deep, contextual budget observation.
type AdvisoryMessage struct {
	Severity string
	Title    string
	Body     string
}

// Plan is a complete income-driven budget view for one workspace/currency.
type Plan struct {
	Workspace         string
	Currency          string
	BaselineMinor     int64
	TaxProvisionMinor int64
	NetAfterTaxMinor  int64
	Profile           Profile
	Categories        []CategoryPlan
	Pillars           []PillarPlan
	AllocatedBPS      int
	AllocatedMinor    int64
	TotalSpentMinor   int64
	UnallocatedBPS    int
	UnallocatedMinor  int64
	AdvisoryEnabled   bool
	Messages          []AdvisoryMessage
}

// DefaultProfile returns the starter pillar split for a workspace.
func DefaultProfile(workspace, currency string) Profile {
	workspace = strings.ToLower(strings.TrimSpace(workspace))
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = "EUR"
	}
	profile := Profile{
		Workspace: workspace, Currency: currency,
		NeedsBPS: 5000, WantsBPS: 3000, SavingsBPS: 2000,
		AdvisoryEnabled: true,
	}
	if workspace == "business" {
		profile.NeedsBPS, profile.WantsBPS, profile.SavingsBPS = 2800, 0, 2100
	}
	return profile
}

// DefaultAllocations returns the zero-config allocation template for a
// workspace. Personal uses a 50/30/20 split; Business uses operating and tax
// reserve categories only.
func DefaultAllocations(workspace string) []Allocation {
	workspace = strings.ToLower(strings.TrimSpace(workspace))
	if workspace == "business" {
		return []Allocation{
			{Category: "Software & Cloud", Pillar: PillarNeeds, PercentBPS: 800, NotifyAtPercent: []int{80, 100}, SortOrder: 0},
			{Category: "Gestoría / Legal", Pillar: PillarNeeds, PercentBPS: 400, NotifyAtPercent: []int{80, 100}, SortOrder: 1},
			{Category: "Office & Equipment", Pillar: PillarNeeds, PercentBPS: 600, NotifyAtPercent: []int{80, 100}, SortOrder: 2},
			{Category: "Freelance Subcontractors", Pillar: PillarNeeds, PercentBPS: 1000, NotifyAtPercent: []int{80, 100}, SortOrder: 3},
			{Category: "Tax Reserve", Pillar: PillarSavings, PercentBPS: 2100, NotifyAtPercent: []int{80, 100}, SortOrder: 4},
		}
	}
	return []Allocation{
		{Category: housing.Category, Pillar: PillarNeeds, PercentBPS: 3000, NotifyAtPercent: []int{80, 100}, SortOrder: 0},
		{Category: "Groceries", Pillar: PillarNeeds, PercentBPS: 1200, NotifyAtPercent: []int{80, 100}, SortOrder: 1},
		{Category: "Utilities & Telecom", Pillar: PillarNeeds, PercentBPS: 800, NotifyAtPercent: []int{80, 100}, SortOrder: 2},
		{Category: "Restaurants & Delivery", Pillar: PillarWants, PercentBPS: 1200, NotifyAtPercent: []int{80, 100}, SortOrder: 3},
		{Category: "Subscriptions", Pillar: PillarWants, PercentBPS: 500, NotifyAtPercent: []int{80, 100}, SortOrder: 4},
		{Category: "Cash / ATM", Pillar: PillarWants, PercentBPS: 300, NotifyAtPercent: []int{80, 100}, SortOrder: 5},
		{Category: "Leisure & Shopping", Pillar: PillarWants, PercentBPS: 1000, NotifyAtPercent: []int{80, 100}, SortOrder: 6},
		{Category: "Emergency Fund", Pillar: PillarSavings, PercentBPS: 2000, NotifyAtPercent: []int{80, 100}, SortOrder: 7},
	}
}

// NormalizeProfile validates and normalizes pillar targets.
func NormalizeProfile(profile Profile) Profile {
	profile.Workspace = strings.ToLower(strings.TrimSpace(profile.Workspace))
	profile.Currency = strings.ToUpper(strings.TrimSpace(profile.Currency))
	if profile.Currency == "" {
		profile.Currency = "EUR"
	}
	if profile.NeedsBPS < 0 {
		profile.NeedsBPS = 0
	}
	if profile.WantsBPS < 0 {
		profile.WantsBPS = 0
	}
	if profile.SavingsBPS < 0 {
		profile.SavingsBPS = 0
	}
	if profile.NeedsBPS+profile.WantsBPS+profile.SavingsBPS > 10000 {
		profile.NeedsBPS, profile.WantsBPS, profile.SavingsBPS = 5000, 3000, 2000
	}
	return profile
}

// NormalizeAllocations trims names and validates pillar membership.
func NormalizeAllocations(allocations []Allocation) []Allocation {
	out := make([]Allocation, 0, len(allocations))
	for index, allocation := range allocations {
		category := strings.TrimSpace(allocation.Category)
		if category == "" {
			category = "Uncategorized"
		}
		pillar := Pillar(strings.ToLower(strings.TrimSpace(string(allocation.Pillar))))
		switch pillar {
		case PillarNeeds, PillarWants, PillarSavings:
		default:
			pillar = PillarNeeds
		}
		percent := allocation.PercentBPS
		if percent < 0 {
			percent = 0
		}
		sortOrder := allocation.SortOrder
		if sortOrder == 0 {
			sortOrder = index
		}
		out = append(out, Allocation{
			Category: category, Pillar: pillar, PercentBPS: percent,
			NotifyAtPercent: allocation.NotifyAtPercent, SortOrder: sortOrder,
			Custom: allocation.Custom,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		return out[i].Category < out[j].Category
	})
	return out
}

// BuildPlan calculates category and pillar consumption from a monthly income
// baseline. Use gross operating revenue for business and net income for
// personal; taxProvision and netAfterTax are retained for display.
func BuildPlan(profile Profile, allocations []Allocation, spending []storage.CategorySpending, baseline, taxProvision, netAfterTax int64, now time.Time) Plan {
	profile = NormalizeProfile(profile)
	allocations = NormalizeAllocations(allocations)
	plan := Plan{
		Workspace: profile.Workspace, Currency: profile.Currency,
		BaselineMinor: baseline, TaxProvisionMinor: taxProvision, NetAfterTaxMinor: netAfterTax,
		Profile: profile, AdvisoryEnabled: profile.AdvisoryEnabled,
	}
	if baseline < 0 {
		baseline = 0
	}
	byCategory := make(map[string]int64, len(spending))
	for _, item := range spending {
		category := strings.ToLower(canonicalSpendingCategory(item.Category))
		currency := strings.ToUpper(strings.TrimSpace(item.Currency))
		if currency == "" {
			currency = "EUR"
		}
		if currency != profile.Currency {
			continue
		}
		byCategory[category] += item.Minor
	}
	pillarCategories := map[Pillar][]CategoryPlan{
		PillarNeeds: {}, PillarWants: {}, PillarSavings: {},
	}
	for _, allocation := range allocations {
		limit := roundBudgetMinor(baseline * int64(allocation.PercentBPS) / 10000)
		spent := byCategory[strings.ToLower(canonicalSpendingCategory(allocation.Category))]
		percent := int64(0)
		if limit > 0 {
			percent = spent * 100 / limit
		} else if spent > 0 {
			percent = 100
		}
		remaining := limit - spent
		tone := ToneHealthy
		switch {
		case percent > 100:
			tone = ToneCritical
		case percent >= 80:
			tone = ToneWarning
		}
		if allocation.Pillar == PillarSavings && percent >= 100 {
			tone = ToneHealthy
		}
		category := CategoryPlan{
			Category: allocation.Category, Pillar: allocation.Pillar,
			PercentBPS: allocation.PercentBPS, LimitMinor: limit, SpentMinor: spent,
			RemainingMinor: remaining, Percent: percent, Tone: tone,
			NotifyAtPercent: allocation.NotifyAtPercent, Custom: allocation.Custom,
		}
		plan.Categories = append(plan.Categories, category)
		pillarCategories[allocation.Pillar] = append(pillarCategories[allocation.Pillar], category)
		plan.AllocatedBPS += allocation.PercentBPS
		plan.AllocatedMinor += limit
		plan.TotalSpentMinor += spent
	}
	plan.UnallocatedBPS = 10000 - plan.AllocatedBPS
	plan.UnallocatedMinor = baseline - plan.AllocatedMinor

	for _, pillar := range []Pillar{PillarNeeds, PillarWants, PillarSavings} {
		targetBPS := profile.NeedsBPS
		switch pillar {
		case PillarWants:
			targetBPS = profile.WantsBPS
		case PillarSavings:
			targetBPS = profile.SavingsBPS
		}
		pillarPlan := PillarPlan{
			Pillar: pillar, Label: PillarLabel(pillar), TargetBPS: targetBPS,
			TargetMinor: baseline * int64(targetBPS) / 10000,
			Categories:  pillarCategories[pillar],
		}
		for _, category := range pillarPlan.Categories {
			pillarPlan.AllocatedBPS += category.PercentBPS
			pillarPlan.AllocatedMinor += category.LimitMinor
			pillarPlan.SpentMinor += category.SpentMinor
		}
		pillarPlan.RemainingMinor = pillarPlan.AllocatedMinor - pillarPlan.SpentMinor
		if pillarPlan.AllocatedMinor > 0 {
			pillarPlan.Percent = pillarPlan.SpentMinor * 100 / pillarPlan.AllocatedMinor
		}
		pillarPlan.Tone = ToneHealthy
		switch {
		case pillarPlan.Percent > 100:
			pillarPlan.Tone = ToneCritical
		case pillarPlan.Percent >= 80:
			pillarPlan.Tone = ToneWarning
		}
		if pillar == PillarSavings && pillarPlan.Percent >= 100 {
			pillarPlan.Tone = ToneHealthy
		}
		switch {
		case targetBPS > 0 && pillarPlan.AllocatedBPS > targetBPS:
			pillarPlan.AllocationTone = "over-target"
		case targetBPS > 0 && pillarPlan.AllocatedBPS < targetBPS:
			pillarPlan.AllocationTone = "under-target"
		case targetBPS == 0 && pillarPlan.AllocatedBPS > 0:
			pillarPlan.AllocationTone = "over-target"
		default:
			pillarPlan.AllocationTone = "on-target"
		}
		plan.Pillars = append(plan.Pillars, pillarPlan)
	}
	if profile.AdvisoryEnabled {
		plan.Messages = buildAdvisoryMessages(profile, plan.Categories, baseline, plan.AllocatedBPS, now)
	}
	return plan
}

// PillarLabel returns the display label for a pillar.
func PillarLabel(pillar Pillar) string {
	switch pillar {
	case PillarNeeds:
		return "Needs"
	case PillarWants:
		return "Wants"
	case PillarSavings:
		return "Savings & Investment"
	default:
		return string(pillar)
	}
}

func buildAdvisoryMessages(profile Profile, categories []CategoryPlan, baseline int64, allocatedBPS int, _ time.Time) []AdvisoryMessage {
	if baseline <= 0 {
		return []AdvisoryMessage{{
			Severity: MessageInfo,
			Title:    "Income baseline needed",
			Body:     "Add confirmed recurring inflows before advisory benchmarks can be calculated.",
		}}
	}
	var messages []AdvisoryMessage
	housingBPS, housingMinor, housingActual := benchmarkBPS(categories, baseline,
		"rent", "mortgage", "housing")
	if housingBPS > 3500 {
		messages = append(messages, AdvisoryMessage{
			Severity: MessageWarning,
			Title:    "Housing exceeds the healthy ceiling",
			Body: fmt.Sprintf("Housing %s %s of your net income (%s), exceeding the recommended 30%%–35%% ceiling.",
				benchmarkVerb(housingActual), formatBPS(housingBPS),
				money.FormatGrouped(housingMinor, profile.Currency)),
		})
	}
	groceryBPS, groceryMinor, groceryActual := benchmarkBPS(categories, baseline, "groceries", "supermarket", "supermercado")
	if groceryBPS > 1200 {
		messages = append(messages, AdvisoryMessage{
			Severity: MessageWarning,
			Title:    "Groceries are above the healthy range",
			Body: fmt.Sprintf("Groceries %s %s of net income (%s), above the recommended 10%%–12%% range.",
				benchmarkVerbPlural(groceryActual), formatBPS(groceryBPS),
				money.FormatGrouped(groceryMinor, profile.Currency)),
		})
	}
	utilityBPS, utilityMinor, utilityActual := benchmarkBPS(categories, baseline, "utilities", "utility", "electricity", "gas", "water", "telecom")
	if utilityBPS > 800 {
		messages = append(messages, AdvisoryMessage{
			Severity: MessageWarning,
			Title:    "Utilities and telecom are above target",
			Body: fmt.Sprintf("Utilities & telecom %s %s of net income (%s), above the recommended 5%%–8%% range.",
				benchmarkVerbPlural(utilityActual), formatBPS(utilityBPS),
				money.FormatGrouped(utilityMinor, profile.Currency)),
		})
	}
	diningBPS, diningMinor, diningActual := benchmarkBPS(categories, baseline, "restaurants", "dining", "delivery", "meals")
	if diningBPS > 1200 {
		messages = append(messages, AdvisoryMessage{
			Severity: MessageWarning,
			Title:    "Dining Out & Delivery is above the lifestyle allowance",
			Body: fmt.Sprintf("Dining Out & Delivery %s %s of your total budget (%s), exceeding the recommended 10%%–12%% lifestyle allowance.",
				benchmarkVerb(diningActual), formatBPS(diningBPS),
				money.FormatGrouped(diningMinor, profile.Currency)),
		})
	}
	if profile.WantsBPS > 3000 || profile.SavingsBPS < 2000 {
		messages = append(messages, AdvisoryMessage{
			Severity: MessageWarning,
			Title:    "Savings rate is compressed",
			Body: fmt.Sprintf("Discretionary spending is set to %s while savings is %s; aim for at least 20%% savings.",
				formatBPS(profile.WantsBPS), formatBPS(profile.SavingsBPS)),
		})
	}
	if profile.SavingsBPS >= 3000 && profile.WantsBPS <= 2000 {
		messages = append(messages, AdvisoryMessage{
			Severity: MessageSuccess,
			Title:    "Excellent allocation",
			Body: fmt.Sprintf("You are directing %s to savings/investments while keeping lifestyle costs at %s. That is %s above the standard guideline.",
				formatBPS(profile.SavingsBPS), formatBPS(profile.WantsBPS),
				formatBPS(profile.SavingsBPS-2000)),
		})
	}
	if allocatedBPS > 10000 {
		messages = append(messages, AdvisoryMessage{
			Severity: MessageWarning,
			Title:    "Income is over-allocated",
			Body: fmt.Sprintf("Your categories allocate %s of monthly income. Reduce or remove allocations to create a buffer.",
				formatBPS(allocatedBPS)),
		})
	}
	return messages
}

func benchmarkBPS(categories []CategoryPlan, baseline int64, names ...string) (int, int64, bool) {
	var budgetBPS int
	var limitMinor int64
	var spentMinor int64
	for _, category := range categories {
		lower := strings.ToLower(category.Category)
		for _, name := range names {
			if strings.Contains(lower, name) {
				budgetBPS += category.PercentBPS
				limitMinor += category.LimitMinor
				spentMinor += category.SpentMinor
				break
			}
		}
	}
	actualBPS := 0
	if baseline > 0 {
		actualBPS = int(spentMinor * 10000 / baseline)
	}
	if actualBPS > budgetBPS {
		return actualBPS, spentMinor, true
	}
	return budgetBPS, limitMinor, false
}

func benchmarkVerb(actual bool) string {
	if actual {
		return "spending is at"
	}
	return "is allocated"
}

func benchmarkVerbPlural(actual bool) string {
	if actual {
		return "spending is at"
	}
	return "are allocated"
}

func formatBPS(bps int) string {
	if bps%100 == 0 {
		return fmt.Sprintf("%d%%", bps/100)
	}
	return fmt.Sprintf("%.1f%%", float64(bps)/100)
}

func roundBudgetMinor(amount int64) int64 {
	if amount == 0 {
		return 0
	}
	if amount < 0 {
		return -(((-amount + 50) / 100) * 100)
	}
	return ((amount + 50) / 100) * 100
}
