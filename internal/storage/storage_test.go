package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "fiscus-test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func seedConnection(t *testing.T, s *Store) domain.Connection {
	t.Helper()
	c := domain.Connection{
		RequisitionID:   "req-1",
		InstitutionID:   "INST_1",
		InstitutionName: "Test Bank",
		Status:          "LN",
		Link:            "https://example.test/link",
		Reference:       "ref",
	}
	if err := s.UpsertConnection(context.Background(), &c); err != nil {
		t.Fatalf("UpsertConnection: %v", err)
	}
	return c
}

func seedAccount(t *testing.T, s *Store, connID int64) domain.Account {
	t.Helper()
	a := domain.Account{
		ID:           "acc-1",
		ConnectionID: connID,
		IBAN:         "ES0000000000000000000001",
		Name:         "Everyday",
		OwnerName:    "Ada Lovelace",
		Currency:     "EUR",
	}
	if err := s.UpsertAccount(context.Background(), &a); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	return a
}

func TestMigrateIdempotent(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if version < 1 {
		t.Fatalf("schema version = %d, want >= 1", version)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	version2, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion after second migrate: %v", err)
	}
	if version2 != version {
		t.Fatalf("schema version changed from %d to %d", version, version2)
	}
}

func TestOpenMemory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open memory: %v", err)
	}
	defer func() { _ = s.Close() }()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate memory: %v", err)
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestConnectionLifecycle(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	c := seedConnection(t, s)
	if c.ID == 0 {
		t.Fatal("connection ID was not populated")
	}

	got, err := s.GetConnectionByRequisitionID(ctx, "req-1")
	if err != nil {
		t.Fatalf("GetConnectionByRequisitionID: %v", err)
	}
	if got.InstitutionName != "Test Bank" || got.Status != "LN" {
		t.Fatalf("unexpected connection: %+v", got)
	}

	got.Link = "https://example.test/new"
	expiresAt := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	got.ConsentExpiresAt = &expiresAt
	if err := s.UpsertConnection(ctx, &got); err != nil {
		t.Fatalf("update connection: %v", err)
	}
	byID, err := s.GetConnectionByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetConnectionByID: %v", err)
	}
	if byID.Link != "https://example.test/new" {
		t.Fatalf("link = %q", byID.Link)
	}
	if byID.ConsentExpiresAt == nil || !byID.ConsentExpiresAt.Equal(expiresAt) {
		t.Fatalf("ConsentExpiresAt = %v, want %v", byID.ConsentExpiresAt, expiresAt)
	}

	renewedAt := expiresAt.AddDate(0, 6, 0)
	if err := s.UpdateConnectionConsentExpiry(ctx, c.ID, &renewedAt); err != nil {
		t.Fatalf("UpdateConnectionConsentExpiry: %v", err)
	}
	byID, err = s.GetConnectionByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetConnectionByID: %v", err)
	}
	if byID.ConsentExpiresAt == nil || !byID.ConsentExpiresAt.Equal(renewedAt) {
		t.Fatalf("renewed ConsentExpiresAt = %v, want %v", byID.ConsentExpiresAt, renewedAt)
	}
	if err := s.UpdateConnectionConsentExpiry(ctx, 9999, &renewedAt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateConnectionConsentExpiry missing error = %v, want ErrNotFound", err)
	}

	syncedAt := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	if err := s.UpdateConnectionSync(ctx, c.ID, syncedAt, "temporary error"); err != nil {
		t.Fatalf("UpdateConnectionSync: %v", err)
	}
	byID, err = s.GetConnectionByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetConnectionByID: %v", err)
	}
	if byID.LastSyncedAt == nil || !byID.LastSyncedAt.Equal(syncedAt) {
		t.Fatalf("LastSyncedAt = %v, want %v", byID.LastSyncedAt, syncedAt)
	}
	if byID.LastError != "temporary error" {
		t.Fatalf("LastError = %q", byID.LastError)
	}

	list, err := s.ListConnections(ctx)
	if err != nil {
		t.Fatalf("ListConnections: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("connections = %d, want 1", len(list))
	}

	if err := s.UpdateConnectionSync(ctx, 9999, syncedAt, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateConnectionSync missing error = %v, want ErrNotFound", err)
	}
}

