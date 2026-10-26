// Package categorizer implements Fiscus' pure-Go, three-tier transaction
// categorizer. Tier one applies deterministic catalog and user rules, tier two
// uses a lightweight in-memory naive Bayes model, and tier three returns a
// conservative review-queue fallback instead of guessing.
package categorizer

import (
	"strings"
	"unicode"
)

// Feature prefixes keep word, bigram, flow, and amount features in separate
// namespaces so they cannot collide in the model.
const (
	featureWord   = "w:"
	featureBigram = "b:"
	featureFlow   = "f:"
	featureAmount = "a:"
)

// Amount bucket boundaries in integer minor units (euro cents).
const (
	microAmountMinor  = 1_000  // < €10
	smallAmountMinor  = 5_000  // €10 - €50
	mediumAmountMinor = 20_000 // €50 - €200
)

var accentFold = map[rune]rune{
	'á': 'a', 'à': 'a', 'ä': 'a', 'â': 'a', 'ã': 'a', 'å': 'a',
	'é': 'e', 'è': 'e', 'ë': 'e', 'ê': 'e',
	'í': 'i', 'ì': 'i', 'ï': 'i', 'î': 'i',
	'ó': 'o', 'ò': 'o', 'ö': 'o', 'ô': 'o', 'õ': 'o',
	'ú': 'u', 'ù': 'u', 'ü': 'u', 'û': 'u',
	'ñ': 'n', 'ç': 'c',
}

// Tokenize lowercases, folds common Spanish accents, removes punctuation, and
// returns sanitized word tokens. Pure numeric tokens are dropped because exact
// amounts are represented by the dedicated amount-bucket feature.
func Tokenize(text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}
	fields := strings.FieldsFunc(text, func(r rune) bool {
		if folded, ok := accentFold[r]; ok {
			r = folded
		}
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		field = foldToken(field)
		if field == "" || numericOnly(field) {
			continue
		}
		if len([]rune(field)) < 2 {
			continue
		}
		tokens = append(tokens, field)
	}
	return tokens
}

func foldToken(token string) string {
	var b strings.Builder
	b.Grow(len(token))
	for _, r := range strings.ToLower(token) {
		if folded, ok := accentFold[r]; ok {
			r = folded
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func numericOnly(token string) bool {
	for _, r := range token {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return token != ""
}

// Features expands a transaction description into word, bigram, flow, and
// amount-bucket features.
func Features(text string, amountMinor int64) []string {
	tokens := Tokenize(text)
	features := make([]string, 0, len(tokens)*2+2)
	for _, token := range tokens {
		features = append(features, featureWord+token)
	}
	for i := 0; i+1 < len(tokens); i++ {
		features = append(features, featureBigram+tokens[i]+" "+tokens[i+1])
	}
	features = append(features, flowFeature(amountMinor), AmountBucket(amountMinor))
	return features
}

func flowFeature(amountMinor int64) string {
	switch {
	case amountMinor > 0:
		return featureFlow + "in"
	case amountMinor < 0:
		return featureFlow + "out"
	default:
		return featureFlow + "unknown"
	}
}

// AmountBucket returns the normalized amount feature used by the classifier.
// Buckets are < €10, €10 - €50, €50 - €200, and > €200.
func AmountBucket(amountMinor int64) string {
	if amountMinor == 0 {
		return featureAmount + "unknown"
	}
	amount := amountMinor
	if amount < 0 {
		amount = -amount
	}
	switch {
	case amount < microAmountMinor:
		return featureAmount + "lt10"
	case amount < smallAmountMinor:
		return featureAmount + "10to50"
	case amount < mediumAmountMinor:
		return featureAmount + "50to200"
	default:
		return featureAmount + "gt200"
	}
}
