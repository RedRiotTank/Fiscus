package demo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	if dataset.CurrentMonthSpend["Restaurants & Delivery"] <= 0 || dataset.CurrentMonthSpend["Groceries"] <= 0 {
		t.Fatalf("current month spend = %+v", dataset.CurrentMonthSpend)
	}

	wantBalances := map[string]int64{
		accountSantander: 345_000,
		accountSavings:   1_250_000,
		accountBusiness:  3_000_000,
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

func TestDemoBudgetPlans(t *testing.T) {
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

	personalProfile, ok, err := store.GetBudgetProfile(ctx, domain.WorkspacePersonal, "EUR")
	if err != nil || !ok {
		t.Fatalf("personal profile = ok=%v err=%v", ok, err)
	}
	if personalProfile.NeedsBPS != 5000 || personalProfile.WantsBPS != 3000 || personalProfile.SavingsBPS != 2000 {
		t.Fatalf("personal profile = %+v", personalProfile)
	}
	personalAllocations, err := store.ListBudgetAllocations(ctx, domain.WorkspacePersonal, "EUR")
	if err != nil {
		t.Fatalf("personal allocations: %v", err)
	}
	if len(personalAllocations) != 8 {
		t.Fatalf("personal allocations = %+v", personalAllocations)
	}
	personalPlan := budgets.BuildPlan(budgets.Profile{
		Workspace: domain.WorkspacePersonal, Currency: "EUR",
		NeedsBPS: personalProfile.NeedsBPS, WantsBPS: personalProfile.WantsBPS,
		SavingsBPS: personalProfile.SavingsBPS, AdvisoryEnabled: true,
	}, convertAllocations(personalAllocations), demoSpending(dataset.CurrentMonthSpend), 280_000, 0, 280_000, now)
	if personalPlan.AllocatedBPS != 10000 || personalPlan.AllocatedMinor != 280_000 ||
		personalPlan.UnallocatedMinor != 0 || len(personalPlan.Categories) != 8 {
		t.Fatalf("personal plan = %+v", personalPlan)
	}
	byCategory := make(map[string]budgets.CategoryPlan, len(personalPlan.Categories))
	for _, category := range personalPlan.Categories {
		byCategory[category.Category] = category
	}
	if byCategory["Rent"].LimitMinor != 85_000 || byCategory["Groceries"].LimitMinor != 35_000 ||
		byCategory["Restaurants & Delivery"].LimitMinor != 30_000 {
		t.Fatalf("personal category limits = %+v", byCategory)
	}
	if !hasBudgetMessage(personalPlan.Messages, "Dining Out & Delivery") {
		t.Fatalf("expected dining advisory, got %+v", personalPlan.Messages)
	}

	businessProfile, ok, err := store.GetBudgetProfile(ctx, domain.WorkspaceBusiness, "EUR")
	if err != nil || !ok {
		t.Fatalf("business profile = ok=%v err=%v", ok, err)
	}
	if businessProfile.NeedsBPS != 1727 || businessProfile.SavingsBPS != 2100 {
		t.Fatalf("business profile = %+v", businessProfile)
	}
	businessAllocations, err := store.ListBudgetAllocations(ctx, domain.WorkspaceBusiness, "EUR")
	if err != nil {
		t.Fatalf("business allocations: %v", err)
	}
	businessPlan := budgets.BuildPlan(budgets.Profile{
		Workspace: domain.WorkspaceBusiness, Currency: "EUR",
		NeedsBPS: businessProfile.NeedsBPS, WantsBPS: businessProfile.WantsBPS,
		SavingsBPS: businessProfile.SavingsBPS, AdvisoryEnabled: true,
	}, convertAllocations(businessAllocations), demoSpending(dataset.CurrentMonthSpend), 550_000, 115_500, 434_500, now)
	if businessPlan.AllocatedBPS != 3827 || businessPlan.AllocatedMinor != 210_500 ||
		businessPlan.UnallocatedMinor != 339_500 || len(businessPlan.Categories) != 4 {
		t.Fatalf("business plan = %+v", businessPlan)
	}
	for _, category := range businessPlan.Categories {
		switch category.Category {
		case "Groceries", "Restaurants & Delivery", "Leisure & Shopping", "Rent":
			t.Fatalf("personal category leaked into business plan: %+v", category)
		}
	}
}

func convertAllocations(items []domain.BudgetAllocation) []budgets.Allocation {
	out := make([]budgets.Allocation, 0, len(items))
	for _, item := range items {
		out = append(out, budgets.Allocation{
			Category: item.Category, Pillar: budgets.Pillar(item.Pillar),
			PercentBPS: item.PercentBPS, NotifyAtPercent: item.NotifyAtPercent,
			SortOrder: item.SortOrder, Custom: item.Custom,
		})
	}
	return out
}

func demoSpending(spend map[string]int64) []storage.CategorySpending {
	out := make([]storage.CategorySpending, 0, len(spend))
	for category, minor := range spend {
		out = append(out, storage.CategorySpending{Category: category, Currency: "EUR", Minor: minor})
	}
	return out
}

func hasBudgetMessage(messages []budgets.AdvisoryMessage, needle string) bool {
	for _, message := range messages {
		if strings.Contains(message.Title, needle) || strings.Contains(message.Body, needle) {
			return true
		}
	}
	return false
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
