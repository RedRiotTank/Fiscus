package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/banking/gocardless"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

type fakeBank struct {
	mu             sync.Mutex
	requisition    gocardless.Requisition
	requisitionErr error
	details        map[string]gocardless.AccountDetails
	detailsErr     map[string]error
	balances       map[string][]gocardless.Balance
	balancesErr    map[string]error
	transactions   map[string]gocardless.Transactions
	txErr          map[string]error
	lastFrom       time.Time
	lastTo         time.Time
}

func (f *fakeBank) GetRequisition(_ context.Context, _ string) (*gocardless.Requisition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.requisitionErr != nil {
		return nil, f.requisitionErr
	}
	out := f.requisition
	return &out, nil
}

func (f *fakeBank) GetAccountDetails(_ context.Context, id string) (*gocardless.AccountDetails, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.detailsErr[id]; err != nil {
		return nil, err
	}
	d, ok := f.details[id]
	if !ok {
		return nil, errors.New("unknown account")
	}
	return &d, nil
}

func (f *fakeBank) GetBalances(_ context.Context, id string) ([]gocardless.Balance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.balancesErr[id]; err != nil {
		return nil, err
	}
	return f.balances[id], nil
}

func (f *fakeBank) GetTransactions(_ context.Context, id string, from, to time.Time) (*gocardless.Transactions, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.txErr[id]; err != nil {
		return nil, err
	}
	f.lastFrom = from
	f.lastTo = to
	txs := f.transactions[id]
	return &txs, nil
}

type fakeEvaluator struct {
	mu       sync.Mutex
	tx       []domain.Transaction
	balances []domain.Account
	err      error
}

func (f *fakeEvaluator) OnTransaction(_ context.Context, tx domain.Transaction, _ domain.Account) ([]domain.Alert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tx = append(f.tx, tx)
	return nil, f.err
}

func (f *fakeEvaluator) OnBalance(_ context.Context, account domain.Account) ([]domain.Alert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.balances = append(f.balances, account)
	return nil, f.err
}

func (f *fakeEvaluator) txCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.tx)
}

func (f *fakeEvaluator) balanceCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.balances)
}

func newIngestTest(t *testing.T, bank *fakeBank, eval Evaluator) (*Worker, *storage.Store, domain.Connection) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "ingest.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	conn := domain.Connection{
		RequisitionID:   "req-1",
		InstitutionID:   "INST_1",
		InstitutionName: "Test Bank",
		Status:          "LN",
	}
	if err := store.UpsertConnection(ctx, &conn); err != nil {
		t.Fatalf("UpsertConnection: %v", err)
	}
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	cfg.Now = func() time.Time { return now }
	cfg.SyncOnStart = false
	cfg.SyncTimeout = 10 * time.Second
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w, err := New(bank, store, eval, logger, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return w, store, conn
}

func standardBank() *fakeBank {
	return &fakeBank{
		requisition: gocardless.Requisition{
			ID: "req-1", Status: "LN", Accounts: []string{"acc-1"},
			InstitutionID: "INST_1", Link: "https://bank.test/link",
		},
		details: map[string]gocardless.AccountDetails{
			"acc-1": {
				ResourceID: "acc-1", IBAN: "ES0001", Currency: "EUR",
				Name: "Everyday", OwnerName: "Ada Lovelace",
			},
		},
		balances: map[string][]gocardless.Balance{
			"acc-1": {
				{BalanceType: "closingBooked", BalanceAmount: gocardless.Amount{Value: "100.00", Currency: "EUR"}},
				{BalanceType: "interimAvailable", BalanceAmount: gocardless.Amount{Value: "250.50", Currency: "EUR"}},
			},
		},
		transactions: map[string]gocardless.Transactions{
			"acc-1": {
				Booked: []gocardless.Transaction{
					{
						TransactionID: "tx-1", BookingDate: "2026-03-01",
						TransactionAmount: gocardless.Amount{Value: "-10.00", Currency: "EUR"},
						CreditorName:      "Shop", RemittanceInformationUnstructured: "Coffee",
					},
					{
						TransactionID: "tx-2", BookingDate: "2026-03-05",
						TransactionAmount: gocardless.Amount{Value: "-20.00", Currency: "EUR"},
						CreditorName:      "Shop",
					},
				},
				Pending: []gocardless.Transaction{
					{
						TransactionID: "tx-3", ValueDate: "2026-03-09",
						TransactionAmount: gocardless.Amount{Value: "-5.00", Currency: "EUR"},
						CreditorName:      "Cafe",
					},
				},
			},
		},
		detailsErr:  map[string]error{},
		balancesErr: map[string]error{},
		txErr:       map[string]error{},
	}
}

