// Package web serves the embedded Fiscus dashboard and operational endpoints.
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/analysis"
	"github.com/RedRiotTank/Fiscus/internal/attachments"
	"github.com/RedRiotTank/Fiscus/internal/budgets"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
	"github.com/RedRiotTank/Fiscus/internal/search"
	"github.com/RedRiotTank/Fiscus/internal/storage"
	"github.com/RedRiotTank/Fiscus/internal/tax"
	"github.com/RedRiotTank/Fiscus/pkg/icons"
	"golang.org/x/crypto/bcrypt"
)

//go:embed templates/*.html templates/partials/*.html static
var assets embed.FS

// Store is the read surface required by the dashboard.
type Store interface {
	ListAccounts(ctx context.Context) ([]domain.Account, error)
	RecentTransactions(ctx context.Context, limit int) ([]domain.Transaction, error)
	ListTransactions(ctx context.Context, filter storage.TransactionFilter) ([]domain.Transaction, error)
	GetTransaction(ctx context.Context, id int64) (domain.Transaction, error)
	UpdateTransactionCategory(ctx context.Context, id int64, category string) error
	UpsertLearnedRule(ctx context.Context, pattern, category, source string) (domain.LearnedRule, error)
	ListAlerts(ctx context.Context, limit int) ([]domain.Alert, error)
	ListConnections(ctx context.Context) ([]domain.Connection, error)
	GetConnectionByID(ctx context.Context, id int64) (domain.Connection, error)
	GetReconciliation(ctx context.Context, accountID string) (domain.Reconciliation, error)
	AttachmentIDsForTransactions(ctx context.Context, transactionIDs []int64) (map[int64]int64, error)
	CountTransactions(ctx context.Context) (int, error)
	CountTransactionsFiltered(ctx context.Context, accountIDs []string) (int, error)
	SpendingByCategory(ctx context.Context, from, to time.Time) ([]storage.CategorySpending, error)
	SpendingByCategoryForAccounts(ctx context.Context, from, to time.Time, accountIDs []string) ([]storage.CategorySpending, error)
	ListTransactionsBetween(ctx context.Context, from, to time.Time) ([]domain.Transaction, error)
	UpdateTransactionNotes(ctx context.Context, id int64, notes string) error
	SetAccountType(ctx context.Context, accountID, accountType string) error
	Recategorize(ctx context.Context, classify func(domain.Transaction) string, apply bool) (storage.RecategorizeResult, error)
	Ping(ctx context.Context) error
	SchemaVersion(ctx context.Context) (int, error)
}

// Classifier applies configured category rules to a transaction.
type Classifier interface {
	Classify(tx domain.Transaction) string
	RuleCount() int
	Categories() []string
}

// SyncTrigger requests an asynchronous bank sync.
type SyncTrigger interface {
	Trigger(requisitionID string) bool
}

// AuthorizationCallback completes a bank consent after an OAuth-style redirect.
type AuthorizationCallback interface {
	CompleteAuthorization(ctx context.Context, code, state string) (string, error)
}

// Config configures the web server.
type Config struct {
	Username       string
	Password       string
	PasswordHash   string
	Profile        string
	Version        string
	Logger         *slog.Logger
	Now            func() time.Time
	ReadyTimeout   time.Duration
	MetricsHandler http.Handler
	AuthHandler    AuthorizationCallback
	Classifier     Classifier
	BudgetPlanner  *budgets.Planner
	BudgetAdvisory budgets.AdvisoryConfig
	IconOverrides  map[string]string
	Attachments    *attachments.Service
	Tax            *tax.Service
	Search         *search.Service
}

// Server renders dashboard pages and exposes health endpoints.
type Server struct {
	store          Store
	analyzer       *analysis.Analyzer
	trigger        SyncTrigger
	logger         *slog.Logger
	now            func() time.Time
	version        string
	username       string
	password       string
	passwordHash   string
	profile        string
	readyTimeout   time.Duration
	metricsHandler http.Handler
	authHandler    AuthorizationCallback
	classifier     Classifier
	budgetPlanner  *budgets.Planner
	budgetAdvisory budgets.AdvisoryConfig
	iconResolver   *icons.Resolver
	brandIcons     map[string]struct{}
	attachments    *attachments.Service
	tax            *tax.Service
	search         *search.Service

	pages    map[string]*template.Template
	partials *template.Template
	handler  http.Handler
}

