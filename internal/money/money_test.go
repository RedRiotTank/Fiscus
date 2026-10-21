package money

import (
	"errors"
	"math"
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		amount   string
		currency string
		want     int64
		wantErr  bool
	}{
		{name: "euros", amount: "12.34", currency: "EUR", want: 1234},
		{name: "whole euros", amount: "12", currency: "EUR", want: 1200},
		{name: "negative", amount: "-0.05", currency: "EUR", want: -5},
		{name: "plus sign", amount: "+7.5", currency: "EUR", want: 750},
		{name: "grouping", amount: "1,234.00", currency: "EUR", want: 123400},
		{name: "leading currency", amount: "EUR 99.99", currency: "EUR", want: 9999},
		{name: "trailing currency", amount: "99.99 EUR", currency: "EUR", want: 9999},
		{name: "yen", amount: "1500", currency: "JPY", want: 1500},
		{name: "yen rejects decimals", amount: "15.5", currency: "JPY", wantErr: true},
		{name: "dinar three decimals", amount: "1.234", currency: "KWD", want: 1234},
		{name: "extra trailing zeros", amount: "1.2300", currency: "EUR", want: 123},
		{name: "extra nonzero precision", amount: "1.234", currency: "EUR", wantErr: true},
		{name: "empty", amount: "", currency: "EUR", wantErr: true},
		{name: "garbage", amount: "abc", currency: "EUR", wantErr: true},
		{name: "multiple dots", amount: "1.2.3", currency: "EUR", wantErr: true},
		{name: "sign only", amount: "-", currency: "EUR", wantErr: true},
		{name: "overflow", amount: "92233720368547758.08", currency: "EUR", wantErr: true},
		{name: "min int", amount: "-92233720368547758.07", currency: "EUR", want: math.MinInt64 + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Parse(tt.amount, tt.currency)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidAmount) {
					t.Fatalf("Parse(%q) error = %v, want ErrInvalidAmount", tt.amount, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.amount, err)
			}
			if got != tt.want {
				t.Fatalf("Parse(%q) = %d, want %d", tt.amount, got, tt.want)
			}
		})
	}
}

func TestFormat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		minor    int64
		currency string
		want     string
	}{
		{1234, "EUR", "12.34"},
		{5, "EUR", "0.05"},
		{-5, "EUR", "-0.05"},
		{0, "EUR", "0.00"},
		{1500, "JPY", "1500"},
		{math.MinInt64, "EUR", "-92233720368547758.08"},
		{1234, "KWD", "1.234"},
	}
	for _, tt := range tests {
		if got := Format(tt.minor, tt.currency); got != tt.want {
			t.Errorf("Format(%d, %s) = %q, want %q", tt.minor, tt.currency, got, tt.want)
		}
	}
}

func TestFormatGrouped(t *testing.T) {
	t.Parallel()
	tests := []struct {
		minor int64
		want  string
	}{
		{0, "0.00"},
		{1234, "12.34"},
		{123400, "1,234.00"},
		{123456789, "1,234,567.89"},
		{-123456789, "-1,234,567.89"},
	}
	for _, tt := range tests {
		if got := FormatGrouped(tt.minor, "EUR"); got != tt.want {
			t.Errorf("FormatGrouped(%d) = %q, want %q", tt.minor, got, tt.want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	for _, minor := range []int64{-123456789, -1, 0, 1, 999, 123456789} {
		formatted := Format(minor, "EUR")
		got, err := Parse(formatted, "EUR")
		if err != nil {
			t.Fatalf("Parse(Format(%d)) error: %v", minor, err)
		}
		if got != minor {
			t.Fatalf("round trip of %d produced %d (%s)", minor, got, formatted)
		}
	}
}
