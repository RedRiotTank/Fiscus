// Package subscriptions provides merchant normalization and real-world
// subscription heuristics shared by the analysis and web layers.
package subscriptions

import (
	"strings"
	"unicode"
)

var legalSuffixes = map[string]struct{}{
	"ab": {}, "sa": {}, "sl": {}, "s a": {}, "s l": {}, "s.a": {}, "s.l": {},
	"gmbh": {}, "ltd": {}, "limited": {}, "inc": {}, "llc": {}, "bv": {}, "nv": {},
	"sas": {}, "sarl": {}, "ag": {}, "plc": {}, "uab": {}, "corp": {}, "corporation": {},
	"company": {}, "co": {},
}

var commonTLDs = []string{".com", ".net", ".org", ".io", ".co", ".es", ".eu", ".uk", ".de", ".fr", ".it", ".nl", ".pt"}

// CleanMerchant strips dynamic card references, asterisks, hashes, legal
// suffixes, and common web prefixes while preserving a readable merchant root.
func CleanMerchant(raw string) string {
	value := strings.TrimSpace(strings.ReplaceAll(raw, "\u00a0", " "))
	if value == "" {
		return ""
	}
	if index := strings.IndexAny(value, "*#"); index >= 0 {
		value = value[:index]
	}
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	for _, prefix := range []string{"http://", "https://", "www."} {
		if strings.HasPrefix(strings.ToLower(fields[0]), prefix) {
			fields[0] = fields[0][len(prefix):]
		}
	}
	for len(fields) > 1 {
		last := trimToken(fields[len(fields)-1])
		lower := strings.ToLower(last)
		if last == "" {
			fields = fields[:len(fields)-1]
			continue
		}
		if _, ok := legalSuffixes[lower]; ok {
			fields = fields[:len(fields)-1]
			continue
		}
		if isDynamicToken(last, len(fields)) {
			fields = fields[:len(fields)-1]
			continue
		}
		break
	}
	if len(fields) > 0 {
		fields[len(fields)-1] = stripTLD(trimToken(fields[len(fields)-1]))
	}
	cleaned := strings.Join(fields, " ")
	cleaned = strings.Trim(cleaned, " \t-_.,;:/\\|()[]{}")
	normalized := strings.ToLower(cleaned)
	switch {
	case strings.Contains(normalized, "google one") || normalized == "google" || strings.HasPrefix(normalized, "google "):
		return "Google"
	case strings.Contains(normalized, "amazon prime"):
		return "Amazon Prime"
	case strings.Contains(normalized, "amazon web service") || strings.Contains(normalized, "amazonaws") || normalized == "aws":
		return "Amazon Web Services"
	case normalized == "amazon" || strings.HasPrefix(normalized, "amazon.") || strings.HasPrefix(normalized, "amazon "):
		return "Amazon"
	case strings.Contains(normalized, "spotify"):
		return "Spotify"
	case strings.Contains(normalized, "netflix"):
		return "Netflix"
	case strings.Contains(normalized, "apple"):
		return "Apple"
	case strings.Contains(normalized, "microsoft"):
		return "Microsoft"
	case strings.Contains(normalized, "github"):
		return "GitHub"
	default:
		return cleaned
	}
}

// MerchantKey returns the case-insensitive grouping key for a merchant.
func MerchantKey(raw string) string {
	return strings.ToLower(CleanMerchant(raw))
}

func trimToken(token string) string {
	return strings.Trim(token, " \t-_.,;:/\\|()[]{}")
}

func isDynamicToken(token string, fieldCount int) bool {
	if fieldCount <= 1 {
		return false
	}
	token = trimToken(token)
	if len(token) < 6 {
		return false
	}
	var digits, letters bool
	for _, r := range token {
		switch {
		case unicode.IsDigit(r):
			digits = true
		case unicode.IsLetter(r):
			letters = true
		}
	}
	if digits && letters {
		return true
	}
	if digits && len(token) >= 8 {
		return true
	}
	return false
}

func stripTLD(token string) string {
	lower := strings.ToLower(token)
	for _, suffix := range commonTLDs {
		if strings.HasSuffix(lower, suffix) && len(token) > len(suffix)+1 {
			return token[:len(token)-len(suffix)]
		}
	}
	return token
}
