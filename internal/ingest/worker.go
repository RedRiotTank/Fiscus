// Package ingest periodically pulls accounts, balances, and transactions from a
// bank provider and persists them. It is deliberately provider-agnostic through
// small interfaces so it can be tested without network access.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/banking/gocardless"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// Bank is the subset of the GoCardless API used by the worker.
type Bank interface {
	GetRequisition(ctx context.Context, requisitionID string) (*gocardless.Requisition, error)
	GetAccountDetails(ctx context.Context, accountID string) (*gocardless.AccountDetails, error)
	GetBalances(ctx context.Context, accountID string) ([]gocardless.Balance, error)
	GetTransactions(ctx context.Context, accountID string, from, to time.Time) (*gocardless.Transactions, error)
}

// Store is the persistence surface required by the worker.
type Store interface {
	ListConnections(ctx context.Context) ([]domain.Connection, error)
	GetConnectionByRequisitionID(ctx context.Context, requisitionID string) (domain.Connection, error)
	UpsertConnection(ctx context.Context, c *domain.Connection) error
	UpdateConnectionSync(ctx context.Context, id int64, syncedAt time.Time, syncErr string) error
	UpsertAccount(ctx context.Context, a *domain.Account) error
	UpdateAccountBalance(ctx context.Context, accountID string, balanceMinor int64, balanceType string, at time.Time) error
	GetAccount(ctx context.Context, accountID string) (domain.Account, error)
	UpsertTransaction(ctx context.Context, t *domain.Transaction) (inserted bool, promoted bool, err error)
}

// Evaluator receives newly ingested data so alert rules can run inline.
type Evaluator interface {
	OnTransaction(ctx context.Context, tx domain.Transaction, account domain.Account) ([]domain.Alert, error)
	OnBalance(ctx context.Context, account domain.Account) ([]domain.Alert, error)
}

// Observer receives sync telemetry. Implementations must be safe for concurrent
// use. A nil observer disables observation.
type Observer interface {
	ObserveSync(requisitionID string, duration time.Duration, err error)
	ObserveTransactions(accountID string, inserted int)
	ObserveBalance(account domain.Account)
}

// Config controls ingestion behaviour.
type Config struct {
	SyncInterval        time.Duration
	SyncTimeout         time.Duration
	InitialLookbackDays int
	OverlapDays         int
	SyncOnStart         bool
	Now                 func() time.Time
}

// DefaultConfig returns production-oriented defaults.
func DefaultConfig() Config {
	return Config{
		SyncInterval:        15 * time.Minute,
		SyncTimeout:         5 * time.Minute,
		InitialLookbackDays: 90,
		OverlapDays:         3,
		SyncOnStart:         true,
		Now:                 time.Now,
	}
}

// Worker synchronizes bank connections on a schedule or on demand.
type Worker struct {
	bank      Bank
	store     Store
	evaluator Evaluator
	observer  Observer
	logger    *slog.Logger
	cfg       Config
	trigger   chan string

	mu sync.Mutex
}

// New constructs a Worker. A nil evaluator disables rule evaluation.
func New(bank Bank, store Store, evaluator Evaluator, logger *slog.Logger, cfg Config) (*Worker, error) {
	if bank == nil {
		return nil, errors.New("ingest: bank client is required")
	}
	if store == nil {
		return nil, errors.New("ingest: store is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	defaults := DefaultConfig()
	if cfg.SyncInterval <= 0 {
		cfg.SyncInterval = defaults.SyncInterval
	}
	if cfg.SyncTimeout <= 0 {
		cfg.SyncTimeout = defaults.SyncTimeout
	}
	if cfg.InitialLookbackDays <= 0 {
		cfg.InitialLookbackDays = defaults.InitialLookbackDays
	}
	if cfg.OverlapDays < 0 {
		cfg.OverlapDays = defaults.OverlapDays
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Worker{
		bank:      bank,
		store:     store,
		evaluator: evaluator,
		logger:    logger,
		cfg:       cfg,
		trigger:   make(chan string, 1),
	}, nil
}

// SetObserver installs an optional telemetry observer.
func (w *Worker) SetObserver(o Observer) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.observer = o
}

func (w *Worker) observeSync(requisitionID string, d time.Duration, err error) {
	w.mu.Lock()
	o := w.observer
	w.mu.Unlock()
	if o != nil {
		o.ObserveSync(requisitionID, d, err)
	}
}

func (w *Worker) observeTransactions(accountID string, inserted int) {
	w.mu.Lock()
	o := w.observer
	w.mu.Unlock()
	if o != nil {
		o.ObserveTransactions(accountID, inserted)
	}
}

func (w *Worker) observeBalance(account domain.Account) {
	w.mu.Lock()
	o := w.observer
	w.mu.Unlock()
	if o != nil {
		o.ObserveBalance(account)
	}
}

// Trigger requests a sync. An empty requisition ID syncs every connection. It
// never blocks; true indicates the request was queued. Requests made while a
// sync is running are coalesced into one subsequent sync.
func (w *Worker) Trigger(requisitionID string) bool {
	select {
	case w.trigger <- requisitionID:
		return true
	default:
		return false
	}
}

// Run performs an optional initial sync and then processes scheduled and manual
// sync requests until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	w.logger.Info("ingestion worker started", "interval", w.cfg.SyncInterval.String())
	if w.cfg.SyncOnStart {
		w.runTimed(ctx, "")
	}

	ticker := time.NewTicker(w.cfg.SyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("ingestion worker stopped")
			return
		case <-ticker.C:
			w.runTimed(ctx, "")
		case target := <-w.trigger:
			w.runTimed(ctx, target)
		}
	}
}

