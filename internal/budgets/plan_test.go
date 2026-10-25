package budgets

import (
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/housing"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

func testSpending() []storage.CategorySpending {
	return []storage.CategorySpending{
		{Category: "Rent", Currency: "EUR", Minor: 85_000},
		{Category: "Groceries", Currency: "EUR", Minor: 26_250},
		{Category: "Utilities & Telecom", Currency: "EUR", Minor: 17_429},
		{Category: "Restaurants & Delivery", Currency: "EUR", Minor: 34_000},
		{Category: "Subscriptions", Currency: "EUR", Minor: 7_072},
		{Category: "Leisure & Shopping", Currency: "EUR", Minor: 30_000},
		{Category: "Emergency Fund", Currency: "EUR", Minor: 30_000},
		{Category: "Investments", Currency: "EUR", Minor: 26_000},
	}
}

func personalProfile() Profile {
	return Profile{
		Workspace: "personal", Currency: "EUR",
		NeedsBPS: 5000, WantsBPS: 3000, SavingsBPS: 2000,
		AdvisoryEnabled: true,
	}
}

func personalAllocations() []Allocation {
	return []Allocation{
		{Category: "Rent", Pillar: PillarNeeds, PercentBPS: 3036, SortOrder: 0},
		{Category: "Groceries", Pillar: PillarNeeds, PercentBPS: 1250, SortOrder: 1},
		{Category: "Utilities & Telecom", Pillar: PillarNeeds, PercentBPS: 714, SortOrder: 2},
		{Category: "Restaurants & Delivery", Pillar: PillarWants, PercentBPS: 1071, SortOrder: 3},
		{Category: "Subscriptions", Pillar: PillarWants, PercentBPS: 321, SortOrder: 4},
		{Category: "Leisure & Shopping", Pillar: PillarWants, PercentBPS: 1608, SortOrder: 5},
		{Category: "Emergency Fund", Pillar: PillarSavings, PercentBPS: 1071, SortOrder: 6},
		{Category: "Investments", Pillar: PillarSavings, PercentBPS: 929, SortOrder: 7},
	}
}

func TestBuildPlanPersonalIncomeDriven(t *testing.T) {
	t.Parallel()
	plan := BuildPlan(personalProfile(), personalAllocations(), testSpending(), 280_000, 0, 280_000, time.Now())
	if plan.BaselineMinor != 280_000 || plan.AllocatedBPS != 10000 ||
		plan.AllocatedMinor != 280_000 || plan.UnallocatedBPS != 0 || plan.UnallocatedMinor != 0 {
		t.Fatalf("plan allocation = %+v", plan)
	}
	if len(plan.Categories) != 8 || len(plan.Pillars) != 3 {
		t.Fatalf("plan shape = categories %d pillars %d", len(plan.Categories), len(plan.Pillars))
	}
	needs := plan.Pillars[0]
	if needs.Label != "Needs" || needs.TargetMinor != 140_000 || needs.AllocatedMinor != 140_000 ||
		needs.AllocatedBPS != 5000 {
		t.Fatalf("needs pillar = %+v", needs)
	}
	wants := plan.Pillars[1]
	if wants.TargetMinor != 84_000 || wants.AllocatedMinor != 84_000 || wants.AllocatedBPS != 3000 {
		t.Fatalf("wants pillar = %+v", wants)
	}
	savings := plan.Pillars[2]
	if savings.TargetMinor != 56_000 || savings.AllocatedMinor != 56_000 || savings.AllocatedBPS != 2000 {
		t.Fatalf("savings pillar = %+v", savings)
	}
	if !hasMessage(plan.Messages, "Dining Out & Delivery is above the lifestyle allowance") {
		t.Fatalf("expected dining advisory, got %+v", plan.Messages)
	}
}

func TestBuildPlanBusinessWorkspaceIsolation(t *testing.T) {
	t.Parallel()
	profile := Profile{
		Workspace: "business", Currency: "EUR",
		NeedsBPS: 2100, WantsBPS: 0, SavingsBPS: 2100,
		AdvisoryEnabled: true,
	}
	allocations := []Allocation{
		{Category: "Software & Cloud", Pillar: PillarNeeds, PercentBPS: 818, SortOrder: 0},
		{Category: "Gestoría / Legal", Pillar: PillarNeeds, PercentBPS: 364, SortOrder: 1},
		{Category: "Office & Equipment", Pillar: PillarNeeds, PercentBPS: 545, SortOrder: 2},
		{Category: "Tax Reserve", Pillar: PillarSavings, PercentBPS: 2100, SortOrder: 3},
	}
	spending := []storage.CategorySpending{
		{Category: "Software & Cloud", Currency: "EUR", Minor: 45_000},
		{Category: "Gestoría / Legal", Currency: "EUR", Minor: 20_000},
		{Category: "Office & Equipment", Currency: "EUR", Minor: 30_000},
		{Category: "Groceries", Currency: "EUR", Minor: 99_000},
	}
	plan := BuildPlan(profile, allocations, spending, 550_000, 115_500, 434_500, time.Now())
	if plan.Workspace != "business" || plan.BaselineMinor != 550_000 ||
		plan.AllocatedBPS != 3827 || plan.AllocatedMinor != 210_500 ||
		plan.UnallocatedBPS != 6173 || plan.UnallocatedMinor != 339_500 {
		t.Fatalf("business plan = %+v", plan)
	}
	if len(plan.Categories) != 4 {
		t.Fatalf("business categories = %+v", plan.Categories)
	}
	for _, category := range plan.Categories {
		if category.Category == "Groceries" {
			t.Fatal("personal category leaked into business plan")
		}
	}
	if plan.Pillars[0].AllocatedMinor != 95_000 || plan.Pillars[2].AllocatedMinor != 115_500 {
		t.Fatalf("business pillars = %+v", plan.Pillars)
	}
}

func TestBuildPlanPositiveAndOverAllocatedMessages(t *testing.T) {
	t.Parallel()
	profile := personalProfile()
	profile.WantsBPS, profile.SavingsBPS = 2000, 3000
	profile.NeedsBPS = 5000
	plan := BuildPlan(profile, []Allocation{
		{Category: "Savings", Pillar: PillarSavings, PercentBPS: 3000},
		{Category: "Lifestyle", Pillar: PillarWants, PercentBPS: 2000},
	}, nil, 280_000, 0, 280_000, time.Now())
	if !hasMessage(plan.Messages, "Excellent allocation") {
		t.Fatalf("expected positive message, got %+v", plan.Messages)
	}
	profile.WantsBPS, profile.SavingsBPS = 4000, 1000
	profile.NeedsBPS = 5000
	plan = BuildPlan(profile, []Allocation{
		{Category: "Lifestyle", Pillar: PillarWants, PercentBPS: 4000},
		{Category: "Savings", Pillar: PillarSavings, PercentBPS: 1000},
	}, nil, 280_000, 0, 280_000, time.Now())
	if !hasMessage(plan.Messages, "Savings rate is compressed") {
		t.Fatalf("expected savings warning, got %+v", plan.Messages)
	}
	if !hasMessage(plan.Messages, "Income is over-allocated") {
		// 5000 + 4000 + 1000 = 10000, so make one category allocation exceed the target.
		plan = BuildPlan(profile, []Allocation{
			{Category: "Rent", Pillar: PillarNeeds, PercentBPS: 6000},
			{Category: "Lifestyle", Pillar: PillarWants, PercentBPS: 4000},
			{Category: "Savings", Pillar: PillarSavings, PercentBPS: 2000},
		}, nil, 280_000, 0, 280_000, time.Now())
	}
	if !hasMessage(plan.Messages, "Income is over-allocated") {
		t.Fatalf("expected over-allocation warning, got %+v", plan.Messages)
	}
}

func TestBuildPlanHousingSubCap(t *testing.T) {
	t.Parallel()
	plan := BuildPlan(personalProfile(), []Allocation{
		{Category: "Rent", Pillar: PillarNeeds, PercentBPS: 4000},
	}, nil, 280_000, 0, 280_000, time.Now())
	if !hasMessage(plan.Messages, "Housing exceeds the healthy ceiling") {
		t.Fatalf("expected housing warning, got %+v", plan.Messages)
	}
}

func TestDefaultProfileAndNoIncomeMessage(t *testing.T) {
	t.Parallel()
	personal := DefaultProfile("personal", "eur")
	if personal.NeedsBPS != 5000 || personal.WantsBPS != 3000 || personal.SavingsBPS != 2000 {
		t.Fatalf("personal default = %+v", personal)
	}
	business := DefaultProfile("business", "EUR")
	if business.NeedsBPS != 2800 || business.WantsBPS != 0 || business.SavingsBPS != 2100 {
		t.Fatalf("business default = %+v", business)
	}
	plan := BuildPlan(personal, nil, nil, 0, 0, 0, time.Now())
	if !hasMessage(plan.Messages, "Income baseline needed") {
		t.Fatalf("expected missing income message, got %+v", plan.Messages)
	}
}

func TestCategoryReassignmentChangesPillar(t *testing.T) {
	t.Parallel()
	allocations := []Allocation{
		{Category: "Restaurants", Pillar: PillarWants, PercentBPS: 1000},
	}
	plan := BuildPlan(personalProfile(), allocations, nil, 280_000, 0, 280_000, time.Now())
	if plan.Pillars[1].AllocatedBPS != 1000 || plan.Pillars[0].AllocatedBPS != 0 {
		t.Fatalf("wants allocation = %+v", plan.Pillars)
	}
	allocations[0].Pillar = PillarNeeds
	plan = BuildPlan(personalProfile(), allocations, nil, 280_000, 0, 280_000, time.Now())
	if plan.Pillars[0].AllocatedBPS != 1000 || plan.Pillars[1].AllocatedBPS != 0 {
		t.Fatalf("needs allocation = %+v", plan.Pillars)
	}
}

func hasMessage(messages []AdvisoryMessage, contains string) bool {
	for _, message := range messages {
		if strings.Contains(message.Title, contains) || strings.Contains(message.Body, contains) {
			return true
		}
	}
	return false
}

func TestDefaultAllocations(t *testing.T) {
	t.Parallel()
	personal := DefaultAllocations("personal")
	if len(personal) != 7 {
		t.Fatalf("personal defaults = %+v", personal)
	}
	if personal[0].Category != housing.Category {
		t.Fatalf("personal housing category = %q, want %q", personal[0].Category, housing.Category)
	}
	var total int
	for _, allocation := range personal {
		total += allocation.PercentBPS
	}
	if total != 10000 {
		t.Fatalf("personal default total = %d", total)
	}
	business := DefaultAllocations("business")
	if len(business) != 5 {
		t.Fatalf("business defaults = %+v", business)
	}
	for _, allocation := range business {
		switch allocation.Category {
		case housing.Category, "Groceries", "Restaurants & Delivery", "Leisure & Shopping":
			t.Fatalf("personal category in business defaults: %+v", allocation)
		}
	}
	profile := DefaultProfile("business", "EUR")
	if profile.NeedsBPS != 2800 || profile.SavingsBPS != 2100 {
		t.Fatalf("business default profile = %+v", profile)
	}
}
