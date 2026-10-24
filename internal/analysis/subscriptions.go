// Package analysis detects recurring subscriptions, bank fees, and price
// changes from stored transactions. All monetary calculations use integer
// minor units.
package analysis

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// Frequency classifies a recurring charge period.
type Frequency string

const (
	// Monthly is roughly a 30-day billing cycle.
	Monthly Frequency = "monthly"
	// Yearly is roughly a 365-day billing cycle.
	Yearly Frequency = "yearly"
)

const (
	monthlyMinDays = 25
	monthlyMaxDays = 35
	yearlyMinDays  = 350
	yearlyMaxDays  = 380

	// A subscription becomes inactive after this many days without a charge.
	monthlyStaleDays = 45
	yearlyStaleDays  = 390
)

// Subscription is a detected recurring outgoing charge.
type Subscription struct {
	AccountID           string
	Counterparty        string
	Category            string
	Currency            string
	Frequency           Frequency
	IntervalDays        int
	Occurrences         int
	AmountMinor         int64
	PreviousAmountMinor int64
	PriceIncreaseMinor  int64
	PriceChangedAt      time.Time
	FirstCharge         time.Time
	LastCharge          time.Time
	NextExpected        time.Time
	Active              bool
}

// MonthlyMinor returns the estimated monthly cost in minor units.
func (s Subscription) MonthlyMinor() int64 {
	if s.Frequency == Yearly {
		return (s.AmountMinor + 6) / 12
	}
	return s.AmountMinor
}

// AnnualMinor returns the estimated annualized cost in minor units.
func (s Subscription) AnnualMinor() int64 {
	if s.Frequency == Yearly {
		return s.AmountMinor
	}
	return s.AmountMinor * 12
}

// Fee is a detected bank commission or charge.
type Fee struct {
	domain.Transaction
	Label string
}

// CurrencyAmount is a per-currency aggregate.
type CurrencyAmount struct {
	Currency string
	Minor    int64
}

// Snapshot is the result of analyzing a transaction set.
type Snapshot struct {
	Subscriptions []Subscription
	Fees          []Fee
	MonthlyCosts  []CurrencyAmount
	AnnualCosts   []CurrencyAmount
	FeeTotals     []CurrencyAmount
}

type feePattern struct {
	label   string
	pattern *regexp.Regexp
}

// Spanish and English patterns are kept together so the detector works across
// the supported bank feeds. Unicode escapes avoid encoding issues.
var defaultFeePatterns = []feePattern{
	{label: "Maintenance fee", pattern: regexp.MustCompile(`(?i)comisi(?:o|\x{f3})n|mantenimiento|maintenance fee|account fee`)},
	{label: "Overdraft fee", pattern: regexp.MustCompile(`(?i)descubierto|overdraft|insufficient funds`)},
	{label: "Claim fee", pattern: regexp.MustCompile(`(?i)reclamaci\x{f3}n|reclamacion|claim fee`)},
	{label: "Debit interest", pattern: regexp.MustCompile(`(?i)intereses?\s+deudor(?:es)?|debit interest`)},
	{label: "Bank fee", pattern: regexp.MustCompile(`(?i)gastos\s+bancarios|bank charges?|service charge`)},
}

// Analyze inspects booked transactions and returns active subscriptions, fee
// charges, and their currency totals. now should be in UTC.
func Analyze(transactions []domain.Transaction, now time.Time) Snapshot {
	now = now.UTC()
	byMerchant := make(map[string][]domain.Transaction)
	fees := make([]Fee, 0)
	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked || tx.AmountMinor >= 0 {
			continue
		}
		if fee, ok := detectFee(tx); ok {
			fees = append(fees, fee)
		}
		if !isSubscriptionCategory(tx.Category) {
			continue
		}
		merchant := transactionMerchant(tx)
		if merchant == "" {
			continue
		}
		key := strings.Join([]string{tx.AccountID, strings.ToUpper(tx.Currency), merchant}, "\x00")
		byMerchant[key] = append(byMerchant[key], tx)
	}

	subscriptions := make([]Subscription, 0, len(byMerchant))
	for _, group := range byMerchant {
		subscription, ok := detectSubscription(group, now)
		if ok {
			subscriptions = append(subscriptions, subscription)
		}
	}
	sort.SliceStable(subscriptions, func(i, j int) bool {
		if subscriptions[i].LastCharge.Equal(subscriptions[j].LastCharge) {
			return subscriptions[i].Counterparty < subscriptions[j].Counterparty
		}
		return subscriptions[i].LastCharge.After(subscriptions[j].LastCharge)
	})

	sort.SliceStable(fees, func(i, j int) bool {
		if fees[i].BookingDate.Equal(fees[j].BookingDate) {
			return fees[i].ID > fees[j].ID
		}
		return fees[i].BookingDate.After(fees[j].BookingDate)
	})

	monthly := make(map[string]int64)
	annual := make(map[string]int64)
	for _, subscription := range subscriptions {
		if !subscription.Active {
			continue
		}
		currency := strings.ToUpper(subscription.Currency)
		monthly[currency] += subscription.MonthlyMinor()
		annual[currency] += subscription.AnnualMinor()
	}
	feeTotals := make(map[string]int64)
	for _, fee := range fees {
		feeTotals[strings.ToUpper(fee.Currency)] += -fee.AmountMinor
	}

	return Snapshot{
		Subscriptions: subscriptions,
		Fees:          fees,
		MonthlyCosts:  currencyAmounts(monthly),
		AnnualCosts:   currencyAmounts(annual),
		FeeTotals:     currencyAmounts(feeTotals),
	}
}

