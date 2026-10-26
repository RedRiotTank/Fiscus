package categorizer

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Tier identifies which stage of the cascade produced a result.
type Tier int

const (
	// TierExact is a deterministic catalog, explicit user, or confirmed rule.
	TierExact Tier = iota + 1
	// TierBayes is an automatic assignment from the probabilistic classifier.
	TierBayes
	// TierFallback is the conservative review-queue result.
	TierFallback
)

// Input is the subset of a transaction needed for categorization.
type Input struct {
	Counterparty string
	Description  string
	Notes        string
	AmountMinor  int64
}

func (i Input) text() string {
	return strings.TrimSpace(i.Counterparty + " " + i.Description + " " + i.Notes)
}

func (i Input) fields() []string {
	return []string{i.Counterparty, i.Description, i.Notes}
}

// Rule is an explicit user or persisted learned regular expression.
type Rule struct {
	Category string
	Pattern  string
}

type compiledRule struct {
	category string
	pattern  *regexp.Regexp
}

// Result describes the winning category and the evidence behind it.
type Result struct {
	Category   string
	Tier       Tier
	Confidence float64
	Matched    string
	// Candidate is the Bayesian best guess even when it is below the automatic
	// assignment threshold. The UI uses it for a one-click confirmation chip.
	Candidate           string
	CandidateConfidence float64
}

// Config controls cascade construction.
type Config struct {
	Rules     []Rule
	Catalog   []CatalogEntry
	Threshold float64
}

// Cascade is a thread-safe three-tier categorizer.
type Cascade struct {
	mu        sync.RWMutex
	rules     []compiledRule
	learned   []learnedPhrase
	catalog   *Catalog
	bayes     *NaiveBayes
	threshold float64
}

type learnedPhrase struct {
	category string
	tokens   []string
}

// NewCascade compiles explicit rules, builds the deterministic catalog, and
// seeds the naive Bayes model from the embedded catalog.
func NewCascade(cfg Config) (*Cascade, error) {
	catalogEntries := cfg.Catalog
	if catalogEntries == nil {
		catalogEntries = DefaultCatalog()
	}
	threshold := cfg.Threshold
	if threshold <= 0 || threshold > 1 {
		threshold = DefaultThreshold
	}
	cascade := &Cascade{
		catalog:   NewCatalog(catalogEntries),
		bayes:     NewNaiveBayes(1),
		threshold: threshold,
	}
	if err := cascade.SetRules(cfg.Rules); err != nil {
		return nil, err
	}
	for _, entry := range catalogEntries {
		for _, keyword := range entry.Keywords {
			cascade.bayes.Train(keyword, entry.Category, 0, 3)
		}
	}
	return cascade, nil
}

// SetRules replaces the deterministic explicit-rule snapshot atomically.
func (c *Cascade) SetRules(rules []Rule) error {
	compiled := make([]compiledRule, 0, len(rules))
	for index, rule := range rules {
		category := strings.TrimSpace(rule.Category)
		if category == "" {
			return fmt.Errorf("categorizer: rule %d has an empty category", index+1)
		}
		pattern := strings.TrimSpace(rule.Pattern)
		if pattern == "" {
			return fmt.Errorf("categorizer: rule %d (%s) has an empty pattern", index+1, category)
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("categorizer: rule %d (%s) pattern: %w", index+1, category, err)
		}
		compiled = append(compiled, compiledRule{category: category, pattern: re})
	}
	c.mu.Lock()
	c.rules = compiled
	c.mu.Unlock()
	return nil
}

// Seed adds a historical or catalog training example. weight controls how much
// the example influences future predictions.
func (c *Cascade) Seed(input Input, category string, weight float64) {
	if c == nil {
		return
	}
	c.bayes.Train(input.text(), category, input.AmountMinor, weight)
}

// Learn performs online training from an explicit user correction. The
// correction is recorded as an in-memory Tier 1 phrase (so it applies
// immediately without a database round-trip) and is also folded into the
// Bayesian model for similar future merchants.
func (c *Cascade) Learn(input Input, category string) {
	c.Seed(input, category, 4)
	category = strings.TrimSpace(category)
	if c == nil || category == "" {
		return
	}
	tokens := Tokenize(input.Counterparty)
	if len(tokens) == 0 {
		tokens = Tokenize(input.Description)
	}
	if len(tokens) == 0 {
		return
	}
	c.mu.Lock()
	c.learned = append(c.learned, learnedPhrase{category: category, tokens: tokens})
	c.mu.Unlock()
}

