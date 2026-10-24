package web

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func (s *Server) handleBudgets(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Budgets", "budgets")
	if err := s.loadOverview(r.Context(), &data); err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, "budgets", data)
}

func (s *Server) handleBudgetAdvisoryJSON(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Budget advisory", "budgets")
	if err := s.loadOverview(r.Context(), &data); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"enabled":    data.BudgetAdvisoryEnabled,
		"model":      data.BudgetAdvisoryModel,
		"advisories": data.BudgetAdvisories,
	})
}

func (s *Server) handleBudgetAdvisoryToggle(w http.ResponseWriter, r *http.Request) {
	if !validSyncRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if s.budgetManager == nil {
		http.Error(w, "Budget advisory persistence is not configured", http.StatusServiceUnavailable)
		return
	}
	raw := strings.ToLower(strings.TrimSpace(r.FormValue("enabled")))
	enabled := raw == "true" || raw == "on" || raw == "1"
	if err := s.budgetManager.SetAdvisoryEnabled(r.Context(), enabled); err != nil {
		s.logger.Error("toggle budget advisory", "error", err)
		http.Error(w, "Could not update the budget advisor", http.StatusInternalServerError)
		return
	}
	s.redirectBudget(w, r, "/budgets")
}

func (s *Server) handleBudgetAdvisoryApply(w http.ResponseWriter, r *http.Request) {
	if !validSyncRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if s.budgetManager == nil {
		http.Error(w, "Budget advisory persistence is not configured", http.StatusServiceUnavailable)
		return
	}
	if workspace := strings.ToLower(strings.TrimSpace(r.FormValue("workspace"))); domain.ValidWorkspaceView(workspace) {
		query := r.URL.Query()
		query.Set("workspace", workspace)
		r.URL.RawQuery = query.Encode()
	}
	data := s.baseData(r, "Budget advisory", "budgets")
	if err := s.loadOverview(r.Context(), &data); err != nil {
		s.fail(w, r, err)
		return
	}
	if !data.BudgetAdvisoryEnabled {
		http.Error(w, "Budget advisory is paused", http.StatusConflict)
		return
	}
	count, err := s.budgetManager.Apply(r.Context(), data.BudgetAdvisories)
	if err != nil {
		s.logger.Error("apply recommended budgets", "error", err)
		http.Error(w, "Could not apply recommended budgets", http.StatusInternalServerError)
		return
	}
	s.redirectBudget(w, r, fmt.Sprintf("/budgets?advisory=applied&count=%d", count))
}

func (s *Server) redirectBudget(w http.ResponseWriter, r *http.Request, target string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
