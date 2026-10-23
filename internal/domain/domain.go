// Package domain contains the core entities used across Fiscus.
//
// Monetary values are always represented as integer minor units (for example,
// euro cents). Floating point is deliberately never used for money.
package domain

import (
	"strings"
	"time"
)

// Severity describes how urgent an alert is.
type Severity string

// Severity levels classify alerts for display and notification routing.
const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Transaction statuses used by the ingestion layer.
const (
	TransactionStatusBooked  = "booked"
	TransactionStatusPending = "pending"
)

// Workspace profiles separate personal and business treasury views.
const (
	// WorkspacePersonal shows only personal finance workflows.
	WorkspacePersonal = "personal"
	// WorkspaceBusiness shows only business and freelance workflows.
	WorkspaceBusiness = "business"
	// WorkspaceHybrid enables both workspaces with a switcher.
	WorkspaceHybrid = "hybrid"
	// WorkspaceAll is the unified hybrid view.
	WorkspaceAll = "all"
)

// AccountType classifies an account as personal or business.
const (
	AccountTypePersonal = "personal"
	AccountTypeBusiness = "business"
)

// NormalizeWorkspaceProfile returns a valid configured profile, defaulting to
// hybrid for unknown values.
func NormalizeWorkspaceProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case WorkspacePersonal:
		return WorkspacePersonal
	case WorkspaceBusiness:
		return WorkspaceBusiness
	default:
		return WorkspaceHybrid
	}
}

// NormalizeAccountType returns a valid account type, defaulting to personal.
func NormalizeAccountType(accountType string) string {
	if strings.EqualFold(strings.TrimSpace(accountType), AccountTypeBusiness) {
		return AccountTypeBusiness
	}
	return AccountTypePersonal
}

// ValidWorkspaceView reports whether value is a selectable hybrid view.
func ValidWorkspaceView(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case WorkspacePersonal, WorkspaceBusiness, WorkspaceAll:
		return true
	default:
		return false
	}
}

// Connection is a GoCardless requisition (one bank consent) tracked locally.
type Connection struct {
	ID                  int64
	RequisitionID       string
	InstitutionID       string
	InstitutionName     string
	Status              string
	Link                string
	Reference           string
	AgreementID         string
	LastError           string
	ConsentExpiresAt    *time.Time
	LastSyncedAt        *time.Time
	RateLimitedUntil    *time.Time
	LastRateLimitReason string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// Linked reports whether the requisition can provide account data.
func (c Connection) Linked() bool {
	return c.Status == "LN" || c.Status == "SA"
}

// CoolingDown reports whether the PSD2 rate-limit cooldown is still active.
func (c Connection) CoolingDown(now time.Time) bool {
	return coolingDown(c.RateLimitedUntil, now)
}

// Account is a bank account exposed by GoCardless.
type Account struct {
	ID                  string
	ConnectionID        int64
	IBAN                string
	Name                string
	OwnerName           string
	Currency            string
	Type                string
	TypeManual          bool
	BalanceMinor        int64
	BalanceType         string
	BalanceUpdatedAt    *time.Time
	RateLimitedUntil    *time.Time
	LastRateLimitReason string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// CoolingDown reports whether the PSD2 rate-limit cooldown is still active.
func (a Account) CoolingDown(now time.Time) bool {
	return coolingDown(a.RateLimitedUntil, now)
}

func coolingDown(until *time.Time, now time.Time) bool {
	return until != nil && now.UTC().Before(until.UTC())
}

// Transaction is a single booked or pending bank transaction.
type Transaction struct {
	ID           int64
	AccountID    string
	ExternalID   string
	AmountMinor  int64
	Currency     string
	BookingDate  time.Time
	ValueDate    *time.Time
	Counterparty string
	Description  string
	Category     string
	Notes        string
	Status       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// IsDebit reports whether the transaction removes money from the account.
func (t Transaction) IsDebit() bool {
	return t.AmountMinor < 0
}

// Attachment is a receipt or invoice stored by Fiscus and optionally paired
// with a transaction.
type Attachment struct {
	ID                   int64
	TransactionID        *int64
	Filename             string
	FilePath             string
	ContentType          string
	SizeBytes            int64
	SHA256               string
	UploadedAt           time.Time
	Source               string
	ExtractedDate        *time.Time
	ExtractedAmountMinor *int64
	ExtractedCurrency    string
}

// Paired reports whether the attachment is linked to a transaction.
func (a Attachment) Paired() bool {
	return a.TransactionID != nil
}

// Reconciliation statuses used by the balance integrity watchdog.
const (
	ReconciliationStatusReconciled  = "reconciled"
	ReconciliationStatusDiscrepancy = "discrepancy"
)

// Reconciliation stores the mathematical anchor and latest watchdog state for
// one account.
type Reconciliation struct {
	AccountID           string
	Currency            string
	OpeningBalanceMinor int64
	AnchorAt            time.Time
	LastCheckedAt       *time.Time
	LastDeltaMinor      int64
	Status              string
	DiscrepancyStart    *time.Time
	DiscrepancyEnd      *time.Time
	// BaselineEstablished is set after the first reconciliation pass that saw
	// booked transaction history. The next pass may then raise discrepancies.
	BaselineEstablished bool
	UpdatedAt           time.Time
}

// Balanced reports whether the official balance matches stored transactions.
func (r Reconciliation) Balanced() bool {
	return r.Status == ReconciliationStatusReconciled
}

// ReconciliationTransition describes the outcome of one reconciliation pass.
// Changed is true only when the discrepancy state or amount changed.
type ReconciliationTransition struct {
	Reconciliation     Reconciliation
	Changed            bool
	PreviousStatus     string
	PreviousDeltaMinor int64
}

// Alert is a persisted notification produced by a rule.
type Alert struct {
	ID            int64
	Rule          string
	Severity      Severity
	Title         string
	Message       string
	AccountID     string
	TransactionID *int64
	DedupKey      string
	CreatedAt     time.Time
	DeliveredAt   *time.Time
	DeliveryError string
}

// Delivered reports whether at least one channel accepted the alert.
func (a Alert) Delivered() bool {
	return a.DeliveredAt != nil
}
