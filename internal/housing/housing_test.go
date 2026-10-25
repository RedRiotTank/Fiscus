package housing

import (
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

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
