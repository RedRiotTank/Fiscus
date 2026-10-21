package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/RedRiotTank/Fiscus/internal/alerts"
	"github.com/RedRiotTank/Fiscus/internal/banking/gocardless"
	"github.com/RedRiotTank/Fiscus/internal/config"
	"github.com/RedRiotTank/Fiscus/internal/ingest"
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

func buildBankClient(cfg *config.Config) (*gocardless.Client, error) {
	if !cfg.BankingConfigured() {
		return nil, nil
	}
	return gocardless.New(gocardless.Config{
		BaseURL:   cfg.GoCardless.BaseURL,
		SecretID:  cfg.GoCardless.SecretID,
		SecretKey: cfg.GoCardless.SecretKey,
		UserAgent: "Fiscus/" + version + " (+https://github.com/RedRiotTank/Fiscus)",
	})
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

func buildEngine(cfg *config.Config, store *storage.Store, logger *slog.Logger) (*alerts.Engine, error) {
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
	notifiers, err := buildNotifiers(cfg)
	if err != nil {
		return nil, err
	}
	dispatcher := alerts.NewDispatcher(notifiers, alerts.DispatchConfig{
		Attempts:  cfg.Alerts.RetryAttempts,
		BaseDelay: cfg.Alerts.RetryBaseDelay.Std(),
		Logger:    logger,
	})
	engine, err := alerts.NewEngine(alerts.EngineConfig{
		Store:        store,
		Dispatcher:   dispatcher,
		Logger:       logger,
		TxRules:      []alerts.TransactionRule{duplicateRule, anomalyRule},
		BalanceRules: []alerts.BalanceRule{liquidityRule},
	})
	if err != nil {
		return nil, err
	}
	if len(notifiers) == 0 {
		logger.Warn("no notification channels configured; alerts will only appear in the dashboard")
	}
	return engine, nil
}

func buildWorker(cfg *config.Config, store *storage.Store, engine *alerts.Engine, logger *slog.Logger) (*ingest.Worker, error) {
	bank, err := buildBankClient(cfg)
	if err != nil {
		return nil, err
	}
	if bank == nil {
		return nil, nil
	}
	worker, err := ingest.New(bank, store, engine, logger, ingest.Config{
		SyncInterval:        cfg.GoCardless.SyncInterval.Std(),
		SyncTimeout:         cfg.GoCardless.SyncTimeout.Std(),
		InitialLookbackDays: cfg.GoCardless.InitialLookbackDays,
		OverlapDays:         cfg.GoCardless.OverlapDays,
		SyncOnStart:         true,
	})
	if err != nil {
		return nil, err
	}
	return worker, nil
}

func newWebServer(cfg *config.Config, store *storage.Store, trigger syncTrigger, logger *slog.Logger) (*web.Server, error) {
	return web.New(web.Config{
		Username:     cfg.Server.Username,
		Password:     cfg.Server.Password,
		PasswordHash: cfg.Server.PasswordHash,
		Version:      version,
		Logger:       logger,
	}, store, trigger)
}

func requireBanking(cfg *config.Config) error {
	if !cfg.BankingConfigured() {
		return fmt.Errorf("GoCardless credentials are not configured (set FISCUS_GOCARDLESS_SECRET_ID and FISCUS_GOCARDLESS_SECRET_KEY)")
	}
	return nil
}
