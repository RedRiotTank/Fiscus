package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/tax"
)

func TestTaxShieldCardForBusinessWorkspace(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	if err := f.store.SetAccountType(context.Background(), "acc-1", domain.AccountTypeBusiness); err != nil {
		t.Fatalf("SetAccountType: %v", err)
	}
	service, err := tax.New(f.store, tax.Config{
		VATRateDefault: 0.21, IncomeTaxRetention: 0.15,
		QuarterStartMonths: []int{1, 4, 7, 10},
	}, f.server.now)
	if err != nil {
		t.Fatalf("tax.New: %v", err)
	}
	f.server.tax = service

	resp := f.request(t, http.MethodGet, "/?workspace=all", true, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d", resp.Code)
	}
	for _, want := range []string{"Tax Shield", "Caja limpia", "True operating capital", "Tax reserve"} {
		if !strings.Contains(resp.Body.String(), want) {
			t.Errorf("dashboard missing %q", want)
		}
	}

	f.server.profile = domain.WorkspacePersonal
	personal := f.request(t, http.MethodGet, "/", true, nil)
	if personal.Code != http.StatusOK {
		t.Fatalf("personal dashboard status = %d", personal.Code)
	}
	if strings.Contains(personal.Body.String(), "Tax Shield") {
		t.Fatal("tax shield must be hidden in the personal workspace")
	}
}

func TestTaxShieldEmptyForPersonalAccounts(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	service, err := tax.New(f.store, tax.Config{
		VATRateDefault: 0.21, IncomeTaxRetention: 0.15,
		QuarterStartMonths: []int{1, 4, 7, 10},
	}, f.server.now)
	if err != nil {
		t.Fatalf("tax.New: %v", err)
	}
	f.server.tax = service
	resp := f.request(t, http.MethodGet, "/?workspace=all", true, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "No business accounts") {
		t.Fatal("expected empty tax shield message")
	}
}
