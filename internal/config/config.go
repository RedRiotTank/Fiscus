// Package config loads Fiscus configuration from an optional YAML file and
// environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the complete runtime configuration.
type Config struct {
	Provider        string                 `yaml:"provider"`
	Profile         string                 `yaml:"profile"`
	Accounts        []AccountConfig        `yaml:"accounts"`
	Server          ServerConfig           `yaml:"server"`
	Database        DatabaseConfig         `yaml:"database"`
	GoCardless      GoCardlessConfig       `yaml:"gocardless"`
	EnableBanking   EnableBankingConfig    `yaml:"enablebanking"`
	Alerts          AlertsConfig           `yaml:"alerts"`
	Logging         LoggingConfig          `yaml:"logging"`
	Rules           []CategoryRule         `yaml:"rules"`
	Budgets         []BudgetConfig         `yaml:"budgets"`
	ExpectedInflows []ExpectedInflowConfig `yaml:"expected_inflows"`
	Attachments     AttachmentsConfig      `yaml:"attachments"`
	Tax             TaxConfig              `yaml:"tax"`
	Backups         BackupsConfig          `yaml:"backups"`
	Webhooks        []WebhookConfig        `yaml:"webhooks"`
}

// CategoryRule tags a transaction whose counterparty or description matches
// Pattern. Rules are applied in order; the first match wins.
type CategoryRule struct {
	Category string `yaml:"category"`
	Pattern  string `yaml:"pattern"`
}

// AccountConfig assigns a workspace type to a bank account. The ID matches the
// provider account ID shown on the Connections page.
type AccountConfig struct {
	ID   string `yaml:"id"`
	Type string `yaml:"type"`
}

// BudgetConfig caps month-to-date spending for one category. Limit is in
// integer minor units. Currency defaults to EUR when omitted.
type BudgetConfig struct {
	Category        string `yaml:"category"`
	Limit           int64  `yaml:"limit"`
	Currency        string `yaml:"currency"`
	NotifyAtPercent []int  `yaml:"notify_at_percent"`
}

// ExpectedInflowConfig describes a recurring credit that should arrive each
// month. AmountMin is in integer minor units. Days are calendar days of the
// month; GraceDays defaults to 2 when omitted.
type ExpectedInflowConfig struct {
	Name         string `yaml:"name"`
	AmountMin    int64  `yaml:"amount_min"`
	Currency     string `yaml:"currency"`
	ExpectedDays []int  `yaml:"expected_days"`
	GraceDays    int    `yaml:"grace_days"`
}

// AttachmentsConfig controls secure receipt and invoice storage. Monetary
// thresholds are integer minor units.
type AttachmentsConfig struct {
	Enabled               bool   `yaml:"enabled"`
	Directory             string `yaml:"directory"`
	MaxSizeBytes          int64  `yaml:"max_size_bytes"`
	ReceiptThresholdMinor int64  `yaml:"receipt_threshold_minor"`
	GraceDays             int    `yaml:"grace_days"`
}

// TaxConfig controls the quarterly VAT and income-tax liquidity shield. Rates
// are decimal fractions (0.21 = 21%) and QuarterStartMonths must contain the
// four month numbers that begin each fiscal quarter.
type TaxConfig struct {
	Enabled            bool    `yaml:"enabled"`
	VATRateDefault     float64 `yaml:"vat_rate_default"`
	IncomeTaxRetention float64 `yaml:"income_tax_retention"`
	QuarterStartMonths []int   `yaml:"quarter_start_months"`
}

// BackupsConfig controls optional scheduled backups and Telegram delivery.
type BackupsConfig struct {
	Enabled   bool     `yaml:"enabled"`
	Telegram  bool     `yaml:"telegram"`
	Directory string   `yaml:"directory"`
	Interval  Duration `yaml:"interval"`
}

