package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func applyEnv(cfg *Config) error {
	var errs []error

	stringEnv("FISCUS_PROVIDER", &cfg.Provider)
	stringEnv("FISCUS_SERVER_ADDRESS", &cfg.Server.Address)
	stringEnv("FISCUS_SERVER_USERNAME", &cfg.Server.Username)
	stringEnv("FISCUS_SERVER_PASSWORD", &cfg.Server.Password)
	stringEnv("FISCUS_SERVER_PASSWORD_HASH", &cfg.Server.PasswordHash)
	durationEnv("FISCUS_SERVER_READ_HEADER_TIMEOUT", &cfg.Server.ReadHeaderTimeout, &errs)
	durationEnv("FISCUS_SERVER_READ_TIMEOUT", &cfg.Server.ReadTimeout, &errs)
	durationEnv("FISCUS_SERVER_WRITE_TIMEOUT", &cfg.Server.WriteTimeout, &errs)
	durationEnv("FISCUS_SERVER_IDLE_TIMEOUT", &cfg.Server.IdleTimeout, &errs)
	durationEnv("FISCUS_SERVER_SHUTDOWN_TIMEOUT", &cfg.Server.ShutdownTimeout, &errs)

	stringEnv("FISCUS_DATABASE_PATH", &cfg.Database.Path)

	stringEnv("FISCUS_GOCARDLESS_BASE_URL", &cfg.GoCardless.BaseURL)
	stringEnv("FISCUS_GOCARDLESS_SECRET_ID", &cfg.GoCardless.SecretID)
	stringEnv("FISCUS_GOCARDLESS_SECRET_KEY", &cfg.GoCardless.SecretKey)
	stringEnv("FISCUS_GOCARDLESS_REDIRECT_URL", &cfg.GoCardless.RedirectURL)
	stringEnv("FISCUS_GOCARDLESS_COUNTRY", &cfg.GoCardless.Country)
	stringEnv("FISCUS_GOCARDLESS_INSTITUTION_ID", &cfg.GoCardless.InstitutionID)
	stringEnv("FISCUS_GOCARDLESS_INSTITUTION_NAME", &cfg.GoCardless.InstitutionName)
	durationEnv("FISCUS_GOCARDLESS_SYNC_INTERVAL", &cfg.GoCardless.SyncInterval, &errs)
	durationEnv("FISCUS_GOCARDLESS_SYNC_TIMEOUT", &cfg.GoCardless.SyncTimeout, &errs)
	intEnv("FISCUS_GOCARDLESS_INITIAL_LOOKBACK_DAYS", &cfg.GoCardless.InitialLookbackDays, &errs)
	intEnv("FISCUS_GOCARDLESS_OVERLAP_DAYS", &cfg.GoCardless.OverlapDays, &errs)

	stringEnv("FISCUS_ENABLEBANKING_BASE_URL", &cfg.EnableBanking.BaseURL)
	stringEnv("FISCUS_ENABLEBANKING_APP_ID", &cfg.EnableBanking.AppID)
	stringEnv("FISCUS_ENABLEBANKING_PRIVATE_KEY_PATH", &cfg.EnableBanking.PrivateKeyPath)
	stringEnv("FISCUS_ENABLEBANKING_PRIVATE_KEY", &cfg.EnableBanking.PrivateKey)
	stringEnv("FISCUS_ENABLEBANKING_REDIRECT_URL", &cfg.EnableBanking.RedirectURL)
	stringEnv("FISCUS_ENABLEBANKING_COUNTRY", &cfg.EnableBanking.Country)
	stringEnv("FISCUS_ENABLEBANKING_INSTITUTION_NAME", &cfg.EnableBanking.InstitutionName)
	stringEnv("FISCUS_ENABLEBANKING_PSU_TYPE", &cfg.EnableBanking.PSUType)
	intEnv("FISCUS_ENABLEBANKING_CONSENT_VALIDITY_DAYS", &cfg.EnableBanking.ConsentValidityDays, &errs)
	durationEnv("FISCUS_ENABLEBANKING_SYNC_INTERVAL", &cfg.EnableBanking.SyncInterval, &errs)
	durationEnv("FISCUS_ENABLEBANKING_SYNC_TIMEOUT", &cfg.EnableBanking.SyncTimeout, &errs)
	intEnv("FISCUS_ENABLEBANKING_INITIAL_LOOKBACK_DAYS", &cfg.EnableBanking.InitialLookbackDays, &errs)
	intEnv("FISCUS_ENABLEBANKING_OVERLAP_DAYS", &cfg.EnableBanking.OverlapDays, &errs)

	durationEnv("FISCUS_ALERTS_DUPLICATE_WINDOW", &cfg.Alerts.DuplicateWindow, &errs)
	floatEnv("FISCUS_ALERTS_ANOMALY_THRESHOLD", &cfg.Alerts.AnomalyThreshold, &errs)
	intEnv("FISCUS_ALERTS_ANOMALY_LOOKBACK_DAYS", &cfg.Alerts.AnomalyLookbackDays, &errs)
	intEnv("FISCUS_ALERTS_ANOMALY_MIN_HISTORY", &cfg.Alerts.AnomalyMinHistory, &errs)
	intEnv("FISCUS_ALERTS_RETRY_ATTEMPTS", &cfg.Alerts.RetryAttempts, &errs)
	durationEnv("FISCUS_ALERTS_RETRY_BASE_DELAY", &cfg.Alerts.RetryBaseDelay, &errs)
	if raw, ok := os.LookupEnv("FISCUS_LIQUIDITY_THRESHOLDS"); ok {
		thresholds, err := parseThresholdsEnv(raw)
		if err != nil {
			errs = append(errs, err)
		} else {
			cfg.Alerts.LiquidityThresholds = thresholds
		}
	}
	stringEnv("FISCUS_TELEGRAM_BOT_TOKEN", &cfg.Alerts.Telegram.BotToken)
	stringEnv("FISCUS_TELEGRAM_CHAT_ID", &cfg.Alerts.Telegram.ChatID)
	stringEnv("FISCUS_DISCORD_WEBHOOK_URL", &cfg.Alerts.Discord.WebhookURL)

	stringEnv("FISCUS_LOG_LEVEL", &cfg.Logging.Level)
	stringEnv("FISCUS_LOG_FORMAT", &cfg.Logging.Format)

	return errors.Join(errs...)
}

