package enablebanking

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/banking/gocardless"
)

// Config configures a Client.
type Config struct {
	BaseURL        string
	AppID          string
	PrivateKeyPEM  string
	UserAgent      string
	HTTPClient     *http.Client
	Now            func() time.Time
	RetryAttempts  int
	RetryBaseDelay time.Duration
}

// APIError is a non-2xx response from the Enable Banking API.
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
		return fmt.Sprintf("enablebanking: %s %s returned HTTP %d", e.Method, e.Path, e.StatusCode)
	}
	return fmt.Sprintf("enablebanking: %s %s returned HTTP %d: %s", e.Method, e.Path, e.StatusCode, body)
}

// HTTPStatusCode exposes the provider HTTP status to rate-limit classifiers.
func (e *APIError) HTTPStatusCode() int {
	if e == nil {
		return 0
	}
	return e.StatusCode
}

// HTTPErrorBody exposes the provider response body to rate-limit classifiers.
func (e *APIError) HTTPErrorBody() string {
	if e == nil {
		return ""
	}
	return e.Body
}

// Client is a concurrency-safe Enable Banking API client. Every request is
// authenticated with a short-lived RS256 JWT signed by the application key.
type Client struct {
	baseURL    string
	appID      string
	privateKey *rsa.PrivateKey
	httpClient *http.Client
	userAgent  string
	now        func() time.Time

	retryAttempts  int
	retryBaseDelay time.Duration
}

// New validates the configuration and parses the RSA private key.
func New(cfg Config) (*Client, error) {
	appID := strings.TrimSpace(cfg.AppID)
	if appID == "" {
		return nil, errors.New("enablebanking: app ID is required")
	}
	key, err := parsePrivateKey([]byte(cfg.PrivateKeyPEM))
	if err != nil {
		return nil, err
	}
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("enablebanking: invalid base URL %q", base)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("enablebanking: unsupported base URL scheme %q", u.Scheme)
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
	attempts := cfg.RetryAttempts
	if attempts <= 0 {
		attempts = 3
	}
	delay := cfg.RetryBaseDelay
	if delay <= 0 {
		delay = 250 * time.Millisecond
	}
	return &Client{
		baseURL:        strings.TrimRight(base, "/"),
		appID:          appID,
		privateKey:     key,
		httpClient:     httpClient,
		userAgent:      userAgent,
		now:            now,
		retryAttempts:  attempts,
		retryBaseDelay: delay,
	}, nil
}

