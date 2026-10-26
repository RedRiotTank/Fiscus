package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/budgets"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/pkg/accrual"
)

func TestAccruingAccountSetupAndAccountsPage(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	form := url.Values{
		"workspace":               {"personal"},
		"currency":                {"EUR"},
		"name":                    {"Revolut Cuenta Remunerada"},
		"anchor_balance":          {"3450.00"},
		"anchor_date":             {"2026-03-10"},
		"apy_percent":             {"3.10"},
		"transfer_match_keyword":  {"Cuenta Remunerada"},
		"tax_withholding_percent": {"19"},
		"compounding_frequency":   {"daily"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/accruing", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(syncRequestHeader, "1")
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/accounts?workspace=personal" {
		t.Fatalf("save accruing account = %d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}

	accounts, err := f.store.ListAccruingAccounts(context.Background())
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accruing accounts = %+v err=%v", accounts, err)
	}
	account := accounts[0]
	if account.Name != "Revolut Cuenta Remunerada" || account.AnchorBalanceCents != 345_000 ||
		account.APYRateBasisPoints != 310 || account.TaxWithholdingBPS != 1900 ||
		account.TransferMatchPattern == "" {
		t.Fatalf("saved account = %+v", account)
	}

	page := f.request(t, http.MethodGet, "/accounts?workspace=personal", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("accounts status = %d body=%s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	for _, want := range []string{
		"Revolut Cuenta Remunerada", "3.10% TAE · Daily", "Add savings account",
		"Passive Yield", "Lifetime interest", "Reconcile", "€5,200.50",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("accounts page missing %q", want)
		}
	}

	modal := f.request(t, http.MethodGet, "/api/accounts/accruing/new?workspace=personal", true, nil)
	if modal.Code != http.StatusOK {
		t.Fatalf("new account form = %d", modal.Code)
	}
	for _, want := range []string{`name="anchor_balance"`, `name="apy_percent"`, "Revolut Cuenta Remunerada"} {
		if !strings.Contains(modal.Body.String(), want) {
			t.Errorf("new account form missing %q", want)
		}
	}

	edit := f.request(t, http.MethodGet, "/api/accounts/accruing/"+account.ID+"/edit", true, nil)
	if edit.Code != http.StatusOK {
		t.Fatalf("edit account form = %d", edit.Code)
	}
	for _, want := range []string{"Reconcile savings account", "3450.00", "2026-03-10", "3.1"} {
		if !strings.Contains(edit.Body.String(), want) {
			t.Errorf("edit account form missing %q", want)
		}
	}

	dashboard := f.request(t, http.MethodGet, "/", true, nil)
	if dashboard.Code != http.StatusOK || !strings.Contains(dashboard.Body.String(), "Revolut Cuenta Remunerada") {
		t.Fatalf("dashboard missing accruing account net worth entry: %d", dashboard.Code)
	}
}

func TestAccruingTransfersLinkToSavingsAndIncomeNeutralWithdrawal(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	ctx := context.Background()
	account := domain.AccruingAccount{
		ID: "accrual-web", WorkspaceID: domain.WorkspacePersonal, Name: "Revolut Cuenta Remunerada",
		Currency: "EUR", AnchorBalanceCents: 100_000,
		AnchorDate:         time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		APYRateBasisPoints: 310, CompoundingFrequency: domain.AccruingDaily,
		TransferMatchPattern: `(?i)cuenta\s*remunerada`, IsActive: true,
	}
	if err := f.store.SaveAccruingAccount(ctx, &account); err != nil {
		t.Fatalf("SaveAccruingAccount: %v", err)
	}
	day := time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		{AccountID: "acc-1", ExternalID: "accrual-out", AmountMinor: -25_000, Currency: "EUR",
			BookingDate: day, Counterparty: "Cuenta Remunerada", Description: "To savings",
			Category: "Transfers / P2P", Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "accrual-in", AmountMinor: 10_000, Currency: "EUR",
			BookingDate: day.Add(time.Hour), Counterparty: "Cuenta Remunerada", Description: "From savings",
			Category: "Transfers / P2P", Status: domain.TransactionStatusBooked},
	}
	for index := range transactions {
		if _, _, err := f.store.UpsertTransaction(ctx, &transactions[index]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}
	if linked, err := f.store.LinkAccruingTransfers(ctx); err != nil || linked != 2 {
		t.Fatalf("LinkAccruingTransfers = %d err=%v", linked, err)
	}
	outflow, err := f.store.GetTransaction(ctx, transactions[0].ID)
	if err != nil {
		t.Fatalf("GetTransaction outflow: %v", err)
	}
	inflow, err := f.store.GetTransaction(ctx, transactions[1].ID)
	if err != nil {
		t.Fatalf("GetTransaction inflow: %v", err)
	}
	if outflow.Category != accrual.CategorySavings || outflow.IsBudgetExcluded {
		t.Fatalf("outflow = %+v", outflow)
	}
	if inflow.Category != accrual.CategoryWithdrawal || !inflow.IsBudgetExcluded {
		t.Fatalf("inflow = %+v", inflow)
	}
	page := f.request(t, http.MethodGet, "/accounts?workspace=personal", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("accounts page = %d", page.Code)
	}
	transfers, err := accrual.TransfersFromTransactions(account, transactions)
	if err != nil {
		t.Fatalf("TransfersFromTransactions: %v", err)
	}
	expected := formatMoney(accrual.Project(account, transfers, f.server.now()).BalanceMinor, "EUR")
	if !strings.Contains(page.Body.String(), expected) {
		t.Fatalf("accounts page does not reflect linked transfer principal %s: %s", expected, page.Body.String())
	}
	f.server.budgetPlanner = budgets.NewPlanner(f.store, f.server.now)
	budgetsPage := f.request(t, http.MethodGet, "/budgets?workspace=personal", true, nil)
	if budgetsPage.Code != http.StatusOK {
		t.Fatalf("budgets page = %d", budgetsPage.Code)
	}
	for _, want := range []string{"Savings / Accruing", "€250.00 of"} {
		if !strings.Contains(budgetsPage.Body.String(), want) {
			t.Errorf("budget page missing accruing savings mapping %q", want)
		}
	}
}
