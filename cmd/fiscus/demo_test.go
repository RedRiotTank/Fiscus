package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

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

func TestDemoBudgets(t *testing.T) {
	t.Parallel()
	configs := demoBudgets()
	if len(configs) != 2 {
		t.Fatalf("budgets = %+v", configs)
	}
	byCategory := make(map[string]int64, len(configs))
	for _, item := range configs {
		byCategory[item.Category] = item.Limit
	}
	if byCategory["Restaurants"] != 45_000 || byCategory["Groceries"] != 24_000 {
		t.Fatalf("demo budgets = %+v", byCategory)
	}
}
