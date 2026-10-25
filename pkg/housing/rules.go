package housing

import (
	"regexp"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/pkg/subscriptions"
)

const flatmateShareToleranceMinor int64 = 500

var inflowKeywordPattern = regexp.MustCompile(`(?i)\b(?:piso|alquiler|renta|rent|habitaci[oó]n|arriendo)\b`)

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
		if nameTokensMatch(raw, target) {
			return true
		}
	}
	return false
}

// nameTokensMatch accepts bank-shortened names such as "Ismael D" for a
// configured "Ismael Díaz" while still requiring the same number of words.
func nameTokensMatch(raw, target string) bool {
	rawTokens := strings.Fields(strings.ToLower(strings.TrimSpace(raw)))
	targetTokens := strings.Fields(strings.ToLower(strings.TrimSpace(target)))
	if len(rawTokens) == 0 || len(rawTokens) != len(targetTokens) {
		return false
	}
	for i := range rawTokens {
		if !strings.HasPrefix(rawTokens[i], targetTokens[i]) &&
			!strings.HasPrefix(targetTokens[i], rawTokens[i]) {
			return false
		}
	}
	return true
}

// IsMasterPayment reports whether the transaction is the configured landlord
// rent payment.
func IsMasterPayment(tx domain.Transaction, rule domain.HousingRule) bool {
	return tx.AmountMinor < 0 && MatchesCounterparty(tx, rule.LandlordCounterparty)
}

// IsFlatmateContribution reports whether an inbound transaction is a genuine
// shared-rent contribution. A flatmate's micro-transfer for dinner or shopping
// is not housing unless its note names the flat/rent or its amount matches the
// configured share within the €5 tolerance.
func IsFlatmateContribution(tx domain.Transaction, rule domain.HousingRule) bool {
	if tx.AmountMinor <= 0 {
		return false
	}
	matched := false
	for _, flatmate := range rule.FlatmateCounterparties {
		if MatchesCounterparty(tx, flatmate) {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	if inflowKeywordPattern.MatchString(tx.Counterparty + " " + tx.Description) {
		return true
	}
	share := rule.PersonalNetShareCents
	return share > 0 && absMinor(tx.AmountMinor-share) <= flatmateShareToleranceMinor
}

func absMinor(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

// RuleCategory returns Housing / Rent when the transaction matches the rule.
func RuleCategory(tx domain.Transaction, rule domain.HousingRule) string {
	if IsMasterPayment(tx, rule) || IsFlatmateContribution(tx, rule) {
		return Category
	}
	return tx.Category
}
