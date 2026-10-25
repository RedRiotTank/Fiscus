package categories

import (
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestParseRevolutTransferAndConcept(t *testing.T) {
	t.Parallel()
	tests := []struct {
		text         string
		ok           bool
		from         bool
		to           bool
		counterparty string
		concept      string
		category     string
	}{
		{text: "From Ismael Díaz", ok: true, from: true, counterparty: "Ismael Díaz", category: CategoryP2P},
		{text: "To Ismael Díaz", ok: true, to: true, counterparty: "Ismael Díaz", category: CategoryP2P},
		{text: "Piso octubre | From Ismael Díaz", ok: true, from: true, counterparty: "Ismael Díaz", concept: "Piso octubre", category: CategoryHousing},
		{text: "For Uber Eats | To John", ok: true, to: true, counterparty: "John", concept: "For Uber Eats", category: "Restaurants & Delivery"},
		{text: "Coviran | To Coviran S.C.A.", ok: true, to: true, counterparty: "Coviran S.C.A.", concept: "Coviran", category: "Groceries"},
		{text: "Card payment", ok: false},
	}
	for _, tc := range tests {
		transfer, ok := ParseRevolutTransfer(tc.text)
		if ok != tc.ok {
			t.Errorf("ParseRevolutTransfer(%q) ok = %v, want %v", tc.text, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if transfer.From != tc.from || transfer.To != tc.to ||
			transfer.Counterparty != tc.counterparty || transfer.Concept != tc.concept {
			t.Errorf("ParseRevolutTransfer(%q) = %+v", tc.text, transfer)
		}
		if got := transfer.Category(); got != tc.category {
			t.Errorf("Category(%q) = %q, want %q", tc.text, got, tc.category)
		}
	}
}

func TestClassifierRecognisesRevolutP2PConcepts(t *testing.T) {
	t.Parallel()
	classifier := MustNew(DefaultRules())
	tests := map[string]string{
		"From Ismael Díaz":              CategoryP2P,
		"To Ismael Díaz":                CategoryP2P,
		"Piso octubre | From Ismael":    CategoryHousing,
		"For Uber Eats | To John":       "Restaurants & Delivery",
		"Coviran | To Ana":              "Groceries",
		"Unknown concept | To Ana":      CategoryP2P,
		"Amazon Prime*nk9ae7fn4":        "Subscriptions",
		"Supermercados Coviran | To Al": "Groceries",
	}
	for text, want := range tests {
		if got := classifier.Classify(domain.Transaction{Counterparty: text, Description: text}); got != want {
			t.Errorf("Classify(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestLearnedRulesBeatP2PFallback(t *testing.T) {
	t.Parallel()
	classifier := MustNew([]Rule{{Category: "Family", Pattern: `(?i)Ismael`}})
	if got := classifier.Classify(domain.Transaction{Counterparty: "To Ismael Díaz"}); got != "Family" {
		t.Fatalf("explicit rule should win: %q", got)
	}
}
