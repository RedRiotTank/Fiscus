package housing

import (
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestRuleCategoryRestrictsFlatmateInflowsToRealRent(t *testing.T) {
	t.Parallel()
	rule := domain.HousingRule{
		Workspace: "personal", Currency: "EUR",
		LandlordCounterparty: "Sonia Reyes", PersonalNetShareCents: 35_000,
		FlatmateCounterparties: []string{"Ismael Díaz"},
	}
	micro := domain.Transaction{
		AmountMinor: 170, Counterparty: "Ismael Díaz Díaz",
		Description: "From Ismael Díaz Díaz", Category: "Uncategorized",
	}
	if got := RuleCategory(micro, rule); got == Category {
		t.Fatalf("micro-transfer was classified as housing: %+v", micro)
	}
	noted := domain.Transaction{
		AmountMinor: 35_000, Counterparty: "Ismael D",
		Description: "Piso octubre | From Ismael D", Category: "Uncategorized",
	}
	if got := RuleCategory(noted, rule); got != Category {
		t.Fatalf("noted rent transfer = %q, want %q", got, Category)
	}
	exact := domain.Transaction{
		AmountMinor: 34_800, Counterparty: "Ismael D",
		Description: "Transferencia", Category: "Uncategorized",
	}
	if got := RuleCategory(exact, rule); got != Category {
		t.Fatalf("share within tolerance = %q, want %q", got, Category)
	}
	tooLarge := domain.Transaction{
		AmountMinor: 40_000, Counterparty: "Ismael D",
		Description: "Transferencia", Category: "Uncategorized",
	}
	if got := RuleCategory(tooLarge, rule); got == Category {
		t.Fatalf("non-rent amount was classified as housing: %+v", tooLarge)
	}
}

func TestHousingExpenseAndReimbursementRecognition(t *testing.T) {
	t.Parallel()
	rent := domain.Transaction{AmountMinor: -105_000, Counterparty: "Inmobiliaria Centro", Description: "Alquiler piso octubre"}
	if !IsExpense(rent) {
		t.Fatal("rent keyword should be recognised as housing expense")
	}
	explicit := domain.Transaction{AmountMinor: -105_000, Category: Category}
	if !IsExpense(explicit) {
		t.Fatal("housing category should be recognised as expense")
	}
	flatmate := domain.Transaction{AmountMinor: 35_000, Counterparty: "Ismael Díaz", Description: "Piso octubre | From Ismael"}
	if !IsReimbursement(flatmate) {
		t.Fatal("flatmate reimbursement should be recognised")
	}
	salary := domain.Transaction{AmountMinor: 280_000, Category: "Payroll", Description: "Salary"}
	if IsReimbursement(salary) {
		t.Fatal("payroll must not be a housing reimbursement")
	}
	if IsExpense(domain.Transaction{AmountMinor: 35_000, Category: Category}) {
		t.Fatal("incoming transaction must not be a housing expense")
	}
}
