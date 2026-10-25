package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/categories"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

func TestInlineCategoryLearningPropagates(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	ctx := context.Background()
	classifier, err := categories.NewDynamicClassifier(ctx, f.store, nil, categories.DefaultRules())
	if err != nil {
		t.Fatalf("NewDynamicClassifier: %v", err)
	}
	f.server.classifier = classifier

	now := f.server.now().UTC()
	merchants := []string{"Amazon Prime*nk9ae7fn4", "Amazon Prime*nj7t039f4"}
	ids := make([]int64, 0, len(merchants))
	for index, merchant := range merchants {
		tx := domain.Transaction{
			AccountID: "acc-1", ExternalID: "learn-" + strconv.Itoa(index),
			AmountMinor: -499, Currency: "EUR", BookingDate: now.AddDate(0, 0, -30*index),
			Counterparty: merchant, Description: "Amazon Prime subscription",
			Category: "Uncategorized", Status: domain.TransactionStatusBooked,
		}
		if _, _, err := f.store.UpsertTransaction(ctx, &tx); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
		ids = append(ids, tx.ID)
	}
	if got := classifier.Classify(domain.Transaction{Counterparty: "Amazon Prime*newtoken", Description: "Amazon Prime"}); got != "Subscriptions" {
		t.Fatalf("default classification = %q", got)
	}

	form := url.Values{"category": {"Leisure & Shopping"}}
	req := httptest.NewRequest(http.MethodPatch, "/api/transactions/"+strconv.FormatInt(ids[0], 10)+"/category", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(syncRequestHeader, "1")
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("category PATCH = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Leisure &amp; Shopping") {
		t.Fatalf("response does not contain selected category: %s", rec.Body.String())
	}

	for _, id := range ids {
		tx, err := f.store.GetTransaction(ctx, id)
		if err != nil {
			t.Fatalf("GetTransaction %d: %v", id, err)
		}
		if tx.Category != "Leisure & Shopping" {
			t.Errorf("transaction %d category = %q", id, tx.Category)
		}
	}
	rules, err := f.store.ListLearnedRules(ctx)
	if err != nil {
		t.Fatalf("ListLearnedRules: %v", err)
	}
	if len(rules) != 1 || rules[0].Category != "Leisure & Shopping" || rules[0].Pattern == "" {
		t.Fatalf("learned rules = %+v", rules)
	}
	if got := classifier.Classify(domain.Transaction{Counterparty: "Amazon Prime*newtoken", Description: "Amazon Prime"}); got != "Leisure & Shopping" {
		t.Fatalf("future classification = %q", got)
	}
	results, err := f.store.SearchTransactionsPage(ctx, storage.TransactionSearch{
		Match: "Leisure", Category: "Leisure & Shopping",
	}, 10, 0)
	if err != nil {
		t.Fatalf("FTS search: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("FTS results = %+v", results)
	}
}

func TestInlineCategorySingleTransactionModeDoesNotLearn(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	ctx := context.Background()
	now := f.server.now().UTC()
	tx := domain.Transaction{
		AccountID: "acc-1", ExternalID: "variable-utility",
		AmountMinor: -1515, Currency: "EUR", BookingDate: now,
		Counterparty: "Bizum Jose", Description: "Luz octubre | To Jose",
		Category: "Transfers / P2P", Status: domain.TransactionStatusBooked,
	}
	if _, _, err := f.store.UpsertTransaction(ctx, &tx); err != nil {
		t.Fatalf("UpsertTransaction: %v", err)
	}
	form := url.Values{
		"category": {"Utilities & Telecom"},
		"remember": {"0"},
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/transactions/"+strconv.FormatInt(tx.ID, 10)+"/category", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(syncRequestHeader, "1")
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("category PATCH = %d %s", rec.Code, rec.Body.String())
	}
	loaded, err := f.store.GetTransaction(ctx, tx.ID)
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if loaded.Category != "Utilities & Telecom" {
		t.Fatalf("category = %q", loaded.Category)
	}
	rules, err := f.store.ListLearnedRules(ctx)
	if err != nil {
		t.Fatalf("ListLearnedRules: %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("single-transaction edit created learned rules: %+v", rules)
	}
}

func TestTransactionsPageRendersBizumBadge(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	now := f.server.now().UTC()
	tx := domain.Transaction{
		AccountID: "acc-1", ExternalID: "bizum-1", AmountMinor: -2500, Currency: "EUR",
		BookingDate: now.Add(-time.Hour), Counterparty: "Bizum Juan Pérez",
		Description: "Bizum enviado", Category: "Transfers / Bizum",
		Status: domain.TransactionStatusBooked,
	}
	if _, _, err := f.store.UpsertTransaction(context.Background(), &tx); err != nil {
		t.Fatalf("UpsertTransaction: %v", err)
	}
	page := f.request(t, http.MethodGet, "/transactions", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("transactions status = %d", page.Code)
	}
	body := page.Body.String()
	for _, want := range []string{"badge bizum", "Transfers / Bizum", "category-select", "Remember for all transfers to this person"} {
		if !strings.Contains(body, want) {
			t.Errorf("transaction page missing %q", want)
		}
	}
}