func TestConnectionAuthorizationLifecycle(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	c := seedConnection(t, s)
	if err := s.UpdateConnectionSync(ctx, c.ID, time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC), "pending"); err != nil {
		t.Fatalf("UpdateConnectionSync: %v", err)
	}

	byRef, err := s.GetConnectionByReference(ctx, "ref")
	if err != nil {
		t.Fatalf("GetConnectionByReference: %v", err)
	}
	if byRef.ID != c.ID {
		t.Fatalf("connection ID = %d, want %d", byRef.ID, c.ID)
	}
	if err := s.UpdateConnectionAuthorization(ctx, c.ID, "session-1", "LN",
		"https://auth.example/start", "Banco Santander", "Santander", "authorization-1"); err != nil {
		t.Fatalf("UpdateConnectionAuthorization: %v", err)
	}

	got, err := s.GetConnectionByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetConnectionByID: %v", err)
	}
	if got.RequisitionID != "session-1" || got.Status != "LN" ||
		got.InstitutionID != "Banco Santander" || got.InstitutionName != "Santander" ||
		got.AgreementID != "authorization-1" || got.LastError != "" {
		t.Fatalf("updated connection = %+v", got)
	}
	if _, err := s.GetConnectionByReference(ctx, "ref"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reference should be cleared, error = %v", err)
	}
	if _, err := s.GetConnectionByReference(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty reference error = %v, want ErrNotFound", err)
	}
	if err := s.UpdateConnectionAuthorization(ctx, 9999, "session-x", "LN", "", "", "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing connection error = %v, want ErrNotFound", err)
	}
}

func TestAccountLifecycle(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	c := seedConnection(t, s)
	a := seedAccount(t, s, c.ID)

	got, err := s.GetAccount(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.OwnerName != "Ada Lovelace" || got.Currency != "EUR" {
		t.Fatalf("unexpected account: %+v", got)
	}

	balanceAt := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	if err := s.UpdateAccountBalance(ctx, a.ID, 123456, "interimAvailable", balanceAt); err != nil {
		t.Fatalf("UpdateAccountBalance: %v", err)
	}
	got, err = s.GetAccount(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.BalanceMinor != 123456 || got.BalanceType != "interimAvailable" {
		t.Fatalf("balance = %d %s", got.BalanceMinor, got.BalanceType)
	}
	if got.BalanceUpdatedAt == nil || !got.BalanceUpdatedAt.Equal(balanceAt) {
		t.Fatalf("BalanceUpdatedAt = %v", got.BalanceUpdatedAt)
	}

	// Metadata refresh must not clobber the cached balance.
	got.Name = "Renamed"
	if err := s.UpsertAccount(ctx, &got); err != nil {
		t.Fatalf("UpsertAccount refresh: %v", err)
	}
	refreshed, err := s.GetAccount(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if refreshed.BalanceMinor != 123456 {
		t.Fatalf("balance after metadata refresh = %d, want 123456", refreshed.BalanceMinor)
	}
	if refreshed.Name != "Renamed" {
		t.Fatalf("name = %q", refreshed.Name)
	}

	if err := s.UpdateAccountBalance(ctx, "missing", 1, "x", balanceAt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateAccountBalance missing error = %v, want ErrNotFound", err)
	}
	accounts, err := s.ListAccounts(ctx)
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("accounts = %d, want 1", len(accounts))
	}
	byConn, err := s.ListAccountsByConnection(ctx, c.ID)
	if err != nil {
		t.Fatalf("ListAccountsByConnection: %v", err)
	}
	if len(byConn) != 1 {
		t.Fatalf("accounts by connection = %d, want 1", len(byConn))
	}
}

func TestRateLimitCooldownPersistence(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	connection := seedConnection(t, s)
	account := seedAccount(t, s, connection.ID)

	until := time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC)
	if err := s.SetConnectionRateLimit(ctx, connection.ID, until, "ASPSP_RATE_LIMIT_EXCEEDED"); err != nil {
		t.Fatalf("SetConnectionRateLimit: %v", err)
	}
	if err := s.SetAccountRateLimit(ctx, account.ID, until, "ASPSP_RATE_LIMIT_EXCEEDED"); err != nil {
		t.Fatalf("SetAccountRateLimit: %v", err)
	}

	gotConnection, err := s.GetConnectionByID(ctx, connection.ID)
	if err != nil {
		t.Fatalf("GetConnectionByID: %v", err)
	}
	if gotConnection.RateLimitedUntil == nil || !gotConnection.RateLimitedUntil.Equal(until) {
		t.Fatalf("connection cooldown = %v, want %v", gotConnection.RateLimitedUntil, until)
	}
	if gotConnection.LastRateLimitReason != "ASPSP_RATE_LIMIT_EXCEEDED" {
		t.Fatalf("connection reason = %q", gotConnection.LastRateLimitReason)
	}
	gotAccount, err := s.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if gotAccount.RateLimitedUntil == nil || !gotAccount.RateLimitedUntil.Equal(until) {
		t.Fatalf("account cooldown = %v, want %v", gotAccount.RateLimitedUntil, until)
	}
	if gotAccount.LastRateLimitReason != "ASPSP_RATE_LIMIT_EXCEEDED" {
		t.Fatalf("account reason = %q", gotAccount.LastRateLimitReason)
	}

	if err := s.SetConnectionRateLimit(ctx, 9999, until, "ASPSP_RATE_LIMIT_EXCEEDED"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing connection error = %v, want ErrNotFound", err)
	}
	if err := s.SetAccountRateLimit(ctx, "missing", until, "ASPSP_RATE_LIMIT_EXCEEDED"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing account error = %v, want ErrNotFound", err)
	}

	if err := s.ClearConnectionRateLimit(ctx, connection.ID); err != nil {
		t.Fatalf("ClearConnectionRateLimit: %v", err)
	}
	if err := s.ClearAccountRateLimit(ctx, account.ID); err != nil {
		t.Fatalf("ClearAccountRateLimit: %v", err)
	}
	gotConnection, err = s.GetConnectionByID(ctx, connection.ID)
	if err != nil {
		t.Fatalf("GetConnectionByID after clear: %v", err)
	}
	gotAccount, err = s.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatalf("GetAccount after clear: %v", err)
	}
	if gotConnection.RateLimitedUntil != nil || gotConnection.LastRateLimitReason != "" {
		t.Fatalf("connection cooldown was not cleared: %+v", gotConnection)
	}
	if gotAccount.RateLimitedUntil != nil || gotAccount.LastRateLimitReason != "" {
		t.Fatalf("account cooldown was not cleared: %+v", gotAccount)
	}
}

