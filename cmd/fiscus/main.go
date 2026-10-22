package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/RedRiotTank/Fiscus/internal/config"
	"github.com/RedRiotTank/Fiscus/internal/observability"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "fiscus:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		args = []string{"serve"}
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:], stdout, stderr)
	case "connect":
		return runConnect(args[1:], stdout, stderr)
	case "institutions":
		return runInstitutions(args[1:], stdout, stderr)
	case "sync":
		return runSync(args[1:], stdout, stderr)
	case "recategorize":
		return runRecategorize(args[1:], stdout, stderr)
	case "export":
		return runExport(args[1:], stdout, stderr)
	case "version", "-version", "--version":
		_, _ = fmt.Fprintf(stdout, "fiscus %s (commit %s, built %s, %s)\n", version, commit, date, runtime.Version())
		return nil
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Fiscus — self-hosted PSD2 treasury monitoring

Usage:
  fiscus serve          Start the dashboard and background sync worker (default)
  fiscus connect        Create a GoCardless bank consent
  fiscus institutions   List available institutions for a country
  fiscus sync           Run one synchronization cycle and exit
  fiscus recategorize   Re-run category rules over historical transactions
  fiscus export         Export transactions as CSV, hledger, or Beancount
  fiscus version        Print build information

Run "fiscus <command> -h" for command flags.

Configuration is read from -config (default config.yaml, or FISCUS_CONFIG)
and then overridden by FISCUS_* environment variables. See config.example.yaml.
`)
}

func defaultConfigPath() string {
	if path := os.Getenv("FISCUS_CONFIG"); path != "" {
		return path
	}
	return "config.yaml"
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func loadConfig(path string) (*config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func newLogger(cfg *config.Config, stderr io.Writer) *slog.Logger {
	var level slog.Level
	switch cfg.Logging.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if cfg.Logging.Format == "json" {
		handler = slog.NewJSONHandler(stderr, opts)
	} else {
		handler = slog.NewTextHandler(stderr, opts)
	}
	return slog.New(handler)
}

func runServe(args []string, _ io.Writer, stderr io.Writer) error {
	fs := newFlagSet("serve", stderr)
	configPath := fs.String("config", defaultConfigPath(), "path to YAML configuration")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	logger := newLogger(cfg, stderr)

	if !cfg.AuthConfigured() {
		logger.Warn("dashboard authentication is disabled; set FISCUS_SERVER_USERNAME and FISCUS_SERVER_PASSWORD")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	appCtx, cancelApp := context.WithCancel(ctx)
	defer cancelApp()

	store, err := openStore(appCtx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := store.Close(); err != nil {
			logger.Error("close database", "error", err)
		}
	}()

	metrics := observability.New(version)

	engine, err := buildEngine(cfg, store, logger, metrics)
	if err != nil {
		return err
	}
	bank, authHandler, err := buildBankProvider(cfg, store, logger)
	if err != nil {
		return err
	}
	worker, err := buildWorker(cfg, store, engine, logger, metrics, bank)
	if err != nil {
		return err
	}
	if worker != nil {
		go worker.Run(appCtx)
	} else {
		logger.Warn("banking credentials are not configured; ingestion is disabled")
	}

	bot, err := buildTelegramBot(cfg, store, worker, logger)
	if err != nil {
		return err
	}
	if worker != nil && bot != nil && cfg.Alerts.Telegram.NotifyUncategorized {
		worker.SetUncategorizedNotifier(bot)
	}
	var botDone chan struct{}
	if bot != nil {
		botDone = make(chan struct{})
		go func() {
			defer close(botDone)
			bot.Run(appCtx)
		}()
	}

	var trigger syncTrigger
	if worker != nil {
		trigger = worker
	}
	server, err := newWebServer(cfg, store, trigger, logger, metrics, authHandler)
	if err != nil {
		return err
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

	errCh := make(chan error, 1)
	go func() {
		if cfg.Server.TLSCertFile != "" {
			logger.Info("dashboard listening", "address", cfg.Server.Address, "scheme", "https", "version", version)
			errCh <- httpServer.ListenAndServeTLS(cfg.Server.TLSCertFile, cfg.Server.TLSKeyFile)
			return
		}
		logger.Info("dashboard listening", "address", cfg.Server.Address, "scheme", "http", "version", version)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown requested")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout.Std())
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	cancelApp()
	if botDone != nil {
		select {
		case <-botDone:
		case <-shutdownCtx.Done():
			logger.Warn("telegram bot did not stop before shutdown timeout")
		}
	}
	return nil
}
