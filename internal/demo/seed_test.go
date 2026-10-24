package demo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/analysis"
	"github.com/RedRiotTank/Fiscus/internal/budgets"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
	"github.com/RedRiotTank/Fiscus/internal/treasury"
	"github.com/RedRiotTank/Fiscus/pkg/icons"
)

func TestSeedPopulatesCoherentDataset(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "demo.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	attachmentsDir := filepath.Join(t.TempDir(), "demo_attachments")
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	dataset, err := Seed(ctx, store, attachmentsDir, now)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if got := len(dataset.Transactions); got < 150 || got > 200 {
		t.Fatalf("transactions = %d, want 150..200", got)
	}
	if len(dataset.Connections) != 4 || len(dataset.Accounts) != 5 {
		t.Fatalf("connections/accounts = %d/%d", len(dataset.Connections), len(dataset.Accounts))
	}
	connectionIDs := make(map[string]int64, len(dataset.Connections))
	for _, connection := range dataset.Connections {
		connectionIDs[connection.RequisitionID] = connection.ID
	}
	savings, err := store.GetAccount(ctx, accountSavings)
	if err != nil {
		t.Fatalf("GetAccount savings: %v", err)
	}
	if savings.ConnectionID != connectionIDs[demoConnectionRevolut] {
		t.Fatalf("savings connection = %d, want Revolut %d", savings.ConnectionID, connectionIDs[demoConnectionRevolut])
	}
	santander, err := store.GetAccount(ctx, accountSantander)
	if err != nil {
		t.Fatalf("GetAccount santander: %v", err)
	}
	if santander.ConnectionID != connectionIDs[demoConnectionSantander] {
		t.Fatalf("santander connection = %d, want Santander %d", santander.ConnectionID, connectionIDs[demoConnectionSantander])
	}
	if len(dataset.Alerts) < 6 || len(dataset.Attachments) != 4 {
		t.Fatalf("alerts/attachments = %d/%d", len(dataset.Alerts), len(dataset.Attachments))
	}
	if dataset.CurrentMonthSpend["Restaurants"] <= 0 || dataset.CurrentMonthSpend["Groceries"] <= 0 {
		t.Fatalf("current month spend = %+v", dataset.CurrentMonthSpend)
	}

	wantBalances := map[string]int64{
		accountSantander: 345_000,
		accountSavings:   1_250_000,
		accountBusiness:  885_000,
		accountWiseUSD:   125_000,
		accountWiseGBP:   80_000,
	}
	for accountID, want := range wantBalances {
		account, err := store.GetAccount(ctx, accountID)
		if err != nil {
			t.Fatalf("GetAccount %s: %v", accountID, err)
		}
		if account.BalanceMinor != want {
			t.Errorf("balance %s = %d, want %d", accountID, account.BalanceMinor, want)
		}
		reconciliation, err := store.GetReconciliation(ctx, accountID)
		if err != nil {
			t.Fatalf("GetReconciliation %s: %v", accountID, err)
		}
		if reconciliation.OpeningBalanceMinor <= 0 {
			t.Errorf("opening balance %s = %d, want positive", accountID, reconciliation.OpeningBalanceMinor)
		}
		if reconciliation.Status != domain.ReconciliationStatusReconciled {
			t.Errorf("reconciliation %s = %q", accountID, reconciliation.Status)
		}
	}

	var transactionCount, ftsCount, attachmentCount, alertCount, reconciliationCount int
	queries := []struct {
		name string
		sql  string
		dest *int
	}{
		{name: "transactions", sql: "SELECT COUNT(*) FROM transactions", dest: &transactionCount},
		{name: "fts", sql: "SELECT COUNT(*) FROM transactions_fts", dest: &ftsCount},
		{name: "attachments", sql: "SELECT COUNT(*) FROM transaction_attachments", dest: &attachmentCount},
		{name: "alerts", sql: "SELECT COUNT(*) FROM alerts", dest: &alertCount},
		{name: "reconciliation", sql: "SELECT COUNT(*) FROM account_reconciliation", dest: &reconciliationCount},
	}
	for _, query := range queries {
		if err := store.DB().QueryRowContext(ctx, query.sql).Scan(query.dest); err != nil {
			t.Fatalf("count %s: %v", query.name, err)
		}
	}
	if transactionCount != len(dataset.Transactions) || ftsCount != transactionCount {
		t.Fatalf("transactions/fts = %d/%d", transactionCount, ftsCount)
	}
	if attachmentCount != 4 || alertCount < 6 || reconciliationCount != 5 {
		t.Fatalf("attachments/alerts/reconciliation = %d/%d/%d", attachmentCount, alertCount, reconciliationCount)
	}
	if got := countUnpaired(dataset.Attachments); got != 1 {
		t.Fatalf("unpaired attachments = %d, want 1", got)
	}
	entries, err := os.ReadDir(attachmentsDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("attachment files = %d, want 4", len(entries))
	}

	rows, err := store.DB().QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		t.Fatal("foreign key violations detected")
	}
	var integrity string
	if err := store.DB().QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatalf("integrity_check: %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("integrity_check = %q", integrity)
	}

	pairs := treasury.PairTransfers(dataset.Transactions, 3)
	if len(pairs) != 6 {
		t.Fatalf("internal transfer pairs = %d, want 6", len(pairs))
	}

	snapshot := analysis.Analyze(dataset.Transactions, now)
	subscriptions := make(map[string]analysis.Subscription, len(snapshot.Subscriptions))
	for _, subscription := range snapshot.Subscriptions {
		subscriptions[subscription.Counterparty] = subscription
		if subscription.Category == "Restaurants" || subscription.Category == "Groceries" ||
			subscription.Category == "Rent" || subscription.Category == "Shopping" {
			t.Errorf("non-subscription category detected: %+v", subscription)
		}
	}
	for _, merchant := range []string{
		"Spotify", "Netflix", "Amazon Web Services", "GitHub Copilot", "Cloudflare",
		"JetBrains", "Vodafone", "Gestoría Fiscal", "Adobe",
	} {
		if _, ok := subscriptions[merchant]; !ok {
			t.Errorf("expected subscription %q, got %+v", merchant, snapshot.Subscriptions)
		}
	}
	if adobe := subscriptions["Adobe"]; adobe.Active {
		t.Errorf("Adobe should be a dormant ghost: %+v", adobe)
	}
	if vodafone := subscriptions["Vodafone"]; vodafone.AmountMinor != 4_990 {
		t.Errorf("Vodafone amount = %d, want 4990", vodafone.AmountMinor)
	}
	if gestoria := subscriptions["Gestoría Fiscal"]; gestoria.AmountMinor != 6_000 {
		t.Errorf("Gestoría amount = %d, want 6000", gestoria.AmountMinor)
	}
	bazar := 0
	spotify := make([]domain.Transaction, 0)
	adobeLast := time.Time{}
	for _, tx := range dataset.Transactions {
		switch tx.Counterparty {
		case "Bazar Central":
			bazar++
			if tx.Category != "Uncategorized" {
				t.Errorf("Bazar category = %q", tx.Category)
			}
		case "Spotify":
			spotify = append(spotify, tx)
		case "Adobe":
			if tx.BookingDate.After(adobeLast) {
				adobeLast = tx.BookingDate
			}
		}
	}
	if bazar != 4 {
		t.Fatalf("Bazar Central transactions = %d, want 4", bazar)
	}
	if len(spotify) != 6 {
		t.Fatalf("Spotify transactions = %d", len(spotify))
	}
	if spotify[len(spotify)-1].AmountMinor != -1_099 || spotify[len(spotify)-2].AmountMinor != -1_099 ||
		spotify[0].AmountMinor != -999 {
		t.Fatalf("Spotify price history = %+v", spotify)
	}
	if !adobeLast.Before(now.AddDate(0, 0, -45)) {
		t.Fatalf("Adobe last charge = %v, want stale", adobeLast)
	}
}

