package enablebanking

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

const testAppID = "test-app-id"

type testClient struct {
	client *Client
	pub    *rsa.PublicKey
	server *httptest.Server
	now    time.Time
}

func newTestClient(t *testing.T, handler http.HandlerFunc) *testClient {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal RSA key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

	var server *httptest.Server
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertRequestJWT(t, r, &key.PublicKey, now)
		handler(w, r)
	})
	server = httptest.NewServer(wrapped)
	t.Cleanup(server.Close)

	client, err := New(Config{
		BaseURL:        server.URL,
		AppID:          testAppID,
		PrivateKeyPEM:  string(keyPEM),
		Now:            func() time.Time { return now },
		RetryAttempts:  1,
		RetryBaseDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &testClient{client: client, pub: &key.PublicKey, server: server, now: now}
}

func assertRequestJWT(t *testing.T, r *http.Request, pub *rsa.PublicKey, now time.Time) {
	t.Helper()
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		t.Fatalf("Authorization = %q, want Bearer token", header)
	}
	parts := strings.Split(strings.TrimPrefix(header, "Bearer "), ".")
	if len(parts) != 3 {
		t.Fatalf("JWT has %d parts, want 3", len(parts))
	}
	encoding := base64.RawURLEncoding
	headerJSON, err := encoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode JWT header: %v", err)
	}
	var jwtHeader struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &jwtHeader); err != nil {
		t.Fatalf("parse JWT header: %v", err)
	}
	if jwtHeader.Alg != "RS256" || jwtHeader.Typ != "JWT" || jwtHeader.Kid != testAppID {
		t.Fatalf("JWT header = %+v", jwtHeader)
	}
	claimsJSON, err := encoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode JWT claims: %v", err)
	}
	var claims struct {
		Iss string `json:"iss"`
		Aud string `json:"aud"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		t.Fatalf("parse JWT claims: %v", err)
	}
	if claims.Iss != "enablebanking.com" || claims.Aud != "api.enablebanking.com" {
		t.Fatalf("JWT claims = %+v", claims)
	}
	if claims.Iat > now.Unix() || claims.Exp <= now.Unix() {
		t.Fatalf("JWT validity = %d..%d, now %d", claims.Iat, claims.Exp, now.Unix())
	}
	signature, err := encoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode JWT signature: %v", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], signature); err != nil {
		t.Fatalf("JWT signature: %v", err)
	}
}

func TestListASPSPs(t *testing.T) {
	t.Parallel()
	tc := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/aspsps" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("country") != "ES" || query.Get("psu_type") != "personal" || query.Get("service") != "AIS" {
			t.Errorf("query = %v", query)
		}
		_, _ = io.WriteString(w, `{"aspsps":[{"name":"Banco Santander","country":"ES","bic":"BSCHESMM","psu_types":["personal","business"],"maximum_consent_validity":7776000}]}`)
	})

	aspsps, err := tc.client.ListASPSPs(context.Background(), "es", "personal")
	if err != nil {
		t.Fatalf("ListASPSPs: %v", err)
	}
	if len(aspsps) != 1 {
		t.Fatalf("ASPSPs = %+v", aspsps)
	}
	if aspsps[0].Name != "Banco Santander" || aspsps[0].MaximumConsentValidity != 7776000 || aspsps[0].BIC != "BSCHESMM" {
		t.Fatalf("ASPSP = %+v", aspsps[0])
	}
}

func TestCreateAuthorization(t *testing.T) {
	t.Parallel()
	var requestBody map[string]any
	tc := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/auth" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_, _ = io.WriteString(w, `{"url":"https://auth.enablebanking.test/start","authorization_id":"auth-1"}`)
	})

	validUntil := tc.now.AddDate(0, 0, 30)
	authorization, err := tc.client.CreateAuthorization(context.Background(), AuthorizationInput{
		ASPSPName:    "Banco Santander",
		ASPSPCountry: "es",
		RedirectURL:  "http://localhost:8080/callback",
		PSUType:      "personal",
		ValidUntil:   validUntil,
	})
	if err != nil {
		t.Fatalf("CreateAuthorization: %v", err)
	}
	if authorization.URL != "https://auth.enablebanking.test/start" || authorization.AuthorizationID != "auth-1" {
		t.Fatalf("authorization = %+v", authorization)
	}
	if len(authorization.State) != 64 {
		t.Fatalf("state = %q, want 64 hex characters", authorization.State)
	}
	if requestBody["state"] != authorization.State {
		t.Fatalf("request state = %v, authorization state = %q", requestBody["state"], authorization.State)
	}
	if requestBody["redirect_url"] != "http://localhost:8080/callback" || requestBody["psu_type"] != "personal" {
		t.Fatalf("request body = %+v", requestBody)
	}
	aspsp, _ := requestBody["aspsp"].(map[string]any)
	if aspsp["name"] != "Banco Santander" || aspsp["country"] != "ES" {
		t.Fatalf("ASPSP = %+v", aspsp)
	}
	access, _ := requestBody["access"].(map[string]any)
	if access["balances"] != true || access["transactions"] != true {
		t.Fatalf("access = %+v", access)
	}
	if access["valid_until"] != validUntil.Format(time.RFC3339) {
		t.Fatalf("valid_until = %v", access["valid_until"])
	}
}

func TestCreateSessionAndGetRequisition(t *testing.T) {
	t.Parallel()
	var createBody map[string]string
	tc := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sessions":
			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				t.Errorf("decode body: %v", err)
			}
			_, _ = io.WriteString(w, `{
				"session_id":"session-1",
				"accounts":[
					{"uid":"acc-1","account_id":{"iban":"ES123"},"name":"Holder","details":"Cuenta corriente","currency":"EUR"}
				],
				"aspsp":{"name":"Banco Santander","country":"ES"},
				"psu_type":"personal",
				"access":{"valid_until":"2027-01-01T00:00:00Z"}
			}`)
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/session-1":
			_, _ = io.WriteString(w, `{
				"status":"AUTHORIZED",
				"accounts":["acc-1"],
				"accounts_data":[{"uid":"acc-1","identification_hash":"hash"}],
				"aspsp":{"name":"Banco Santander","country":"ES"},
				"psu_type":"personal",
				"access":{"valid_until":"2027-01-01T00:00:00Z"}
			}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	session, err := tc.client.CreateSession(context.Background(), "code-1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if createBody["code"] != "code-1" {
		t.Fatalf("create body = %+v", createBody)
	}
	if session.SessionID != "session-1" || len(session.Accounts) != 1 || session.Accounts[0].UID != "acc-1" {
		t.Fatalf("session = %+v", session)
	}
	if session.ASPSP.Name != "Banco Santander" {
		t.Fatalf("ASPSP = %+v", session.ASPSP)
	}

	requisition, err := tc.client.GetRequisition(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("GetRequisition: %v", err)
	}
	if requisition.ID != "session-1" || requisition.Status != "LN" {
		t.Fatalf("requisition = %+v", requisition)
	}
	if len(requisition.Accounts) != 1 || requisition.Accounts[0] != "acc-1" {
		t.Fatalf("accounts = %+v", requisition.Accounts)
	}
	if requisition.InstitutionID != "Banco Santander" {
		t.Fatalf("institution = %q", requisition.InstitutionID)
	}
}

