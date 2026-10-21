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
	if list[0].Description != "updated" || list[0].Status != domain.TransactionStatusBooked {
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
