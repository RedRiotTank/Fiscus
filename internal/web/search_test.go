package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/search"
)

func TestTransactionSearchUI(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	service := search.New(f.store)
	f.server.search = service

	resp := f.request(t, http.MethodGet, "/transactions?q=coffee", true, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("search status = %d", resp.Code)
	}
	body := resp.Body.String()
	for _, want := range []string{"Coffee Shop", "Flat white", "value=\"coffee\""} {
		if !strings.Contains(body, want) {
			t.Errorf("search response missing %q", want)
		}
	}
	if strings.Contains(body, "Employer") {
		t.Error("search result unexpectedly contains non-matching salary")
	}

	invalid := f.request(t, http.MethodGet, "/transactions?q=status%3Adone", true, nil)
	if invalid.Code != http.StatusOK || !strings.Contains(invalid.Body.String(), "invalid status") {
		t.Fatalf("invalid search = %d %s", invalid.Code, invalid.Body.String())
	}
}

func TestSearchRespectsWorkspaceIsolation(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	if err := f.store.SetAccountType(context.Background(), "acc-1", domain.AccountTypeBusiness); err != nil {
		t.Fatalf("SetAccountType: %v", err)
	}
	f.server.search = search.New(f.store)
	personal := f.request(t, http.MethodGet, "/transactions?workspace=personal&q=employer", true, nil)
	if personal.Code != http.StatusOK {
		t.Fatalf("personal search status = %d", personal.Code)
	}
	if strings.Contains(personal.Body.String(), "Employer") {
		t.Fatal("personal workspace leaked a business search result")
	}
	business := f.request(t, http.MethodGet, "/transactions?workspace=business&q=employer", true, nil)
	if business.Code != http.StatusOK || !strings.Contains(business.Body.String(), "Employer") {
		t.Fatalf("business search = %d", business.Code)
	}
}
