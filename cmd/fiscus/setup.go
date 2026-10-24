package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/alerts"
	"github.com/RedRiotTank/Fiscus/internal/analysis"
	"github.com/RedRiotTank/Fiscus/internal/attachments"
	"github.com/RedRiotTank/Fiscus/internal/banking/enablebanking"
	"github.com/RedRiotTank/Fiscus/internal/banking/gocardless"
	"github.com/RedRiotTank/Fiscus/internal/budgets"
	"github.com/RedRiotTank/Fiscus/internal/categories"
	"github.com/RedRiotTank/Fiscus/internal/config"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/events"
	"github.com/RedRiotTank/Fiscus/internal/ingest"
	"github.com/RedRiotTank/Fiscus/internal/monitoring"
	"github.com/RedRiotTank/Fiscus/internal/observability"
	"github.com/RedRiotTank/Fiscus/internal/search"
	"github.com/RedRiotTank/Fiscus/internal/storage"
	"github.com/RedRiotTank/Fiscus/internal/tax"
	"github.com/RedRiotTank/Fiscus/internal/telegram"
	"github.com/RedRiotTank/Fiscus/internal/web"
	"github.com/RedRiotTank/Fiscus/internal/webhooks"
)

type syncTrigger = web.SyncTrigger

func openStore(ctx context.Context, cfg *config.Config) (*storage.Store, error) {
	store, err := storage.Open(ctx, cfg.Database.Path)
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	for id, accountType := range cfg.AccountTypes() {
		if err := store.ApplyConfigAccountType(ctx, id, accountType); err != nil && !errors.Is(err, storage.ErrNotFound) {
			_ = store.Close()
			return nil, fmt.Errorf("apply account type for %s: %w", id, err)
		}
	}
	return store, nil
}

func buildGoCardlessClient(cfg *config.Config) (*gocardless.Client, error) {
	if !cfg.GoCardlessConfigured() {
		return nil, nil
	}
	return gocardless.New(gocardless.Config{
		BaseURL:   cfg.GoCardless.BaseURL,
		SecretID:  cfg.GoCardless.SecretID,
		SecretKey: cfg.GoCardless.SecretKey,
		UserAgent: "Fiscus/" + version + " (+https://github.com/RedRiotTank/Fiscus)",
	})
}

func buildEnableBankingClient(cfg *config.Config) (*enablebanking.Client, error) {
	if !cfg.EnableBankingConfigured() {
		return nil, nil
	}
	privateKey := cfg.EnableBanking.PrivateKey
	if path := strings.TrimSpace(cfg.EnableBanking.PrivateKeyPath); path != "" {
		// #nosec G304 -- the private key path is supplied by the operator.
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read enablebanking private key %s: %w", path, err)
		}
		privateKey = string(data)
	}
	return enablebanking.New(enablebanking.Config{
		BaseURL:       cfg.EnableBanking.BaseURL,
		AppID:         cfg.EnableBanking.AppID,
		PrivateKeyPEM: privateKey,
		UserAgent:     "Fiscus/" + version + " (+https://github.com/RedRiotTank/Fiscus)",
	})
}

// buildBankProvider constructs the selected provider and, for Enable Banking,
// the callback used to complete a consent redirect.
func buildBankProvider(cfg *config.Config, store *storage.Store, logger *slog.Logger) (ingest.Bank, web.AuthorizationCallback, error) {
	if !cfg.BankingConfigured() {
		return nil, nil, nil
	}
	switch cfg.ProviderName() {
	case "enablebanking":
		client, err := buildEnableBankingClient(cfg)
		if err != nil {
			return nil, nil, err
		}
		flow, err := enablebanking.NewAuthFlow(client, store, logger)
		if err != nil {
			return nil, nil, err
		}
		return client, flow, nil
	default:
		client, err := buildGoCardlessClient(cfg)
		if err != nil {
			return nil, nil, err
		}
		return client, nil, nil
	}
}

