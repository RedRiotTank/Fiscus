package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTelegramSend(t *testing.T) {
	t.Parallel()
	var got telegramPayload
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/bot123:ABC/sendMessage" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content type = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	tg, err := NewTelegram(TelegramConfig{
		BotToken: "123:ABC", ChatID: "-100123", BaseURL: ts.URL, HTTPClient: ts.Client(),
	})
	if err != nil {
		t.Fatalf("NewTelegram: %v", err)
	}
	alert := domain.Alert{
		Rule: RuleDuplicate, Severity: domain.SeverityWarning, Title: "Duplicate <charge>",
		Message: "Shop & Co charged twice", AccountID: "acc-1",
	}
	if err := tg.Send(context.Background(), alert); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.ChatID != "-100123" || got.ParseMode != "HTML" || !got.DisableWebPagePreview {
		t.Fatalf("payload = %+v", got)
	}
	if !strings.Contains(got.Text, "&lt;charge&gt;") || strings.Contains(got.Text, "<charge>") {
		t.Fatalf("HTML was not escaped: %q", got.Text)
	}
	if !strings.Contains(got.Text, "Shop &amp; Co") {
		t.Fatalf("message was not escaped: %q", got.Text)
	}
	if !strings.Contains(got.Text, "WARNING") {
		t.Fatalf("severity missing: %q", got.Text)
	}
}

func TestTelegramErrors(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"ok":false,"description":"chat not found"}`, http.StatusBadRequest)
	}))
	defer ts.Close()
	tg, err := NewTelegram(TelegramConfig{
		BotToken: "123:ABC", ChatID: "1", BaseURL: ts.URL, HTTPClient: ts.Client(),
	})
	if err != nil {
		t.Fatalf("NewTelegram: %v", err)
	}
	err = tg.Send(context.Background(), domain.Alert{Title: "t"})
	var deliveryErr *DeliveryError
	if !errors.As(err, &deliveryErr) || deliveryErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("error = %v", err)
	}
	if deliveryErr.Retryable() {
		t.Fatal("400 should not be retryable")
	}

	// A successful HTTP response with ok=false is still a failure.
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"description":"bot blocked"}`))
	}))
	defer ts2.Close()
	tg2, _ := NewTelegram(TelegramConfig{BotToken: "123:ABC", ChatID: "1", BaseURL: ts2.URL, HTTPClient: ts2.Client()})
	if err := tg2.Send(context.Background(), domain.Alert{}); err == nil {
		t.Fatal("expected ok=false to fail")
	}
}

func TestTelegramNetworkErrorDoesNotLeakToken(t *testing.T) {
	t.Parallel()
	token := "123456:super-secret-token"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("Post %q: connection refused", r.URL.String())
	})}
	tg, err := NewTelegram(TelegramConfig{BotToken: token, ChatID: "1", HTTPClient: client})
	if err != nil {
		t.Fatalf("NewTelegram: %v", err)
	}
	err = tg.Send(context.Background(), domain.Alert{})
	if err == nil {
		t.Fatal("expected network error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error leaked token: %v", err)
	}
}

func TestTelegramValidation(t *testing.T) {
	t.Parallel()
	tests := []TelegramConfig{
		{},
		{BotToken: "123:ABC"},
		{BotToken: "bad/token", ChatID: "1"},
		{BotToken: "123:ABC", ChatID: "1", BaseURL: "://bad"},
		{BotToken: "123:ABC", ChatID: "1", BaseURL: "ftp://host"},
	}
	for _, cfg := range tests {
		if _, err := NewTelegram(cfg); err == nil {
			t.Errorf("expected validation error for %+v", cfg)
		}
	}
}

func TestDiscordSend(t *testing.T) {
	t.Parallel()
	var got discordPayload
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	d, err := NewDiscord(DiscordConfig{WebhookURL: ts.URL + "/api/webhooks/1/token", HTTPClient: ts.Client()})
	if err != nil {
		t.Fatalf("NewDiscord: %v", err)
	}
	alert := domain.Alert{
		Rule: RuleLiquidity, Severity: domain.SeverityCritical, Title: "Low balance",
		Message: "@everyone your balance is low", AccountID: "acc-1",
	}
	if err := d.Send(context.Background(), alert); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.Contains(got.Content, "CRITICAL") || !strings.Contains(got.Content, "@everyone") {
		t.Fatalf("content = %q", got.Content)
	}
	if got.AllowedMentions == nil || len(got.AllowedMentions.Parse) != 0 {
		t.Fatalf("mentions were not suppressed: %+v", got.AllowedMentions)
	}
}

func TestDiscordTruncatesLongMessages(t *testing.T) {
	t.Parallel()
	var got discordPayload
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()
	d, err := NewDiscord(DiscordConfig{WebhookURL: ts.URL, HTTPClient: ts.Client()})
	if err != nil {
		t.Fatalf("NewDiscord: %v", err)
	}
	if err := d.Send(context.Background(), domain.Alert{Title: "Long", Message: strings.Repeat("x", 5000)}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := len([]rune(got.Content)); got > discordMaxRunes {
		t.Fatalf("content length = %d, want <= %d", got, discordMaxRunes)
	}
}

func TestDiscordErrorsAndValidation(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer ts.Close()
	d, err := NewDiscord(DiscordConfig{WebhookURL: ts.URL, HTTPClient: ts.Client()})
	if err != nil {
		t.Fatalf("NewDiscord: %v", err)
	}
	err = d.Send(context.Background(), domain.Alert{})
	var deliveryErr *DeliveryError
	if !errors.As(err, &deliveryErr) || deliveryErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("error = %v", err)
	}
	if !deliveryErr.Retryable() {
		t.Fatal("429 should be retryable")
	}

	// Webhook secrets must not leak through network errors.
	secretURL := "https://discord.com/api/webhooks/123/secret-token"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("Post %q: dial error", r.URL.String())
	})}
	d, err = NewDiscord(DiscordConfig{WebhookURL: secretURL, HTTPClient: client})
	if err != nil {
		t.Fatalf("NewDiscord: %v", err)
	}
	err = d.Send(context.Background(), domain.Alert{})
	if err == nil || strings.Contains(err.Error(), secretURL) {
		t.Fatalf("error leaked webhook URL: %v", err)
	}

	for _, cfg := range []DiscordConfig{{}, {WebhookURL: "://bad"}, {WebhookURL: "ftp://host"}} {
		if _, err := NewDiscord(cfg); err == nil {
			t.Errorf("expected validation error for %+v", cfg)
		}
	}
}
