package budgets

import (
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/housing"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

func TestNetHousingSpendingOffsetsFlatmateReimbursements(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		{
			AccountID: "checking", ExternalID: "rent", AmountMinor: -105_000, Currency: "EUR",
			BookingDate: now.AddDate(0, 0, -4), Counterparty: "Inmobiliaria Centro",
			Description: "Alquiler piso", Category: housing.Category, Status: domain.TransactionStatusBooked,
		},
		{
			AccountID: "checking", ExternalID: "flatmate-1", AmountMinor: 35_000, Currency: "EUR",
			BookingDate: now.AddDate(0, 0, -3), Counterparty: "Ismael Díaz",
			Description: "Piso octubre | From Ismael", Category: "Uncategorized", Status: domain.TransactionStatusBooked,
		},
		{
			AccountID: "checking", ExternalID: "flatmate-2", AmountMinor: 35_000, Currency: "EUR",
			BookingDate: now.AddDate(0, 0, -2), Counterparty: "Ana Ruiz",
			Description: "Piso octubre | From Ana", Category: "Uncategorized", Status: domain.TransactionStatusBooked,
		},
	}
	spending := []storage.CategorySpending{{Category: housing.Category, Currency: "EUR", Minor: 105_000}}
	net := NetHousingSpending(spending, transactions)
	if len(net) != 1 || net[0].Category != housing.Category || net[0].Minor != 35_000 {
		t.Fatalf("net housing = %+v, want 35000", net)
	}
}

func TestNetHousingSpendingRepairsTransferCategory(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		{
			AccountID: "checking", ExternalID: "rent", AmountMinor: -105_000, Currency: "EUR",
			BookingDate: now.AddDate(0, 0, -4), Counterparty: "Casa", Description: "Alquiler",
			Category: "Transfers", Status: domain.TransactionStatusBooked,
		},
		{
			AccountID: "checking", ExternalID: "flatmate", AmountMinor: 35_000, Currency: "EUR",
			BookingDate: now.AddDate(0, 0, -2), Counterparty: "Ismael Díaz",
			Description: "Piso octubre | From Ismael", Category: "Uncategorized", Status: domain.TransactionStatusBooked,
		},
		{
			AccountID: "checking", ExternalID: "flatmate-2", AmountMinor: 35_000, Currency: "EUR",
			BookingDate: now.AddDate(0, 0, -1), Counterparty: "Ana Ruiz",
			Description: "Piso octubre | From Ana", Category: "Uncategorized", Status: domain.TransactionStatusBooked,
		},
	}
	spending := []storage.CategorySpending{{Category: "Transfers", Currency: "EUR", Minor: 105_000}}
	net := NetHousingSpending(spending, transactions)
	if len(net) != 1 || net[0].Category != housing.Category || net[0].Minor != 35_000 {
		t.Fatalf("repaired net housing = %+v, want 35000", net)
	}
}

func TestBuildPlanUsesNetHousingSpend(t *testing.T) {
	t.Parallel()
	allocations := []Allocation{{Category: housing.Category, Pillar: PillarNeeds, PercentBPS: 3000}}
	plan := BuildPlan(personalProfile(), allocations, []storage.CategorySpending{
		{Category: housing.Category, Currency: "EUR", Minor: 35_000},
	}, 280_000, 0, 280_000, time.Now())
	if len(plan.Categories) != 1 || plan.Categories[0].SpentMinor != 35_000 {
		t.Fatalf("housing plan = %+v", plan.Categories)
	}
}