// NewState returns a cryptographically random OAuth state value.
func NewState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("enablebanking: generate state: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// ListASPSPs lists banks available in a country.
func (c *Client) ListASPSPs(ctx context.Context, country, psuType string) ([]ASPSP, error) {
	q := url.Values{"service": []string{"AIS"}}
	if country != "" {
		q.Set("country", strings.ToUpper(strings.TrimSpace(country)))
	}
	if psuType != "" {
		q.Set("psu_type", strings.ToLower(strings.TrimSpace(psuType)))
	}
	var out struct {
		ASPSPs []ASPSP `json:"aspsps"`
	}
	if err := c.do(ctx, http.MethodGet, "/aspsps", q, nil, &out, true); err != nil {
		return nil, err
	}
	return out.ASPSPs, nil
}

// CreateAuthorization starts a consent flow and returns the URL the user must
// open at the bank.
func (c *Client) CreateAuthorization(ctx context.Context, in AuthorizationInput) (*Authorization, error) {
	name := strings.TrimSpace(in.ASPSPName)
	if name == "" {
		return nil, errors.New("enablebanking: ASPSP name is required")
	}
	country := strings.ToUpper(strings.TrimSpace(in.ASPSPCountry))
	if country == "" {
		return nil, errors.New("enablebanking: ASPSP country is required")
	}
	redirect := strings.TrimSpace(in.RedirectURL)
	if redirect == "" {
		return nil, errors.New("enablebanking: redirect URL is required")
	}
	state := strings.TrimSpace(in.State)
	if state == "" {
		generated, err := NewState()
		if err != nil {
			return nil, err
		}
		state = generated
	}
	psuType := strings.ToLower(strings.TrimSpace(in.PSUType))
	if psuType == "" {
		psuType = "personal"
	}
	validUntil := in.ValidUntil
	if validUntil.IsZero() {
		validUntil = c.now().AddDate(0, 0, 90)
	}
	enabled := true
	body := struct {
		Access      Access `json:"access"`
		ASPSP       ASPSP  `json:"aspsp"`
		State       string `json:"state"`
		RedirectURL string `json:"redirect_url"`
		PSUType     string `json:"psu_type"`
		Language    string `json:"language,omitempty"`
	}{
		Access: Access{
			ValidUntil:   validUntil.UTC().Format(time.RFC3339),
			Balances:     &enabled,
			Transactions: &enabled,
		},
		ASPSP:       ASPSP{Name: name, Country: country},
		State:       state,
		RedirectURL: redirect,
		PSUType:     psuType,
		Language:    strings.TrimSpace(in.Language),
	}
	var out struct {
		URL             string `json:"url"`
		AuthorizationID string `json:"authorization_id"`
	}
	if err := c.do(ctx, http.MethodPost, "/auth", nil, body, &out, false); err != nil {
		return nil, err
	}
	if out.URL == "" || out.AuthorizationID == "" {
		return nil, errors.New("enablebanking: authorization response is incomplete")
	}
	return &Authorization{URL: out.URL, AuthorizationID: out.AuthorizationID, State: state}, nil
}

// CreateSession exchanges the authorization code from the redirect for a
// long-lived session.
func (c *Client) CreateSession(ctx context.Context, code string) (*AuthorizedSession, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, errors.New("enablebanking: authorization code is required")
	}
	var session AuthorizedSession
	if err := c.do(ctx, http.MethodPost, "/sessions", nil, map[string]string{"code": code}, &session, false); err != nil {
		return nil, err
	}
	if session.SessionID == "" {
		return nil, errors.New("enablebanking: session response omitted session ID")
	}
	return &session, nil
}

// GetSession fetches an existing session.
func (c *Client) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("enablebanking: session ID is required")
	}
	var session Session
	path := "/sessions/" + url.PathEscape(sessionID)
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &session, true); err != nil {
		return nil, err
	}
	session.SessionID = sessionID
	return &session, nil
}

// GetRequisition adapts a session to the provider-neutral shape expected by the
// ingestion worker.
func (c *Client) GetRequisition(ctx context.Context, sessionID string) (*gocardless.Requisition, error) {
	session, err := c.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	accounts := session.Accounts
	if len(accounts) == 0 {
		for _, account := range session.AccountsData {
			if account.UID != "" {
				accounts = append(accounts, account.UID)
			}
		}
	}
	status := mapSessionStatus(session.Status)
	expiresAt := parseConsentExpiry(session.Access.ValidUntil)
	if status == "LN" && expiresAt != nil && c.now().After(*expiresAt) {
		status = "EX"
	}
	return &gocardless.Requisition{
		ID:               session.SessionID,
		Status:           status,
		InstitutionID:    session.ASPSP.Name,
		Accounts:         accounts,
		ConsentExpiresAt: expiresAt,
	}, nil
}

func parseConsentExpiry(validUntil string) *time.Time {
	validUntil = strings.TrimSpace(validUntil)
	if validUntil == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, validUntil); err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	return nil
}

// GetAccountDetails fetches account metadata.
func (c *Client) GetAccountDetails(ctx context.Context, accountID string) (*gocardless.AccountDetails, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, errors.New("enablebanking: account ID is required")
	}
	var account AccountResource
	path := "/accounts/" + url.PathEscape(accountID) + "/details"
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &account, true); err != nil {
		return nil, err
	}
	name := firstNonEmpty(account.Details, account.Product, account.CashAccountType)
	return &gocardless.AccountDetails{
		ResourceID: firstNonEmpty(account.UID, accountID),
		IBAN:       accountIBAN(account),
		Currency:   strings.ToUpper(account.Currency),
		OwnerName:  account.Name,
		Name:       name,
		Product:    account.Product,
	}, nil
}