// WebhookConfig describes a generic outbound JSON webhook. Secret is optional;
// when set it is used to compute an HMAC-SHA256 signature.
type WebhookConfig struct {
	URL    string   `yaml:"url"`
	Secret string   `yaml:"secret"`
	Events []string `yaml:"events"`
}

// ServerConfig controls the HTTP(S) dashboard.
type ServerConfig struct {
	Address           string   `yaml:"address"`
	Username          string   `yaml:"username"`
	Password          string   `yaml:"password"`
	PasswordHash      string   `yaml:"password_hash"`
	TLSCertFile       string   `yaml:"tls_cert_file"`
	TLSKeyFile        string   `yaml:"tls_key_file"`
	ReadHeaderTimeout Duration `yaml:"read_header_timeout"`
	ReadTimeout       Duration `yaml:"read_timeout"`
	WriteTimeout      Duration `yaml:"write_timeout"`
	IdleTimeout       Duration `yaml:"idle_timeout"`
	ShutdownTimeout   Duration `yaml:"shutdown_timeout"`
}

// DatabaseConfig controls SQLite persistence.
type DatabaseConfig struct {
	Path string `yaml:"path"`
}

// GoCardlessConfig controls PSD2 ingestion through GoCardless Bank Account Data.
type GoCardlessConfig struct {
	BaseURL             string   `yaml:"base_url"`
	SecretID            string   `yaml:"secret_id"`
	SecretKey           string   `yaml:"secret_key"`
	RedirectURL         string   `yaml:"redirect_url"`
	Country             string   `yaml:"country"`
	InstitutionID       string   `yaml:"institution_id"`
	InstitutionName     string   `yaml:"institution_name"`
	SyncInterval        Duration `yaml:"sync_interval"`
	SyncTimeout         Duration `yaml:"sync_timeout"`
	InitialLookbackDays int      `yaml:"initial_lookback_days"`
	OverlapDays         int      `yaml:"overlap_days"`
}

// EnableBankingConfig controls PSD2 ingestion through Enable Banking.
type EnableBankingConfig struct {
	BaseURL             string   `yaml:"base_url"`
	AppID               string   `yaml:"app_id"`
	PrivateKeyPath      string   `yaml:"private_key_path"`
	PrivateKey          string   `yaml:"private_key"`
	RedirectURL         string   `yaml:"redirect_url"`
	Country             string   `yaml:"country"`
	InstitutionName     string   `yaml:"institution_name"`
	PSUType             string   `yaml:"psu_type"`
	ConsentValidityDays int      `yaml:"consent_validity_days"`
	SyncInterval        Duration `yaml:"sync_interval"`
	SyncTimeout         Duration `yaml:"sync_timeout"`
	InitialLookbackDays int      `yaml:"initial_lookback_days"`
	OverlapDays         int      `yaml:"overlap_days"`
}

// AlertsConfig controls rules and notification channels.
type AlertsConfig struct {
	DuplicateWindow     Duration          `yaml:"duplicate_window"`
	AnomalyThreshold    float64           `yaml:"anomaly_threshold"`
	AnomalyLookbackDays int               `yaml:"anomaly_lookback_days"`
	AnomalyMinHistory   int               `yaml:"anomaly_min_history"`
	LiquidityThresholds map[string]string `yaml:"liquidity_thresholds"`
	Telegram            TelegramConfig    `yaml:"telegram"`
	Discord             DiscordConfig     `yaml:"discord"`
	RetryAttempts       int               `yaml:"retry_attempts"`
	RetryBaseDelay      Duration          `yaml:"retry_base_delay"`
}

// TelegramConfig configures the Telegram notifier.
type TelegramConfig struct {
	BotToken            string `yaml:"bot_token"`
	ChatID              string `yaml:"chat_id"`
	UserID              string `yaml:"user_id"`
	NotifyUncategorized bool   `yaml:"notify_uncategorized"`
}

