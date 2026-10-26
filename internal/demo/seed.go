// Package demo builds a coherent, fully populated Fiscus dataset for the
// interactive demo mode.
package demo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand"
	"sort"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/attachments"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

const (
	demoConnectionSantander = "demo-santander"
	demoConnectionRevolut   = "demo-revolut"
	demoConnectionBBVA      = "demo-bbva"
	demoConnectionWise      = "demo-wise"

	accountSantander = "demo-santander-personal"
	accountSavings   = "demo-revolut-savings"
	accountBusiness  = "demo-bbva-business"
	accountWiseUSD   = "demo-wise-usd"
	accountWiseGBP   = "demo-wise-gbp"
)

var (
	restaurantNames = []string{
		"La Taberna del Puerto", "Sushi Nikkei", "Café Central", "Pizzería Roma",
		"El Mercado", "Casa Lucio", "La Musa", "Tapa Tapa",
		"Vermut & Co", "Bistró Norte", "La Pérgola", "Rincón de Sevilla",
		"Tandoor House", "Miso Ramen", "El Huerto", "La Marina",
		"Bodega Andaluza", "Terraza del Sol", "The Green Fork", "La Cuchara",
		"Barrio Soho", "Osteria Nera", "El Faro", "Mercado de San Antón",
	}
	groceryNames = []string{
		"Mercadona", "Carrefour", "Lidl", "Aldi", "Dia", "Eroski",
		"Alcampo", "Consum", "Spar", "Hipercor", "BM", "Ahorramás",
		"Masymas", "Gadis", "Froiz", "Caprabo", "Bonpreu", "Supercor",
	}
	electricityNames = []string{
		"Iberdrola", "Endesa", "Naturgy", "Repsol", "Holaluz", "Lucera",
	}
	gasNames = []string{
		"Naturgy", "Repsol", "Iberdrola", "Endesa", "Lucera", "Holaluz",
	}
)

// Dataset summarizes the rows and presentation hints created by Seed.
type Dataset struct {
	Connections       []domain.Connection
	Accounts          []domain.Account
	Transactions      []domain.Transaction
	Alerts            []domain.Alert
	Attachments       []domain.Attachment
	CurrentMonthSpend map[string]int64
}

type builder struct {
	ctx          context.Context
	store        *storage.Store
	now          time.Time
	monthStart   time.Time
	months       []time.Time
	rng          *rand.Rand
	sequence     int
	transactions []domain.Transaction
	byKey        map[string]domain.Transaction
	currentSpend map[string]int64
	err          error
}

type accountSpec struct {
	id         string
	connection string
	name       string
	iban       string
	currency   string
	accountTyp string
	balance    int64
}

// Seed inserts a complete demo dataset into an already migrated store and
// writes mock receipt files below attachmentsDir.
func Seed(ctx context.Context, store *storage.Store, attachmentsDir string, now time.Time) (Dataset, error) {
	if store == nil {
		return Dataset{}, errors.New("demo: store is required")
	}
	if strings.TrimSpace(attachmentsDir) == "" {
		return Dataset{}, errors.New("demo: attachment directory is required")
	}
	now = now.UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	b := &builder{
		ctx:          ctx,
		store:        store,
		now:          now,
		monthStart:   monthStart,
		rng:          rand.New(rand.NewSource(20261024)), // #nosec G404 -- deterministic demo data.
		byKey:        make(map[string]domain.Transaction),
		currentSpend: make(map[string]int64),
	}
	for i := 0; i < 6; i++ {
		b.months = append(b.months, monthStart.AddDate(0, i-5, 0))
	}

	connections, accounts, err := b.seedAccounts()
	if err != nil {
		return Dataset{}, err
	}
	if err := b.seedTransactions(); err != nil {
		return Dataset{}, err
	}
	if err := b.seedReconciliations(accounts); err != nil {
		return Dataset{}, err
	}
	alerts, err := b.seedAlerts()
	if err != nil {
		return Dataset{}, err
	}
	receipts, err := b.seedAttachments(attachmentsDir)
	if err != nil {
		return Dataset{}, err
	}
	if err := seedBudgetPlans(ctx, store, now); err != nil {
		return Dataset{}, err
	}
	return Dataset{
		Connections:       connections,
		Accounts:          accounts,
		Transactions:      b.transactions,
		Alerts:            alerts,
		Attachments:       receipts,
		CurrentMonthSpend: b.currentSpend,
	}, nil
}

