package categories

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/pkg/categorizer"
)

// LearnedRuleSource loads persisted inline-learning rules.
type LearnedRuleSource interface {
	ListLearnedRules(ctx context.Context) ([]domain.LearnedRule, error)
}

// HistorySource optionally supplies confirmed historical transactions used to
// seed the in-memory Bayesian classifier at startup.
type HistorySource interface {
	ListCategorizedTransactionsForTraining(ctx context.Context, limit int) ([]domain.Transaction, error)
}

// HousingRuleSource optionally supplies confirmed shared-rent rules.
type HousingRuleSource interface {
	ListHousingRules(ctx context.Context) ([]domain.HousingRule, error)
}

// RecurringBillRuleSource optionally supplies confirmed household bills.
type RecurringBillRuleSource interface {
	ListAllRecurringBillRules(ctx context.Context) ([]domain.RecurringBillRule, error)
}

// DynamicClassifier is a thread-safe classifier that can reload learned rules
// and learn from user corrections without restarting the process.
type DynamicClassifier struct {
	mu       sync.RWMutex
	store    LearnedRuleSource
	custom   []Rule
	defaults []Rule
	current  *Classifier
	cascade  *categorizer.Cascade
}

// NewDynamicClassifier compiles custom rules, learned rules, and fallback
// defaults in that precedence order. The Bayesian tier is seeded from the
// embedded catalog and, when available, confirmed SQLite history.
func NewDynamicClassifier(ctx context.Context, store LearnedRuleSource, custom, defaults []Rule) (*DynamicClassifier, error) {
	cascade, err := categorizer.NewCascade(categorizer.Config{
		Catalog:   categorizer.DefaultCatalog(),
		Threshold: categorizer.DefaultThreshold,
	})
	if err != nil {
		return nil, err
	}
	classifier := &DynamicClassifier{
		store: store, custom: append([]Rule(nil), custom...), defaults: append([]Rule(nil), defaults...),
		cascade: cascade,
	}
	classifier.seedHistory(ctx)
	if err := classifier.Reload(ctx); err != nil {
		return nil, err
	}
	return classifier, nil
}

// Reload refreshes the deterministic rule snapshot from SQLite. The Bayesian
// model is intentionally preserved so online corrections are not discarded.
func (d *DynamicClassifier) Reload(ctx context.Context) error {
	explicitRules := append([]Rule(nil), d.custom...)
	if d.store != nil {
		learned, err := d.store.ListLearnedRules(ctx)
		if err != nil {
			return err
		}
		for _, rule := range learned {
			explicitRules = append(explicitRules, Rule{Category: rule.Category, Pattern: rule.Pattern})
		}
		if source, ok := d.store.(HousingRuleSource); ok {
			housingRules, err := source.ListHousingRules(ctx)
			if err != nil {
				return err
			}
			for _, rule := range housingRules {
				landlord := strings.TrimSpace(rule.LandlordCounterparty)
				if len([]rune(landlord)) < 3 {
					continue
				}
				explicitRules = append(explicitRules, Rule{Category: categorizer.CategoryHousing, Pattern: LearnedPattern(landlord)})
			}
		}
		if source, ok := d.store.(RecurringBillRuleSource); ok {
			recurringRules, err := source.ListAllRecurringBillRules(ctx)
			if err != nil {
				return err
			}
			for _, rule := range recurringRules {
				counterparty := strings.TrimSpace(rule.Counterparty)
				category := strings.TrimSpace(rule.Category)
				if len([]rune(counterparty)) < 3 || category == "" {
					continue
				}
				explicitRules = append(explicitRules, Rule{Category: category, Pattern: LearnedPattern(counterparty)})
			}
		}
	}
	rules := append(append([]Rule(nil), explicitRules...), d.defaults...)

	compiled, err := New(rules)
	if err != nil {
		return err
	}
	// Defaults are represented by the deterministic catalog inside the
	// cascade. Only explicit/learned rules are compiled as regexes there, so a
	// specific P2P dining concept can outrank the generic "Bizum" catalog entry.
	cascadeRules := make([]categorizer.Rule, 0, len(explicitRules))
	for _, rule := range explicitRules {
		cascadeRules = append(cascadeRules, categorizer.Rule{Category: rule.Category, Pattern: rule.Pattern})
	}
	if err := d.cascade.SetRules(cascadeRules); err != nil {
		return err
	}
	d.mu.Lock()
	d.current = compiled
	d.mu.Unlock()
	return nil
}

