package categorizer

import (
	"regexp"
	"strings"
)

// Built-in category names shared with the dashboard and budget engine.
const (
	CategoryUncategorized = "Uncategorized"
	CategoryHousing       = "Housing / Rent"
	CategoryP2P           = "Transfers / P2P"
	CategoryBizum         = "Transfers / Bizum"
	CategoryCash          = "Cash / ATM"
	CategorySubscriptions = "Subscriptions"
	CategoryLeisure       = "Leisure & Shopping"
	CategoryRestaurants   = "Restaurants & Delivery"
	CategoryTransport     = "Transport"
	CategoryUtilities     = "Utilities & Telecom"
	CategoryBankFees      = "Bank Fees"
	CategoryGroceries     = "Groceries"
)

// CatalogEntry is one high-confidence deterministic merchant rule. Keywords
// are literal phrases; matching is token based so "Uber Eats" never falls
// through to the broader "Uber" transport rule.
type CatalogEntry struct {
	Category       string
	Keywords       []string
	MaxAmountMinor int64
}

// Pattern renders the entry as a conservative, case-insensitive Go regular
// expression for the legacy rule adapter and configuration migrations.
func (e CatalogEntry) Pattern() string {
	parts := make([]string, 0, len(e.Keywords))
	for _, keyword := range e.Keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" {
			continue
		}
		parts = append(parts, regexp.QuoteMeta(keyword))
	}
	if len(parts) == 0 {
		return ""
	}
	return `(?i)(?:^|[^[:alnum:]])(?:` + strings.Join(parts, "|") + `)(?:$|[^[:alnum:]])`
}

// Catalog is an ordered deterministic matcher.
type Catalog struct {
	entries []compiledCatalogEntry
}

type compiledCatalogEntry struct {
	category       string
	phrases        [][]string
	maxAmountMinor int64
}

// NewCatalog compiles catalog entries in precedence order.
func NewCatalog(entries []CatalogEntry) *Catalog {
	compiled := make([]compiledCatalogEntry, 0, len(entries))
	for _, entry := range entries {
		category := strings.TrimSpace(entry.Category)
		if category == "" {
			continue
		}
		phrases := make([][]string, 0, len(entry.Keywords))
		for _, keyword := range entry.Keywords {
			tokens := Tokenize(keyword)
			if len(tokens) == 0 {
				continue
			}
			phrases = append(phrases, tokens)
		}
		if len(phrases) == 0 {
			continue
		}
		compiled = append(compiled, compiledCatalogEntry{
			category: category, phrases: phrases, maxAmountMinor: entry.MaxAmountMinor,
		})
	}
	return &Catalog{entries: compiled}
}

// Match returns the first deterministic catalog match. Amount caps protect
// generic tokens such as an energy drink from stealing large rent payments.
func (c *Catalog) Match(text string, amountMinor int64) (Result, bool) {
	if c == nil {
		return Result{}, false
	}
	tokens := Tokenize(text)
	if len(tokens) == 0 {
		return Result{}, false
	}
	absoluteAmount := amountMinor
	if absoluteAmount < 0 {
		absoluteAmount = -absoluteAmount
	}
	for _, entry := range c.entries {
		if entry.maxAmountMinor > 0 && amountMinor != 0 && absoluteAmount > entry.maxAmountMinor {
			continue
		}
		for _, phrase := range entry.phrases {
			if containsTokens(tokens, phrase) {
				return Result{
					Category: entry.category, Tier: TierExact, Confidence: 1,
					Matched: strings.Join(phrase, " "),
				}, true
			}
		}
	}
	return Result{}, false
}

// Entries returns the number of compiled catalog entries.
func (c *Catalog) Entries() int {
	if c == nil {
		return 0
	}
	return len(c.entries)
}

func containsTokens(haystack, needle []string) bool {
	if len(needle) == 0 || len(haystack) < len(needle) {
		return false
	}
	for start := 0; start+len(needle) <= len(haystack); start++ {
		match := true
		for offset, token := range needle {
			if haystack[start+offset] != token {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// DefaultCatalog returns the embedded European merchant catalog. Order is
// significant: specific phrases precede broader merchant families.
func DefaultCatalog() []CatalogEntry {
	return []CatalogEntry{
		{Category: CategoryBizum, Keywords: []string{"bizum"}},
		{Category: CategoryCash, Keywords: []string{"cash at", "cajero", "reintegro", "atm withdrawal"}},
		{Category: CategorySubscriptions, Keywords: []string{
			"amazon prime", "amazon web services", "aws", "netflix", "spotify", "google",
			"apple", "cloudflare", "openai", "chatgpt", "github", "jetbrains", "youtube",
			"disney", "hbo", "max", "dazn", "playstation", "xbox", "steam",
			"eweka", "usenet", "newsgroup", "newshosting", "giganews",
		}},
		{Category: CategoryLeisure, Keywords: []string{
			"amazon", "aliexpress", "shein", "temu", "wallapop", "el corte ingles",
			"el corte inglés", "decathlon", "zara", "pull bear", "bershka",
			"stradivarius", "mango", "primark", "ikea", "leroy merlin",
			"mediamarkt", "pc componentes", //nolint:misspell // Spanish merchant name.
		}},
		{Category: CategoryRestaurants, Keywords: []string{
			"glovo", "uber eats", "just eat", "burger king", "mcdonald", "mcdonalds",
			"kfc", "telepizza", "starbucks",
		}},
		{Category: CategoryTransport, Keywords: []string{
			"renfe", "metro", "emt", "tmb", "uber", "cabify", "bolt", "repsol",
			"cepsa", "bp", "shell", "galp", "bicing",
		}},
		{Category: CategoryUtilities, Keywords: []string{
			"iberdrola", "endesa", "naturgy", "totalenergies", "vodafone", "movistar",
			"o2", "orange", "digi", "pepephone", "yoigo", "lowi", "simyo",
			"masmovil", "másmóvil", "canal isabel ii", "agbar",
		}},
		{Category: CategoryBankFees, Keywords: []string{
			"comision", "comisión", "mantenimiento", "cuota tarjeta", //nolint:misspell // Spanish bank-fee wording.
		}},
		{Category: CategoryGroceries, Keywords: []string{
			"mercadona", "carrefour", "lidl", "dia", "día", "alcampo", "eroski",
			"aldi", "consum", "bonpreu", "hipercor", "ahorramas", "ahorramás",
			"coviran", "supermercado",
		}},
		// A sub-ten-euro "Monster" is an energy drink, not a housing charge.
		{Category: CategoryGroceries, Keywords: []string{"monster"}, MaxAmountMinor: microAmountMinor},
	}
}
