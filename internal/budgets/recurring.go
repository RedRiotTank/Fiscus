package budgets

import (
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
	"github.com/RedRiotTank/Fiscus/pkg/recurring"
)

// NetRecurringBillSpending replaces a confirmed recurring bill's gross debit
// with the user's effective personal share. Category spending is loaded from
// SQLite first, so a €16.00 Bizum confirmed as a shared utilities bill with a
// €5.33 personal share reports €5.33 against Utilities & Telecom instead of the
// gross transfer.
func NetRecurringBillSpending(spending []storage.CategorySpending, transactions []domain.Transaction, rules []domain.RecurringBillRule) []storage.CategorySpending {
	if len(rules) == 0 {
		return spending
	}
	active := make([]domain.RecurringBillRule, 0, len(rules))
	for _, rule := range rules {
		if rule.Active && strings.TrimSpace(rule.Counterparty) != "" {
			active = append(active, rule)
		}
	}
	if len(active) == 0 {
		return spending
	}

	type spendingKey struct {
		category string
		currency string
	}
	amounts := make(map[spendingKey]int64, len(spending)+len(active))
	for _, item := range spending {
		key := spendingKey{
			category: canonicalSpendingCategory(item.Category),
			currency: currencyOf(item.Currency),
		}
		amounts[key] += item.Minor
	}

	matched := false
	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked || tx.AmountMinor >= 0 {
			continue
		}
		if isExcludedTransaction(tx) {
			continue
		}
		currency := currencyOf(tx.Currency)
		for _, rule := range active {
			if !strings.EqualFold(currency, currencyOf(rule.Currency)) ||
				!recurring.MatchesCounterparty(tx, rule.Counterparty) {
				continue
			}
			matched = true
			gross := -tx.AmountMinor
			source := spendingKey{
				category: canonicalSpendingCategory(tx.Category),
				currency: currency,
			}
			amounts[source] -= gross
			if amounts[source] < 0 {
				amounts[source] = 0
			}
			target := spendingKey{
				category: canonicalSpendingCategory(rule.Category),
				currency: currency,
			}
			share := rule.PersonalShareMinor
			if share <= 0 {
				share = gross
			}
			amounts[target] += share
			break
		}
	}
	if !matched {
		return spending
	}

	out := make([]storage.CategorySpending, 0, len(amounts))
	for key, amount := range amounts {
		if amount == 0 {
			continue
		}
		out = append(out, storage.CategorySpending{
			Category: key.category, Currency: key.currency, Minor: amount,
		})
	}
	sortSpending(out)
	return out
}
