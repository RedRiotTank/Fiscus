package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/analytics"
)

func (s *Server) handleAnalyticsProjections(w http.ResponseWriter, r *http.Request) {
	extra, err := parseOptionalInt64(r.URL.Query().Get("extra_monthly_minor"))
	if err != nil {
		s.analyticsBadRequest(w, "extra_monthly_minor must be an integer")
		return
	}
	yield, err := parseOptionalInt64(r.URL.Query().Get("yield_bp"))
	if err != nil {
		s.analyticsBadRequest(w, "yield_bp must be an integer")
		return
	}
	if r.URL.Query().Get("yield_bp") == "" {
		yield = 300
	}
	if yield < -500 {
		yield = -500
	}
	if yield > 2000 {
		yield = 2000
	}
	accounts, err := s.store.ListAccounts(r.Context())
	if err != nil {
		s.analyticsStoreError(w, r, err)
		return
	}
	currency := analyticsCurrency(accounts, r.URL.Query().Get("currency"))
	now := s.now().UTC()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -12, 0)
	transactions, err := s.store.ListTransactionsBetween(r.Context(), from, now.AddDate(0, 0, 1))
	if err != nil {
		s.analyticsStoreError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, analytics.ComputeProjections(transactions, accounts, now, currency, extra, yield))
}

func parseOptionalInt64(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseInt(raw, 10, 64)
}
