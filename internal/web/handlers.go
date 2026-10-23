package web

import (
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/analysis"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/treasury"
)

const syncRequestHeader = "X-Fiscus-Request"

func (s *Server) baseData(r *http.Request, title, active string) PageData {
	path := "/"
	if r != nil && r.URL != nil && r.URL.Path != "" {
		path = r.URL.Path
	}
	return PageData{
		Title:     title,
		Active:    active,
		Version:   s.version,
		Now:       s.now().UTC(),
		Workspace: s.resolveWorkspace(r),
		Path:      path,
	}
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Overview", "dashboard")
	ctx := r.Context()
	if err := s.loadOverview(ctx, &data); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.loadAlerts(ctx, &data, 12); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.loadConnections(ctx, &data); err != nil {
		s.fail(w, r, err)
		return
	}
	transactions, err := s.store.RecentTransactions(ctx, 20)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	transactions = filterTransactions(transactions, allowedAccountIDs(data.Accounts, data.Workspace.View), data.Workspace.View)
	data.Transactions = transactionViews(transactions, accountMap(data.Accounts))
	s.render(w, "dashboard", data)
}

func (s *Server) handleTransactions(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Transactions", "transactions")
	data.FilterAccount = strings.TrimSpace(r.URL.Query().Get("account"))
	data.SearchQuery = strings.TrimSpace(r.URL.Query().Get("q"))
	data.Page = parsePage(r.URL.Query().Get("page"))

	ctx := r.Context()
	accounts, err := s.workspaceAccounts(ctx, data.Workspace.View)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data.Accounts = accounts
	data.TotalAccounts = len(accounts)
	allowed := allowedAccountIDs(accounts, data.Workspace.View)
	filterBlocked := false
	if data.FilterAccount != "" {
		if _, ok := allowed[data.FilterAccount]; !ok {
			filterBlocked = true
		}
	}

	accountIDs := accountIDsArg(accounts, data.Workspace.View)
	restrictedEmpty := filterBlocked || (data.Workspace.View != domain.WorkspaceAll && len(accounts) == 0)
	searchMode := data.SearchQuery != "" && s.search != nil
	var transactions []domain.Transaction
	if !restrictedEmpty {
		if searchMode {
			transactions, err = s.search.Search(ctx, data.SearchQuery, 50)
			if err != nil {
				data.ErrorMessage = err.Error()
				transactions = nil
			} else {
				transactions = filterTransactions(transactions, allowed, data.Workspace.View)
				if data.FilterAccount != "" {
					filtered := transactions[:0]
					for _, tx := range transactions {
						if tx.AccountID == data.FilterAccount {
							filtered = append(filtered, tx)
						}
					}
					transactions = filtered
				}
			}
		} else {
			transactions, err = s.store.ListTransactions(ctx, transactionFilter(data.FilterAccount, accountIDs, data.Page))
			if err != nil {
				s.fail(w, r, err)
				return
			}
		}
	}
	data.Transactions = transactionViews(transactions, accountMap(accounts))
	if searchMode {
		data.Page = 1
		data.HasNext = false
	} else {
		data.HasNext = len(transactions) == transactionFilter("", nil, 0).Limit
	}
	data.PrevPage = max(data.Page-1, 0)
	data.NextPage = data.Page + 1
	var total int
	if !restrictedEmpty {
		if searchMode {
			total = len(transactions)
		} else {
			total, err = s.store.CountTransactionsFiltered(ctx, accountIDs)
			if err != nil {
				s.fail(w, r, err)
				return
			}
		}
	}
	data.TotalTransactions = total
	data.AttachmentNotice = attachmentNotice(r.URL.Query().Get("attachment"))
	if err := s.loadAttachmentData(ctx, &data); err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, "transactions", data)
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	format, err := treasury.ParseFormat(query.Get("format"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	from, err := parseExportDate(query.Get("from"), false)
	if err != nil {
		http.Error(w, "Invalid from date: "+err.Error(), http.StatusBadRequest)
		return
	}
	to, err := parseExportDate(query.Get("to"), true)
	if err != nil {
		http.Error(w, "Invalid to date: "+err.Error(), http.StatusBadRequest)
		return
	}
	if to.IsZero() {
		to = s.now().UTC().Add(24 * time.Hour)
	}
	if !from.IsZero() && !from.Before(to) {
		http.Error(w, "The from date must be before the to date", http.StatusBadRequest)
		return
	}
	accountID := strings.TrimSpace(query.Get("account"))
	transactions, err := s.store.ListTransactionsBetween(r.Context(), from, to)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	workspace := s.workspaceState(r)
	workspaceAccounts, err := s.workspaceAccounts(r.Context(), workspace.View)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	transactions = filterTransactions(transactions, allowedAccountIDs(workspaceAccounts, workspace.View), workspace.View)
	if query.Get("include_transfers") != "1" {
		pairs := treasury.PairTransfers(transactions, 3)
		transactions = treasury.ExcludeTransfers(transactions, pairs)
	}
	if accountID != "" {
		filtered := make([]domain.Transaction, 0, len(transactions))
		for _, transaction := range transactions {
			if transaction.AccountID == accountID {
				filtered = append(filtered, transaction)
			}
		}
		transactions = filtered
	}
	accounts, err := s.store.ListAccounts(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data, err := treasury.Export(format, transactions, accounts)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", treasury.ContentType(format))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="fiscus-export-%s%s"`, s.now().UTC().Format("2006-01-02"), treasury.Extension(format)))
	// #nosec G705 -- the response is a non-HTML download with an attachment disposition.
	_, _ = w.Write(data)
}

func parseExportDate(raw string, endOfDay bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("use YYYY-MM-DD")
	}
	if endOfDay {
		parsed = parsed.Add(24 * time.Hour)
	}
	return parsed, nil
}

func (s *Server) handleSubscriptions(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Subscriptions & Fees", "subscriptions")
	ctx := r.Context()
	snapshot, err := s.analyzer.Snapshot(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	accounts, err := s.workspaceAccounts(ctx, data.Workspace.View)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data.Accounts = accounts
	if data.Workspace.View != domain.WorkspaceAll {
		allowed := allowedAccountIDs(accounts, data.Workspace.View)
		subscriptions := make([]analysis.Subscription, 0, len(snapshot.Subscriptions))
		for _, subscription := range snapshot.Subscriptions {
			if _, ok := allowed[subscription.AccountID]; ok {
				subscriptions = append(subscriptions, subscription)
			}
		}
		fees := make([]analysis.Fee, 0, len(snapshot.Fees))
		for _, fee := range snapshot.Fees {
			if _, ok := allowed[fee.AccountID]; ok {
				fees = append(fees, fee)
			}
		}
		snapshot.Subscriptions = subscriptions
		snapshot.Fees = fees
	}
	data.Subscriptions = subscriptionViews(snapshot.Subscriptions, accountMap(accounts))
	data.Fees = feeViews(snapshot.Fees, accountMap(accounts))
	data.MonthlyCosts = currencyAmountViews(snapshot.MonthlyCosts)
	data.AnnualCosts = currencyAmountViews(snapshot.AnnualCosts)
	data.FeeTotals = currencyAmountViews(snapshot.FeeTotals)
	s.render(w, "subscriptions", data)
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Alerts", "alerts")
	if err := s.loadAlerts(r.Context(), &data, 200); err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, "alerts", data)
}

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Connections", "connections")
	switch r.URL.Query().Get("auth") {
	case "success":
		data.AuthStatus = "success"
	case "error":
		data.AuthStatus = "error"
	}
	if err := s.loadConnections(r.Context(), &data); err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, "connections", data)
}

