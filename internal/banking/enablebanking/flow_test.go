package enablebanking

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestAuthFlowCompleteAuthorization(t *testing.T) {
	t.Parallel()
	tc := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/sessions" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{
			"session_id":"session-1",
			"accounts":[{"uid":"acc-1","account_id":{"iban":"ES123"},"currency":"EUR"}],
			"aspsp":{"name":"Banco Santander","country":"ES"},
			"psu_type":"personal",
			"access":{"valid_until":"2027-01-01T00:00:00Z"}
		}`)
	})

	store := &fakeConnectionStore{
		conn: domain.Connection{
			ID:              7,
			RequisitionID:   "authorization-1",
			InstitutionID:   "Banco Santander",
			InstitutionName: "Santander",
			Status:          "UA",
			Link:            "https://auth.example/start",
			Reference:       "state-1",
			AgreementID:     "authorization-1",
		},
	}
	flow, err := NewAuthFlow(tc.client, store, nil)
	if err != nil {
		t.Fatalf("NewAuthFlow: %v", err)
	}
	sessionID, err := flow.CompleteAuthorization(context.Background(), "code-1", "state-1")
	if err != nil {
		t.Fatalf("CompleteAuthorization: %v", err)
	}
	if sessionID != "session-1" {
		t.Fatalf("session ID = %q", sessionID)
	}
	if !store.updated {
		t.Fatal("connection was not updated")
	}
	if store.conn.RequisitionID != "session-1" || store.conn.Status != "LN" {
		t.Fatalf("connection = %+v", store.conn)
	}
	if store.conn.Link != "https://auth.example/start" {
		t.Fatalf("link = %q, want preserved", store.conn.Link)
	}
	if store.conn.AgreementID != "authorization-1" || store.conn.Reference != "" {
		t.Fatalf("connection = %+v", store.conn)
	}
	if store.conn.InstitutionName != "Banco Santander" || store.conn.InstitutionID != "Banco Santander" {
		t.Fatalf("institution = %+v", store.conn)
	}
}

func TestAuthFlowRejectsUnknownState(t *testing.T) {
	t.Parallel()
	store := &fakeConnectionStore{
		conn: domain.Connection{Reference: "expected-state"},
	}
	tc := newTestClient(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("API should not be called for an unknown state")
	})
	flow, err := NewAuthFlow(tc.client, store, nil)
	if err != nil {
		t.Fatalf("NewAuthFlow: %v", err)
	}
	if _, err := flow.CompleteAuthorization(context.Background(), "code", "wrong-state"); err == nil {
		t.Fatal("expected unknown state error")
	}
	if store.updated {
		t.Fatal("connection should not be updated")
	}
}
