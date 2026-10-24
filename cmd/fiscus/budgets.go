package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/budgets"
	"github.com/RedRiotTank/Fiscus/internal/money"
	"github.com/RedRiotTank/Fiscus/internal/tax"
)

func runBudgets(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: fiscus budgets recommend [--apply] [--model=normative|heuristic|hybrid]")
		return errors.New("budgets: subcommand is required")
	}
	switch args[0] {
	case "recommend":
		return runBudgetRecommend(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("budgets: unknown subcommand %q", args[0])
	}
}

func runBudgetRecommend(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("budgets recommend", stderr)
	configPath := fs.String("config", defaultConfigPath(), "path to YAML configuration")
	apply := fs.Bool("apply", false, "persist recommended category budgets")
	model := fs.String("model", "", "override advisory model: normative, heuristic, or hybrid")
	if err := fs.Parse(args); err != nil {
		return err
	}
	modelOverride := strings.ToLower(strings.TrimSpace(*model))
	switch modelOverride {
	case "", budgets.AdvisoryModelNormative, budgets.AdvisoryModelHeuristic, budgets.AdvisoryModelHybrid:
	default:
		return errors.New("budgets: --model must be normative, heuristic, or hybrid")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	ctx, cancel := commandContext(2 * time.Minute)
	defer cancel()
	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := store.Close(); err != nil {
			_, _ = fmt.Fprintf(stderr, "fiscus: close database: %v\n", err)
		}
	}()
	manager := buildBudgetManager(cfg, store)
	advisoryConfig, err := manager.Advisory(ctx)
	if err != nil {
		return err
	}
	if !advisoryConfig.Enabled {
		return errors.New("budgets: advisory is disabled")
	}
	var taxService *tax.Service
	if cfg.Tax.Enabled && cfg.WorkspaceProfile() != "personal" {
		taxService, err = tax.New(store, configuredTaxConfig(cfg), nil)
		if err != nil {
			return err
		}
	}
	advisories, err := computeBudgetAdvisoriesWithModel(ctx, cfg, store, manager, taxService, time.Now().UTC(), modelOverride)
	if err != nil {
		return err
	}
	if len(advisories) == 0 {
		_, _ = fmt.Fprintln(stdout, "No confirmed income or category history is available yet.")
		return nil
	}
	writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "CURRENCY\tCATEGORY\tCURRENT BUDGET\tHISTORICAL AVG\tRECOMMENDED\tSTATUS")
	for _, advisory := range advisories {
		for _, category := range advisory.Categories {
			current := "—"
			if category.CurrentBudgetMinor > 0 {
				current = money.FormatGrouped(category.CurrentBudgetMinor, advisory.Currency)
			}
			_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n",
				advisory.Currency,
				category.Category,
				current,
				money.FormatGrouped(category.HistoricalAverageMinor, advisory.Currency),
				money.FormatGrouped(category.RecommendedMinor, advisory.Currency),
				advisoryStatusLabel(category.Status),
			)
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if *apply {
		count, err := manager.Apply(ctx, advisories)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "\nApplied %d recommended budget(s).\n", count)
	}
	return nil
}

func advisoryStatusLabel(status string) string {
	switch status {
	case budgets.StatusOverAllocated:
		return "Over-allocated"
	case budgets.StatusUnderBudget:
		return "Under-budget"
	case budgets.StatusBalanced:
		return "Balanced"
	default:
		return "No budget"
	}
}
