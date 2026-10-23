// Package tax estimates quarterly VAT and income-tax obligations and derives
// the true liquid operating capital ("caja limpia") for business workspaces.
package tax

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// Store is the persistence surface required by the tax estimator.
type Store interface {
	ListAccounts(ctx context.Context) ([]domain.Account, error)
	ListBusinessTransactionsBetween(ctx context.Context, from, to time.Time) ([]domain.Transaction, error)
}

// CategoryConfig assigns a VAT rate and income-tax deductibility to one
// transaction category. Rates and percentages are decimal fractions (0.21 =
// 21%, 1.00 = 100%).
type CategoryConfig struct {
	Name              string
	VATRate           float64
	DeductiblePercent float64
}

// Config controls tax estimation rates and fiscal quarter starts.
type Config struct {
	VATRateDefault     float64
	IncomeTaxRetention float64
	QuarterStartMonths []int
	Categories         []CategoryConfig
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

// CategoryLine is the VAT and income-tax breakdown for one category.
type CategoryLine struct {
	Category                    string
	VATRateBps                  int64
	DeductiblePercentBps        int64
	TaxableIncomeGrossMinor     int64
	TaxableIncomeBaseMinor      int64
	ExpenseGrossMinor           int64
	DeductibleExpenseGrossMinor int64
	DeductibleExpenseBaseMinor  int64
	VATCollectedMinor           int64
	VATPaidMinor                int64
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
	VATCarryForwardMinor        int64
	VATDueMinor                 int64
	VATCreditMinor              int64
	IncomeTaxMinor              int64
	EstimatedTaxDueMinor        int64
	Lines                       []CategoryLine
}

// Shield combines the tax estimate with business account liquidity.
type Shield struct {
	Estimate
	TotalBalanceMinor      int64
	TaxReserveMinor        int64
	TrueLiquidCapitalMinor int64
}

type categoryRate struct {
	name          string
	vatRateBps    int64
	deductibleBps int64
}

// Service computes quarterly tax estimates from business transactions.
type Service struct {
	store        Store
	vatRateBps   int64
	retentionBps int64
	months       []int
	categories   map[string]categoryRate
	defaultRate  categoryRate
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
	defaultRate := categoryRate{
		name:          "Default",
		vatRateBps:    rateBps(cfg.VATRateDefault),
		deductibleBps: 10000,
	}
	categories := make(map[string]categoryRate, len(cfg.Categories))
	for i, category := range cfg.Categories {
		name := strings.TrimSpace(category.Name)
		if name == "" {
			return nil, fmt.Errorf("tax: category %d name is required", i)
		}
		if category.VATRate < 0 || category.VATRate >= 1 {
			return nil, fmt.Errorf("tax: category %q VAT rate must be between 0 and 1", name)
		}
		if category.DeductiblePercent < 0 || category.DeductiblePercent > 1 {
			return nil, fmt.Errorf("tax: category %q deductible percent must be between 0 and 1", name)
		}
		key := strings.ToLower(name)
		if _, exists := categories[key]; exists {
			return nil, fmt.Errorf("tax: duplicate category %q", name)
		}
		categories[key] = categoryRate{
			name:          name,
			vatRateBps:    rateBps(category.VATRate),
			deductibleBps: rateBps(category.DeductiblePercent),
		}
	}
	return &Service{
		store:        store,
		vatRateBps:   defaultRate.vatRateBps,
		retentionBps: rateBps(cfg.IncomeTaxRetention),
		months:       months,
		categories:   categories,
		defaultRate:  defaultRate,
		now:          now,
	}, nil
}

func rateBps(rate float64) int64 {
	return int64(math.Round(rate * 10000))
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

// QuarterNumber returns the 1-based fiscal quarter index for q.
func (s *Service) QuarterNumber(q Quarter) int {
	for i, month := range s.months {
		if int(q.Start.Month()) == month {
			return i + 1
		}
	}
	return 1
}

// Shields returns tax shields for the current quarter.
func (s *Service) Shields(ctx context.Context) ([]Shield, error) {
	return s.ShieldsAt(ctx, s.now())
}

// ShieldsAt returns tax shields for the quarter containing now.
func (s *Service) ShieldsAt(ctx context.Context, now time.Time) ([]Shield, error) {
	return s.shieldsForQuarter(ctx, s.QuarterFor(now))
}

// ShieldsForQuarter returns tax shields for a calendar year and 1-based fiscal
// quarter.
func (s *Service) ShieldsForQuarter(ctx context.Context, year, quarter int) ([]Shield, error) {
	if quarter < 1 || quarter > 4 {
		return nil, fmt.Errorf("tax: quarter must be between 1 and 4")
	}
	start := time.Date(year, time.Month(s.months[quarter-1]), 1, 0, 0, 0, 0, time.UTC)
	return s.shieldsForQuarter(ctx, s.QuarterFor(start))
}

func (s *Service) shieldsForQuarter(ctx context.Context, quarter Quarter) ([]Shield, error) {
	accounts, err := s.store.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	carryStart := s.QuarterFor(time.Date(quarter.Start.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)).Start
	if carryStart.After(quarter.Start) {
		carryStart = quarter.Start
	}
	transactions, err := s.store.ListBusinessTransactionsBetween(ctx, carryStart, quarter.EndExclusive)
	if err != nil {
		return nil, err
	}

	balances := make(map[string]int64)
	currencies := make(map[string]struct{})
	for _, account := range accounts {
		if domain.NormalizeAccountType(account.Type) != domain.AccountTypeBusiness {
			continue
		}
		currency := currencyOf(account.Currency)
		balances[currency] += account.BalanceMinor
		currencies[currency] = struct{}{}
	}
	byQuarter := make(map[time.Time][]domain.Transaction)
	for _, tx := range transactions {
		currency := currencyOf(tx.Currency)
		currencies[currency] = struct{}{}
		q := s.QuarterFor(tx.BookingDate)
		byQuarter[q.Start] = append(byQuarter[q.Start], tx)
	}

	currenciesList := make([]string, 0, len(currencies))
	for currency := range currencies {
		currenciesList = append(currenciesList, currency)
	}
	sort.Strings(currenciesList)

	out := make([]Shield, 0, len(currenciesList))
	for _, currency := range currenciesList {
		carry := int64(0)
		var current Estimate
		for q := s.QuarterFor(carryStart); !q.Start.After(quarter.Start); q = s.QuarterFor(q.EndExclusive) {
			estimate := s.estimateFor(byQuarter[q.Start], currency, q, carry)
			if q.Start.Equal(quarter.Start) {
				current = estimate
				break
			}
			carry = estimate.VATCreditMinor
			if q.EndExclusive.After(quarter.EndExclusive) {
				break
			}
		}
		if current.Quarter.Start.IsZero() {
			current = s.estimateFor(nil, currency, quarter, carry)
		}
		total := balances[currency]
		out = append(out, Shield{
			Estimate:               current,
			TotalBalanceMinor:      total,
			TaxReserveMinor:        current.EstimatedTaxDueMinor,
			TrueLiquidCapitalMinor: total - current.EstimatedTaxDueMinor,
		})
	}
	return out, nil
}

func (s *Service) estimateFor(transactions []domain.Transaction, currency string, quarter Quarter, carry int64) Estimate {
	linesByKey := make(map[string]*CategoryLine)
	for _, tx := range transactions {
		if currencyOf(tx.Currency) != currency || !isTaxableCategory(tx.Category) || tx.AmountMinor == 0 {
			continue
		}
		display, rate := s.categoryFor(tx.Category)
		key := strings.ToLower(display)
		line := linesByKey[key]
		if line == nil {
			line = &CategoryLine{
				Category:             display,
				VATRateBps:           rate.vatRateBps,
				DeductiblePercentBps: rate.deductibleBps,
			}
			linesByKey[key] = line
		}
		if tx.AmountMinor > 0 {
			line.TaxableIncomeGrossMinor += tx.AmountMinor
		} else {
			line.ExpenseGrossMinor += -tx.AmountMinor
		}
	}

	keys := make([]string, 0, len(linesByKey))
	for key := range linesByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	estimate := Estimate{Quarter: quarter, Currency: currency, VATCarryForwardMinor: carry}
	lines := make([]CategoryLine, 0, len(keys))
	for _, key := range keys {
		line := *linesByKey[key]
		line.TaxableIncomeBaseMinor = baseFromGross(line.TaxableIncomeGrossMinor, line.VATRateBps)
		line.VATCollectedMinor = line.TaxableIncomeGrossMinor - line.TaxableIncomeBaseMinor
		line.DeductibleExpenseGrossMinor = percentageOf(line.ExpenseGrossMinor, line.DeductiblePercentBps)
		line.DeductibleExpenseBaseMinor = baseFromGross(line.DeductibleExpenseGrossMinor, line.VATRateBps)
		line.VATPaidMinor = line.DeductibleExpenseGrossMinor - line.DeductibleExpenseBaseMinor
		lines = append(lines, line)

		estimate.TaxableIncomeGrossMinor += line.TaxableIncomeGrossMinor
		estimate.TaxableIncomeBaseMinor += line.TaxableIncomeBaseMinor
		estimate.DeductibleExpenseGrossMinor += line.DeductibleExpenseGrossMinor
		estimate.DeductibleExpenseBaseMinor += line.DeductibleExpenseBaseMinor
		estimate.VATCollectedMinor += line.VATCollectedMinor
		estimate.VATPaidMinor += line.VATPaidMinor
	}
	estimate.Lines = lines

	available := estimate.VATCollectedMinor - estimate.VATPaidMinor - estimate.VATCarryForwardMinor
	if available >= 0 {
		estimate.VATDueMinor = available
		estimate.VATCreditMinor = 0
	} else {
		estimate.VATDueMinor = 0
		estimate.VATCreditMinor = -available
	}
	profit := estimate.TaxableIncomeBaseMinor - estimate.DeductibleExpenseBaseMinor
	if profit > 0 {
		estimate.IncomeTaxMinor = roundedTax(profit, s.retentionBps)
	}
	estimate.EstimatedTaxDueMinor = estimate.VATDueMinor + estimate.IncomeTaxMinor
	return estimate
}

func (s *Service) categoryFor(category string) (string, categoryRate) {
	display := strings.TrimSpace(category)
	if display == "" {
		display = "Uncategorized"
	}
	if rate, ok := s.categories[strings.ToLower(display)]; ok {
		return rate.name, rate
	}
	return display, s.defaultRate
}

func baseFromGross(grossMinor, rateBps int64) int64 {
	if grossMinor <= 0 {
		return 0
	}
	denominator := 10000 + rateBps
	return (grossMinor*10000 + denominator/2) / denominator
}

func percentageOf(value, percentBps int64) int64 {
	if value <= 0 || percentBps <= 0 {
		return 0
	}
	if percentBps >= 10000 {
		return value
	}
	return (value*percentBps + 5000) / 10000
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

// SummaryCSV renders a category-level tax filing summary. Amounts are decimal
// major units; rates and deductibility are decimal fractions.
func SummaryCSV(shields []Shield) ([]byte, error) {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	header := []string{
		"quarter_start", "quarter_end", "currency", "category", "vat_rate",
		"deductible_percent", "taxable_income_gross", "taxable_income_base",
		"expense_gross", "deductible_expense_gross", "deductible_expense_base",
		"vat_collected", "vat_paid", "vat_carry_forward", "vat_due", "vat_credit",
		"income_tax", "estimated_tax_due",
	}
	if err := writer.Write(header); err != nil {
		return nil, err
	}
	for _, shield := range shields {
		start := shield.Quarter.Start.Format("2006-01-02")
		end := shield.Quarter.End.Format("2006-01-02")
		for _, line := range shield.Lines {
			row := []string{
				start, end, shield.Currency, line.Category,
				formatRate(line.VATRateBps), formatRate(line.DeductiblePercentBps),
				money.Format(line.TaxableIncomeGrossMinor, shield.Currency),
				money.Format(line.TaxableIncomeBaseMinor, shield.Currency),
				money.Format(line.ExpenseGrossMinor, shield.Currency),
				money.Format(line.DeductibleExpenseGrossMinor, shield.Currency),
				money.Format(line.DeductibleExpenseBaseMinor, shield.Currency),
				money.Format(line.VATCollectedMinor, shield.Currency),
				money.Format(line.VATPaidMinor, shield.Currency),
				"", "", "", "", "",
			}
			if err := writer.Write(row); err != nil {
				return nil, err
			}
		}
		row := []string{
			start, end, shield.Currency, "TOTAL", "", "",
			money.Format(shield.TaxableIncomeGrossMinor, shield.Currency),
			money.Format(shield.TaxableIncomeBaseMinor, shield.Currency),
			money.Format(expenseGross(shield), shield.Currency),
			money.Format(shield.DeductibleExpenseGrossMinor, shield.Currency),
			money.Format(shield.DeductibleExpenseBaseMinor, shield.Currency),
			money.Format(shield.VATCollectedMinor, shield.Currency),
			money.Format(shield.VATPaidMinor, shield.Currency),
			money.Format(shield.VATCarryForwardMinor, shield.Currency),
			money.Format(shield.VATDueMinor, shield.Currency),
			money.Format(shield.VATCreditMinor, shield.Currency),
			money.Format(shield.IncomeTaxMinor, shield.Currency),
			money.Format(shield.EstimatedTaxDueMinor, shield.Currency),
		}
		if err := writer.Write(row); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func expenseGross(shield Shield) int64 {
	var total int64
	for _, line := range shield.Lines {
		total += line.ExpenseGrossMinor
	}
	return total
}

func formatRate(bps int64) string {
	return fmt.Sprintf("%.4f", float64(bps)/10000)
}
