package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/attachments"
	"github.com/RedRiotTank/Fiscus/internal/budgets"
	"github.com/RedRiotTank/Fiscus/internal/config"
	"github.com/RedRiotTank/Fiscus/internal/demo"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/observability"
	"github.com/RedRiotTank/Fiscus/internal/search"
	"github.com/RedRiotTank/Fiscus/internal/storage"
	"github.com/RedRiotTank/Fiscus/internal/tax"
)

type demoOptions struct {
	Address   string
	Directory string
	DryRun    bool
	Stdout    io.Writer
	Stderr    io.Writer
}

func runDemo(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("demo", stderr)
	address := fs.String("address", "127.0.0.1:8080", "demo dashboard listen address")
	dir := fs.String("dir", "", "persistent demo directory (default: temporary directory)")
	dryRun := fs.Bool("dry-run", false, "seed the demo dataset and exit without starting the dashboard")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return runDemoMode(demoOptions{
		Address:   *address,
		Directory: *dir,
		DryRun:    *dryRun,
		Stdout:    stdout,
		Stderr:    stderr,
	})
}

func runDemoMode(opts demoOptions) error {
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	if strings.TrimSpace(opts.Address) == "" {
		opts.Address = "127.0.0.1:8080"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := strings.TrimSpace(opts.Directory)
	cleanup := false
	if root == "" {
		var err error
		root, err = os.MkdirTemp("", "fiscus-demo-*")
		if err != nil {
			return fmt.Errorf("demo: create temporary directory: %w", err)
		}
		cleanup = true
	}
	defer func() {
		if cleanup {
			_ = os.RemoveAll(root)
		}
	}()
	dataDir := filepath.Join(root, "data")
	databasePath := filepath.Join(dataDir, "demo.db")
	attachmentsDir := filepath.Join(dataDir, "demo_attachments")
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("demo: create data directory: %w", err)
	}
	if opts.Directory != "" {
		_ = os.Remove(databasePath)
		_ = os.Remove(databasePath + "-wal")
		_ = os.Remove(databasePath + "-shm")
		if err := os.RemoveAll(attachmentsDir); err != nil {
			return fmt.Errorf("demo: reset attachment directory: %w", err)
		}
	}

	store, err := storage.Open(ctx, databasePath)
	if err != nil {
		return fmt.Errorf("demo: open database: %w", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("demo: migrate database: %w", err)
	}
	now := time.Now().UTC()
	dataset, err := demo.Seed(ctx, store, attachmentsDir, now)
	if err != nil {
		return err
	}
	if opts.DryRun {
		_, _ = fmt.Fprintf(opts.Stdout,
			"Demo dataset ready: %d accounts, %d transactions, %d alerts, %d attachments.\n",
			len(dataset.Accounts), len(dataset.Transactions), len(dataset.Alerts), len(dataset.Attachments))
		if opts.Directory != "" {
			_, _ = fmt.Fprintf(opts.Stdout, "Database: %s\n", databasePath)
		} else {
			_, _ = fmt.Fprintln(opts.Stdout, "Temporary demo database removed on exit.")
		}
		return nil
	}

	cfg := demoConfig(opts.Address, databasePath, attachmentsDir)
	logger := newLogger(cfg, opts.Stderr)
	attachmentService, err := attachments.New(store, attachmentsDir, cfg.Attachments.MaxSizeBytes, nil)
	if err != nil {
		return fmt.Errorf("demo: attachment service: %w", err)
	}
	taxService, err := tax.New(store, configuredTaxConfig(cfg), nil)
	if err != nil {
		return fmt.Errorf("demo: tax service: %w", err)
	}
	metrics := observability.New(version)
	budgetManager := budgets.NewManager(store, configuredBudgets(cfg), configuredBudgetAdvisory(cfg), nil)
	server, err := newWebServer(cfg, store, nil, logger, metrics, nil, attachmentService, taxService, search.New(store), budgetManager)
	if err != nil {
		return fmt.Errorf("demo: web server: %w", err)
	}

	httpServer := &http.Server{
		Addr:              cfg.Server.Address,
		Handler:           server.Handler(),
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout.Std(),
		ReadTimeout:       cfg.Server.ReadTimeout.Std(),
		WriteTimeout:      cfg.Server.WriteTimeout.Std(),
		IdleTimeout:       cfg.Server.IdleTimeout.Std(),
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	url := demoURL(opts.Address)
	_, _ = fmt.Fprintf(opts.Stdout,
		"🚀 Fiscus running in DEMO mode! Explore the dashboard at %s (No real bank connection required).\n",
		url)
	_, _ = fmt.Fprintf(opts.Stdout, "Demo database: %s\nPress Ctrl+C to stop.\n", databasePath)

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		logger.Info("demo shutdown requested")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("demo: http server: %w", err)
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout.Std())
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("demo: http shutdown: %w", err)
	}
	return nil
}

