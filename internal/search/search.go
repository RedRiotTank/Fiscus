// Package search parses human-friendly transaction queries and executes them
// against the SQLite FTS5 index.
package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

// Store is the query surface required by the search service.
type Store interface {
	SearchTransactions(ctx context.Context, search storage.TransactionSearch, limit int) ([]domain.Transaction, error)
}

// Service executes parsed transaction searches.
type Service struct {
	store Store
}

// New constructs a search service.
func New(store Store) *Service {
	return &Service{store: store}
}

// Search parses raw and returns up to limit matching transactions.
func (s *Service) Search(ctx context.Context, raw string, limit int) ([]domain.Transaction, error) {
	query, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	return s.store.SearchTransactions(ctx, query, limit)
}

// Parse converts free text, amount filters, category filters, status, receipt
// presence, and workspace type into a storage query.
func Parse(raw string) (storage.TransactionSearch, error) {
	var (
		out    storage.TransactionSearch
		tokens []string
	)
	for _, field := range strings.Fields(raw) {
		key, value, structured := strings.Cut(field, ":")
		if !structured {
			tokens = append(tokens, field)
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "amount":
			op, amount, err := parseAmountFilter(value)
			if err != nil {
				return storage.TransactionSearch{}, err
			}
			out.AmountOp, out.AmountMinor = op, amount
		case "category":
			if value == "" {
				return storage.TransactionSearch{}, errors.New("search: category filter is empty")
			}
			out.Category = value
		case "status":
			value = strings.ToLower(value)
			if value != domain.TransactionStatusBooked && value != domain.TransactionStatusPending {
				return storage.TransactionSearch{}, fmt.Errorf("search: invalid status %q", value)
			}
			out.Status = value
		case "type":
			value = strings.ToLower(value)
			if value != domain.AccountTypePersonal && value != domain.AccountTypeBusiness {
				return storage.TransactionSearch{}, fmt.Errorf("search: invalid type %q", value)
			}
			out.AccountType = value
		case "has":
			value = strings.ToLower(value)
			switch value {
			case "receipt", "recibo":
				yes := true
				out.HasReceipt = &yes
			case "no-receipt", "no_recibo", "sin-recibo":
				no := false
				out.HasReceipt = &no
			default:
				return storage.TransactionSearch{}, fmt.Errorf("search: invalid has filter %q", value)
			}
		default:
			tokens = append(tokens, field)
		}
	}
	out.Match = ftsMatch(tokens)
	return out, nil
}

func parseAmountFilter(value string) (string, int64, error) {
	op := "="
	for _, candidate := range []string{">=", "<=", ">", "<", "="} {
		if strings.HasPrefix(value, candidate) {
			op = candidate
			value = strings.TrimSpace(strings.TrimPrefix(value, candidate))
			break
		}
	}
	if value == "" {
		return "", 0, errors.New("search: amount filter is empty")
	}
	if strings.Contains(value, ",") && !strings.Contains(value, ".") {
		value = strings.ReplaceAll(value, ",", ".")
	}
	minor, err := money.Parse(value, "EUR")
	if err != nil || minor < 0 {
		return "", 0, fmt.Errorf("search: invalid amount %q", value)
	}
	return op, minor, nil
}

func ftsMatch(tokens []string) string {
	terms := make([]string, 0, len(tokens))
	for _, token := range tokens {
		var cleaned strings.Builder
		for _, r := range token {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
				cleaned.WriteRune(r)
			}
		}
		if cleaned.Len() == 0 {
			continue
		}
		terms = append(terms, `"`+cleaned.String()+`"*`)
	}
	return strings.Join(terms, " AND ")
}
