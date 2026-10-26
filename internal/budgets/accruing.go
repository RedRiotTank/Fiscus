package budgets

import (
	"sort"
	"strings"
)

const (
	// CategoryAccruingSavings is the budget category for transfers that fund a
	// manual accruing savings account.
	CategoryAccruingSavings = "Savings / Accruing"
	// DefaultAccruingSavingsBPS reserves 5% of income when an accruing account
	// exists and the plan has no explicit savings-transfer category.
	DefaultAccruingSavingsBPS = 500
)

// EnsureAccruingSavingsAllocation adds a Savings-pillar bar for linked accruing
// transfers while keeping the plan total and savings target intact.
func EnsureAccruingSavingsAllocation(profile Profile, allocations []Allocation, enabled bool) []Allocation {
	profile = NormalizeProfile(profile)
	out := NormalizeAllocations(allocations)
	if !enabled || profile.Workspace == "business" {
		return out
	}
	for index := range out {
		if strings.EqualFold(strings.TrimSpace(out[index].Category), CategoryAccruingSavings) {
			out[index].Pillar = PillarSavings
			return NormalizeAllocations(out)
		}
	}
	totalBPS, savingsBPS := allocationTotalsByPillar(out, PillarSavings)
	amount := DefaultAccruingSavingsBPS
	roomTotal := 10000 - totalBPS
	roomSavings := profile.SavingsBPS - savingsBPS
	if savingsBPS >= amount && (roomTotal < amount || roomSavings < amount) {
		reducePillarCategories(out, PillarSavings, amount)
		totalBPS, savingsBPS = allocationTotalsByPillar(out, PillarSavings)
		roomTotal = 10000 - totalBPS
		roomSavings = profile.SavingsBPS - savingsBPS
	}
	if roomTotal < amount {
		amount = roomTotal
	}
	if roomSavings < amount {
		amount = roomSavings
	}
	if amount < 0 {
		amount = 0
	}
	sortOrder := 0
	for _, allocation := range out {
		if allocation.SortOrder > sortOrder {
			sortOrder = allocation.SortOrder
		}
	}
	out = append(out, Allocation{
		Category: CategoryAccruingSavings, Pillar: PillarSavings, PercentBPS: amount,
		NotifyAtPercent: []int{80, 100}, SortOrder: sortOrder + 1,
	})
	return NormalizeAllocations(out)
}

func allocationTotalsByPillar(allocations []Allocation, pillar Pillar) (total, pillarTotal int) {
	for _, allocation := range allocations {
		total += allocation.PercentBPS
		if allocation.Pillar == pillar {
			pillarTotal += allocation.PercentBPS
		}
	}
	return total, pillarTotal
}

func reducePillarCategories(allocations []Allocation, pillar Pillar, amount int) {
	indices := make([]int, 0, len(allocations))
	for index, allocation := range allocations {
		if allocation.Pillar == pillar {
			indices = append(indices, index)
		}
	}
	sort.SliceStable(indices, func(i, j int) bool {
		left, right := allocations[indices[i]], allocations[indices[j]]
		if left.PercentBPS != right.PercentBPS {
			return left.PercentBPS > right.PercentBPS
		}
		return left.Category < right.Category
	})
	for _, index := range indices {
		if amount <= 0 {
			break
		}
		take := allocations[index].PercentBPS
		if take > amount {
			take = amount
		}
		allocations[index].PercentBPS -= take
		amount -= take
	}
}
