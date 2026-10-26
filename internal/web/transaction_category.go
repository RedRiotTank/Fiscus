package web

import (
	"context"
	"errors"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/categories"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
	"github.com/RedRiotTank/Fiscus/pkg/subscriptions"
	"github.com/RedRiotTank/Fiscus/pkg/transfers"
)

var bizumPattern = regexp.MustCompile(`(?i)\bbizum\b`)

// TransactionCategoryView supplies the inline category editor.
type TransactionCategoryView struct {
	Transaction          domain.Transaction
	Categories           []string
	Suggestion           string
	SuggestionConfidence int
	QuickActions         []QuickCategoryAction
	PassThroughLabel     string
	PassThroughTooltip   string
}

// QuickCategoryAction is one single-transaction category shortcut.
type QuickCategoryAction struct {
	Label    string
	Category string
	Class    string
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
	return TransactionCategoryView{
		Transaction:        transaction,
		Categories:         options,
		PassThroughLabel:   tx.PassThroughLabel,
		PassThroughTooltip: tx.PassThroughTooltip,
	}
}

// categoryEditor adds quick single-transaction chips for P2P and
// uncategorized rows, plus a Bayesian confirmation chip when available.
func (s *Server) categoryEditor(tx TransactionView, options []string) TransactionCategoryView {
	view := categoryEditor(tx, options)
	category := strings.TrimSpace(view.Transaction.Category)
	if !strings.EqualFold(category, categories.DefaultCategory) &&
		!isP2PCategoryName(category) {
		return view
	}
	view.QuickActions = []QuickCategoryAction{
		{Label: "🍕 Food / Delivery", Category: "Restaurants & Delivery", Class: "quick-food"},
		{Label: "🛒 Groceries", Category: "Groceries", Class: "quick-groceries"},
		{Label: "🍻 Leisure / Drinks", Category: "Leisure & Shopping", Class: "quick-leisure"},
	}
	suggester, ok := s.classifier.(interface {
		SuggestCategory(domain.Transaction) (string, float64)
	})
	if !ok {
		return view
	}
	suggestion, confidence := suggester.SuggestCategory(view.Transaction)
	suggestion = strings.TrimSpace(suggestion)
	if suggestion == "" || strings.EqualFold(suggestion, category) || confidence < 0.5 {
		return view
	}
	view.Suggestion = suggestion
	view.SuggestionConfidence = int(math.Round(confidence * 100))
	return view
}

func isBizum(counterparty, description string) bool {
	return bizumPattern.MatchString(counterparty + " " + description)
}

func (s *Server) categoryOptions(extras ...string) []string {
	seen := map[string]struct{}{
		categories.DefaultCategory:    {},
		transfers.CategoryPassThrough: {},
	}
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
	remember := !strings.EqualFold(strings.TrimSpace(r.FormValue("remember")), "0")
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
	tx.Category = category
	if updated, loadErr := s.store.GetTransaction(r.Context(), id); loadErr == nil {
		tx = updated
	} else {
		s.logger.Error("reload transaction after category update", "transaction_id", id, "error", loadErr)
	}
	learnable := remember && !strings.EqualFold(category, categories.DefaultCategory) &&
		!transfers.IsPassThroughCategory(category)
	if learnable {
		if learner, ok := s.classifier.(interface {
			Learn(domain.Transaction, string)
		}); ok {
			learner.Learn(tx, category)
		}
		s.learnCategoryRule(r.Context(), tx, category)
	}
	views := []TransactionView{{Transaction: tx}}
	s.enrichPassThroughViews(r.Context(), views)
	view := s.categoryEditor(views[0], s.categoryOptions(category))
	s.renderPartialData(w, "transaction_category", view)
}

func learnedPattern(merchant string) string {
	return categories.LearnedPattern(merchant)
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