func (w *Worker) runTimed(ctx context.Context, requisitionID string) {
	syncCtx, cancel := context.WithTimeout(ctx, w.cfg.SyncTimeout)
	defer cancel()

	var err error
	if requisitionID == "" {
		err = w.SyncAll(syncCtx)
	} else {
		err = w.SyncConnection(syncCtx, requisitionID)
	}
	if err != nil {
		w.logger.Error("bank sync failed", "requisition_id", requisitionID, "error", err)
	}
}

// SyncAll synchronizes every known connection. Errors from individual
// connections are joined so one failing bank does not stop the others.
func (w *Worker) SyncAll(ctx context.Context) error {
	connections, err := w.store.ListConnections(ctx)
	if err != nil {
		return fmt.Errorf("ingest: list connections: %w", err)
	}
	if len(connections) == 0 {
		w.logger.Debug("bank sync skipped: no connections")
		return nil
	}
	var errs []error
	for _, c := range connections {
		if err := w.syncConnection(ctx, c); err != nil {
			errs = append(errs, fmt.Errorf("requisition %s: %w", c.RequisitionID, err))
		}
	}
	return errors.Join(errs...)
}

// SyncConnection synchronizes a single requisition.
func (w *Worker) SyncConnection(ctx context.Context, requisitionID string) error {
	connection, err := w.store.GetConnectionByRequisitionID(ctx, requisitionID)
	if err != nil {
		return fmt.Errorf("ingest: load connection %q: %w", requisitionID, err)
	}
	return w.syncConnection(ctx, connection)
}

func (w *Worker) syncConnection(ctx context.Context, connection domain.Connection) error {
	started := w.cfg.Now()
	var syncErr error
	defer func() {
		w.observeSync(connection.RequisitionID, w.cfg.Now().Sub(started), syncErr)
	}()

	requisition, err := w.bank.GetRequisition(ctx, connection.RequisitionID)
	if err != nil {
		syncErr = fmt.Errorf("fetch requisition: %w", err)
		w.recordSyncFailure(ctx, connection, syncErr)
		return syncErr
	}

	connection.Status = requisition.Status
	connection.Link = requisition.Link
	connection.AgreementID = requisition.AgreementID
	if connection.InstitutionID == "" {
		connection.InstitutionID = requisition.InstitutionID
	}
	if err := w.store.UpsertConnection(ctx, &connection); err != nil {
		syncErr = fmt.Errorf("update requisition status: %w", err)
		w.recordSyncFailure(ctx, connection, syncErr)
		return syncErr
	}

	if !connection.Linked() {
		w.logger.Info("bank connection is not linked; skipping account sync",
			"requisition_id", connection.RequisitionID, "status", connection.Status)
		err := fmt.Errorf("connection status %q does not allow account access", connection.Status)
		syncErr = err
		w.recordSyncFailure(ctx, connection, err)
		return err
	}

	var errs []error
	for _, accountID := range requisition.Accounts {
		if err := w.syncAccount(ctx, connection, accountID); err != nil {
			w.logger.Error("account sync failed",
				"requisition_id", connection.RequisitionID, "account_id", accountID, "error", err)
			errs = append(errs, fmt.Errorf("account %s: %w", accountID, err))
		}
	}
	syncErr = errors.Join(errs...)
	w.recordSyncResult(ctx, connection, syncErr)
	return syncErr
}

