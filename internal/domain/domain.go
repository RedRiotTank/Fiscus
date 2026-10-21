// Package domain contains the core entities used across Fiscus.
//
// Monetary values are always represented as integer minor units (for example,
// euro cents). Floating point is deliberately never used for money.
package domain

import "time"

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

// Connection is a GoCardless requisition (one bank consent) tracked locally.
type Connection struct {
	ID              int64
	RequisitionID   string
	InstitutionID   string
	InstitutionName string
	Status          string
	Link            string
	Reference       string
	AgreementID     string
	LastError       string
	LastSyncedAt    *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Linked reports whether the requisition can provide account data.
func (c Connection) Linked() bool {
	return c.Status == "LN" || c.Status == "SA"
}

// Account is a bank account exposed by GoCardless.
type Account struct {
	ID               string
	ConnectionID     int64
	IBAN             string
	Name             string
	OwnerName        string
	Currency         string
	BalanceMinor     int64
	BalanceType      string
	BalanceUpdatedAt *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
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
	Status       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// IsDebit reports whether the transaction removes money from the account.
func (t Transaction) IsDebit() bool {
	return t.AmountMinor < 0
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
