package web

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/analysis"
	"github.com/RedRiotTank/Fiscus/internal/budgets"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/pkg/recurring"
)

func workspaceList(view string) []string {
	switch view {
	case domain.WorkspacePersonal, domain.WorkspaceBusiness:
		return []string{view}
	default:
		return []string{domain.WorkspacePersonal, domain.WorkspaceBusiness}
	}
}

func uniqueAccountCurrencies(accounts []domain.Account) []string {
	seen := make(map[string]struct{}, len(accounts))
	out := make([]string, 0, len(accounts))
	for _, account := range accounts {
		currency := strings.ToUpper(strings.TrimSpace(account.Currency))
		if currency == "" {
			currency = "EUR"
		}
		if _, exists := seen[currency]; exists {
			continue
		}
		seen[currency] = struct{}{}
		out = append(out, currency)
	}
	if len(out) == 0 {
		out = append(out, "EUR")
	}
	sort.Strings(out)
	return out
}

func fixedBillKey(workspace, currency, counterparty string) string {
	return strings.ToLower(strings.Join([]string{
		strings.TrimSpace(workspace), strings.TrimSpace(currency),
		strings.TrimSpace(counterparty),
	}, "\x00"))
}

// loadFixedBills combines confirmed recurring rules, detected monthly bills,
// and recurring utilities into the Fixed Bills & Utilities view.
func (s *Server) loadFixedBills(ctx context.Context, workspace string, accounts []domain.Account, now time.Time) ([]FixedBillView, []domain.RecurringBillRule, error) {
	now = now.UTC()
	transactions, err := s.store.ListTransactionsBetween(ctx, now.AddDate(0, -7, 0), now.Add(24*time.Hour))
	if err != nil {
		return nil, nil, err
	}
	transactions = filterTransactions(transactions, allowedAccountIDs(accounts, workspace), workspace)
	accountByID := accountMap(accounts)
	candidates := recurring.Detect(transactions, now)
	currencies := uniqueAccountCurrencies(accounts)

	entries := make(map[string]FixedBillView)
	rules := make([]domain.RecurringBillRule, 0)
	for _, ws := range workspaceList(workspace) {
		for _, currency := range currencies {
			loaded, loadErr := s.store.ListRecurringBillRules(ctx, ws, currency)
			if loadErr != nil {
				return nil, nil, loadErr
			}
			for _, rule := range loaded {
				rules = append(rules, rule)
				share := rule.PersonalShareMinor
				if share <= 0 {
					share = rule.GrossMinor
				}
				key := fixedBillKey(ws, currency, rule.Counterparty)
				entries[key] = FixedBillView{
					Workspace: ws, Currency: currency, Counterparty: rule.Counterparty,
					Category: rule.Category, Recurrence: rule.Recurrence,
					GrossMinor: rule.GrossMinor, PersonalShareMinor: share,
					LastSeen: rule.UpdatedAt, Confirmed: true,
					SplitMode: rule.SplitMode, SplitPeople: rule.SplitPeople,
				}
			}
		}
	}

	for _, candidate := range candidates {
		account, ok := accountByID[candidate.AccountID]
		if !ok {
			continue
		}
		ws := domain.NormalizeAccountType(account.Type)
		key := fixedBillKey(ws, candidate.Currency, candidate.Counterparty)
		if _, exists := entries[key]; exists {
			continue
		}
		entries[key] = FixedBillView{
			Workspace: ws, Currency: candidate.Currency, Counterparty: candidate.Counterparty,
			Category: candidate.Category, Recurrence: domain.RecurringMonthly,
			GrossMinor: candidate.AmountMinor, PersonalShareMinor: candidate.AmountMinor,
			Occurrences: candidate.Occurrences, LastSeen: candidate.LastSeen,
		}
	}

	for _, group := range recurringUtilityGroups(transactions, accountByID) {
		key := fixedBillKey(group.workspace, group.currency, group.counterparty)
		if _, exists := entries[key]; exists {
			continue
		}
		entries[key] = FixedBillView{
			Workspace: group.workspace, Currency: group.currency, Counterparty: group.counterparty,
			Category: "Utilities & Telecom", Recurrence: domain.RecurringMonthly,
			GrossMinor: group.amount, PersonalShareMinor: group.amount,
			Occurrences: group.occurrences, LastSeen: group.lastSeen,
		}
	}

	out := make([]FixedBillView, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Confirmed != out[j].Confirmed {
			return out[i].Confirmed
		}
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return strings.ToLower(out[i].Counterparty) < strings.ToLower(out[j].Counterparty)
	})
	return out, rules, nil
}

type utilityGroup struct {
	workspace    string
	currency     string
	counterparty string
	amount       int64
	occurrences  int
	lastSeen     time.Time
}

