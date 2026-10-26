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

func TestDynamicClassifierRoutesP2PDiningConcept(t *testing.T) {
	t.Parallel()
	classifier, err := NewDynamicClassifier(context.Background(), &fakeLearnedSource{}, nil, DefaultRules())
	if err != nil {
		t.Fatalf("NewDynamicClassifier: %v", err)
	}
	tx := domain.Transaction{Counterparty: "Bizum Juan", Description: "cena viernes", AmountMinor: -2_000}
	result := classifier.ClassifyDetailed(tx)
	if result.Category != "Restaurants & Delivery" || result.Confidence < 0.82 {
		t.Fatalf("P2P dining result = %+v, want Restaurants & Delivery", result)
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

func TestDynamicClassifierLearnsUserCorrection(t *testing.T) {
	t.Parallel()
	source := &fakeLearnedSource{}
	classifier, err := NewDynamicClassifier(context.Background(), source, nil, DefaultRules())
	if err != nil {
		t.Fatalf("NewDynamicClassifier: %v", err)
	}
	tx := domain.Transaction{Counterparty: "Panaderia Luna", AmountMinor: -350}
	before := classifier.ClassifyDetailed(tx)
	if before.Category != DefaultCategory {
		t.Fatalf("before learning = %+v, want Uncategorized", before)
	}
	classifier.Learn(tx, "Groceries")
	after := classifier.ClassifyDetailed(tx)
	if after.Category != "Groceries" || after.Confidence < 0.82 {
		t.Fatalf("after learning = %+v, want high-confidence Groceries", after)
	}
}

type fakeHistorySource struct {
	fakeLearnedSource
	history []domain.Transaction
}

func (f *fakeHistorySource) ListCategorizedTransactionsForTraining(context.Context, int) ([]domain.Transaction, error) {
	return f.history, nil
}

func TestDynamicClassifierSeedsFromConfirmedHistory(t *testing.T) {
	t.Parallel()
	source := &fakeHistorySource{}
	for i := 0; i < 20; i++ {
		source.history = append(source.history, domain.Transaction{
			Counterparty: "Panaderia Luna", AmountMinor: -350, Category: "Groceries",
		})
	}
	classifier, err := NewDynamicClassifier(context.Background(), source, nil, DefaultRules())
	if err != nil {
		t.Fatalf("NewDynamicClassifier: %v", err)
	}
	result := classifier.ClassifyDetailed(domain.Transaction{Counterparty: "Panaderia Luna", AmountMinor: -350})
	if result.Category != "Groceries" || result.Confidence < 0.82 {
		t.Fatalf("history-trained result = %+v, want high-confidence Groceries", result)
	}
}
