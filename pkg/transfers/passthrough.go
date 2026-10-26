// Package transfers detects and neutralizes offsetting pass-through movements
// that would otherwise inflate both income and lifestyle spending.
package transfers

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/pkg/categorizer"
)

const (
	// CategoryPassThrough marks both legs of a neutralized mirror transfer.
	CategoryPassThrough = "Transfers / Pass-through"
	// DefaultWindowDays is the rolling pairing window used by the detector.
	DefaultWindowDays = 3
)

// Match is one exact offsetting outflow/inflow pair.
type Match struct {
	Outflow domain.Transaction
	Inflow  domain.Transaction
}

// internalSignalPattern recognises bridge, savings, and internal-transfer
// language that makes an exact offset a likely pass-through.
var internalSignalPattern = regexp.MustCompile(`(?i)\b(?:cuenta remunerada|internal transfer|transferencia interna|traspaso|savings|ahorro|ahorros|bridge|pass[-\s]?through|mirror|offset|logistics|logística)\b`)

// IsPassThroughCategory reports whether a category is the neutral pass-through
// bucket. The matcher tolerates "pass through" and the legacy "Internal Offset"
// alias.
func IsPassThroughCategory(category string) bool {
	normalized := strings.ToLower(strings.TrimSpace(category))
	return strings.Contains(normalized, "pass-through") ||
		strings.Contains(normalized, "pass through") ||
		strings.Contains(normalized, "internal offset")
}

// IsExcluded reports whether a transaction must be removed from budget income
// and spending aggregations.
func IsExcluded(tx domain.Transaction) bool {
	return tx.IsBudgetExcluded || IsPassThroughCategory(tx.Category)
}

// Detect finds exact +A/-A pairs inside a rolling window. At least one leg must
// look like an internal, P2P, bridge, savings, or pass-through movement.
func Detect(transactions []domain.Transaction, windowDays int) []Match {
	if windowDays <= 0 {
		windowDays = DefaultWindowDays
	}
	window := time.Duration(windowDays) * 24 * time.Hour

	debits := make([]domain.Transaction, 0)
	credits := make([]domain.Transaction, 0)
	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked || tx.AmountMinor == 0 {
			continue
		}
		if tx.PassThroughSuppressed || tx.PairedTransactionID != nil {
			continue
		}
		if tx.AmountMinor < 0 {
			debits = append(debits, tx)
		} else {
			credits = append(credits, tx)
		}
	}
	sortTransactions(debits)
	sortTransactions(credits)

	usedCredits := make(map[int]struct{}, len(credits))
	matches := make([]Match, 0)
	for _, debit := range debits {
		best := -1
		bestDistance := window + time.Nanosecond
		for index, credit := range credits {
			if _, used := usedCredits[index]; used {
				continue
			}
			if !strings.EqualFold(debit.Currency, credit.Currency) {
				continue
			}
			if credit.AmountMinor != -debit.AmountMinor {
				continue
			}
			distance := debit.BookingDate.Sub(credit.BookingDate)
			if distance < 0 {
				distance = -distance
			}
			if distance > window || distance >= bestDistance {
				continue
			}
			if !involvesTransfer(debit, credit) {
				continue
			}
			best = index
			bestDistance = distance
		}
		if best < 0 {
			continue
		}
		usedCredits[best] = struct{}{}
		matches = append(matches, Match{Outflow: debit, Inflow: credits[best]})
	}
	return matches
}

func sortTransactions(transactions []domain.Transaction) {
	sort.SliceStable(transactions, func(i, j int) bool {
		if transactions[i].BookingDate.Equal(transactions[j].BookingDate) {
			return transactions[i].ID < transactions[j].ID
		}
		return transactions[i].BookingDate.Before(transactions[j].BookingDate)
	})
}

func involvesTransfer(debit, credit domain.Transaction) bool {
	if debit.AccountID != credit.AccountID {
		return true
	}
	if IsPassThroughCategory(debit.Category) || IsPassThroughCategory(credit.Category) {
		return true
	}
	if isTransferCategory(debit.Category) || isTransferCategory(credit.Category) {
		return true
	}
	return transferText(debit) || transferText(credit)
}

func isTransferCategory(category string) bool {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "transfers", "transfer", "transfers / p2p", "p2p",
		"transfers / bizum", "bizum", "savings", "saving", "internal transfer":
		return true
	default:
		return false
	}
}

func transferText(tx domain.Transaction) bool {
	text := strings.TrimSpace(tx.Counterparty + " " + tx.Description)
	return categorizer.IsP2PText(text) || internalSignalPattern.MatchString(text)
}

// IDs returns the IDs of both legs of every match.
func IDs(matches []Match) map[int64]struct{} {
	out := make(map[int64]struct{}, len(matches)*2)
	for _, match := range matches {
		if match.Outflow.ID > 0 {
			out[match.Outflow.ID] = struct{}{}
		}
		if match.Inflow.ID > 0 {
			out[match.Inflow.ID] = struct{}{}
		}
	}
	return out
}