func buildNotifiers(cfg *config.Config) ([]alerts.Notifier, error) {
	var notifiers []alerts.Notifier
	if cfg.TelegramConfigured() {
		telegram, err := alerts.NewTelegram(alerts.TelegramConfig{
			BotToken: cfg.Alerts.Telegram.BotToken,
			ChatID:   cfg.Alerts.Telegram.ChatID,
		})
		if err != nil {
			return nil, err
		}
		notifiers = append(notifiers, telegram)
	}
	if cfg.DiscordConfigured() {
		discord, err := alerts.NewDiscord(alerts.DiscordConfig{
			WebhookURL: cfg.Alerts.Discord.WebhookURL,
		})
		if err != nil {
			return nil, err
		}
		notifiers = append(notifiers, discord)
	}
	return notifiers, nil
}

func buildTelegramBot(cfg *config.Config, store *storage.Store, worker *ingest.Worker, logger *slog.Logger, attachmentService *attachments.Service, searchService *search.Service, budgetManager *budgets.Manager, advisories telegram.AdvisoryProvider) (*telegram.Bot, error) {
	if !cfg.TelegramConfigured() {
		return nil, nil
	}
	var syncer telegram.Syncer
	if worker != nil {
		syncer = worker
	}
	var ingestor telegram.AttachmentIngestor
	if attachmentService != nil {
		ingestor = attachmentService
	}
	var searcher telegram.Searcher
	if searchService != nil {
		searcher = searchService
	}
	return telegram.New(telegram.Config{
		BotToken:      cfg.Alerts.Telegram.BotToken,
		ChatID:        cfg.Alerts.Telegram.ChatID,
		UserID:        cfg.Alerts.Telegram.UserID,
		Logger:        logger,
		Store:         store,
		Syncer:        syncer,
		Attachments:   ingestor,
		Search:        searcher,
		Categories:    configuredCategories(cfg),
		Budgets:       configuredBudgets(cfg),
		BudgetManager: budgetManager,
		Advisories:    advisories,
	})
}

func buildBudgetManager(cfg *config.Config, store *storage.Store) *budgets.Manager {
	return budgets.NewManager(store, configuredBudgets(cfg), configuredBudgetAdvisory(cfg), nil)
}