func TestMetadataUpsertsPreserveRateLimitCooldown(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	connection := seedConnection(t, s)
	account := seedAccount(t, s, connection.ID)
	until := time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC)
	if err := s.SetConnectionRateLimit(ctx, connection.ID, until, "HTTP 429"); err != nil {
		t.Fatalf("SetConnectionRateLimit: %v", err)
	}
	if err := s.SetAccountRateLimit(ctx, account.ID, until, "HTTP 429"); err != nil {
		t.Fatalf("SetAccountRateLimit: %v", err)
	}

	connection.InstitutionName = "Renamed Bank"
	if err := s.UpsertConnection(ctx, &connection); err != nil {
		t.Fatalf("UpsertConnection: %v", err)
	}
	account.Name = "Renamed Account"
	if err := s.UpsertAccount(ctx, &account); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	gotConnection, err := s.GetConnectionByID(ctx, connection.ID)
	if err != nil {
		t.Fatalf("GetConnectionByID: %v", err)
	}
	gotAccount, err := s.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if gotConnection.RateLimitedUntil == nil || !gotConnection.RateLimitedUntil.Equal(until) {
		t.Fatalf("connection cooldown was overwritten: %+v", gotConnection)
	}
	if gotAccount.RateLimitedUntil == nil || !gotAccount.RateLimitedUntil.Equal(until) {
		t.Fatalf("account cooldown was overwritten: %+v", gotAccount)
	}
}

