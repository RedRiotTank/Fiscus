package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/budgets"
	"github.com/RedRiotTank/Fiscus/internal/categories"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

type fakeTrigger struct {
	mu      sync.Mutex
	targets []string
	queue   bool
}

func (f *fakeTrigger) Trigger(requisitionID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.queue {
		f.targets = append(f.targets, requisitionID)
	}
	return f.queue
}

func (f *fakeTrigger) lastTarget() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.targets) == 0 {
		return ""
	}
	return f.targets[len(f.targets)-1]
}

type webFixture struct {
	server  *Server
	store   *storage.Store
	trigger *fakeTrigger
	handler http.Handler
}

func newWebFixture(t *testing.T) *webFixture {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	connection := domain.Connection{
		RequisitionID: "req-1", InstitutionID: "INST_1", InstitutionName: "Dev Bank",
		Status: "LN", Link: "https://bank.test/link", AgreementID: "agr-1",
	}
	if err := store.UpsertConnection(ctx, &connection); err != nil {
		t.Fatalf("UpsertConnection: %v", err)
	}
	expired := domain.Connection{
		RequisitionID: "req-2", InstitutionID: "INST_2", InstitutionName: "Old Bank",
		Status: "UA", Link: "https://bank.test/consent-2",
	}
	if err := store.UpsertConnection(ctx, &expired); err != nil {
		t.Fatalf("UpsertConnection expired: %v", err)
	}
	accounts := []domain.Account{
		{
			ID: "acc-1", ConnectionID: connection.ID, Name: "Everyday", OwnerName: "Ada Lovelace",
			IBAN: "ES0000000000000000000001", Currency: "EUR",
		},
		{
			ID: "acc-2", ConnectionID: connection.ID, Name: "Savings", OwnerName: "Ada Lovelace",
			IBAN: "ES0000000000000000000002", Currency: "EUR",
		},
	}
	for i := range accounts {
		if err := store.UpsertAccount(ctx, &accounts[i]); err != nil {
			t.Fatalf("UpsertAccount: %v", err)
		}
	}
	balanceAt := time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC)
	if err := store.UpdateAccountBalance(ctx, "acc-1", 125050, "interimAvailable", balanceAt); err != nil {
		t.Fatalf("balance: %v", err)
	}
	if err := store.UpdateAccountBalance(ctx, "acc-2", 50000, "interimAvailable", balanceAt); err != nil {
		t.Fatalf("balance: %v", err)
	}
	if err := store.UpdateConnectionSync(ctx, connection.ID, balanceAt, ""); err != nil {
		t.Fatalf("connection sync: %v", err)
	}

	transactions := []domain.Transaction{
		{
			AccountID: "acc-1", ExternalID: "tx-1", AmountMinor: -1250, Currency: "EUR",
			BookingDate: balanceAt.Add(-time.Hour), Counterparty: "Coffee Shop",
			Description: "Flat white", Category: "Dining", Status: domain.TransactionStatusBooked,
		},
		{
			AccountID: "acc-1", ExternalID: "tx-2", AmountMinor: 300000, Currency: "EUR",
			BookingDate: balanceAt.Add(-2 * time.Hour), Counterparty: "Employer",
			Description: "Salary", Category: "Payroll", Status: domain.TransactionStatusBooked,
		},
		{
			AccountID: "acc-2", ExternalID: "tx-3", AmountMinor: -999, Currency: "EUR",
			BookingDate: balanceAt.Add(-3 * time.Hour), Counterparty: "Cafe",
			Description: "Pending coffee", Status: domain.TransactionStatusPending,
		},
	}
	for i := range transactions {
		if _, _, err := store.UpsertTransaction(ctx, &transactions[i]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}

	deliveredAt := balanceAt.Add(30 * time.Minute)
	alerts := []domain.Alert{
		{
			Rule: "duplicate", Severity: domain.SeverityWarning, Title: "Possible duplicate charge",
			Message: "Coffee Shop was charged twice", AccountID: "acc-1",
			DedupKey: "dup:1", DeliveredAt: &deliveredAt,
		},
		{
			Rule: "liquidity", Severity: domain.SeverityCritical, Title: "Low balance",
			Message: "Balance below threshold", AccountID: "acc-2",
			DedupKey: "liq:1",
		},
	}
	for i := range alerts {
		if _, err := store.CreateAlert(ctx, &alerts[i]); err != nil {
			t.Fatalf("CreateAlert: %v", err)
		}
	}

	trigger := &fakeTrigger{queue: true}
	server, err := New(Config{
		Username: "admin",
		Password: "secret",
		Version:  "test",
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:      func() time.Time { return balanceAt.Add(time.Hour) },
		MetricsHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("fiscus_test_metric 1\n"))
		}),
		Classifier: categories.MustNew([]categories.Rule{
			{Category: "Dining", Pattern: "(?i)coffee|cafe"},
			{Category: "Payroll", Pattern: "(?i)employer"},
		}),
		Budgets: []budgets.Budget{{
			Category: "Dining", LimitMinor: 2500, Currency: "EUR", NotifyAtPercent: []int{80, 100},
		}},
	}, store, trigger)
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	return &webFixture{server: server, store: store, trigger: trigger, handler: server.Handler()}
}