func TestDemoBudgetAdvisorySignals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "demo.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	dataset, err := Seed(ctx, store, filepath.Join(t.TempDir(), "attachments"), now)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	personal := make([]domain.Transaction, 0, len(dataset.Transactions))
	for _, tx := range dataset.Transactions {
		if tx.AccountID == accountSantander || tx.AccountID == accountSavings {
			personal = append(personal, tx)
		}
	}
	advisories := budgets.ComputeAdvisory(personal, []budgets.Budget{
		{Category: "Restaurants", LimitMinor: 45_000, Currency: "EUR", NotifyAtPercent: []int{80, 100}},
		{Category: "Groceries", LimitMinor: 24_000, Currency: "EUR", NotifyAtPercent: []int{80, 100}},
	}, budgets.AdvisoryConfig{
		Enabled: true, Model: budgets.AdvisoryModelHybrid,
		NeedsPercent: 50, WantsPercent: 30, SavingsPercent: 20,
		HeuristicWindowMonths: 6, OverbudgetWarningThreshold: 0.15, UnderbudgetNoticeThreshold: 0.30,
	}, nil, now)
	if len(advisories) != 1 {
		t.Fatalf("advisories = %+v", advisories)
	}
	advisory := advisories[0]
	if advisory.MonthlyNetIncomeMinor < 280_000 || advisory.MonthlyNetIncomeMinor > 281_000 {
		t.Fatalf("demo income = %+v", advisory)
	}
	if advisory.UnallocatedWantsMinor < 17_000 || advisory.UnallocatedWantsMinor > 19_000 {
		t.Fatalf("demo wants surplus = %d", advisory.UnallocatedWantsMinor)
	}
	byCategory := make(map[string]budgets.CategoryAdvisory, len(advisory.Categories))
	for _, category := range advisory.Categories {
		byCategory[category.Category] = category
	}
	restaurants := byCategory["Restaurants"]
	if restaurants.Status != budgets.StatusOverAllocated || restaurants.RecommendedMinor < 27_000 ||
		restaurants.RecommendedMinor > 29_000 || restaurants.DeviationPercent < 55 {
		t.Fatalf("restaurants advisory = %+v", restaurants)
	}
	if groceries := byCategory["Groceries"]; groceries.Status != budgets.StatusBalanced {
		t.Fatalf("groceries advisory = %+v", groceries)
	}
}

func countUnpaired(items []domain.Attachment) int {
	count := 0
	for _, item := range items {
		if !item.Paired() {
			count++
		}
	}
	return count
}

func TestDemoSubscriptionsResolveBrandIcons(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "demo.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	dataset, err := Seed(ctx, store, filepath.Join(t.TempDir(), "attachments"), now)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	snapshot := analysis.Analyze(dataset.Transactions, now)
	if len(snapshot.Subscriptions) == 0 {
		t.Fatal("expected detected demo subscriptions")
	}
	resolver := icons.New(map[string]string{"Gestoría Fiscal": "briefcase"})
	for _, subscription := range snapshot.Subscriptions {
		if _, ok := resolver.Resolve(subscription.Counterparty); !ok {
			t.Errorf("demo subscription %q does not resolve to a brand icon", subscription.Counterparty)
		}
	}
}