// GetBalances fetches current balances for an account.
func (c *Client) GetBalances(ctx context.Context, accountID string) ([]gocardless.Balance, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, errors.New("enablebanking: account ID is required")
	}
	var out Balances
	path := "/accounts/" + url.PathEscape(accountID) + "/balances"
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &out, true); err != nil {
		return nil, err
	}
	balances := make([]gocardless.Balance, 0, len(out.Balances))
	for _, balance := range out.Balances {
		balances = append(balances, gocardless.Balance{
			BalanceAmount: gocardless.Amount{
				Value:    balance.BalanceAmount.Amount,
				Currency: strings.ToUpper(balance.BalanceAmount.Currency),
			},
			BalanceType:        mapBalanceType(balance.BalanceType),
			ReferenceDate:      balance.ReferenceDate,
			LastChangeDateTime: balance.LastChangeDateTime,
		})
	}
	return balances, nil
}

// GetTransactions fetches all pages of transactions in [from, to].
func (c *Client) GetTransactions(ctx context.Context, accountID string, from, to time.Time) (*gocardless.Transactions, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, errors.New("enablebanking: account ID is required")
	}
	if from.IsZero() {
		return nil, errors.New("enablebanking: transactions from date is required")
	}
	if !to.IsZero() && to.Before(from) {
		return nil, errors.New("enablebanking: transactions to date is before from date")
	}
	q := url.Values{"date_from": []string{from.UTC().Format(DateFormat)}}
	if !to.IsZero() {
		q.Set("date_to", to.UTC().Format(DateFormat))
	}

	var collected []Transaction
	for page := 0; page < 50; page++ {
		var out Transactions
		path := "/accounts/" + url.PathEscape(accountID) + "/transactions"
		if err := c.do(ctx, http.MethodGet, path, q, nil, &out, true); err != nil {
			return nil, err
		}
		collected = append(collected, out.Transactions...)
		if out.ContinuationKey == "" {
			break
		}
		if page == 49 {
			return nil, errors.New("enablebanking: transactions pagination exceeded 50 pages")
		}
		q.Set("continuation_key", out.ContinuationKey)
	}
	return toGoCardlessTransactions(collected), nil
}

func accountIBAN(account AccountResource) string {
	if account.AccountID.IBAN != "" {
		return account.AccountID.IBAN
	}
	if account.AccountID.Other != nil && strings.EqualFold(account.AccountID.Other.SchemeName, "IBAN") {
		return account.AccountID.Other.Identification
	}
	for _, id := range account.AllAccountIDs {
		if strings.EqualFold(id.SchemeName, "IBAN") {
			return id.Identification
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func mapSessionStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "AUTHORIZED":
		return "LN"
	case "EXPIRED", "CLOSED":
		return "EX"
	case "REVOKED", "CANCELLED", "INVALID":
		return "RJ"
	default:
		return "UA"
	}
}

func mapBalanceType(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "ITAV":
		return "interimAvailable"
	case "CLAV":
		return "closingAvailable"
	case "ITBD":
		return "interimBooked"
	case "CLBD":
		return "closingBooked"
	case "OPAV":
		return "openingAvailable"
	case "OPBD":
		return "openingBooked"
	case "FWAV":
		return "forwardAvailable"
	case "XPCD":
		return "expected"
	case "VALU":
		return "valueDate"
	default:
		return strings.ToLower(strings.TrimSpace(code))
	}
}

