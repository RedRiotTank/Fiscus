package categories

import (
	"regexp"
	"strings"
)

// LearnedPattern builds a case-insensitive regex that tolerates common accent
// variants for a merchant or counterparty name.
func LearnedPattern(merchant string) string {
	var b strings.Builder
	b.WriteString(`(?i)`)
	for _, r := range merchant {
		switch r {
		case 'a', 'á', 'à', 'ä', 'â', 'ã':
			b.WriteString("[aáàäâã]")
		case 'e', 'é', 'è', 'ë', 'ê':
			b.WriteString("[eéèëê]")
		case 'i', 'í', 'ì', 'ï', 'î':
			b.WriteString("[iíìïî]")
		case 'o', 'ó', 'ò', 'ö', 'ô', 'õ':
			b.WriteString("[oóòöôõ]")
		case 'u', 'ú', 'ù', 'ü', 'û':
			b.WriteString("[uúùüû]")
		case 'n', 'ñ':
			b.WriteString("[nñ]")
		case 'c', 'ç':
			b.WriteString("[cç]")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	return b.String()
}
