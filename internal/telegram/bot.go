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

	"github.com/RedRiotTank/Fiscus/internal/budgets"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
	"github.com/RedRiotTank/Fiscus/internal/storage"
	"github.com/RedRiotTank/Fiscus/internal/treasury"
)

const (
	defaultBaseURL      = "https://api.telegram.org"
	defaultPollTimeout  = 30 * time.Second
	defaultRetryDelay   = 3 * time.Second
	maxMessageRunes     = 4096
	maxRecent           = 50
	maxInlineCategories = 5
)

var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9:_-]+$`)

// Store is the read surface required by bot commands.
type Store interface {
	ListAccounts(ctx context.Context) ([]domain.Account, error)
	RecentBookedTransactions(ctx context.Context, limit int) ([]domain.Transaction, error)
	ListTransactionsBetween(ctx context.Context, from, to time.Time) ([]domain.Transaction, error)
	SpendingByCategory(ctx context.Context, from, to time.Time) ([]storage.CategorySpending, error)
	UpdateTransactionCategory(ctx context.Context, id int64, category string) error
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
	Categories []string
	Budgets    []budgets.Budget
	Now        func() time.Time
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
	categories []string
	budgets    []budgets.Budget
	now        func() time.Time
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
	now := cfg.Now
	if now == nil {
		now = time.Now
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
		categories: normalizeCategories(cfg.Categories),
		budgets:    cfg.Budgets,
		now:        now,
		poll:       defaultPollTimeout,
		retryDelay: defaultRetryDelay,
	}, nil
}

type update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *message       `json:"message"`
	CallbackQuery *callbackQuery `json:"callback_query"`
}

type callbackQuery struct {
	ID      string   `json:"id"`
	From    *user    `json:"from"`
	Message *message `json:"message"`
	Data    string   `json:"data"`
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

type inlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
}

type inlineKeyboardMarkup struct {
	InlineKeyboard [][]inlineKeyboardButton `json:"inline_keyboard"`
}

type telegramMessagePayload struct {
	ChatID      string                `json:"chat_id"`
	Text        string                `json:"text"`
	ReplyMarkup *inlineKeyboardMarkup `json:"reply_markup,omitempty"`
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
	query.Set("allowed_updates", `["message","callback_query"]`)
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
	if item.CallbackQuery != nil {
		b.handleCallback(ctx, item.CallbackQuery)
		return
	}
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

func (b *Bot) handleCallback(ctx context.Context, query *callbackQuery) {
	if query == nil || query.Message == nil {
		return
	}
	if !b.authorized(query.Message.Chat.ID, query.From) {
		b.logger.Warn("telegram callback rejected", "chat_id", query.Message.Chat.ID)
		return
	}
	txID, category, ok := b.callbackCategory(query.Data)
	if !ok {
		b.answerCallback(ctx, query.ID, "Instrucción no válida.")
		return
	}
	if err := b.store.UpdateTransactionCategory(ctx, txID, category); err != nil {
		b.logger.Error("telegram category update failed", "transaction_id", txID, "error", err)
		b.answerCallback(ctx, query.ID, "No se pudo actualizar el movimiento.")
		return
	}
	if category == "Ignored" {
		b.answerCallback(ctx, query.ID, "Movimiento ignorado.")
		return
	}
	b.answerCallback(ctx, query.ID, "Categoría actualizada a "+category+".")
}

func (b *Bot) callbackCategory(data string) (int64, string, bool) {
	parts := strings.Split(data, ":")
	if len(parts) < 3 || parts[0] != "tx" {
		return 0, "", false
	}
	txID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || txID <= 0 {
		return 0, "", false
	}
	if len(parts) == 3 && parts[2] == "ignore" {
		return txID, "Ignored", true
	}
	if len(parts) != 4 || parts[2] != "cat" {
		return 0, "", false
	}
	index, err := strconv.Atoi(parts[3])
	if err != nil || index < 0 || index >= len(b.categories) {
		return 0, "", false
	}
	return txID, b.categories[index], true
}

// NotifyUncategorized sends an interactive inline keyboard for a transaction
// that no YAML rule could classify.
func (b *Bot) NotifyUncategorized(ctx context.Context, tx domain.Transaction) error {
	chatID, err := strconv.ParseInt(b.chatID, 10, 64)
	if err != nil || chatID == 0 {
		return errors.New("telegram: a numeric chat ID is required for notifications")
	}
	name := strings.TrimSpace(tx.Counterparty)
	if name == "" {
		name = strings.TrimSpace(tx.Description)
	}
	if name == "" {
		name = tx.ExternalID
	}
	currency := strings.ToUpper(tx.Currency)
	if currency == "" {
		currency = "EUR"
	}
	text := fmt.Sprintf("Movimiento sin categoría\n%s %s · %s\n%s",
		money.FormatGrouped(tx.AmountMinor, currency), currency,
		truncateRunes(name, 120), tx.BookingDate.Format("02 Jan 2006"))
	if description := strings.TrimSpace(tx.Description); description != "" && description != name {
		text += "\n" + truncateRunes(description, 180)
	}
	rows := make([][]inlineKeyboardButton, 0, (len(b.categories)+1)/2+1)
	row := make([]inlineKeyboardButton, 0, 2)
	for index, category := range b.categories {
		row = append(row, inlineKeyboardButton{
			Text:         truncateRunes(category, 28),
			CallbackData: fmt.Sprintf("tx:%d:cat:%d", tx.ID, index),
		})
		if len(row) == 2 {
			rows = append(rows, row)
			row = make([]inlineKeyboardButton, 0, 2)
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []inlineKeyboardButton{{
		Text: "Ignorar", CallbackData: fmt.Sprintf("tx:%d:ignore", tx.ID),
	}})
	return b.sendMessageWithMarkup(ctx, chatID, text, &inlineKeyboardMarkup{InlineKeyboard: rows})
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
	case "/runway":
		return b.runway(ctx)
	case "/presupuesto":
		return b.budget(ctx)
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

func (b *Bot) runway(ctx context.Context) string {
	accounts, err := b.store.ListAccounts(ctx)
	if err != nil {
		return "No se pudo obtener la autonomía."
	}
	if len(accounts) == 0 {
		return "No hay cuentas enlazadas."
	}
	now := b.now().UTC()
	windowStart := now.Add(-time.Duration(treasury.DefaultBurnWindowDays) * 24 * time.Hour)
	transactions, err := b.store.ListTransactionsBetween(ctx, windowStart, now.Add(24*time.Hour))
	if err != nil {
		return "No se pudo obtener la autonomía."
	}
	runways := treasury.ComputeRunways(accounts, transactions, now, treasury.DefaultBurnWindowDays)
	if len(runways) == 0 {
		return "No hay datos suficientes para obtener la autonomía."
	}
	var out strings.Builder
	out.WriteString("Autonomía financiera:\n")
	for _, runway := range runways {
		label := "sin consumo"
		if runway.RunwayKnown {
			label = fmt.Sprintf("%d.%d meses", runway.RunwayTenths/10, runway.RunwayTenths%10)
		}
		fmt.Fprintf(&out, "- %s: liquidez %s · consumo mensual %s · autonomía %s\n",
			runway.Currency,
			money.FormatGrouped(runway.LiquidMinor, runway.Currency),
			money.FormatGrouped(runway.MonthlyBurnMinor, runway.Currency),
			label,
		)
	}
	return truncate(out.String())
}

func (b *Bot) budget(ctx context.Context) string {
	if len(b.budgets) == 0 {
		return "No hay presupuestos configurados."
	}
	now := b.now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	spending, err := b.store.SpendingByCategory(ctx, monthStart, monthStart.AddDate(0, 1, 0))
	if err != nil {
		return "No se pudo cargar el presupuesto."
	}
	statuses := budgets.Compute(spending, b.budgets, now)
	if len(statuses) == 0 {
		return "No hay presupuestos configurados."
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Presupuesto de %s:\n", monthStart.Format("January 2006"))
	for _, status := range statuses {
		fmt.Fprintf(&out, "- %s: %s de %s %s (%d%%)\n",
			status.Category,
			money.FormatGrouped(status.SpentMinor, status.Currency),
			money.FormatGrouped(status.LimitMinor, status.Currency),
			status.Currency,
			status.Percent,
		)
	}
	fmt.Fprintf(&out, "\nMes transcurrido: %d%%", budgets.MonthElapsedPercent(now))
	return truncate(out.String())
}

func helpText() string {
	return strings.Join([]string{
		"Instrucciones de Fiscus:",
		"/saldo - Saldo consolidado y desglose por cuenta",
		"/sync - Ejecutar una sincronización bancaria ahora",
		"/ultimos [n] - Últimos movimientos confirmados (máximo 50)",
		"/runway - Liquidez, consumo mensual y autonomía",
		"/presupuesto - Consumo del presupuesto del mes actual",
		"/help - Mostrar esta ayuda",
	}, "\n")
}

func (b *Bot) sendMessage(ctx context.Context, chatID int64, text string) error {
	return b.sendMessageWithMarkup(ctx, chatID, text, nil)
}

func (b *Bot) sendMessageWithMarkup(ctx context.Context, chatID int64, text string, markup *inlineKeyboardMarkup) error {
	payload := telegramMessagePayload{
		ChatID:      strconv.FormatInt(chatID, 10),
		Text:        truncateRunes(text, maxMessageRunes),
		ReplyMarkup: markup,
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

func (b *Bot) answerCallback(ctx context.Context, callbackID, text string) {
	if strings.TrimSpace(callbackID) == "" {
		return
	}
	payload := struct {
		CallbackQueryID string `json:"callback_query_id"`
		Text            string `json:"text"`
	}{CallbackQueryID: callbackID, Text: truncateRunes(text, 200)}
	body, err := json.Marshal(payload)
	if err != nil {
		b.logger.Error("telegram answerCallbackQuery payload", "error", err)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiBase+"/answerCallbackQuery", bytes.NewReader(body))
	if err != nil {
		b.logger.Error("telegram answerCallbackQuery request", "error", b.sanitize(err.Error()))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		b.logger.Error("telegram answerCallbackQuery failed", "error", b.sanitize(err.Error()))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		b.logger.Error("telegram answerCallbackQuery read", "error", err)
		return
	}
	var decoded apiResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 || !decoded.OK {
		b.logger.Error("telegram answerCallbackQuery rejected", "status", resp.StatusCode)
	}
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

func normalizeCategories(categories []string) []string {
	seen := make(map[string]struct{}, len(categories))
	out := make([]string, 0, min(len(categories), maxInlineCategories))
	for _, category := range categories {
		category = strings.TrimSpace(category)
		if category == "" || strings.EqualFold(category, "Uncategorized") {
			continue
		}
		key := strings.ToLower(category)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, category)
		if len(out) == maxInlineCategories {
			break
		}
	}
	return out
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
