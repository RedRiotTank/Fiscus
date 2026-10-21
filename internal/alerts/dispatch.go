package alerts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// Notifier delivers an alert to one external channel.
type Notifier interface {
	Name() string
	Send(ctx context.Context, alert domain.Alert) error
}

// DeliveryError describes a failed notification HTTP call. It deliberately does
// not carry request URLs because those may contain bot tokens or webhook
// secrets.
type DeliveryError struct {
	Channel    string
	StatusCode int
	Body       string
	Err        error
}

func (e *DeliveryError) Error() string {
	switch {
	case e.StatusCode != 0 && e.Body != "":
		return fmt.Sprintf("%s: HTTP %d: %s", e.Channel, e.StatusCode, e.Body)
	case e.StatusCode != 0:
		return fmt.Sprintf("%s: HTTP %d", e.Channel, e.StatusCode)
	case e.Err != nil:
		return fmt.Sprintf("%s: %v", e.Channel, e.Err)
	default:
		return e.Channel + ": delivery failed"
	}
}

// Unwrap exposes the underlying network error.
func (e *DeliveryError) Unwrap() error { return e.Err }

// Retryable reports whether a retry might succeed.
func (e *DeliveryError) Retryable() bool {
	if e.StatusCode == 0 {
		return true // network-level error
	}
	switch e.StatusCode {
	case 408, 425, 429:
		return true
	}
	return e.StatusCode >= 500
}

// DeliveryObserver records final delivery outcomes per channel.
type DeliveryObserver interface {
	ObserveDelivery(channel string, success bool)
}

// DispatchConfig configures a Dispatcher.
type DispatchConfig struct {
	Attempts  int
	BaseDelay time.Duration
	Logger    *slog.Logger
	Observer  DeliveryObserver
}

// Dispatcher fans alerts out to all configured notifiers and retries transient
// failures with exponential backoff. Delivery to at least one channel counts as
// success.
type Dispatcher struct {
	notifiers []Notifier
	attempts  int
	baseDelay time.Duration
	logger    *slog.Logger
	observer  DeliveryObserver
}

// NewDispatcher constructs a Dispatcher. Nil notifiers are ignored.
func NewDispatcher(notifiers []Notifier, cfg DispatchConfig) *Dispatcher {
	filtered := make([]Notifier, 0, len(notifiers))
	for _, n := range notifiers {
		if n != nil {
			filtered = append(filtered, n)
		}
	}
	if cfg.Attempts <= 0 {
		cfg.Attempts = 3
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = 500 * time.Millisecond
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Dispatcher{
		notifiers: filtered,
		attempts:  cfg.Attempts,
		baseDelay: cfg.BaseDelay,
		logger:    cfg.Logger,
		observer:  cfg.Observer,
	}
}

// Dispatch sends the alert to every channel. Channels are delivered
// sequentially to keep ordering predictable and respect provider rate limits.
func (d *Dispatcher) Dispatch(ctx context.Context, alert domain.Alert) error {
	if len(d.notifiers) == 0 {
		d.logger.Debug("alert persisted without notification channels", "alert_id", alert.ID)
		return nil
	}

	delivered := 0
	var errs []error
	for _, notifier := range d.notifiers {
		err := d.deliverWithRetry(ctx, notifier, alert)
		if d.observer != nil {
			d.observer.ObserveDelivery(notifier.Name(), err == nil)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", notifier.Name(), err))
			continue
		}
		delivered++
	}
	if delivered == 0 {
		return fmt.Errorf("alerts: all notification channels failed: %w", errors.Join(errs...))
	}
	if len(errs) > 0 {
		d.logger.Warn("alert partially delivered",
			"alert_id", alert.ID, "delivered", delivered, "total", len(d.notifiers), "errors", errors.Join(errs...))
	}
	return nil
}

func (d *Dispatcher) deliverWithRetry(ctx context.Context, notifier Notifier, alert domain.Alert) error {
	var lastErr error
	for attempt := 1; attempt <= d.attempts; attempt++ {
		lastErr = notifier.Send(ctx, alert)
		if lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !retryable(lastErr) || attempt == d.attempts {
			return lastErr
		}
		delay := d.backoff(attempt)
		d.logger.Debug("retrying alert delivery",
			"channel", notifier.Name(), "attempt", attempt, "delay", delay.String(), "error", lastErr)
		if err := sleep(ctx, delay); err != nil {
			return err
		}
	}
	return lastErr
}

func (d *Dispatcher) backoff(attempt int) time.Duration {
	delay := d.baseDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay > 10*time.Second {
			return 10 * time.Second
		}
	}
	return delay
}

func retryable(err error) bool {
	var re interface{ Retryable() bool }
	if errors.As(err, &re) {
		return re.Retryable()
	}
	return true
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