// New constructs a Server and parses the embedded templates.
func New(cfg Config, store Store, trigger SyncTrigger) (*Server, error) {
	if store == nil {
		return nil, errors.New("web: store is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.ReadyTimeout <= 0 {
		cfg.ReadyTimeout = 3 * time.Second
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	profile := domain.NormalizeWorkspaceProfile(cfg.Profile)
	analyzer, err := analysis.NewAnalyzer(store, cfg.Now)
	if err != nil {
		return nil, err
	}
	s := &Server{
		store:          store,
		analyzer:       analyzer,
		trigger:        trigger,
		logger:         cfg.Logger,
		now:            cfg.Now,
		version:        cfg.Version,
		username:       cfg.Username,
		password:       cfg.Password,
		passwordHash:   cfg.PasswordHash,
		profile:        profile,
		readyTimeout:   cfg.ReadyTimeout,
		metricsHandler: cfg.MetricsHandler,
		authHandler:    cfg.AuthHandler,
		classifier:     cfg.Classifier,
		budgetPlanner:  cfg.BudgetPlanner,
		budgetAdvisory: budgets.NormalizeAdvisoryConfig(cfg.BudgetAdvisory),
		iconResolver:   icons.New(cfg.IconOverrides),
		brandIcons:     embeddedBrandIcons(),
		attachments:    cfg.Attachments,
		tax:            cfg.Tax,
		search:         cfg.Search,
	}
	if err := s.parseTemplates(); err != nil {
		return nil, err
	}
	s.handler = s.routes()
	return s, nil
}

// Handler returns the complete HTTP handler, including security, logging, and
// authentication middleware.
func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) templateFuncs() template.FuncMap {
	return template.FuncMap{
		"money":               formatMoney,
		"date":                formatDate,
		"datetime":            formatDateTime,
		"ago":                 s.formatAgo,
		"statusLabel":         statusLabel,
		"statusClass":         statusClass,
		"severityClass":       severityClass,
		"externalURL":         externalURL,
		"reconciliationLabel": reconciliationLabel,
		"reconciliationClass": reconciliationClass,
		"versionLabel":        formatVersionLabel,
		"serviceInitials":     serviceInitials,
		"serviceClass":        serviceClass,
		"brandIcon":           s.brandIcon,
		"categoryClass":       categoryClass,
		"bpsPercent":          bpsPercent,
		"bpsInput":            bpsInput,
		"categoryEditor":      categoryEditor,
		"isBizum":             isBizum,
	}
}

func (s *Server) parseTemplates() error {
	s.pages = make(map[string]*template.Template)
	for _, page := range []string{"dashboard", "transactions", "budgets", "subscriptions", "alerts", "connections", "analytics", "error"} {
		tmpl, err := template.New("layout").Funcs(s.templateFuncs()).ParseFS(assets,
			"templates/layout.html",
			"templates/partials/*.html",
			"templates/"+page+".html",
		)
		if err != nil {
			return fmt.Errorf("web: parse %s template: %w", page, err)
		}
		s.pages[page] = tmpl
	}
	partials, err := template.New("partials").Funcs(s.templateFuncs()).ParseFS(assets, "templates/partials/*.html")
	if err != nil {
		return fmt.Errorf("web: parse partial templates: %w", err)
	}
	s.partials = partials
	return nil
}

func (s *Server) routes() http.Handler {
	app := http.NewServeMux()
	app.HandleFunc("GET /{$}", s.handleDashboard)
	app.HandleFunc("GET /transactions", s.handleTransactions)
	app.HandleFunc("GET /transactions/export", s.handleExport)
	app.HandleFunc("GET /budgets", s.handleBudgets)
	app.HandleFunc("GET /api/budgets/customize", s.handleBudgetCustomize)
	app.HandleFunc("POST /api/budgets/customize", s.handleBudgetCustomizeSave)
	app.HandleFunc("POST /api/budgets/advisory/toggle", s.handleBudgetAdvisoryToggle)
	app.HandleFunc("POST /attachments", s.handleAttachmentUpload)
	app.HandleFunc("POST /attachments/repair", s.handleAttachmentRepair)
	app.HandleFunc("POST /attachments/{id}/pair", s.handleAttachmentPair)
	app.HandleFunc("GET /attachments/{id}", s.handleAttachmentDownload)
	app.HandleFunc("GET /api/attachments/unpaired", s.handleUnpairedAttachments)
	app.HandleFunc("PATCH /api/transactions/{id}/notes", s.handleTransactionNotes)
	app.HandleFunc("PATCH /api/transactions/{id}/category", s.handleTransactionCategory)
	app.HandleFunc("POST /api/accounts/{id}/type", s.handleAccountType)
	app.HandleFunc("GET /subscriptions", s.handleSubscriptions)
	app.HandleFunc("GET /alerts", s.handleAlerts)
	app.HandleFunc("GET /connections", s.handleConnections)
	app.HandleFunc("GET /analytics", s.handleAnalytics)
	app.HandleFunc("GET /api/analytics/spending", s.handleAnalyticsSpending)
	app.HandleFunc("GET /api/analytics/pace", s.handleAnalyticsPace)
	app.HandleFunc("GET /api/analytics/activity", s.handleAnalyticsActivity)
	app.HandleFunc("GET /api/analytics/projections", s.handleAnalyticsProjections)
	app.HandleFunc("POST /api/analytics/scenarios/simulate", s.handleAnalyticsScenario)
	app.HandleFunc("GET /api/analytics/liquidity", s.handleAnalyticsLiquidity)
	app.HandleFunc("POST /sync", s.handleSyncAll)
	app.HandleFunc("POST /recategorize", s.handleRecategorize)
	app.HandleFunc("POST /connections/{id}/sync", s.handleSyncConnection)
	app.HandleFunc("GET /partials/overview", s.handleOverviewPartial)
	app.HandleFunc("GET /partials/alerts", s.handleAlertsPartial)
	app.HandleFunc("GET /partials/connections", s.handleConnectionsPartial)

	staticSub, err := fs.Sub(assets, "static")
	if err != nil {
		// Embedded assets are compile-time constants; a failure here is a build bug.
		panic(fmt.Sprintf("web: static assets: %v", err))
	}
	staticHandler := http.StripPrefix("/static/", http.FileServer(http.FS(staticSub)))
	app.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		staticHandler.ServeHTTP(w, r)
	}))

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", s.handleHealth)
	root.HandleFunc("GET /readyz", s.handleReady)
	root.HandleFunc("GET /callback", s.handleAuthorizationCallback)
	if s.metricsHandler != nil {
		root.Handle("GET /metrics", s.metricsHandler)
	}
	root.Handle("/", s.requireAuth(s.workspaceMiddleware(app)))

	return s.recoverer(s.securityHeaders(s.requestLogger(root)))
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	if s.username == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || !s.validCredentials(username, password) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Fiscus", charset="UTF-8"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) validCredentials(username, password string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(s.username)) == 1
	if s.passwordHash != "" {
		hashOK := bcrypt.CompareHashAndPassword([]byte(s.passwordHash), []byte(password)) == nil
		return userOK && hashOK
	}
	passOK := subtle.ConstantTimeCompare([]byte(password), []byte(s.password)) == 1
	return userOK && passOK
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'; "+
				"img-src 'self' data:; style-src 'self' 'unsafe-inline'; "+
				"script-src 'self' 'unsafe-inline'; connect-src 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(data)
	r.bytes += n
	return n, err
}

