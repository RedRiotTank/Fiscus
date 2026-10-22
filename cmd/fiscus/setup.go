package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/alerts"
	"github.com/RedRiotTank/Fiscus/internal/analysis"
	"github.com/RedRiotTank/Fiscus/internal/banking/enablebanking"
	"github.com/RedRiotTank/Fiscus/internal/banking/gocardless"
	"github.com/RedRiotTank/Fiscus/internal/categories"
	"github.com/RedRiotTank/Fiscus/internal/config"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/ingest"
	"github.com/RedRiotTank/Fiscus/internal/observability"
	"github.com/RedRiotTank/Fiscus/internal/storage"
	"github.com/RedRiotTank/Fiscus/internal/web"
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

func buildEngine(cfg *config.Config, store *storage.Store, logger *slog.Logger, metrics *observability.Metrics) (*alerts.Engine, error) {
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
	engine, err := alerts.NewEngine(alerts.EngineConfig{
		Store:         store,
		Dispatcher:    dispatcher,
		Logger:        logger,
		TxRules:       []alerts.TransactionRule{duplicateRule, anomalyRule},
		BalanceRules:  []alerts.BalanceRule{liquidityRule},
		ConsentRules:  []alerts.ConsentRule{consentRule},
		AnalysisRules: []alerts.AnalysisRule{subscriptionRule},
		Observer:      engineObserver,
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

func buildWorker(cfg *config.Config, store *storage.Store, engine *alerts.Engine, logger *slog.Logger, metrics *observability.Metrics, bank ingest.Bank) (*ingest.Worker, error) {
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
	})
	if err != nil {
		return nil, err
	}
	if metrics != nil {
		worker.SetObserver(metrics)
	}
	return worker, nil
}

func newWebServer(cfg *config.Config, store *storage.Store, trigger syncTrigger, logger *slog.Logger, metrics *observability.Metrics, authHandler web.AuthorizationCallback) (*web.Server, error) {
	return web.New(web.Config{
		Username:       cfg.Server.Username,
		Password:       cfg.Server.Password,
		PasswordHash:   cfg.Server.PasswordHash,
		Version:        version,
		Logger:         logger,
		MetricsHandler: metrics.Handler(),
		AuthHandler:    authHandler,
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