func (w *Worker) recordSyncFailure(ctx context.Context, connection domain.Connection, err error) {
	if connection.ID == 0 {
		return
	}
	// Use a background context so a cancelled sync can still record its error.
	updateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if updateErr := w.store.UpdateConnectionSync(updateCtx, connection.ID, w.cfg.Now(), truncate(err.Error(), 1000)); updateErr != nil {
		w.logger.Error("failed to record sync failure", "requisition_id", connection.RequisitionID, "error", updateErr)
	}
}

func (w *Worker) recordSyncResult(ctx context.Context, connection domain.Connection, syncErr error) {
	msg := ""
	if syncErr != nil {
		msg = truncate(syncErr.Error(), 1000)
	}
	if err := w.store.UpdateConnectionSync(ctx, connection.ID, w.cfg.Now(), msg); err != nil {
		w.logger.Error("failed to record sync result", "requisition_id", connection.RequisitionID, "error", err)
	}
}

func (w *Worker) syncAccount(ctx context.Context, connection domain.Connection, accountID string) error {
	var errs []error

	details, err := w.bank.GetAccountDetails(ctx, accountID)
	if err != nil {
		return fmt.Errorf("account details: %w", err)
	}
	account := domain.Account{
		ID:           accountID,
		ConnectionID: connection.ID,
		IBAN:         details.IBAN,
		Name:         details.Name,
		OwnerName:    details.OwnerName,
		Currency:     strings.ToUpper(details.Currency),
	}
	if account.Currency == "" {
		account.Currency = "EUR"
	}
	if account.Name == "" {
		account.Name = account.IBAN
	}
	if account.Name == "" {
		account.Name = accountID
	}
	if err := w.store.UpsertAccount(ctx, &account); err != nil {
		return fmt.Errorf("persist account: %w", err)
	}

	balances, balanceErr := w.bank.GetBalances(ctx, accountID)
	if balanceErr != nil {
		errs = append(errs, fmt.Errorf("balances: %w", balanceErr))
	} else if chosen, ok := selectBalance(balances); ok {
		balanceCurrency := strings.ToUpper(chosen.BalanceAmount.Currency)
		if balanceCurrency == "" {
			balanceCurrency = account.Currency
		}
		minor, parseErr := money.Parse(chosen.BalanceAmount.Value, balanceCurrency)
		if parseErr != nil {
			errs = append(errs, fmt.Errorf("parse balance: %w", parseErr))
		} else {
			if err := w.store.UpdateAccountBalance(ctx, accountID, minor, chosen.BalanceType, w.cfg.Now()); err != nil {
				errs = append(errs, fmt.Errorf("persist balance: %w", err))
			} else {
				account.BalanceMinor = minor
				account.BalanceType = chosen.BalanceType
				w.observeBalance(account)
				errs = append(errs, w.evaluateBalance(ctx, accountID))
			}
		}
	}

	inserted, err := w.syncTransactions(ctx, account, connection)
	if err != nil {
		errs = append(errs, err)
	}
	if inserted > 0 {
		w.observeTransactions(accountID, inserted)
	}
	return errors.Join(errs...)
}

func (w *Worker) evaluateBalance(ctx context.Context, accountID string) error {
	if w.evaluator == nil {
		return nil
	}
	account, err := w.store.GetAccount(ctx, accountID)
	if err != nil {
		return fmt.Errorf("load account for balance rules: %w", err)
	}
	if _, err := w.evaluator.OnBalance(ctx, account); err != nil {
		return fmt.Errorf("evaluate balance rules: %w", err)
	}
	return nil
}

// selectBalance prefers balances that represent spendable funds. GoCardless
// commonly returns both interimAvailable and closingBooked.
func selectBalance(balances []gocardless.Balance) (gocardless.Balance, bool) {
	preference := []string{
		"interimAvailable", "expected", "forwardAvailable", "interimBooked",
		"closingBooked", "openingBooked", "authorised", "available",
	}
	for _, wanted := range preference {
		for _, b := range balances {
			if b.BalanceType == wanted && b.BalanceAmount.Value != "" {
				return b, true
			}
		}
	}
	for _, b := range balances {
		if b.BalanceAmount.Value != "" {
			return b, true
		}
	}
	return gocardless.Balance{}, false
}

