package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/analytics"
)

func (s *Server) handleAnalyticsLiquidity(w http.ResponseWriter, r *http.Request) {
	days := 90
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || (parsed != 90 && parsed != 180) {
			s.analyticsBadRequest(w, "days must be 90 or 180")
			return
		}
		days = parsed
	}
	accounts, err := s.store.ListAccounts(r.Context())
	if err != nil {
		s.analyticsStoreError(w, r, err)
		return
	}
	connections, err := s.store.ListConnections(r.Context())
	if err != nil {
		s.analyticsStoreError(w, r, err)
		return
	}
	currency := analyticsCurrency(accounts, r.URL.Query().Get("currency"))
	now := s.now().UTC()
	transactions, err := s.store.ListTransactionsBetween(r.Context(), now.AddDate(0, 0, -(days+2)), now.AddDate(0, 0, 1))
	if err != nil {
		s.analyticsStoreError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, analytics.ComputeLiquidity(accounts, connections, transactions, now, currency, days))
}
