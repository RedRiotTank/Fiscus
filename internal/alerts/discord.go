package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

const discordMaxRunes = 2000

// DiscordConfig configures a Discord webhook notifier.
type DiscordConfig struct {
	WebhookURL string
	HTTPClient *http.Client
}

// Discord sends alerts to a Discord webhook.
type Discord struct {
	webhookURL string
	httpClient *http.Client
}

// NewDiscord validates the webhook URL and constructs a notifier.
func NewDiscord(cfg DiscordConfig) (*Discord, error) {
	raw := strings.TrimSpace(cfg.WebhookURL)
	if raw == "" {
		return nil, errors.New("alerts: discord webhook URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("alerts: invalid discord webhook URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("alerts: unsupported discord webhook URL scheme")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Discord{webhookURL: raw, httpClient: httpClient}, nil
}

// Name implements Notifier.
func (d *Discord) Name() string { return "discord" }

type discordPayload struct {
	Content         string           `json:"content"`
	AllowedMentions *allowedMentions `json:"allowed_mentions,omitempty"`
}

type allowedMentions struct {
	Parse []string `json:"parse"`
}

// Send implements Notifier.
func (d *Discord) Send(ctx context.Context, alert domain.Alert) error {
	payload := discordPayload{
		Content:         truncateRunes(formatDiscord(alert), discordMaxRunes),
		AllowedMentions: &allowedMentions{Parse: []string{}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return &DeliveryError{Channel: "discord", Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.webhookURL, bytes.NewReader(body))
	if err != nil {
		return &DeliveryError{Channel: "discord", Err: errors.New("could not build request")}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return networkDeliveryError("discord", err, d.webhookURL)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if readErr != nil {
		return &DeliveryError{Channel: "discord", StatusCode: resp.StatusCode, Err: errors.New("could not read response")}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &DeliveryError{
			Channel:    "discord",
			StatusCode: resp.StatusCode,
			Body:       truncateRunes(strings.TrimSpace(string(respBody)), 300),
		}
	}
	return nil
}
