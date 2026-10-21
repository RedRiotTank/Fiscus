package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

const (
	defaultTelegramBaseURL = "https://api.telegram.org"
	telegramMaxRunes       = 4096
)

var telegramTokenPattern = regexp.MustCompile(`^[A-Za-z0-9:_-]+$`)

// TelegramConfig configures a Telegram bot notifier.
type TelegramConfig struct {
	BotToken   string
	ChatID     string
	BaseURL    string
	HTTPClient *http.Client
}

// Telegram sends alerts through the Telegram Bot API.
type Telegram struct {
	token      string
	chatID     string
	endpoint   string
	httpClient *http.Client
}

// NewTelegram validates configuration and constructs a notifier.
func NewTelegram(cfg TelegramConfig) (*Telegram, error) {
	token := strings.TrimSpace(cfg.BotToken)
	if token == "" {
		return nil, errors.New("alerts: telegram bot token is required")
	}
	if !telegramTokenPattern.MatchString(token) {
		return nil, errors.New("alerts: telegram bot token contains invalid characters")
	}
	chatID := strings.TrimSpace(cfg.ChatID)
	if chatID == "" {
		return nil, errors.New("alerts: telegram chat ID is required")
	}
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = defaultTelegramBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("alerts: invalid telegram base URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("alerts: unsupported telegram base URL scheme")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Telegram{
		token:      token,
		chatID:     chatID,
		endpoint:   strings.TrimRight(base, "/") + "/bot" + token + "/sendMessage",
		httpClient: httpClient,
	}, nil
}

// Name implements Notifier.
func (t *Telegram) Name() string { return "telegram" }

type telegramPayload struct {
	ChatID                string `json:"chat_id"`
	Text                  string `json:"text"`
	ParseMode             string `json:"parse_mode"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

// Send implements Notifier.
func (t *Telegram) Send(ctx context.Context, alert domain.Alert) error {
	payload := telegramPayload{
		ChatID:                t.chatID,
		Text:                  truncateRunes(formatTelegram(alert), telegramMaxRunes),
		ParseMode:             "HTML",
		DisableWebPagePreview: true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return &DeliveryError{Channel: "telegram", Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return &DeliveryError{Channel: "telegram", Err: errors.New("could not build request")}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return networkDeliveryError("telegram", err, t.token)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if readErr != nil {
		return &DeliveryError{Channel: "telegram", StatusCode: resp.StatusCode, Err: errors.New("could not read response")}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &DeliveryError{
			Channel:    "telegram",
			StatusCode: resp.StatusCode,
			Body:       truncateRunes(strings.TrimSpace(string(respBody)), 300),
		}
	}
	var decoded telegramResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return &DeliveryError{Channel: "telegram", StatusCode: resp.StatusCode, Err: errors.New("invalid API response")}
	}
	if !decoded.OK {
		return &DeliveryError{
			Channel:    "telegram",
			StatusCode: resp.StatusCode,
			Body:       truncateRunes(strings.TrimSpace(decoded.Description), 300),
		}
	}
	return nil
}

func networkDeliveryError(channel string, err error, secrets ...string) error {
	msg := err.Error()
	for _, secret := range secrets {
		if secret != "" {
			msg = strings.ReplaceAll(msg, secret, "***")
		}
	}
	return &DeliveryError{Channel: channel, Err: errors.New(msg)}
}