func (w *Worker) syncTransactions(ctx context.Context, account domain.Account, connection domain.Connection) (int, error) {
	now := w.cfg.Now()
	from := now.AddDate(0, 0, -w.cfg.InitialLookbackDays)
	if connection.LastSyncedAt != nil {
		candidate := connection.LastSyncedAt.AddDate(0, 0, -w.cfg.OverlapDays)
		if candidate.After(from) {
			from = candidate
		}
	}
	from = startOfDay(from)

	transactions, err := w.bank.GetTransactions(ctx, account.ID, from, now)
	if err != nil {
		return 0, fmt.Errorf("transactions: %w", err)
	}

	booked := make([]domain.Transaction, 0, len(transactions.Booked))
	for _, tx := range transactions.Booked {
		normalized, err := normalizeTransaction(account, tx, domain.TransactionStatusBooked, now)
		if err != nil {
			w.logger.Warn("skipping invalid booked transaction",
				"account_id", account.ID, "transaction_id", tx.TransactionID, "error", err)
			continue
		}
		booked = append(booked, normalized)
	}
	pending := make([]domain.Transaction, 0, len(transactions.Pending))
	for _, tx := range transactions.Pending {
		normalized, err := normalizeTransaction(account, tx, domain.TransactionStatusPending, now)
		if err != nil {
			w.logger.Warn("skipping invalid pending transaction",
				"account_id", account.ID, "transaction_id", tx.TransactionID, "error", err)
			continue
		}
		pending = append(pending, normalized)
	}

	// Process oldest first so duplicate and anomaly rules see a coherent history
	// even during the first backfill.
	sortTransactions(booked)
	sortTransactions(pending)

	inserted := 0
	for _, tx := range append(booked, pending...) {
		created, promoted, err := w.store.UpsertTransaction(ctx, &tx)
		if err != nil {
			return inserted, fmt.Errorf("persist transaction %s: %w", tx.ExternalID, err)
		}
		if !created && !promoted {
			continue
		}
		if created {
			inserted++
		}
		if w.evaluator == nil || tx.Status != domain.TransactionStatusBooked {
			continue
		}
		if _, err := w.evaluator.OnTransaction(ctx, tx, account); err != nil {
			w.logger.Error("alert rule evaluation failed",
				"account_id", account.ID, "transaction_id", tx.ExternalID, "error", err)
		}
	}
	return inserted, nil
}

func sortTransactions(txs []domain.Transaction) {
	slices.SortStableFunc(txs, func(a, b domain.Transaction) int {
		switch {
		case a.BookingDate.Before(b.BookingDate):
			return -1
		case a.BookingDate.After(b.BookingDate):
			return 1
		default:
			return 0
		}
	})
}

func normalizeTransaction(account domain.Account, tx gocardless.Transaction, status string, now time.Time) (domain.Transaction, error) {
	currency := strings.ToUpper(tx.TransactionAmount.Currency)
	if currency == "" {
		currency = account.Currency
	}
	if currency == "" {
		return domain.Transaction{}, errors.New("missing currency")
	}
	amount, err := money.Parse(tx.TransactionAmount.Value, currency)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("amount %q: %w", tx.TransactionAmount.Value, err)
	}

	booking := parseAPIDate(tx.BookingDate)
	if booking.IsZero() {
		booking = parseAPIDate(tx.ValueDate)
	}
	if booking.IsZero() {
		booking = now
	}
	var valueDate *time.Time
	if v := parseAPIDate(tx.ValueDate); !v.IsZero() {
		valueDate = &v
	}

	counterparty := tx.CreditorName
	if amount >= 0 {
		counterparty = tx.DebtorName
		if counterparty == "" {
			counterparty = tx.CreditorName
		}
	}
	counterparty = strings.TrimSpace(counterparty)

	description := strings.TrimSpace(tx.RemittanceInformationUnstructured)
	if description == "" && len(tx.RemittanceInformationUnstructuredArray) > 0 {
		description = strings.Join(tx.RemittanceInformationUnstructuredArray, " | ")
	}
	if description == "" {
		description = strings.TrimSpace(tx.AdditionalInformation)
	}
	if description == "" {
		description = strings.TrimSpace(tx.ProprietaryBankTransactionCode)
	}

	externalID := strings.TrimSpace(tx.TransactionID)
	if externalID == "" {
		externalID = strings.TrimSpace(tx.InternalTransactionID)
	}
	if externalID == "" {
		externalID = fallbackExternalID(account.ID, status, booking, amount, currency, counterparty, description)
	}

	return domain.Transaction{
		AccountID:    account.ID,
		ExternalID:   externalID,
		AmountMinor:  amount,
		Currency:     currency,
		BookingDate:  booking,
		ValueDate:    valueDate,
		Counterparty: counterparty,
		Description:  description,
		Status:       status,
	}, nil
}

func fallbackExternalID(accountID, status string, booking time.Time, amount int64, currency, counterparty, description string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d|%s|%s|%s",
		accountID, status, booking.UTC().Format(time.RFC3339), amount, currency, counterparty, description)))
	return "sha256:" + hex.EncodeToString(sum[:16])
}

func parseAPIDate(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		gocardless.DateFormat,
		time.RFC3339Nano,
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
	} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func startOfDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