func TestWorkerSyncPersistsAndEvaluates(t *testing.T) {
	t.Parallel()
	bank := standardBank()
	eval := &fakeEvaluator{}
	w, store, _ := newIngestTest(t, bank, eval)
	ctx := context.Background()

	if err := w.SyncAll(ctx); err != nil {
		t.Fatalf("SyncAll: %v", err)
	}

	account, err := store.GetAccount(ctx, "acc-1")
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if account.BalanceMinor != 25050 || account.BalanceType != "interimAvailable" {
		t.Fatalf("balance = %d %q", account.BalanceMinor, account.BalanceType)
	}
	if account.Name != "Everyday" || account.Currency != "EUR" {
		t.Fatalf("account = %+v", account)
	}

	txs, err := store.ListTransactions(ctx, storage.TransactionFilter{AccountID: "acc-1"})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	if len(txs) != 3 {
		t.Fatalf("transactions = %d, want 3", len(txs))
	}
	if got := eval.txCount(); got != 2 {
		t.Fatalf("evaluated transactions = %d, want 2", got)
	}
	if got := eval.balanceCount(); got != 1 {
		t.Fatalf("evaluated balances = %d, want 1", got)
	}

	conn, err := store.GetConnectionByRequisitionID(ctx, "req-1")
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	if conn.LastSyncedAt == nil {
		t.Fatal("LastSyncedAt was not recorded")
	}
	if conn.LastError != "" {
		t.Fatalf("LastError = %q", conn.LastError)
	}
	if conn.Link != "https://bank.test/link" {
		t.Fatalf("Link = %q", conn.Link)
	}

	// First sync must backfill 90 days by default from the configured "now".
	expectedFrom := time.Date(2025, 12, 10, 0, 0, 0, 0, time.UTC)
	bank.mu.Lock()
	gotFrom, gotTo := bank.lastFrom, bank.lastTo
	bank.mu.Unlock()
	if !gotFrom.Equal(expectedFrom) {
		t.Fatalf("transactions from = %v, want %v", gotFrom, expectedFrom)
	}
	if !gotTo.Equal(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("transactions to = %v", gotTo)
	}
}

func TestWorkerIdempotentAndPromotesPending(t *testing.T) {
	t.Parallel()
	bank := standardBank()
	eval := &fakeEvaluator{}
	w, store, _ := newIngestTest(t, bank, eval)
	ctx := context.Background()

	if err := w.SyncAll(ctx); err != nil {
		t.Fatalf("first SyncAll: %v", err)
	}
	firstCount := eval.txCount()
	if err := w.SyncAll(ctx); err != nil {
		t.Fatalf("second SyncAll: %v", err)
	}
	if got := eval.txCount(); got != firstCount {
		t.Fatalf("duplicate ingestion re-evaluated transactions: %d -> %d", firstCount, got)
	}

	// The bank now books the pending transaction.
	bank.mu.Lock()
	pending := bank.transactions["acc-1"].Pending[0]
	pending.BookingDate = "2026-03-09"
	bank.transactions["acc-1"] = gocardless.Transactions{
		Booked:  append(bank.transactions["acc-1"].Booked, pending),
		Pending: nil,
	}
	bank.mu.Unlock()

	if err := w.SyncAll(ctx); err != nil {
		t.Fatalf("third SyncAll: %v", err)
	}
	if got := eval.txCount(); got != firstCount+1 {
		t.Fatalf("promoted transaction evaluations = %d, want %d", got, firstCount+1)
	}
	txs, err := store.ListTransactions(ctx, storage.TransactionFilter{AccountID: "acc-1"})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	for _, tx := range txs {
		if tx.ExternalID == "tx-3" && tx.Status != domain.TransactionStatusBooked {
			t.Fatalf("tx-3 status = %q, want booked", tx.Status)
		}
	}
}

func TestWorkerIsolatesAccountErrors(t *testing.T) {
	t.Parallel()
	bank := standardBank()
	bank.requisition.Accounts = []string{"acc-1", "bad"}
	bank.details["bad"] = gocardless.AccountDetails{ResourceID: "bad", Currency: "EUR"}
	bank.detailsErr["bad"] = errors.New("bank unavailable")
	eval := &fakeEvaluator{}
	w, store, _ := newIngestTest(t, bank, eval)
	ctx := context.Background()

	err := w.SyncAll(ctx)
	if err == nil {
		t.Fatal("expected sync error for failing account")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Fatalf("error = %v", err)
	}
	if _, err := store.GetAccount(ctx, "acc-1"); err != nil {
		t.Fatalf("healthy account was not synced: %v", err)
	}
	conn, err := store.GetConnectionByRequisitionID(ctx, "req-1")
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	if !strings.Contains(conn.LastError, "bad") {
		t.Fatalf("LastError = %q", conn.LastError)
	}
}