func recurringUtilityGroups(transactions []domain.Transaction, accountByID map[string]domain.Account) []utilityGroup {
	type accumulator struct {
		group utilityGroup
	}
	groups := make(map[string]*accumulator)
	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked || tx.AmountMinor >= 0 ||
			!isUtilityCategory(tx.Category) {
			continue
		}
		merchant := strings.TrimSpace(tx.Counterparty)
		if merchant == "" {
			merchant = strings.TrimSpace(tx.Description)
		}
		if merchant == "" {
			continue
		}
		currency := strings.ToUpper(strings.TrimSpace(tx.Currency))
		if currency == "" {
			currency = "EUR"
		}
		key := strings.ToLower(currency + "\x00" + merchant)
		group := groups[key]
		if group == nil {
			workspace := domain.WorkspacePersonal
			if account, ok := accountByID[tx.AccountID]; ok {
				workspace = domain.NormalizeAccountType(account.Type)
			}
			group = &accumulator{group: utilityGroup{
				workspace: workspace, currency: currency, counterparty: merchant,
			}}
			groups[key] = group
		}
		group.group.occurrences++
		if group.group.lastSeen.IsZero() || tx.BookingDate.After(group.group.lastSeen) {
			group.group.lastSeen = tx.BookingDate
			group.group.amount = -tx.AmountMinor
		}
	}
	out := make([]utilityGroup, 0, len(groups))
	for _, group := range groups {
		if group.group.occurrences < 2 {
			continue
		}
		out = append(out, group.group)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].counterparty) < strings.ToLower(out[j].counterparty)
	})
	return out
}

func isUtilityCategory(category string) bool {
	category = strings.ToLower(strings.TrimSpace(category))
	return strings.Contains(category, "utilit") || strings.Contains(category, "telecom")
}

func isFixedNeedCategory(category string, categories map[string]struct{}) bool {
	_, exists := categories[strings.ToLower(strings.TrimSpace(category))]
	return exists
}

func isSubscriptionLikeCategory(category string) bool {
	category = strings.ToLower(strings.TrimSpace(category))
	return strings.Contains(category, "subscription") ||
		strings.Contains(category, "software") ||
		strings.Contains(category, "cloud") ||
		strings.Contains(category, "saas")
}

func (s *Server) fixedNeedCategories(ctx context.Context, workspace, currency string) (map[string]struct{}, error) {
	allocations, err := s.workspaceBudgetAllocations(ctx, workspace, currency)
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(allocations))
	for _, allocation := range allocations {
		if allocation.Pillar != budgets.PillarNeeds || isSubscriptionLikeCategory(allocation.Category) {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(allocation.Category))] = struct{}{}
	}
	return out, nil
}

func (s *Server) loadHousingRules(ctx context.Context, workspace string, accounts []domain.Account) ([]domain.HousingRule, error) {
	currencies := uniqueAccountCurrencies(accounts)
	out := make([]domain.HousingRule, 0)
	for _, ws := range workspaceList(workspace) {
		for _, currency := range currencies {
			rule, exists, err := s.store.GetHousingRule(ctx, ws, currency)
			if err != nil {
				return nil, err
			}
			if exists {
				out = append(out, rule)
			}
		}
	}
	return out, nil
}

// filterFixedBillSubscriptions removes household bills, confirmed fixed rules,
// housing rules, and user-defined Needs categories from the consumer feed.
func filterFixedBillSubscriptions(subscriptions []analysis.Subscription, rules []domain.RecurringBillRule, housingRules []domain.HousingRule, fixedNeeds map[string]struct{}) []analysis.Subscription {
	out := make([]analysis.Subscription, 0, len(subscriptions))
	for _, subscription := range subscriptions {
		if isUtilityCategory(subscription.Category) || isFixedNeedCategory(subscription.Category, fixedNeeds) {
			continue
		}
		fixed := false
		transaction := domain.Transaction{Counterparty: subscription.Counterparty}
		for _, rule := range rules {
			if recurring.MatchesCounterparty(transaction, rule.Counterparty) {
				fixed = true
				break
			}
		}
		if !fixed {
			for _, rule := range housingRules {
				if recurring.MatchesCounterparty(transaction, rule.LandlordCounterparty) {
					fixed = true
					break
				}
			}
		}
		if fixed {
			continue
		}
		out = append(out, subscription)
	}
	return out
}

func subscriptionCurrencyAmounts(subscriptions []analysis.Subscription) ([]analysis.CurrencyAmount, []analysis.CurrencyAmount) {
	monthly := make(map[string]int64)
	annual := make(map[string]int64)
	for _, subscription := range subscriptions {
		if !subscription.Active {
			continue
		}
		currency := strings.ToUpper(subscription.Currency)
		monthly[currency] += subscription.MonthlyMinor()
		annual[currency] += subscription.AnnualMinor()
	}
	return sortCurrencyAmounts(monthly), sortCurrencyAmounts(annual)
}

func sortCurrencyAmounts(values map[string]int64) []analysis.CurrencyAmount {
	currencies := make([]string, 0, len(values))
	for currency := range values {
		currencies = append(currencies, currency)
	}
	sort.Strings(currencies)
	out := make([]analysis.CurrencyAmount, 0, len(currencies))
	for _, currency := range currencies {
		out = append(out, analysis.CurrencyAmount{Currency: currency, Minor: values[currency]})
	}
	return out
}