// DiscordConfig configures the Discord notifier.
type DiscordConfig struct {
	WebhookURL string `yaml:"webhook_url"`
}

// LoggingConfig controls structured logging.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// Default returns production-oriented defaults. Secrets are intentionally empty.
func Default() *Config {
	return &Config{
		Provider: "gocardless",
		Profile:  "hybrid",
		Accounts: []AccountConfig{},
		Server: ServerConfig{
			Address:           ":8080",
			ReadHeaderTimeout: Duration(10 * time.Second),
			ReadTimeout:       Duration(30 * time.Second),
			WriteTimeout:      Duration(30 * time.Second),
			IdleTimeout:       Duration(90 * time.Second),
			ShutdownTimeout:   Duration(10 * time.Second),
		},
		Database: DatabaseConfig{Path: "data/fiscus.db"},
		GoCardless: GoCardlessConfig{
			BaseURL:             "https://bankaccountdata.gocardless.com/api/v2",
			Country:             "ES",
			SyncInterval:        Duration(6 * time.Hour),
			SyncTimeout:         Duration(5 * time.Minute),
			InitialLookbackDays: 90,
			OverlapDays:         3,
		},
		EnableBanking: EnableBankingConfig{
			BaseURL:             "https://api.enablebanking.com",
			Country:             "ES",
			PSUType:             "personal",
			ConsentValidityDays: 90,
			SyncInterval:        Duration(6 * time.Hour),
			SyncTimeout:         Duration(5 * time.Minute),
			InitialLookbackDays: 90,
			OverlapDays:         3,
		},
		Alerts: AlertsConfig{
			DuplicateWindow:     Duration(24 * time.Hour),
			AnomalyThreshold:    0.30,
			AnomalyLookbackDays: 90,
			AnomalyMinHistory:   3,
			LiquidityThresholds: map[string]string{},
			RetryAttempts:       3,
			RetryBaseDelay:      Duration(500 * time.Millisecond),
			Telegram: TelegramConfig{
				NotifyUncategorized: true,
			},
		},
		Attachments: AttachmentsConfig{
			Enabled:               true,
			Directory:             "data/attachments",
			MaxSizeBytes:          10 << 20,
			ReceiptThresholdMinor: 5000,
			GraceDays:             5,
		},
		Tax: TaxConfig{
			Enabled:            true,
			VATRateDefault:     0.21,
			IncomeTaxRetention: 0.15,
			QuarterStartMonths: []int{1, 4, 7, 10},
		},
		Backups: BackupsConfig{
			Directory: "backups",
			Interval:  Duration(7 * 24 * time.Hour),
		},
		Logging: LoggingConfig{Level: "info", Format: "text"},
	}
}