// Unwrap allows http.ResponseController to access the underlying writer.
func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := s.now()
		recorder := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		duration := s.now().Sub(started)
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"duration", duration.String(),
			"bytes", recorder.bytes,
		}
		if recorder.status >= 500 {
			s.logger.Warn("http request failed", attrs...)
		} else {
			s.logger.Debug("http request", attrs...)
		}
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("panic serving request",
					"panic", recovered, "path", r.URL.Path, "stack", string(debug.Stack()))
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": s.version,
		"time":    s.now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.readyTimeout)
	defer cancel()

	pingErr := s.store.Ping(ctx)
	version, schemaErr := s.store.SchemaVersion(ctx)
	ready := pingErr == nil && schemaErr == nil && version > 0
	payload := map[string]any{
		"version": s.version,
		"time":    s.now().UTC().Format(time.RFC3339),
	}
	if ready {
		payload["status"] = "ready"
		payload["database"] = "ok"
		payload["schema_version"] = version
		s.writeJSON(w, http.StatusOK, payload)
		return
	}
	payload["status"] = "not_ready"
	payload["database"] = "unavailable"
	s.logger.Warn("readiness check failed", "ping_error", pingErr, "schema_error", schemaErr)
	s.writeJSON(w, http.StatusServiceUnavailable, payload)
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		s.logger.Error("encode JSON response", "error", err)
	}
}