func toGoCardlessTransactions(items []Transaction) *gocardless.Transactions {
	out := &gocardless.Transactions{}
	for _, item := range items {
		status := mapTransactionStatus(item.Status)
		if status == "" || strings.TrimSpace(item.TransactionAmount.Amount) == "" {
			continue
		}
		transaction := gocardless.Transaction{
			TransactionID:         firstNonEmpty(item.EntryReference, item.TransactionID),
			TransactionAmount:     gocardless.Amount{Value: signedAmount(item), Currency: strings.ToUpper(item.TransactionAmount.Currency)},
			CreditorName:          item.Creditor.Name,
			DebtorName:            item.Debtor.Name,
			BookingDate:           firstNonEmpty(item.BookingDate, item.ValueDate, item.TransactionDate),
			ValueDate:             item.ValueDate,
			AdditionalInformation: firstNonEmpty(item.Note, item.ReferenceNumber),
		}
		transaction.RemittanceInformationUnstructuredArray = item.RemittanceInformation
		if status == "booked" {
			out.Booked = append(out.Booked, transaction)
		} else {
			out.Pending = append(out.Pending, transaction)
		}
	}
	return out
}

func mapTransactionStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "BOOK":
		return "booked"
	case "CNCL", "RJCT":
		return ""
	default:
		return "pending"
	}
}

// signedAmount applies the credit/debit indicator without floating point math.
func signedAmount(tx Transaction) string {
	amount := strings.TrimSpace(tx.TransactionAmount.Amount)
	negative := strings.HasPrefix(amount, "-")
	amount = strings.TrimPrefix(amount, "-")
	switch strings.ToUpper(strings.TrimSpace(tx.CreditDebitIndicator)) {
	case "DBIT":
		return "-" + amount
	case "CRDT":
		if negative {
			return "-" + amount
		}
		return amount
	default:
		if negative {
			return "-" + amount
		}
		return amount
	}
}

func parsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("enablebanking: private key is not valid PEM")
	}
	if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		key, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("enablebanking: private key is not RSA")
		}
		if key.N.BitLen() < 2048 {
			return nil, errors.New("enablebanking: RSA key must be at least 2048 bits")
		}
		return key, nil
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("enablebanking: parse private key: %w", err)
	}
	if key.N.BitLen() < 2048 {
		return nil, errors.New("enablebanking: RSA key must be at least 2048 bits")
	}
	return key, nil
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

type jwtClaims struct {
	Iss string `json:"iss"`
	Aud string `json:"aud"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

func (c *Client) authorization() (string, error) {
	now := c.now().UTC()
	header, err := json.Marshal(jwtHeader{Alg: "RS256", Typ: "JWT", Kid: c.appID})
	if err != nil {
		return "", fmt.Errorf("enablebanking: encode JWT header: %w", err)
	}
	claims, err := json.Marshal(jwtClaims{
		Iss: "enablebanking.com",
		Aud: "api.enablebanking.com",
		Iat: now.Add(-30 * time.Second).Unix(),
		Exp: now.Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("enablebanking: encode JWT claims: %w", err)
	}
	encoding := base64.RawURLEncoding
	signingInput := encoding.EncodeToString(header) + "." + encoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, c.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("enablebanking: sign JWT: %w", err)
	}
	return signingInput + "." + encoding.EncodeToString(signature), nil
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any, retry bool) error {
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("enablebanking: encode request: %w", err)
		}
	}

	maxAttempts := 1
	if retry && method == http.MethodGet {
		maxAttempts = c.retryAttempts
	}
	refreshed := false
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		token, err := c.authorization()
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("enablebanking: build request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)
		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt < maxAttempts && ctx.Err() == nil {
				if err := sleep(ctx, c.backoff(attempt)); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("enablebanking: %s %s: %w", method, path, err)
		}
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		closeErr := resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("enablebanking: read response: %w", readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("enablebanking: close response: %w", closeErr)
		}

		if resp.StatusCode == http.StatusUnauthorized && !refreshed {
			refreshed = true
			attempt--
			continue
		}
		if resp.StatusCode >= 500 && attempt < maxAttempts {
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
				return fmt.Errorf("enablebanking: %s %s returned an empty response", method, path)
			}
			if err := json.Unmarshal(respBody, out); err != nil {
				return fmt.Errorf("enablebanking: decode %s %s response: %w", method, path, err)
			}
		}
		return nil
	}
	return fmt.Errorf("enablebanking: %s %s exhausted retries", method, path)
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
