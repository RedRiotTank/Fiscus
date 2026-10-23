package tax

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

type taxFixture struct {
	store   *storage.Store
	service *Service
	now     time.Time
}

func newTaxFixture(t *testing.T) *taxFixture {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "tax.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	connection := domain.Connection{RequisitionID: "req-tax", InstitutionName: "Bank", Status: "LN"}
	if err := store.UpsertConnection(ctx, &connection); err != nil {
		t.Fatalf("UpsertConnection: %v", err)
	}
	accounts := []domain.Account{
		{ID: "biz-eur", ConnectionID: connection.ID, Name: "Business EUR", Currency: "EUR", Type: domain.AccountTypeBusiness, BalanceMinor: 1_000_000},
		{ID: "biz-usd", ConnectionID: connection.ID, Name: "Business USD", Currency: "USD", Type: domain.AccountTypeBusiness, BalanceMinor: 500_000},
		{ID: "personal", ConnectionID: connection.ID, Name: "Personal", Currency: "EUR", Type: domain.AccountTypePersonal, BalanceMinor: 9_999_999},
	}
	for i := range accounts {
		if err := store.UpsertAccount(ctx, &accounts[i]); err != nil {
			t.Fatalf("UpsertAccount: %v", err)
		}
	}
	date := func(month time.Month, day int) time.Time {
		return time.Date(2026, month, day, 10, 0, 0, 0, time.UTC)
	}
	transactions := []domain.Transaction{
		{AccountID: "biz-eur", ExternalID: "eur-income", AmountMinor: 121_000, Currency: "EUR", Category: "Sales", BookingDate: date(time.February, 10), Status: domain.TransactionStatusBooked},
		{AccountID: "biz-eur", ExternalID: "eur-expense-1", AmountMinor: -60_500, Currency: "EUR", Category: "Supplies", BookingDate: date(time.February, 11), Status: domain.TransactionStatusBooked},
		{AccountID: "biz-eur", ExternalID: "eur-expense-2", AmountMinor: -24_200, Currency: "EUR", Category: "Software", BookingDate: date(time.March, 1), Status: domain.TransactionStatusBooked},
		{AccountID: "personal", ExternalID: "personal-income", AmountMinor: 500_000, Currency: "EUR", Category: "Salary", BookingDate: date(time.February, 12), Status: domain.TransactionStatusBooked},
		{AccountID: "biz-eur", ExternalID: "transfer", AmountMinor: -300_000, Currency: "EUR", Category: "Transfer", BookingDate: date(time.February, 13), Status: domain.TransactionStatusBooked},
		{AccountID: "biz-eur", ExternalID: "tax-payment", AmountMinor: -50_000, Currency: "EUR", Category: "Taxes", BookingDate: date(time.February, 14), Status: domain.TransactionStatusBooked},
		{AccountID: "biz-eur", ExternalID: "pending", AmountMinor: -100_000, Currency: "EUR", Category: "Supplies", BookingDate: date(time.February, 15), Status: domain.TransactionStatusPending},
		{AccountID: "biz-eur", ExternalID: "outside-quarter", AmountMinor: 500_000, Currency: "EUR", Category: "Sales", BookingDate: date(time.December, 31), Status: domain.TransactionStatusBooked},
		{AccountID: "biz-usd", ExternalID: "usd-income", AmountMinor: 121_000, Currency: "USD", Category: "Sales", BookingDate: date(time.February, 20), Status: domain.TransactionStatusBooked},
		{AccountID: "biz-usd", ExternalID: "usd-expense", AmountMinor: -60_500, Currency: "USD", Category: "Supplies", BookingDate: date(time.February, 21), Status: domain.TransactionStatusBooked},
	}
	for i := range transactions {
		if _, _, err := store.UpsertTransaction(ctx, &transactions[i]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}
	now := time.Date(2026, time.March, 18, 12, 0, 0, 0, time.UTC)
	service, err := New(store, Config{
		VATRateDefault: 0.21, IncomeTaxRetention: 0.15,
		QuarterStartMonths: []int{1, 4, 7, 10},
	}, func() time.Time { return now })
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &taxFixture{store: store, service: service, now: now}
}

func newEmptyTaxStore(t *testing.T) (*storage.Store, domain.Account) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "tax-empty.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	connection := domain.Connection{RequisitionID: "req-tax-empty", InstitutionName: "Bank", Status: "LN"}
	if err := store.UpsertConnection(ctx, &connection); err != nil {
		t.Fatalf("UpsertConnection: %v", err)
	}
	account := domain.Account{
		ID: "biz", ConnectionID: connection.ID, Name: "Business", Currency: "EUR",
		Type: domain.AccountTypeBusiness, BalanceMinor: 1_000_000,
	}
	if err := store.UpsertAccount(ctx, &account); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	return store, account
}

