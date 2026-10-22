package web

import (
	"context"
	"net/http"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

const workspaceCookie = "fiscus_workspace"

// WorkspaceOption is one selectable entry in the hybrid workspace switcher.
type WorkspaceOption struct {
	Value string
	Label string
}

// WorkspaceState is the resolved view state for a request.
type WorkspaceState struct {
	Profile  string
	View     string
	Locked   bool
	Label    string
	Options  []WorkspaceOption
	IsAll    bool
	Personal bool
	Business bool
}

func workspaceLabel(view string) string {
	switch view {
	case domain.WorkspacePersonal:
		return "Personal"
	case domain.WorkspaceBusiness:
		return "Business"
	default:
		return "All workspaces"
	}
}

func workspaceOptions() []WorkspaceOption {
	return []WorkspaceOption{
		{Value: domain.WorkspaceAll, Label: "All workspaces"},
		{Value: domain.WorkspacePersonal, Label: "Personal"},
		{Value: domain.WorkspaceBusiness, Label: "Business"},
	}
}

// resolveWorkspace determines the workspace view for a request. In locked
// profiles the configured value always wins; in hybrid mode an explicit query
// parameter or the persisted cookie selects the view.
func (s *Server) resolveWorkspace(r *http.Request) WorkspaceState {
	profile := domain.NormalizeWorkspaceProfile(s.profile)
	state := WorkspaceState{Profile: profile, Options: workspaceOptions()}
	if profile != domain.WorkspaceHybrid {
		state.View = profile
		state.Locked = true
	} else {
		state.View = domain.WorkspaceAll
		if r != nil {
			if requested := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("workspace"))); domain.ValidWorkspaceView(requested) {
				state.View = requested
			} else if cookie, err := r.Cookie(workspaceCookie); err == nil && domain.ValidWorkspaceView(cookie.Value) {
				state.View = strings.ToLower(strings.TrimSpace(cookie.Value))
			}
		}
	}
	state.Label = workspaceLabel(state.View)
	state.IsAll = state.View == domain.WorkspaceAll
	state.Personal = state.View == domain.WorkspacePersonal
	state.Business = state.View == domain.WorkspaceBusiness
	return state
}

func (s *Server) workspaceState(r *http.Request) WorkspaceState {
	return s.resolveWorkspace(r)
}

// workspaceMiddleware persists an explicit hybrid selection in a cookie so it
// survives navigation.
func (s *Server) workspaceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if domain.NormalizeWorkspaceProfile(s.profile) == domain.WorkspaceHybrid {
			if requested := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("workspace"))); domain.ValidWorkspaceView(requested) {
				// #nosec G124 -- Secure is enabled for TLS requests; plain-HTTP
				// self-hosting still needs the workspace preference to persist.
				http.SetCookie(w, &http.Cookie{
					Name:     workspaceCookie,
					Value:    requested,
					Path:     "/",
					MaxAge:   365 * 24 * 60 * 60,
					HttpOnly: true,
					Secure:   r.TLS != nil,
					SameSite: http.SameSiteLaxMode,
				})
			}
		}
		next.ServeHTTP(w, r)
	})
}

func accountMatchesWorkspace(account domain.Account, workspace string) bool {
	switch workspace {
	case domain.WorkspacePersonal:
		return domain.NormalizeAccountType(account.Type) == domain.AccountTypePersonal
	case domain.WorkspaceBusiness:
		return domain.NormalizeAccountType(account.Type) == domain.AccountTypeBusiness
	default:
		return true
	}
}

func filterAccounts(accounts []domain.Account, workspace string) []domain.Account {
	if workspace == domain.WorkspaceAll || workspace == "" {
		return accounts
	}
	out := make([]domain.Account, 0, len(accounts))
	for _, account := range accounts {
		if accountMatchesWorkspace(account, workspace) {
			out = append(out, account)
		}
	}
	return out
}

func allowedAccountIDs(accounts []domain.Account, workspace string) map[string]struct{} {
	out := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		if accountMatchesWorkspace(account, workspace) {
			out[account.ID] = struct{}{}
		}
	}
	return out
}

// accountIDsArg returns nil to mean "all accounts" for storage filters, and a
// non-nil (possibly empty) slice to mean "only these accounts". Callers must
// skip the query when a restricted view has no matching accounts.
func accountIDsArg(accounts []domain.Account, workspace string) []string {
	if workspace == domain.WorkspaceAll || workspace == "" {
		return nil
	}
	out := make([]string, 0, len(accounts))
	for _, account := range accounts {
		if accountMatchesWorkspace(account, workspace) {
			out = append(out, account.ID)
		}
	}
	return out
}

func filterTransactions(transactions []domain.Transaction, allowed map[string]struct{}, workspace string) []domain.Transaction {
	if workspace == domain.WorkspaceAll || workspace == "" {
		return transactions
	}
	out := make([]domain.Transaction, 0, len(transactions))
	for _, tx := range transactions {
		if _, ok := allowed[tx.AccountID]; ok {
			out = append(out, tx)
		}
	}
	return out
}

func (s *Server) workspaceAccounts(ctx context.Context, workspace string) ([]domain.Account, error) {
	accounts, err := s.store.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	return filterAccounts(accounts, workspace), nil
}
