// Package housing recognises rent, mortgage, and shared-flat transactions so
// recurring housing costs are not confused with subscriptions and flatmate
// reimbursements can offset the gross housing expense.
package housing

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// Category is the canonical category used for rent and housing costs.
const Category = "Housing / Rent"

var (
	expensePattern = regexp.MustCompile(`(?i)(?:\balquiler\b|\brenta\b|\bpiso\b|\bhipoteca\b|\bmortgage\b|\brent\b|\blandlord\b|\barrendador\b)`)
	// Reimbursements are flatmate transfers that commonly carry the month's
	// shared-flat note, for example "Piso octubre | From Ismael".
	reimbursementPattern = regexp.MustCompile(`(?i)(?:\bpiso\b|\balquiler\b|\brenta\b|\bhipoteca\b|\bmortgage\b|\brent\b|\bhabitaci[oó]n\b|\broommate\b|\bflatmate\b|compa[nñ]er[oa]\s+de\s+piso)`)
)

// IsCategory reports whether a category represents housing or rent.
func IsCategory(category string) bool {
	normalized := strings.ToLower(strings.TrimSpace(category))
	if normalized == "" {
		return false
	}
	tokens := strings.FieldsFunc(normalized, func(r rune) bool {
		return !unicode.IsLetter(r)
	})
	for _, token := range tokens {
		switch token {
		case "housing", "rent", "renta", "mortgage", "alquiler", "hipoteca":
			return true
		}
	}
	return false
}

// IsExpense reports whether an outgoing transaction is a housing cost. It
// recognises both explicit housing categories and rent keywords in bank text.
func IsExpense(tx domain.Transaction) bool {
	if tx.AmountMinor >= 0 {
		return false
	}
	return IsCategory(tx.Category) || matches(expensePattern, tx.Counterparty+" "+tx.Description)
}

// IsReimbursement reports whether an incoming transaction is a shared-flat
// housing reimbursement rather than salary or general income.
func IsReimbursement(tx domain.Transaction) bool {
	if tx.AmountMinor <= 0 {
		return false
	}
	if IsCategory(tx.Category) {
		return true
	}
	return matches(reimbursementPattern, tx.Counterparty+" "+tx.Description)
}

// TextMatchesExpense reports whether text contains a rent or housing keyword.
func TextMatchesExpense(text string) bool {
	return matches(expensePattern, text)
}

func matches(pattern *regexp.Regexp, text string) bool {
	return pattern.MatchString(strings.TrimSpace(text))
}
