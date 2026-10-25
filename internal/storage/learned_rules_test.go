package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLearnedRulesPersistAndUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "rules.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	rule, err := store.UpsertLearnedRule(ctx, `(?i)Amazon Prime`, "Subscriptions", "inline")
	if err != nil {
		t.Fatalf("UpsertLearnedRule: %v", err)
	}
	if rule.ID == 0 || rule.Category != "Subscriptions" {
		t.Fatalf("rule = %+v", rule)
	}
	if _, err := store.UpsertLearnedRule(ctx, `(?i)Amazon`, "Leisure & Shopping", "inline"); err != nil {
		t.Fatalf("Upsert broad: %v", err)
	}
	rules, err := store.ListLearnedRules(ctx)
	if err != nil {
		t.Fatalf("ListLearnedRules: %v", err)
	}
	if len(rules) != 2 || rules[0].Pattern != `(?i)Amazon Prime` {
		t.Fatalf("rules = %+v", rules)
	}
	updated, err := store.UpsertLearnedRule(ctx, `(?i)Amazon Prime`, "Leisure & Shopping", "inline")
	if err != nil {
		t.Fatalf("update learned rule: %v", err)
	}
	if updated.Category != "Leisure & Shopping" {
		t.Fatalf("updated = %+v", updated)
	}
	if _, err := store.UpsertLearnedRule(ctx, "", "Groceries", ""); err == nil {
		t.Fatal("expected empty pattern error")
	}
}
