package categorizer

import (
	"regexp"
	"strings"
)

// p2pSignalPattern recognises Bizum transfers and Revolut's inline
// "From Name" / "To Name" direction markers.
var p2pSignalPattern = regexp.MustCompile(`(?i)\bbizum\b|(?:^|\|\s*)(?:from|to)\s+`)

// diningConceptPattern recognises food-delivery and shared-dining notes on
// peer-to-peer transfers. The concept wins over a generic "Bizum" catalog hit.
var diningConceptPattern = regexp.MustCompile(`(?i)\b(?:uber\s*eats|glovo|just\s*eat|burger|pizza|cena|comida|kebab|tacos|mcdonalds?|mcdonald'?s|restaurante|restaurant|bar|tapas|brunch)\b`)

// IsP2PText reports whether a transaction description looks like a Bizum or
// Revolut peer-to-peer transfer.
func IsP2PText(text string) bool {
	return p2pSignalPattern.MatchString(strings.TrimSpace(text))
}

// DiningConceptCategory maps outbound P2P dining notes to Restaurants &
// Delivery. It returns false for unrelated transfer concepts.
func DiningConceptCategory(text string) (string, bool) {
	if !diningConceptPattern.MatchString(text) {
		return "", false
	}
	return CategoryRestaurants, true
}