func (f *webFixture) request(t *testing.T, method, path string, auth bool, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if auth {
		req.SetBasicAuth("admin", "secret")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func TestDashboardAuthAndContent(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)

	unauth := f.request(t, http.MethodGet, "/", false, nil)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauth.Code)
	}
	if !strings.Contains(unauth.Header().Get("WWW-Authenticate"), "Fiscus") {
		t.Fatalf("missing challenge: %q", unauth.Header().Get("WWW-Authenticate"))
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", "wrong")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d", rec.Code)
	}

	resp := f.request(t, http.MethodGet, "/", true, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	for _, want := range []string{
		"Cash position", "Everyday", "Savings", "€1,250.50", "€500.00",
		"Coffee Shop", "Possible duplicate charge", "Low balance", "Dev Bank", "Linked",
		"Spending by category", "Dining", "€12.50", "monthly burn", "Runway",
		"Budgets", "50%", "€12.50 of €25.00", "32% of month elapsed",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	if got := resp.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("content type = %q", got)
	}
	if got := resp.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache control = %q", got)
	}
	if !strings.Contains(resp.Header().Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatalf("missing CSP: %q", resp.Header().Get("Content-Security-Policy"))
	}
}

func TestSubscriptionsAndFeesPage(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 10, 11, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		{AccountID: "acc-1", ExternalID: "sub-1", AmountMinor: -999, Currency: "EUR", BookingDate: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), Counterparty: "Streaming", Status: domain.TransactionStatusBooked, Category: "Subscriptions"},
		{AccountID: "acc-1", ExternalID: "sub-2", AmountMinor: -999, Currency: "EUR", BookingDate: time.Date(2026, 2, 4, 0, 0, 0, 0, time.UTC), Counterparty: "Streaming", Status: domain.TransactionStatusBooked, Category: "Subscriptions"},
		{AccountID: "acc-1", ExternalID: "sub-3", AmountMinor: -999, Currency: "EUR", BookingDate: time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC), Counterparty: "Streaming", Status: domain.TransactionStatusBooked, Category: "Subscriptions"},
		{AccountID: "acc-1", ExternalID: "fee-1", AmountMinor: -1500, Currency: "EUR", BookingDate: now.Add(-48 * time.Hour), Counterparty: "Comisi\u00f3n mantenimiento", Status: domain.TransactionStatusBooked, Category: "Fees"},
	}
	for i := range transactions {
		if _, _, err := f.store.UpsertTransaction(ctx, &transactions[i]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}

	resp := f.request(t, http.MethodGet, "/subscriptions", true, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	for _, want := range []string{
		"Subscriptions &amp; Fees", "Recurring subscriptions", "Streaming", "€9.99",
		"€119.88", "Detected bank fees", "Maintenance fee", "€15.00",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("subscriptions page missing %q", want)
		}
	}
}