func stringEnv(name string, target *string) {
	if value, ok := os.LookupEnv(name); ok {
		*target = value
	}
}

func durationEnv(name string, target *Duration, errs *[]error) {
	value, ok := os.LookupEnv(name)
	if !ok {
		return
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: invalid duration %q", name, value))
		return
	}
	*target = Duration(parsed)
}

func intEnv(name string, target *int, errs *[]error) {
	value, ok := os.LookupEnv(name)
	if !ok {
		return
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: invalid integer %q", name, value))
		return
	}
	*target = parsed
}

func floatEnv(name string, target *float64, errs *[]error) {
	value, ok := os.LookupEnv(name)
	if !ok {
		return
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: invalid number %q", name, value))
		return
	}
	*target = parsed
}

func parseThresholdsEnv(raw string) (map[string]string, error) {
	out := make(map[string]string)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		currency, amount, ok := strings.Cut(entry, ":")
		if !ok || strings.TrimSpace(currency) == "" || strings.TrimSpace(amount) == "" {
			return nil, fmt.Errorf("FISCUS_LIQUIDITY_THRESHOLDS: invalid entry %q, expected CUR:AMOUNT", entry)
		}
		out[strings.ToUpper(strings.TrimSpace(currency))] = strings.TrimSpace(amount)
	}
	if len(out) == 0 {
		return nil, errors.New("FISCUS_LIQUIDITY_THRESHOLDS: no thresholds specified")
	}
	return out, nil
}
