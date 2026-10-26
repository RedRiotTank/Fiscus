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
	"github.com/RedRiotTank/Fiscus/internal/banking/ratelimit"
	"github.com/RedRiotTank/Fiscus/internal/categories"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/events"
	"github.com/RedRiotTank/Fiscus/internal/money"
	"github.com/RedRiotTank/Fiscus/internal/storage"
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
	SetConnectionRateLimit(ctx context.Context, id int64, until time.Time, reason string) error
	ClearConnectionRateLimit(ctx context.Context, id int64) error
	UpsertAccount(ctx context.Context, a *domain.Account) error
	UpdateAccountBalance(ctx context.Context, accountID string, balanceMinor int64, balanceType string, at time.Time) error
	SetAccountRateLimit(ctx context.Context, accountID string, until time.Time, reason string) error
	ClearAccountRateLimit(ctx context.Context, accountID string) error
	GetAccount(ctx context.Context, accountID string) (domain.Account, error)
	ListAccountsByConnection(ctx context.Context, connectionID int64) ([]domain.Account, error)
	UpsertTransaction(ctx context.Context, t *domain.Transaction) (inserted bool, promoted bool, err error)
}

// Evaluator receives newly ingested data so alert rules can run inline.
type Evaluator interface {
	OnTransaction(ctx context.Context, tx domain.Transaction, account domain.Account) ([]domain.Alert, error)
	OnBalance(ctx context.Context, account domain.Account) ([]domain.Alert, error)
}

// ConsentEvaluator is an optional extension for rules that inspect connection
// consent validity after a sync.
type ConsentEvaluator interface {
	OnConsentCheck(ctx context.Context) ([]domain.Alert, error)
}

// AnalysisEvaluator is an optional extension for historical analysis rules.
type AnalysisEvaluator interface {
	OnAnalysis(ctx context.Context) ([]domain.Alert, error)
}

// ReconciliationEvaluator is an optional extension for balance integrity
// alerts. It is called only when the discrepancy state changes.
type ReconciliationEvaluator interface {
	OnReconciliation(ctx context.Context, transition domain.ReconciliationTransition) ([]domain.Alert, error)
}

// Reconciler mathematically checks an account's official balance against its
// stored booked transaction history.
type Reconciler interface {
	Reconcile(ctx context.Context, account domain.Account) (domain.ReconciliationTransition, error)
}

// ReceiptRepairer retries pairing unpaired receipts after new transactions
// have been ingested.
type ReceiptRepairer interface {
	RepairUnpaired(ctx context.Context) (int, error)
}

// Categorizer assigns a category to a normalized transaction. A nil
// categorizer leaves transactions in the storage default category.
type Categorizer interface {
	Classify(tx domain.Transaction) string
}

// UncategorizedNotifier sends an interactive notification when a booked
// transaction has no matching category rule.
type UncategorizedNotifier interface {
	NotifyUncategorized(ctx context.Context, tx domain.Transaction) error
}

// PassThroughNeutralizer detects offsetting mirror transfers after ingestion.
type PassThroughNeutralizer interface {
	NeutralizePassThroughs(ctx context.Context, windowDays int) (int, error)
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
	Categorizer         Categorizer
	AccountTypes        map[string]string
}

// DefaultConfig returns production-oriented defaults.
func DefaultConfig() Config {
	return Config{
		SyncInterval:        6 * time.Hour,
		SyncTimeout:         5 * time.Minute,
		InitialLookbackDays: 90,
		OverlapDays:         3,
		SyncOnStart:         true,
		Now:                 time.Now,
	}
}

// Worker synchronizes bank connections on a schedule or on demand.
type Worker struct {
	bank          Bank
	store         Store
	evaluator     Evaluator
	reconciler    Reconciler
	receipts      ReceiptRepairer
	observer      Observer
	uncategorized UncategorizedNotifier
	events        events.Sink
	logger        *slog.Logger
	cfg           Config
	trigger       chan string

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

// SetUncategorizedNotifier installs an optional interactive notification hook.
func (w *Worker) SetUncategorizedNotifier(notifier UncategorizedNotifier) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.uncategorized = notifier
}