func TestTransactionIdempotencyAndPromotion(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	c := seedConnection(t, s)
	seedAccount(t, s, c.ID)

	booking := time.Date(2026, 3, 3, 9, 30, 0, 0, time.UTC)
	tx := domain.Transaction{
		AccountID:    "acc-1",
		ExternalID:   "tx-1",
		AmountMinor:  -2599,
		Currency:     "EUR",
		BookingDate:  booking,
		Counterparty: "Coffee Shop",
		Description:  "Flat white",
		Category:     "Dining",
		Status:       domain.TransactionStatusPending,
	}
	inserted, promoted, err := s.UpsertTransaction(ctx, &tx)
	if err != nil {
		t.Fatalf("UpsertTransaction: %v", err)
	}
	if !inserted || promoted {
		t.Fatalf("first upsert inserted=%v promoted=%v", inserted, promoted)
	}
	if tx.ID == 0 {
		t.Fatal("transaction ID was not populated")
	}

	// Same external ID is idempotent, and pending -> booked is a promotion.
	tx.Status = domain.TransactionStatusBooked
	inserted, promoted, err = s.UpsertTransaction(ctx, &tx)
	if err != nil {
		t.Fatalf("UpsertTransaction promotion: %v", err)
	}
	if inserted || !promoted {
		t.Fatalf("promotion inserted=%v promoted=%v", inserted, promoted)
	}

	tx.Description = "updated"
	inserted, promoted, err = s.UpsertTransaction(ctx, &tx)
	if err != nil {
		t.Fatalf("UpsertTransaction update: %v", err)
	}
	if inserted || promoted {
		t.Fatalf("repeat upsert inserted=%v promoted=%v", inserted, promoted)
	}

	list, err := s.ListTransactions(ctx, TransactionFilter{AccountID: "acc-1"})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("transactions = %d, want 1", len(list))
	}
	if list[0].Description != "updated" || list[0].Status != domain.TransactionStatusBooked || list[0].Category != "Dining" {
		t.Fatalf("unexpected transaction: %+v", list[0])
	}
	if !list[0].CreatedAt.Equal(tx.CreatedAt) {
		t.Fatalf("created_at was not preserved")
	}

	count, err := s.CountTransactions(ctx)
	if err != nil {
		t.Fatalf("CountTransactions: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	recent, err := s.RecentTransactions(ctx, 10)
	if err != nil {
		t.Fatalf("RecentTransactions: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("recent = %d, want 1", len(recent))
	}
	bookedOnly, err := s.RecentBookedTransactions(ctx, 10)
	if err != nil {
		t.Fatalf("RecentBookedTransactions: %v", err)
	}
	if len(bookedOnly) != 1 || bookedOnly[0].Status != domain.TransactionStatusBooked {
		t.Fatalf("recent booked = %+v", bookedOnly)
	}
}

func TestSpendingByCategory(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	c := seedConnection(t, s)
	seedAccount(t, s, c.ID)

	day := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		{AccountID: "acc-1", ExternalID: "spend-1", AmountMinor: -1000, Currency: "EUR", BookingDate: day, Category: "Utilities", Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "spend-2", AmountMinor: -2500, Currency: "EUR", BookingDate: day.Add(24 * time.Hour), Category: "Utilities", Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "spend-3", AmountMinor: -500, Currency: "EUR", BookingDate: day, Category: "Dining", Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "spend-4", AmountMinor: 9000, Currency: "EUR", BookingDate: day, Category: "Payroll", Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "spend-5", AmountMinor: -700, Currency: "EUR", BookingDate: day.AddDate(0, 1, 0), Category: "Utilities", Status: domain.TransactionStatusBooked},
	}
	for i := range transactions {
		if _, _, err := s.UpsertTransaction(ctx, &transactions[i]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}

	spending, err := s.SpendingByCategory(ctx, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("SpendingByCategory: %v", err)
	}
	if len(spending) != 2 {
		t.Fatalf("spending = %+v, want 2 rows", spending)
	}
	got := map[string]int64{}
	for _, item := range spending {
		got[item.Category] = item.Minor
	}
	if got["Utilities"] != 3500 || got["Dining"] != 500 {
		t.Fatalf("spending = %+v", spending)
	}
}

func TestTransactionValidationAndForeignKeys(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	if _, _, err := s.UpsertTransaction(ctx, &domain.Transaction{}); err == nil {
		t.Fatal("expected error for missing account")
	}
	_, _, err := s.UpsertTransaction(ctx, &domain.Transaction{
		AccountID: "missing", ExternalID: "x", Currency: "EUR",
		BookingDate: time.Now(), Status: domain.TransactionStatusBooked,
	})
	if err == nil {
		t.Fatal("expected foreign key error for unknown account")
	}
}

func TestDuplicateCandidatesAndDebitStats(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	c := seedConnection(t, s)
	seedAccount(t, s, c.ID)

	base := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)
	seed := func(external string, amount int64, counterparty string, at time.Time) domain.Transaction {
		t.Helper()
		tx := domain.Transaction{
			AccountID:    "acc-1",
			ExternalID:   external,
			AmountMinor:  amount,
			Currency:     "EUR",
			BookingDate:  at,
			Counterparty: counterparty,
			Status:       domain.TransactionStatusBooked,
		}
		if _, _, err := s.UpsertTransaction(ctx, &tx); err != nil {
			t.Fatalf("seed %s: %v", external, err)
		}
		return tx
	}

	first := seed("tx-1", -4200, "Power Co", base)
	seed("tx-2", -4200, "  power co ", base.Add(2*time.Hour))
	seed("tx-3", -4200, "Power Co", base.Add(48*time.Hour))
	seed("tx-4", -1500, "Power Co", base.Add(time.Hour))
	seed("tx-5", 4200, "Refund Co", base.Add(time.Hour))

	dups, err := s.FindDuplicateCandidates(ctx, "acc-1", "Power Co", -4200,
		base.Add(-24*time.Hour), base.Add(24*time.Hour), first.ID)
	if err != nil {
		t.Fatalf("FindDuplicateCandidates: %v", err)
	}
	if len(dups) != 1 || dups[0].ExternalID != "tx-2" {
		t.Fatalf("duplicates = %+v, want tx-2 only", dups)
	}

	total, count, err := s.DebitStats(ctx, "acc-1",
		base.Add(-30*24*time.Hour), base.Add(24*time.Hour), first.ID)
	if err != nil {
		t.Fatalf("DebitStats: %v", err)
	}
	// tx-2 (-4200) and tx-4 (-1500); tx-5 is a credit and tx-3 is out of range.
	if count != 2 || total != 5700 {
		t.Fatalf("DebitStats = (%d, %d), want (5700, 2)", total, count)
	}
}

func TestAlerts(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	alert := domain.Alert{
		Rule:      "duplicate",
		Severity:  domain.SeverityWarning,
		Title:     "Duplicate charge",
		Message:   "Charged twice",
		AccountID: "acc-1",
		DedupKey:  "dup:acc-1:tx-2",
	}
	inserted, err := s.CreateAlert(ctx, &alert)
	if err != nil {
		t.Fatalf("CreateAlert: %v", err)
	}
	if !inserted || alert.ID == 0 {
		t.Fatalf("inserted=%v id=%d", inserted, alert.ID)
	}
	inserted, err = s.CreateAlert(ctx, &domain.Alert{
		Rule: "duplicate", Severity: domain.SeverityWarning, DedupKey: "dup:acc-1:tx-2",
	})
	if err != nil {
		t.Fatalf("CreateAlert duplicate: %v", err)
	}
	if inserted {
		t.Fatal("dedup key should have prevented a second insert")
	}

	deliveredAt := time.Now().UTC().Round(0)
	if err := s.MarkAlertDelivered(ctx, alert.ID, deliveredAt); err != nil {
		t.Fatalf("MarkAlertDelivered: %v", err)
	}
	got, err := s.GetAlert(ctx, alert.ID)
	if err != nil {
		t.Fatalf("GetAlert: %v", err)
	}
	if !got.Delivered() || got.DeliveredAt == nil {
		t.Fatal("alert should be marked delivered")
	}
	if err := s.MarkAlertDeliveryFailed(ctx, alert.ID, "telegram: 500"); err != nil {
		t.Fatalf("MarkAlertDeliveryFailed: %v", err)
	}
	got, err = s.GetAlert(ctx, alert.ID)
	if err != nil {
		t.Fatalf("GetAlert: %v", err)
	}
	if got.Delivered() || got.DeliveryError != "telegram: 500" {
		t.Fatalf("unexpected alert state: %+v", got)
	}

	list, err := s.ListAlerts(ctx, 10)
	if err != nil {
		t.Fatalf("ListAlerts: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("alerts = %d, want 1", len(list))
	}
	count, err := s.CountAlerts(ctx)
	if err != nil {
		t.Fatalf("CountAlerts: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	if err := s.MarkAlertDelivered(ctx, 999, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("MarkAlertDelivered missing = %v, want ErrNotFound", err)
	}
}

func TestTimestampOrderingPrecision(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	c := seedConnection(t, s)
	ctx := context.Background()
	seedAccount(t, s, c.ID)

	base := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)
	for i, at := range []time.Time{base, base.Add(500 * time.Millisecond), base.Add(time.Second)} {
		tx := domain.Transaction{
			AccountID: "acc-1", ExternalID: string(rune('a' + i)), AmountMinor: -1,
			Currency: "EUR", BookingDate: at, Status: domain.TransactionStatusBooked,
		}
		if _, _, err := s.UpsertTransaction(ctx, &tx); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	list, err := s.ListTransactions(ctx, TransactionFilter{})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("len = %d", len(list))
	}
	if !list[0].BookingDate.Equal(base.Add(time.Second)) {
		t.Fatalf("newest = %v", list[0].BookingDate)
	}
	if !list[2].BookingDate.Equal(base) {
		t.Fatalf("oldest = %v", list[2].BookingDate)
	}
}

func TestAccountWorkspaceTypeLifecycle(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	conn := seedConnection(t, s)
	account := seedAccount(t, s, conn.ID)
	if account.Type != "" {
		// The caller's struct is not mutated by UpsertAccount.
		t.Fatalf("insert seed type = %q, want empty", account.Type)
	}
	stored, err := s.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if stored.Type != domain.AccountTypePersonal {
		t.Fatalf("default type = %q, want personal", stored.Type)
	}
	if err := s.SetAccountType(ctx, account.ID, domain.AccountTypeBusiness); err != nil {
		t.Fatalf("SetAccountType: %v", err)
	}
	stored, err = s.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if stored.Type != domain.AccountTypeBusiness || !stored.TypeManual {
		t.Fatalf("type = %q manual=%v, want business/manual", stored.Type, stored.TypeManual)
	}
	if err := s.ApplyConfigAccountType(ctx, account.ID, domain.AccountTypePersonal); err != nil {
		t.Fatalf("ApplyConfigAccountType: %v", err)
	}
	stored, err = s.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatalf("GetAccount after config apply: %v", err)
	}
	if stored.Type != domain.AccountTypeBusiness {
		t.Fatalf("manual type was overwritten by config: %q", stored.Type)
	}
	// Metadata-only upserts (Type empty) must not overwrite the mapping.
	stored.Name = "Renamed"
	if err := s.UpsertAccount(ctx, &stored); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	stored.Type = ""
	if err := s.UpsertAccount(ctx, &stored); err != nil {
		t.Fatalf("UpsertAccount without type: %v", err)
	}
	reloaded, err := s.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if reloaded.Type != domain.AccountTypeBusiness {
		t.Fatalf("type after metadata upsert = %q", reloaded.Type)
	}
	if err := s.SetAccountType(ctx, account.ID, "shared"); err == nil {
		t.Fatal("expected invalid account type error")
	}
	if err := s.SetAccountType(ctx, "missing", domain.AccountTypePersonal); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing account error = %v", err)
	}
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if version < 6 {
		t.Fatalf("schema version = %d, want >= 6", version)
	}
}

