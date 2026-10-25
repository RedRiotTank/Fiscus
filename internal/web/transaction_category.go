package web

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/categories"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
	"github.com/RedRiotTank/Fiscus/pkg/subscriptions"
)

var bizumPattern = regexp.MustCompile(`(?i)\bbizum\b`)

// TransactionCategoryView supplies the inline category editor.
type TransactionCategoryView struct {
	Transaction domain.Transaction
	Categories  []string
}

func categoryEditor(tx TransactionView, categories []string) TransactionCategoryView {
	transaction := tx.Transaction
	if strings.TrimSpace(transaction.Category) == "" {
		transaction.Category = "Uncategorized"
	}
	seen := make(map[string]struct{}, len(categories)+1)
	for _, category := range categories {
		category = strings.TrimSpace(category)
		if category != "" {
			seen[category] = struct{}{}
		}
	}
	seen[transaction.Category] = struct{}{}
	options := make([]string, 0, len(seen))
	for category := range seen {
		options = append(options, category)
	}
	sort.Strings(options)
	return TransactionCategoryView{Transaction: transaction, Categories: options}
}

func isBizum(counterparty, description string) bool {
	return bizumPattern.MatchString(counterparty + " " + description)
}

func (s *Server) categoryOptions(extras ...string) []string {
	seen := map[string]struct{}{categories.DefaultCategory: {}}
	if s.classifier != nil {
		for _, category := range s.classifier.Categories() {
			if category = strings.TrimSpace(category); category != "" {
				seen[category] = struct{}{}
			}
		}
	}
	for _, category := range extras {
		if category = strings.TrimSpace(category); category != "" {
			seen[category] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for category := range seen {
		out = append(out, category)
	}
	sort.Strings(out)
	return out
}

func (s *Server) handleTransactionCategory(w http.ResponseWriter, r *http.Request) {
	if !validSyncRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	category := strings.TrimSpace(r.FormValue("category"))
	if len([]rune(category)) > 120 {
		http.Error(w, "Category is too long", http.StatusBadRequest)
		return
	}
	if category == "" {
		category = categories.DefaultCategory
	}
	tx, err := s.store.GetTransaction(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.Error("load transaction for category update", "transaction_id", id, "error", err)
		http.Error(w, "Could not load transaction", http.StatusInternalServerError)
		return
	}
	if err := s.store.UpdateTransactionCategory(r.Context(), id, category); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.Error("update transaction category", "transaction_id", id, "error", err)
		http.Error(w, "Could not update category", http.StatusInternalServerError)
		return
	}
	if !strings.EqualFold(category, categories.DefaultCategory) {
		s.learnCategoryRule(r.Context(), tx, category)
	}
	tx.Category = category
	view := categoryEditor(TransactionView{Transaction: tx}, s.categoryOptions(category))
	s.renderPartialData(w, "transaction_category", view)
}

func learnedPattern(merchant string) string {
	var b strings.Builder
	b.WriteString(`(?i)`)
	for _, r := range merchant {
		switch r {
		case 'a', 'á', 'à', 'ä', 'â', 'ã':
			b.WriteString("[aáàäâã]")
		case 'e', 'é', 'è', 'ë', 'ê':
			b.WriteString("[eéèëê]")
		case 'i', 'í', 'ì', 'ï', 'î':
			b.WriteString("[iíìïî]")
		case 'o', 'ó', 'ò', 'ö', 'ô', 'õ':
			b.WriteString("[oóòöôõ]")
		case 'u', 'ú', 'ù', 'ü', 'û':
			b.WriteString("[uúùüû]")
		case 'n', 'ñ':
			b.WriteString("[nñ]")
		case 'c', 'ç':
			b.WriteString("[cç]")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	return b.String()
}

func (s *Server) learnCategoryRule(ctx context.Context, tx domain.Transaction, category string) {
	merchant := subscriptions.CleanMerchant(tx.Counterparty)
	if merchant == "" {
		merchant = subscriptions.CleanMerchant(tx.Description)
	}
	if len([]rune(merchant)) < 3 {
		return
	}
	pattern := learnedPattern(merchant)
	if _, err := s.store.UpsertLearnedRule(ctx, pattern, category, "inline"); err != nil {
		s.logger.Error("persist learned category rule", "pattern", pattern, "error", err)
		return
	}
	if reloader, ok := s.classifier.(interface{ Reload(context.Context) error }); ok {
		if err := reloader.Reload(ctx); err != nil {
			s.logger.Error("reload learned category rules", "error", err)
			return
		}
	}
	if s.classifier != nil {
		if _, err := s.store.Recategorize(ctx, categories.OnlyUncategorized(s.classifier.Classify), true); err != nil {
			s.logger.Error("apply learned category rule", "pattern", pattern, "error", err)
		}
	}
}
