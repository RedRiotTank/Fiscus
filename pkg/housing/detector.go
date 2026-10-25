package housing

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/pkg/subscriptions"
)

// Detection bounds for realistic Spanish household rent in minor units.
const (
	MinMasterRentMinor int64 = 40_000
	MaxMasterRentMinor int64 = 300_000
	MinFlatmateMinor   int64 = 5_000
	MaxFlatmateMinor   int64 = 150_000
	detectionMonths          = 6
)

var (
	flatmateConceptPattern = regexp.MustCompile(`(?i)(?:\bpiso\b|\balquiler\b|\brent\b|\brenta\b|\bhabitaci[oó]n\b)`)
	incomePattern          = regexp.MustCompile(`(?i)(?:\bn[oó]mina\b|\bsalary\b|\bpayroll\b|\bsueldo\b)`)
	subscriptionPattern    = regexp.MustCompile(`(?i)(?:netflix|spotify|amazon|google|apple|disney|hbo|dazn|youtube|openai|chatgpt|github|cloudflare|adobe|jetbrains)`)
)

// RentCandidate is one detected master-rent or flatmate counterparty.
type RentCandidate struct {
	Counterparty string
	AmountMinor  int64
	Currency     string
	Occurrences  int
	LastSeen     time.Time
	Descriptions []string
}

// Detection is the result of scanning recent month-edge transactions for a
// shared-flat rent pattern.
type Detection struct {
	Found              bool
	Landlord           RentCandidate
	Flatmates          []RentCandidate
	GrossRentMinor     int64
	FlatmateTotalMinor int64
	NetShareMinor      int64
	Currency           string
}

type candidateAccumulator struct {
	candidate  RentCandidate
	lastAmount int64
	lastSeen   time.Time
	score      int
}

// Detect scans the last six month-edge windows (day 25 through day 5) for a
// likely master rent payment and the flatmate transfers that offset it.
func Detect(transactions []domain.Transaction, now time.Time) Detection {
	now = now.UTC()
	cutoff := now.AddDate(0, -detectionMonths, 0)
	masters := make(map[string]*candidateAccumulator)
	flatmates := make(map[string]*candidateAccumulator)

	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked || tx.BookingDate.After(now) ||
			tx.BookingDate.Before(cutoff) || !monthEdge(tx.BookingDate) {
			continue
		}
		currency := currencyOf(tx.Currency)
		merchant := subscriptions.CleanMerchant(tx.Counterparty)
		if merchant == "" {
			merchant = subscriptions.CleanMerchant(tx.Description)
		}
		if merchant == "" {
			continue
		}
		key := strings.ToLower(currency + "\x00" + merchant)
		if tx.AmountMinor < 0 {
			amount := -tx.AmountMinor
			if amount < MinMasterRentMinor || amount > MaxMasterRentMinor ||
				!masterCategoryAllowed(tx.Category) || subscriptionPattern.MatchString(tx.Counterparty+" "+tx.Description) {
				continue
			}
			accumulate(masters, key, tx, merchant, amount, currency)
			masters[key].score += masterTextScore(tx)
			continue
		}
		if tx.AmountMinor < MinFlatmateMinor || tx.AmountMinor > MaxFlatmateMinor ||
			incomePattern.MatchString(tx.Counterparty+" "+tx.Description) || incomeCategory(tx.Category) {
			continue
		}
		accumulate(flatmates, key, tx, merchant, tx.AmountMinor, currency)
		if flatmateConceptPattern.MatchString(tx.Counterparty + " " + tx.Description) {
			flatmates[key].score += 120
		}
	}

	masterList := rankedCandidates(masters)
	if len(masterList) == 0 {
		return Detection{}
	}
	master := masterList[0]
	detection := Detection{
		Found:          true,
		Landlord:       master,
		GrossRentMinor: master.AmountMinor,
		Currency:       master.Currency,
	}
	flatmateList := make([]RentCandidate, 0)
	for _, candidate := range rankedCandidates(flatmates) {
		if candidate.Currency != master.Currency || strings.EqualFold(candidate.Counterparty, master.Counterparty) {
			continue
		}
		accumulator := flatmates[strings.ToLower(candidate.Currency+"\x00"+candidate.Counterparty)]
		// Repeated transfers from the same contact are a strong flatmate
		// signal even when the bank note has no housing keyword.
		if candidate.AmountMinor >= master.AmountMinor ||
			(accumulator != nil && accumulator.candidate.Occurrences < 2 && accumulator.score < 120) {
			continue
		}
		flatmateList = append(flatmateList, candidate)
		detection.FlatmateTotalMinor += candidate.AmountMinor
	}
	detection.Flatmates = flatmateList
	detection.NetShareMinor = master.AmountMinor - detection.FlatmateTotalMinor
	if detection.NetShareMinor < 0 {
		detection.NetShareMinor = 0
	}
	return detection
}

