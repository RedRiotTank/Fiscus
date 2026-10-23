// Package tax estimates quarterly VAT and income-tax obligations and derives
// the true liquid operating capital ("caja limpia") for business workspaces.
package tax

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// Store is the persistence surface required by the tax estimator.
type Store interface {
	ListAccounts(ctx context.Context) ([]domain.Account, error)
	ListBusinessTransactionsBetween(ctx context.Context, from, to time.Time) ([]domain.Transaction, error)
}

// Config controls tax estimation rates and fiscal quarter starts.
type Config struct {
	VATRateDefault     float64
	IncomeTaxRetention float64
	QuarterStartMonths []int
}

// Quarter describes one fiscal quarter.
type Quarter struct {
	Start        time.Time
	EndExclusive time.Time
	End          time.Time
}

// AlertWindow reports whether now is within the 14 days before quarter-end.
func (q Quarter) AlertWindow(now time.Time) bool {
	threshold := q.End.AddDate(0, 0, -14)
	return !now.Before(threshold) && now.Before(q.EndExclusive)
}

// Estimate is the tax segregation for one currency and quarter.
type Estimate struct {
	Quarter                     Quarter
	Currency                    string
	TaxableIncomeGrossMinor     int64
	TaxableIncomeBaseMinor      int64
	DeductibleExpenseGrossMinor int64
	DeductibleExpenseBaseMinor  int64
	VATCollectedMinor           int64
	VATPaidMinor                int64
	VATDueMinor                 int64
	IncomeTaxMinor              int64
	EstimatedTaxDueMinor        int64
}

// Shield combines the tax estimate with business account liquidity.
type Shield struct {
	Estimate
	TotalBalanceMinor      int64
	TaxReserveMinor        int64
	TrueLiquidCapitalMinor int64
}

// Service computes quarterly tax estimates from business transactions.
type Service struct {
	store        Store
	vatRateBps   int64
	retentionBps int64
	months       []int
	now          func() time.Time
}

// New constructs a tax estimator.
func New(store Store, cfg Config, now func() time.Time) (*Service, error) {
	if store == nil {
		return nil, errors.New("tax: store is required")
	}
	if now == nil {
		now = time.Now
	}
	months := append([]int(nil), cfg.QuarterStartMonths...)
	if len(months) == 0 {
		months = []int{1, 4, 7, 10}
	}
	if len(months) != 4 {
		return nil, errors.New("tax: exactly four quarter start months are required")
	}
	seen := make(map[int]struct{}, 4)
	for _, month := range months {
		if month < 1 || month > 12 {
			return nil, fmt.Errorf("tax: invalid quarter start month %d", month)
		}
		if _, exists := seen[month]; exists {
			return nil, fmt.Errorf("tax: duplicate quarter start month %d", month)
		}
		seen[month] = struct{}{}
	}
	sort.Ints(months)
	if cfg.VATRateDefault < 0 || cfg.VATRateDefault >= 1 {
		return nil, errors.New("tax: VAT rate must be between 0 and 1")
	}
	if cfg.IncomeTaxRetention < 0 || cfg.IncomeTaxRetention >= 1 {
		return nil, errors.New("tax: income-tax retention must be between 0 and 1")
	}
	return &Service{
		store:        store,
		vatRateBps:   int64(math.Round(cfg.VATRateDefault * 10000)),
		retentionBps: int64(math.Round(cfg.IncomeTaxRetention * 10000)),
		months:       months,
		now:          now,
	}, nil
}

// QuarterFor returns the fiscal quarter containing now.
func (s *Service) QuarterFor(now time.Time) Quarter {
	now = now.UTC()
	year, month := now.Year(), int(now.Month())
	index := -1
	for i, startMonth := range s.months {
		if startMonth <= month {
			index = i
		}
	}
	var startYear, startMonth, nextYear, nextMonth int
	if index >= 0 {
		startYear, startMonth = year, s.months[index]
		if index+1 < len(s.months) {
			nextYear, nextMonth = year, s.months[index+1]
		} else {
			nextYear, nextMonth = year+1, s.months[0]
		}
	} else {
		startYear, startMonth = year-1, s.months[len(s.months)-1]
		nextYear, nextMonth = year, s.months[0]
	}
	start := time.Date(startYear, time.Month(startMonth), 1, 0, 0, 0, 0, time.UTC)
	endExclusive := time.Date(nextYear, time.Month(nextMonth), 1, 0, 0, 0, 0, time.UTC)
	return Quarter{Start: start, EndExclusive: endExclusive, End: endExclusive.AddDate(0, 0, -1)}
}