func (b *builder) seedAccounts() ([]domain.Connection, []domain.Account, error) {
	connectionSpecs := []domain.Connection{
		{
			RequisitionID:   demoConnectionSantander,
			InstitutionID:   "DEMO_SANTANDER",
			InstitutionName: "Banco Santander",
			Status:          "LN",
			Reference:       "demo",
			AgreementID:     "demo-agreement-santander",
		},
		{
			RequisitionID:   demoConnectionRevolut,
			InstitutionID:   "DEMO_REVOLUT",
			InstitutionName: "Revolut Ltd",
			Status:          "LN",
			Reference:       "demo",
			AgreementID:     "demo-agreement-revolut",
		},
		{
			RequisitionID:   demoConnectionBBVA,
			InstitutionID:   "DEMO_BBVA",
			InstitutionName: "BBVA",
			Status:          "LN",
			Reference:       "demo",
			AgreementID:     "demo-agreement-bbva",
		},
		{
			RequisitionID:   demoConnectionWise,
			InstitutionID:   "DEMO_WISE",
			InstitutionName: "Wise",
			Status:          "LN",
			Reference:       "demo",
			AgreementID:     "demo-agreement-wise",
		},
	}
	connections := make([]domain.Connection, 0, len(connectionSpecs))
	connectionIDs := make(map[string]int64, len(connectionSpecs))
	for i := range connectionSpecs {
		connection := connectionSpecs[i]
		if err := b.store.UpsertConnection(b.ctx, &connection); err != nil {
			return nil, nil, fmt.Errorf("demo: seed connection: %w", err)
		}
		if err := b.store.UpdateConnectionSync(b.ctx, connection.ID, b.now, ""); err != nil {
			return nil, nil, fmt.Errorf("demo: seed connection sync: %w", err)
		}
		connections = append(connections, connection)
		connectionIDs[connection.RequisitionID] = connection.ID
	}

	accountSpecs := []accountSpec{
		{
			id: accountSantander, connection: demoConnectionSantander,
			name: "Santander Smart - Personal Checking", iban: "ES9121000418450200051332",
			currency: "EUR", accountTyp: domain.AccountTypePersonal, balance: 345_000,
		},
		{
			id: accountSavings, connection: demoConnectionRevolut,
			name: "Revolut Vault - Emergency Fund", iban: "ES7620770024003102575766",
			currency: "EUR", accountTyp: domain.AccountTypePersonal, balance: 1_250_000,
		},
		{
			id: accountBusiness, connection: demoConnectionBBVA,
			name: "BBVA Empresas - Freelance Operating", iban: "ES7101823000000000000001",
			currency: "EUR", accountTyp: domain.AccountTypeBusiness, balance: 3_000_000,
		},
		{
			id: accountWiseUSD, connection: demoConnectionWise,
			name: "Wise Multi-Currency - USD", iban: "BE71096123456769",
			currency: "USD", accountTyp: domain.AccountTypePersonal, balance: 125_000,
		},
		{
			id: accountWiseGBP, connection: demoConnectionWise,
			name: "Wise Multi-Currency - GBP", iban: "GB33BUKB20201555555555",
			currency: "GBP", accountTyp: domain.AccountTypePersonal, balance: 80_000,
		},
	}
	accounts := make([]domain.Account, 0, len(accountSpecs))
	for _, spec := range accountSpecs {
		account := domain.Account{
			ID: spec.id, ConnectionID: connectionIDs[spec.connection],
			IBAN: spec.iban, Name: spec.name, OwnerName: "Alex Morgan",
			Currency: spec.currency, Type: spec.accountTyp,
			BalanceMinor: spec.balance, BalanceType: "closingBooked",
			BalanceUpdatedAt: &b.now,
		}
		if err := b.store.UpsertAccount(b.ctx, &account); err != nil {
			return nil, nil, fmt.Errorf("demo: seed account %s: %w", spec.id, err)
		}
		accounts = append(accounts, account)
	}
	return connections, accounts, nil
}