// SetReconciler installs the balance integrity watchdog.
func (w *Worker) SetReconciler(reconciler Reconciler) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reconciler = reconciler
}

// SetReceiptRepairer installs the post-sync receipt re-pairing service.
func (w *Worker) SetReceiptRepairer(repairer ReceiptRepairer) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.receipts = repairer
}

// SetEventSink installs an optional outbound event sink for webhooks.
func (w *Worker) SetEventSink(sink events.Sink) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = sink
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
		if ratelimit.Is(err) {
			// The rate-limit handler already emitted a warning with the cooldown
			// time. Background syncs treat this as a pause, not a failure.
			w.logger.Debug("bank sync paused by rate limit", "requisition_id", requisitionID)
			return
		}
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
	w.evaluateConsents(ctx)
	w.evaluateAnalysis(ctx)
	syncErr := errors.Join(errs...)
	if syncErr == nil {
		w.repairReceipts(ctx)
	}
	w.emitSyncCompleted(ctx, "all", "", len(connections), syncErr)
	return syncErr
}

// SyncConnection synchronizes a single requisition.
func (w *Worker) SyncConnection(ctx context.Context, requisitionID string) error {
	connection, err := w.store.GetConnectionByRequisitionID(ctx, requisitionID)
	if err != nil {
		return fmt.Errorf("ingest: load connection %q: %w", requisitionID, err)
	}
	err = w.syncConnection(ctx, connection)
	w.evaluateConsents(ctx)
	w.evaluateAnalysis(ctx)
	if err == nil {
		w.repairReceipts(ctx)
	}
	w.emitSyncCompleted(ctx, "connection", requisitionID, 1, err)
	return err
}

func (w *Worker) emitTransactionCreated(ctx context.Context, tx domain.Transaction) {
	w.mu.Lock()
	sink := w.events
	w.mu.Unlock()
	if sink == nil {
		return
	}
	sink.Emit(ctx, events.TransactionCreated, map[string]any{
		"id": tx.ID, "account_id": tx.AccountID, "external_id": tx.ExternalID,
		"amount_minor": tx.AmountMinor, "currency": tx.Currency,
		"booking_date": tx.BookingDate, "counterparty": tx.Counterparty,
		"description": tx.Description, "category": tx.Category, "status": tx.Status,
	})
}

func (w *Worker) emitSyncCompleted(ctx context.Context, scope, requisitionID string, connections int, err error) {
	w.mu.Lock()
	sink := w.events
	w.mu.Unlock()
	if sink == nil {
		return
	}
	payload := map[string]any{
		"scope": scope, "requisition_id": requisitionID,
		"connections": connections, "success": err == nil,
	}
	if err != nil {
		payload["error"] = err.Error()
	}
	sink.Emit(ctx, events.SyncCompleted, payload)
}

func (w *Worker) notifyUncategorized(ctx context.Context, tx domain.Transaction) {
	w.mu.Lock()
	notifier := w.uncategorized
	w.mu.Unlock()
	if notifier == nil {
		return
	}
	notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := notifier.NotifyUncategorized(notifyCtx, tx); err != nil {
		w.logger.Warn("uncategorized transaction notification failed",
			"account_id", tx.AccountID, "transaction_id", tx.ExternalID, "error", err)
	}
}

func (w *Worker) evaluateConsents(ctx context.Context) {
	evaluator, ok := w.evaluator.(ConsentEvaluator)
	if !ok {
		return
	}
	if _, err := evaluator.OnConsentCheck(ctx); err != nil {
		w.logger.Error("consent expiration rules failed", "error", err)
	}
}

func (w *Worker) evaluateAnalysis(ctx context.Context) {
	evaluator, ok := w.evaluator.(AnalysisEvaluator)
	if !ok {
		return
	}
	if _, err := evaluator.OnAnalysis(ctx); err != nil {
		w.logger.Error("historical analysis rules failed", "error", err)
	}
}

