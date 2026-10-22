// Package webhooks delivers asynchronous JSON events with retries and optional
// HMAC-SHA256 signatures.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	defaultAttempts  = 3
	defaultBaseDelay = 500 * time.Millisecond
	defaultTimeout   = 10 * time.Second
)

// Config describes one outbound webhook subscription.
type Config struct {
	URL    string
	Secret string
	Events []string
}

// Envelope is the JSON body sent to every webhook.
type Envelope struct {
	Event     string    `json:"event"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}

type hook struct {
	url    string
	secret string
	events map[string]struct{}
}

// Dispatcher queues and delivers matching events without blocking callers.
type Dispatcher struct {
	hooks     []hook
	client    *http.Client
	logger    *slog.Logger
	attempts  int
	baseDelay time.Duration
	now       func() time.Time
	wg        sync.WaitGroup
}

// New constructs a dispatcher. Invalid URLs are rejected.
func New(configs []Config, logger *slog.Logger) (*Dispatcher, error) {
	if logger == nil {
		logger = slog.Default()
	}
	hooks := make([]hook, 0, len(configs))
	for i, cfg := range configs {
		raw := strings.TrimSpace(cfg.URL)
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, fmt.Errorf("webhooks: webhook %d must be an absolute http(s) URL", i)
		}
		if len(cfg.Events) == 0 {
			return nil, fmt.Errorf("webhooks: webhook %d must subscribe to at least one event", i)
		}
		events := make(map[string]struct{}, len(cfg.Events))
		for _, event := range cfg.Events {
			event = strings.TrimSpace(event)
			if event == "" {
				return nil, fmt.Errorf("webhooks: webhook %d has an empty event name", i)
			}
			events[event] = struct{}{}
		}
		hooks = append(hooks, hook{url: raw, secret: cfg.Secret, events: events})
	}
	return &Dispatcher{
		hooks:     hooks,
		client:    &http.Client{Timeout: defaultTimeout},
		logger:    logger,
		attempts:  defaultAttempts,
		baseDelay: defaultBaseDelay,
		now:       time.Now,
	}, nil
}

// Emit serializes an event once and schedules one delivery per matching hook.
// It never performs network I/O on the caller's goroutine.
func (d *Dispatcher) Emit(ctx context.Context, event string, data any) {
	if d == nil {
		return
	}
	envelope := Envelope{Event: event, Timestamp: d.now().UTC(), Data: data}
	body, err := json.Marshal(envelope)
	if err != nil {
		d.logger.Error("webhook event marshal failed", "event", event, "error", err)
		return
	}
	deliveryCtx := context.WithoutCancel(ctx)
	for _, candidate := range d.hooks {
		if _, ok := candidate.events[event]; !ok {
			continue
		}
		candidate := candidate
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.deliver(deliveryCtx, candidate, event, envelope.Timestamp, body)
		}()
	}
}

// Wait blocks until all scheduled deliveries have finished. It is intended for
// short-lived CLI commands and clean shutdown paths.
func (d *Dispatcher) Wait() {
	if d == nil {
		return
	}
	d.wg.Wait()
}

func (d *Dispatcher) deliver(ctx context.Context, target hook, event string, timestamp time.Time, body []byte) {
	var lastErr error
	for attempt := 1; attempt <= d.attempts; attempt++ {
		status, retry, err := d.send(ctx, target, event, timestamp, body)
		if err == nil && status >= 200 && status < 300 {
			return
		}
		if err == nil {
			err = fmt.Errorf("HTTP %d", status)
		}
		lastErr = err
		if !retry {
			break
		}
		if attempt == d.attempts {
			break
		}
		delay := d.baseDelay * time.Duration(1<<(attempt-1))
		select {
		case <-ctx.Done():
			d.logger.Warn("webhook delivery cancelled", "event", event, "attempt", attempt)
			return
		case <-time.After(delay):
		}
	}
	d.logger.Error("webhook delivery failed", "event", event, "error", lastErr)
}

func (d *Dispatcher) send(ctx context.Context, target hook, event string, timestamp time.Time, body []byte) (int, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.url, bytes.NewReader(body))
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Fiscus-Webhook/1")
	req.Header.Set("X-Fiscus-Event", event)
	req.Header.Set("X-Fiscus-Timestamp", timestamp.Format(time.RFC3339))
	if target.secret != "" {
		mac := hmac.New(sha256.New, []byte(target.secret))
		_, _ = mac.Write(body)
		req.Header.Set("X-Fiscus-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return 0, true, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, false, nil
	}
	retry := resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout ||
		resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusTooEarly
	return resp.StatusCode, retry, nil
}

// Signature returns the hex HMAC-SHA256 digest used in the X-Fiscus-Signature
// header. It is exported for integration tests and tooling.
func Signature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
