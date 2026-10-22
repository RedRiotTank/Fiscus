// Package categories classifies transactions using ordered regular-expression
// rules. Rules are evaluated in configuration order and the first match wins.
package categories

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
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
// pattern never accidentally spans both fields.
func (c *Classifier) Classify(tx domain.Transaction) string {
	if c == nil {
		return DefaultCategory
	}
	for _, rule := range c.rules {
		if rule.pattern.MatchString(tx.Counterparty) || rule.pattern.MatchString(tx.Description) {
			return rule.category
		}
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

// ValidateRules checks a rule set without constructing a classifier.
func ValidateRules(rules []Rule) error {
	_, err := New(rules)
	return err
}