func (s *Server) render(w http.ResponseWriter, page string, data PageData) {
	tmpl, ok := s.pages[page]
	if !ok {
		s.logger.Error("unknown template", "page", page)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		s.logger.Error("render page", "page", page, "error", err)
	}
}

func (s *Server) renderPartial(w http.ResponseWriter, name string, data PageData) {
	s.renderPartialData(w, name, data)
}

func (s *Server) renderPartialData(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.partials.ExecuteTemplate(w, name, data); err != nil {
		s.logger.Error("render partial", "partial", name, "error", err)
	}
}

func (s *Server) renderError(w http.ResponseWriter, status int, message string) {
	data := PageData{
		Title:        "Error",
		Version:      s.version,
		Now:          s.now().UTC(),
		ErrorMessage: message,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := s.pages["error"].ExecuteTemplate(w, "layout", data); err != nil {
		s.logger.Error("render error page", "error", err)
	}
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Error("dashboard request failed", "path", r.URL.Path, "error", err)
	s.renderError(w, http.StatusInternalServerError, "Fiscus could not load this data. Check the daemon logs for details.")
}

func serviceInitials(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.Contains(normalized, "netflix"):
		return "N"
	case strings.Contains(normalized, "spotify"):
		return "S"
	case strings.Contains(normalized, "amazon web services") || strings.Contains(normalized, "aws"):
		return "AWS"
	case strings.Contains(normalized, "github"):
		return "GH"
	case strings.Contains(normalized, "vodafone"):
		return "V"
	case strings.Contains(normalized, "cloudflare"):
		return "CF"
	case strings.Contains(normalized, "jetbrains"):
		return "JB"
	case strings.Contains(normalized, "adobe"):
		return "A"
	case strings.Contains(normalized, "stripe"):
		return "S"
	case strings.Contains(normalized, "apple"):
		return "A"
	case strings.Contains(normalized, "namecheap"):
		return "NC"
	case strings.Contains(normalized, "figma"):
		return "F"
	}
	words := strings.Fields(name)
	initials := make([]rune, 0, 2)
	for _, word := range words {
		runes := []rune(word)
		if len(runes) == 0 {
			continue
		}
		initials = append(initials, runes[0])
		if len(initials) == 2 {
			break
		}
	}
	if len(initials) == 0 {
		return "?"
	}
	return strings.ToUpper(string(initials))
}

func serviceClass(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.Contains(normalized, "netflix"):
		return "service-netflix"
	case strings.Contains(normalized, "spotify"):
		return "service-spotify"
	case strings.Contains(normalized, "amazon web services") || strings.Contains(normalized, "aws"):
		return "service-aws"
	case strings.Contains(normalized, "github"):
		return "service-github"
	case strings.Contains(normalized, "vodafone"):
		return "service-vodafone"
	case strings.Contains(normalized, "cloudflare"):
		return "service-cloudflare"
	case strings.Contains(normalized, "jetbrains"):
		return "service-jetbrains"
	case strings.Contains(normalized, "adobe"):
		return "service-adobe"
	default:
		return "service-default"
	}
}

func embeddedBrandIcons() map[string]struct{} {
	entries, err := fs.ReadDir(assets, "static/icons/brands")
	if err != nil {
		return map[string]struct{}{}
	}
	out := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".svg") {
			continue
		}
		out[strings.TrimSuffix(entry.Name(), ".svg")] = struct{}{}
	}
	return out
}

