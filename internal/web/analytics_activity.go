package web

import (
	"net/http"

	"github.com/RedRiotTank/Fiscus/internal/analytics"
)

func (s *Server) handleAnalyticsActivity(w http.ResponseWriter, r *http.Request) {
	accounts, state, err := s.analyticsWorkspaceAccounts(r)
	if err != nil {
		s.analyticsStoreError(w, r, err)
		return
	}
	currency := analyticsCurrency(accounts, r.URL.Query().Get("currency"))
	now := s.now().UTC()
	transactions, err := s.store.ListTransactionsBetween(r.Context(), now.AddDate(0, 0, -100), now.AddDate(0, 0, 1))
	if err != nil {
		s.analyticsStoreError(w, r, err)
		return
	}
	transactions = filterAnalyticsTransactions(transactions, accounts, state)
	s.writeJSON(w, http.StatusOK, analytics.ComputeActivity(transactions, now, currency))
}
