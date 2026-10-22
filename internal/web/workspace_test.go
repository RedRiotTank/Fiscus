package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHybridWorkspaceFilteringAndCookie(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	ctx := context.Background()
	if err := f.store.SetAccountType(ctx, "acc-1", "personal"); err != nil {
		t.Fatalf("SetAccountType acc-1: %v", err)
	}
	if err := f.store.SetAccountType(ctx, "acc-2", "business"); err != nil {
		t.Fatalf("SetAccountType acc-2: %v", err)
	}

	all := f.request(t, http.MethodGet, "/", true, nil)
	if all.Code != http.StatusOK {
		t.Fatalf("all status = %d", all.Code)
	}
	if !strings.Contains(all.Body.String(), "Everyday") || !strings.Contains(all.Body.String(), "Savings") {
		t.Fatal("all workspace should include both accounts")
	}
	if !strings.Contains(all.Body.String(), "workspace-switcher") {
		t.Fatal("hybrid profile should render the workspace switcher")
	}

	business := f.request(t, http.MethodGet, "/?workspace=business", true, nil)
	if business.Code != http.StatusOK {
		t.Fatalf("business status = %d", business.Code)
	}
	body := business.Body.String()
	if !strings.Contains(body, "Savings") || strings.Contains(body, "Everyday") {
		t.Fatalf("business view was not isolated: %s", body)
	}
	cookies := business.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != workspaceCookie || cookies[0].Value != "business" {
		t.Fatalf("workspace cookie = %+v", cookies)
	}

	transactions := f.request(t, http.MethodGet, "/transactions", true, map[string]string{
		"Cookie": workspaceCookie + "=business",
	})
	if transactions.Code != http.StatusOK || !strings.Contains(transactions.Body.String(), "Pending coffee") {
		t.Fatalf("business transactions = %d %s", transactions.Code, transactions.Body.String())
	}
	if strings.Contains(transactions.Body.String(), "Coffee Shop") {
		t.Fatal("personal transaction leaked into business view")
	}

	personal := f.request(t, http.MethodGet, "/?workspace=personal", true, nil)
	if personal.Code != http.StatusOK {
		t.Fatalf("personal status = %d", personal.Code)
	}
	body = personal.Body.String()
	if !strings.Contains(body, "Everyday") || strings.Contains(body, "Savings") {
		t.Fatalf("personal view was not isolated: %s", body)
	}
}

func TestWorkspaceProfileLocksView(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	ctx := context.Background()
	if err := f.store.SetAccountType(ctx, "acc-1", "personal"); err != nil {
		t.Fatalf("SetAccountType acc-1: %v", err)
	}
	if err := f.store.SetAccountType(ctx, "acc-2", "business"); err != nil {
		t.Fatalf("SetAccountType acc-2: %v", err)
	}

	server, err := New(Config{
		Username: "admin", Password: "secret", Profile: "business",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    f.server.now,
	}, f.store, f.trigger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	handler := server.Handler()
	req := httptest.NewRequest(http.MethodGet, "/?workspace=personal", nil)
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Savings") || strings.Contains(body, "Everyday") {
		t.Fatalf("locked business profile was not isolated: %s", body)
	}
	if strings.Contains(body, "workspace-switcher") {
		t.Fatal("locked profile must not offer a workspace switcher")
	}
	if !strings.Contains(body, "workspace-lock") {
		t.Fatal("locked profile should render a workspace badge")
	}
}

func TestWorkspaceBadgesOnTransactions(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	ctx := context.Background()
	if err := f.store.SetAccountType(ctx, "acc-2", "business"); err != nil {
		t.Fatalf("SetAccountType: %v", err)
	}
	resp := f.request(t, http.MethodGet, "/transactions", true, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	body := resp.Body.String()
	if !strings.Contains(body, "badge workspace business") || !strings.Contains(body, "badge workspace personal") {
		t.Fatalf("transaction workspace badges missing: %s", body)
	}
}
