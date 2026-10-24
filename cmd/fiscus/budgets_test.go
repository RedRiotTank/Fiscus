package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/demo"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

func TestBudgetRecommendCLIAndApply(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "demo.db")
	store, err := storage.Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := demo.Seed(ctx, store, filepath.Join(root, "attachments"), time.Now().UTC()); err != nil {
		t.Fatalf("demo.Seed: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}
	configPath := filepath.Join(root, "config.yaml")
	configYAML := fmt.Sprintf(`
database:
  path: %q
profile: personal
budgets:
  items:
    - category: Restaurants
      limit: 45000
      currency: EUR
      notify_at_percent: [80, 100]
    - category: Groceries
      limit: 24000
      currency: EUR
      notify_at_percent: [80, 100]
  advisory:
    enabled: true
    model: hybrid
    normative_split:
      needs_percent: 50
      wants_percent: 30
      savings_percent: 20
    heuristic_window_months: 6
    overbudget_warning_threshold: 0.15
    underbudget_notice_threshold: 0.30
`, databasePath)
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := runBudgetRecommend([]string{"-config", configPath}, &stdout, &stderr); err != nil {
		t.Fatalf("runBudgetRecommend: %v (stderr=%s)", err, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"CURRENT BUDGET", "Restaurants", "Over-allocated", "Groceries", "Balanced"} {
		if !strings.Contains(output, want) {
			t.Errorf("recommend output missing %q: %s", want, output)
		}
	}

	stdout.Reset()
	if err := runBudgetRecommend([]string{"-config", configPath, "--apply"}, &stdout, &stderr); err != nil {
		t.Fatalf("runBudgetRecommend --apply: %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Applied") {
		t.Fatalf("apply output = %s", stdout.String())
	}
	applied, err := storage.Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("open applied store: %v", err)
	}
	defer func() { _ = applied.Close() }()
	overrides, err := applied.ListBudgetOverrides(ctx)
	if err != nil {
		t.Fatalf("ListBudgetOverrides: %v", err)
	}
	if len(overrides) == 0 {
		t.Fatal("expected persisted budget overrides")
	}
	foundRestaurants := false
	for _, override := range overrides {
		if override.Category == "Restaurants" {
			foundRestaurants = true
			if override.LimitMinor >= 45_000 {
				t.Fatalf("restaurants override = %+v", override)
			}
		}
	}
	if !foundRestaurants {
		t.Fatalf("overrides = %+v", overrides)
	}
}
