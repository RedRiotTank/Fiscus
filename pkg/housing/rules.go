package housing

import (
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/pkg/subscriptions"
)

// MatchesCounterparty reports whether a transaction refers to the named
// landlord or flatmate.
func MatchesCounterparty(tx domain.Transaction, counterparty string) bool {
	target := strings.TrimSpace(counterparty)
	if target == "" {
		return false
	}
	cleanedTarget := subscriptions.CleanMerchant(target)
	for _, field := range []string{tx.Counterparty, tx.Description} {
		raw := strings.ToLower(strings.TrimSpace(field))
		if raw == "" {
			continue
		}
		if raw == strings.ToLower(target) || (cleanedTarget != "" && raw == strings.ToLower(cleanedTarget)) {
			return true
		}
		if cleanedTarget != "" && len([]rune(cleanedTarget)) >= 3 &&
			strings.Contains(raw, strings.ToLower(cleanedTarget)) {
			return true
		}
	}
	return false
}

// IsMasterPayment reports whether the transaction is the configured landlord
// rent payment.
func IsMasterPayment(tx domain.Transaction, rule domain.HousingRule) bool {
	return tx.AmountMinor < 0 && MatchesCounterparty(tx, rule.LandlordCounterparty)
}

// IsFlatmateContribution reports whether the transaction is an inbound
// contribution from one of the configured flatmates.
func IsFlatmateContribution(tx domain.Transaction, rule domain.HousingRule) bool {
	if tx.AmountMinor <= 0 {
		return false
	}
	for _, flatmate := range rule.FlatmateCounterparties {
		if MatchesCounterparty(tx, flatmate) {
			return true
		}
	}
	return false
}

// RuleCategory returns Housing / Rent when the transaction matches the rule.
func RuleCategory(tx domain.Transaction, rule domain.HousingRule) string {
	if IsMasterPayment(tx, rule) || IsFlatmateContribution(tx, rule) {
		return Category
	}
	return tx.Category
}
