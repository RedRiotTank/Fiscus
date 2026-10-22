package gocardless

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recordedRequest
}

type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Auth   string
	Body   string
}

func newTestServer(t *testing.T, handler http.HandlerFunc) *testServer {
	t.Helper()
	ts := &testServer{}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		ts.mu.Lock()
		ts.requests = append(ts.requests, recordedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.RawQuery,
			Auth:   r.Header.Get("Authorization"),
			Body:   string(body),
		})
		ts.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func (ts *testServer) baseURL() string {
	return ts.URL + "/api/v2"
}

func (ts *testServer) lastRequest() recordedRequest {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.requests[len(ts.requests)-1]
}

func newTestClient(t *testing.T, ts *testServer, httpClient *http.Client) *Client {
	t.Helper()
	c, err := New(Config{
		BaseURL:        ts.baseURL(),
		SecretID:       "secret-id",
		SecretKey:      "secret-key",
		HTTPClient:     httpClient,
		RetryAttempts:  3,
		RetryBaseDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func tokenHandler(t *testing.T, access string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/token/new/" {
			t.Errorf("unexpected token request %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, Token{
			Access: access, Refresh: "refresh-1", AccessExpires: 3600, RefreshExpires: 86400,
		})
	}
}

func TestCreateRequisitionAndTokenReuse(t *testing.T) {
	t.Parallel()
	var tokenCalls atomic.Int32
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/token/new/":
			tokenCalls.Add(1)
			tokenHandler(t, "access-1")(w, r)
		case "/api/v2/requisitions/":
			if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
				t.Errorf("Authorization = %q", got)
			}
			var in CreateRequisitionInput
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Errorf("decode body: %v", err)
			}
			if in.InstitutionID != "INST_1" || in.Redirect != "https://app.test/cb" {
				t.Errorf("unexpected input: %+v", in)
			}
			writeJSON(t, w, http.StatusCreated, Requisition{
				ID: "req-1", Status: "CR", Link: "https://bank.test/auth",
				Accounts: []string{"acc-1"},
			})
		case "/api/v2/requisitions/req-1/":
			writeJSON(t, w, http.StatusOK, Requisition{ID: "req-1", Status: "LN"})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	c := newTestClient(t, ts, nil)

	req, err := c.CreateRequisition(context.Background(), CreateRequisitionInput{
		InstitutionID: "INST_1", Redirect: "https://app.test/cb", Reference: "fiscus",
	})
	if err != nil {
		t.Fatalf("CreateRequisition: %v", err)
	}
	if req.ID != "req-1" || req.Status != "CR" || len(req.Accounts) != 1 {
		t.Fatalf("unexpected requisition: %+v", req)
	}

	// A second API call must reuse the cached access token.
	if _, err := c.GetRequisition(context.Background(), "req-1"); err != nil {
		t.Fatalf("GetRequisition: %v", err)
	}
	if got := tokenCalls.Load(); got != 1 {
		t.Fatalf("token calls = %d, want 1", got)
	}
	if got := ts.lastRequest().Path; got != "/api/v2/requisitions/req-1/" {
		t.Fatalf("last path = %q", got)
	}
}