// Classify runs the deterministic, probabilistic, and fallback tiers in order.
func (c *Cascade) Classify(input Input) Result {
	if c == nil {
		return Result{Category: CategoryUncategorized, Tier: TierFallback}
	}
	c.mu.RLock()
	rules := c.rules
	learned := c.learned
	catalog := c.catalog
	c.mu.RUnlock()

	for _, rule := range rules {
		for _, field := range input.fields() {
			if rule.pattern.MatchString(field) {
				return Result{
					Category: rule.category, Tier: TierExact, Confidence: 1,
					Matched: rule.pattern.String(), Candidate: rule.category, CandidateConfidence: 1,
				}
			}
		}
	}
	for _, phrase := range learned {
		for _, field := range input.fields() {
			if containsTokens(Tokenize(field), phrase.tokens) {
				return Result{
					Category: phrase.category, Tier: TierExact, Confidence: 1,
					Matched: "learned", Candidate: phrase.category, CandidateConfidence: 1,
				}
			}
		}
	}
	// A dining note is more specific than the generic Bizum catalog entry. This
	// applies to outbound peer-to-peer transfers only, regardless of the
	// counterparty name.
	if input.AmountMinor <= 0 {
		text := input.text()
		if IsP2PText(text) {
			if category, ok := DiningConceptCategory(text); ok {
				return Result{
					Category: category, Tier: TierExact, Confidence: 1,
					Matched: "p2p-dining-concept", Candidate: category, CandidateConfidence: 1,
				}
			}
		}
	}
	for _, field := range input.fields() {
		if result, ok := catalog.Match(field, input.AmountMinor); ok {
			result.Candidate = result.Category
			result.CandidateConfidence = result.Confidence
			return result
		}
	}

	prediction, ok := c.bayes.Predict(input.text(), input.AmountMinor)
	if ok && prediction.Confidence >= c.threshold {
		return Result{
			Category: prediction.Category, Tier: TierBayes, Confidence: prediction.Confidence,
			Matched: "naive-bayes", Candidate: prediction.Category, CandidateConfidence: prediction.Confidence,
		}
	}
	result := Result{Category: CategoryUncategorized, Tier: TierFallback}
	if ok {
		result.Candidate = prediction.Category
		result.CandidateConfidence = prediction.Confidence
	}
	return result
}

// Threshold returns the automatic-assignment confidence threshold.
func (c *Cascade) Threshold() float64 {
	if c == nil {
		return DefaultThreshold
	}
	return c.threshold
}

// RuleCount returns the number of deterministic explicit rules.
func (c *Cascade) RuleCount() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.rules)
}

// Categories returns every category known to any tier.
func (c *Cascade) Categories() []string {
	if c == nil {
		return nil
	}
	seen := make(map[string]struct{})
	c.mu.RLock()
	for _, rule := range c.rules {
		seen[rule.category] = struct{}{}
	}
	for _, entry := range c.catalog.entries {
		seen[entry.category] = struct{}{}
	}
	c.mu.RUnlock()
	for _, category := range c.bayes.Categories() {
		seen[category] = struct{}{}
	}
	seen[CategoryUncategorized] = struct{}{}
	seen[CategoryP2P] = struct{}{}
	out := make([]string, 0, len(seen))
	for category := range seen {
		out = append(out, category)
	}
	sort.Strings(out)
	return out
}

// Examples returns the weighted number of Bayesian training examples.
func (c *Cascade) Examples() float64 {
	if c == nil {
		return 0
	}
	return c.bayes.Examples()
}

// ValidateRules checks explicit rules without constructing a cascade.
func ValidateRules(rules []Rule) error {
	for index, rule := range rules {
		if strings.TrimSpace(rule.Category) == "" {
			return fmt.Errorf("categorizer: rule %d has an empty category", index+1)
		}
		if strings.TrimSpace(rule.Pattern) == "" {
			return fmt.Errorf("categorizer: rule %d (%s) has an empty pattern", index+1, rule.Category)
		}
		if _, err := regexp.Compile(rule.Pattern); err != nil {
			return fmt.Errorf("categorizer: rule %d (%s) pattern: %w", index+1, rule.Category, err)
		}
	}
	return nil
}