func insertTaxTransaction(t *testing.T, store *storage.Store, externalID string, minute int64, category string, date time.Time) {
	t.Helper()
	tx := domain.Transaction{
		AccountID: "biz", ExternalID: externalID, AmountMinor: minute, Currency: "EUR",
		BookingDate: date, Category: category, Status: domain.TransactionStatusBooked,
	}
	if _, _, err := store.UpsertTransaction(context.Background(), &tx); err != nil {
		t.Fatalf("UpsertTransaction %s: %v", externalID, err)
	}
}

func TestGranularCategoryRatesAndDeductibility(t *testing.T) {
	t.Parallel()
	store, _ := newEmptyTaxStore(t)
	day := time.Date(2026, 2, 10, 10, 0, 0, 0, time.UTC)
	insertTaxTransaction(t, store, "sales", 121_000, "Sales", day)
	insertTaxTransaction(t, store, "software", -12_100, "Software", day)
	insertTaxTransaction(t, store, "groceries", -10_000, "Groceries", day)
	insertTaxTransaction(t, store, "exempt", -5_000, "Exempt Services", day)
	service, err := New(store, Config{
		VATRateDefault: 0.21, IncomeTaxRetention: 0.15,
		QuarterStartMonths: []int{1, 4, 7, 10},
		Categories: []CategoryConfig{
			{Name: "Software", VATRate: 0.21, DeductiblePercent: 1},
			{Name: "Groceries", VATRate: 0.04, DeductiblePercent: 0},
			{Name: "Exempt Services", VATRate: 0, DeductiblePercent: 1},
		},
	}, func() time.Time { return day })
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	shields, err := service.ShieldsForQuarter(context.Background(), 2026, 1)
	if err != nil {
		t.Fatalf("ShieldsForQuarter: %v", err)
	}
	if len(shields) != 1 {
		t.Fatalf("shields = %+v", shields)
	}
	shield := shields[0]
	if shield.VATCollectedMinor != 21_000 || shield.VATPaidMinor != 2_100 {
		t.Fatalf("VAT = %+v", shield.Estimate)
	}
	if shield.DeductibleExpenseGrossMinor != 17_100 || shield.DeductibleExpenseBaseMinor != 15_000 {
		t.Fatalf("deductible expenses = %+v", shield.Estimate)
	}
	if shield.VATDueMinor != 18_900 || shield.IncomeTaxMinor != 12_750 || shield.EstimatedTaxDueMinor != 31_650 {
		t.Fatalf("tax due = %+v", shield.Estimate)
	}
	if len(shield.Lines) != 4 {
		t.Fatalf("lines = %+v", shield.Lines)
	}
	var groceries CategoryLine
	for _, line := range shield.Lines {
		if line.Category == "Groceries" {
			groceries = line
		}
	}
	if groceries.DeductibleExpenseGrossMinor != 0 || groceries.VATPaidMinor != 0 {
		t.Fatalf("non-deductible groceries = %+v", groceries)
	}
}