func (b *builder) seedTransactions() error {
	for i, month := range b.months {
		monthKey := month.Format("200601")
		if err := b.seedPersonalMonth(month, monthKey, i); err != nil {
			return err
		}
		if err := b.seedBusinessMonth(month, monthKey, i); err != nil {
			return err
		}
	}
	b.seedOneOffTransactions()
	b.ensureCurrentMonthBudgets()
	return b.err
}

func (b *builder) ensureCurrentMonthBudgets() {
	if b.currentSpend["Restaurants & Delivery"] == 0 {
		b.add("current-restaurant", accountSantander, b.now, -4_200, "EUR",
			"Café Central", "Dining out", "Restaurants & Delivery", domain.TransactionStatusBooked, "")
	}
	if b.currentSpend["Groceries"] == 0 {
		b.add("current-groceries", accountSantander, b.now, -6_500, "EUR",
			"Mercadona", "Weekly groceries", "Groceries", domain.TransactionStatusBooked, "")
	}
}

func (b *builder) seedPersonalMonth(month time.Time, monthKey string, monthIndex int) error {
	b.add("netflix-"+monthKey, accountSantander, b.date(month, 3), -1_799, "EUR",
		"Netflix", "Netflix subscription", "Subscriptions", domain.TransactionStatusBooked, "Family plan")
	spotifyAmount := int64(-999)
	if monthIndex >= len(b.months)-2 {
		spotifyAmount = -1_099
	}
	b.add("spotify-"+monthKey, accountSantander, b.date(month, 5), spotifyAmount, "EUR",
		"Spotify", "Spotify Premium", "Subscriptions", domain.TransactionStatusBooked, "")
	b.add("payroll-"+monthKey, accountSantander, b.date(month, 22), 280_000, "EUR",
		"ACME Studio S.L.", "Monthly payroll", "Payroll", domain.TransactionStatusBooked, "")
	b.add("rent-"+monthKey, accountSantander, b.date(month, 1), -85_000, "EUR",
		"Inmobiliaria Centro", "Monthly rent", "Rent", domain.TransactionStatusBooked, "")
	b.add("transfer-out-"+monthKey, accountSantander, b.date(month, 2), -30_000, "EUR",
		"Revolut Vault", "Monthly emergency-fund transfer", "Emergency Fund", domain.TransactionStatusBooked, "")
	b.add("transfer-in-"+monthKey, accountSavings, b.date(month, 2), 30_000, "EUR",
		"Santander Smart", "Monthly emergency-fund transfer", "Transfer", domain.TransactionStatusBooked, "")
	b.add("investment-"+monthKey, accountSantander, b.date(month, 3), -26_000, "EUR",
		"Indexa Capital", "Monthly index fund contribution", "Investments", domain.TransactionStatusBooked, "")

	groceryDays := []int{3, 10, 17}
	for i, day := range groceryDays {
		name := groceryNames[monthIndex*len(groceryDays)+i]
		b.add(fmt.Sprintf("groceries-%d-%s", i, monthKey), accountSantander, b.date(month, day),
			-b.amount(7_500, 10_000), "EUR", name, "Weekly groceries", "Groceries",
			domain.TransactionStatusBooked, "")
	}

	b.add("utilities-1-"+monthKey, accountSantander, b.date(month, 5), -b.amount(5_500, 8_500), "EUR",
		electricityNames[monthIndex], "Electricity bill", "Utilities & Telecom", domain.TransactionStatusBooked, "")
	b.add("utilities-2-"+monthKey, accountSantander, b.date(month, 12), -b.amount(3_800, 6_200), "EUR",
		gasNames[monthIndex], "Gas bill", "Utilities & Telecom", domain.TransactionStatusBooked, "")
	b.add("utilities-3-"+monthKey, accountSantander, b.date(month, 20), -4_990, "EUR",
		"Vodafone", "Fibra + Móvil", "Utilities & Telecom", domain.TransactionStatusBooked, "")

	restaurantDays := []int{4, 9, 14, 19}
	for i, day := range restaurantDays {
		name := restaurantNames[monthIndex*len(restaurantDays)+i]
		b.add(fmt.Sprintf("restaurant-%d-%s", i, monthKey), accountSantander, b.date(month, day),
			-b.amount(7_800, 9_200), "EUR", name, "Dining out", "Restaurants & Delivery",
			domain.TransactionStatusBooked, "")
	}
	b.add("shopping-"+monthKey, accountSantander, b.date(month, 16), -b.amount(28_000, 32_000), "EUR",
		"Amazon", "Household shopping", "Leisure & Shopping", domain.TransactionStatusBooked, "")
	b.add("bizum-"+monthKey, accountSantander, b.date(month, 21), -2_500, "EUR",
		"Bizum Juan Pérez", "Dinner split", "Transfers / P2P", domain.TransactionStatusBooked, "")

	if monthIndex == 1 || monthIndex == 4 {
		b.add("bank-fee-"+monthKey, accountSantander, b.date(month, 1), -1_500, "EUR",
			"Santander", "Comisión mantenimiento de cuenta", "Bank Fees", domain.TransactionStatusBooked, "")
	}
	if monthIndex == 0 || monthIndex == 2 || monthIndex == 4 || monthIndex == 5 {
		b.add("bazar-"+monthKey, accountSantander, b.date(month, 11), -b.amount(1_200, 2_800), "EUR",
			"Bazar Central", "Miscellaneous purchase", "Uncategorized", domain.TransactionStatusBooked, "")
	}

	b.add("copilot-"+monthKey, accountWiseUSD, b.date(month, 7), -1_000, "USD",
		"GitHub Copilot", "GitHub Copilot", "Subscriptions", domain.TransactionStatusBooked, "")
	if monthIndex%2 == 0 {
		b.add("figma-"+monthKey, accountWiseGBP, b.date(month, 14), -1_200, "GBP",
			"Figma", "Figma professional", "Subscriptions", domain.TransactionStatusBooked, "")
	}
	return b.err
}