// handleAuthorizationCallback completes an OAuth-style bank consent. It is
// intentionally unauthenticated: the bank redirects the user here, and the
// stored, random state value authenticates the pending connection.
func (s *Server) handleAuthorizationCallback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if providerErr := query.Get("error"); providerErr != "" {
		s.logger.Warn("bank authorization failed", "error", providerErr, "description", query.Get("error_description"))
		http.Redirect(w, r, "/connections?auth=error", http.StatusSeeOther)
		return
	}
	code := query.Get("code")
	state := query.Get("state")
	if code == "" || state == "" {
		http.Error(w, "Missing authorization code or state", http.StatusBadRequest)
		return
	}
	if s.authHandler == nil {
		http.NotFound(w, r)
		return
	}
	if _, err := s.authHandler.CompleteAuthorization(r.Context(), code, state); err != nil {
		s.logger.Error("complete bank authorization", "error", err)
		http.Redirect(w, r, "/connections?auth=error", http.StatusSeeOther)
		return
	}
	if s.trigger != nil {
		s.trigger.Trigger("")
	}
	http.Redirect(w, r, "/connections?auth=success", http.StatusSeeOther)
}

func (s *Server) handleOverviewPartial(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Overview", "dashboard")
	if err := s.loadOverview(r.Context(), &data); err != nil {
		s.logger.Error("load overview partial", "error", err)
		http.Error(w, "Could not load overview", http.StatusInternalServerError)
		return
	}
	s.renderPartial(w, "overview_panel", data)
}

func (s *Server) handleAlertsPartial(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Alerts", "dashboard")
	if err := s.loadAlerts(r.Context(), &data, 12); err != nil {
		s.logger.Error("load alerts partial", "error", err)
		http.Error(w, "Could not load alerts", http.StatusInternalServerError)
		return
	}
	s.renderPartial(w, "alerts_feed", data)
}

func (s *Server) handleConnectionsPartial(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Connections", "connections")
	if err := s.loadConnections(r.Context(), &data); err != nil {
		s.logger.Error("load connections partial", "error", err)
		http.Error(w, "Could not load connections", http.StatusInternalServerError)
		return
	}
	s.renderPartial(w, "connections_panel", data)
}

