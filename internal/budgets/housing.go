package budgets

import (
	"sort"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
	"github.com/RedRiotTank/Fiscus/pkg/housing"
)

// canonicalSpendingCategory maps housing aliases onto one display category so
// rent stored as "Rent", "Housing", or "Housing / Rent" matches the same
// default allocation.
func canonicalSpendingCategory(category string) string {
	if housing.IsCategory(category) {
		return housing.Category
	}
	if IsP2PCategory(category) {
		return CategoryP2P
	}
	category = strings.TrimSpace(category)
	if category == "" {
		return "Uncategorized"
	}
	return category
}

// NetHousingSpending subtracts flatmate reimbursements from gross rent while
// keeping the reimbursement out of the expense total. It also repairs housing
// expenses that live in a non-housing category but contain rent keywords.
func NetHousingSpending(spending []storage.CategorySpending, transactions []domain.Transaction) []storage.CategorySpending {
	return NetHousingSpendingWithRule(spending, transactions, nil)
}

// NetHousingSpendingWithRule applies a persisted shared-flat rule. Fixed-share
// rules report the user's personal share once rent is paid; net-balance rules
// subtract the actual flatmate inflows. A nil rule keeps the generic heuristic.
func NetHousingSpendingWithRule(spending []storage.CategorySpending, transactions []domain.Transaction, rule *domain.HousingRule) []storage.CategorySpending {
	if rule != nil {
		return netHousingWithRule(spending, transactions, *rule)
	}
	return netHousingFallback(spending, transactions)
}

func netHousingWithRule(spending []storage.CategorySpending, transactions []domain.Transaction, rule domain.HousingRule) []storage.CategorySpending {
	currency := currencyOf(rule.Currency)
	existingGross := int64(0)
	kept := make([]storage.CategorySpending, 0, len(spending))
	for _, item := range spending {
		if strings.EqualFold(currencyOf(item.Currency), currency) && housing.IsCategory(item.Category) {
			existingGross += item.Minor
			continue
		}
		kept = append(kept, item)
	}

	masterOutflow := int64(0)
	flatmateInflow := int64(0)
	adjustments := make(map[string]int64)
	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked || !strings.EqualFold(currencyOf(tx.Currency), currency) {
			continue
		}
		if housing.IsMasterPayment(tx, rule) {
			masterOutflow += -tx.AmountMinor
			if !housing.IsCategory(tx.Category) {
				key := canonicalSpendingCategory(tx.Category) + "\x00" + currency
				adjustments[key] += -tx.AmountMinor
			}
		}
		if housing.IsFlatmateContribution(tx, rule) {
			flatmateInflow += tx.AmountMinor
		}
	}
	if len(adjustments) > 0 {
		filtered := kept[:0]
		for _, item := range kept {
			key := canonicalSpendingCategory(item.Category) + "\x00" + currencyOf(item.Currency)
			if adjustment, ok := adjustments[key]; ok {
				item.Minor -= adjustment
				if item.Minor < 0 {
					item.Minor = 0
				}
			}
			if item.Minor != 0 {
				filtered = append(filtered, item)
			}
		}
		kept = filtered
	}
	gross := masterOutflow
	if gross <= 0 {
		gross = existingGross
	}
	net := int64(0)
	switch rule.CalculationMode {
	case domain.HousingModeFixedShare:
		if gross > 0 {
			net = rule.PersonalNetShareCents
		}
	case domain.HousingModeNetBalance:
		net = gross - flatmateInflow
	default:
		net = gross - flatmateInflow
	}
	if net < 0 {
		net = 0
	}
	if net > 0 {
		kept = append(kept, storage.CategorySpending{
			Category: housing.Category, Currency: currency, Minor: net,
		})
	}
	sortSpending(kept)
	return kept
}

func netHousingFallback(spending []storage.CategorySpending, transactions []domain.Transaction) []storage.CategorySpending {
	type key struct {
		category string
		currency string
	}
	amounts := make(map[key]int64, len(spending))
	for _, item := range spending {
		currency := currencyOf(item.Currency)
		category := canonicalSpendingCategory(item.Category)
		amounts[key{category: category, currency: currency}] += item.Minor
	}

	grossHousing := make(map[string]int64)
	for itemKey, amount := range amounts {
		if strings.EqualFold(itemKey.category, housing.Category) {
			grossHousing[itemKey.currency] += amount
			delete(amounts, itemKey)
		}
	}

	// Existing rows may predate the housing category or originate from a
	// transfer labelled "alquiler". Move those outflows into housing.
	adjustments := make(map[key]int64)
	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked || !housing.IsExpense(tx) ||
			housing.IsCategory(tx.Category) {
			continue
		}
		currency := currencyOf(tx.Currency)
		category := canonicalSpendingCategory(tx.Category)
		adjustments[key{category: category, currency: currency}] += -tx.AmountMinor
	}
	for itemKey, amount := range adjustments {
		if _, ok := amounts[itemKey]; !ok {
			continue
		}
		amounts[itemKey] -= amount
		if amounts[itemKey] < 0 {
			amounts[itemKey] = 0
		}
		grossHousing[itemKey.currency] += amount
	}

	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked || !housing.IsReimbursement(tx) {
			continue
		}
		grossHousing[currencyOf(tx.Currency)] -= tx.AmountMinor
	}
	for currency, gross := range grossHousing {
		if gross <= 0 {
			continue
		}
		amounts[key{category: housing.Category, currency: currency}] += gross
	}

	out := make([]storage.CategorySpending, 0, len(amounts))
	for itemKey, amount := range amounts {
		if amount == 0 {
			continue
		}
		out = append(out, storage.CategorySpending{
			Category: itemKey.category, Currency: itemKey.currency, Minor: amount,
		})
	}
	sortSpending(out)
	return out
}

func sortSpending(items []storage.CategorySpending) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Category != items[j].Category {
			return items[i].Category < items[j].Category
		}
		return items[i].Currency < items[j].Currency
	})
}
