// Package icons resolves merchant names to bundled subscription brand icons.
package icons

import (
	"sort"
	"strings"
	"unicode"
)

// Resolver maps merchant names to embedded brand icon slugs.
type Resolver struct {
	overrides []override
}

type override struct {
	key  string
	slug string
}

type brandRule struct {
	icon    string
	aliases []string
	exact   []string
}

// New constructs a resolver from optional merchant-to-icon overrides. Override
// values may be a bundled slug such as "briefcase" or an embedded path such as
// "/static/icons/brands/briefcase.svg".
func New(overrides map[string]string) *Resolver {
	list := make([]override, 0, len(overrides))
	for key, value := range overrides {
		normalizedKey := Normalize(key)
		slug, ok := normalizeIconReference(value)
		if normalizedKey == "" || !ok {
			continue
		}
		list = append(list, override{key: normalizedKey, slug: slug})
	}
	sort.Slice(list, func(i, j int) bool {
		if len(list[i].key) != len(list[j].key) {
			return len(list[i].key) > len(list[j].key)
		}
		return list[i].key < list[j].key
	})
	return &Resolver{overrides: list}
}

// Resolve returns the brand icon slug for a merchant name.
func (r *Resolver) Resolve(merchant string) (string, bool) {
	normalized := Normalize(merchant)
	if normalized == "" {
		return "", false
	}
	if r != nil {
		for _, item := range r.overrides {
			if normalized == item.key || strings.Contains(normalized, item.key) {
				return item.slug, true
			}
		}
	}
	for _, rule := range brandRules {
		for _, alias := range rule.aliases {
			if strings.Contains(normalized, alias) {
				return rule.icon, true
			}
		}
		for _, exact := range rule.exact {
			if normalized == exact {
				return rule.icon, true
			}
		}
	}
	return "", false
}

// Normalize lowercases a merchant name and keeps only letters, digits, and
// single spaces. Common Latin accents are folded to ASCII.
func Normalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if folded, ok := accentFold[r]; ok {
			b.WriteRune(folded)
			continue
		}
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '.':
			// Dotted abbreviations such as S.L. should collapse, not split.
			continue
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func normalizeIconReference(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || strings.Contains(value, "://") || strings.Contains(value, "..") {
		return "", false
	}
	value = strings.TrimPrefix(value, "/static/icons/brands/")
	value = strings.TrimSuffix(value, ".svg")
	value = strings.Trim(value, "/")
	if value == "" {
		return "", false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return "", false
	}
	return value, true
}

var accentFold = map[rune]rune{
	'á': 'a', 'à': 'a', 'â': 'a', 'ä': 'a', 'ã': 'a', 'å': 'a',
	'é': 'e', 'è': 'e', 'ê': 'e', 'ë': 'e',
	'í': 'i', 'ì': 'i', 'î': 'i', 'ï': 'i',
	'ó': 'o', 'ò': 'o', 'ô': 'o', 'ö': 'o', 'õ': 'o',
	'ú': 'u', 'ù': 'u', 'û': 'u', 'ü': 'u',
	'ç': 'c', 'ñ': 'n',
}

var brandRules = []brandRule{
	{icon: "googleworkspace", aliases: []string{"google workspace", "googleworkspace", "gsuite", "g suite"}},
	{icon: "aws", aliases: []string{"amazon web services", "amazon web service", "amazonaws", "aws"}},
	{icon: "primevideo", aliases: []string{"amazon prime", "prime video", "primevideo"}},
	{icon: "hbomax", aliases: []string{"hbo max", "hbomax", "hbo"}, exact: []string{"max"}},
	{icon: "youtube", aliases: []string{"youtube"}},
	{icon: "disneyplus", aliases: []string{"disney plus", "disneyplus", "disney"}},
	{icon: "microsoft", aliases: []string{"microsoft", "office 365", "microsoft 365"}},
	{icon: "apple", aliases: []string{"apple music", "apple tv", "apple one", "icloud", "apple", "app store"}},
	{icon: "google", aliases: []string{"google one", "google play", "google"}},
	{icon: "netflix", aliases: []string{"netflix"}},
	{icon: "spotify", aliases: []string{"spotify"}},
	{icon: "github", aliases: []string{"github"}},
	{icon: "cloudflare", aliases: []string{"cloudflare"}},
	{icon: "openai", aliases: []string{"openai", "open ai", "chatgpt"}},
	{icon: "jetbrains", aliases: []string{"jetbrains"}},
	{icon: "vercel", aliases: []string{"vercel"}},
	{icon: "digitalocean", aliases: []string{"digitalocean", "digital ocean"}},
	{icon: "hetzner", aliases: []string{"hetzner"}},
	{icon: "heroku", aliases: []string{"heroku"}},
	{icon: "vodafone", aliases: []string{"vodafone"}},
	{icon: "movistar", aliases: []string{"movistar"}},
	{icon: "orange", aliases: []string{"orange"}},
	{icon: "endesa", aliases: []string{"endesa"}},
	{icon: "iberdrola", aliases: []string{"iberdrola"}},
	{icon: "adobe", aliases: []string{"adobe"}},
	{icon: "slack", aliases: []string{"slack"}},
	{icon: "notion", aliases: []string{"notion"}},
	{icon: "1password", aliases: []string{"1password", "one password", "onepassword"}},
	{icon: "basicfit", aliases: []string{"basic fit", "basicfit"}},
	{icon: "strava", aliases: []string{"strava"}},
	{icon: "audible", aliases: []string{"audible"}},
	{icon: "figma", aliases: []string{"figma"}},
	{icon: "namecheap", aliases: []string{"namecheap"}},
	{icon: "stripe", aliases: []string{"stripe"}},
}