func detectSubscription(group []domain.Transaction, now time.Time) (Subscription, bool) {
	if len(group) < 2 {
		return Subscription{}, false
	}
	sort.SliceStable(group, func(i, j int) bool {
		if group[i].BookingDate.Equal(group[j].BookingDate) {
			return group[i].ID < group[j].ID
		}
		return group[i].BookingDate.Before(group[j].BookingDate)
	})

	gaps := make([]int, 0, len(group)-1)
	for i := 1; i < len(group); i++ {
		gap := int(group[i].BookingDate.Sub(group[i-1].BookingDate).Hours()/24 + 0.5)
		if gap < 0 {
			return Subscription{}, false
		}
		gaps = append(gaps, gap)
	}
	monthly, yearly := true, true
	for _, gap := range gaps {
		if gap < monthlyMinDays || gap > monthlyMaxDays {
			monthly = false
		}
		if gap < yearlyMinDays || gap > yearlyMaxDays {
			yearly = false
		}
	}
	if !monthly && !yearly {
		return Subscription{}, false
	}
	frequency := Monthly
	interval := average(gaps)
	maxAgeDays := monthlyStaleDays
	if yearly {
		frequency = Yearly
		interval = average(gaps)
		maxAgeDays = yearlyStaleDays
	}

	last := group[len(group)-1]
	current := -last.AmountMinor
	previous := current
	priceChangedAt := time.Time{}
	for i := len(group) - 2; i >= 0; i-- {
		amount := -group[i].AmountMinor
		if amount != current {
			previous = amount
			priceChangedAt = group[i+1].BookingDate
			break
		}
	}
	priceIncrease := current - previous
	ageDays := int(now.Sub(last.BookingDate).Hours() / 24)
	active := !last.BookingDate.After(now) && ageDays <= maxAgeDays
	next := nextExpected(last.BookingDate, frequency)
	return Subscription{
		AccountID:           last.AccountID,
		Counterparty:        transactionMerchant(last),
		Category:            strings.TrimSpace(last.Category),
		Currency:            strings.ToUpper(last.Currency),
		Frequency:           frequency,
		IntervalDays:        interval,
		Occurrences:         len(group),
		AmountMinor:         current,
		PreviousAmountMinor: previous,
		PriceIncreaseMinor:  priceIncrease,
		PriceChangedAt:      priceChangedAt,
		FirstCharge:         group[0].BookingDate,
		LastCharge:          last.BookingDate,
		NextExpected:        next,
		Active:              active,
	}, true
}

func nextExpected(last time.Time, frequency Frequency) time.Time {
	if frequency == Yearly {
		return last.AddDate(1, 0, 0)
	}
	return last.AddDate(0, 1, 0)
}

func average(values []int) int {
	if len(values) == 0 {
		return 0
	}
	total := 0
	for _, value := range values {
		total += value
	}
	return (total + len(values)/2) / len(values)
}

func isSubscriptionCategory(category string) bool {
	category = strings.ToLower(strings.TrimSpace(category))
	if category == "" {
		// Imported or legacy rows may not carry a category; keep detecting
		// them as before.
		return true
	}
	switch category {
	case "uncategorized", "groceries", "restaurants", "dining", "shopping",
		"leisure & shopping", "rent", "housing", "transfer", "transfers",
		"emergency fund", "savings", "saving", "investments", "investment",
		"bank fees", "payroll", "meals", "travel", "hardware", "office",
		"office & equipment", "tax reserve", "taxes", "tax":
		return false
	default:
		return true
	}
}

func transactionMerchant(tx domain.Transaction) string {
	merchant := strings.TrimSpace(tx.Counterparty)
	if merchant == "" {
		merchant = strings.TrimSpace(tx.Description)
	}
	return strings.TrimSpace(merchant)
}

func detectFee(tx domain.Transaction) (Fee, bool) {
	text := strings.TrimSpace(tx.Counterparty + " " + tx.Description)
	for _, candidate := range defaultFeePatterns {
		if candidate.pattern.MatchString(text) {
			return Fee{Transaction: tx, Label: candidate.label}, true
		}
	}
	return Fee{}, false
}

func currencyAmounts(values map[string]int64) []CurrencyAmount {
	currencies := make([]string, 0, len(values))
	for currency := range values {
		if currency == "" {
			currency = "EUR"
		}
		currencies = append(currencies, currency)
	}
	sort.Strings(currencies)
	out := make([]CurrencyAmount, 0, len(currencies))
	for _, currency := range currencies {
		out = append(out, CurrencyAmount{Currency: currency, Minor: values[currency]})
	}
	return out
}

// IncreasePercent returns the integer percentage increase from previous to
// current. It returns 0 when previous is not positive.
func IncreasePercent(current, previous int64) int64 {
	if previous <= 0 {
		return 0
	}
	return (current - previous) * 100 / previous
}

// Description renders a compact subscription description for logs and alerts.
func (s Subscription) Description() string {
	return fmt.Sprintf("%s (%s, %d days)", s.Counterparty, s.Frequency, s.IntervalDays)
}
