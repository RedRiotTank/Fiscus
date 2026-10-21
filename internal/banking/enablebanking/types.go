// Package enablebanking implements a read-only client for the Enable Banking
// PSD2 account information API.
package enablebanking

import "time"

// DefaultBaseURL is the production Enable Banking API endpoint.
const DefaultBaseURL = "https://api.enablebanking.com"

// DateFormat is the date-only format used by the transactions endpoint.
const DateFormat = "2006-01-02"

// ASPSP describes a bank supported by Enable Banking.
type ASPSP struct {
	Name                   string   `json:"name"`
	Country                string   `json:"country"`
	Logo                   string   `json:"logo,omitempty"`
	BIC                    string   `json:"bic,omitempty"`
	PSUTypes               []string `json:"psu_types,omitempty"`
	MaximumConsentValidity int64    `json:"maximum_consent_validity,omitempty"`
	Beta                   bool     `json:"beta,omitempty"`
}

// Access describes the consent scope requested from the bank.
type Access struct {
	ValidUntil   string                  `json:"valid_until"`
	Balances     *bool                   `json:"balances,omitempty"`
	Transactions *bool                   `json:"transactions,omitempty"`
	Accounts     []AccountIdentification `json:"accounts,omitempty"`
}

// AccountIdentification identifies an account by IBAN or another scheme.
type AccountIdentification struct {
	IBAN  string                 `json:"iban,omitempty"`
	Other *GenericIdentification `json:"other,omitempty"`
}

// GenericIdentification is a non-IBAN account or party identifier.
type GenericIdentification struct {
	Identification string `json:"identification"`
	SchemeName     string `json:"scheme_name"`
	Issuer         string `json:"issuer,omitempty"`
}

// FinancialInstitution identifies the bank servicing an account.
type FinancialInstitution struct {
	Name string `json:"name,omitempty"`
	BIC  string `json:"bic_fi,omitempty"`
}

// AccountResource is the account metadata returned by the API.
type AccountResource struct {
	UID             string                  `json:"uid"`
	AccountID       AccountIdentification   `json:"account_id"`
	AllAccountIDs   []GenericIdentification `json:"all_account_ids,omitempty"`
	Name            string                  `json:"name,omitempty"`
	Details         string                  `json:"details,omitempty"`
	Product         string                  `json:"product,omitempty"`
	Currency        string                  `json:"currency,omitempty"`
	CashAccountType string                  `json:"cash_account_type,omitempty"`
	AccountServicer FinancialInstitution    `json:"account_servicer,omitempty"`
}

// Amount is a decimal monetary value.
type Amount struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// BalanceResource is a single balance snapshot.
type BalanceResource struct {
	Name               string `json:"name"`
	BalanceAmount      Amount `json:"balance_amount"`
	BalanceType        string `json:"balance_type"`
	ReferenceDate      string `json:"reference_date,omitempty"`
	LastChangeDateTime string `json:"last_change_date_time,omitempty"`
}

// Balances is the balances endpoint response.
type Balances struct {
	Balances []BalanceResource `json:"balances"`
}

// Party identifies the creditor or debtor of a transaction.
type Party struct {
	Name string `json:"name,omitempty"`
}

// Transaction is a single booked or pending transaction.
type Transaction struct {
	EntryReference        string   `json:"entry_reference,omitempty"`
	TransactionID         string   `json:"transaction_id,omitempty"`
	TransactionAmount     Amount   `json:"transaction_amount"`
	Creditor              Party    `json:"creditor,omitempty"`
	Debtor                Party    `json:"debtor,omitempty"`
	CreditDebitIndicator  string   `json:"credit_debit_indicator"`
	Status                string   `json:"status"`
	BookingDate           string   `json:"booking_date,omitempty"`
	ValueDate             string   `json:"value_date,omitempty"`
	TransactionDate       string   `json:"transaction_date,omitempty"`
	RemittanceInformation []string `json:"remittance_information,omitempty"`
	ReferenceNumber       string   `json:"reference_number,omitempty"`
	Note                  string   `json:"note,omitempty"`
	BalanceAfter          *Amount  `json:"balance_after_transaction,omitempty"`
}

// Transactions is one page of the transactions endpoint.
type Transactions struct {
	Transactions    []Transaction `json:"transactions"`
	ContinuationKey string        `json:"continuation_key,omitempty"`
}

// SessionAccount is the minimal account information stored in a session.
type SessionAccount struct {
	UID                  string   `json:"uid"`
	IdentificationHash   string   `json:"identification_hash"`
	IdentificationHashes []string `json:"identification_hashes,omitempty"`
}

// Session is an authorized PSD2 session.
type Session struct {
	SessionID    string           `json:"session_id,omitempty"`
	Status       string           `json:"status,omitempty"`
	Accounts     []string         `json:"accounts"`
	AccountsData []SessionAccount `json:"accounts_data,omitempty"`
	ASPSP        ASPSP            `json:"aspsp"`
	PSUType      string           `json:"psu_type,omitempty"`
	Access       Access           `json:"access"`
	Created      string           `json:"created,omitempty"`
	Authorized   string           `json:"authorized,omitempty"`
	Closed       string           `json:"closed,omitempty"`
}

// AuthorizedSession is the response from creating a session. Unlike Session,
// its accounts include the account metadata selected during authorization.
type AuthorizedSession struct {
	SessionID string            `json:"session_id"`
	Accounts  []AccountResource `json:"accounts"`
	ASPSP     ASPSP             `json:"aspsp"`
	PSUType   string            `json:"psu_type,omitempty"`
	Access    Access            `json:"access"`
}

// Authorization is the result of starting a consent flow.
type Authorization struct {
	URL             string
	AuthorizationID string
	State           string
}

// AuthorizationInput configures a new consent flow.
type AuthorizationInput struct {
	ASPSPName    string
	ASPSPCountry string
	PSUType      string
	RedirectURL  string
	State        string
	Language     string
	ValidUntil   time.Time
}
