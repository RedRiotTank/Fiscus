package budgets

import (
	"sort"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/housing"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

// canonicalSpendingCategory maps housing aliases onto one display category so
// rent stored as "Rent", "Housing", or "Housing / Rent" matches the same
// default allocation.
func canonicalSpendingCategory(category string) string {
	if housing.IsCategory(category) {
		return housing.Category
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
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Currency < out[j].Currency
	})
	return out
}