// Shields returns tax shields for the current quarter.
func (s *Service) Shields(ctx context.Context) ([]Shield, error) {
	return s.ShieldsAt(ctx, s.now())
}

// ShieldsAt returns tax shields for the quarter containing now.
func (s *Service) ShieldsAt(ctx context.Context, now time.Time) ([]Shield, error) {
	quarter := s.QuarterFor(now)
	accounts, err := s.store.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	transactions, err := s.store.ListBusinessTransactionsBetween(ctx, quarter.Start, quarter.EndExclusive)
	if err != nil {
		return nil, err
	}

	balances := make(map[string]int64)
	for _, account := range accounts {
		if domain.NormalizeAccountType(account.Type) != domain.AccountTypeBusiness {
			continue
		}
		balances[currencyOf(account.Currency)] += account.BalanceMinor
	}
	byCurrency := make(map[string]*Estimate)
	ensure := func(currency string) *Estimate {
		estimate, ok := byCurrency[currency]
		if !ok {
			estimate = &Estimate{Quarter: quarter, Currency: currency}
			byCurrency[currency] = estimate
		}
		return estimate
	}
	for currency := range balances {
		ensure(currency)
	}
	for _, tx := range transactions {
		if !isTaxableCategory(tx.Category) {
			continue
		}
		estimate := ensure(currencyOf(tx.Currency))
		if tx.AmountMinor > 0 {
			estimate.TaxableIncomeGrossMinor += tx.AmountMinor
		} else if tx.AmountMinor < 0 {
			estimate.DeductibleExpenseGrossMinor += -tx.AmountMinor
		}
	}

	currencies := make([]string, 0, len(byCurrency))
	for currency := range byCurrency {
		currencies = append(currencies, currency)
	}
	sort.Strings(currencies)
	out := make([]Shield, 0, len(currencies))
	for _, currency := range currencies {
		estimate := byCurrency[currency]
		estimate.TaxableIncomeBaseMinor = baseFromGross(estimate.TaxableIncomeGrossMinor, s.vatRateBps)
		estimate.DeductibleExpenseBaseMinor = baseFromGross(estimate.DeductibleExpenseGrossMinor, s.vatRateBps)
		estimate.VATCollectedMinor = estimate.TaxableIncomeGrossMinor - estimate.TaxableIncomeBaseMinor
		estimate.VATPaidMinor = estimate.DeductibleExpenseGrossMinor - estimate.DeductibleExpenseBaseMinor
		estimate.VATDueMinor = max(0, estimate.VATCollectedMinor-estimate.VATPaidMinor)
		profit := estimate.TaxableIncomeBaseMinor - estimate.DeductibleExpenseBaseMinor
		if profit > 0 {
			estimate.IncomeTaxMinor = roundedTax(profit, s.retentionBps)
		}
		estimate.EstimatedTaxDueMinor = estimate.VATDueMinor + estimate.IncomeTaxMinor
		total := balances[currency]
		out = append(out, Shield{
			Estimate:               *estimate,
			TotalBalanceMinor:      total,
			TaxReserveMinor:        estimate.EstimatedTaxDueMinor,
			TrueLiquidCapitalMinor: total - estimate.EstimatedTaxDueMinor,
		})
	}
	return out, nil
}

func baseFromGross(grossMinor, rateBps int64) int64 {
	if grossMinor <= 0 {
		return 0
	}
	denominator := 10000 + rateBps
	return (grossMinor*10000 + denominator/2) / denominator
}

func roundedTax(baseMinor, rateBps int64) int64 {
	if baseMinor <= 0 || rateBps <= 0 {
		return 0
	}
	return (baseMinor*rateBps + 5000) / 10000
}

func currencyOf(currency string) string {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return "EUR"
	}
	return currency
}

func isTaxableCategory(category string) bool {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "transfer", "transfers", "internal transfer", "traspaso", "traspasos",
		"tax", "taxes", "impuestos", "irpf", "vat", "iva":
		return false
	default:
		return true
	}
}
