package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/budgets"
	"github.com/RedRiotTank/Fiscus/internal/demo"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

func TestRunDemoDryRunSeedsDatabase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := runDemo([]string{"-dry-run", "-dir", dir}, &stdout, &stderr); err != nil {
		t.Fatalf("runDemo: %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Demo dataset ready") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	store, err := storage.Open(context.Background(), filepath.Join(dir, "data", "demo.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	var transactions int
	if err := store.DB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM transactions").Scan(&transactions); err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	if transactions < 150 || transactions > 200 {
		t.Fatalf("transactions = %d, want 150..200", transactions)
	}
	var attachments int
	if err := store.DB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM transaction_attachments").Scan(&attachments); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if attachments != 4 {
		t.Fatalf("attachments = %d, want 4", attachments)
	}
}

func TestDemoBudgetsTargetPercentages(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	dataset := demo.Dataset{CurrentMonthSpend: map[string]int64{
		"Restaurants": 8_500,
		"Groceries":   6_000,
	}}
	configs := demoBudgets(dataset, now)
	if len(configs) != 2 {
		t.Fatalf("budgets = %+v", configs)
	}
	configured := make([]budgets.Budget, 0, len(configs))
	for _, item := range configs {
		configured = append(configured, budgets.Budget{
			Category: item.Category, LimitMinor: item.Limit, Currency: item.Currency,
			NotifyAtPercent: item.NotifyAtPercent,
		})
	}
	statuses := budgets.Compute([]storage.CategorySpending{
		{Category: "Restaurants", Currency: "EUR", Minor: 8_500},
		{Category: "Groceries", Currency: "EUR", Minor: 6_000},
	}, configured, now)
	if len(statuses) != 2 {
		t.Fatalf("statuses = %+v", statuses)
	}
	byCategory := make(map[string]budgets.Status, len(statuses))
	for _, status := range statuses {
		byCategory[status.Category] = status
	}
	if byCategory["Restaurants"].Percent != 85 || byCategory["Groceries"].Percent != 60 {
		t.Fatalf("budget percentages = %+v", byCategory)
	}
}