func TestVATCarryForwardAcrossQuarters(t *testing.T) {
	t.Parallel()
	store, _ := newEmptyTaxStore(t)
	q1 := time.Date(2026, 2, 10, 10, 0, 0, 0, time.UTC)
	q2 := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	insertTaxTransaction(t, store, "q1-expense", -121_000, "Software", q1)
	insertTaxTransaction(t, store, "q2-income", 121_000, "Sales", q2)
	service, err := New(store, Config{
		VATRateDefault: 0.21, IncomeTaxRetention: 0.15,
		QuarterStartMonths: []int{1, 4, 7, 10},
	}, func() time.Time { return q2 })
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	q1Shields, err := service.ShieldsForQuarter(context.Background(), 2026, 1)
	if err != nil {
		t.Fatalf("ShieldsForQuarter Q1: %v", err)
	}
	if q1Shields[0].VATDueMinor != 0 || q1Shields[0].VATCreditMinor != 21_000 {
		t.Fatalf("Q1 VAT = %+v", q1Shields[0].Estimate)
	}
	q2Shields, err := service.ShieldsForQuarter(context.Background(), 2026, 2)
	if err != nil {
		t.Fatalf("ShieldsForQuarter Q2: %v", err)
	}
	if q2Shields[0].VATCarryForwardMinor != 21_000 || q2Shields[0].VATDueMinor != 0 ||
		q2Shields[0].VATCreditMinor != 0 {
		t.Fatalf("Q2 VAT = %+v", q2Shields[0].Estimate)
	}
	if q2Shields[0].IncomeTaxMinor != 15_000 || q2Shields[0].EstimatedTaxDueMinor != 15_000 {
		t.Fatalf("Q2 income tax = %+v", q2Shields[0].Estimate)
	}
}

func TestSummaryCSVIncludesCategoryRatesAndTotals(t *testing.T) {
	t.Parallel()
	store, _ := newEmptyTaxStore(t)
	day := time.Date(2026, 2, 10, 10, 0, 0, 0, time.UTC)
	insertTaxTransaction(t, store, "sales", 121_000, "Sales", day)
	insertTaxTransaction(t, store, "groceries", -10_000, "Groceries", day)
	service, err := New(store, Config{
		VATRateDefault: 0.21, IncomeTaxRetention: 0.15,
		QuarterStartMonths: []int{1, 4, 7, 10},
		Categories: []CategoryConfig{
			{Name: "Groceries", VATRate: 0.04, DeductiblePercent: 0},
		},
	}, func() time.Time { return day })
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	shields, err := service.ShieldsForQuarter(context.Background(), 2026, 1)
	if err != nil {
		t.Fatalf("ShieldsForQuarter: %v", err)
	}
	data, err := SummaryCSV(shields)
	if err != nil {
		t.Fatalf("SummaryCSV: %v", err)
	}
	text := string(data)
	for _, want := range []string{"quarter_start", "vat_rate", "TOTAL", "1210.00", "0.0400", "estimated_tax_due"} {
		if !strings.Contains(text, want) {
			t.Errorf("summary missing %q: %s", want, text)
		}
	}
}

func TestQuarterForBoundaries(t *testing.T) {
	t.Parallel()
	f := newTaxFixture(t)
	tests := []struct {
		now     time.Time
		start   string
		end     string
		endExcl string
	}{
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "2026-01-01", "2026-03-31", "2026-04-01"},
		{time.Date(2026, 3, 31, 23, 59, 0, 0, time.UTC), "2026-01-01", "2026-03-31", "2026-04-01"},
		{time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), "2026-04-01", "2026-06-30", "2026-07-01"},
		{time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC), "2026-10-01", "2026-12-31", "2027-01-01"},
	}
	for _, test := range tests {
		q := f.service.QuarterFor(test.now)
		if q.Start.Format("2006-01-02") != test.start || q.End.Format("2006-01-02") != test.end ||
			q.EndExclusive.Format("2006-01-02") != test.endExcl {
			t.Errorf("QuarterFor(%s) = %s..%s (exclusive %s)", test.now, q.Start, q.End, q.EndExclusive)
		}
	}
}

