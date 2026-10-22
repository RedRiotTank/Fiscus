// Package ratelimit classifies provider rate-limit errors and computes the
// persistent PSD2 cooldown windows used by the ingestion worker.
package ratelimit

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	// CodeASPSPDailyLimit is returned by European ASPSPs when the PSD2 daily
	// access quota has been exhausted.
	CodeASPSPDailyLimit = "ASPSP_RATE_LIMIT_EXCEEDED"

	codeHTTP429       = "HTTP 429"
	defaultCooldown   = 6 * time.Hour
	rateLimitBodyScan = 64 << 10
)

// HTTPError is implemented by provider API error types. It exposes the HTTP
// status and response body without coupling this package to either provider.
type HTTPError interface {
	error
	HTTPStatusCode() int
	HTTPErrorBody() string
}

// Error describes a provider rate limit and the persistent cooldown that
// applies to the affected connection or account.
type Error struct {
	Reason string
	Until  time.Time
	Cause  error
}

// Error implements error.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	reason := strings.TrimSpace(e.Reason)
	if reason == "" {
		reason = CodeASPSPDailyLimit
	}
	until := e.Until.UTC().Format(time.RFC3339)
	if e.Cause != nil {
		return fmt.Sprintf("bank rate limit reached (%s); cooldown until %s: %v", reason, until, e.Cause)
	}
	return fmt.Sprintf("bank rate limit reached (%s); cooldown until %s", reason, until)
}

// Unwrap returns the provider error that triggered the cooldown.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// NewError wraps a provider error in an Error with a fresh cooldown window.
func NewError(cause error, now time.Time) *Error {
	reason, _ := Detect(cause)
	if reason == "" {
		reason = CodeASPSPDailyLimit
	}
	return &Error{
		Reason: reason,
		Until:  CooldownUntil(now),
		Cause:  cause,
	}
}

// Detect reports whether err represents an HTTP 429 or an ASPSP daily-limit
// response and returns a stable reason suitable for persistence.
func Detect(err error) (string, bool) {
	if err == nil {
		return "", false
	}

	var limited *Error
	if errors.As(err, &limited) {
		reason := strings.TrimSpace(limited.Reason)
		if reason == "" {
			reason = CodeASPSPDailyLimit
		}
		return reason, true
	}

	var httpErr HTTPError
	if errors.As(err, &httpErr) {
		if reason := reasonFor(httpErr.HTTPStatusCode(), httpErr.HTTPErrorBody()); reason != "" {
			return reason, true
		}
	}

	// Some adapters wrap provider failures in plain errors. Keep the fallback
	// deliberately narrow so unrelated failures are never treated as quota
	// exhaustion.
	if hasDailyLimitCode(err.Error()) {
		return CodeASPSPDailyLimit, true
	}
	return "", false
}

// Is reports whether err is a rate-limit error.
func Is(err error) bool {
	_, ok := Detect(err)
	return ok
}

// CooldownUntil returns the earlier of the next UTC midnight and six hours from
// now. This avoids hammering an exhausted ASPSP while ensuring a long rate
// limit block can never exceed one polling window.
func CooldownUntil(now time.Time) time.Time {
	now = now.UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	backoff := now.Add(defaultCooldown)
	if midnight.Before(backoff) {
		return midnight
	}
	return backoff
}

func reasonFor(status int, body string) string {
	if hasDailyLimitCode(body) {
		return CodeASPSPDailyLimit
	}
	if status == http.StatusTooManyRequests {
		return codeHTTP429
	}
	return ""
}

func hasDailyLimitCode(body string) bool {
	if len(body) > rateLimitBodyScan {
		body = body[:rateLimitBodyScan]
	}
	return strings.Contains(strings.ToUpper(body), CodeASPSPDailyLimit)
}
