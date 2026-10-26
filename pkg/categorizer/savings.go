package categorizer

import (
	"regexp"
	"strings"
	"unicode"
)

// savingsPattern recognises transfers into savings products, including
// Revolut's "Cuenta Remunerada" and flexible/vault products.
var savingsPattern = regexp.MustCompile(`(?i)\b(?:cuenta\s*remunerada|flexible|ahorro|ahorros|deposito|depósito|vault|savings)\b`)

func normalizeWhitespace(text string) string {
	text = strings.TrimSpace(text)
	text = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, text)
	return strings.Join(strings.Fields(text), " ")
}

// IsSavingsTransferText reports whether a transaction description looks like a
// savings transfer.
func IsSavingsTransferText(text string) bool {
	return savingsPattern.MatchString(normalizeWhitespace(text))
}

// SavingsMatch extracts the matched savings keyword or product name from text.
func SavingsMatch(text string) (string, bool) {
	match := savingsPattern.FindString(normalizeWhitespace(text))
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