func (s *Server) handleSyncAll(w http.ResponseWriter, r *http.Request) {
	if !validSyncRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if s.trigger == nil {
		s.syncNotice(w, http.StatusServiceUnavailable, "error", "Banking is not configured.")
		return
	}
	connections, err := s.store.ListConnections(r.Context())
	if err != nil {
		s.logger.Error("load connections for sync", "error", err)
		s.syncNotice(w, http.StatusInternalServerError, "error", "Could not check bank cooldowns.")
		return
	}
	accounts, err := s.store.ListAccounts(r.Context())
	if err != nil {
		s.logger.Error("load accounts for sync", "error", err)
		accounts = nil
	}
	if blocked, until := connectionsCoolingDown(connections, accounts, s.now()); blocked {
		s.syncNotice(w, http.StatusOK, "info", rateLimitMessage(until))
		return
	}
	if s.trigger.Trigger("") {
		s.syncNotice(w, http.StatusAccepted, "success", "Sync requested. Data refreshes shortly.")
		return
	}
	s.syncNotice(w, http.StatusAccepted, "info", "A sync is already queued or running.")
}

func (s *Server) handleSyncConnection(w http.ResponseWriter, r *http.Request) {
	if !validSyncRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if s.trigger == nil {
		s.syncNotice(w, http.StatusServiceUnavailable, "error", "Banking is not configured.")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.syncNotice(w, http.StatusNotFound, "error", "Connection not found.")
		return
	}
	connection, err := s.store.GetConnectionByID(r.Context(), id)
	if err != nil {
		s.syncNotice(w, http.StatusNotFound, "error", "Connection not found.")
		return
	}
	accounts, err := s.store.ListAccounts(r.Context())
	if err != nil {
		s.logger.Error("load accounts for sync", "error", err)
		accounts = nil
	}
	if blocked, until := connectionCoolingDown(connection, accounts, s.now()); blocked {
		s.syncNotice(w, http.StatusOK, "info", rateLimitMessage(until))
		return
	}
	if s.trigger.Trigger(connection.RequisitionID) {
		s.syncNotice(w, http.StatusAccepted, "success", "Sync requested for "+connection.InstitutionName+".")
		return
	}
	s.syncNotice(w, http.StatusAccepted, "info", "A sync is already queued or running.")
}

func rateLimitMessage(until time.Time) string {
	when := "soon"
	if !until.IsZero() {
		when = until.UTC().Format("2006-01-02 15:04 UTC")
	}
	return "Bank rate limit reached (PSD2 max daily limit). Displaying cached data. Next sync available at " + when + "."
}

func connectionsCoolingDown(connections []domain.Connection, accounts []domain.Account, now time.Time) (bool, time.Time) {
	if len(connections) == 0 {
		return false, time.Time{}
	}
	var earliest time.Time
	for _, connection := range connections {
		blocked, until := connectionCoolingDown(connection, accounts, now)
		if !blocked {
			return false, time.Time{}
		}
		earliest = earlier(earliest, until)
	}
	return true, earliest
}

func connectionCoolingDown(connection domain.Connection, accounts []domain.Account, now time.Time) (bool, time.Time) {
	if connection.CoolingDown(now) {
		return true, *connection.RateLimitedUntil
	}

	var (
		earliest time.Time
		count    int
	)
	for _, account := range accounts {
		if account.ConnectionID != connection.ID {
			continue
		}
		count++
		if !account.CoolingDown(now) {
			return false, time.Time{}
		}
		earliest = earlier(earliest, *account.RateLimitedUntil)
	}
	if count == 0 {
		return false, time.Time{}
	}
	return true, earliest
}

func earlier(current, candidate time.Time) time.Time {
	if current.IsZero() || candidate.Before(current) {
		return candidate
	}
	return current
}

func (s *Server) handleRecategorize(w http.ResponseWriter, r *http.Request) {
	if !validSyncRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if s.classifier == nil || s.classifier.RuleCount() == 0 {
		s.syncNotice(w, http.StatusServiceUnavailable, "error", "No categorization rules are configured.")
		return
	}
	result, err := s.store.Recategorize(r.Context(), s.classifier.Classify, true)
	if err != nil {
		s.logger.Error("recategorize transactions", "error", err)
		s.syncNotice(w, http.StatusInternalServerError, "error", "Could not re-run category rules.")
		return
	}
	message := fmt.Sprintf("Examined %d transactions; updated %d.", result.Examined, result.Changed)
	s.syncNotice(w, http.StatusOK, "success", message)
}

func validSyncRequest(r *http.Request) bool {
	return r.Header.Get(syncRequestHeader) == "1"
}

func (s *Server) syncNotice(w http.ResponseWriter, status int, class, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<div class="notice %s" role="status">%s</div>`, class, html.EscapeString(message))
}

func parsePage(raw string) int {
	page, err := strconv.Atoi(raw)
	if err != nil || page < 0 {
		return 0
	}
	if page > 10000 {
		return 10000
	}
	return page
}