func (b *builder) seedBusinessMonth(month time.Time, monthKey string, monthIndex int) error {
	invoice := int64(550_000)
	counterparty := "Acme Studio S.L."
	if monthIndex%2 == 1 {
		counterparty = "Northwind Retail"
	}
	b.add("invoice-"+monthKey, accountBusiness, b.date(month, 5), invoice, "EUR",
		counterparty, "Client invoice payment", "Sales", domain.TransactionStatusBooked, "Invoice paid")

	b.add("aws-"+monthKey, accountBusiness, b.date(month, 2), -6_420, "EUR",
		"Amazon Web Services", "AWS Cloud", "Software & Cloud", domain.TransactionStatusBooked, "Production hosting")
	b.add("stripe-"+monthKey, accountBusiness, b.date(month, 7), -8_900, "EUR",
		"Stripe", "Payment processing fees", "Software & Cloud", domain.TransactionStatusBooked, "")
	b.add("cloudflare-"+monthKey, accountBusiness, b.date(month, 9), -2_000, "EUR",
		"Cloudflare", "Cloudflare Pro", "Software & Cloud", domain.TransactionStatusBooked, "")
	b.add("apple-"+monthKey, accountBusiness, b.date(month, 15), -9_900, "EUR",
		"Apple", "Apple Developer Program", "Software & Cloud", domain.TransactionStatusBooked, "")
	b.add("coworking-"+monthKey, accountBusiness, b.date(month, 1), -25_000, "EUR",
		"Impact Hub", "Coworking desk", "Office & Equipment", domain.TransactionStatusBooked, "")
	b.add("accountant-"+monthKey, accountBusiness, b.date(month, 10), -6_000, "EUR",
		"Gestoría Fiscal", "Asesoría fiscal", "Gestoría / Legal", domain.TransactionStatusBooked, "")
	b.add("travel-"+monthKey, accountBusiness, b.date(month, 17), -40_000, "EUR",
		"Iberia", "Client visit flight", "Travel", domain.TransactionStatusBooked, "")
	b.add("meals-"+monthKey, accountBusiness, b.date(month, 20), -15_000, "EUR",
		"Restaurante El Puerto", "Client lunch", "Meals", domain.TransactionStatusBooked, "")
	return b.err
}

