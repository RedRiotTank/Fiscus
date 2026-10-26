// Package accrual projects manually tracked savings accounts with an anchor
// balance, APY/TAE, optional tax withholding, and linked transfer flows.
package accrual

import (
	"errors"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

const (
	// CategorySavings is assigned to outflows that fund an accruing account.
	CategorySavings = "Savings / Accruing"
	// CategoryWithdrawal marks inflows from an accruing account as a neutral
	// internal bridge rather than income.
	CategoryWithdrawal = "Transfers / Pass-through"
	// DaysPerYear is the daily-compounding convention used by the engine.
	DaysPerYear = 365.0
)

// Transfer is one linked checking-account movement. Positive amounts increase
// the savings principal; negative amounts reduce it.
type Transfer struct {
	Date        time.Time
	AmountMinor int64
}

// Report is the projected state of an accruing account.
type Report struct {
	BalanceMinor          int64
	LifetimeInterestMinor int64
	MonthInterestMinor    int64
	DaysProjected         int
	APYRate               float64
	NetDailyRate          float64
}

// DailyRate converts an APY/TAE in basis points into the equivalent daily
// effective rate: (1 + APY)^(1/365) - 1.
func DailyRate(apyBasisPoints int) float64 {
	apy := float64(apyBasisPoints) / 10000
	if apy <= -1 {
		return 0
	}
	return math.Pow(1+apy, 1/DaysPerYear) - 1
}

// MonthlyRate converts an APY/TAE into the equivalent monthly effective rate.
func MonthlyRate(apyBasisPoints int) float64 {
	apy := float64(apyBasisPoints) / 10000
	if apy <= -1 {
		return 0
	}
	return math.Pow(1+apy, 1.0/12.0) - 1
}

// NetDailyRate applies optional tax withholding to the daily rate.
func NetDailyRate(apyBasisPoints, taxWithholdingBPS int) float64 {
	return DailyRate(apyBasisPoints) * (1 - normalizedTaxRate(taxWithholdingBPS))
}

// NetMonthlyRate applies optional tax withholding to the monthly rate.
func NetMonthlyRate(apyBasisPoints, taxWithholdingBPS int) float64 {
	return MonthlyRate(apyBasisPoints) * (1 - normalizedTaxRate(taxWithholdingBPS))
}

func normalizedTaxRate(taxWithholdingBPS int) float64 {
	if taxWithholdingBPS <= 0 {
		return 0
	}
	if taxWithholdingBPS >= 10000 {
		return 1
	}
	return float64(taxWithholdingBPS) / 10000
}

// Project computes the account balance and interest at asOf. Interest accrues
// on each day's starting balance, then same-day linked transfers are applied.
// Floating point is confined to the engine; results are rounded to cents only
// when returned.
func Project(account domain.AccruingAccount, transfers []Transfer, asOf time.Time) Report {
	anchor := dateOnly(account.AnchorDate)
	target := dateOnly(asOf)
	if target.Before(anchor) {
		target = anchor
	}
	frequency := strings.ToLower(strings.TrimSpace(account.CompoundingFrequency))
	if frequency != domain.AccruingMonthly {
		frequency = domain.AccruingDaily
	}
	dailyRate := NetDailyRate(account.APYRateBasisPoints, account.TaxWithholdingBPS)
	monthlyRate := NetMonthlyRate(account.APYRateBasisPoints, account.TaxWithholdingBPS)
	report := Report{
		APYRate:      float64(account.APYRateBasisPoints) / 10000,
		NetDailyRate: dailyRate,
	}
	balance := float64(account.AnchorBalanceCents)
	lifetime := 0.0
	monthInterest := 0.0
	byDay := make(map[string]float64, len(transfers))
	for _, transfer := range transfers {
		day := dateOnly(transfer.Date)
		if day.Before(anchor) {
			continue
		}
		byDay[day.Format("2006-01-02")] += float64(transfer.AmountMinor)
	}

	for day := anchor; day.Before(target); day = day.AddDate(0, 0, 1) {
		dailyYield := 0.0
		switch frequency {
		case domain.AccruingMonthly:
			if day.AddDate(0, 0, 1).Month() != day.Month() {
				dailyYield = balance * monthlyRate
			}
		default:
			dailyYield = balance * dailyRate
		}
		balance += dailyYield
		lifetime += dailyYield
		if day.Year() == target.Year() && day.Month() == target.Month() {
			monthInterest += dailyYield
		}
		balance += byDay[day.Format("2006-01-02")]
		report.DaysProjected++
	}
	// Movements on the target date affect the current balance but not interest
	// for a period that ends at the start of that date.
	balance += byDay[target.Format("2006-01-02")]

	report.BalanceMinor = int64(math.Round(balance))
	report.LifetimeInterestMinor = int64(math.Round(lifetime))
	report.MonthInterestMinor = int64(math.Round(monthInterest))
	return report
}

// CurrentBalance is a convenience wrapper around Project.
func CurrentBalance(account domain.AccruingAccount, transfers []Transfer, asOf time.Time) int64 {
	return Project(account, transfers, asOf).BalanceMinor
}

// ComputeBalance is an alias for CurrentBalance used by integration callers.
func ComputeBalance(account domain.AccruingAccount, transfers []Transfer, asOf time.Time) int64 {
	return CurrentBalance(account, transfers, asOf)
}

// AccruedInterest returns lifetime and current-month interest for a target
// date.
func AccruedInterest(account domain.AccruingAccount, transfers []Transfer, asOf time.Time) (lifetimeMinor, monthMinor int64) {
	report := Project(account, transfers, asOf)
	return report.LifetimeInterestMinor, report.MonthInterestMinor
}

// TransfersFromTransactions converts matching booked checking-account
// transactions into principal movements. Savings inflows (positive checking
// amounts) reduce the accruing principal.
func TransfersFromTransactions(account domain.AccruingAccount, transactions []domain.Transaction) ([]Transfer, error) {
	pattern := strings.TrimSpace(account.TransferMatchPattern)
	if pattern == "" {
		return nil, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, errors.New("accrual: invalid transfer match pattern: " + err.Error())
	}
	out := make([]Transfer, 0)
	for _, tx := range transactions {
		if (tx.Status != domain.TransactionStatusBooked && tx.Status != "") || tx.AmountMinor == 0 {
			continue
		}
		if account.Currency != "" && tx.Currency != "" && !strings.EqualFold(tx.Currency, account.Currency) {
			continue
		}
		text := strings.TrimSpace(tx.Counterparty + " " + tx.Description)
		if !re.MatchString(text) {
			continue
		}
		if dateOnly(tx.BookingDate).Before(dateOnly(account.AnchorDate)) {
			continue
		}
		out = append(out, Transfer{Date: dateOnly(tx.BookingDate), AmountMinor: -tx.AmountMinor})
	}
	return out, nil
}

// CompilePattern validates a stored regex and returns its compiled form.
func CompilePattern(pattern string) (*regexp.Regexp, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, errors.New("accrual: invalid transfer match pattern: " + err.Error())
	}
	return re, nil
}

// KeywordPattern converts a user-friendly transfer keyword into a
// case-insensitive regex. Raw regex values are passed through.
func KeywordPattern(keyword string) (string, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return "", nil
	}
	if strings.HasPrefix(keyword, "(?") || strings.ContainsAny(keyword, `\[](){}*+?|^$`) {
		if _, err := regexp.Compile(keyword); err != nil {
			return "", errors.New("accrual: invalid transfer match pattern: " + err.Error())
		}
		return keyword, nil
	}
	return "(?i)" + regexp.QuoteMeta(keyword), nil
}

// CategoryForTransaction returns the budget category for a matched transfer.
func CategoryForTransaction(amountMinor int64) (category string, budgetExcluded bool) {
	if amountMinor >= 0 {
		return CategoryWithdrawal, true
	}
	return CategorySavings, false
}

func dateOnly(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}