func (w *Worker) repairReceipts(ctx context.Context) {
	w.mu.Lock()
	repairer := w.receipts
	w.mu.Unlock()
	if repairer == nil {
		return
	}
	repaired, err := repairer.RepairUnpaired(ctx)
	if err != nil {
		w.logger.Warn("receipt re-pairing failed", "repaired", repaired, "error", err)
		return
	}
	if repaired > 0 {
		w.logger.Info("receipts re-paired after sync", "count", repaired)
	}
}

func (w *Worker) syncConnection(ctx context.Context, connection domain.Connection) error {
	started := w.cfg.Now()
	var syncErr error
	defer func() {
		w.observeSync(connection.RequisitionID, w.cfg.Now().Sub(started), syncErr)
	}()

	if connection.CoolingDown(w.cfg.Now()) {
		w.logger.Debug("bank connection is cooling down; skipping sync",
			"requisition_id", connection.RequisitionID,
			"until", connection.RateLimitedUntil,
			"reason", connection.LastRateLimitReason)
		syncErr = &ratelimit.Error{
			Reason: connection.LastRateLimitReason,
			Until:  *connection.RateLimitedUntil,
		}
		return syncErr
	}

	if pendingAuthorizationStatus(connection.Status) {
		w.logger.Info("bank authorization is pending; skipping sync",
			"requisition_id", connection.RequisitionID, "status", connection.Status)
		return nil
	}

	if connection.Linked() {
		if limited, ok := w.allAccountsCooling(ctx, connection); ok {
			w.logger.Debug("all bank accounts are cooling down; skipping sync",
				"requisition_id", connection.RequisitionID, "until", limited.Until)
			syncErr = limited
			return limited
		}
	}

	requisition, err := w.bank.GetRequisition(ctx, connection.RequisitionID)
	if err != nil {
		if limited := w.markRateLimited(ctx, connection, "", err); limited != nil {
			syncErr = limited
			return limited
		}
		syncErr = fmt.Errorf("fetch requisition: %w", err)
		w.recordSyncFailure(ctx, connection, syncErr)
		return syncErr
	}

	connection.Status = requisition.Status
	if requisition.Link != "" {
		connection.Link = requisition.Link
	}
	if requisition.AgreementID != "" {
		connection.AgreementID = requisition.AgreementID
	}
	if requisition.ConsentExpiresAt != nil {
		connection.ConsentExpiresAt = requisition.ConsentExpiresAt
	}
	if connection.InstitutionID == "" {
		connection.InstitutionID = requisition.InstitutionID
	}
	if err := w.store.UpsertConnection(ctx, &connection); err != nil {
		syncErr = fmt.Errorf("update requisition status: %w", err)
		w.recordSyncFailure(ctx, connection, syncErr)
		return syncErr
	}

	if !connection.Linked() {
		if pendingConnectionStatus(connection.Status) {
			w.logger.Info("bank connection authorization is pending; skipping account sync",
				"requisition_id", connection.RequisitionID, "status", connection.Status)
			return nil
		}
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
			if ratelimit.Is(err) {
				syncErr = err
				return err
			}
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
	if connection.ID == 0 || ratelimit.Is(err) {
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
	if ratelimit.Is(syncErr) {
		return
	}
	msg := ""
	if syncErr != nil {
		msg = truncate(syncErr.Error(), 1000)
	}
	if err := w.store.UpdateConnectionSync(ctx, connection.ID, w.cfg.Now(), msg); err != nil {
		w.logger.Error("failed to record sync result", "requisition_id", connection.RequisitionID, "error", err)
	}
	if syncErr == nil {
		if err := w.store.ClearConnectionRateLimit(ctx, connection.ID); err != nil {
			w.logger.Error("failed to clear connection rate limit", "requisition_id", connection.RequisitionID, "error", err)
		}
	}
}

func (w *Worker) syncAccount(ctx context.Context, connection domain.Connection, accountID string) error {
	var errs []error
	now := w.cfg.Now()
	balanceUpdated := false

	account, err := w.store.GetAccount(ctx, accountID)
	switch {
	case err == nil:
		if account.CoolingDown(now) {
			w.logger.Debug("bank account is cooling down; skipping sync",
				"account_id", accountID, "until", account.RateLimitedUntil,
				"reason", account.LastRateLimitReason)
			return nil
		}
	case errors.Is(err, storage.ErrNotFound):
		account = domain.Account{ID: accountID, ConnectionID: connection.ID}
	default:
		return fmt.Errorf("load account metadata: %w", err)
	}

	if hint, ok := w.cfg.AccountTypes[accountID]; ok && hint != "" && !account.TypeManual {
		normalized := domain.NormalizeAccountType(hint)
		if account.Type != normalized {
			account.Type = normalized
			if persistErr := w.store.UpsertAccount(ctx, &account); persistErr != nil {
				return fmt.Errorf("persist account workspace type: %w", persistErr)
			}
		}
	}

	// Account details are static. Only fetch them when SQLite has no cached
	// metadata, so routine syncs spend the PSD2 quota on balances and
	// transactions instead of repeating /details.
	if !metadataCached(account) {
		details, detailsErr := w.bank.GetAccountDetails(ctx, accountID)
		if detailsErr != nil {
			if limited := w.markRateLimited(ctx, connection, accountID, detailsErr); limited != nil {
				return limited
			}
			return fmt.Errorf("account details: %w", detailsErr)
		}
		account = domain.Account{
			ID:           accountID,
			ConnectionID: connection.ID,
			IBAN:         details.IBAN,
			Name:         details.Name,
			OwnerName:    details.OwnerName,
			Currency:     strings.ToUpper(details.Currency),
			Type:         account.Type,
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
		if upsertErr := w.store.UpsertAccount(ctx, &account); upsertErr != nil {
			return fmt.Errorf("persist account: %w", upsertErr)
		}
	}

	balances, balanceErr := w.bank.GetBalances(ctx, accountID)
	if balanceErr != nil {
		if limited := w.markRateLimited(ctx, connection, accountID, balanceErr); limited != nil {
			return limited
		}
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
				balanceUpdated = true
				w.observeBalance(account)
				errs = append(errs, w.evaluateBalance(ctx, accountID))
			}
		}
	}

	inserted, err := w.syncTransactions(ctx, account, connection)
	if err != nil {
		if limited := w.markRateLimited(ctx, connection, accountID, err); limited != nil {
			return limited
		}
		errs = append(errs, err)
	}
	if inserted > 0 {
		w.observeTransactions(accountID, inserted)
	}
	if balanceUpdated {
		errs = append(errs, w.evaluateReconciliation(ctx, account))
	}
	result := errors.Join(errs...)
	if result == nil {
		if clearErr := w.store.ClearAccountRateLimit(ctx, accountID); clearErr != nil {
			w.logger.Error("failed to clear account rate limit", "account_id", accountID, "error", clearErr)
		}
	}
	return result
}

func metadataCached(account domain.Account) bool {
	return strings.TrimSpace(account.Name) != "" && strings.TrimSpace(account.Currency) != ""
}

// allAccountsCooling reports whether every known account for a connection is
// still inside its rate-limit cooldown.
func (w *Worker) allAccountsCooling(ctx context.Context, connection domain.Connection) (*ratelimit.Error, bool) {
	if connection.ID == 0 {
		return nil, false
	}
	accounts, err := w.store.ListAccountsByConnection(ctx, connection.ID)
	if err != nil || len(accounts) == 0 {
		return nil, false
	}
	now := w.cfg.Now()
	var until time.Time
	reason := ""
	for _, account := range accounts {
		if !account.CoolingDown(now) {
			return nil, false
		}
		if until.IsZero() || account.RateLimitedUntil.Before(until) {
			until = *account.RateLimitedUntil
			reason = account.LastRateLimitReason
		}
	}
	if until.IsZero() {
		return nil, false
	}
	return &ratelimit.Error{Reason: reason, Until: until}, true
}

// markRateLimited persists a cooldown for the affected connection and, when it
// already exists locally, the account. It returns nil when err is not a
// provider rate limit.
func (w *Worker) markRateLimited(ctx context.Context, connection domain.Connection, accountID string, cause error) *ratelimit.Error {
	reason, ok := ratelimit.Detect(cause)
	if !ok {
		return nil
	}
	limited := ratelimit.NewError(cause, w.cfg.Now())
	limited.Reason = reason

	updateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	if connection.ID != 0 {
		if err := w.store.SetConnectionRateLimit(updateCtx, connection.ID, limited.Until, limited.Reason); err != nil {
			w.logger.Error("failed to persist connection rate limit",
				"requisition_id", connection.RequisitionID, "error", err)
		}
	}
	if accountID != "" {
		if err := w.store.SetAccountRateLimit(updateCtx, accountID, limited.Until, limited.Reason); err != nil && !errors.Is(err, storage.ErrNotFound) {
			w.logger.Error("failed to persist account rate limit",
				"account_id", accountID, "error", err)
		}
	}

	w.logger.Warn("bank rate limit reached; pausing sync until cooldown",
		"requisition_id", connection.RequisitionID,
		"account_id", accountID,
		"reason", limited.Reason,
		"next_sync_at", limited.Until,
	)
	return limited
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

func (w *Worker) evaluateReconciliation(ctx context.Context, account domain.Account) error {
	w.mu.Lock()
	reconciler := w.reconciler
	w.mu.Unlock()
	if reconciler == nil {
		return nil
	}
	transition, err := reconciler.Reconcile(ctx, account)
	if err != nil {
		return fmt.Errorf("reconcile account: %w", err)
	}
	if !transition.Changed {
		return nil
	}
	evaluator, ok := w.evaluator.(ReconciliationEvaluator)
	if !ok {
		return nil
	}
	if _, err := evaluator.OnReconciliation(ctx, transition); err != nil {
		return fmt.Errorf("evaluate reconciliation rules: %w", err)
	}
	return nil
}

// selectBalance prefers booked closing balances because reconciliation compares
// the official balance with the booked transaction ledger. GoCardless commonly
// returns both closingBooked and interimAvailable; only fall back to available
// balances when no booked balance is exposed.
func selectBalance(balances []gocardless.Balance) (gocardless.Balance, bool) {
	preference := []string{
		"closingBooked", "interimBooked", "openingBooked",
		"interimAvailable", "closingAvailable", "expected", "forwardAvailable",
		"openingAvailable", "authorised", "available",
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
		w.categorize(&normalized)
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
		w.categorize(&normalized)
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
			w.emitTransactionCreated(ctx, tx)
		}
		if tx.Status == domain.TransactionStatusBooked && strings.TrimSpace(tx.Category) == categories.DefaultCategory {
			w.notifyUncategorized(ctx, tx)
		}
		if w.evaluator == nil || tx.Status != domain.TransactionStatusBooked {
			continue
		}
		if _, err := w.evaluator.OnTransaction(ctx, tx, account); err != nil {
			w.logger.Error("alert rule evaluation failed",
				"account_id", account.ID, "transaction_id", tx.ExternalID, "error", err)
		}
	}
	if neutralizer, ok := w.store.(PassThroughNeutralizer); ok {
		if paired, err := neutralizer.NeutralizePassThroughs(ctx, 3); err != nil {
			w.logger.Error("pass-through neutralization failed", "account_id", account.ID, "error", err)
		} else if paired > 0 {
			w.logger.Info("neutralized pass-through transfers", "account_id", account.ID, "pairs", paired)
		}
	}
	return inserted, nil
}

func (w *Worker) categorize(tx *domain.Transaction) {
	if w.cfg.Categorizer == nil {
		return
	}
	if category := strings.TrimSpace(w.cfg.Categorizer.Classify(*tx)); category != "" {
		tx.Category = category
	}
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

// pendingConnectionStatus reports statuses that are waiting for user action and
// should not be treated as sync failures.
func pendingConnectionStatus(status string) bool {
	switch status {
	case "CR", "GC", "UA", "GA", "PENDING_AUTHORIZATION", "RETURNED_FROM_BANK":
		return true
	default:
		return false
	}
}

// pendingAuthorizationStatus reports a consent that has not produced a provider
// session yet. Such connections must not be sent to the provider API.
func pendingAuthorizationStatus(status string) bool {
	return status == "PENDING_AUTHORIZATION"
}