func (b *builder) seedOneOffTransactions() {
	b.add("jetbrains", accountBusiness, b.now.AddDate(0, 0, -120), -28_900, "EUR",
		"JetBrains", "All Products Pack yearly license", "Software & Cloud", domain.TransactionStatusBooked, "Annual renewal")
	b.add("jetbrains-previous", accountBusiness, b.now.AddDate(0, 0, -480), -28_900, "EUR",
		"JetBrains", "All Products Pack yearly license", "Software & Cloud", domain.TransactionStatusBooked, "Annual renewal")
	b.add("domain", accountSantander, b.now.AddDate(0, 0, -150), -1_450, "EUR",
		"Namecheap", "Domain registration", "Subscriptions", domain.TransactionStatusBooked, "")
	b.add("domain-previous", accountSantander, b.now.AddDate(0, 0, -510), -1_450, "EUR",
		"Namecheap", "Domain registration", "Subscriptions", domain.TransactionStatusBooked, "")
	b.add("hardware", accountBusiness, b.now.AddDate(0, 0, -120), -199_900, "EUR",
		"Apple Store", "MacBook Pro hardware purchase", "Hardware", domain.TransactionStatusBooked, "Work laptop")
	for i, month := range b.months {
		adobeDate := b.date(month, 12)
		if !adobeDate.IsZero() && adobeDate.Before(b.now.AddDate(0, 0, -60)) {
			b.add(fmt.Sprintf("adobe-%d", i), accountSantander, adobeDate, -5_999, "EUR",
				"Adobe", "Creative Cloud subscription", "Subscriptions", domain.TransactionStatusBooked, "")
		}
	}
	b.add("pending-amazon", accountSantander, b.now.AddDate(0, 0, -2), -4_250, "EUR",
		"Amazon", "Pending card authorization", "Shopping", domain.TransactionStatusPending, "")
	b.add("pending-uber", accountSantander, b.now.AddDate(0, 0, -1), -1_850, "EUR",
		"Uber", "Pending ride", "Transport", domain.TransactionStatusPending, "")
}

func (b *builder) seedReconciliations(accounts []domain.Account) error {
	for _, account := range accounts {
		var booked []domain.Transaction
		for _, tx := range b.transactions {
			if tx.AccountID == account.ID && tx.Status == domain.TransactionStatusBooked {
				booked = append(booked, tx)
			}
		}
		sort.Slice(booked, func(i, j int) bool {
			return booked[i].BookingDate.Before(booked[j].BookingDate)
		})
		anchor := b.now.AddDate(0, 0, -181)
		if len(booked) > 0 {
			first := booked[0].BookingDate.UTC()
			anchor = time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
		}
		var movement int64
		for _, tx := range booked {
			if !tx.BookingDate.Before(anchor) {
				movement += tx.AmountMinor
			}
		}
		rec := domain.Reconciliation{
			AccountID:           account.ID,
			Currency:            account.Currency,
			OpeningBalanceMinor: account.BalanceMinor - movement,
			AnchorAt:            anchor,
			LastCheckedAt:       &b.now,
			Status:              domain.ReconciliationStatusReconciled,
			BaselineEstablished: true,
			UpdatedAt:           b.now,
		}
		if account.ID == accountSantander {
			start := b.now.AddDate(0, 0, -40)
			end := b.now.AddDate(0, 0, -35)
			rec.DiscrepancyStart = &start
			rec.DiscrepancyEnd = &end
		}
		if err := b.store.SaveReconciliation(b.ctx, &rec); err != nil {
			return fmt.Errorf("demo: seed reconciliation: %w", err)
		}
	}
	return nil
}

