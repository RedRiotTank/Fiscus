package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/search"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

func TestTransactionNotesUpdateAndFTSReindex(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	txID := webTransactionID(t, f, "tx-1")
	form := url.Values{"notes": {"client reimbursed lunch"}}
	req := httptest.NewRequest(http.MethodPatch, "/api/transactions/"+strconv.FormatInt(txID, 10)+"/notes", strings.NewReader(form.Encode()))
	req.SetBasicAuth("admin", "secret")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(syncRequestHeader, "1")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("notes update = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "client reimbursed lunch") {
		t.Fatalf("updated form missing note: %s", rec.Body.String())
	}
	transactions, err := f.store.ListTransactions(context.Background(), storage.TransactionFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	found := false
	for _, tx := range transactions {
		if tx.ID == txID {
			found = tx.Notes == "client reimbursed lunch"
		}
	}
	if !found {
		t.Fatal("note was not persisted")
	}

	f.server.search = search.New(f.store)
	result := f.request(t, http.MethodGet, "/transactions?q=reimbursed", true, nil)
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), "Coffee Shop") {
		t.Fatalf("FTS search after note update = %d %s", result.Code, result.Body.String())
	}
}

func TestTransactionNotesRejectsMissingRequestHeader(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	txID := webTransactionID(t, f, "tx-1")
	form := url.Values{"notes": {"unprotected"}}
	req := httptest.NewRequest(http.MethodPatch, "/api/transactions/"+strconv.FormatInt(txID, 10)+"/notes", strings.NewReader(form.Encode()))
	req.SetBasicAuth("admin", "secret")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unprotected notes update = %d, want 403", rec.Code)
	}
}

func TestAccountWorkspaceTypeUpdatePersists(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	form := url.Values{"type": {"business"}}
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/acc-1/type", strings.NewReader(form.Encode()))
	req.SetBasicAuth("admin", "secret")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(syncRequestHeader, "1")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("account type update = %d %s", rec.Code, rec.Body.String())
	}
	account, err := f.store.GetAccount(context.Background(), "acc-1")
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if account.Type != domain.AccountTypeBusiness || !account.TypeManual {
		t.Fatalf("account = %+v", account)
	}
	if err := f.store.ApplyConfigAccountType(context.Background(), "acc-1", domain.AccountTypePersonal); err != nil {
		t.Fatalf("ApplyConfigAccountType: %v", err)
	}
	account, err = f.store.GetAccount(context.Background(), "acc-1")
	if err != nil {
		t.Fatalf("GetAccount after config apply: %v", err)
	}
	if account.Type != domain.AccountTypeBusiness {
		t.Fatalf("manual mapping was overwritten: %+v", account)
	}
}

func TestConnectionsPageExposesWorkspaceSelector(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	resp := f.request(t, http.MethodGet, "/connections", true, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("connections status = %d", resp.Code)
	}
	if body := resp.Body.String(); !strings.Contains(body, `/api/accounts/acc-1/type`) ||
		!strings.Contains(body, `name="type"`) {
		t.Fatalf("workspace selector missing: %s", body)
	}
}

func TestAccountWorkspaceTypeRejectsMissingRequestHeader(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	form := url.Values{"type": {"business"}}
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/acc-1/type", strings.NewReader(form.Encode()))
	req.SetBasicAuth("admin", "secret")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unprotected account update = %d, want 403", rec.Code)
	}
}