func (s *Server) brandIcon(name string) string {
	if s.iconResolver == nil {
		return ""
	}
	slug, ok := s.iconResolver.Resolve(name)
	if !ok {
		return ""
	}
	if _, embedded := s.brandIcons[slug]; !embedded {
		return ""
	}
	return "/static/icons/brands/" + slug + ".svg"
}

func formatVersionLabel(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimLeft(value, "vV")
	if value == "" {
		return "dev"
	}
	return "v" + value
}

func formatMoney(minor int64, currency string) string {
	amount := money.FormatGrouped(minor, currency)
	symbol := currencySymbols[strings.ToUpper(currency)]
	if symbol == "" {
		return amount + " " + strings.ToUpper(currency)
	}
	if strings.HasPrefix(amount, "-") {
		return "-" + symbol + strings.TrimPrefix(amount, "-")
	}
	return symbol + amount
}

var currencySymbols = map[string]string{
	"EUR": "€", "USD": "$", "GBP": "£", "JPY": "¥", "CHF": "CHF ",
	"SEK": "kr ", "NOK": "kr ", "DKK": "kr ", "PLN": "zł ", "CZK": "Kč ",
}

func reconciliationLabel(rec domain.Reconciliation) string {
	if rec.AccountID == "" {
		return "Not checked"
	}
	if rec.Balanced() {
		return "Reconciled"
	}
	currency := rec.Currency
	if currency == "" {
		currency = "EUR"
	}
	amount := formatMoney(rec.LastDeltaMinor, currency)
	if !strings.HasPrefix(amount, "-") {
		amount = "+" + amount
	}
	return "Discrepancy: " + amount
}

func reconciliationClass(rec domain.Reconciliation) string {
	if rec.AccountID == "" {
		return "muted"
	}
	if rec.Balanced() {
		return "ok"
	}
	return "error"
}

func formatDate(value any) string {
	t := asTime(value)
	if t.IsZero() {
		return "—"
	}
	return t.UTC().Format("02 Jan 2006")
}

func formatDateTime(value any) string {
	t := asTime(value)
	if t.IsZero() {
		return "—"
	}
	return t.UTC().Format("02 Jan 2006 15:04")
}

func (s *Server) formatAgo(value any) string {
	t := asTime(value)
	if t.IsZero() {
		return "never"
	}
	delta := s.now().Sub(t)
	if delta < 0 {
		delta = 0
	}
	switch {
	case delta < time.Minute:
		return "just now"
	case delta < time.Hour:
		return fmt.Sprintf("%dm ago", int(delta.Minutes()))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(delta.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(delta.Hours()/24))
	}
}

func asTime(value any) time.Time {
	switch typed := value.(type) {
	case time.Time:
		return typed
	case *time.Time:
		if typed == nil {
			return time.Time{}
		}
		return *typed
	default:
		return time.Time{}
	}
}

func statusLabel(status string) string {
	labels := map[string]string{
		"CR": "Created", "GC": "Consent given", "UA": "Authenticating",
		"RJ": "Rejected", "SA": "Selecting accounts", "GA": "Granting access",
		"LN": "Linked", "EX": "Expired", "SU": "Suspended",
		"PENDING_AUTHORIZATION": "Waiting for bank", "RETURNED_FROM_BANK": "Returned from bank",
	}
	if label, ok := labels[status]; ok {
		return label
	}
	if status == "" {
		return "Unknown"
	}
	return status
}

func statusClass(status string) string {
	switch status {
	case "LN", "SA":
		return "ok"
	case "CR", "GC", "UA", "GA", "PENDING_AUTHORIZATION", "RETURNED_FROM_BANK":
		return "pending"
	case "RJ", "EX", "SU":
		return "error"
	default:
		return "muted"
	}
}

func severityClass(severity domain.Severity) string {
	switch severity {
	case domain.SeverityCritical:
		return "severity-critical"
	case domain.SeverityWarning:
		return "severity-warning"
	default:
		return "severity-info"
	}
}

func externalURL(raw string) string {
	if strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") {
		return raw
	}
	return "#"
}
