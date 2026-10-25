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
	"github.com/RedRiotTank/Fiscus/pkg/housing"
)

func seedHousingTransactions(t *testing.T, f *webFixture) {
	t.Helper()
	monthStart := time.Date(f.server.now().Year(), f.server.now().Month(), 1, 0, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		{
			AccountID: "acc-1", ExternalID: "housing-rent", AmountMinor: -105_000, Currency: "EUR",
			BookingDate: monthStart, Counterparty: "Sonia Reyes Álvarez",
			Description: "Transferencia mensual", Category: "Transfers", Status: domain.TransactionStatusBooked,
		},
		{
			AccountID: "acc-1", ExternalID: "housing-flatmate-1", AmountMinor: 35_000, Currency: "EUR",
			BookingDate: monthStart.AddDate(0, 0, 1), Counterparty: "Ismael Díaz",
			Description: "Piso marzo | From Ismael", Category: "Uncategorized", Status: domain.TransactionStatusBooked,
		},
		{
			AccountID: "acc-1", ExternalID: "housing-flatmate-2", AmountMinor: 35_000, Currency: "EUR",
			BookingDate: monthStart.AddDate(0, 0, 2), Counterparty: "Jose Hinojos",
			Description: "Piso marzo | From Jose", Category: "Uncategorized", Status: domain.TransactionStatusBooked,
		},
	}
	for i := range transactions {
		if _, _, err := f.store.UpsertTransaction(context.Background(), &transactions[i]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}
}

func TestHousingSetupModalDetectsSharedRent(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	f.server.budgetPlanner = budgets.NewPlanner(f.store, f.server.now)
	seedHousingTransactions(t, f)
	page := f.request(t, http.MethodGet, "/api/budgets/housing/setup?workspace=personal&currency=EUR", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("housing setup = %d %s", page.Code, page.Body.String())
	}
	budgetPage := f.request(t, http.MethodGet, "/budgets?workspace=personal", true, nil)
	if budgetPage.Code != http.StatusOK {
		t.Fatalf("budgets = %d", budgetPage.Code)
	}
	for _, want := range []string{"Shared rent detected", "Configure Rent / Flatmates", "Review &amp; confirm"} {
		if !strings.Contains(budgetPage.Body.String(), want) {
			t.Errorf("budgets page missing housing prompt %q", want)
		}
	}
	body := page.Body.String()
	for _, want := range []string{
		"Configure Rent / Flatmates", "Sonia Reyes Álvarez", "Ismael Díaz", "Jose Hinojos",
		"€1,050.00", `value="350.00"`, "Save Housing Rule", "Dismiss",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("housing setup missing %q", want)
		}
	}
}

func TestHousingSetupSavesRuleAndNetsBudget(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	f.server.budgetPlanner = budgets.NewPlanner(f.store, f.server.now)
	seedHousingTransactions(t, f)
	form := url.Values{
		"workspace":             {"personal"},
		"currency":              {"EUR"},
		"landlord_counterparty": {"Sonia Reyes Álvarez"},
		"personal_net_share":    {"350.00"},
		"calculation_mode":      {"fixed_share"},
		"flatmate":              {"Ismael Díaz", "Jose Hinojos"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/budgets/housing/setup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(syncRequestHeader, "1")
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/budgets?workspace=personal" {
		t.Fatalf("save housing = %d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	rule, exists, err := f.store.GetHousingRule(context.Background(), "personal", "EUR")
	if err != nil || !exists {
		t.Fatalf("rule exists=%v err=%v", exists, err)
	}
	if rule.PersonalNetShareCents != 35_000 || rule.CalculationMode != domain.HousingModeFixedShare ||
		len(rule.FlatmateCounterparties) != 2 {
		t.Fatalf("rule = %+v", rule)
	}
	transactions, err := f.store.ListTransactionsBetween(context.Background(),
		f.server.now().AddDate(0, -1, 0), f.server.now().AddDate(0, 1, 0))
	if err != nil {
		t.Fatalf("transactions: %v", err)
	}
	categorized := 0
	for _, tx := range transactions {
		if tx.ExternalID == "housing-rent" || strings.HasPrefix(tx.ExternalID, "housing-flatmate") {
			if tx.Category != housing.Category {
				t.Errorf("transaction %s category = %q", tx.ExternalID, tx.Category)
			}
			categorized++
		}
	}
	if categorized != 3 {
		t.Fatalf("categorized housing transactions = %d", categorized)
	}
	rules, err := f.store.ListLearnedRules(context.Background())
	if err != nil {
		t.Fatalf("ListLearnedRules: %v", err)
	}
	housingRules := 0
	for _, learned := range rules {
		if learned.Source == housingRuleSourceFor("personal") && learned.Category == housing.Category {
			housingRules++
		}
	}
	if housingRules != 1 {
		t.Fatalf("learned housing rules = %+v", rules)
	}
	page := f.request(t, http.MethodGet, "/budgets?workspace=personal", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("budgets = %d", page.Code)
	}
	body := page.Body.String()
	if !strings.Contains(body, "Housing / Rent") || !strings.Contains(body, "€350.00 of") {
		t.Fatalf("budget page does not show net housing spend: %s", body)
	}
	if strings.Contains(body, "€1,050.00 of") {
		t.Fatal("budget page still shows gross rent")
	}
	if !strings.Contains(body, "Housing rule active") {
		t.Fatal("budget page does not show the active housing rule")
	}
}

func TestHousingSetupRejectsInvalidShare(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	f.server.budgetPlanner = budgets.NewPlanner(f.store, f.server.now)
	form := url.Values{
		"workspace":             {"personal"},
		"currency":              {"EUR"},
		"landlord_counterparty": {"Sonia Reyes Álvarez"},
		"personal_net_share":    {"-1"},
		"calculation_mode":      {"fixed_share"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/budgets/housing/setup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(syncRequestHeader, "1")
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "must be a positive amount") {
		t.Fatalf("invalid share = %d %s", rec.Code, rec.Body.String())
	}
	if _, exists, err := f.store.GetHousingRule(context.Background(), "personal", "EUR"); err != nil || exists {
		t.Fatalf("invalid rule persisted exists=%v err=%v", exists, err)
	}
}