func TestApplyConfigAccountTypeLeavesManualOverrideAlone(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	conn := seedConnection(t, s)
	account := seedAccount(t, s, conn.ID)
	if err := s.ApplyConfigAccountType(ctx, account.ID, domain.AccountTypeBusiness); err != nil {
		t.Fatalf("ApplyConfigAccountType: %v", err)
	}
	stored, err := s.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if stored.Type != domain.AccountTypeBusiness || stored.TypeManual {
		t.Fatalf("configured account = %+v", stored)
	}
	if err := s.SetAccountType(ctx, account.ID, domain.AccountTypePersonal); err != nil {
		t.Fatalf("SetAccountType: %v", err)
	}
	if err := s.ApplyConfigAccountType(ctx, account.ID, domain.AccountTypeBusiness); err != nil {
		t.Fatalf("ApplyConfigAccountType after manual: %v", err)
	}
	stored, err = s.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatalf("GetAccount after manual: %v", err)
	}
	if stored.Type != domain.AccountTypePersonal || !stored.TypeManual {
		t.Fatalf("manual override lost = %+v", stored)
	}
}

func TestReconciliationPersistence(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	conn := seedConnection(t, s)
	account := seedAccount(t, s, conn.ID)
	now := time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC)
	rec := domain.Reconciliation{
		AccountID: account.ID, Currency: "EUR", OpeningBalanceMinor: 5000,
		AnchorAt: now.AddDate(0, 0, -30), LastCheckedAt: &now,
		LastDeltaMinor: -250, Status: domain.ReconciliationStatusDiscrepancy,
		DiscrepancyStart: &now, DiscrepancyEnd: &now, UpdatedAt: now,
	}
	if err := s.SaveReconciliation(ctx, &rec); err != nil {
		t.Fatalf("SaveReconciliation: %v", err)
	}
	got, err := s.GetReconciliation(ctx, account.ID)
	if err != nil {
		t.Fatalf("GetReconciliation: %v", err)
	}
	if got.LastDeltaMinor != -250 || got.Status != domain.ReconciliationStatusDiscrepancy || got.OpeningBalanceMinor != 5000 {
		t.Fatalf("reconciliation = %+v", got)
	}
	if err := s.SaveReconciliation(ctx, &rec); err != nil {
		t.Fatalf("second SaveReconciliation: %v", err)
	}
	if _, err := s.GetReconciliation(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing reconciliation error = %v", err)
	}
}