func (b *builder) seedAlerts() ([]domain.Alert, error) {
	hardwareID := b.transactionID("hardware")
	santander := "Santander Smart - Personal Checking"
	business := "BBVA Empresas - Freelance Operating"
	specs := []domain.Alert{
		{
			Rule: "subscription_price_increase", Severity: domain.SeverityWarning,
			Title: "Subscription price increase", AccountID: accountSantander,
			Message:  "Spotify increased from 9.99 EUR to 10.99 EUR per month.",
			DedupKey: "demo:spotify-price-increase", CreatedAt: b.now.AddDate(0, 0, -3),
		},
		{
			Rule: "bank_fee", Severity: domain.SeverityInfo,
			Title: "Bank fee detected", AccountID: accountSantander,
			Message:  "Comisión mantenimiento de cuenta: 15.00 EUR on " + santander + ".",
			DedupKey: "demo:bank-fee", CreatedAt: b.now.AddDate(0, 0, -8),
		},
		{
			Rule: "budget", Severity: domain.SeverityWarning,
			Title: "Restaurants budget at 85%", AccountID: accountSantander,
			Message:  "Restaurants spending reached the 80% warning threshold for this month.",
			DedupKey: "demo:budget-restaurants", CreatedAt: b.now.AddDate(0, 0, -1),
		},
		{
			Rule: "integrity", Severity: domain.SeverityInfo,
			Title: "Balance reconciled", AccountID: accountSantander,
			Message:  santander + " matches the booked ledger after the latest sync.",
			DedupKey: "demo:reconciled", CreatedAt: b.now.AddDate(0, 0, -2),
		},
		{
			Rule: "integrity", Severity: domain.SeverityInfo,
			Title: "Historical discrepancy resolved", AccountID: accountBusiness,
			Message:  business + ": a 42.10 EUR discrepancy was reviewed and resolved.",
			DedupKey: "demo:discrepancy-resolved", CreatedAt: b.now.AddDate(0, 0, -20),
		},
		{
			Rule: "missing_receipt", Severity: domain.SeverityWarning,
			Title: "Missing receipt", AccountID: accountBusiness, TransactionID: hardwareID,
			Message:  "A hardware purchase over 50 EUR still has no receipt attached.",
			DedupKey: "demo:missing-receipt", CreatedAt: b.now.AddDate(0, 0, -5),
		},
	}
	alerts := make([]domain.Alert, 0, len(specs))
	for i := range specs {
		alert := specs[i]
		if i < len(specs)-1 {
			delivered := alert.CreatedAt.Add(2 * time.Minute)
			alert.DeliveredAt = &delivered
		}
		if _, err := b.store.CreateAlert(b.ctx, &alert); err != nil {
			return nil, fmt.Errorf("demo: seed alert: %w", err)
		}
		alerts = append(alerts, alert)
	}
	return alerts, nil
}