func TestExportDownloads(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	ctx := context.Background()
	transferTime := time.Date(2026, 3, 10, 9, 30, 0, 0, time.UTC)
	transfers := []domain.Transaction{
		{AccountID: "acc-1", ExternalID: "move-out", AmountMinor: -5000, Currency: "EUR", BookingDate: transferTime, Counterparty: "Own savings", Status: domain.TransactionStatusBooked},
		{AccountID: "acc-2", ExternalID: "move-in", AmountMinor: 5000, Currency: "EUR", BookingDate: transferTime, Counterparty: "Own checking", Status: domain.TransactionStatusBooked},
	}
	for i := range transfers {
		if _, _, err := f.store.UpsertTransaction(ctx, &transfers[i]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}

	csvResponse := f.request(t, http.MethodGet, "/transactions/export?format=csv", true, nil)
	if csvResponse.Code != http.StatusOK {
		t.Fatalf("csv status = %d body=%s", csvResponse.Code, csvResponse.Body.String())
	}
	if got := csvResponse.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Fatalf("csv content type = %q", got)
	}
	if got := csvResponse.Header().Get("Content-Disposition"); !strings.Contains(got, ".csv") {
		t.Fatalf("csv disposition = %q", got)
	}
	body := csvResponse.Body.String()
	if !strings.Contains(body, "Coffee Shop") || !strings.Contains(body, "-12.50") {
		t.Fatalf("csv missing transactions: %s", body)
	}
	if strings.Contains(body, "Own savings") || strings.Contains(body, "Own checking") {
		t.Fatalf("internal transfer was exported by default: %s", body)
	}

	included := f.request(t, http.MethodGet, "/transactions/export?format=csv&include_transfers=1", true, nil)
	if included.Code != http.StatusOK || !strings.Contains(included.Body.String(), "Own savings") {
		t.Fatalf("include_transfers did not include transfer: %d %s", included.Code, included.Body.String())
	}

	journal := f.request(t, http.MethodGet, "/transactions/export?format=hledger&account=acc-1", true, nil)
	if journal.Code != http.StatusOK {
		t.Fatalf("journal status = %d", journal.Code)
	}
	if got := journal.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("journal content type = %q", got)
	}
	if got := journal.Header().Get("Content-Disposition"); !strings.Contains(got, ".journal") {
		t.Fatalf("journal disposition = %q", got)
	}
	journalBody := journal.Body.String()
	if !strings.Contains(journalBody, "assets:bank:everyday") || !strings.Contains(journalBody, "expenses:dining") {
		t.Fatalf("journal missing expected accounts: %s", journalBody)
	}
	if strings.Contains(journalBody, "Cafe") {
		t.Fatalf("journal account filter not applied: %s", journalBody)
	}

	beancount := f.request(t, http.MethodGet, "/transactions/export?format=beancount&from=2026-03-10&to=2026-03-10", true, nil)
	if beancount.Code != http.StatusOK {
		t.Fatalf("beancount status = %d body=%s", beancount.Code, beancount.Body.String())
	}
	if got := beancount.Header().Get("Content-Disposition"); !strings.Contains(got, ".beancount") {
		t.Fatalf("beancount disposition = %q", got)
	}
	if !strings.Contains(beancount.Body.String(), "2000-01-01 open") || !strings.Contains(beancount.Body.String(), "Coffee Shop") {
		t.Fatalf("beancount missing content: %s", beancount.Body.String())
	}
}

func TestExportRejectsInvalidParameters(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	if got := f.request(t, http.MethodGet, "/transactions/export?format=pdf", true, nil).Code; got != http.StatusBadRequest {
		t.Fatalf("invalid format status = %d, want 400", got)
	}
	if got := f.request(t, http.MethodGet, "/transactions/export?from=10-03-2026", true, nil).Code; got != http.StatusBadRequest {
		t.Fatalf("invalid date status = %d, want 400", got)
	}
	if got := f.request(t, http.MethodGet, "/transactions/export?from=2026-03-11&to=2026-03-10", true, nil).Code; got != http.StatusBadRequest {
		t.Fatalf("inverted range status = %d, want 400", got)
	}
	if got := f.request(t, http.MethodGet, "/transactions/export?format=csv", false, nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated export status = %d, want 401", got)
	}
}