func (d *DynamicClassifier) seedHistory(ctx context.Context) {
	source, ok := d.store.(HistorySource)
	if !ok {
		return
	}
	transactions, err := source.ListCategorizedTransactionsForTraining(ctx, 5000)
	if err != nil {
		return
	}
	for _, tx := range transactions {
		category := strings.TrimSpace(tx.Category)
		if category == "" || strings.EqualFold(category, DefaultCategory) {
			continue
		}
		// Neutral transfer buckets are structural fallbacks, not merchant
		// training examples. Seeding them teaches Bayes to swallow salaries and
		// other unrelated inflows.
		if strings.EqualFold(category, categorizer.CategoryP2P) || strings.EqualFold(category, "Transfers / Pass-through") {
			continue
		}
		d.cascade.Seed(categorizerInput(tx), category, 1)
	}
}

// Classify applies deterministic rules first, then the Bayesian tier, then the
// conservative review-queue fallback.
func (d *DynamicClassifier) Classify(tx domain.Transaction) string {
	return d.ClassifyDetailed(tx).Category
}

// ClassifyDetailed exposes the winning tier and confidence for UI affordances
// and diagnostics.
func (d *DynamicClassifier) ClassifyDetailed(tx domain.Transaction) categorizer.Result {
	cascade := d.cascadeSnapshot()
	result := cascade.Classify(categorizerInput(tx))
	if result.Tier != categorizer.TierFallback {
		return result
	}
	if transfer, ok := hasRevolutP2P(tx.Counterparty, tx.Description); ok {
		category := transfer.Category()
		result.Category = category
		result.Matched = "revolut-p2p"
		result.Candidate = category
		result.CandidateConfidence = 1
	}
	return result
}

// Learn updates the in-memory Bayesian model from an explicit user correction.
func (d *DynamicClassifier) Learn(tx domain.Transaction, category string) {
	category = strings.TrimSpace(category)
	if category == "" || strings.EqualFold(category, DefaultCategory) {
		return
	}
	d.cascadeSnapshot().Learn(categorizerInput(tx), category)
}

// SuggestCategory returns the Bayesian candidate for a low-confidence row.
func (d *DynamicClassifier) SuggestCategory(tx domain.Transaction) (string, float64) {
	result := d.ClassifyDetailed(tx)
	if result.Tier == categorizer.TierFallback {
		if result.Category != DefaultCategory {
			return result.Category, 1
		}
		return result.Candidate, result.CandidateConfidence
	}
	return result.Category, result.Confidence
}

// RuleCount returns the number of deterministic rules in the current
// snapshot.
func (d *DynamicClassifier) RuleCount() int {
	d.mu.RLock()
	current := d.current
	d.mu.RUnlock()
	if current == nil {
		return d.cascadeSnapshot().RuleCount()
	}
	return current.RuleCount()
}

// Categories returns the unique categories known to every tier.
func (d *DynamicClassifier) Categories() []string {
	d.mu.RLock()
	current := d.current
	cascade := d.cascade
	d.mu.RUnlock()
	seen := make(map[string]struct{})
	if current != nil {
		for _, category := range current.Categories() {
			seen[category] = struct{}{}
		}
	}
	if cascade != nil {
		for _, category := range cascade.Categories() {
			seen[category] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for category := range seen {
		out = append(out, category)
	}
	sort.Strings(out)
	return out
}

// Examples reports the weighted Bayesian training corpus size.
func (d *DynamicClassifier) Examples() float64 {
	return d.cascadeSnapshot().Examples()
}

func (d *DynamicClassifier) cascadeSnapshot() *categorizer.Cascade {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.cascade
}

func categorizerInput(tx domain.Transaction) categorizer.Input {
	return categorizer.Input{
		Counterparty: tx.Counterparty,
		Description:  tx.Description,
		Notes:        tx.Notes,
		AmountMinor:  tx.AmountMinor,
	}
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
