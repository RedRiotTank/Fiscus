package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/analytics"
)

type scenarioRequest struct {
	Currency          string `json:"currency"`
	Months            int    `json:"months"`
	IncomeDeltaMinor  int64  `json:"income_delta_minor"`
	ExpenseDeltaMinor int64  `json:"expense_delta_minor"`
	OneOffMinor       int64  `json:"one_off_minor"`
	OneOffMonth       int    `json:"one_off_month"`
	ExtraMonthlyMinor int64  `json:"extra_monthly_minor"`
	AnnualYieldBP     int64  `json:"annual_yield_bp"`
}

func (s *Server) handleAnalyticsScenario(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var input scenarioRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil && err != io.EOF {
		s.analyticsBadRequest(w, "invalid scenario payload")
		return
	}
	if input.Months < 0 || input.Months > 180 {
		s.analyticsBadRequest(w, "months must be between 1 and 180")
		return
	}
	if input.Months == 0 {
		input.Months = 12
	}
	if input.OneOffMonth < 0 || input.OneOffMonth > input.Months {
		s.analyticsBadRequest(w, "one_off_month must be between 1 and the scenario horizon")
		return
	}
	if input.AnnualYieldBP < -500 {
		input.AnnualYieldBP = -500
	}
	if input.AnnualYieldBP > 2000 {
		input.AnnualYieldBP = 2000
	}
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))

	accounts, state, err := s.analyticsWorkspaceAccounts(r)
	if err != nil {
		s.analyticsStoreError(w, r, err)
		return
	}
	currency := analyticsCurrency(accounts, input.Currency)
	now := s.now().UTC()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -12, 0)
	transactions, err := s.store.ListTransactionsBetween(r.Context(), from, now.AddDate(0, 0, 1))
	if err != nil {
		s.analyticsStoreError(w, r, err)
		return
	}
	transactions = filterAnalyticsTransactions(transactions, accounts, state)
	result := analytics.ComputeScenario(transactions, accounts, now, currency, analytics.ScenarioInput{
		Months:            input.Months,
		IncomeDeltaMinor:  input.IncomeDeltaMinor,
		ExpenseDeltaMinor: input.ExpenseDeltaMinor,
		OneOffMinor:       input.OneOffMinor,
		OneOffMonth:       input.OneOffMonth,
		ExtraMonthlyMinor: input.ExtraMonthlyMinor,
		AnnualYieldBP:     input.AnnualYieldBP,
	})
	s.writeJSON(w, http.StatusOK, result)
}
