// Package categories classifies transactions using ordered regular-expression
// rules. Rules are evaluated in configuration order and the first match wins.
package categories

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/pkg/categorizer"
)

// DefaultCategory is assigned when no rule matches.
const DefaultCategory = "Uncategorized"

// Rule is a single category matcher.
type Rule struct {
	Category string
	Pattern  string
}

// Classifier applies ordered rules to transactions.
type Classifier struct {
	rules []compiledRule
}

type compiledRule struct {
	category string
	pattern  *regexp.Regexp
}

// New compiles rules. The first invalid rule is reported as an error.
func New(rules []Rule) (*Classifier, error) {
	compiled := make([]compiledRule, 0, len(rules))
	for i, rule := range rules {
		category := strings.TrimSpace(rule.Category)
		if category == "" {
			return nil, fmt.Errorf("categories: rule %d has an empty category", i+1)
		}
		pattern := strings.TrimSpace(rule.Pattern)
		if pattern == "" {
			return nil, fmt.Errorf("categories: rule %d (%s) has an empty pattern", i+1, category)
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("categories: rule %d (%s) pattern: %w", i+1, category, err)
		}
		compiled = append(compiled, compiledRule{category: category, pattern: re})
	}
	return &Classifier{rules: compiled}, nil
}

// MustNew is New with a panic on invalid rules. It is intended for tests and
// static rule sets.
func MustNew(rules []Rule) *Classifier {
	classifier, err := New(rules)
	if err != nil {
		panic(err)
	}
	return classifier
}

// Classify returns the category of the first matching rule, or DefaultCategory.
// Rules are tested against the counterparty and description separately, so a
// pattern never accidentally spans both fields. Explicit rules always win over
// the built-in Revolut P2P fallback so learned merchant categories take
// precedence.
func (c *Classifier) Classify(tx domain.Transaction) string {
	if c == nil {
		return DefaultCategory
	}
	text := strings.TrimSpace(tx.Counterparty + " " + tx.Description)
	if tx.AmountMinor <= 0 && categorizer.IsP2PText(text) {
		if category, ok := categorizer.DiningConceptCategory(text); ok {
			return category
		}
	}
	for _, rule := range c.rules {
		if rule.pattern.MatchString(tx.Counterparty) || rule.pattern.MatchString(tx.Description) {
			return rule.category
		}
	}
	if transfer, ok := hasRevolutP2P(tx.Counterparty, tx.Description); ok {
		return transfer.Category()
	}
	return DefaultCategory
}

// RuleCount returns the number of compiled rules.
func (c *Classifier) RuleCount() int {
	if c == nil {
		return 0
	}
	return len(c.rules)
}

// Categories returns the unique categories covered by the compiled rules.
func (c *Classifier) Categories() []string {
	seen := map[string]struct{}{DefaultCategory: {}, CategoryHousing: {}, CategoryP2P: {}}
	if c != nil {
		for _, rule := range c.rules {
			seen[rule.category] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for category := range seen {
		out = append(out, category)
	}
	sort.Strings(out)
	return out
}

// ValidateRules checks a rule set without constructing a classifier.
func ValidateRules(rules []Rule) error {
	_, err := New(rules)
	return err
}
