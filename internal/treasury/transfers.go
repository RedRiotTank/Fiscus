// Package treasury computes burn rate, runway, internal transfer pairs, and
// plain-text accounting exports. Monetary values remain integer minor units.
package treasury

import (
	"sort"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// TransferPair is an offsetting debit and credit between two own accounts.
type TransferPair struct {
	Debit  domain.Transaction
	Credit domain.Transaction
}

// PairTransfers finds booked, equal-and-opposite movements in different
// accounts with booking dates no more than windowDays apart. It greedily pairs
// the closest dates first and never reuses a transaction.
func PairTransfers(transactions []domain.Transaction, windowDays int) []TransferPair {
	if windowDays <= 0 {
		windowDays = 3
	}
	window := time.Duration(windowDays) * 24 * time.Hour

	var debits, credits []domain.Transaction
	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked {
			continue
		}
		if tx.AmountMinor < 0 {
			debits = append(debits, tx)
		} else if tx.AmountMinor > 0 {
			credits = append(credits, tx)
		}
	}
	sort.SliceStable(debits, func(i, j int) bool { return debits[i].BookingDate.Before(debits[j].BookingDate) })
	sort.SliceStable(credits, func(i, j int) bool { return credits[i].BookingDate.Before(credits[j].BookingDate) })

	usedCredits := make(map[int]bool, len(credits))
	pairs := make([]TransferPair, 0)
	for _, debit := range debits {
		best := -1
		bestDistance := window + time.Nanosecond
		for i, credit := range credits {
			if usedCredits[i] || debit.AccountID == credit.AccountID {
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
			if distance > window {
				continue
			}
			if distance < bestDistance {
				best = i
				bestDistance = distance
			}
		}
		if best >= 0 {
			usedCredits[best] = true
			pairs = append(pairs, TransferPair{Debit: debit, Credit: credits[best]})
		}
	}
	return pairs
}

// TransferIDs returns the IDs of both sides of every pair.
func TransferIDs(pairs []TransferPair) map[int64]struct{} {
	out := make(map[int64]struct{}, len(pairs)*2)
	for _, pair := range pairs {
		if pair.Debit.ID != 0 {
			out[pair.Debit.ID] = struct{}{}
		}
		if pair.Credit.ID != 0 {
			out[pair.Credit.ID] = struct{}{}
		}
	}
	return out
}

// ExcludeTransfers removes transactions that belong to an internal transfer.
func ExcludeTransfers(transactions []domain.Transaction, pairs []TransferPair) []domain.Transaction {
	ids := TransferIDs(pairs)
	if len(ids) == 0 {
		return transactions
	}
	out := make([]domain.Transaction, 0, len(transactions))
	for _, tx := range transactions {
		if _, ok := ids[tx.ID]; ok {
			continue
		}
		out = append(out, tx)
	}
	return out
}
