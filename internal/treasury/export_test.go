package treasury

import (
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func exportFixture() ([]domain.Transaction, []domain.Account) {
	accounts := []domain.Account{
		{ID: "acc-1", Name: "Everyday", IBAN: "ES0001", Currency: "EUR"},
		{ID: "acc-2", Name: "Café Savings", IBAN: "ES0002", Currency: "EUR"},
	}
	transactions := []domain.Transaction{
		{
			ID: 1, AccountID: "acc-1", AmountMinor: -1234, Currency: "EUR",
			BookingDate:  time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			Counterparty: "Coffee Shop", Description: "Flat white", Category: "Dining",
			Status: domain.TransactionStatusBooked,
		},
		{
			ID: 2, AccountID: "acc-1", AmountMinor: 300000, Currency: "EUR",
			BookingDate:  time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC),
			Counterparty: "Employer", Description: "Monthly salary", Category: "Payroll",
			Status: domain.TransactionStatusBooked,
		},
		{
			ID: 3, AccountID: "acc-2", AmountMinor: -50, Currency: "EUR",
			BookingDate:  time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC),
			Counterparty: "Pending Shop", Description: "Pending", Category: "Shopping",
			Status: domain.TransactionStatusPending,
		},
	}
	return transactions, accounts
}

func TestExportCSV(t *testing.T) {
	t.Parallel()
	transactions, accounts := exportFixture()
	data, err := Export(FormatCSV, transactions, accounts)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v\n%s", err, data)
	}
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4\n%s", len(rows), data)
	}
	if rows[0][0] != "date" || rows[0][6] != "amount" {
		t.Fatalf("header = %v", rows[0])
	}
	if rows[1][0] != "2026-03-01" || rows[1][1] != "Everyday" || rows[1][3] != "Coffee Shop" ||
		rows[1][5] != "Dining" || rows[1][6] != "-12.34" || rows[1][7] != "EUR" {
		t.Fatalf("first row = %v", rows[1])
	}
	if rows[3][8] != domain.TransactionStatusPending {
		t.Fatalf("pending row = %v", rows[3])
	}
}

func TestExportHLedger(t *testing.T) {
	t.Parallel()
	transactions, accounts := exportFixture()
	data, err := Export(FormatHLedger, transactions, accounts)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	output := string(data)
	for _, want := range []string{
		"2026-03-01 * Coffee Shop | Flat white",
		"    expenses:dining  12.34 EUR",
		"    assets:bank:everyday  -12.34 EUR",
		"2026-03-02 * Employer | Monthly salary",
		"    assets:bank:everyday  3000.00 EUR",
		"    income:payroll  -3000.00 EUR",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("hledger output missing %q\n%s", want, output)
		}
	}
	if strings.Contains(output, "Pending Shop") {
		t.Fatalf("pending transaction exported to hledger\n%s", output)
	}
}

func TestExportBeancount(t *testing.T) {
	t.Parallel()
	transactions, accounts := exportFixture()
	data, err := Export(FormatBeancount, transactions, accounts)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	output := string(data)
	for _, want := range []string{
		"2000-01-01 open Assets:Bank:Everyday EUR",
		"2000-01-01 open Expenses:Dining EUR",
		"2000-01-01 open Income:Payroll EUR",
		`2026-03-01 * "Coffee Shop" "Flat white"`,
		"  Expenses:Dining  12.34 EUR",
		"  Assets:Bank:Everyday  -12.34 EUR",
		"  Assets:Bank:Everyday  3000.00 EUR",
		"  Income:Payroll  -3000.00 EUR",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("beancount output missing %q\n%s", want, output)
		}
	}
	if strings.Contains(output, "Pending Shop") {
		t.Fatalf("pending transaction exported to beancount\n%s", output)
	}
}

func TestParseFormatAndMetadata(t *testing.T) {
	t.Parallel()
	cases := map[string]ExportFormat{
		"":          FormatCSV,
		"csv":       FormatCSV,
		"hledger":   FormatHLedger,
		"journal":   FormatHLedger,
		"beancount": FormatBeancount,
	}
	for input, want := range cases {
		got, err := ParseFormat(input)
		if err != nil || got != want {
			t.Fatalf("ParseFormat(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := ParseFormat("pdf"); err == nil {
		t.Fatal("expected unsupported format error")
	}
	if Extension(FormatHLedger) != ".journal" || Extension(FormatBeancount) != ".beancount" || Extension(FormatCSV) != ".csv" {
		t.Fatal("unexpected extension mapping")
	}
}

func TestAccountNamesAreSafe(t *testing.T) {
	t.Parallel()
	if got := slug("Café Savings"); got != "caf-savings" {
		t.Fatalf("slug = %q", got)
	}
	if got := titleComponent("Café Savings"); got != "CafSavings" {
		t.Fatalf("titleComponent = %q", got)
	}
}
