package categories

import (
	"context"
	"strings"
	"sync"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// LearnedRuleSource loads persisted inline-learning rules.
type LearnedRuleSource interface {
	ListLearnedRules(ctx context.Context) ([]domain.LearnedRule, error)
}

// DynamicClassifier is a thread-safe classifier that can reload learned rules
// after an inline category override.
type DynamicClassifier struct {
	mu       sync.RWMutex
	store    LearnedRuleSource
	custom   []Rule
	defaults []Rule
	current  *Classifier
}

// NewDynamicClassifier compiles custom rules, learned rules, and fallback
// defaults in that precedence order.
func NewDynamicClassifier(ctx context.Context, store LearnedRuleSource, custom, defaults []Rule) (*DynamicClassifier, error) {
	classifier := &DynamicClassifier{
		store: store, custom: append([]Rule(nil), custom...), defaults: append([]Rule(nil), defaults...),
	}
	if err := classifier.Reload(ctx); err != nil {
		return nil, err
	}
	return classifier, nil
}

// Reload refreshes the learned-rule snapshot from SQLite.
func (d *DynamicClassifier) Reload(ctx context.Context) error {
	rules := append([]Rule(nil), d.custom...)
	if d.store != nil {
		learned, err := d.store.ListLearnedRules(ctx)
		if err != nil {
			return err
		}
		for _, rule := range learned {
			rules = append(rules, Rule{Category: rule.Category, Pattern: rule.Pattern})
		}
	}
	rules = append(rules, d.defaults...)
	compiled, err := New(rules)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.current = compiled
	d.mu.Unlock()
	return nil
}

// Classify applies the current custom -> learned -> default rule snapshot.
func (d *DynamicClassifier) Classify(tx domain.Transaction) string {
	d.mu.RLock()
	classifier := d.current
	d.mu.RUnlock()
	return classifier.Classify(tx)
}

// RuleCount returns the number of rules in the current snapshot.
func (d *DynamicClassifier) RuleCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.current.RuleCount()
}

// Categories returns the unique categories in the current snapshot.
func (d *DynamicClassifier) Categories() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.current.Categories()
}

// OnlyUncategorized returns a classifier wrapper that never overwrites a
// category that a user or previous rule already assigned.
func OnlyUncategorized(classify func(domain.Transaction) string) func(domain.Transaction) string {
	return func(tx domain.Transaction) string {
		current := strings.TrimSpace(tx.Category)
		if current != "" && !strings.EqualFold(current, DefaultCategory) {
			return current
		}
		return classify(tx)
	}
}
