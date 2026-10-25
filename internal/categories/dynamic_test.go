package categories

import (
	"context"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

type fakeLearnedSource struct {
	rules []domain.LearnedRule
}

func (f *fakeLearnedSource) ListLearnedRules(context.Context) ([]domain.LearnedRule, error) {
	return f.rules, nil
}

func TestDynamicClassifierReloadsLearnedRules(t *testing.T) {
	t.Parallel()
	source := &fakeLearnedSource{}
	classifier, err := NewDynamicClassifier(context.Background(), source, nil, DefaultRules())
	if err != nil {
		t.Fatalf("NewDynamicClassifier: %v", err)
	}
	tx := domain.Transaction{Counterparty: "Custom Vendor SL", Description: "Custom Vendor SL"}
	if got := classifier.Classify(tx); got != DefaultCategory {
		t.Fatalf("before learn = %q", got)
	}
	source.rules = []domain.LearnedRule{{Pattern: `(?i)Custom Vendor`, Category: "Shopping"}}
	if err := classifier.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := classifier.Classify(tx); got != "Shopping" {
		t.Fatalf("after learn = %q", got)
	}
}

func TestOnlyUncategorized(t *testing.T) {
	t.Parallel()
	classify := OnlyUncategorized(func(domain.Transaction) string { return "Groceries" })
	if got := classify(domain.Transaction{Category: "Restaurants"}); got != "Restaurants" {
		t.Fatalf("categorized override = %q", got)
	}
	if got := classify(domain.Transaction{Category: "Uncategorized"}); got != "Groceries" {
		t.Fatalf("uncategorized result = %q", got)
	}
	if got := classify(domain.Transaction{}); got != "Groceries" {
		t.Fatalf("empty result = %q", got)
	}
}