func (b *builder) seedAttachments(dir string) ([]domain.Attachment, error) {
	service, err := attachments.New(b.store, dir, 10<<20, func() time.Time { return b.now })
	if err != nil {
		return nil, fmt.Errorf("demo: attachment service: %w", err)
	}
	latestMonth := b.months[len(b.months)-1].Format("200601")
	specs := []struct {
		transactionKey string
		filename       string
		contentType    string
		content        []byte
	}{
		{
			transactionKey: "aws-" + latestMonth,
			filename:       "aws-invoice-oct2026.pdf",
			contentType:    "application/pdf",
			content:        demoPDF("AWS Cloud invoice - October 2026"),
		},
		{
			transactionKey: "hardware",
			filename:       "macbook-receipt.jpg",
			contentType:    "image/jpeg",
			content:        demoJPEG(),
		},
		{
			transactionKey: "jetbrains",
			filename:       "jetbrains-license.pdf",
			contentType:    "application/pdf",
			content:        demoPDF("JetBrains All Products Pack license"),
		},
	}
	out := make([]domain.Attachment, 0, len(specs)+1)
	for _, spec := range specs {
		tx, ok := b.byKey[spec.transactionKey]
		if !ok {
			return nil, fmt.Errorf("demo: attachment transaction %q not found", spec.transactionKey)
		}
		att, err := service.Save(b.ctx, attachments.SaveInput{
			Reader:        bytes.NewReader(spec.content),
			Filename:      spec.filename,
			ContentType:   spec.contentType,
			TransactionID: tx.ID,
			Source:        "demo",
		})
		if err != nil {
			return nil, fmt.Errorf("demo: seed paired attachment %s: %w", spec.filename, err)
		}
		out = append(out, att)
	}
	unpaired, err := service.Save(b.ctx, attachments.SaveInput{
		Reader:      bytes.NewReader(demoPDF("Hotel Alameda - pending receipt")),
		Filename:    "hotel-ticket.pdf",
		ContentType: "application/pdf",
		Source:      "demo",
	})
	if err != nil {
		return nil, fmt.Errorf("demo: seed unpaired attachment: %w", err)
	}
	out = append(out, unpaired)
	return out, nil
}

func (b *builder) add(key, accountID string, date time.Time, amount int64, currency, counterparty, description, category, status, notes string) {
	if b.err != nil || date.IsZero() || date.After(b.now) {
		return
	}
	b.sequence++
	if key == "" {
		key = fmt.Sprintf("tx-%03d", b.sequence)
	}
	externalID := "demo-" + key
	tx := domain.Transaction{
		AccountID:    accountID,
		ExternalID:   externalID,
		AmountMinor:  amount,
		Currency:     currency,
		BookingDate:  date.UTC(),
		Counterparty: counterparty,
		Description:  description,
		Category:     category,
		Notes:        notes,
		Status:       status,
	}
	if _, _, err := b.store.UpsertTransaction(b.ctx, &tx); err != nil {
		b.err = fmt.Errorf("demo: seed transaction %s: %w", key, err)
		return
	}
	b.transactions = append(b.transactions, tx)
	b.byKey[key] = tx
	if status == domain.TransactionStatusBooked && amount < 0 && !tx.BookingDate.Before(b.monthStart) {
		b.currentSpend[category] += -amount
	}
}

func (b *builder) date(month time.Time, day int) time.Time {
	date := time.Date(month.Year(), month.Month(), day, 10, 0, 0, 0, time.UTC)
	if date.After(b.now) {
		return time.Time{}
	}
	return date
}

func (b *builder) amount(minimum, maximum int64) int64 {
	return minimum + b.rng.Int63n(maximum-minimum+1)
}

func (b *builder) transactionID(key string) *int64 {
	tx, ok := b.byKey[key]
	if !ok || tx.ID == 0 {
		return nil
	}
	id := tx.ID
	return &id
}

func demoPDF(title string) []byte {
	stream := fmt.Sprintf("BT /F1 18 Tf 72 760 Td (%s) Tj ET", title)
	content := fmt.Sprintf("%%PDF-1.4\n"+
		"1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n"+
		"2 0 obj<</Type/Pages/Count 1/Kids[3 0 R]>>endobj\n"+
		"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 595 842]/Resources<</Font<</F1 5 0 R>>>>/Contents 4 0 R>>endobj\n"+
		"4 0 obj<</Length %d>>stream\n%s\nendstream\nendobj\n"+
		"5 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj\n"+
		"trailer<</Root 1 0 R>>\n%%%%EOF\n", len(stream), stream)
	return []byte(content)
}

