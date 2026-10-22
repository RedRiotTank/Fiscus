package ratelimit

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

type fakeHTTPError struct {
	status int
	body   string
}

func (e fakeHTTPError) Error() string         { return "provider error" }
func (e fakeHTTPError) HTTPStatusCode() int   { return e.status }
func (e fakeHTTPError) HTTPErrorBody() string { return e.body }

func TestDetect(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		reason string
		ok     bool
	}{
		{
			name:   "aspsp code in body",
			err:    fakeHTTPError{status: http.StatusTooManyRequests, body: `{"error":"ASPSP_RATE_LIMIT_EXCEEDED"}`},
			reason: CodeASPSPDailyLimit,
			ok:     true,
		},
		{
			name:   "plain http 429",
			err:    fakeHTTPError{status: http.StatusTooManyRequests, body: "slow down"},
			reason: codeHTTP429,
			ok:     true,
		},
		{
			name:   "aspsp code without 429",
			err:    fakeHTTPError{status: http.StatusForbidden, body: "ASPSP_RATE_LIMIT_EXCEEDED"},
			reason: CodeASPSPDailyLimit,
			ok:     true,
		},
		{
			name:   "unrelated provider error",
			err:    fakeHTTPError{status: http.StatusBadGateway, body: "upstream unavailable"},
			reason: "",
			ok:     false,
		},
		{
			name:   "wrapped string fallback",
			err:    errors.New("adapter: ASPSP_RATE_LIMIT_EXCEEDED"),
			reason: CodeASPSPDailyLimit,
			ok:     true,
		},
		{
			name:   "already classified",
			err:    &Error{Reason: "custom", Until: time.Now()},
			reason: "custom",
			ok:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reason, ok := Detect(tt.err)
			if ok != tt.ok || reason != tt.reason {
				t.Fatalf("Detect() = %q, %v; want %q, %v", reason, ok, tt.reason, tt.ok)
			}
		})
	}
	if _, ok := Detect(nil); ok {
		t.Fatal("nil error must not be a rate limit")
	}
}

func TestCooldownUntil(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "early morning caps at six hours",
			now:  time.Date(2026, 3, 10, 1, 0, 0, 0, time.UTC),
			want: time.Date(2026, 3, 10, 7, 0, 0, 0, time.UTC),
		},
		{
			name: "evening ends at midnight",
			now:  time.Date(2026, 3, 10, 21, 0, 0, 0, time.UTC),
			want: time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "near midnight still ends at midnight",
			now:  time.Date(2026, 3, 10, 23, 30, 0, 0, time.UTC),
			want: time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "non-UTC input is normalised",
			now:  time.Date(2026, 3, 10, 23, 30, 0, 0, time.FixedZone("CET", 3600)),
			want: time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := CooldownUntil(tt.now); !got.Equal(tt.want) {
				t.Fatalf("CooldownUntil(%v) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}
}

func TestNewErrorPreservesCauseAndReason(t *testing.T) {
	t.Parallel()
	cause := fakeHTTPError{status: http.StatusTooManyRequests, body: "ASPSP_RATE_LIMIT_EXCEEDED"}
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	err := NewError(cause, now)
	if err.Reason != CodeASPSPDailyLimit || !err.Until.Equal(now.Add(6*time.Hour)) {
		t.Fatalf("error = %+v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatal("wrapped cause is not reachable")
	}
}
