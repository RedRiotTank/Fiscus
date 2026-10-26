package categorizer

import (
	"regexp"
	"strings"
)

// savingsPattern recognises transfers into savings products, including
// Revolut's "Cuenta Remunerada" and flexible/vault products.
var savingsPattern = regexp.MustCompile(`(?i)\b(?:cuenta\s*remunerada|flexible|ahorro|ahorros|deposito|depósito|vault|savings)\b`)

// IsSavingsTransferText reports whether a transaction description looks like a
// savings transfer.
func IsSavingsTransferText(text string) bool {
	return savingsPattern.MatchString(strings.TrimSpace(text))
}

// SavingsMatch extracts the matched savings keyword or product name from text.
func SavingsMatch(text string) (string, bool) {
	match := savingsPattern.FindString(strings.TrimSpace(text))
	if match == "" {
		return "", false
	}
	return match, true
}

// SavingsTransferCategory returns the canonical category for a matched savings
// transfer. Outflows fund the Savings pillar; inflows are neutral withdrawals.
func SavingsTransferCategory(amountMinor int64) (category string, budgetExcluded bool) {
	if amountMinor > 0 {
		return "Transfers / Pass-through", true
	}
	return CategorySavings, false
}

// SavingsMatchPattern exposes the shared savings regex for storage linkers.
const SavingsMatchPattern = `(?i)\b(?:cuenta\s*remunerada|flexible|ahorro|ahorros|deposito|depósito|vault|savings)\b`
