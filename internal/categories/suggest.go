package categories

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// Suggestion is a frequent uncategorized counterparty and a ready-to-paste
// YAML rule fragment.
type Suggestion struct {
	Counterparty string
	Count        int
	Category     string
	Pattern      string
}

// Suggest aggregates uncategorized transactions by normalized counterparty and
// returns those seen at least minCount times. Results are ordered by frequency
// and then by counterparty so output is deterministic.
func Suggest(transactions []domain.Transaction, minCount int) []Suggestion {
	if minCount <= 0 {
		minCount = 3
	}
	type aggregate struct {
		display string
		count   int
	}
	groups := make(map[string]aggregate)
	for _, tx := range transactions {
		if !strings.EqualFold(strings.TrimSpace(tx.Category), DefaultCategory) {
			continue
		}
		counterparty := strings.TrimSpace(tx.Counterparty)
		if counterparty == "" {
			continue
		}
		key := strings.ToLower(counterparty)
		current := groups[key]
		if current.display == "" {
			current.display = counterparty
		}
		current.count++
		groups[key] = current
	}

	out := make([]Suggestion, 0, len(groups))
	for _, group := range groups {
		if group.count < minCount {
			continue
		}
		out = append(out, Suggestion{
			Counterparty: group.display,
			Count:        group.count,
			Category:     group.display,
			Pattern:      "(?i)" + regexp.QuoteMeta(group.display),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return strings.ToLower(out[i].Counterparty) < strings.ToLower(out[j].Counterparty)
		}
		return out[i].Count > out[j].Count
	})
	return out
}

// YAML renders suggestions as a rules block suitable for config.yaml.
func YAML(suggestions []Suggestion) string {
	if len(suggestions) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Frequent Uncategorized counterparties discovered by \"fiscus recategorize --suggest\".\n")
	b.WriteString("# Review the category labels before adding these entries under rules:.\n")
	b.WriteString("rules:\n")
	for i, suggestion := range suggestions {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "  # %d transaction(s)\n", suggestion.Count)
		fmt.Fprintf(&b, "  - category: %q\n", suggestion.Category)
		fmt.Fprintf(&b, "    pattern: %q\n", suggestion.Pattern)
	}
	return b.String()
}
