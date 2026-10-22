package categories

import (
	"strings"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestSuggestAggregatesFrequentUncategorizedCounterparties(t *testing.T) {
	t.Parallel()
	transactions := []domain.Transaction{
		{Counterparty: "Acme Store", Category: DefaultCategory},
		{Counterparty: "acme store", Category: DefaultCategory},
		{Counterparty: " Acme Store ", Category: DefaultCategory},
		{Counterparty: "Coffee Bar", Category: DefaultCategory},
		{Counterparty: "Coffee Bar", Category: DefaultCategory},
		{Counterparty: "Categorized", Category: "Dining"},
		{Counterparty: "", Category: DefaultCategory},
	}
	suggestions := Suggest(transactions, 3)
	if len(suggestions) != 1 {
		t.Fatalf("suggestions = %+v, want 1", suggestions)
	}
	got := suggestions[0]
	if got.Counterparty != "Acme Store" || got.Count != 3 || got.Category != "Acme Store" {
		t.Fatalf("suggestion = %+v", got)
	}
	if got.Pattern != `(?i)Acme Store` {
		t.Fatalf("pattern = %q", got.Pattern)
	}
	yaml := YAML(suggestions)
	for _, want := range []string{"rules:", `category: "Acme Store"`, `pattern: "(?i)Acme Store"`, "# 3 transaction(s)"} {
		if !strings.Contains(yaml, want) {
			t.Errorf("YAML missing %q:\n%s", want, yaml)
		}
	}
}

func TestSuggestEscapesRegexCharacters(t *testing.T) {
	t.Parallel()
	transactions := []domain.Transaction{
		{Counterparty: "A+B (Store)", Category: DefaultCategory},
		{Counterparty: "A+B (Store)", Category: DefaultCategory},
		{Counterparty: "A+B (Store)", Category: DefaultCategory},
	}
	suggestions := Suggest(transactions, 3)
	if len(suggestions) != 1 || suggestions[0].Pattern != `(?i)A\+B \(Store\)` {
		t.Fatalf("suggestions = %+v", suggestions)
	}
}

func TestSuggestEmpty(t *testing.T) {
	t.Parallel()
	if got := Suggest([]domain.Transaction{{Counterparty: "Once", Category: DefaultCategory}}, 3); len(got) != 0 {
		t.Fatalf("suggestions = %+v, want none", got)
	}
	if got := YAML(nil); got != "" {
		t.Fatalf("YAML = %q, want empty", got)
	}
}