func buildEngine(cfg *config.Config, store *storage.Store, logger *slog.Logger, metrics *observability.Metrics, sink events.Sink, taxService *tax.Service, budgetManager *budgets.Manager) (*alerts.Engine, error) {
	thresholds, err := cfg.LiquidityThresholdsMinor()
	if err != nil {
		return nil, err
	}
	duplicateRule, err := alerts.NewDuplicateRule(store, cfg.Alerts.DuplicateWindow.Std())
	if err != nil {
		return nil, err
	}
	anomalyRule, err := alerts.NewAnomalyRule(store, alerts.AnomalyConfig{
		Threshold:    cfg.Alerts.AnomalyThreshold,
		LookbackDays: cfg.Alerts.AnomalyLookbackDays,
		MinHistory:   cfg.Alerts.AnomalyMinHistory,
	})
	if err != nil {
		return nil, err
	}
	liquidityRule := alerts.NewLiquidityRule(thresholds)
	consentRule, err := alerts.NewConsentRule(store, nil, consentRenewalURL(cfg))
	if err != nil {
		return nil, err
	}
	analyzer, err := analysis.NewAnalyzer(store, nil)
	if err != nil {
		return nil, err
	}
	subscriptionRule, err := analysis.NewSubscriptionRule(analyzer)
	if err != nil {
		return nil, err
	}
	budgetRule, err := budgets.NewRuleWithProvider(store, budgetManager, nil)
	if err != nil {
		return nil, err
	}
	ghostRule, err := monitoring.NewGhostSubscriptionRule(analyzer, nil)
	if err != nil {
		return nil, err
	}
	inflowRule, err := monitoring.NewInflowRule(store, configuredInflows(cfg), nil)
	if err != nil {
		return nil, err
	}
	notifiers, err := buildNotifiers(cfg)
	if err != nil {
		return nil, err
	}
	var engineObserver alerts.EngineObserver
	var deliveryObserver alerts.DeliveryObserver
	if metrics != nil {
		engineObserver = metrics
		deliveryObserver = metrics.DeliveryObserver()
	}
	dispatcher := alerts.NewDispatcher(notifiers, alerts.DispatchConfig{
		Attempts:  cfg.Alerts.RetryAttempts,
		BaseDelay: cfg.Alerts.RetryBaseDelay.Std(),
		Logger:    logger,
		Observer:  deliveryObserver,
	})
	analysisRules := []alerts.AnalysisRule{subscriptionRule, budgetRule, ghostRule, inflowRule}
	if cfg.Attachments.Enabled && cfg.WorkspaceProfile() != domain.WorkspacePersonal {
		analysisRules = append(analysisRules,
			attachments.NewMissingReceiptRule(store, cfg.Attachments.ReceiptThresholdMinor, cfg.Attachments.GraceDays, nil))
	}
	if taxService != nil && cfg.WorkspaceProfile() != domain.WorkspacePersonal {
		analysisRules = append(analysisRules, tax.NewQuarterlyRule(taxService, nil))
	}
	engine, err := alerts.NewEngine(alerts.EngineConfig{
		Store:               store,
		Dispatcher:          dispatcher,
		Logger:              logger,
		TxRules:             []alerts.TransactionRule{duplicateRule, anomalyRule},
		BalanceRules:        []alerts.BalanceRule{liquidityRule},
		ConsentRules:        []alerts.ConsentRule{consentRule},
		AnalysisRules:       analysisRules,
		ReconciliationRules: []alerts.ReconciliationRule{alerts.NewIntegrityRule()},
		Observer:            engineObserver,
		Events:              sink,
	})
	if err != nil {
		return nil, err
	}
	if len(notifiers) == 0 {
		logger.Warn("no notification channels configured; alerts will only appear in the dashboard")
	}
	return engine, nil
}

func consentRenewalURL(cfg *config.Config) func(domain.Connection) string {
	fallback := "/connections"
	redirect := cfg.GoCardless.RedirectURL
	if cfg.ProviderName() == "enablebanking" {
		redirect = cfg.EnableBanking.RedirectURL
	}
	if parsed, err := url.Parse(strings.TrimSpace(redirect)); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.Path = "/connections"
		parsed.RawQuery = ""
		parsed.Fragment = ""
		fallback = parsed.String()
	}
	return func(connection domain.Connection) string {
		link := strings.TrimSpace(connection.Link)
		if strings.HasPrefix(link, "https://") || strings.HasPrefix(link, "http://") {
			return link
		}
		return fallback
	}
}

func buildCategoryClassifier(cfg *config.Config) (*categories.Classifier, error) {
	rules := make([]categories.Rule, 0, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		rules = append(rules, categories.Rule{Category: rule.Category, Pattern: rule.Pattern})
	}
	return categories.New(rules)
}

func configuredBudgets(cfg *config.Config) []budgets.Budget {
	out := make([]budgets.Budget, 0, len(cfg.Budgets.Items))
	for _, item := range cfg.Budgets.Items {
		out = append(out, budgets.Budget{
			Category:        item.Category,
			LimitMinor:      item.Limit,
			Currency:        item.Currency,
			NotifyAtPercent: item.NotifyAtPercent,
		})
	}
	return out
}

func configuredTaxConfig(cfg *config.Config) tax.Config {
	categories := make([]tax.CategoryConfig, 0, len(cfg.Tax.Categories))
	for _, category := range cfg.Tax.Categories {
		categories = append(categories, tax.CategoryConfig{
			Name:              category.Name,
			VATRate:           category.VATRate,
			DeductiblePercent: category.DeductiblePercent,
		})
	}
	return tax.Config{
		VATRateDefault:     cfg.Tax.VATRateDefault,
		IncomeTaxRetention: cfg.Tax.IncomeTaxRetention,
		QuarterStartMonths: cfg.Tax.QuarterStartMonths,
		Categories:         categories,
	}
}

