package web

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/analysis"
	"github.com/RedRiotTank/Fiscus/internal/budgets"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
	"github.com/RedRiotTank/Fiscus/internal/treasury"
	"github.com/RedRiotTank/Fiscus/pkg/housing"
	"github.com/RedRiotTank/Fiscus/pkg/recurring"
)

// PageData is the common view model for all templates.
type PageData struct {
	Title                   string
	Active                  string
	Version                 string
	Now                     time.Time
	Summary                 []CurrencySummary
	CategorySpending        []CategorySpendingView
	SpendingMonth           string
	BudgetPlan              *BudgetPlanView
	BudgetEmpty             bool
	BudgetWorkspaceRequired bool
	BudgetWorkspace         string
	BudgetCurrency          string
	BudgetMonth             time.Time
	BudgetMonthValue        string
	BudgetMonthLabel        string
	BudgetPrevHref          string
	BudgetNextHref          string
	BudgetCurrentHref       string
	BudgetIsCurrentMonth    bool
	TransactionMonth        string
	TransactionMonthLabel   string
	TransactionMonthCurrent bool
	FilterCategory          string
	Subscriptions           []SubscriptionView
	ActiveSubscriptions     []SubscriptionView
	InactiveSubscriptions   []SubscriptionView
	Fees                    []FeeView
	MonthlyCosts            []CurrencyAmountView
	AnnualCosts             []CurrencyAmountView
	FeeTotals               []CurrencyAmountView
	Treasury                []RunwayView
	Accounts                []domain.Account
	AccruingAccounts        []AccruingAccountView
	Transactions            []TransactionView
	Categories              []string
	Housing                 *HousingSetupView
	Recurring               *RecurringBillsView
	FlowFacet               string
	CategoryFacet           string
	FlowFilters             []FacetView
	CategoryFilters         []FacetView
	FixedBills              []FixedBillView
	Alerts                  []AlertView
	Connections             []ConnectionView
	Reconciliations         map[string]domain.Reconciliation
	UnpairedAttachments     []domain.Attachment
	AttachmentIDs           map[int64]int64
	AttachmentsEnabled      bool
	AttachmentNotice        string
	TaxEnabled              bool
	TaxShields              []TaxShieldView
	ReconciliationTotal     int
	ReconciledAccounts      int
	DiscrepancyAccounts     int
	TotalAccounts           int
	TotalTransactions       int
	Workspace               WorkspaceState
	Path                    string
	FilterAccount           string
	SearchQuery             string
	Page                    int
	Limit                   int
	PrevPage                int
	NextPage                int
	HasNext                 bool
	ErrorMessage            string
	AuthStatus              string
}

// CurrencySummary aggregates balances for one currency.
type CurrencySummary struct {
	Currency string
	Minor    int64
	Accounts int
}

// AccruingAccountView augments a manual savings account with projected yield.
type AccruingAccountView struct {
	domain.AccruingAccount
	BalanceMinor          int64
	LifetimeInterestMinor int64
	MonthInterestMinor    int64
	TAELabel              string
}

// AccruingAccountFormView supplies the setup and reconcile modal.
type AccruingAccountFormView struct {
	ID                    string
	Workspace             string
	Name                  string
	Currency              string
	AnchorBalance         string
	AnchorDate            string
	APYPercent            string
	TransferMatchKeyword  string
	TaxWithholdingPercent string
	CompoundingFrequency  string
	Error                 string
	Editing               bool
}

// HousingSetupView supplies the budget-page housing prompt, detection banner,
// and modal state.
type HousingSetupView struct {
	Workspace  string
	Currency   string
	Rule       *domain.HousingRule
	Detection  housing.Detection
	MasterPaid bool
}

// HousingFlatmateView is one editable flatmate contribution.
type HousingFlatmateView struct {
	Counterparty string
	AmountMinor  int64
	Detected     bool
	Selected     bool
}

// HousingEditorView is the interactive shared-rent modal model.
type HousingEditorView struct {
	Workspace             string
	Currency              string
	Landlord              string
	MasterAmountMinor     int64
	PersonalNetShareMinor int64
	CalculationMode       string
	Flatmates             []HousingFlatmateView
	Detected              bool
	Error                 string
}

