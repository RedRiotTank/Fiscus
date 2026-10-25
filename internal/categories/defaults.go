package categories

// DefaultRules returns the embedded Spanish and European merchant fallback
// ruleset. Callers should place custom and learned rules before these defaults.
func DefaultRules() []Rule {
	return []Rule{
		{Category: "Transfers / Bizum", Pattern: `(?i)\bbizum\b`},
		{Category: "Subscriptions", Pattern: `(?i)(?:amazon prime|amazon web services|\baws\b|netflix|spotify|\bgoogle\b|apple|cloudflare|openai|chatgpt|github|jetbrains|youtube|disney|hbo|\bmax\b|dazn|playstation|xbox|steam)`},
		{Category: "Leisure & Shopping", Pattern: `(?i)(?:amazon|aliexpress|shein|temu|wallapop|el corte ingl[eé]s|decathlon|zara|pull\s*&?\s*bear|bershka|stradivarius|mango|primark|ikea|leroy merlin|mediamarkt|pc\s*c[o]mponentes)`},
		{Category: "Restaurants & Delivery", Pattern: `(?i)(?:glovo|uber eats|just eat|burger king|mcdonald'?s|\bkfc\b|telepizza|starbucks)`},
		{Category: "Transport", Pattern: `(?i)(?:renfe|\bmetro\b|\bemt\b|\btmb\b|\buber\b|cabify|\bbolt\b|repsol|cepsa|\bbp\b|shell|galp|bicing)`},
		{Category: "Utilities & Telecom", Pattern: `(?i)(?:iberdrola|endesa|naturgy|totalenergies|vodafone|movistar|\bo2\b|orange|\bdigi\b|pepephone|yoigo|lowi|simyo|masmovil|másmóvil|canal isabel ii|agbar)`},
		{Category: "Bank Fees", Pattern: `(?i)(?:comisi[oó]n|mantenimiento|cuota tarjeta)`},
		{Category: "Groceries", Pattern: `(?i)(?:mercadona|carrefour|lidl|\bd[ií]a\b|alcampo|eroski|\baldi\b|consum|bonpreu|hipercor|ahorramas|ahorramás|coviran)`},
	}
}