func TestUnauthorizedTriggersRefresh(t *testing.T) {
	t.Parallel()
	var tokenCalls, refreshCalls, apiCalls atomic.Int32
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/token/new/":
			tokenCalls.Add(1)
			writeJSON(t, w, http.StatusOK, Token{
				Access: "stale", Refresh: "refresh-1", AccessExpires: 3600, RefreshExpires: 86400,
			})
		case "/api/v2/token/refresh/":
			refreshCalls.Add(1)
			writeJSON(t, w, http.StatusOK, Token{Access: "fresh", AccessExpires: 3600})
		case "/api/v2/accounts/acc-1/details/":
			apiCalls.Add(1)
			if r.Header.Get("Authorization") == "Bearer stale" {
				http.Error(w, `{"detail":"Authentication credentials were not provided."}`, http.StatusUnauthorized)
				return
			}
			writeJSON(t, w, http.StatusOK, map[string]any{
				"account": AccountDetails{ResourceID: "acc-1", IBAN: "ES00", Currency: "EUR"},
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	c := newTestClient(t, ts, nil)
	details, err := c.GetAccountDetails(context.Background(), "acc-1")
	if err != nil {
		t.Fatalf("GetAccountDetails: %v", err)
	}
	if details.IBAN != "ES00" {
		t.Fatalf("unexpected details: %+v", details)
	}
	if tokenCalls.Load() != 1 || refreshCalls.Load() != 1 || apiCalls.Load() != 2 {
		t.Fatalf("token=%d refresh=%d api=%d, want 1/1/2", tokenCalls.Load(), refreshCalls.Load(), apiCalls.Load())
	}
}

func TestRefreshNearExpiry(t *testing.T) {
	t.Parallel()
	var tokenCalls, refreshCalls atomic.Int32
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/token/new/":
			tokenCalls.Add(1)
			writeJSON(t, w, http.StatusOK, Token{
				Access: "short", Refresh: "refresh-1", AccessExpires: 10, RefreshExpires: 3600,
			})
		case "/api/v2/token/refresh/":
			refreshCalls.Add(1)
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["refresh"] != "refresh-1" {
				t.Errorf("refresh body = %v", body)
			}
			writeJSON(t, w, http.StatusOK, Token{Access: "long", AccessExpires: 3600})
		case "/api/v2/accounts/acc-1/balances/":
			if r.Header.Get("Authorization") != "Bearer short" {
				t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
			}
			writeJSON(t, w, http.StatusOK, Balances{})
		case "/api/v2/institutions/":
			if r.Header.Get("Authorization") != "Bearer long" {
				t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
			}
			if r.URL.Query().Get("country") != "ES" {
				t.Errorf("country = %q", r.URL.Query().Get("country"))
			}
			writeJSON(t, w, http.StatusOK, []Institution{{ID: "INST_1", Name: "Test Bank"}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	c := newTestClient(t, ts, nil)
	if _, err := c.GetBalances(context.Background(), "acc-1"); err != nil {
		t.Fatalf("first GetBalances: %v", err)
	}
	insts, err := c.ListInstitutions(context.Background(), "es")
	if err != nil {
		t.Fatalf("ListInstitutions: %v", err)
	}
	if len(insts) != 1 || insts[0].ID != "INST_1" {
		t.Fatalf("unexpected institutions: %+v", insts)
	}
	if tokenCalls.Load() != 1 || refreshCalls.Load() != 1 {
		t.Fatalf("token=%d refresh=%d", tokenCalls.Load(), refreshCalls.Load())
	}
}

func TestRetriesTransientGETFailures(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/token/new/" {
			tokenHandler(t, "access-1")(w, r)
			return
		}
		if r.URL.Path != "/api/v2/accounts/acc-1/balances/" {
			t.Errorf("unexpected path %s", r.URL.Path)
			return
		}
		if attempts.Add(1) < 3 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		writeJSON(t, w, http.StatusOK, Balances{Balances: []Balance{{
			BalanceAmount: Amount{Value: "123.45", Currency: "EUR"},
			BalanceType:   "interimAvailable",
		}}})
	})
	c := newTestClient(t, ts, nil)
	balances, err := c.GetBalances(context.Background(), "acc-1")
	if err != nil {
		t.Fatalf("GetBalances: %v", err)
	}
	if len(balances) != 1 || balances[0].BalanceAmount.Value != "123.45" {
		t.Fatalf("unexpected balances: %+v", balances)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
}

func TestTransactionsAndDelete(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v2/token/new/":
			tokenHandler(t, "access-1")(w, r)
		case r.URL.Path == "/api/v2/accounts/acc-1/transactions/":
			if r.URL.Query().Get("date_from") != "2026-01-01" || r.URL.Query().Get("date_to") != "2026-01-31" {
				t.Errorf("unexpected query %q", r.URL.RawQuery)
			}
			writeJSON(t, w, http.StatusOK, transactionsEnvelope{
				Transactions: Transactions{
					Booked: []Transaction{{
						TransactionID:     "tx-1",
						BookingDate:       "2026-01-15",
						TransactionAmount: Amount{Value: "-12.34", Currency: "EUR"},
						CreditorName:      "Shop",
					}},
					Pending: []Transaction{{TransactionID: "tx-2"}},
				},
			})
		case r.URL.Path == "/api/v2/requisitions/req-1/" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	c := newTestClient(t, ts, nil)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	txs, err := c.GetTransactions(context.Background(), "acc-1", from, to)
	if err != nil {
		t.Fatalf("GetTransactions: %v", err)
	}
	if len(txs.Booked) != 1 || txs.Booked[0].TransactionID != "tx-1" {
		t.Fatalf("unexpected transactions: %+v", txs)
	}
	if len(txs.Pending) != 1 {
		t.Fatalf("pending = %d", len(txs.Pending))
	}
	if err := c.DeleteRequisition(context.Background(), "req-1"); err != nil {
		t.Fatalf("DeleteRequisition: %v", err)
	}
}

func TestASPSPRateLimitIsNotRetried(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/token/new/" {
			tokenHandler(t, "access-1")(w, r)
			return
		}
		attempts.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":"ASPSP_RATE_LIMIT_EXCEEDED","message":"Maximum daily access exceeded"}`)
	})
	c := newTestClient(t, ts, nil)

	_, err := c.GetBalances(context.Background(), "acc-1")
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.HTTPStatusCode() != http.StatusTooManyRequests {
		t.Fatalf("status = %d", apiErr.HTTPStatusCode())
	}
	if !strings.Contains(apiErr.HTTPErrorBody(), "ASPSP_RATE_LIMIT_EXCEEDED") {
		t.Fatalf("body = %q", apiErr.HTTPErrorBody())
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("provider attempts = %d, want 1 (429 must not be retried)", got)
	}
}

func TestAPIError(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/token/new/" {
			tokenHandler(t, "access-1")(w, r)
			return
		}
		http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
	})
	c := newTestClient(t, ts, nil)
	_, err := c.GetAccountDetails(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", apiErr.StatusCode)
	}
	if strings.Contains(err.Error(), "access-1") {
		t.Fatal("API error leaked access token")
	}
}

func TestNewValidation(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected credentials error")
	}
	if _, err := New(Config{SecretID: "a", SecretKey: "b", BaseURL: "://bad"}); err == nil {
		t.Fatal("expected URL error")
	}
	if _, err := New(Config{SecretID: "a", SecretKey: "b", BaseURL: "ftp://host"}); err == nil {
		t.Fatal("expected scheme error")
	}
	c, err := New(Config{SecretID: "a", SecretKey: "b"})
	if err != nil {
		t.Fatalf("New defaults: %v", err)
	}
	if c.baseURL != DefaultBaseURL {
		t.Fatalf("base URL = %q", c.baseURL)
	}
}
