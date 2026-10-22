// Package telegram implements the interactive Fiscus bot on top of the
// Telegram Bot API. It uses long polling and only accepts commands from the
// configured chat (and optional user).
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

const (
	defaultBaseURL     = "https://api.telegram.org"
	defaultPollTimeout = 30 * time.Second
	defaultRetryDelay  = 3 * time.Second
	maxMessageRunes    = 4096
	maxRecent          = 50
)

var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9:_-]+$`)

// Store is the read surface required by bot commands.
type Store interface {
	ListAccounts(ctx context.Context) ([]domain.Account, error)
	RecentBookedTransactions(ctx context.Context, limit int) ([]domain.Transaction, error)
}

// Syncer performs an immediate bank synchronization.
type Syncer interface {
	SyncAll(ctx context.Context) error
}

// Config configures the interactive bot.
type Config struct {
	BotToken   string
	ChatID     string
	UserID     string
	BaseURL    string
	HTTPClient *http.Client
	Logger     *slog.Logger
	Store      Store
	Syncer     Syncer
}

// Bot polls Telegram for commands and replies to authorized users.
type Bot struct {
	token      string
	chatID     string
	userID     string
	apiBase    string
	httpClient *http.Client
	logger     *slog.Logger
	store      Store
	syncer     Syncer
	poll       time.Duration
	retryDelay time.Duration
	offset     int64
}

// New validates configuration and constructs a Bot.
func New(cfg Config) (*Bot, error) {
	token := strings.TrimSpace(cfg.BotToken)
	if token == "" {
		return nil, errors.New("telegram: bot token is required")
	}
	if !tokenPattern.MatchString(token) {
		return nil, errors.New("telegram: bot token contains invalid characters")
	}
	chatID := strings.TrimSpace(cfg.ChatID)
	userID := strings.TrimSpace(cfg.UserID)
	if chatID == "" && userID == "" {
		return nil, errors.New("telegram: chat ID or user ID is required")
	}
	if chatID != "" {
		if _, err := strconv.ParseInt(chatID, 10, 64); err != nil {
			return nil, errors.New("telegram: chat ID must be numeric")
		}
	}
	if userID != "" {
		if _, err := strconv.ParseInt(userID, 10, 64); err != nil {
			return nil, errors.New("telegram: user ID must be numeric")
		}
	}
	if cfg.Store == nil {
		return nil, errors.New("telegram: store is required")
	}
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = defaultBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("telegram: invalid API base URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("telegram: unsupported API base URL scheme")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultPollTimeout + 10*time.Second}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Bot{
		token:      token,
		chatID:     chatID,
		userID:     userID,
		apiBase:    strings.TrimRight(base, "/") + "/bot" + token,
		httpClient: httpClient,
		logger:     logger,
		store:      cfg.Store,
		syncer:     cfg.Syncer,
		poll:       defaultPollTimeout,
		retryDelay: defaultRetryDelay,
	}, nil
}

type update struct {
	UpdateID int64    `json:"update_id"`
	Message  *message `json:"message"`
}

type message struct {
	MessageID int64  `json:"message_id"`
	Text      string `json:"text"`
	Chat      chat   `json:"chat"`
	From      *user  `json:"from"`
}

type chat struct {
	ID int64 `json:"id"`
}

type user struct {
	ID int64 `json:"id"`
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
}

// Run polls for updates until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) {
	b.logger.Info("telegram bot polling started")
	for {
		if ctx.Err() != nil {
			b.logger.Info("telegram bot stopped")
			return
		}
		updates, err := b.getUpdates(ctx)
		if err != nil {
			if ctx.Err() != nil {
				b.logger.Info("telegram bot stopped")
				return
			}
			b.logger.Warn("telegram getUpdates failed", "error", b.sanitize(err.Error()))
			if !wait(ctx, b.retryDelay) {
				b.logger.Info("telegram bot stopped")
				return
			}
			continue
		}
		for _, item := range updates {
			if item.UpdateID >= b.offset {
				b.offset = item.UpdateID + 1
			}
			b.handleUpdate(ctx, item)
		}
	}
}

func (b *Bot) getUpdates(ctx context.Context) ([]update, error) {
	query := url.Values{}
	query.Set("timeout", strconv.Itoa(int(b.poll.Seconds())))
	query.Set("allowed_updates", `["message"]`)
	if b.offset > 0 {
		query.Set("offset", strconv.FormatInt(b.offset, 10))
	}
	endpoint := b.apiBase + "/getUpdates?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("telegram: build getUpdates request")
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, errors.New("telegram: read getUpdates response")
	}
	var decoded apiResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, errors.New("telegram: invalid getUpdates response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !decoded.OK {
		description := decoded.Description
		if description == "" {
			description = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("telegram: getUpdates failed: %s", description)
	}
	var updates []update
	if err := json.Unmarshal(decoded.Result, &updates); err != nil {
		return nil, errors.New("telegram: invalid updates payload")
	}
	return updates, nil
}

func (b *Bot) handleUpdate(ctx context.Context, item update) {
	msg := item.Message
	if msg == nil {
		return
	}
	if !b.authorized(msg.Chat.ID, msg.From) {
		b.logger.Warn("telegram command rejected", "chat_id", msg.Chat.ID)
		return
	}
	text := strings.TrimSpace(msg.Text)
	if !strings.HasPrefix(text, "/") {
		return
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return
	}
	command := strings.ToLower(strings.SplitN(fields[0], "@", 2)[0])
	reply := b.command(ctx, command, fields[1:])
	if reply == "" {
		return
	}
	if err := b.sendMessage(ctx, msg.Chat.ID, reply); err != nil {
		b.logger.Error("telegram reply failed", "error", b.sanitize(err.Error()))
	}
}

func (b *Bot) authorized(chatID int64, from *user) bool {
	if b.chatID != "" && strconv.FormatInt(chatID, 10) != b.chatID {
		return false
	}
	if b.userID != "" {
		if from == nil || strconv.FormatInt(from.ID, 10) != b.userID {
			return false
		}
	}
	return b.chatID != "" || b.userID != ""
}

func (b *Bot) command(ctx context.Context, command string, args []string) string {
	switch command {
	case "/saldo":
		return b.balance(ctx)
	case "/sync":
		return b.sync(ctx)
	case "/ultimos", "/últimos":
		return b.recent(ctx, args)
	case "/help", "/start":
		return helpText()
	default:
		return "Instrucción desconocida. Usa /help para ver las opciones."
	}
}

func (b *Bot) balance(ctx context.Context) string {
	accounts, err := b.store.ListAccounts(ctx)
	if err != nil {
		return "No se pudo cargar el saldo."
	}
	if len(accounts) == 0 {
		return "No hay cuentas enlazadas."
	}
	totals := make(map[string]int64)
	for _, account := range accounts {
		currency := strings.ToUpper(account.Currency)
		if currency == "" {
			currency = "EUR"
		}
		totals[currency] += account.BalanceMinor
	}
	currencies := make([]string, 0, len(totals))
	for currency := range totals {
		currencies = append(currencies, currency)
	}
	sort.Strings(currencies)

	var out strings.Builder
	out.WriteString("Saldo consolidado:\n")
	for _, currency := range currencies {
		fmt.Fprintf(&out, "%s: %s\n", currency, money.FormatGrouped(totals[currency], currency))
	}
	out.WriteString("\nCuentas:\n")
	for _, account := range accounts {
		name := strings.TrimSpace(account.Name)
		if name == "" {
			name = account.IBAN
		}
		if name == "" {
			name = account.ID
		}
		currency := strings.ToUpper(account.Currency)
		if currency == "" {
			currency = "EUR"
		}
		fmt.Fprintf(&out, "- %s: %s %s\n", name, money.FormatGrouped(account.BalanceMinor, currency), currency)
	}
	return truncate(out.String())
}

func (b *Bot) sync(ctx context.Context) string {
	if b.syncer == nil {
		return "La sincronización bancaria no está configurada."
	}
	if err := b.syncer.SyncAll(ctx); err != nil {
		return "La sincronización falló: " + b.sanitize(err.Error())
	}
	return "Sincronización completada."
}

func (b *Bot) recent(ctx context.Context, args []string) string {
	limit := 10
	if len(args) > 0 {
		parsed, err := strconv.Atoi(args[0])
		if err != nil || parsed < 1 || parsed > maxRecent {
			return fmt.Sprintf("Uso: /ultimos [1-%d]", maxRecent)
		}
		limit = parsed
	}
	transactions, err := b.store.RecentBookedTransactions(ctx, limit)
	if err != nil {
		return "No se pudieron cargar los movimientos."
	}
	if len(transactions) == 0 {
		return "No hay movimientos confirmados."
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Últimos %d movimientos:\n", len(transactions))
	for _, tx := range transactions {
		name := strings.TrimSpace(tx.Counterparty)
		if name == "" {
			name = strings.TrimSpace(tx.Description)
		}
		if name == "" {
			name = tx.AccountID
		}
		category := strings.TrimSpace(tx.Category)
		if category == "" {
			category = "Uncategorized"
		}
		fmt.Fprintf(&out, "%s  %s  %s %s  [%s]\n",
			tx.BookingDate.Format("02 Jan"), truncateRunes(name, 40),
			money.FormatGrouped(tx.AmountMinor, tx.Currency), strings.ToUpper(tx.Currency), category,
		)
	}
	return truncate(out.String())
}

func helpText() string {
	return strings.Join([]string{
		"Instrucciones de Fiscus:",
		"/saldo - Saldo consolidado y desglose por cuenta",
		"/sync - Ejecutar una sincronización bancaria ahora",
		"/ultimos [n] - Últimos movimientos confirmados (máximo 50)",
		"/help - Mostrar esta ayuda",
	}, "\n")
}

func (b *Bot) sendMessage(ctx context.Context, chatID int64, text string) error {
	payload := struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}{
		ChatID: strconv.FormatInt(chatID, 10),
		Text:   truncateRunes(text, maxMessageRunes),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return errors.New("telegram: build sendMessage payload")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiBase+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return errors.New("telegram: build sendMessage request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return errors.New("telegram: read sendMessage response")
	}
	var decoded apiResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return errors.New("telegram: invalid sendMessage response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !decoded.OK {
		description := decoded.Description
		if description == "" {
			description = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("telegram: sendMessage failed: %s", description)
	}
	return nil
}

func (b *Bot) sanitize(value string) string {
	if b.token == "" {
		return value
	}
	return strings.ReplaceAll(value, b.token, "***")
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func truncate(value string) string { return truncateRunes(value, maxMessageRunes) }

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 0 {
		return ""
	}
	return string(runes[:limit])
}