func demoConfig(address, databasePath, attachmentsDir string) *config.Config {
	cfg := config.Default()
	cfg.Profile = domain.WorkspaceHybrid
	cfg.Server.Address = address
	cfg.Server.Username = ""
	cfg.Server.Password = ""
	cfg.Server.PasswordHash = ""
	cfg.Server.TLSCertFile = ""
	cfg.Server.TLSKeyFile = ""
	cfg.Database.Path = databasePath
	cfg.Attachments.Enabled = true
	cfg.Attachments.Directory = attachmentsDir
	cfg.Attachments.MaxSizeBytes = 10 << 20
	cfg.Attachments.ReceiptThresholdMinor = 5_000
	cfg.Attachments.GraceDays = 5
	cfg.Tax.Enabled = true
	cfg.Tax.VATRateDefault = 0.21
	cfg.Tax.IncomeTaxRetention = 0.15
	cfg.Tax.QuarterStartMonths = []int{1, 4, 7, 10}
	cfg.Tax.Categories = []config.TaxCategoryConfig{
		{Name: "Software", VATRate: 0.21, DeductiblePercent: 1},
		{Name: "Travel", VATRate: 0.10, DeductiblePercent: 1},
		{Name: "Meals", VATRate: 0.10, DeductiblePercent: 0.5},
		{Name: "Insurance", VATRate: 0, DeductiblePercent: 1},
	}
	cfg.Budgets.Items = demoBudgets()
	cfg.Subscriptions.IconOverrides = map[string]string{"Gestoría Fiscal": "briefcase"}
	cfg.Budgets.Advisory = config.BudgetAdvisoryConfig{
		Enabled:                    true,
		Model:                      "hybrid",
		NormativeSplit:             config.NormativeSplit{NeedsPercent: 50, WantsPercent: 30, SavingsPercent: 20},
		HeuristicWindowMonths:      6,
		OverbudgetWarningThreshold: 0.15,
		UnderbudgetNoticeThreshold: 0.30,
	}
	cfg.Rules = demoRules()
	cfg.Backups.Enabled = false
	cfg.Backups.Telegram = false
	cfg.Webhooks = nil
	cfg.Alerts.Telegram = config.TelegramConfig{}
	cfg.Alerts.Discord = config.DiscordConfig{}
	return cfg
}

func demoBudgets() []config.BudgetConfig {
	return []config.BudgetConfig{
		{Category: "Restaurants", Limit: 45_000, Currency: "EUR", NotifyAtPercent: []int{80, 100}},
		{Category: "Groceries", Limit: 24_000, Currency: "EUR", NotifyAtPercent: []int{80, 100}},
	}
}

func demoRules() []config.CategoryRule {
	return []config.CategoryRule{
		{Category: "Groceries", Pattern: "(?i)mercadona|carrefour|lidl"},
		{Category: "Utilities", Pattern: "(?i)iberdrola|endesa|vodafone"},
		{Category: "Restaurants", Pattern: "(?i)taberna|sushi|café central|pizzería"},
		{Category: "Subscriptions", Pattern: "(?i)netflix|spotify|adobe|figma|github|namecheap"},
		{Category: "Software", Pattern: "(?i)aws|stripe|cloudflare|apple|jetbrains"},
		{Category: "Payroll", Pattern: "(?i)payroll|nómina|acme studio"},
	}
}

func demoURL(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "http://localhost:8080"
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "127.0.0.1" || host == "::1" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}
