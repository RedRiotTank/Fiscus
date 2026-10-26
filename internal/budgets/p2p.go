package budgets

import (
	"sort"
	"strings"
)

const (
	// CategoryP2P is the dedicated budget category for unlinked peer-to-peer
	// and Bizum transfers.
	CategoryP2P = "Transfers / P2P"
	// DefaultP2PWantsBPS reserves 3% of monthly income for casual transfers.
	DefaultP2PWantsBPS = 300
)

// IsP2PCategory reports whether a category name represents a peer-to-peer or
// Bizum transfer. Historical imports used several aliases.
func IsP2PCategory(category string) bool {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "transfers / p2p", "transfers/p2p", "p2p",
		"transfers / bizum", "transfers/bizum", "bizum":
		return true
	default:
		return false
	}
}

// EnsureP2PWantsAllocation guarantees that a personal plan exposes the
// Transfers / P2P bar under Wants. Missing allocations are carved out of other
// Wants categories when the plan is already fully allocated, so the 30% Wants
// envelope and the 100% total remain intact.
func EnsureP2PWantsAllocation(profile Profile, allocations []Allocation) []Allocation {
	profile = NormalizeProfile(profile)
	out := NormalizeAllocations(allocations)
	if profile.Workspace == "business" {
		return out
	}
	for index := range out {
		if IsP2PCategory(out[index].Category) {
			out[index].Category = CategoryP2P
			out[index].Pillar = PillarWants
			return NormalizeAllocations(out)
		}
	}

	totalBPS, wantsBPS := allocationTotals(out)
	amount := DefaultP2PWantsBPS
	roomTotal := 10000 - totalBPS
	roomWants := profile.WantsBPS - wantsBPS
	if wantsBPS >= amount && (roomTotal < amount || roomWants < amount) {
		reduceWantsCategories(out, amount)
		totalBPS, wantsBPS = allocationTotals(out)
		roomTotal = 10000 - totalBPS
		roomWants = profile.WantsBPS - wantsBPS
	}
	if roomTotal < amount {
		amount = roomTotal
	}
	if roomWants < amount {
		amount = roomWants
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
		Category: CategoryP2P, Pillar: PillarWants, PercentBPS: amount,
		NotifyAtPercent: []int{80, 100}, SortOrder: sortOrder + 1,
	})
	return NormalizeAllocations(out)
}

func allocationTotals(allocations []Allocation) (total, wants int) {
	for _, allocation := range allocations {
		total += allocation.PercentBPS
		if allocation.Pillar == PillarWants {
			wants += allocation.PercentBPS
		}
	}
	return total, wants
}

func reduceWantsCategories(allocations []Allocation, amount int) {
	indices := make([]int, 0, len(allocations))
	for index, allocation := range allocations {
		if allocation.Pillar == PillarWants {
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
