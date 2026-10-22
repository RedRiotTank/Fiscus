package web

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

// PageData is the common view model for all templates.
type PageData struct {
	Title             string
	Active            string
	Version           string
	Now               time.Time
	Summary           []CurrencySummary
	CategorySpending  []CategorySpendingView
	SpendingMonth     string
	Accounts          []domain.Account
	Transactions      []TransactionView
	Alerts            []AlertView
	Connections       []ConnectionView
	TotalAccounts     int
	TotalTransactions int
	FilterAccount     string
	Page              int
	PrevPage          int
	NextPage          int
	HasNext           bool
	ErrorMessage      string
	AuthStatus        string
}

// CurrencySummary aggregates balances for one currency.
type CurrencySummary struct {
	Currency string
	Minor    int64
	Accounts int
}

// CategorySpendingView is a category total rendered on the overview.
type CategorySpendingView struct {
	Category string
	Currency string
	Minor    int64
}

// TransactionView augments a transaction with display metadata.
type TransactionView struct {
	domain.Transaction
	AccountName string
}

// AlertView augments an alert with display metadata.
type AlertView struct {
	domain.Alert
	AccountName string
}

// ConnectionView augments a connection with its accounts.
type ConnectionView struct {
	domain.Connection
	Accounts []domain.Account
}

func summarizeAccounts(accounts []domain.Account) []CurrencySummary {
	type aggregate struct {
		minor    int64
		accounts int
	}
	byCurrency := make(map[string]aggregate)
	for _, account := range accounts {
		currency := strings.ToUpper(account.Currency)
		if currency == "" {
			currency = "EUR"
		}
		current := byCurrency[currency]
		current.minor += account.BalanceMinor
		current.accounts++
		byCurrency[currency] = current
	}
	currencies := make([]string, 0, len(byCurrency))
	for currency := range byCurrency {
		currencies = append(currencies, currency)
	}
	sort.Strings(currencies)
	out := make([]CurrencySummary, 0, len(currencies))
	for _, currency := range currencies {
		agg := byCurrency[currency]
		out = append(out, CurrencySummary{Currency: currency, Minor: agg.minor, Accounts: agg.accounts})
	}
	return out
}

func transactionViews(transactions []domain.Transaction, accounts map[string]domain.Account) []TransactionView {
	out := make([]TransactionView, 0, len(transactions))
	for _, tx := range transactions {
		name := tx.AccountID
		if account, ok := accounts[tx.AccountID]; ok {
			if account.Name != "" {
				name = account.Name
			} else if account.IBAN != "" {
				name = account.IBAN
			}
		}
		out = append(out, TransactionView{Transaction: tx, AccountName: name})
	}
	return out
}

func alertViews(alerts []domain.Alert, accounts map[string]domain.Account) []AlertView {
	out := make([]AlertView, 0, len(alerts))
	for _, alert := range alerts {
		name := alert.AccountID
		if account, ok := accounts[alert.AccountID]; ok {
			if account.Name != "" {
				name = account.Name
			} else if account.IBAN != "" {
				name = account.IBAN
			}
		}
		out = append(out, AlertView{Alert: alert, AccountName: name})
	}
	return out
}

func accountMap(accounts []domain.Account) map[string]domain.Account {
	out := make(map[string]domain.Account, len(accounts))
	for _, account := range accounts {
		out[account.ID] = account
	}
	return out
}

func connectionViews(connections []domain.Connection, accounts []domain.Account) []ConnectionView {
	byConnection := make(map[int64][]domain.Account)
	for _, account := range accounts {
		byConnection[account.ConnectionID] = append(byConnection[account.ConnectionID], account)
	}
	out := make([]ConnectionView, 0, len(connections))
	for _, connection := range connections {
		out = append(out, ConnectionView{
			Connection: connection,
			Accounts:   byConnection[connection.ID],
		})
	}
	return out
}

func (s *Server) loadOverview(ctx context.Context, data *PageData) error {
	accounts, err := s.store.ListAccounts(ctx)
	if err != nil {
		return err
	}
	data.Accounts = accounts
	data.Summary = summarizeAccounts(accounts)
	data.TotalAccounts = len(accounts)
	now := s.now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	data.SpendingMonth = monthStart.Format("January 2006")
	spending, err := s.store.SpendingByCategory(ctx, monthStart, monthStart.AddDate(0, 1, 0))
	if err != nil {
		return err
	}
	for _, item := range spending {
		data.CategorySpending = append(data.CategorySpending, CategorySpendingView{
			Category: item.Category, Currency: item.Currency, Minor: item.Minor,
		})
	}
	total, err := s.store.CountTransactions(ctx)
	if err != nil {
		return err
	}
	data.TotalTransactions = total
	return nil
}

func (s *Server) loadConnections(ctx context.Context, data *PageData) error {
	connections, err := s.store.ListConnections(ctx)
	if err != nil {
		return err
	}
	accounts, err := s.store.ListAccounts(ctx)
	if err != nil {
		return err
	}
	data.Connections = connectionViews(connections, accounts)
	return nil
}

func (s *Server) loadAlerts(ctx context.Context, data *PageData, limit int) error {
	alerts, err := s.store.ListAlerts(ctx, limit)
	if err != nil {
		return err
	}
	accounts, err := s.store.ListAccounts(ctx)
	if err != nil {
		return err
	}
	data.Alerts = alertViews(alerts, accountMap(accounts))
	return nil
}

// transactionFilter builds a storage filter while keeping pagination bounded.
func transactionFilter(accountID string, page int) storage.TransactionFilter {
	const pageSize = 50
	if page < 0 {
		page = 0
	}
	return storage.TransactionFilter{
		AccountID: accountID,
		Limit:     pageSize,
		Offset:    page * pageSize,
	}
}
