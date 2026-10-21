package config

import (
	"fmt"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/money"
)

// LiquidityThresholdsMinor parses configured per-currency thresholds into
// integer minor units.
func (c *Config) LiquidityThresholdsMinor() (map[string]int64, error) {
	out := make(map[string]int64, len(c.Alerts.LiquidityThresholds))
	for rawCurrency, rawAmount := range c.Alerts.LiquidityThresholds {
		currency := strings.ToUpper(strings.TrimSpace(rawCurrency))
		if currency == "" {
			return nil, fmt.Errorf("liquidity threshold has an empty currency")
		}
		minor, err := money.Parse(rawAmount, currency)
		if err != nil {
			return nil, fmt.Errorf("%s threshold %q: %w", currency, rawAmount, err)
		}
		if minor <= 0 {
			return nil, fmt.Errorf("%s threshold must be positive", currency)
		}
		out[currency] = minor
	}
	return out, nil
}
