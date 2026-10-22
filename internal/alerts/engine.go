package alerts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// AlertStore persists alerts and their delivery state.
type AlertStore interface {
	CreateAlert(ctx context.Context, alert *domain.Alert) (bool, error)
	MarkAlertDelivered(ctx context.Context, id int64, at time.Time) error
	MarkAlertDeliveryFailed(ctx context.Context, id int64, deliveryErr string) error
}

// AlertDispatcher delivers a persisted alert to external channels.
type AlertDispatcher interface {
	Dispatch(ctx context.Context, alert domain.Alert) error
}

// EngineObserver receives rule and delivery telemetry.
type EngineObserver interface {
	ObserveAlert(alert domain.Alert)
	ObserveDelivery(alert domain.Alert, err error)
}

// EngineConfig configures an Engine.
type EngineConfig struct {
	Store         AlertStore
	Dispatcher    AlertDispatcher
	Logger        *slog.Logger
	Now           func() time.Time
	TxRules       []TransactionRule
	BalanceRules  []BalanceRule
	ConsentRules  []ConsentRule
	AnalysisRules []AnalysisRule
	Observer      EngineObserver
}

// Engine runs transaction and balance rules, persists de-duplicated alerts, and
// dispatches them.
type Engine struct {
	store         AlertStore
	dispatcher    AlertDispatcher
	logger        *slog.Logger
	now           func() time.Time
	txRules       []TransactionRule
	balanceRules  []BalanceRule
	consentRules  []ConsentRule
	analysisRules []AnalysisRule
	observer      EngineObserver
}

// NewEngine validates the configuration and returns an Engine.
func NewEngine(cfg EngineConfig) (*Engine, error) {
	if cfg.Store == nil {
		return nil, errors.New("alerts: alert store is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Engine{
		store:         cfg.Store,
		dispatcher:    cfg.Dispatcher,
		logger:        cfg.Logger,
		now:           cfg.Now,
		txRules:       cfg.TxRules,
		balanceRules:  cfg.BalanceRules,
		consentRules:  cfg.ConsentRules,
		analysisRules: cfg.AnalysisRules,
		observer:      cfg.Observer,
	}, nil
}

// OnTransaction implements ingest.Evaluator.
func (e *Engine) OnTransaction(ctx context.Context, tx domain.Transaction, account domain.Account) ([]domain.Alert, error) {
	var (
		produced []domain.Alert
		errs     []error
	)
	for _, rule := range e.txRules {
		alerts, err := rule.Evaluate(ctx, account, tx)
		if err != nil {
			errs = append(errs, fmt.Errorf("rule %s: %w", rule.Name(), err))
			continue
		}
		for i := range alerts {
			alert := alerts[i]
			if alert.AccountID == "" {
				alert.AccountID = account.ID
			}
			if alert.TransactionID == nil && tx.ID != 0 {
				id := tx.ID
				alert.TransactionID = &id
			}
			produced = append(produced, alert)
			if err := e.persistAndDispatch(ctx, &produced[len(produced)-1]); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return produced, errors.Join(errs...)
}

// OnBalance implements ingest.Evaluator.
func (e *Engine) OnBalance(ctx context.Context, account domain.Account) ([]domain.Alert, error) {
	var (
		produced []domain.Alert
		errs     []error
	)
	for _, rule := range e.balanceRules {
		alerts, err := rule.Evaluate(ctx, account)
		if err != nil {
			errs = append(errs, fmt.Errorf("rule %s: %w", rule.Name(), err))
			continue
		}
		for i := range alerts {
			alert := alerts[i]
			if alert.AccountID == "" {
				alert.AccountID = account.ID
			}
			produced = append(produced, alert)
			if err := e.persistAndDispatch(ctx, &produced[len(produced)-1]); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return produced, errors.Join(errs...)
}

// OnConsentCheck implements ingest.ConsentEvaluator.
func (e *Engine) OnConsentCheck(ctx context.Context) ([]domain.Alert, error) {
	var (
		produced []domain.Alert
		errs     []error
	)
	for _, rule := range e.consentRules {
		alerts, err := rule.Evaluate(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("rule %s: %w", rule.Name(), err))
			continue
		}
		for i := range alerts {
			alert := alerts[i]
			produced = append(produced, alert)
			if err := e.persistAndDispatch(ctx, &produced[len(produced)-1]); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return produced, errors.Join(errs...)
}

// OnAnalysis implements ingest.AnalysisEvaluator.
func (e *Engine) OnAnalysis(ctx context.Context) ([]domain.Alert, error) {
	var (
		produced []domain.Alert
		errs     []error
	)
	for _, rule := range e.analysisRules {
		alerts, err := rule.Evaluate(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("rule %s: %w", rule.Name(), err))
			continue
		}
		for i := range alerts {
			alert := alerts[i]
			produced = append(produced, alert)
			if err := e.persistAndDispatch(ctx, &produced[len(produced)-1]); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return produced, errors.Join(errs...)
}

func (e *Engine) persistAndDispatch(ctx context.Context, alert *domain.Alert) error {
	if alert.DedupKey == "" {
		return errors.New("alerts: rule produced an alert without a dedup key")
	}
	if alert.CreatedAt.IsZero() {
		alert.CreatedAt = e.now().UTC()
	}
	inserted, err := e.store.CreateAlert(ctx, alert)
	if err != nil {
		return fmt.Errorf("persist alert: %w", err)
	}
	if !inserted {
		return nil
	}
	if e.observer != nil {
		e.observer.ObserveAlert(*alert)
	}
	if e.dispatcher == nil {
		return nil
	}

	dispatchErr := e.dispatcher.Dispatch(ctx, *alert)
	if e.observer != nil {
		e.observer.ObserveDelivery(*alert, dispatchErr)
	}
	if dispatchErr != nil {
		e.logger.Error("alert delivery failed", "alert_id", alert.ID, "rule", alert.Rule, "error", dispatchErr)
		if markErr := e.store.MarkAlertDeliveryFailed(ctx, alert.ID, truncate(dispatchErr.Error(), 1000)); markErr != nil {
			return errors.Join(dispatchErr, fmt.Errorf("record delivery failure: %w", markErr))
		}
		return dispatchErr
	}
	if err := e.store.MarkAlertDelivered(ctx, alert.ID, e.now().UTC()); err != nil {
		return fmt.Errorf("record delivery: %w", err)
	}
	delivered := e.now().UTC()
	alert.DeliveredAt = &delivered
	return nil
}
