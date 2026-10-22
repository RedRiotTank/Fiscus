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
	if cfg.ProviderName() != "gocardless" {
		t.Fatalf("provider = %q", cfg.ProviderName())
	}
	if cfg.EnableBanking.BaseURL != "https://api.enablebanking.com" ||
		cfg.EnableBanking.ConsentValidityDays != 90 ||
		cfg.EnableBanking.PSUType != "personal" ||
		cfg.EnableBanking.SyncInterval.Std() != 15*time.Minute {
		t.Fatalf("enablebanking defaults = %+v", cfg.EnableBanking)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestLoadCategoryRules(t *testing.T) {
	path := writeConfig(t, `
rules:
  - category: Utilities
    pattern: "(?i)IBERDROLA|ENDESA|AGUAS"
  - category: Payroll
    pattern: "(?i)NOMINA|SALARY"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Rules) != 2 || cfg.Rules[0].Category != "Utilities" || cfg.Rules[1].Pattern != "(?i)NOMINA|SALARY" {
		t.Fatalf("rules = %+v", cfg.Rules)
	}
}

func TestInvalidCategoryRuleRejected(t *testing.T) {
	path := writeConfig(t, `
rules:
  - category: Broken
    pattern: "("
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected invalid regex error")
	}
}

func TestLoadBudgets(t *testing.T) {
	path := writeConfig(t, `
budgets:
  - category: Restaurants
    limit: 30000
    notify_at_percent: [80, 100]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Budgets) != 1 || cfg.Budgets[0].Category != "Restaurants" ||
		cfg.Budgets[0].Limit != 30000 || len(cfg.Budgets[0].NotifyAtPercent) != 2 {
		t.Fatalf("budgets = %+v", cfg.Budgets)
	}
}

func TestInvalidBudgetsRejected(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{name: "missing category", yaml: "budgets:\n  - limit: 100\n"},
		{name: "non-positive limit", yaml: "budgets:\n  - category: Dining\n    limit: 0\n"},
		{name: "invalid threshold", yaml: "budgets:\n  - category: Dining\n    limit: 100\n    notify_at_percent: [0]\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, test.yaml)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLoadEnableBankingConfig(t *testing.T) {
	path := writeConfig(t, `
provider: enablebanking
enablebanking:
  app_id: app-id
  private_key_path: /run/secrets/enablebanking.pem
  redirect_url: http://localhost:8080/callback
  country: es
  institution_name: Banco Santander
  psu_type: business
  consent_validity_days: 30
  sync_interval: 20m
  sync_timeout: 2m
  initial_lookback_days: 45
  overlap_days: 5
`)
	t.Setenv("FISCUS_PROVIDER", "enablebanking")
	t.Setenv("FISCUS_ENABLEBANKING_APP_ID", "env-app")
	t.Setenv("FISCUS_ENABLEBANKING_PRIVATE_KEY_PATH", "/run/secrets/env.pem")
	t.Setenv("FISCUS_ENABLEBANKING_REDIRECT_URL", "http://localhost:9090/callback")
	t.Setenv("FISCUS_ENABLEBANKING_COUNTRY", "ES")
	t.Setenv("FISCUS_ENABLEBANKING_PSU_TYPE", "personal")
	t.Setenv("FISCUS_ENABLEBANKING_CONSENT_VALIDITY_DAYS", "60")
	t.Setenv("FISCUS_ENABLEBANKING_SYNC_INTERVAL", "5m")
	t.Setenv("FISCUS_ENABLEBANKING_OVERLAP_DAYS", "2")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ProviderName() != "enablebanking" {
		t.Fatalf("provider = %q", cfg.ProviderName())
	}
	if !cfg.BankingConfigured() || cfg.GoCardlessConfigured() {
		t.Fatalf("configured flags: banking=%v gocardless=%v", cfg.BankingConfigured(), cfg.GoCardlessConfigured())
	}
	if cfg.EnableBanking.AppID != "env-app" ||
		cfg.EnableBanking.PrivateKeyPath != "/run/secrets/env.pem" ||
		cfg.EnableBanking.RedirectURL != "http://localhost:9090/callback" ||
		cfg.EnableBanking.PSUType != "personal" ||
		cfg.EnableBanking.ConsentValidityDays != 60 ||
		cfg.EnableBanking.SyncInterval.Std() != 5*time.Minute ||
		cfg.EnableBanking.OverlapDays != 2 {
		t.Fatalf("enablebanking config = %+v", cfg.EnableBanking)
	}
	if cfg.EnableBanking.InstitutionName != "Banco Santander" || cfg.EnableBanking.InitialLookbackDays != 45 {
		t.Fatalf("yaml values were not preserved: %+v", cfg.EnableBanking)
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
	t.Setenv("FISCUS_TELEGRAM_USER_ID", "1234")

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
	if cfg.Alerts.Telegram.UserID != "1234" {
		t.Fatalf("telegram user ID = %q", cfg.Alerts.Telegram.UserID)
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
		{name: "TLS certificate only", mutate: func(c *Config) { c.Server.TLSCertFile = "/tmp/cert.pem" }, want: "tls_key_file"},
		{name: "TLS key only", mutate: func(c *Config) { c.Server.TLSKeyFile = "/tmp/key.pem" }, want: "tls_cert_file"},
		{name: "half credentials", mutate: func(c *Config) { c.GoCardless.SecretID = "id" }, want: "configured together"},
		{name: "telegram token only", mutate: func(c *Config) { c.Alerts.Telegram.BotToken = "token" }, want: "configured together"},
		{name: "telegram chat only", mutate: func(c *Config) { c.Alerts.Telegram.ChatID = "chat" }, want: "configured together"},
		{name: "bad redirect", mutate: func(c *Config) { c.GoCardless.RedirectURL = "not-a-url" }, want: "redirect_url"},
		{name: "bad base url", mutate: func(c *Config) { c.GoCardless.BaseURL = "://bad" }, want: "base_url"},
		{name: "bad provider", mutate: func(c *Config) { c.Provider = "other" }, want: "provider"},
		{name: "enablebanking app only", mutate: func(c *Config) { c.EnableBanking.AppID = "app" }, want: "configured together"},
		{name: "enablebanking key only", mutate: func(c *Config) { c.EnableBanking.PrivateKeyPath = "/tmp/key.pem" }, want: "configured together"},
		{name: "enablebanking both keys", mutate: func(c *Config) {
			c.EnableBanking.AppID = "app"
			c.EnableBanking.PrivateKeyPath = "/tmp/key.pem"
			c.EnableBanking.PrivateKey = "pem"
			c.EnableBanking.RedirectURL = "http://localhost:8080/callback"
		}, want: "only one"},
		{name: "enablebanking missing redirect", mutate: func(c *Config) {
			c.EnableBanking.AppID = "app"
			c.EnableBanking.PrivateKeyPath = "/tmp/key.pem"
		}, want: "redirect_url"},
		{name: "enablebanking bad base", mutate: func(c *Config) { c.EnableBanking.BaseURL = "://bad" }, want: "base_url"},
		{name: "enablebanking bad redirect", mutate: func(c *Config) {
			c.EnableBanking.AppID = "app"
			c.EnableBanking.PrivateKeyPath = "/tmp/key.pem"
			c.EnableBanking.RedirectURL = "not-a-url"
		}, want: "redirect_url"},
		{name: "enablebanking bad psu", mutate: func(c *Config) { c.EnableBanking.PSUType = "robot" }, want: "psu_type"},
		{name: "enablebanking zero consent", mutate: func(c *Config) { c.EnableBanking.ConsentValidityDays = 0 }, want: "consent_validity_days"},
		{name: "enablebanking zero sync", mutate: func(c *Config) { c.EnableBanking.SyncInterval = 0 }, want: "sync_interval"},
		{name: "enablebanking negative overlap", mutate: func(c *Config) { c.EnableBanking.OverlapDays = -1 }, want: "overlap_days"},
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