func configuredBudgetAdvisory(cfg *config.Config) budgets.AdvisoryConfig {
	advisory := cfg.Budgets.Advisory
	return budgets.AdvisoryConfig{
		Enabled:                    advisory.Enabled,
		Model:                      advisory.Model,
		NeedsPercent:               advisory.NormativeSplit.NeedsPercent,
		WantsPercent:               advisory.NormativeSplit.WantsPercent,
		SavingsPercent:             advisory.NormativeSplit.SavingsPercent,
		HeuristicWindowMonths:      advisory.HeuristicWindowMonths,
		OverbudgetWarningThreshold: advisory.OverbudgetWarningThreshold,
		UnderbudgetNoticeThreshold: advisory.UnderbudgetNoticeThreshold,
	}
}

type advisoryProviderFunc func(context.Context) ([]budgets.Advisory, error)

func (f advisoryProviderFunc) Advisories(ctx context.Context) ([]budgets.Advisory, error) {
	return f(ctx)
}

func buildAdvisoryProvider(cfg *config.Config, store *storage.Store, manager *budgets.Manager, taxService *tax.Service) telegram.AdvisoryProvider {
	return advisoryProviderFunc(func(ctx context.Context) ([]budgets.Advisory, error) {
		return computeBudgetAdvisoriesWithModel(ctx, cfg, store, manager, taxService, time.Now().UTC(), "")
	})
}

func computeBudgetAdvisoriesWithModel(ctx context.Context, cfg *config.Config, store *storage.Store, manager *budgets.Manager, taxService *tax.Service, now time.Time, modelOverride string) ([]budgets.Advisory, error) {
	if manager == nil {
		return nil, nil
	}
	advisoryConfig, err := manager.Advisory(ctx)
	if err != nil || !advisoryConfig.Enabled {
		return nil, err
	}
	if strings.TrimSpace(modelOverride) != "" {
		advisoryConfig.Model = strings.ToLower(strings.TrimSpace(modelOverride))
	}
	active, err := manager.Active(ctx)
	if err != nil {
		return nil, err
	}
	accounts, err := store.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	profile := cfg.WorkspaceProfile()
	allowed := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		switch profile {
		case domain.WorkspacePersonal:
			if domain.NormalizeAccountType(account.Type) != domain.AccountTypePersonal {
				continue
			}
		case domain.WorkspaceBusiness:
			if domain.NormalizeAccountType(account.Type) != domain.AccountTypeBusiness {
				continue
			}
		}
		allowed[account.ID] = struct{}{}
	}
	windowStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).
		AddDate(0, -(budgets.NormalizeAdvisoryConfig(advisoryConfig).HeuristicWindowMonths - 1), 0)
	transactions, err := store.ListTransactionsBetween(ctx, windowStart, now.Add(24*time.Hour))
	if err != nil {
		return nil, err
	}
	filtered := make([]domain.Transaction, 0, len(transactions))
	for _, tx := range transactions {
		if _, ok := allowed[tx.AccountID]; ok {
			filtered = append(filtered, tx)
		}
	}
	taxProvision := make(map[string]int64)
	if profile != domain.WorkspacePersonal && taxService != nil {
		shields, err := taxService.Shields(ctx)
		if err != nil {
			return nil, err
		}
		for _, shield := range shields {
			taxProvision[shield.Currency] += shield.TaxReserveMinor / 3
		}
	}
	return budgets.ComputeAdvisory(filtered, active, advisoryConfig, taxProvision, now), nil
}

func configuredInflows(cfg *config.Config) []monitoring.Inflow {
	out := make([]monitoring.Inflow, 0, len(cfg.ExpectedInflows))
	for _, item := range cfg.ExpectedInflows {
		out = append(out, monitoring.Inflow{
			Name:         item.Name,
			AmountMin:    item.AmountMin,
			Currency:     item.Currency,
			ExpectedDays: item.ExpectedDays,
			GraceDays:    item.GraceDays,
		})
	}
	return out
}

