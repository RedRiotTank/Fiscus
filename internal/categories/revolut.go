package categories

import (
	"regexp"
	"strings"
)

// Built-in transfer categories. They share the violet transfer badge family in
// the dashboard.
const (
	CategoryHousing = "Housing / Rent"
	CategoryP2P     = "Transfers / P2P"
)

// RevolutP2PPattern recognises the peer-to-peer directions emitted by Revolut:
// "From Name", "To Name", "| From Name", and "| To Name".
var RevolutP2PPattern = regexp.MustCompile(`(?i)(?:^|\|\s*)(from|to)\s+(.+?)\s*$`)

var p2pConceptRules = []struct {
	category string
	pattern  *regexp.Regexp
}{
	{
		category: "Utilities & Telecom",
		pattern:  regexp.MustCompile(`(?i)\b(?:luz|agua|gas|electricidad|endesa|iberdrola|naturgy)\b`),
	},
	{
		category: CategoryHousing,
		pattern:  regexp.MustCompile(`(?i)(?:\bpiso\b|\balquiler\b|\brenta\b|\bhipoteca\b|\bmortgage\b|\brent\b|\blandlord\b)`),
	},
	{
		category: "Restaurants & Delivery",
		pattern:  regexp.MustCompile(`(?i)(?:uber eats|glovo|just eat|burger king|mcdonald'?s|\bkfc\b|telepizza|starbucks|restaurant|cena|comida|dinner|lunch)`),
	},
	{
		category: "Groceries",
		pattern:  regexp.MustCompile(`(?i)(?:coviran|mercadona|carrefour|lidl|\bd[ií]a\b|alcampo|eroski|\baldi\b|consum|bonpreu|hipercor|supermercado|compra)`),
	},
}

// RevolutTransfer is a parsed internal peer-to-peer transaction.
type RevolutTransfer struct {
	From         bool
	To           bool
	Counterparty string
	Concept      string
}

// ParseRevolutTransfer extracts the direction, counterparty, and optional
// prepended concept from a Revolut P2P description.
func ParseRevolutTransfer(text string) (RevolutTransfer, bool) {
	text = strings.TrimSpace(text)
	match := RevolutP2PPattern.FindStringSubmatchIndex(text)
	if match == nil {
		return RevolutTransfer{}, false
	}
	direction := strings.ToLower(text[match[2]:match[3]])
	counterparty := strings.TrimSpace(text[match[4]:match[5]])
	if counterparty == "" {
		return RevolutTransfer{}, false
	}
	concept := strings.TrimSpace(text[:match[0]])
	concept = strings.Trim(concept, "|- \t")
	return RevolutTransfer{
		From: direction == "from", To: direction == "to",
		Counterparty: counterparty, Concept: concept,
	}, true
}

// Category returns the concept-derived category, falling back to the generic
// internal P2P transfer category.
func (t RevolutTransfer) Category() string {
	return CategoryForP2PConcept(t.Concept)
}

// CategoryForP2PConcept maps a prepended Revolut note to a category.
func CategoryForP2PConcept(concept string) string {
	concept = strings.TrimSpace(concept)
	for _, rule := range p2pConceptRules {
		if rule.pattern.MatchString(concept) {
			return rule.category
		}
	}
	return CategoryP2P
}

// hasRevolutP2P returns true when either bank field carries a P2P direction.
func hasRevolutP2P(transactionFields ...string) (RevolutTransfer, bool) {
	for _, field := range transactionFields {
		if transfer, ok := ParseRevolutTransfer(field); ok {
			return transfer, true
		}
	}
	return RevolutTransfer{}, false
}
