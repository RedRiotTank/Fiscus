package alerts

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

type fakeNotifier struct {
	name     string
	failures int
	calls    atomic.Int32
	err      error
}

func (f *fakeNotifier) Name() string { return f.name }

func (f *fakeNotifier) Send(context.Context, domain.Alert) error {
	n := int(f.calls.Add(1))
	if f.err != nil {
		return f.err
	}
	if n <= f.failures {
		return errors.New("temporary failure")
	}
	return nil
}

type fakeDeliveryObserver struct {
	mu       sync.Mutex
	channels []string
	success  []bool
}

func (f *fakeDeliveryObserver) ObserveDelivery(channel string, success bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.channels = append(f.channels, channel)
	f.success = append(f.success, success)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestDispatcherRetriesUntilSuccess(t *testing.T) {
	t.Parallel()
	notifier := &fakeNotifier{name: "telegram", failures: 2}
	observer := &fakeDeliveryObserver{}
	d := NewDispatcher([]Notifier{notifier}, DispatchConfig{
		Attempts: 3, BaseDelay: time.Millisecond, Logger: discardLogger(), Observer: observer,
	})
	if err := d.Dispatch(context.Background(), domain.Alert{ID: 1}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if got := notifier.calls.Load(); got != 3 {
		t.Fatalf("calls = %d, want 3", got)
	}
	if len(observer.channels) != 1 || observer.channels[0] != "telegram" || !observer.success[0] {
		t.Fatalf("observer = %+v", observer)
	}
}

func TestDispatcherAllChannelsFail(t *testing.T) {
	t.Parallel()
	tg := &fakeNotifier{name: "telegram", err: errors.New("down")}
	dc := &fakeNotifier{name: "discord", err: errors.New("down")}
	observer := &fakeDeliveryObserver{}
	d := NewDispatcher([]Notifier{tg, dc}, DispatchConfig{
		Attempts: 2, BaseDelay: time.Millisecond, Logger: discardLogger(), Observer: observer,
	})
	if err := d.Dispatch(context.Background(), domain.Alert{ID: 4}); err == nil {
		t.Fatal("expected error when all channels fail")
	}
	if tg.calls.Load() != 2 || dc.calls.Load() != 2 {
		t.Fatalf("calls telegram=%d discord=%d", tg.calls.Load(), dc.calls.Load())
	}
	if len(observer.success) != 2 || observer.success[0] || observer.success[1] {
		t.Fatalf("observer = %+v", observer)
	}
}

func TestDispatcherPartialSuccess(t *testing.T) {
	t.Parallel()
	tg := &fakeNotifier{name: "telegram", err: &DeliveryError{Channel: "telegram", StatusCode: 400}}
	dc := &fakeNotifier{name: "discord"}
	d := NewDispatcher([]Notifier{tg, dc}, DispatchConfig{
		Attempts: 2, BaseDelay: time.Millisecond, Logger: discardLogger(),
	})
	if err := d.Dispatch(context.Background(), domain.Alert{ID: 5}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if tg.calls.Load() != 1 {
		t.Fatalf("non-retryable failure attempted %d times", tg.calls.Load())
	}
	if dc.calls.Load() != 1 {
		t.Fatalf("discord calls = %d", dc.calls.Load())
	}
}

func TestDispatcherRetryableClassification(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want int32
	}{
		{err: &DeliveryError{StatusCode: 500}, want: 3},
		{err: &DeliveryError{StatusCode: 429}, want: 3},
		{err: &DeliveryError{StatusCode: 403}, want: 1},
		{err: errors.New("network"), want: 3},
	}
	for _, tc := range cases {
		notifier := &fakeNotifier{name: "test", err: tc.err}
		d := NewDispatcher([]Notifier{notifier}, DispatchConfig{
			Attempts: 3, BaseDelay: time.Millisecond, Logger: discardLogger(),
		})
		_ = d.Dispatch(context.Background(), domain.Alert{})
		if got := notifier.calls.Load(); got != tc.want {
			t.Errorf("error %v: calls = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestDispatcherWithoutNotifiers(t *testing.T) {
	t.Parallel()
	d := NewDispatcher(nil, DispatchConfig{})
	if err := d.Dispatch(context.Background(), domain.Alert{ID: 1}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
}

func TestDispatcherContextCancellation(t *testing.T) {
	t.Parallel()
	notifier := &fakeNotifier{name: "test", err: errors.New("down")}
	d := NewDispatcher([]Notifier{notifier}, DispatchConfig{
		Attempts: 5, BaseDelay: time.Hour, Logger: discardLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := d.Dispatch(ctx, domain.Alert{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