func configuredCategories(cfg *config.Config) []string {
	out := make([]string, 0, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		category := strings.TrimSpace(rule.Category)
		if category != "" {
			out = append(out, category)
		}
	}
	return out
}

func buildEventSink(cfg *config.Config, logger *slog.Logger) (*webhooks.Dispatcher, error) {
	if len(cfg.Webhooks) == 0 {
		return nil, nil
	}
	configs := make([]webhooks.Config, 0, len(cfg.Webhooks))
	for _, hook := range cfg.Webhooks {
		configs = append(configs, webhooks.Config{
			URL: hook.URL, Secret: hook.Secret, Events: hook.Events,
		})
	}
	return webhooks.New(configs, logger)
}

func buildWorker(cfg *config.Config, store *storage.Store, engine *alerts.Engine, logger *slog.Logger, metrics *observability.Metrics, bank ingest.Bank, sink events.Sink) (*ingest.Worker, error) {
	if bank == nil {
		return nil, nil
	}
	var syncInterval, syncTimeout = cfg.GoCardless.SyncInterval.Std(), cfg.GoCardless.SyncTimeout.Std()
	lookbackDays, overlapDays := cfg.GoCardless.InitialLookbackDays, cfg.GoCardless.OverlapDays
	if cfg.ProviderName() == "enablebanking" {
		syncInterval = cfg.EnableBanking.SyncInterval.Std()
		syncTimeout = cfg.EnableBanking.SyncTimeout.Std()
		lookbackDays = cfg.EnableBanking.InitialLookbackDays
		overlapDays = cfg.EnableBanking.OverlapDays
	}
	classifier, err := buildCategoryClassifier(cfg)
	if err != nil {
		return nil, err
	}
	worker, err := ingest.New(bank, store, engine, logger, ingest.Config{
		SyncInterval:        syncInterval,
		SyncTimeout:         syncTimeout,
		InitialLookbackDays: lookbackDays,
		OverlapDays:         overlapDays,
		SyncOnStart:         true,
		Categorizer:         classifier,
		AccountTypes:        cfg.AccountTypes(),
	})
	if err != nil {
		return nil, err
	}
	if metrics != nil {
		worker.SetObserver(metrics)
	}
	worker.SetEventSink(sink)
	return worker, nil
}

func newWebServer(cfg *config.Config, store *storage.Store, trigger syncTrigger, logger *slog.Logger, metrics *observability.Metrics, authHandler web.AuthorizationCallback, attachmentService *attachments.Service, taxService *tax.Service, searchService *search.Service, budgetManager *budgets.Manager) (*web.Server, error) {
	classifier, err := buildCategoryClassifier(cfg)
	if err != nil {
		return nil, err
	}
	return web.New(web.Config{
		Username:       cfg.Server.Username,
		Password:       cfg.Server.Password,
		PasswordHash:   cfg.Server.PasswordHash,
		Profile:        cfg.WorkspaceProfile(),
		Version:        version,
		Logger:         logger,
		MetricsHandler: metrics.Handler(),
		AuthHandler:    authHandler,
		Classifier:     classifier,
		Budgets:        configuredBudgets(cfg),
		BudgetManager:  budgetManager,
		BudgetAdvisory: configuredBudgetAdvisory(cfg),
		Attachments:    attachmentService,
		Tax:            taxService,
		Search:         searchService,
	}, store, trigger)
}

func requireBanking(cfg *config.Config) error {
	if cfg.BankingConfigured() {
		return nil
	}
	switch cfg.ProviderName() {
	case "enablebanking":
		return fmt.Errorf("enablebanking credentials are not configured (set FISCUS_ENABLEBANKING_APP_ID and FISCUS_ENABLEBANKING_PRIVATE_KEY_PATH)")
	default:
		return fmt.Errorf("GoCardless credentials are not configured (set FISCUS_GOCARDLESS_SECRET_ID and FISCUS_GOCARDLESS_SECRET_KEY)")
	}
}