// RecurringBillsView supplies recurring-bill prompts, confirmed rules, and the
// split wizard state.
type RecurringBillsView struct {
	Workspace  string
	Currency   string
	Candidates []recurring.Candidate
	Rules      []domain.RecurringBillRule
}

// RecurringCategoryOption is one budget category grouped by pillar.
type RecurringCategoryOption struct {
	Name   string `json:"name"`
	Pillar string `json:"pillar"`
}

// RecurringBillEditorView is the interactive recurring split modal model.
type RecurringBillEditorView struct {
	Workspace          string
	Currency           string
	Counterparty       string
	Category           string
	Pillar             string
	Recurrence         string
	SplitMode          string
	SplitPeople        int
	GrossMinor         int64
	PersonalShareMinor int64
	Occurrences        int
	TypicalDay         int
	Categories         []RecurringCategoryOption
	CategoryOptions    string
	Detected           bool
	Error              string
}

// FacetView is one instant transaction-history facet button.
type FacetView struct {
	Label  string
	Value  string
	Href   string
	Active bool
}

// FixedBillView is one confirmed or detected household bill on the
// subscriptions page.
type FixedBillView struct {
	Workspace          string
	Currency           string
	Counterparty       string
	Category           string
	Recurrence         string
	GrossMinor         int64
	PersonalShareMinor int64
	Occurrences        int
	LastSeen           time.Time
	Confirmed          bool
	SplitMode          string
	SplitPeople        int
}

// CategorySpendingView is a category total rendered on the overview.
type CategorySpendingView struct {
	Category string
	Currency string
	Minor    int64
}

// SubscriptionView augments a detected subscription with its account name.
type SubscriptionView struct {
	analysis.Subscription
	AccountName string
}

// FeeView augments a detected fee with its account name.
type FeeView struct {
	analysis.Fee
	AccountName string
}

// CurrencyAmountView is a per-currency total for display.
type CurrencyAmountView struct {
	Currency string
	Minor    int64
}

// RunwayView augments a treasury runway with a display label.
type RunwayView struct {
	treasury.Runway
	RunwayLabel string
}

// TransactionView augments a transaction with display metadata.
type TransactionView struct {
	domain.Transaction
	AccountName        string
	AccountType        string
	PassThroughLabel   string
	PassThroughTooltip string
}

// AlertView augments an alert with display metadata.
type AlertView struct {
	domain.Alert
	AccountName string
}

// ConnectionView augments a connection with its accounts and current cooldown
// state.
type ConnectionView struct {
	domain.Connection
	Accounts      []domain.Account
	RateLimited   bool
	CooldownUntil time.Time
}

// TaxShieldView is the display model for the business tax reserve card.
type TaxShieldView struct {
	Currency               string
	TotalBalanceMinor      int64
	TaxReserveMinor        int64
	TrueLiquidCapitalMinor int64
	VATDueMinor            int64
	VATCreditMinor         int64
	IncomeTaxMinor         int64
	QuarterEnd             time.Time
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
		accountType := domain.AccountTypePersonal
		if account, ok := accounts[tx.AccountID]; ok {
			if account.Name != "" {
				name = account.Name
			} else if account.IBAN != "" {
				name = account.IBAN
			}
			accountType = domain.NormalizeAccountType(account.Type)
		}
		out = append(out, TransactionView{Transaction: tx, AccountName: name, AccountType: accountType})
	}
	return out
}