func TestWorkerRequiresLinkedConnection(t *testing.T) {
	t.Parallel()
	bank := standardBank()
	bank.requisition.Status = "UA"
	eval := &fakeEvaluator{}
	w, store, _ := newIngestTest(t, bank, eval)
	ctx := context.Background()

	if err := w.SyncAll(ctx); err == nil {
		t.Fatal("expected error for unlinked connection")
	}
	conn, err := store.GetConnectionByRequisitionID(ctx, "req-1")
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	if conn.Status != "UA" {
		t.Fatalf("status = %q", conn.Status)
	}
	if conn.LastError == "" {
		t.Fatal("LastError should describe the unlinked status")
	}
	if eval.txCount() != 0 || eval.balanceCount() != 0 {
		t.Fatal("rules should not run without account data")
	}
}

func TestSelectBalance(t *testing.T) {
	t.Parallel()
	balances := []gocardless.Balance{
		{BalanceType: "closingBooked", BalanceAmount: gocardless.Amount{Value: "1.00"}},
		{BalanceType: "interimAvailable", BalanceAmount: gocardless.Amount{Value: "2.00"}},
		{BalanceType: "expected", BalanceAmount: gocardless.Amount{Value: "3.00"}},
	}
	b, ok := selectBalance(balances)
	if !ok || b.BalanceType != "interimAvailable" {
		t.Fatalf("selected %+v ok=%v", b, ok)
	}
	b, ok = selectBalance([]gocardless.Balance{{BalanceType: "custom", BalanceAmount: gocardless.Amount{Value: "9.00"}}})
	if !ok || b.BalanceType != "custom" {
		t.Fatalf("fallback selected %+v ok=%v", b, ok)
	}
	if _, ok := selectBalance(nil); ok {
		t.Fatal("empty balances should not select a value")
	}
	if _, ok := selectBalance([]gocardless.Balance{{BalanceType: "interimAvailable"}}); ok {
		t.Fatal("empty amount should not select a value")
	}
}

func TestNormalizeTransaction(t *testing.T) {
	t.Parallel()
	account := domain.Account{ID: "acc-1", Currency: "EUR"}
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

	tx, err := normalizeTransaction(account, gocardless.Transaction{
		TransactionID:                          "tx-1",
		BookingDate:                            "2026-03-01",
		ValueDate:                              "2026-03-02",
		TransactionAmount:                      gocardless.Amount{Value: "-12.34", Currency: "EUR"},
		CreditorName:                           "Shop",
		RemittanceInformationUnstructuredArray: []string{"Invoice 1", "March"},
	}, domain.TransactionStatusBooked, now)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if tx.AmountMinor != -1234 || tx.Counterparty != "Shop" || tx.Description != "Invoice 1 | March" {
		t.Fatalf("unexpected tx: %+v", tx)
	}
	if tx.ValueDate == nil || !tx.ValueDate.Equal(time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("ValueDate = %v", tx.ValueDate)
	}

	// Missing transaction IDs produce a deterministic fallback.
	raw := gocardless.Transaction{
		ValueDate:         "2026-03-02",
		TransactionAmount: gocardless.Amount{Value: "42.00", Currency: "EUR"},
		DebtorName:        "Employer",
	}
	a, err := normalizeTransaction(account, raw, domain.TransactionStatusPending, now)
	if err != nil {
		t.Fatalf("normalize fallback: %v", err)
	}
	b, err := normalizeTransaction(account, raw, domain.TransactionStatusPending, now)
	if err != nil {
		t.Fatalf("normalize fallback: %v", err)
	}
	if !strings.HasPrefix(a.ExternalID, "sha256:") || a.ExternalID != b.ExternalID {
		t.Fatalf("fallback IDs = %q, %q", a.ExternalID, b.ExternalID)
	}
	if a.Counterparty != "Employer" || a.AmountMinor != 4200 {
		t.Fatalf("unexpected credit: %+v", a)
	}

	if _, err := normalizeTransaction(account, gocardless.Transaction{
		TransactionAmount: gocardless.Amount{Value: "not-money", Currency: "EUR"},
	}, domain.TransactionStatusBooked, now); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestTriggerCoalesces(t *testing.T) {
	t.Parallel()
	w, _, _ := newIngestTest(t, standardBank(), nil)
	if !w.Trigger("") {
		t.Fatal("first trigger should queue")
	}
	if w.Trigger("req-1") {
		t.Fatal("second trigger should be dropped while one is queued")
	}
	if got := <-w.trigger; got != "" {
		t.Fatalf("queued target = %q", got)
	}
}

func TestNewValidation(t *testing.T) {
	t.Parallel()
	if _, err := New(nil, nil, nil, nil, Config{}); err == nil {
		t.Fatal("expected validation error")
	}
	w, _, _ := newIngestTest(t, standardBank(), nil)
	if w.cfg.SyncInterval != DefaultConfig().SyncInterval {
		t.Fatalf("sync interval = %v", w.cfg.SyncInterval)
	}
}
