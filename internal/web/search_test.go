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

func TestTransactionSearchPagination(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	f.server.search = search.New(f.store)
	first := f.request(t, http.MethodGet, "/transactions?q=coffee&page=1&limit=1", true, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first page status = %d", first.Code)
	}
	firstBody := first.Body.String()
	if !strings.Contains(firstBody, "Page 1 · 1 per page") || !strings.Contains(firstBody, "page=2") {
		t.Fatalf("first page pagination missing: %s", firstBody)
	}
	if !strings.Contains(firstBody, "Coffee Shop") || strings.Contains(firstBody, "Pending coffee") {
		t.Fatalf("first page results = %s", firstBody)
	}
	second := f.request(t, http.MethodGet, "/transactions?q=coffee&page=2&limit=1", true, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second page status = %d", second.Code)
	}
	if body := second.Body.String(); !strings.Contains(body, "Pending coffee") || !strings.Contains(body, "page=1") {
		t.Fatalf("second page = %s", body)
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
