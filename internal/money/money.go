// Package money provides exact parsing and formatting of monetary amounts using
// integer minor units. It intentionally avoids float64.
package money

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ErrInvalidAmount is returned when an amount cannot be parsed exactly.
var ErrInvalidAmount = errors.New("invalid monetary amount")

// zeroScaleCurrencies lists ISO 4217 currencies without a minor unit.
var zeroScaleCurrencies = map[string]struct{}{
	"BIF": {}, "CLP": {}, "DJF": {}, "GNF": {}, "ISK": {}, "JPY": {}, "KMF": {},
	"KRW": {}, "PYG": {}, "RWF": {}, "UGX": {}, "UYI": {}, "VND": {}, "VUV": {},
	"XAF": {}, "XOF": {}, "XPF": {},
}

// threeScaleCurrencies lists ISO 4217 currencies with three decimal digits.
var threeScaleCurrencies = map[string]struct{}{
	"BHD": {}, "IQD": {}, "JOD": {}, "KWD": {}, "LYD": {}, "OMR": {}, "TND": {},
}

// Scale returns the number of decimal digits for a currency. Unknown
// currencies default to two digits, matching the vast majority of ISO 4217.
func Scale(currency string) int {
	c := strings.ToUpper(strings.TrimSpace(currency))
	if _, ok := zeroScaleCurrencies[c]; ok {
		return 0
	}
	if _, ok := threeScaleCurrencies[c]; ok {
		return 3
	}
	return 2
}

// Parse converts a decimal string such as "12.34", "-0.05", or "1,234.00" into
// minor units for the given currency. It rejects values with more precision than
// the currency allows and values that are not valid decimals.
func Parse(amount, currency string) (int64, error) {
	s := strings.TrimSpace(amount)
	if s == "" {
		return 0, fmt.Errorf("%w: empty value", ErrInvalidAmount)
	}
	s = strings.ReplaceAll(s, ",", "")

	// Accept a leading or trailing currency code, e.g. "EUR 12.34" or "12.34 EUR".
	c := strings.ToUpper(strings.TrimSpace(currency))
	upper := strings.ToUpper(s)
	if c != "" {
		if strings.HasPrefix(upper, c+" ") {
			s = strings.TrimSpace(s[len(c):])
		} else if strings.HasSuffix(upper, " "+c) {
			s = strings.TrimSpace(s[:len(s)-len(c)])
		}
	}

	sign := int64(1)
	switch {
	case strings.HasPrefix(s, "-"):
		sign = -1
		s = s[1:]
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	}
	if s == "" || strings.Count(s, ".") > 1 {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, amount)
	}

	integerPart, fractionPart, hasFraction := strings.Cut(s, ".")
	if integerPart == "" && fractionPart == "" {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, amount)
	}
	if integerPart == "" {
		integerPart = "0"
	}
	if !allDigits(integerPart) || !allDigits(fractionPart) {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, amount)
	}

	scale := Scale(currency)
	if hasFraction {
		if len(fractionPart) > scale {
			// Allow extra trailing zeroes, but reject meaningful extra precision.
			for _, r := range fractionPart[scale:] {
				if r != '0' {
					return 0, fmt.Errorf("%w: %q has more than %d decimal places", ErrInvalidAmount, amount, scale)
				}
			}
			fractionPart = fractionPart[:scale]
		}
		fractionPart += strings.Repeat("0", scale-len(fractionPart))
	} else {
		fractionPart = strings.Repeat("0", scale)
	}

	whole, err := strconv.ParseInt(integerPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q overflows int64", ErrInvalidAmount, amount)
	}
	pow := int64(1)
	for i := 0; i < scale; i++ {
		pow *= 10
	}
	maxWhole := math.MaxInt64 / pow
	maxFrac := math.MaxInt64 % pow
	if whole > maxWhole {
		return 0, fmt.Errorf("%w: %q overflows int64", ErrInvalidAmount, amount)
	}
	var frac int64
	if fractionPart != "" {
		frac, err = strconv.ParseInt(fractionPart, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, amount)
		}
	}
	if whole == maxWhole && frac > maxFrac {
		return 0, fmt.Errorf("%w: %q overflows int64", ErrInvalidAmount, amount)
	}
	minor := whole*pow + frac
	if sign < 0 {
		minor = -minor
	}
	return minor, nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Format renders minor units as a decimal string without a currency symbol,
// using the currency's scale. It never introduces floating point errors.
func Format(minor int64, currency string) string {
	scale := Scale(currency)
	sign := ""
	// #nosec G115 -- int64 and uint64 have the same width; the sign is handled below.
	u := uint64(minor)
	if minor < 0 {
		sign = "-"
		// Handle math.MinInt64 without overflow.
		// #nosec G115 -- negating before conversion keeps the magnitude representable.
		u = uint64(-(minor + 1)) + 1
	}
	if scale == 0 {
		return sign + strconv.FormatUint(u, 10)
	}
	pow := uint64(1)
	for i := 0; i < scale; i++ {
		pow *= 10
	}
	whole := u / pow
	frac := u % pow
	return fmt.Sprintf("%s%d.%0*d", sign, whole, scale, frac)
}

// FormatGrouped is like Format but inserts thousands separators in the integer
// portion, e.g. "-1,234.56".
func FormatGrouped(minor int64, currency string) string {
	plain := Format(minor, currency)
	sign := ""
	if strings.HasPrefix(plain, "-") {
		sign = "-"
		plain = plain[1:]
	}
	whole, frac, hasFrac := strings.Cut(plain, ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := sign + b.String()
	if hasFrac {
		out += "." + frac
	}
	return out
}
