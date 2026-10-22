package web

import (
	"net/http"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/analytics"
)

func (s *Server) handleAnalyticsPace(w http.ResponseWriter, r *http.Request) {
	accounts, state, err := s.analyticsWorkspaceAccounts(r)
	if err != nil {
		s.analyticsStoreError(w, r, err)
		return
	}
	currency := analyticsCurrency(accounts, r.URL.Query().Get("currency"))
	now := s.now().UTC()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -7, 0)
	transactions, err := s.store.ListTransactionsBetween(r.Context(), from, now.AddDate(0, 0, 1))
	if err != nil {
		s.analyticsStoreError(w, r, err)
		return
	}
	transactions = filterAnalyticsTransactions(transactions, accounts, state)
	s.writeJSON(w, http.StatusOK, analytics.ComputePace(transactions, now, currency))
}