func demoJPEG() []byte {
	imageData := image.NewRGBA(image.Rect(0, 0, 640, 840))
	white := color.RGBA{R: 248, G: 246, B: 240, A: 255}
	ink := color.RGBA{R: 28, G: 34, B: 42, A: 255}
	accent := color.RGBA{R: 30, G: 122, B: 90, A: 255}
	for y := 0; y < 840; y++ {
		for x := 0; x < 640; x++ {
			imageData.SetRGBA(x, y, white)
		}
	}
	for y := 100; y < 160; y++ {
		for x := 60; x < 580; x++ {
			imageData.SetRGBA(x, y, accent)
		}
	}
	for line := 0; line < 12; line++ {
		y := 220 + line*38
		for x := 60; x < 580-line*12; x++ {
			imageData.SetRGBA(x, y, ink)
		}
	}
	var buf bytes.Buffer
	// #nosec G104 -- bytes.Buffer writes cannot fail.
	_ = jpeg.Encode(&buf, imageData, &jpeg.Options{Quality: 82})
	return buf.Bytes()
}

func seedBudgetPlans(ctx context.Context, store *storage.Store, now time.Time) error {
	personal := domain.BudgetProfile{
		Workspace: domain.WorkspacePersonal, Currency: "EUR",
		NeedsBPS: 5000, WantsBPS: 3000, SavingsBPS: 2000,
		AdvisoryEnabled: true, UpdatedAt: now,
	}
	personalAllocations := []domain.BudgetAllocation{
		{Category: "Rent", Pillar: "needs", PercentBPS: 3036, NotifyAtPercent: []int{80, 100}, SortOrder: 0},
		{Category: "Groceries", Pillar: "needs", PercentBPS: 1250, NotifyAtPercent: []int{80, 100}, SortOrder: 1},
		{Category: "Utilities & Telecom", Pillar: "needs", PercentBPS: 714, NotifyAtPercent: []int{80, 100}, SortOrder: 2},
		{Category: "Restaurants & Delivery", Pillar: "wants", PercentBPS: 1071, NotifyAtPercent: []int{80, 100}, SortOrder: 3},
		{Category: "Subscriptions", Pillar: "wants", PercentBPS: 321, NotifyAtPercent: []int{80, 100}, SortOrder: 4},
		{Category: "Leisure & Shopping", Pillar: "wants", PercentBPS: 1608, NotifyAtPercent: []int{80, 100}, SortOrder: 5},
		{Category: "Emergency Fund", Pillar: "savings", PercentBPS: 1071, NotifyAtPercent: []int{80, 100}, SortOrder: 6},
		{Category: "Investments", Pillar: "savings", PercentBPS: 929, NotifyAtPercent: []int{80, 100}, SortOrder: 7},
	}
	for i := range personalAllocations {
		personalAllocations[i].Workspace = domain.WorkspacePersonal
		personalAllocations[i].Currency = "EUR"
		personalAllocations[i].UpdatedAt = now
	}
	if err := store.SaveBudgetPlan(ctx, personal, personalAllocations); err != nil {
		return fmt.Errorf("demo: seed personal budget plan: %w", err)
	}

	business := domain.BudgetProfile{
		Workspace: domain.WorkspaceBusiness, Currency: "EUR",
		NeedsBPS: 1727, WantsBPS: 0, SavingsBPS: 2100,
		AdvisoryEnabled: true, UpdatedAt: now,
	}
	businessAllocations := []domain.BudgetAllocation{
		{Category: "Software & Cloud", Pillar: "needs", PercentBPS: 818, NotifyAtPercent: []int{80, 100}, SortOrder: 0},
		{Category: "Gestoría / Legal", Pillar: "needs", PercentBPS: 364, NotifyAtPercent: []int{80, 100}, SortOrder: 1},
		{Category: "Office & Equipment", Pillar: "needs", PercentBPS: 545, NotifyAtPercent: []int{80, 100}, SortOrder: 2},
		{Category: "Tax Reserve", Pillar: "savings", PercentBPS: 2100, NotifyAtPercent: []int{80, 100}, SortOrder: 3},
	}
	for i := range businessAllocations {
		businessAllocations[i].Workspace = domain.WorkspaceBusiness
		businessAllocations[i].Currency = "EUR"
		businessAllocations[i].UpdatedAt = now
	}
	if err := store.SaveBudgetPlan(ctx, business, businessAllocations); err != nil {
		return fmt.Errorf("demo: seed business budget plan: %w", err)
	}
	return nil
}