func TestAccountDetailsBalancesAndTransactions(t *testing.T) {
	t.Parallel()
	var pageRequests int
	tc := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/accounts/acc-1/details":
			_, _ = io.WriteString(w, `{
				"uid":"acc-1",
				"account_id":{"iban":"ES123"},
				"name":"Holder Name",
				"details":"Cuenta corriente",
				"product":"Cuenta Santander",
				"currency":"eur",
				"cash_account_type":"CACC"
			}`)
		case "/accounts/acc-1/balances":
			_, _ = io.WriteString(w, `{"balances":[
				{"name":"Interim available","balance_amount":{"amount":"123.45","currency":"EUR"},"balance_type":"ITAV","reference_date":"2026-10-20"},
				{"name":"Closing booked","balance_amount":{"amount":"120.00","currency":"EUR"},"balance_type":"CLBD"}
			]}`)
		case "/accounts/acc-1/transactions":
			if r.URL.Query().Get("date_from") != "2026-10-01" || r.URL.Query().Get("date_to") != "2026-10-21" {
				t.Errorf("date query = %v", r.URL.Query())
			}
			pageRequests++
			if pageRequests == 1 {
				if r.URL.Query().Get("continuation_key") != "" {
					t.Errorf("first page continuation = %q", r.URL.Query().Get("continuation_key"))
				}
				_, _ = io.WriteString(w, `{"transactions":[
					{"entry_reference":"tx-1","transaction_amount":{"amount":"10.00","currency":"EUR"},"credit_debit_indicator":"CRDT","status":"BOOK","booking_date":"2026-10-20","value_date":"2026-10-20","creditor":{"name":"Employer"},"debtor":{"name":"Holder"},"remittance_information":["Salary"]},
					{"entry_reference":"tx-2","transaction_amount":{"amount":"4.50","currency":"EUR"},"credit_debit_indicator":"DBIT","status":"BOOK","booking_date":"2026-10-20","creditor":{"name":"Shop"},"debtor":{"name":"Holder"},"note":"Card payment"},
					{"entry_reference":"tx-cancel","transaction_amount":{"amount":"1.00","currency":"EUR"},"credit_debit_indicator":"DBIT","status":"CNCL"}
				],"continuation_key":"next-page"}`)
				return
			}
			if r.URL.Query().Get("continuation_key") != "next-page" {
				t.Errorf("second page continuation = %q", r.URL.Query().Get("continuation_key"))
			}
			_, _ = io.WriteString(w, `{"transactions":[
				{"entry_reference":"tx-3","transaction_amount":{"amount":"7.25","currency":"EUR"},"credit_debit_indicator":"DBIT","status":"PDNG","value_date":"2026-10-21","creditor":{"name":"Cafe"}}
			]}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	details, err := tc.client.GetAccountDetails(context.Background(), "acc-1")
	if err != nil {
		t.Fatalf("GetAccountDetails: %v", err)
	}
	if details.ResourceID != "acc-1" || details.IBAN != "ES123" || details.Currency != "EUR" || details.Name != "Cuenta corriente" {
		t.Fatalf("details = %+v", details)
	}

	balances, err := tc.client.GetBalances(context.Background(), "acc-1")
	if err != nil {
		t.Fatalf("GetBalances: %v", err)
	}
	if len(balances) != 2 || balances[0].BalanceType != "interimAvailable" || balances[1].BalanceType != "closingBooked" {
		t.Fatalf("balances = %+v", balances)
	}
	if balances[0].BalanceAmount.Value != "123.45" || balances[0].BalanceAmount.Currency != "EUR" {
		t.Fatalf("balance amount = %+v", balances[0].BalanceAmount)
	}

	transactions, err := tc.client.GetTransactions(context.Background(), "acc-1",
		time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GetTransactions: %v", err)
	}
	if pageRequests != 2 {
		t.Fatalf("pages = %d, want 2", pageRequests)
	}
	if len(transactions.Booked) != 2 || len(transactions.Pending) != 1 {
		t.Fatalf("transactions = %+v", transactions)
	}
	if transactions.Booked[0].TransactionAmount.Value != "10.00" || transactions.Booked[0].TransactionID != "tx-1" {
		t.Fatalf("credit transaction = %+v", transactions.Booked[0])
	}
	if transactions.Booked[1].TransactionAmount.Value != "-4.50" || transactions.Booked[1].CreditorName != "Shop" {
		t.Fatalf("debit transaction = %+v", transactions.Booked[1])
	}
	if transactions.Pending[0].TransactionAmount.Value != "-7.25" || transactions.Pending[0].BookingDate != "2026-10-21" {
		t.Fatalf("pending transaction = %+v", transactions.Pending[0])
	}
}

func TestAPIError(t *testing.T) {
	t.Parallel()
	tc := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"ACCESS_DENIED","message":"no access"}`)
	})
	_, err := tc.client.ListASPSPs(context.Background(), "ES", "")
	if err == nil {
		t.Fatal("expected API error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T", err)
	}
	if apiErr.StatusCode != http.StatusForbidden || !strings.Contains(apiErr.Error(), "ACCESS_DENIED") {
		t.Fatalf("APIError = %+v", apiErr)
	}
}

func TestParsePrivateKeyRejectsNonRSA(t *testing.T) {
	t.Parallel()
	_, err := New(Config{AppID: "app", PrivateKeyPEM: "not a key"})
	if err == nil || !strings.Contains(err.Error(), "PEM") {
		t.Fatalf("error = %v", err)
	}
}

type fakeConnectionStore struct {
	mu      sync.Mutex
	conn    domain.Connection
	updated bool
	err     error
}

func (f *fakeConnectionStore) GetConnectionByReference(_ context.Context, reference string) (domain.Connection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return domain.Connection{}, f.err
	}
	if reference != f.conn.Reference {
		return domain.Connection{}, errors.New("not found")
	}
	return f.conn, nil
}

func (f *fakeConnectionStore) UpdateConnectionAuthorization(_ context.Context, id int64, requisitionID, status, link, institutionID, institutionName, agreementID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updated = true
	f.conn.ID = id
	f.conn.RequisitionID = requisitionID
	f.conn.Status = status
	f.conn.Link = link
	f.conn.InstitutionID = institutionID
	f.conn.InstitutionName = institutionName
	f.conn.AgreementID = agreementID
	f.conn.Reference = ""
	return nil
}
