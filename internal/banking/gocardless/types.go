// Package gocardless implements a read-only client for the GoCardless Bank
// Account Data API (PSD2 account information services).
package gocardless

import "time"

// Token is the response of the token endpoints.
type Token struct {
	Access         string `json:"access"`
	Refresh        string `json:"refresh"`
	AccessExpires  int64  `json:"access_expires"`
	RefreshExpires int64  `json:"refresh_expires"`
}

// Requisition represents a bank consent / account selection.
type Requisition struct {
	ID                string     `json:"id"`
	Created           string     `json:"created"`
	Redirect          string     `json:"redirect"`
	Status            string     `json:"status"`
	InstitutionID     string     `json:"institution_id"`
	AgreementID       string     `json:"agreement"`
	Reference         string     `json:"reference"`
	Accounts          []string   `json:"accounts"`
	Link              string     `json:"link"`
	SSN               string     `json:"ssn"`
	AccountSelection  bool       `json:"account_selection"`
	RedirectImmediate bool       `json:"redirect_immediate"`
	ConsentExpiresAt  *time.Time `json:"-"`
}

// Institution describes a supported bank.
type Institution struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	BIC                  string   `json:"bic"`
	TransactionTotalDays string   `json:"transaction_total_days"`
	Countries            []string `json:"countries"`
	Logo                 string   `json:"logo"`
}

// Amount is a decimal amount as returned by GoCardless.
type Amount struct {
	Value    string `json:"amount"`
	Currency string `json:"currency"`
}

// Balance is a single balance snapshot.
type Balance struct {
	BalanceAmount      Amount `json:"balanceAmount"`
	BalanceType        string `json:"balanceType"`
	ReferenceDate      string `json:"referenceDate"`
	LastChangeDateTime string `json:"lastChangeDateTime"`
}

// Balances is the balances endpoint response.
type Balances struct {
	Balances []Balance `json:"balances"`
}

// AccountDetails is the account metadata returned by the details endpoint.
type AccountDetails struct {
	ResourceID      string `json:"resourceId"`
	IBAN            string `json:"iban"`
	BBAN            string `json:"bban"`
	Currency        string `json:"currency"`
	OwnerName       string `json:"ownerName"`
	Name            string `json:"name"`
	Product         string `json:"product"`
	CashAccountType string `json:"cashAccountType"`
	Status          string `json:"status"`
}

type accountDetailsEnvelope struct {
	Account AccountDetails `json:"account"`
}

// Transaction is a booked or pending transaction from the API.
type Transaction struct {
	TransactionID         string `json:"transactionId"`
	InternalTransactionID string `json:"internalTransactionId"`
	BookingDate           string `json:"bookingDate"`
	ValueDate             string `json:"valueDate"`
	TransactionAmount     Amount `json:"transactionAmount"`
	CreditorName          string `json:"creditorName"`
	CreditorAccount       *struct {
		IBAN string `json:"iban"`
	} `json:"creditorAccount"`
	DebtorName    string `json:"debtorName"`
	DebtorAccount *struct {
		IBAN string `json:"iban"`
	} `json:"debtorAccount"`
	RemittanceInformationUnstructured      string   `json:"remittanceInformationUnstructured"`
	RemittanceInformationUnstructuredArray []string `json:"remittanceInformationUnstructuredArray"`
	AdditionalInformation                  string   `json:"additionalInformation"`
	BankTransactionCode                    string   `json:"bankTransactionCode"`
	ProprietaryBankTransactionCode         string   `json:"proprietaryBankTransactionCode"`
}

// Transactions is the transactions endpoint response, split by status.
type Transactions struct {
	Booked  []Transaction `json:"booked"`
	Pending []Transaction `json:"pending"`
}

type transactionsEnvelope struct {
	Transactions Transactions `json:"transactions"`
}

// CreateRequisitionInput describes a new consent request.
type CreateRequisitionInput struct {
	InstitutionID     string `json:"institution_id"`
	Redirect          string `json:"redirect"`
	Reference         string `json:"reference,omitempty"`
	UserLanguage      string `json:"user_language,omitempty"`
	AccountSelection  bool   `json:"account_selection,omitempty"`
	RedirectImmediate bool   `json:"redirect_immediate,omitempty"`
}

// DateFormat is the date-only format expected by the transactions endpoint.
const DateFormat = "2006-01-02"