func monthEdge(date time.Time) bool {
	day := date.Day()
	return day >= 25 || day <= 5
}

func accumulate(group map[string]*candidateAccumulator, key string, tx domain.Transaction, merchant string, amount int64, currency string) {
	accumulator := group[key]
	if accumulator == nil {
		accumulator = &candidateAccumulator{
			candidate: RentCandidate{Counterparty: merchant, Currency: currency},
		}
		group[key] = accumulator
	}
	accumulator.candidate.Occurrences++
	accumulator.score += 40
	if accumulator.lastSeen.IsZero() || tx.BookingDate.After(accumulator.lastSeen) {
		accumulator.lastSeen = tx.BookingDate
		accumulator.lastAmount = amount
		accumulator.candidate.LastSeen = tx.BookingDate
	}
	if description := strings.TrimSpace(tx.Description); description != "" && len(accumulator.candidate.Descriptions) < 3 {
		accumulator.candidate.Descriptions = append(accumulator.candidate.Descriptions, description)
	}
	accumulator.candidate.AmountMinor = accumulator.lastAmount
}

func rankedCandidates(group map[string]*candidateAccumulator) []RentCandidate {
	out := make([]RentCandidate, 0, len(group))
	for _, accumulator := range group {
		candidate := accumulator.candidate
		candidate.AmountMinor = accumulator.lastAmount
		candidate.LastSeen = accumulator.lastSeen
		out = append(out, candidate)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := group[strings.ToLower(out[i].Currency+"\x00"+out[i].Counterparty)]
		right := group[strings.ToLower(out[j].Currency+"\x00"+out[j].Counterparty)]
		leftScore, rightScore := 0, 0
		if left != nil {
			leftScore = left.score
		}
		if right != nil {
			rightScore = right.score
		}
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if out[i].AmountMinor != out[j].AmountMinor {
			return out[i].AmountMinor > out[j].AmountMinor
		}
		return out[i].Counterparty < out[j].Counterparty
	})
	return out
}

func masterCategoryAllowed(category string) bool {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "subscriptions", "groceries", "restaurants", "restaurants & delivery", "dining",
		"leisure & shopping", "utilities", "utilities & telecom", "transport",
		"bank fees", "payroll", "tax", "taxes", "savings", "investments":
		return false
	default:
		return true
	}
}

func incomeCategory(category string) bool {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "payroll", "salary", "income", "revenue", "sales":
		return true
	default:
		return false
	}
}

func masterTextScore(tx domain.Transaction) int {
	text := strings.ToLower(tx.Counterparty + " " + tx.Description)
	switch {
	case strings.Contains(text, "alquiler"), strings.Contains(text, "rent"), strings.Contains(text, "piso"),
		strings.Contains(text, "hipoteca"), strings.Contains(text, "landlord"):
		return 100
	case strings.Contains(text, "transfer"), strings.Contains(text, "transferencia"):
		return 30
	default:
		return 0
	}
}

func currencyOf(currency string) string {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return "EUR"
	}
	return currency
}