func TestShieldsSegregateBusinessTransactionsByCurrency(t *testing.T) {
	t.Parallel()
	f := newTaxFixture(t)
	shields, err := f.service.ShieldsAt(context.Background(), f.now)
	if err != nil {
		t.Fatalf("ShieldsAt: %v", err)
	}
	if len(shields) != 2 {
		t.Fatalf("shields = %+v", shields)
	}
	byCurrency := make(map[string]Shield, len(shields))
	for _, shield := range shields {
		byCurrency[shield.Currency] = shield
	}
	eur := byCurrency["EUR"]
	if eur.TaxableIncomeGrossMinor != 121_000 || eur.TaxableIncomeBaseMinor != 100_000 {
		t.Fatalf("EUR income = %+v", eur.Estimate)
	}
	if eur.DeductibleExpenseGrossMinor != 84_700 || eur.DeductibleExpenseBaseMinor != 70_000 {
		t.Fatalf("EUR expenses = %+v", eur.Estimate)
	}
	if eur.VATCollectedMinor != 21_000 || eur.VATPaidMinor != 14_700 || eur.VATDueMinor != 6_300 {
		t.Fatalf("EUR VAT = %+v", eur.Estimate)
	}
	if eur.IncomeTaxMinor != 4_500 || eur.EstimatedTaxDueMinor != 10_800 {
		t.Fatalf("EUR tax = %+v", eur.Estimate)
	}
	if eur.TotalBalanceMinor != 1_000_000 || eur.TaxReserveMinor != 10_800 || eur.TrueLiquidCapitalMinor != 989_200 {
		t.Fatalf("EUR shield = %+v", eur)
	}
	usd := byCurrency["USD"]
	if usd.VATDueMinor != 10_500 || usd.IncomeTaxMinor != 7_500 || usd.EstimatedTaxDueMinor != 18_000 {
		t.Fatalf("USD tax = %+v", usd.Estimate)
	}
	if usd.TotalBalanceMinor != 500_000 || usd.TrueLiquidCapitalMinor != 482_000 {
		t.Fatalf("USD shield = %+v", usd)
	}
}

func TestQuarterlyRuleAlertWindow(t *testing.T) {
	t.Parallel()
	f := newTaxFixture(t)
	rule := NewQuarterlyRule(f.service, func() time.Time { return f.now })
	alerts, err := rule.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(alerts) != 2 {
		t.Fatalf("alerts = %+v", alerts)
	}
	for _, alert := range alerts {
		if alert.Rule != "tax_reserve" || !strings.Contains(alert.Message, "Reserve") ||
			!strings.Contains(alert.DedupKey, "2026-01-01") {
			t.Fatalf("alert = %+v", alert)
		}
	}
	early := NewQuarterlyRule(f.service, func() time.Time {
		return time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC)
	})
	alerts, err = early.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate early: %v", err)
	}
	if len(alerts) != 0 {
		t.Fatalf("early alerts = %+v", alerts)
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	f := newTaxFixture(t)
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "VAT rate", cfg: Config{VATRateDefault: 1, IncomeTaxRetention: 0.15, QuarterStartMonths: []int{1, 4, 7, 10}}},
		{name: "retention", cfg: Config{VATRateDefault: 0.21, IncomeTaxRetention: -0.1, QuarterStartMonths: []int{1, 4, 7, 10}}},
		{name: "months count", cfg: Config{VATRateDefault: 0.21, IncomeTaxRetention: 0.15, QuarterStartMonths: []int{1, 4}}},
		{name: "duplicate month", cfg: Config{VATRateDefault: 0.21, IncomeTaxRetention: 0.15, QuarterStartMonths: []int{1, 1, 7, 10}}},
		{name: "category name", cfg: Config{VATRateDefault: 0.21, IncomeTaxRetention: 0.15, QuarterStartMonths: []int{1, 4, 7, 10}, Categories: []CategoryConfig{{Name: " ", VATRate: 0.21}}}},
		{name: "category VAT", cfg: Config{VATRateDefault: 0.21, IncomeTaxRetention: 0.15, QuarterStartMonths: []int{1, 4, 7, 10}, Categories: []CategoryConfig{{Name: "Software", VATRate: 1}}}},
		{name: "category deductible", cfg: Config{VATRateDefault: 0.21, IncomeTaxRetention: 0.15, QuarterStartMonths: []int{1, 4, 7, 10}, Categories: []CategoryConfig{{Name: "Software", DeductiblePercent: 1.1}}}},
		{name: "duplicate category", cfg: Config{VATRateDefault: 0.21, IncomeTaxRetention: 0.15, QuarterStartMonths: []int{1, 4, 7, 10}, Categories: []CategoryConfig{{Name: "Software", VATRate: 0.21}, {Name: "software", VATRate: 0.1}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(f.store, test.cfg, nil); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
