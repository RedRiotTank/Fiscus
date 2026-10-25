// Package recurring detects repeating monthly debits that behave like shared
// bills or recurring household expenses and calculates the user's effective
// personal share.
package recurring

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/pkg/subscriptions"
)

const (
	// MinOccurrences is the number of distinct months required before a debit is
	// treated as a recurring bill candidate.
	MinOccurrences = 2
	// detectionMonths bounds the lookback used by Detect.
	detectionMonths = 6
	// edgeVarianceDays is the allowed distance from the start or end of a month.
	edgeVarianceDays = 3
	// minAmountToleranceMinor keeps small bills from being rejected because of
	// cent-level bank rounding.
	minAmountToleranceMinor = 50
	// amountToleranceBPS allows up to 3% amount drift between months.
	amountToleranceBPS = 300
)

var (
	saasPattern = regexp.MustCompile(`(?i)(?:netflix|spotify|amazon prime|amazon web services|\baws\b|disney|hbo|\bmax\b|dazn|youtube|openai|chatgpt|github|cloudflare|adobe|jetbrains|dropbox|notion|figma|microsoft\s*365|google\s*(?:one|play))`)
	// housingPattern mirrors the rent vocabulary so housing is left to the
	// dedicated shared-rent engine.
	housingPattern          = regexp.MustCompile(`(?i)(?:\balquiler\b|\brenta\b|\bpiso\b|\bhipoteca\b|\bmortgage\b|\brent\b|\blandlord\b|\barrendador\b)`)
	excludedCategoryPattern = regexp.MustCompile(`(?i)(?:housing|rent|subscription|software|cloud|\bsaas\b|bank fee|payroll|income|salary|savings|investment|emergency fund|vault|tax)`)
)

// Candidate is one repeating monthly debit that may be a shared bill.
type Candidate struct {
	Counterparty string
	AccountID    string
	AmountMinor  int64
	Currency     string
	Occurrences  int
	TypicalDay   int
	LastSeen     time.Time
	Category     string
	Descriptions []string
}

type amountMonth struct {
	month     string
	amount    int64
	day       int
	seen      time.Time
	accountID string
}

type accumulator struct {
	counterparty string
	currency     string
	category     string
	byMonth      map[string]amountMonth
	descriptions []string
}

// Detect scans recent month-edge debits for repeating payments to the same
// counterparty with stable amounts. Housing rent and SaaS subscriptions are
// intentionally excluded because dedicated engines handle them.
func Detect(transactions []domain.Transaction, now time.Time) []Candidate {
	now = now.UTC()
	cutoff := now.AddDate(0, -detectionMonths, 0)
	groups := make(map[string]*accumulator)

	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked || tx.AmountMinor >= 0 ||
			tx.BookingDate.After(now) || tx.BookingDate.Before(cutoff) ||
			!nearMonthEdge(tx.BookingDate) {
			continue
		}
		if excludedTransaction(tx) {
			continue
		}
		merchant := subscriptions.CleanMerchant(tx.Counterparty)
		if merchant == "" {
			merchant = subscriptions.CleanMerchant(tx.Description)
		}
		if merchant == "" {
			continue
		}
		currency := currencyOf(tx.Currency)
		key := strings.ToLower(currency + "\x00" + merchant)
		group := groups[key]
		if group == nil {
			group = &accumulator{
				counterparty: merchant,
				currency:     currency,
				category:     categoryOf(tx.Category),
				byMonth:      make(map[string]amountMonth),
			}
			groups[key] = group
		}
		booking := tx.BookingDate.UTC()
		month := booking.Format("2006-01")
		amount := -tx.AmountMinor
		current, exists := group.byMonth[month]
		if !exists || booking.After(current.seen) {
			group.byMonth[month] = amountMonth{
				month: month, amount: amount, day: booking.Day(), seen: booking,
				accountID: tx.AccountID,
			}
		}
		if description := strings.TrimSpace(tx.Description); description != "" && len(group.descriptions) < 3 {
			group.descriptions = append(group.descriptions, description)
		}
	}

	candidates := make([]Candidate, 0, len(groups))
	for _, group := range groups {
		candidate, ok := evaluate(group)
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Occurrences != candidates[j].Occurrences {
			return candidates[i].Occurrences > candidates[j].Occurrences
		}
		if candidates[i].AmountMinor != candidates[j].AmountMinor {
			return candidates[i].AmountMinor > candidates[j].AmountMinor
		}
		return strings.ToLower(candidates[i].Counterparty) < strings.ToLower(candidates[j].Counterparty)
	})
	return candidates
}

