package categories

import "github.com/RedRiotTank/Fiscus/pkg/categorizer"

// DefaultRules returns the embedded Spanish and European merchant fallback
// ruleset generated from the deterministic catalog. The catalog is the single
// source of truth; this adapter keeps the legacy regex-based classifier and
// configuration tooling working while the live pipeline uses the full cascade.
func DefaultRules() []Rule {
	entries := categorizer.DefaultCatalog()
	rules := make([]Rule, 0, len(entries))
	for _, entry := range entries {
		pattern := entry.Pattern()
		if pattern == "" {
			continue
		}
		rules = append(rules, Rule{Category: entry.Category, Pattern: pattern})
	}
	return rules
}
