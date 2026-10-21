package gocardless

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the production GoCardless Bank Account Data API endpoint.
const DefaultBaseURL = "https://bankaccountdata.gocardless.com/api/v2"

// Config configures a Client.
type Config struct {
	BaseURL    string
	SecretID   string
	SecretKey  string
	HTTPClient *http.Client
	UserAgent  string
	Now        func() time.Time
	// MinTokenLifetime refreshes tokens that expire sooner than this.
	MinTokenLifetime time.Duration
	// RetryAttempts applies to idempotent GET requests.
	RetryAttempts  int
	RetryBaseDelay time.Duration
}

// APIError is a non-2xx response from the GoCardless API.
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	Body       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 300 {
		body = body[:300] + "..."
	}
	if body == "" {
		return fmt.Sprintf("gocardless: %s %s returned HTTP %d", e.Method, e.Path, e.StatusCode)
	}
	return fmt.Sprintf("gocardless: %s %s returned HTTP %d: %s", e.Method, e.Path, e.StatusCode, body)
}

// Client is a concurrency-safe, read-oriented GoCardless API client. It manages
// access-token acquisition and refresh automatically.
type Client struct {
	baseURL    string
	secretID   string
	secretKey  string
	httpClient *http.Client
	userAgent  string
	now        func() time.Time

	minTokenLifetime time.Duration
	retryAttempts    int
	retryBaseDelay   time.Duration

	mu            sync.Mutex
	accessToken   string
	accessExpiry  time.Time
	refreshToken  string
	refreshExpiry time.Time
}

// New validates the configuration and returns a Client.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.SecretID) == "" {
		return nil, errors.New("gocardless: secret ID is required")
	}
	if strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, errors.New("gocardless: secret key is required")
	}
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("gocardless: invalid base URL %q", base)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("gocardless: unsupported base URL scheme %q", u.Scheme)
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	userAgent := cfg.UserAgent
	if userAgent == "" {
		userAgent = "Fiscus/1.0 (+https://github.com/RedRiotTank/Fiscus)"
	}
	minLifetime := cfg.MinTokenLifetime
	if minLifetime <= 0 {
		minLifetime = time.Minute
	}
	attempts := cfg.RetryAttempts
	if attempts <= 0 {
		attempts = 3
	}
	delay := cfg.RetryBaseDelay
	if delay <= 0 {
		delay = 250 * time.Millisecond
	}
	return &Client{
		baseURL:          strings.TrimRight(base, "/"),
		secretID:         cfg.SecretID,
		secretKey:        cfg.SecretKey,
		httpClient:       httpClient,
		userAgent:        userAgent,
		now:              now,
		minTokenLifetime: minLifetime,
		retryAttempts:    attempts,
		retryBaseDelay:   delay,
	}, nil
}

// CreateRequisition starts a new bank consent flow.
func (c *Client) CreateRequisition(ctx context.Context, in CreateRequisitionInput) (*Requisition, error) {
	if in.InstitutionID == "" {
		return nil, errors.New("gocardless: institution ID is required")
	}
	if in.Redirect == "" {
		return nil, errors.New("gocardless: redirect URL is required")
	}
	var out Requisition
	if err := c.do(ctx, http.MethodPost, "/requisitions/", nil, in, &out, true, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetRequisition fetches a requisition and its selected accounts.
func (c *Client) GetRequisition(ctx context.Context, requisitionID string) (*Requisition, error) {
	if requisitionID == "" {
		return nil, errors.New("gocardless: requisition ID is required")
	}
	var out Requisition
	path := "/requisitions/" + url.PathEscape(requisitionID) + "/"
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &out, true, true); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteRequisition revokes a consent.
func (c *Client) DeleteRequisition(ctx context.Context, requisitionID string) error {
	if requisitionID == "" {
		return errors.New("gocardless: requisition ID is required")
	}
	path := "/requisitions/" + url.PathEscape(requisitionID) + "/"
	return c.do(ctx, http.MethodDelete, path, nil, nil, nil, true, false)
}

// ListInstitutions lists institutions available in a country.
func (c *Client) ListInstitutions(ctx context.Context, country string) ([]Institution, error) {
	if country == "" {
		return nil, errors.New("gocardless: country code is required")
	}
	q := url.Values{"country": []string{strings.ToUpper(country)}}
	var out []Institution
	if err := c.do(ctx, http.MethodGet, "/institutions/", q, nil, &out, true, true); err != nil {
		return nil, err
	}
	return out, nil
}

// GetAccountDetails fetches account metadata.
func (c *Client) GetAccountDetails(ctx context.Context, accountID string) (*AccountDetails, error) {
	if accountID == "" {
		return nil, errors.New("gocardless: account ID is required")
	}
	var out accountDetailsEnvelope
	path := "/accounts/" + url.PathEscape(accountID) + "/details/"
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &out, true, true); err != nil {
		return nil, err
	}
	return &out.Account, nil
}

// GetBalances fetches current balances for an account.
func (c *Client) GetBalances(ctx context.Context, accountID string) ([]Balance, error) {
	if accountID == "" {
		return nil, errors.New("gocardless: account ID is required")
	}
	var out Balances
	path := "/accounts/" + url.PathEscape(accountID) + "/balances/"
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &out, true, true); err != nil {
		return nil, err
	}
	return out.Balances, nil
}

