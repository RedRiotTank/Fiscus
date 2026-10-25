package categories

import (
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestDefaultRulesCategorizeSpanishMerchants(t *testing.T) {
	t.Parallel()
	classifier := MustNew(DefaultRules())
	tests := map[string]string{
		"Mercadona":                     "Groceries",
		"Carrefour Express":             "Groceries",
		"Día":                           "Groceries",
		"Glovo":                         "Restaurants & Delivery",
		"Uber Eats":                     "Restaurants & Delivery",
		"Iberdrola":                     "Utilities & Telecom",
		"Vodafone España":               "Utilities & Telecom",
		"Renfe Cercanías":               "Transport",
		"Uber":                          "Transport",
		"El Corte Inglés":               "Leisure & Shopping",
		"Amazon Prime*nk9ae7fn4":        "Subscriptions",
		"Amazon Marketplace purchase":   "Leisure & Shopping",
		"Comisión mantenimiento cuenta": "Bank Fees",
		"BIZUM Juan Pérez":              "Transfers / Bizum",
		"Netflix.com":                   "Subscriptions",
	}
	for merchant, want := range tests {
		tx := domain.Transaction{Counterparty: merchant, Description: merchant}
		if got := classifier.Classify(tx); got != want {
			t.Errorf("Classify(%q) = %q, want %q", merchant, got, want)
		}
	}
}

func TestCustomRulesPrecedeDefaults(t *testing.T) {
	t.Parallel()
	rules := append([]Rule{{Category: "Business Meals", Pattern: `(?i)glovo`}}, DefaultRules()...)
	classifier := MustNew(rules)
	if got := classifier.Classify(domain.Transaction{Counterparty: "Glovo"}); got != "Business Meals" {
		t.Fatalf("custom precedence = %q", got)
	}
}

func TestCategoriesIncludesDefaults(t *testing.T) {
	t.Parallel()
	categories := MustNew(DefaultRules()).Categories()
	want := map[string]bool{
		DefaultCategory: false, "Groceries": false, "Subscriptions": false,
		"Leisure & Shopping": false, "Restaurants & Delivery": false,
		"Transport": false, "Utilities & Telecom": false, "Bank Fees": false,
		"Transfers / Bizum": false,
	}
	for _, category := range categories {
		if _, ok := want[category]; ok {
			want[category] = true
		}
	}
	for category, found := range want {
		if !found {
			t.Errorf("categories missing %q: %v", category, categories)
		}
	}
}