// Load reads an optional YAML file (missing files are ignored), applies
// environment overrides, and validates the result.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path != "" {
		// #nosec G304 -- the configuration path is supplied by the operator.
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			decoder := yaml.NewDecoder(strings.NewReader(string(data)))
			decoder.KnownFields(true)
			if err := decoder.Decode(cfg); err != nil {
				return nil, fmt.Errorf("config: parse %s: %w", path, err)
			}
		case errors.Is(err, os.ErrNotExist):
			// Configuration is optional; environment variables may be sufficient.
		default:
			return nil, fmt.Errorf("config: read %s: %w", path, err)
		}
	}
	if err := applyEnv(cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate checks required fields, durations, and cross-field consistency.
func (c *Config) Validate() error {
	var errs []error
	switch c.ProviderName() {
	case "gocardless", "enablebanking":
	default:
		errs = append(errs, fmt.Errorf("provider %q must be gocardless or enablebanking", c.Provider))
	}
	switch strings.ToLower(strings.TrimSpace(c.Profile)) {
	case "", "personal", "business", "hybrid":
	default:
		errs = append(errs, fmt.Errorf("profile %q must be personal, business, or hybrid", c.Profile))
	}
	seenAccounts := make(map[string]struct{}, len(c.Accounts))
	for i, account := range c.Accounts {
		id := strings.TrimSpace(account.ID)
		if id == "" {
			errs = append(errs, fmt.Errorf("accounts[%d].id is required", i))
			continue
		}
		if _, exists := seenAccounts[id]; exists {
			errs = append(errs, fmt.Errorf("accounts[%d].id %q is duplicated", i, id))
		}
		seenAccounts[id] = struct{}{}
		switch strings.ToLower(strings.TrimSpace(account.Type)) {
		case "personal", "business":
		default:
			errs = append(errs, fmt.Errorf("accounts[%d].type %q must be personal or business", i, account.Type))
		}
	}
	if strings.TrimSpace(c.Server.Address) == "" {
		errs = append(errs, errors.New("server.address is required"))
	}
	if c.Server.ReadHeaderTimeout <= 0 {
		errs = append(errs, errors.New("server.read_header_timeout must be positive"))
	}
	if c.Server.ShutdownTimeout <= 0 {
		errs = append(errs, errors.New("server.shutdown_timeout must be positive"))
	}
	if strings.TrimSpace(c.Database.Path) == "" {
		errs = append(errs, errors.New("database.path is required"))
	}
	if c.Server.Username == "" && (c.Server.Password != "" || c.Server.PasswordHash != "") {
		errs = append(errs, errors.New("server.username is required when a password is configured"))
	}
	if c.Server.Username != "" && c.Server.Password == "" && c.Server.PasswordHash == "" {
		errs = append(errs, errors.New("server.password or server.password_hash is required when a username is configured"))
	}
	if c.Server.Password != "" && c.Server.PasswordHash != "" {
		errs = append(errs, errors.New("configure only one of server.password and server.password_hash"))
	}
	if c.Server.PasswordHash != "" && !strings.HasPrefix(c.Server.PasswordHash, "$2") {
		errs = append(errs, errors.New("server.password_hash must be a bcrypt hash"))
	}
	if cert, key := strings.TrimSpace(c.Server.TLSCertFile), strings.TrimSpace(c.Server.TLSKeyFile); (cert == "") != (key == "") {
		errs = append(errs, errors.New("server.tls_cert_file and server.tls_key_file must be configured together"))
	}
	if id, key := strings.TrimSpace(c.GoCardless.SecretID), strings.TrimSpace(c.GoCardless.SecretKey); (id == "") != (key == "") {
		errs = append(errs, errors.New("gocardless.secret_id and gocardless.secret_key must be configured together"))
	}
	if token, chat := strings.TrimSpace(c.Alerts.Telegram.BotToken), strings.TrimSpace(c.Alerts.Telegram.ChatID); (token == "") != (chat == "") {
		errs = append(errs, errors.New("alerts.telegram.bot_token and alerts.telegram.chat_id must be configured together"))
	}
	if c.GoCardless.SyncInterval <= 0 {
		errs = append(errs, errors.New("gocardless.sync_interval must be positive"))
	}
	if c.GoCardless.SyncTimeout <= 0 {
		errs = append(errs, errors.New("gocardless.sync_timeout must be positive"))
	}
	if c.GoCardless.InitialLookbackDays <= 0 {
		errs = append(errs, errors.New("gocardless.initial_lookback_days must be positive"))
	}
	if c.GoCardless.OverlapDays < 0 {
		errs = append(errs, errors.New("gocardless.overlap_days cannot be negative"))
	}
	if c.GoCardless.BaseURL != "" && !validHTTPURL(c.GoCardless.BaseURL) {
		errs = append(errs, errors.New("gocardless.base_url must be an absolute http(s) URL"))
	}
	if c.GoCardless.RedirectURL != "" && !validHTTPURL(c.GoCardless.RedirectURL) {
		errs = append(errs, errors.New("gocardless.redirect_url must be an absolute http(s) URL"))
	}
	eb := c.EnableBanking
	hasAppID := strings.TrimSpace(eb.AppID) != ""
	hasPrivateKey := strings.TrimSpace(eb.PrivateKeyPath) != "" || strings.TrimSpace(eb.PrivateKey) != ""
	if hasAppID != hasPrivateKey {
		errs = append(errs, errors.New("enablebanking.app_id and a private key must be configured together"))
	}
	if strings.TrimSpace(eb.PrivateKeyPath) != "" && strings.TrimSpace(eb.PrivateKey) != "" {
		errs = append(errs, errors.New("configure only one of enablebanking.private_key_path and enablebanking.private_key"))
	}
	if strings.TrimSpace(eb.AppID) != "" && strings.TrimSpace(eb.RedirectURL) == "" {
		errs = append(errs, errors.New("enablebanking.redirect_url is required when an app_id is configured"))
	}
	if eb.BaseURL != "" && !validHTTPURL(eb.BaseURL) {
		errs = append(errs, errors.New("enablebanking.base_url must be an absolute http(s) URL"))
	}
	if eb.RedirectURL != "" && !validHTTPURL(eb.RedirectURL) {
		errs = append(errs, errors.New("enablebanking.redirect_url must be an absolute http(s) URL"))
	}
	switch strings.ToLower(strings.TrimSpace(eb.PSUType)) {
	case "personal", "business":
	default:
		errs = append(errs, fmt.Errorf("enablebanking.psu_type %q must be personal or business", eb.PSUType))
	}
	if eb.ConsentValidityDays <= 0 {
		errs = append(errs, errors.New("enablebanking.consent_validity_days must be positive"))
	}
	if eb.SyncInterval <= 0 {
		errs = append(errs, errors.New("enablebanking.sync_interval must be positive"))
	}
	if eb.SyncTimeout <= 0 {
		errs = append(errs, errors.New("enablebanking.sync_timeout must be positive"))
	}
	if eb.InitialLookbackDays <= 0 {
		errs = append(errs, errors.New("enablebanking.initial_lookback_days must be positive"))
	}
	if eb.OverlapDays < 0 {
		errs = append(errs, errors.New("enablebanking.overlap_days cannot be negative"))
	}
	if c.Alerts.DuplicateWindow <= 0 {
		errs = append(errs, errors.New("alerts.duplicate_window must be positive"))
	}
	if c.Alerts.AnomalyThreshold <= 0 {
		errs = append(errs, errors.New("alerts.anomaly_threshold must be positive"))
	}
	if c.Alerts.AnomalyLookbackDays <= 0 {
		errs = append(errs, errors.New("alerts.anomaly_lookback_days must be positive"))
	}
	if c.Alerts.AnomalyMinHistory <= 0 {
		errs = append(errs, errors.New("alerts.anomaly_min_history must be positive"))
	}
	if c.Alerts.RetryAttempts <= 0 {
		errs = append(errs, errors.New("alerts.retry_attempts must be positive"))
	}
	if c.Alerts.RetryBaseDelay <= 0 {
		errs = append(errs, errors.New("alerts.retry_base_delay must be positive"))
	}
	if _, err := c.LiquidityThresholdsMinor(); err != nil {
		errs = append(errs, fmt.Errorf("alerts.liquidity_thresholds: %w", err))
	}
	if !validLogLevel(c.Logging.Level) {
		errs = append(errs, fmt.Errorf("logging.level %q must be debug, info, warn, or error", c.Logging.Level))
	}
	if c.Logging.Format != "text" && c.Logging.Format != "json" {
		errs = append(errs, fmt.Errorf("logging.format %q must be text or json", c.Logging.Format))
	}
	if strings.TrimSpace(c.Backups.Directory) == "" {
		errs = append(errs, errors.New("backups.directory is required"))
	}
	if c.Backups.Interval <= 0 {
		errs = append(errs, errors.New("backups.interval must be positive"))
	}
	if c.Backups.Telegram && !c.Backups.Enabled {
		errs = append(errs, errors.New("backups.telegram requires backups.enabled"))
	}
	if c.Attachments.Enabled {
		if strings.TrimSpace(c.Attachments.Directory) == "" {
			errs = append(errs, errors.New("attachments.directory is required when attachments are enabled"))
		}
		if c.Attachments.MaxSizeBytes <= 0 || c.Attachments.MaxSizeBytes > 100<<20 {
			errs = append(errs, errors.New("attachments.max_size_bytes must be between 1 and 104857600"))
		}
		if c.Attachments.ReceiptThresholdMinor < 0 {
			errs = append(errs, errors.New("attachments.receipt_threshold_minor cannot be negative"))
		}
		if c.Attachments.GraceDays < 0 || c.Attachments.GraceDays > 365 {
			errs = append(errs, errors.New("attachments.grace_days must be between 0 and 365"))
		}
	}
	if c.Tax.Enabled {
		if c.Tax.VATRateDefault < 0 || c.Tax.VATRateDefault >= 1 {
			errs = append(errs, errors.New("tax.vat_rate_default must be between 0 and 1"))
		}
		if c.Tax.IncomeTaxRetention < 0 || c.Tax.IncomeTaxRetention >= 1 {
			errs = append(errs, errors.New("tax.income_tax_retention must be between 0 and 1"))
		}
		if len(c.Tax.QuarterStartMonths) != 4 {
			errs = append(errs, errors.New("tax.quarter_start_months must contain exactly four months"))
		} else {
			seen := make(map[int]struct{}, 4)
			for _, month := range c.Tax.QuarterStartMonths {
				if month < 1 || month > 12 {
					errs = append(errs, fmt.Errorf("tax.quarter_start_months contains invalid month %d", month))
					continue
				}
				if _, exists := seen[month]; exists {
					errs = append(errs, fmt.Errorf("tax.quarter_start_months contains duplicate month %d", month))
				}
				seen[month] = struct{}{}
			}
		}
	}
	for i, hook := range c.Webhooks {
		if !validHTTPURL(hook.URL) {
			errs = append(errs, fmt.Errorf("webhooks[%d].url must be an absolute http(s) URL", i))
		}
		if len(hook.Events) == 0 {
			errs = append(errs, fmt.Errorf("webhooks[%d].events must not be empty", i))
		}
		for j, event := range hook.Events {
			switch strings.TrimSpace(event) {
			case "transaction.created", "alert.triggered", "sync.completed":
			default:
				errs = append(errs, fmt.Errorf("webhooks[%d].events[%d] %q is not supported", i, j, event))
			}
		}
	}
	for i, inflow := range c.ExpectedInflows {
		if strings.TrimSpace(inflow.Name) == "" {
			errs = append(errs, fmt.Errorf("expected_inflows[%d].name is required", i))
		}
		if inflow.AmountMin <= 0 {
			errs = append(errs, fmt.Errorf("expected_inflows[%d].amount_min must be a positive integer minor-unit amount", i))
		}
		if len(inflow.ExpectedDays) == 0 {
			errs = append(errs, fmt.Errorf("expected_inflows[%d].expected_days must not be empty", i))
		}
		for j, day := range inflow.ExpectedDays {
			if day < 1 || day > 31 {
				errs = append(errs, fmt.Errorf("expected_inflows[%d].expected_days[%d] must be between 1 and 31", i, j))
			}
		}
		if inflow.GraceDays < 0 {
			errs = append(errs, fmt.Errorf("expected_inflows[%d].grace_days cannot be negative", i))
		}
	}
	for i, budget := range c.Budgets {
		if strings.TrimSpace(budget.Category) == "" {
			errs = append(errs, fmt.Errorf("budgets[%d].category is required", i))
		}
		if budget.Limit <= 0 {
			errs = append(errs, fmt.Errorf("budgets[%d].limit must be a positive integer minor-unit amount", i))
		}
		for j, threshold := range budget.NotifyAtPercent {
			if threshold <= 0 || threshold > 100 {
				errs = append(errs, fmt.Errorf("budgets[%d].notify_at_percent[%d] must be between 1 and 100", i, j))
			}
		}
	}
	for i, rule := range c.Rules {
		if strings.TrimSpace(rule.Category) == "" {
			errs = append(errs, fmt.Errorf("rules[%d].category is required", i))
		}
		pattern := strings.TrimSpace(rule.Pattern)
		if pattern == "" {
			errs = append(errs, fmt.Errorf("rules[%d].pattern is required", i))
			continue
		}
		if _, err := regexp.Compile(pattern); err != nil {
			errs = append(errs, fmt.Errorf("rules[%d].pattern: %w", i, err))
		}
	}
	return errors.Join(errs...)
}

// WorkspaceProfile returns the normalized workspace profile.
func (c *Config) WorkspaceProfile() string {
	switch strings.ToLower(strings.TrimSpace(c.Profile)) {
	case "personal":
		return "personal"
	case "business":
		return "business"
	default:
		return "hybrid"
	}
}

// AccountTypes returns configured account ID to workspace type mappings.
func (c *Config) AccountTypes() map[string]string {
	out := make(map[string]string, len(c.Accounts))
	for _, account := range c.Accounts {
		id := strings.TrimSpace(account.ID)
		if id == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(account.Type)) {
		case "business":
			out[id] = "business"
		case "personal":
			out[id] = "personal"
		}
	}
	return out
}