// GetTransactions fetches booked and pending transactions in [from, to]. The
// "to" date is omitted when zero. GoCardless limits the lookback window.
func (c *Client) GetTransactions(ctx context.Context, accountID string, from, to time.Time) (*Transactions, error) {
	if accountID == "" {
		return nil, errors.New("gocardless: account ID is required")
	}
	if from.IsZero() {
		return nil, errors.New("gocardless: transactions from date is required")
	}
	if !to.IsZero() && to.Before(from) {
		return nil, errors.New("gocardless: transactions to date is before from date")
	}
	q := url.Values{"date_from": []string{from.UTC().Format(DateFormat)}}
	if !to.IsZero() {
		q.Set("date_to", to.UTC().Format(DateFormat))
	}
	var out transactionsEnvelope
	path := "/accounts/" + url.PathEscape(accountID) + "/transactions/"
	if err := c.do(ctx, http.MethodGet, path, q, nil, &out, true, true); err != nil {
		return nil, err
	}
	return &out.Transactions, nil
}

// token returns a valid access token, refreshing or creating one if needed.
func (c *Client) token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if c.accessToken != "" && now.Add(c.minTokenLifetime).Before(c.accessExpiry) {
		return c.accessToken, nil
	}
	if c.refreshToken != "" && now.Add(c.minTokenLifetime).Before(c.refreshExpiry) {
		if err := c.refreshLocked(ctx); err == nil {
			return c.accessToken, nil
		}
		// A rejected refresh should not prevent a fresh token request.
	}
	if err := c.newTokenLocked(ctx); err != nil {
		return "", err
	}
	return c.accessToken, nil
}

// refreshAfterUnauthorized refreshes the token unless another goroutine already
// replaced the stale token.
func (c *Client) refreshAfterUnauthorized(ctx context.Context, stale string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if c.accessToken != "" && c.accessToken != stale &&
		now.Add(c.minTokenLifetime).Before(c.accessExpiry) {
		return nil
	}
	if c.refreshToken != "" && now.Add(c.minTokenLifetime).Before(c.refreshExpiry) {
		if err := c.refreshLocked(ctx); err == nil {
			return nil
		}
	}
	return c.newTokenLocked(ctx)
}

func (c *Client) newTokenLocked(ctx context.Context) error {
	body := map[string]string{"secret_id": c.secretID, "secret_key": c.secretKey}
	var tok Token
	if err := c.do(ctx, http.MethodPost, "/token/new/", nil, body, &tok, false, false); err != nil {
		return fmt.Errorf("gocardless: obtain access token: %w", err)
	}
	if tok.Access == "" {
		return errors.New("gocardless: token response omitted access token")
	}
	c.applyToken(tok)
	return nil
}

func (c *Client) refreshLocked(ctx context.Context) error {
	body := map[string]string{"refresh": c.refreshToken}
	var tok Token
	if err := c.do(ctx, http.MethodPost, "/token/refresh/", nil, body, &tok, false, false); err != nil {
		return fmt.Errorf("gocardless: refresh access token: %w", err)
	}
	if tok.Access == "" {
		return errors.New("gocardless: refresh response omitted access token")
	}
	c.applyToken(tok)
	return nil
}

func (c *Client) applyToken(tok Token) {
	now := c.now()
	c.accessToken = tok.Access
	c.accessExpiry = expiry(now, tok.AccessExpires)
	if tok.Refresh != "" {
		c.refreshToken = tok.Refresh
		c.refreshExpiry = expiry(now, tok.RefreshExpires)
	}
}

func expiry(now time.Time, seconds int64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return now.Add(time.Duration(seconds) * time.Second)
}

// do performs an API request. GET requests are retried on rate limits,
// transient server errors, and transport errors.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any, auth, retry bool) error {
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("gocardless: encode request: %w", err)
		}
	}

	maxAttempts := 1
	if retry && method == http.MethodGet {
		maxAttempts = c.retryAttempts
	}
	refreshed := false
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var usedToken string
		if auth {
			var err error
			usedToken, err = c.token(ctx)
			if err != nil {
				return err
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("gocardless: build request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if auth {
			req.Header.Set("Authorization", "Bearer "+usedToken)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt < maxAttempts && ctx.Err() == nil {
				if err := sleep(ctx, c.backoff(attempt)); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("gocardless: %s %s: %w", method, path, err)
		}
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		closeErr := resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("gocardless: read response: %w", readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("gocardless: close response: %w", closeErr)
		}

		if resp.StatusCode == http.StatusUnauthorized && auth && !refreshed {
			refreshed = true
			if err := c.refreshAfterUnauthorized(ctx, usedToken); err != nil {
				return err
			}
			// Re-issue the request; do not count the token refresh as an attempt.
			attempt--
			continue
		}

		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && attempt < maxAttempts {
			delay := c.backoff(attempt)
			if after := parseRetryAfter(resp.Header.Get("Retry-After")); after > 0 {
				delay = min(after, 10*time.Second)
			}
			if err := sleep(ctx, delay); err != nil {
				return err
			}
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return &APIError{
				StatusCode: resp.StatusCode,
				Method:     method,
				Path:       path,
				Body:       string(respBody),
				RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
			}
		}
		if out != nil {
			if len(bytes.TrimSpace(respBody)) == 0 {
				return fmt.Errorf("gocardless: %s %s returned an empty response", method, path)
			}
			if err := json.Unmarshal(respBody, out); err != nil {
				return fmt.Errorf("gocardless: decode %s %s response: %w", method, path, err)
			}
		}
		return nil
	}
	return fmt.Errorf("gocardless: %s %s exhausted retries", method, path)
}

func (c *Client) backoff(attempt int) time.Duration {
	d := c.retryBaseDelay
	for i := 1; i < attempt; i++ {
		d *= 2
		if d > 5*time.Second {
			return 5 * time.Second
		}
	}
	return d
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

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
