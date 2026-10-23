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
	if cfg.GoCardless.SyncInterval.Std() != 6*time.Hour {
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
		cfg.EnableBanking.SyncInterval.Std() != 6*time.Hour {
		t.Fatalf("enablebanking defaults = %+v", cfg.EnableBanking)
	}
	if !cfg.Attachments.Enabled || cfg.Attachments.Directory != "data/attachments" ||
		cfg.Attachments.MaxSizeBytes != 10<<20 || cfg.Attachments.ReceiptThresholdMinor != 5000 ||
		cfg.Attachments.GraceDays != 5 {
		t.Fatalf("attachments defaults = %+v", cfg.Attachments)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestLoadAttachments(t *testing.T) {
	path := writeConfig(t, `
attachments:
  enabled: true
  directory: "var/receipts"
  max_size_bytes: 2048
  receipt_threshold_minor: 7000
  grace_days: 4
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Attachments.Directory != "var/receipts" || cfg.Attachments.MaxSizeBytes != 2048 ||
		cfg.Attachments.ReceiptThresholdMinor != 7000 || cfg.Attachments.GraceDays != 4 {
		t.Fatalf("attachments = %+v", cfg.Attachments)
	}
}

func TestInvalidAttachmentsRejected(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{name: "missing directory", yaml: "attachments:\n  enabled: true\n  directory: \"\"\n"},
		{name: "zero max size", yaml: "attachments:\n  enabled: true\n  max_size_bytes: 0\n"},
		{name: "oversize max", yaml: "attachments:\n  enabled: true\n  max_size_bytes: 104857601\n"},
		{name: "negative threshold", yaml: "attachments:\n  enabled: true\n  receipt_threshold_minor: -1\n"},
		{name: "invalid grace", yaml: "attachments:\n  enabled: true\n  grace_days: 400\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, test.yaml)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestAttachmentsEnvironmentOverride(t *testing.T) {
	t.Setenv("FISCUS_ATTACHMENTS_ENABLED", "false")
	t.Setenv("FISCUS_ATTACHMENTS_DIRECTORY", "env/receipts")
	t.Setenv("FISCUS_ATTACHMENTS_MAX_SIZE_BYTES", "4096")
	t.Setenv("FISCUS_ATTACHMENTS_RECEIPT_THRESHOLD_MINOR", "9000")
	t.Setenv("FISCUS_ATTACHMENTS_GRACE_DAYS", "2")
	cfg, err := Load(writeConfig(t, "profile: hybrid\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Attachments.Enabled || cfg.Attachments.Directory != "env/receipts" ||
		cfg.Attachments.MaxSizeBytes != 4096 || cfg.Attachments.ReceiptThresholdMinor != 9000 ||
		cfg.Attachments.GraceDays != 2 {
		t.Fatalf("attachments env = %+v", cfg.Attachments)
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

func TestLoadExpectedInflows(t *testing.T) {
	path := writeConfig(t, `
expected_inflows:
  - name: Payroll
    amount_min: 200000
    expected_days: [27, 31]
    grace_days: 3
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.ExpectedInflows) != 1 || cfg.ExpectedInflows[0].Name != "Payroll" ||
		cfg.ExpectedInflows[0].AmountMin != 200000 || cfg.ExpectedInflows[0].GraceDays != 3 {
		t.Fatalf("inflows = %+v", cfg.ExpectedInflows)
	}
}

func TestInvalidExpectedInflowsRejected(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{name: "missing name", yaml: "expected_inflows:\n  - amount_min: 100\n    expected_days: [1]\n"},
		{name: "non-positive amount", yaml: "expected_inflows:\n  - name: Payroll\n    amount_min: 0\n    expected_days: [1]\n"},
		{name: "missing days", yaml: "expected_inflows:\n  - name: Payroll\n    amount_min: 100\n"},
		{name: "invalid day", yaml: "expected_inflows:\n  - name: Payroll\n    amount_min: 100\n    expected_days: [32]\n"},
		{name: "negative grace", yaml: "expected_inflows:\n  - name: Payroll\n    amount_min: 100\n    expected_days: [1]\n    grace_days: -1\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, test.yaml)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLoadBackupsAndWebhooks(t *testing.T) {
	path := writeConfig(t, `
backups:
  enabled: true
  telegram: true
  directory: snapshots
  interval: 24h
webhooks:
  - url: https://example.test/events
    secret: top-secret
    events: [transaction.created, alert.triggered]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Backups.Enabled || !cfg.Backups.Telegram || cfg.Backups.Directory != "snapshots" || cfg.Backups.Interval.Std() != 24*time.Hour {
		t.Fatalf("backups = %+v", cfg.Backups)
	}
	if len(cfg.Webhooks) != 1 || cfg.Webhooks[0].URL != "https://example.test/events" || len(cfg.Webhooks[0].Events) != 2 {
		t.Fatalf("webhooks = %+v", cfg.Webhooks)
	}
}

func TestInvalidBackupsAndWebhooksRejected(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{name: "telegram without enabled", yaml: "backups:\n  telegram: true\n"},
		{name: "invalid interval", yaml: "backups:\n  interval: 0s\n"},
		{name: "invalid webhook URL", yaml: "webhooks:\n  - url: ftp://example.test\n    events: [sync.completed]\n"},
		{name: "empty webhook events", yaml: "webhooks:\n  - url: https://example.test\n"},
		{name: "unknown webhook event", yaml: "webhooks:\n  - url: https://example.test\n    events: [unknown]\n"},
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

func TestWorkspaceProfileAndAccountTypes(t *testing.T) {
	path := writeConfig(t, `
profile: hybrid
accounts:
  - id: acc-business
    type: business
  - id: acc-personal
    type: personal
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.WorkspaceProfile() != "hybrid" {
		t.Fatalf("profile = %q", cfg.WorkspaceProfile())
	}
	types := cfg.AccountTypes()
	if types["acc-business"] != "business" || types["acc-personal"] != "personal" || len(types) != 2 {
		t.Fatalf("account types = %+v", types)
	}
}

func TestInvalidWorkspaceConfigurationRejected(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{name: "invalid profile", yaml: "profile: charity\n"},
		{name: "missing account id", yaml: "accounts:\n  - type: business\n"},
		{name: "invalid account type", yaml: "accounts:\n  - id: acc-1\n    type: shared\n"},
		{name: "duplicate account", yaml: "accounts:\n  - id: acc-1\n    type: personal\n  - id: acc-1\n    type: business\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, test.yaml)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestWorkspaceProfileEnvironmentOverride(t *testing.T) {
	t.Setenv("FISCUS_PROFILE", "business")
	cfg, err := Load(writeConfig(t, "profile: personal\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.WorkspaceProfile() != "business" {
		t.Fatalf("profile = %q", cfg.WorkspaceProfile())
	}
}