// ProviderName returns the normalized banking provider.
func (c *Config) ProviderName() string {
	provider := strings.ToLower(strings.TrimSpace(c.Provider))
	if provider == "" {
		return "gocardless"
	}
	return provider
}

// BankingConfigured reports whether credentials for the selected provider are
// available.
func (c *Config) BankingConfigured() bool {
	switch c.ProviderName() {
	case "enablebanking":
		return c.EnableBankingConfigured()
	default:
		return strings.TrimSpace(c.GoCardless.SecretID) != "" && strings.TrimSpace(c.GoCardless.SecretKey) != ""
	}
}

// EnableBankingConfigured reports whether Enable Banking credentials are set.
func (c *Config) EnableBankingConfigured() bool {
	return strings.TrimSpace(c.EnableBanking.AppID) != "" &&
		(strings.TrimSpace(c.EnableBanking.PrivateKeyPath) != "" || strings.TrimSpace(c.EnableBanking.PrivateKey) != "")
}

// GoCardlessConfigured reports whether GoCardless credentials are set.
func (c *Config) GoCardlessConfigured() bool {
	return strings.TrimSpace(c.GoCardless.SecretID) != "" && strings.TrimSpace(c.GoCardless.SecretKey) != ""
}

// TelegramConfigured reports whether Telegram delivery is enabled.
func (c *Config) TelegramConfigured() bool {
	return strings.TrimSpace(c.Alerts.Telegram.BotToken) != "" && strings.TrimSpace(c.Alerts.Telegram.ChatID) != ""
}

// DiscordConfigured reports whether Discord delivery is enabled.
func (c *Config) DiscordConfigured() bool {
	return strings.TrimSpace(c.Alerts.Discord.WebhookURL) != ""
}

// AuthConfigured reports whether the dashboard requires credentials.
func (c *Config) AuthConfigured() bool {
	return c.Server.Username != ""
}

func validHTTPURL(raw string) bool {
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return false
	}
	// Keep this dependency-free; config validation only needs scheme + host.
	rest := strings.TrimPrefix(strings.TrimPrefix(raw, "http://"), "https://")
	return rest != "" && !strings.HasPrefix(rest, "/")
}

func validLogLevel(level string) bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}
