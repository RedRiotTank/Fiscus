package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/pkg/transfers"
)

func TestTransactionsPageShowsPassThroughPairAndFilter(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	ctx := context.Background()
	day := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		{
			AccountID: "acc-1", ExternalID: "ui-mirror-in", AmountMinor: 58_556, Currency: "EUR",
			BookingDate: day, Counterparty: "Cuenta Remunerada", Description: "Transferencia interna",
			Category: "Transfers / P2P", Status: domain.TransactionStatusBooked,
		},
		{
			AccountID: "acc-1", ExternalID: "ui-mirror-out", AmountMinor: -58_556, Currency: "EUR",
			BookingDate: day.Add(5 * time.Minute), Counterparty: "Ismael Díaz Díaz",
			Description: "To Ismael Díaz Díaz", Category: "Transfers / P2P",
			Status: domain.TransactionStatusBooked,
		},
	}
	for index := range transactions {
		if _, _, err := f.store.UpsertTransaction(ctx, &transactions[index]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}
	if paired, err := f.store.NeutralizePassThroughs(ctx, transfers.DefaultWindowDays); err != nil {
		t.Fatalf("NeutralizePassThroughs: %v", err)
	} else if paired != 1 {
		t.Fatalf("paired = %d, want 1", paired)
	}

	page := f.request(t, http.MethodGet, "/transactions", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("transactions status = %d body=%s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	for _, want := range []string{
		"⇄ Pass-through (Linked to #", "Offset by", "€585.56 from Cuenta Remunerada on 05 Oct",
		`value="Transfers / Pass-through"`, "cat-passthrough",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pass-through UI missing %q", want)
		}
	}

	filtered := f.request(t, http.MethodGet, "/transactions?facet=p2p", true, map[string]string{"HX-Request": "true"})
	if filtered.Code != http.StatusOK {
		t.Fatalf("P2P filter status = %d", filtered.Code)
	}
	for _, want := range []string{"Cuenta Remunerada", "Ismael Díaz Díaz", "Pass-through"} {
		if !strings.Contains(filtered.Body.String(), want) {
			t.Errorf("P2P filter missing %q", want)
		}
	}
}

func TestManualCategoryChangeUnpairsPassThroughFromUI(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	ctx := context.Background()
	day := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		{
			AccountID: "acc-1", ExternalID: "ui-unlink-in", AmountMinor: 58_556, Currency: "EUR",
			BookingDate: day, Counterparty: "Cuenta Remunerada", Description: "Transferencia interna",
			Category: "Transfers / P2P", Status: domain.TransactionStatusBooked,
		},
		{
			AccountID: "acc-1", ExternalID: "ui-unlink-out", AmountMinor: -58_556, Currency: "EUR",
			BookingDate: day, Counterparty: "Ismael Díaz Díaz", Description: "To Ismael Díaz Díaz",
			Category: "Transfers / P2P", Status: domain.TransactionStatusBooked,
		},
	}
	for index := range transactions {
		if _, _, err := f.store.UpsertTransaction(ctx, &transactions[index]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}
	if _, err := f.store.NeutralizePassThroughs(ctx, transfers.DefaultWindowDays); err != nil {
		t.Fatalf("NeutralizePassThroughs: %v", err)
	}
	form := url.Values{"category": {"Groceries"}, "remember": {"0"}}
	req := httptest.NewRequest(http.MethodPatch,
		"/api/transactions/"+strconv.FormatInt(transactions[1].ID, 10)+"/category",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(syncRequestHeader, "1")
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("category PATCH = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "Pass-through (Linked") {
		t.Fatalf("pass-through badge survived manual unlink: %s", rec.Body.String())
	}
	outflow, err := f.store.GetTransaction(ctx, transactions[1].ID)
	if err != nil {
		t.Fatalf("GetTransaction outflow: %v", err)
	}
	inflow, err := f.store.GetTransaction(ctx, transactions[0].ID)
	if err != nil {
		t.Fatalf("GetTransaction inflow: %v", err)
	}
	if outflow.Category != "Groceries" || outflow.IsBudgetExcluded ||
		inflow.Category != "Transfers / P2P" || inflow.IsBudgetExcluded {
		t.Fatalf("unlinked transactions = outflow %+v inflow %+v", outflow, inflow)
	}
}