func TestBcryptAuth(t *testing.T) {
	t.Parallel()
	hash, err := bcrypt.GenerateFromPassword([]byte("hashed-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "bcrypt.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	server, err := New(Config{
		Username: "admin", PasswordHash: string(hash),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, store, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	handler := server.Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", "hashed-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHealthReadinessAndMetrics(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)

	health := f.request(t, http.MethodGet, "/healthz", false, nil)
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"status":"ok"`) {
		t.Fatalf("health = %d %s", health.Code, health.Body.String())
	}
	ready := f.request(t, http.MethodGet, "/readyz", false, nil)
	if ready.Code != http.StatusOK || !strings.Contains(ready.Body.String(), `"status":"ready"`) {
		t.Fatalf("ready = %d %s", ready.Code, ready.Body.String())
	}
	metrics := f.request(t, http.MethodGet, "/metrics", false, nil)
	if metrics.Code != http.StatusOK || !strings.Contains(metrics.Body.String(), "fiscus_test_metric") {
		t.Fatalf("metrics = %d %s", metrics.Code, metrics.Body.String())
	}
}

func TestReadinessFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "unmigrated.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	server, err := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, store, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed store status = %d, want 503", rec.Code)
	}
}

func TestSyncHandlers(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)

	noHeader := f.request(t, http.MethodPost, "/sync", true, nil)
	if noHeader.Code != http.StatusForbidden {
		t.Fatalf("missing header status = %d", noHeader.Code)
	}

	headers := map[string]string{"X-Fiscus-Request": "1"}
	syncAll := f.request(t, http.MethodPost, "/sync", true, headers)
	if syncAll.Code != http.StatusAccepted || !strings.Contains(syncAll.Body.String(), "Sync requested") {
		t.Fatalf("sync all = %d %s", syncAll.Code, syncAll.Body.String())
	}
	if got := f.trigger.lastTarget(); got != "" {
		t.Fatalf("target = %q", got)
	}

	perConnection := f.request(t, http.MethodPost, "/connections/1/sync", true, headers)
	if perConnection.Code != http.StatusAccepted {
		t.Fatalf("per connection = %d %s", perConnection.Code, perConnection.Body.String())
	}
	if got := f.trigger.lastTarget(); got != "req-1" {
		t.Fatalf("target = %q", got)
	}
	missing := f.request(t, http.MethodPost, "/connections/999/sync", true, headers)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing connection = %d", missing.Code)
	}
	invalid := f.request(t, http.MethodPost, "/connections/abc/sync", true, headers)
	if invalid.Code != http.StatusNotFound {
		t.Fatalf("invalid connection = %d", invalid.Code)
	}

	// A queued sync is accepted but reported as already pending.
	f.trigger.mu.Lock()
	f.trigger.queue = false
	f.trigger.mu.Unlock()
	queued := f.request(t, http.MethodPost, "/sync", true, headers)
	if queued.Code != http.StatusAccepted || !strings.Contains(queued.Body.String(), "already") {
		t.Fatalf("queued = %d %s", queued.Code, queued.Body.String())
	}

	// GET is not allowed on POST-only routes.
	if got := f.request(t, http.MethodGet, "/sync", true, nil).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("GET /sync = %d, want 405", got)
	}
}

func TestSyncHandlersRespectRateLimitCooldown(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	ctx := context.Background()
	until := time.Date(2026, 3, 10, 18, 0, 0, 0, time.UTC)
	if err := f.store.UpdateConnectionSync(ctx, 1, time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC), "generic sync error"); err != nil {
		t.Fatalf("UpdateConnectionSync: %v", err)
	}
	connections, err := f.store.ListConnections(ctx)
	if err != nil {
		t.Fatalf("ListConnections: %v", err)
	}
	for _, connection := range connections {
		if err := f.store.SetConnectionRateLimit(ctx, connection.ID, until, "ASPSP_RATE_LIMIT_EXCEEDED"); err != nil {
			t.Fatalf("SetConnectionRateLimit: %v", err)
		}
	}

	page := f.request(t, http.MethodGet, "/connections", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("connections page = %d", page.Code)
	}
	body := page.Body.String()
	if !strings.Contains(body, "Quota Exceeded (Cooldown until 10 Mar 2026 18:00)") {
		t.Fatalf("connections page missing cooldown badge: %s", body)
	}
	if strings.Contains(body, "generic sync error") {
		t.Fatal("cooldown badge should replace the generic sync error")
	}

	headers := map[string]string{"X-Fiscus-Request": "1"}
	for _, path := range []string{"/connections/1/sync", "/sync"} {
		resp := f.request(t, http.MethodPost, path, true, headers)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, resp.Code)
		}
		message := resp.Body.String()
		if !strings.Contains(message, "Bank rate limit reached (PSD2 max daily limit).") ||
			!strings.Contains(message, "Displaying cached data.") ||
			!strings.Contains(message, "Next sync available at 2026-03-10 18:00 UTC.") {
			t.Fatalf("%s message = %s", path, message)
		}
	}
	f.trigger.mu.Lock()
	defer f.trigger.mu.Unlock()
	if len(f.trigger.targets) != 0 {
		t.Fatalf("sync was triggered during cooldown: %v", f.trigger.targets)
	}
}

func TestRecategorizeTrigger(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	headers := map[string]string{"X-Fiscus-Request": "1"}

	page := f.request(t, http.MethodGet, "/transactions", true, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Re-run Rules") {
		t.Fatalf("transactions page missing re-run control: %d", page.Code)
	}

	forbidden := f.request(t, http.MethodPost, "/recategorize", true, nil)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("missing header status = %d", forbidden.Code)
	}
	resp := f.request(t, http.MethodPost, "/recategorize", true, headers)
	if resp.Code != http.StatusOK {
		t.Fatalf("recategorize status = %d body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "updated 1") {
		t.Fatalf("recategorize body = %s", resp.Body.String())
	}
	ctx := context.Background()
	transactions, err := f.store.ListTransactions(ctx, storage.TransactionFilter{})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	found := false
	for _, tx := range transactions {
		if tx.ExternalID == "tx-3" {
			found = true
			if tx.Category != "Dining" {
				t.Fatalf("tx-3 category = %q, want Dining", tx.Category)
			}
		}
	}
	if !found {
		t.Fatal("tx-3 not found")
	}
}

func TestRecategorizeWithoutRules(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	f.server.classifier = nil
	resp := f.request(t, http.MethodPost, "/recategorize", true, map[string]string{"X-Fiscus-Request": "1"})
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.Code)
	}
}

func TestSyncWithoutBanking(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	f.server.trigger = nil
	resp := f.request(t, http.MethodPost, "/sync", true, map[string]string{"X-Fiscus-Request": "1"})
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.Code)
	}
}

func TestPagesAndPartials(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	auth := true

	transactions := f.request(t, http.MethodGet, "/transactions?account=acc-1", auth, nil)
	if transactions.Code != http.StatusOK {
		t.Fatalf("transactions status = %d", transactions.Code)
	}
	body := transactions.Body.String()
	if !strings.Contains(body, "Coffee Shop") || strings.Contains(body, "Pending coffee") {
		t.Fatalf("account filter not applied")
	}
	if !strings.Contains(body, "3 transactions stored") {
		t.Fatalf("transaction count missing")
	}

	filtered := f.request(t, http.MethodGet, "/transactions?account=unknown", auth, nil)
	if filtered.Code != http.StatusOK || !strings.Contains(filtered.Body.String(), "No transactions match") {
		t.Fatalf("empty filter page: %d", filtered.Code)
	}

	alerts := f.request(t, http.MethodGet, "/alerts", auth, nil)
	if alerts.Code != http.StatusOK || !strings.Contains(alerts.Body.String(), "Low balance") {
		t.Fatalf("alerts page missing content")
	}

	connections := f.request(t, http.MethodGet, "/connections", auth, nil)
	if connections.Code != http.StatusOK {
		t.Fatalf("connections status = %d", connections.Code)
	}
	for _, want := range []string{"Dev Bank", "Everyday", "Linked", "Continue consent"} {
		if !strings.Contains(connections.Body.String(), want) {
			t.Errorf("connections page missing %q", want)
		}
	}

	overview := f.request(t, http.MethodGet, "/partials/overview", auth, nil)
	if overview.Code != http.StatusOK || !strings.Contains(overview.Body.String(), `id="overview-panel"`) {
		t.Fatalf("overview partial = %d", overview.Code)
	}
	alertsPartial := f.request(t, http.MethodGet, "/partials/alerts", auth, nil)
	if alertsPartial.Code != http.StatusOK || !strings.Contains(alertsPartial.Body.String(), `id="alerts-panel"`) {
		t.Fatalf("alerts partial = %d", alertsPartial.Code)
	}
	connPartial := f.request(t, http.MethodGet, "/partials/connections", auth, nil)
	if connPartial.Code != http.StatusOK || !strings.Contains(connPartial.Body.String(), `id="connections-panel"`) {
		t.Fatalf("connections partial = %d", connPartial.Code)
	}
}

func TestStaticAssetsAndSecurity(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)

	static := f.request(t, http.MethodGet, "/static/htmx.min.js", true, nil)
	if static.Code != http.StatusOK || static.Body.Len() == 0 {
		t.Fatalf("static = %d len=%d", static.Code, static.Body.Len())
	}
	if got := static.Header().Get("Cache-Control"); !strings.Contains(got, "max-age") {
		t.Fatalf("cache control = %q", got)
	}
	if unauth := f.request(t, http.MethodGet, "/static/htmx.min.js", false, nil); unauth.Code != http.StatusUnauthorized {
		t.Fatalf("static without auth = %d", unauth.Code)
	}
	if got := f.request(t, http.MethodGet, "/nope", true, nil).Code; got != http.StatusNotFound {
		t.Fatalf("unknown route = %d, want 404", got)
	}
	headers := f.request(t, http.MethodGet, "/", true, nil).Header()
	if headers.Get("X-Content-Type-Options") != "nosniff" || headers.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("security headers missing: %v", headers)
	}
}

func TestNoAuthMode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "noauth.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	server, err := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, store, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestTemplateEscaping(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	ctx := context.Background()
	account, err := f.store.GetAccount(ctx, "acc-1")
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	account.Name = `<script>alert(1)</script>`
	if err := f.store.UpsertAccount(ctx, &account); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	body := f.request(t, http.MethodGet, "/", true, nil).Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatal("account name was not escaped")
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatal("escaped account name not rendered")
	}
}

type authorizationCall struct {
	code  string
	state string
}

type fakeAuthorizationCallback struct {
	result string
	err    error
	calls  []authorizationCall
}

func (f *fakeAuthorizationCallback) CompleteAuthorization(_ context.Context, code, state string) (string, error) {
	f.calls = append(f.calls, authorizationCall{code: code, state: state})
	if f.err != nil {
		return "", f.err
	}
	return f.result, nil
}

func TestAuthorizationCallback(t *testing.T) {
	t.Parallel()
	callback := &fakeAuthorizationCallback{result: "session-1"}
	f := newWebFixture(t)
	f.server.authHandler = callback

	resp := f.request(t, http.MethodGet, "/callback?code=code-1&state=state-1", false, nil)
	if resp.Code != http.StatusSeeOther {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	if location := resp.Header().Get("Location"); location != "/connections?auth=success" {
		t.Fatalf("location = %q", location)
	}
	if len(callback.calls) != 1 || callback.calls[0] != (authorizationCall{code: "code-1", state: "state-1"}) {
		t.Fatalf("callback calls = %+v", callback.calls)
	}
	if len(f.trigger.targets) != 1 || f.trigger.targets[0] != "" {
		t.Fatalf("trigger targets = %+v", f.trigger.targets)
	}

	page := f.request(t, http.MethodGet, "/connections?auth=success", true, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Bank authorization completed") {
		t.Fatalf("success page status=%d body=%s", page.Code, page.Body.String())
	}

	callback.err = errors.New("exchange failed")
	resp = f.request(t, http.MethodGet, "/callback?code=code-2&state=state-2", false, nil)
	if resp.Code != http.StatusSeeOther || resp.Header().Get("Location") != "/connections?auth=error" {
		t.Fatalf("error callback status=%d location=%q", resp.Code, resp.Header().Get("Location"))
	}
	page = f.request(t, http.MethodGet, "/connections?auth=error", true, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "could not be completed") {
		t.Fatalf("error page status=%d body=%s", page.Code, page.Body.String())
	}

	resp = f.request(t, http.MethodGet, "/callback?error=access_denied", false, nil)
	if resp.Code != http.StatusSeeOther || resp.Header().Get("Location") != "/connections?auth=error" {
		t.Fatalf("provider error status=%d location=%q", resp.Code, resp.Header().Get("Location"))
	}

	resp = f.request(t, http.MethodGet, "/callback?code=code-1", false, nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("missing state status = %d", resp.Code)
	}
}
