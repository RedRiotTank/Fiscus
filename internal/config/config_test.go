package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestDefaultConfig(t *testing.T) {
	t.Parallel()
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	if cfg.Server.Address != ":8080" || cfg.Database.Path != "data/fiscus.db" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.GoCardless.SyncInterval.Std() != 15*time.Minute {
		t.Fatalf("sync interval = %v", cfg.GoCardless.SyncInterval)
	}
	if cfg.BankingConfigured() || cfg.AuthConfigured() || cfg.TelegramConfigured() || cfg.DiscordConfigured() {
		t.Fatal("credentials should be empty by default")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestLoadYAMLAndEnv(t *testing.T) {
	path := writeConfig(t, `
server:
  address: "127.0.0.1:9090"
  username: admin
  password: secret
  read_header_timeout: 5s
database:
  path: /tmp/fiscus-test.db
gocardless:
  secret_id: id
  secret_key: key
  redirect_url: https://fiscus.example/callback
  country: gb
  sync_interval: 30m
  initial_lookback_days: 60
alerts:
  duplicate_window: 12h
  anomaly_threshold: 0.5
  liquidity_thresholds:
    EUR: "750.00"
    JPY: "10000"
  telegram:
    bot_token: token
    chat_id: chat
  discord:
    webhook_url: https://discord.example/webhook
logging:
  level: debug
  format: json
`)
	t.Setenv("FISCUS_SERVER_ADDRESS", "127.0.0.1:7070")
	t.Setenv("FISCUS_GOCARDLESS_SYNC_INTERVAL", "10m")
	t.Setenv("FISCUS_ALERTS_RETRY_ATTEMPTS", "5")
	t.Setenv("FISCUS_LIQUIDITY_THRESHOLDS", "EUR:800.00, USD: 25.50")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Address != "127.0.0.1:7070" {
		t.Fatalf("env override failed: %q", cfg.Server.Address)
	}
	if cfg.Server.ReadHeaderTimeout.Std() != 5*time.Second {
		t.Fatalf("read header timeout = %v", cfg.Server.ReadHeaderTimeout)
	}
	if cfg.GoCardless.SyncInterval.Std() != 10*time.Minute {
		t.Fatalf("sync interval = %v", cfg.GoCardless.SyncInterval)
	}
	if cfg.Alerts.DuplicateWindow.Std() != 12*time.Hour || cfg.Alerts.AnomalyThreshold != 0.5 {
		t.Fatalf("alerts config = %+v", cfg.Alerts)
	}
	if cfg.Alerts.RetryAttempts != 5 {
		t.Fatalf("retry attempts = %d", cfg.Alerts.RetryAttempts)
	}
	thresholds, err := cfg.LiquidityThresholdsMinor()
	if err != nil {
		t.Fatalf("LiquidityThresholdsMinor: %v", err)
	}
	if thresholds["EUR"] != 80000 || thresholds["USD"] != 2550 {
		t.Fatalf("thresholds = %+v", thresholds)
	}
	if !cfg.BankingConfigured() || !cfg.TelegramConfigured() || !cfg.DiscordConfigured() || !cfg.AuthConfigured() {
		t.Fatal("configured flags are wrong")
	}
}

func TestLoadMissingFile(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	if cfg.Database.Path != Default().Database.Path {
		t.Fatalf("defaults were not applied: %+v", cfg.Database)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := writeConfig(t, "database:\n  path: x.db\nunknown_field: true\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestLoadRejectsMalformedYAML(t *testing.T) {
	path := writeConfig(t, "server: [not-a-map\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected YAML error")
	}
}

func TestValidationErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "empty address", mutate: func(c *Config) { c.Server.Address = "" }, want: "server.address"},
		{name: "empty database", mutate: func(c *Config) { c.Database.Path = "" }, want: "database.path"},
		{name: "password without username", mutate: func(c *Config) { c.Server.Password = "x" }, want: "server.username"},
		{name: "username without password", mutate: func(c *Config) { c.Server.Username = "admin" }, want: "server.password"},
		{name: "both passwords", mutate: func(c *Config) {
			c.Server.Username = "a"
			c.Server.Password = "b"
			c.Server.PasswordHash = "$2a$10$hash"
		}, want: "only one"},
		{name: "bad hash", mutate: func(c *Config) { c.Server.Username = "a"; c.Server.PasswordHash = "plain" }, want: "bcrypt"},
		{name: "half credentials", mutate: func(c *Config) { c.GoCardless.SecretID = "id" }, want: "configured together"},
		{name: "telegram token only", mutate: func(c *Config) { c.Alerts.Telegram.BotToken = "token" }, want: "configured together"},
		{name: "telegram chat only", mutate: func(c *Config) { c.Alerts.Telegram.ChatID = "chat" }, want: "configured together"},
		{name: "bad redirect", mutate: func(c *Config) { c.GoCardless.RedirectURL = "not-a-url" }, want: "redirect_url"},
		{name: "bad base url", mutate: func(c *Config) { c.GoCardless.BaseURL = "://bad" }, want: "base_url"},
		{name: "zero sync interval", mutate: func(c *Config) { c.GoCardless.SyncInterval = 0 }, want: "sync_interval"},
		{name: "negative overlap", mutate: func(c *Config) { c.GoCardless.OverlapDays = -1 }, want: "overlap_days"},
		{name: "zero duplicate window", mutate: func(c *Config) { c.Alerts.DuplicateWindow = 0 }, want: "duplicate_window"},
		{name: "zero anomaly", mutate: func(c *Config) { c.Alerts.AnomalyThreshold = 0 }, want: "anomaly_threshold"},
		{name: "bad threshold", mutate: func(c *Config) { c.Alerts.LiquidityThresholds = map[string]string{"EUR": "abc"} }, want: "threshold"},
		{name: "threshold precision", mutate: func(c *Config) { c.Alerts.LiquidityThresholds = map[string]string{"EUR": "1.234"} }, want: "decimal places"},
		{name: "zero threshold", mutate: func(c *Config) { c.Alerts.LiquidityThresholds = map[string]string{"EUR": "0"} }, want: "positive"},
		{name: "bad log level", mutate: func(c *Config) { c.Logging.Level = "verbose" }, want: "logging.level"},
		{name: "bad log format", mutate: func(c *Config) { c.Logging.Format = "xml" }, want: "logging.format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			tt.mutate(cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("expected validation error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestInvalidEnv(t *testing.T) {
	t.Setenv("FISCUS_GOCARDLESS_SYNC_INTERVAL", "soon")
	t.Setenv("FISCUS_ALERTS_RETRY_ATTEMPTS", "many")
	t.Setenv("FISCUS_ALERTS_ANOMALY_THRESHOLD", "high")
	t.Setenv("FISCUS_LIQUIDITY_THRESHOLDS", "EUR")
	_, err := Load("")
	if err == nil {
		t.Fatal("expected env errors")
	}
	for _, want := range []string{"FISCUS_GOCARDLESS_SYNC_INTERVAL", "FISCUS_ALERTS_RETRY_ATTEMPTS", "FISCUS_ALERTS_ANOMALY_THRESHOLD", "FISCUS_LIQUIDITY_THRESHOLDS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %v missing %s", err, want)
		}
	}
}

func TestLiquidityThresholdsMinor(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Alerts.LiquidityThresholds = map[string]string{
		"eur": "500.00",
		"JPY": "1000",
		"KWD": "1.234",
	}
	got, err := cfg.LiquidityThresholdsMinor()
	if err != nil {
		t.Fatalf("LiquidityThresholdsMinor: %v", err)
	}
	if got["EUR"] != 50000 || got["JPY"] != 1000 || got["KWD"] != 1234 {
		t.Fatalf("thresholds = %+v", got)
	}
	cfg.Alerts.LiquidityThresholds = map[string]string{"": "10"}
	if _, err := cfg.LiquidityThresholdsMinor(); err == nil {
		t.Fatal("expected empty currency error")
	}
}

func TestDuration(t *testing.T) {
	t.Parallel()
	cfg := Default()
	if got := cfg.Server.ShutdownTimeout.String(); got != "10s" {
		t.Fatalf("duration string = %q", got)
	}
	if got := cfg.Server.ShutdownTimeout.Std(); got != 10*time.Second {
		t.Fatalf("duration std = %v", got)
	}
}
