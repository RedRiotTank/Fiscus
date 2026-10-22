package web

import (
	"net/http"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/analytics"
	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// handleAnalytics renders the interactive analytics workspace. Charts load
// their datasets from the JSON endpoints.
func (s *Server) handleAnalytics(w http.ResponseWriter, _ *http.Request) {
	data := s.baseData("Analytics", "analytics")
	s.render(w, "analytics", data)
}

func (s *Server) handleAnalyticsSpending(w http.ResponseWriter, r *http.Request) {
	period, ok := analytics.NormalizePeriod(r.URL.Query().Get("period"))
	if !ok {
		s.analyticsBadRequest(w, "period must be month, quarter, or ytd")
		return
	}
	accounts, err := s.store.ListAccounts(r.Context())
	if err != nil {
		s.analyticsStoreError(w, r, err)
		return
	}
	currency := analyticsCurrency(accounts, r.URL.Query().Get("currency"))
	now := s.now().UTC()
	transactions, err := s.store.ListTransactionsBetween(r.Context(), now.AddDate(-1, 0, 0), now.AddDate(0, 0, 1))
	if err != nil {
		s.analyticsStoreError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, analytics.ComputeSpending(transactions, accounts, now, currency, period))
}

func analyticsCurrency(accounts []domain.Account, requested string) string {
	currency := strings.ToUpper(strings.TrimSpace(requested))
	if currency != "" {
		return currency
	}
	for _, account := range accounts {
		currency = strings.ToUpper(strings.TrimSpace(account.Currency))
		if currency != "" {
			return currency
		}
	}
	return "EUR"
}

func (s *Server) analyticsStoreError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Error("analytics request failed", "path", r.URL.Path, "error", err)
	s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load analytics data"})
}

func (s *Server) analyticsBadRequest(w http.ResponseWriter, message string) {
	s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": message})
}