func evaluate(group *accumulator) (Candidate, bool) {
	if len(group.byMonth) < MinOccurrences {
		return Candidate{}, false
	}
	months := make([]amountMonth, 0, len(group.byMonth))
	for _, entry := range group.byMonth {
		months = append(months, entry)
	}
	sort.SliceStable(months, func(i, j int) bool {
		return months[i].month < months[j].month
	})
	for i := 1; i < len(months); i++ {
		gap := monthDistance(months[i-1].month, months[i].month)
		if gap < 1 || gap > 2 {
			return Candidate{}, false
		}
	}
	minAmount, maxAmount := months[0].amount, months[0].amount
	for _, month := range months[1:] {
		if month.amount < minAmount {
			minAmount = month.amount
		}
		if month.amount > maxAmount {
			maxAmount = month.amount
		}
	}
	tolerance := maxAmount * amountToleranceBPS / 10000
	if tolerance < minAmountToleranceMinor {
		tolerance = minAmountToleranceMinor
	}
	if maxAmount-minAmount > tolerance {
		return Candidate{}, false
	}
	last := months[len(months)-1]
	days := make([]int, 0, len(months))
	for _, month := range months {
		days = append(days, month.day)
	}
	sort.Ints(days)
	return Candidate{
		Counterparty: group.counterparty,
		AccountID:    last.accountID,
		AmountMinor:  last.amount,
		Currency:     group.currency,
		Occurrences:  len(months),
		TypicalDay:   days[len(days)/2],
		LastSeen:     last.seen,
		Category:     group.category,
		Descriptions: append([]string(nil), group.descriptions...),
	}, true
}

func monthDistance(from, to string) int {
	left, err := time.Parse("2006-01", from)
	if err != nil {
		return 0
	}
	right, err := time.Parse("2006-01", to)
	if err != nil {
		return 0
	}
	return (right.Year()-left.Year())*12 + int(right.Month()-left.Month())
}

func nearMonthEdge(date time.Time) bool {
	day := date.Day()
	lastDay := time.Date(date.Year(), date.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return day <= 1+edgeVarianceDays || day >= lastDay-edgeVarianceDays
}

func excludedTransaction(tx domain.Transaction) bool {
	text := tx.Counterparty + " " + tx.Description
	if housingPattern.MatchString(text) || saasPattern.MatchString(text) {
		return true
	}
	category := strings.ToLower(strings.TrimSpace(tx.Category))
	switch category {
	case "transfer", "transfers", "internal transfer", "bank transfer":
		return true
	}
	return excludedCategoryPattern.MatchString(category)
}

func categoryOf(category string) string {
	category = strings.TrimSpace(category)
	if category == "" {
		return "Uncategorized"
	}
	return category
}

func currencyOf(currency string) string {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return "EUR"
	}
	return currency
}

// PersonalShare returns the rounded personal share of a gross bill split
// between people. For example, 1600 minor units split between 3 people returns
// 533.
func PersonalShare(grossMinor int64, people int) int64 {
	if people < 1 {
		people = 1
	}
	if grossMinor <= 0 {
		return 0
	}
	return (grossMinor + int64(people)/2) / int64(people)
}

// MatchesCounterparty reports whether a transaction belongs to the confirmed
// recurring counterparty, using the same merchant normalization as detection.
func MatchesCounterparty(tx domain.Transaction, counterparty string) bool {
	target := strings.TrimSpace(counterparty)
	if target == "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(tx.Counterparty), target) {
		return true
	}
	normalizedTarget := normalizeCounterparty(target)
	if normalizedTarget == "" {
		return false
	}
	if normalizeCounterparty(tx.Counterparty) == normalizedTarget {
		return true
	}
	return normalizeCounterparty(tx.Description) == normalizedTarget
}

func normalizeCounterparty(value string) string {
	normalized := strings.ToLower(subscriptions.CleanMerchant(value))
	normalized = strings.TrimSpace(strings.TrimPrefix(normalized, "bizum "))
	return normalized
}