// enrichPassThroughViews attaches the linked partner label and tooltip used by
// the transaction-table pass-through badge.
func (s *Server) enrichPassThroughViews(ctx context.Context, views []TransactionView) {
	ids := make([]int64, 0, len(views))
	for _, view := range views {
		if view.PairedTransactionID != nil {
			ids = append(ids, *view.PairedTransactionID)
		}
	}
	if len(ids) == 0 {
		return
	}
	partners, err := s.store.GetTransactionsByIDs(ctx, ids)
	if err != nil {
		s.logger.Error("load pass-through partners", "error", err)
		return
	}
	for index := range views {
		pairedID := views[index].PairedTransactionID
		if pairedID == nil {
			continue
		}
		partner, ok := partners[*pairedID]
		if !ok {
			continue
		}
		views[index].PassThroughLabel = fmt.Sprintf("⇄ Pass-through (Linked to #%d)", partner.ID)
		amount := formatMoney(partner.AmountMinor, partner.Currency)
		if partner.AmountMinor > 0 {
			amount = "+" + amount
		}
		name := strings.TrimSpace(partner.Counterparty)
		if name == "" {
			name = strings.TrimSpace(partner.Description)
		}
		if name == "" {
			name = "linked account"
		}
		views[index].PassThroughTooltip = fmt.Sprintf("Offset by %s from %s on %s",
			amount, name, partner.BookingDate.UTC().Format("02 Jan"))
	}
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

func connectionViews(connections []domain.Connection, accounts []domain.Account, now time.Time) []ConnectionView {
	byConnection := make(map[int64][]domain.Account)
	for _, account := range accounts {
		byConnection[account.ConnectionID] = append(byConnection[account.ConnectionID], account)
	}
	out := make([]ConnectionView, 0, len(connections))
	for _, connection := range connections {
		accounts := byConnection[connection.ID]
		rateLimited, until := connectionCoolingDown(connection, accounts, now)
		out = append(out, ConnectionView{
			Connection:    connection,
			Accounts:      accounts,
			RateLimited:   rateLimited,
			CooldownUntil: until,
		})
	}
	return out
}

func accountDisplayName(accountID string, accounts map[string]domain.Account) string {
	name := accountID
	if account, ok := accounts[accountID]; ok {
		if account.Name != "" {
			name = account.Name
		} else if account.IBAN != "" {
			name = account.IBAN
		}
	}
	return name
}

func subscriptionViews(subscriptions []analysis.Subscription, accounts map[string]domain.Account) []SubscriptionView {
	out := make([]SubscriptionView, 0, len(subscriptions))
	for _, subscription := range subscriptions {
		out = append(out, SubscriptionView{
			Subscription: subscription,
			AccountName:  accountDisplayName(subscription.AccountID, accounts),
		})
	}
	return out
}

func feeViews(fees []analysis.Fee, accounts map[string]domain.Account) []FeeView {
	out := make([]FeeView, 0, len(fees))
	for _, fee := range fees {
		out = append(out, FeeView{
			Fee:         fee,
			AccountName: accountDisplayName(fee.AccountID, accounts),
		})
	}
	return out
}

func currencyAmountViews(items []analysis.CurrencyAmount) []CurrencyAmountView {
	out := make([]CurrencyAmountView, 0, len(items))
	for _, item := range items {
		out = append(out, CurrencyAmountView{Currency: item.Currency, Minor: item.Minor})
	}
	return out
}

func runwayLabel(runway treasury.Runway) string {
	if !runway.RunwayKnown {
		return "no burn"
	}
	return fmt.Sprintf("%d.%d months", runway.RunwayTenths/10, runway.RunwayTenths%10)
}

func (s *Server) loadOverview(ctx context.Context, data *PageData) error {
	accounts, err := s.workspaceAccounts(ctx, data.Workspace.View)
	if err != nil {
		return err
	}
	data.Accounts = accounts
	now := s.now().UTC()
	accruing, err := s.accruingAccountViews(ctx, data.Workspace.View, accounts, now)
	if err != nil {
		return err
	}
	data.AccruingAccounts = accruing
	data.Summary = summarizeAccountsAndAccruing(accounts, accruing)
	data.TotalAccounts = len(accounts) + len(accruing)
	monthStart := firstOfMonth(now)
	if !data.BudgetMonth.IsZero() {
		monthStart = firstOfMonth(data.BudgetMonth)
	}
	var spending []storage.CategorySpending
	if data.Workspace.View == domain.WorkspaceAll || len(accounts) > 0 {
		spending, err = s.store.SpendingByCategoryForAccounts(ctx, monthStart, monthStart.AddDate(0, 1, 0), accountIDsArg(accounts, data.Workspace.View))
		if err != nil {
			return err
		}
		monthTransactions, err := s.store.ListTransactionsBetween(ctx, monthStart, monthStart.AddDate(0, 1, 0))
		if err != nil {
			return err
		}
		monthTransactions = filterTransactions(monthTransactions, allowedAccountIDs(accounts, data.Workspace.View), data.Workspace.View)
		var housingRule *domain.HousingRule
		if data.Workspace.View != domain.WorkspaceAll {
			currency := budgetCurrency(accounts)
			rule, exists, ruleErr := s.store.GetHousingRule(ctx, data.Workspace.View, currency)
			if ruleErr != nil {
				return ruleErr
			}
			if exists {
				housingRule = &rule
			}
		}
		spending = budgets.NetHousingSpendingWithRule(spending, monthTransactions, housingRule)
	}
	for _, item := range spending {
		data.CategorySpending = append(data.CategorySpending, CategorySpendingView{
			Category: item.Category, Currency: item.Currency, Minor: item.Minor,
		})
	}
	data.SpendingMonth = monthStart.Format("January 2006")
	windowStart := now.Add(-time.Duration(treasury.DefaultBurnWindowDays) * 24 * time.Hour)
	windowTransactions, err := s.store.ListTransactionsBetween(ctx, windowStart, now.Add(24*time.Hour))
	if err != nil {
		return err
	}
	windowTransactions = filterTransactions(windowTransactions, allowedAccountIDs(accounts, data.Workspace.View), data.Workspace.View)
	for _, runway := range treasury.ComputeRunways(accounts, windowTransactions, now, treasury.DefaultBurnWindowDays) {
		data.Treasury = append(data.Treasury, RunwayView{
			Runway:      runway,
			RunwayLabel: runwayLabel(runway),
		})
	}
	var total int
	if data.Workspace.View == domain.WorkspaceAll || len(accounts) > 0 {
		total, err = s.store.CountTransactionsFiltered(ctx, accountIDsArg(accounts, data.Workspace.View))
		if err != nil {
			return err
		}
	}
	data.TotalTransactions = total
	if err := s.loadAttachmentData(ctx, data); err != nil {
		return err
	}
	if err := s.loadTaxShields(ctx, data); err != nil {
		return err
	}
	if err := s.loadBudgetPlan(ctx, data, spending); err != nil {
		return err
	}
	return s.loadReconciliations(ctx, data)
}

func (s *Server) loadConnections(ctx context.Context, data *PageData) error {
	connections, err := s.store.ListConnections(ctx)
	if err != nil {
		return err
	}
	accounts, err := s.workspaceAccounts(ctx, data.Workspace.View)
	if err != nil {
		return err
	}
	data.Connections = connectionViews(connections, accounts, s.now())
	return s.loadReconciliations(ctx, data)
}

func (s *Server) loadReconciliations(ctx context.Context, data *PageData) error {
	accounts, err := s.workspaceAccounts(ctx, data.Workspace.View)
	if err != nil {
		return err
	}
	out := make(map[string]domain.Reconciliation, len(accounts))
	for _, account := range accounts {
		rec, err := s.store.GetReconciliation(ctx, account.ID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				continue
			}
			return err
		}
		out[account.ID] = rec
	}
	data.Reconciliations = out
	data.ReconciliationTotal = len(accounts)
	data.ReconciledAccounts = 0
	data.DiscrepancyAccounts = 0
	for _, rec := range out {
		if rec.Balanced() {
			data.ReconciledAccounts++
		} else {
			data.DiscrepancyAccounts++
		}
	}
	return nil
}

