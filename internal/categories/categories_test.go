package categories

import (
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestClassifierFirstMatchWins(t *testing.T) {
	t.Parallel()
	classifier, err := New([]Rule{
		{Category: "Utilities", Pattern: `(?i)IBERDROLA|ENDESA|AGUAS`},
		{Category: "Energy", Pattern: `(?i)IBERDROLA`},
		{Category: "Payroll", Pattern: `(?i)NOMINA|SALARY`},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []struct {
		name string
		tx   domain.Transaction
		want string
	}{
		{name: "first rule wins", tx: domain.Transaction{Counterparty: "Iberdrola"}, want: "Utilities"},
		{name: "description match", tx: domain.Transaction{Description: "Pago ENDESA"}, want: "Utilities"},
		{name: "payroll", tx: domain.Transaction{Counterparty: "ACME SL", Description: "Nomina marzo"}, want: "Payroll"},
		{name: "default", tx: domain.Transaction{Counterparty: "Coffee Shop"}, want: DefaultCategory},
		{name: "empty transaction", tx: domain.Transaction{}, want: DefaultCategory},
		{name: "does not span fields", tx: domain.Transaction{Counterparty: "IBER", Description: "DROLA"}, want: DefaultCategory},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := classifier.Classify(tc.tx); got != tc.want {
				t.Fatalf("Classify = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClassifierCaseSensitiveByDefault(t *testing.T) {
	t.Parallel()
	classifier := MustNew([]Rule{{Category: "Fees", Pattern: `FEE`}})
	if got := classifier.Classify(domain.Transaction{Description: "fee maintenance"}); got != DefaultCategory {
		t.Fatalf("Classify = %q, want %q", got, DefaultCategory)
	}
	if got := classifier.Classify(domain.Transaction{Description: "FEE maintenance"}); got != "Fees" {
		t.Fatalf("Classify = %q, want Fees", got)
	}
}

func TestClassifierValidation(t *testing.T) {
	t.Parallel()
	if _, err := New([]Rule{{Category: "", Pattern: "x"}}); err == nil {
		t.Fatal("expected empty category error")
	}
	if _, err := New([]Rule{{Category: "Broken", Pattern: "("}}); err == nil {
		t.Fatal("expected invalid pattern error")
	}
	if _, err := New([]Rule{{Category: "Empty", Pattern: "  "}}); err == nil {
		t.Fatal("expected empty pattern error")
	}
	var nilClassifier *Classifier
	if got := nilClassifier.Classify(domain.Transaction{Counterparty: "Anything"}); got != DefaultCategory {
		t.Fatalf("nil Classify = %q", got)
	}
}