func (s *Server) loadAlerts(ctx context.Context, data *PageData, limit int) error {
	alerts, err := s.store.ListAlerts(ctx, limit)
	if err != nil {
		return err
	}
	accounts, err := s.workspaceAccounts(ctx, data.Workspace.View)
	if err != nil {
		return err
	}
	allowed := allowedAccountIDs(accounts, data.Workspace.View)
	if data.Workspace.View != domain.WorkspaceAll {
		filtered := make([]domain.Alert, 0, len(alerts))
		for _, alert := range alerts {
			if alert.AccountID == "" {
				continue
			}
			if _, ok := allowed[alert.AccountID]; ok {
				filtered = append(filtered, alert)
			}
		}
		alerts = filtered
	}
	data.Alerts = alertViews(alerts, accountMap(accounts))
	return nil
}

// transactionFilter builds a storage filter while keeping pagination bounded.
// accountIDs restricts the workspace view; nil means all accounts.
func transactionFilter(accountID string, accountIDs []string, page, limit int) storage.TransactionFilter {
	if page < 1 {
		page = 1
	}
	if limit <= 0 {
		limit = defaultTransactionLimit
	}
	return storage.TransactionFilter{
		AccountID:  accountID,
		AccountIDs: accountIDs,
		Limit:      limit,
		Offset:     (page - 1) * limit,
	}
}
