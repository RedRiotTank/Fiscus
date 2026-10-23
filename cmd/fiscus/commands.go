// Package main implements the Fiscus command-line entrypoint.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/backup"
	"github.com/RedRiotTank/Fiscus/internal/banking/enablebanking"
	"github.com/RedRiotTank/Fiscus/internal/banking/gocardless"
	"github.com/RedRiotTank/Fiscus/internal/categories"
	"github.com/RedRiotTank/Fiscus/internal/config"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
	"github.com/RedRiotTank/Fiscus/internal/tax"
	"github.com/RedRiotTank/Fiscus/internal/treasury"
)

func commandContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	return ctx, func() {
		cancel()
		stop()
	}
}

func runConnect(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("connect", stderr)
	configPath := fs.String("config", defaultConfigPath(), "path to YAML configuration")
	institution := fs.String("institution", "", "institution ID (GoCardless) or bank name (Enable Banking)")
	name := fs.String("name", "", "human-readable institution name for the dashboard")
	reference := fs.String("reference", "fiscus", "consent reference shown by the bank")
	country := fs.String("country", "", "ISO 3166-1 alpha-2 country code (Enable Banking)")
	psu := fs.String("psu", "", "PSU type for Enable Banking: personal or business")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := requireBanking(cfg); err != nil {
		return err
	}

	options := connectOptions{
		institution: strings.TrimSpace(*institution),
		name:        strings.TrimSpace(*name),
		reference:   strings.TrimSpace(*reference),
		country:     strings.TrimSpace(*country),
		psu:         strings.TrimSpace(*psu),
	}
	switch cfg.ProviderName() {
	case "enablebanking":
		return connectEnableBanking(cfg, options, stdout, stderr)
	default:
		return connectGoCardless(cfg, options, stdout, stderr)
	}
}

type connectOptions struct {
	institution string
	name        string
	reference   string
	country     string
	psu         string
}

func connectGoCardless(cfg *config.Config, options connectOptions, stdout, stderr io.Writer) error {
	if strings.TrimSpace(cfg.GoCardless.RedirectURL) == "" {
		return errors.New("gocardless.redirect_url is required to create a consent")
	}
	bank, err := buildGoCardlessClient(cfg)
	if err != nil {
		return err
	}
	institution := options.institution
	if institution == "" {
		institution = strings.TrimSpace(cfg.GoCardless.InstitutionID)
	}
	if institution == "" {
		return errors.New("-institution (or gocardless.institution_id) is required")
	}
	institutionName := options.name
	if institutionName == "" {
		institutionName = strings.TrimSpace(cfg.GoCardless.InstitutionName)
	}
	if institutionName == "" {
		institutionName = institution
	}

	ctx, cancel := commandContext(60 * time.Second)
	defer cancel()

	requisition, err := bank.CreateRequisition(ctx, gocardless.CreateRequisitionInput{
		InstitutionID: institution,
		Redirect:      cfg.GoCardless.RedirectURL,
		Reference:     options.reference,
	})
	if err != nil {
		return err
	}

	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := store.Close(); err != nil {
			_, _ = fmt.Fprintf(stderr, "fiscus: close database: %v\n", err)
		}
	}()

	connection := domain.Connection{
		RequisitionID:   requisition.ID,
		InstitutionID:   requisition.InstitutionID,
		InstitutionName: institutionName,
		Status:          requisition.Status,
		Link:            requisition.Link,
		Reference:       requisition.Reference,
		AgreementID:     requisition.AgreementID,
	}
	if connection.InstitutionID == "" {
		connection.InstitutionID = institution
	}
	if err := store.UpsertConnection(ctx, &connection); err != nil {
		return err
	}

	printConsent(stdout, connection)
	_, _ = fmt.Fprintf(stdout, "After authorizing, run \"fiscus sync\" or wait for the next scheduled sync.\n")
	return nil
}

func connectEnableBanking(cfg *config.Config, options connectOptions, stdout, stderr io.Writer) error {
	settings := cfg.EnableBanking
	if strings.TrimSpace(settings.RedirectURL) == "" {
		return errors.New("enablebanking.redirect_url is required to create a consent")
	}
	institution := options.institution
	if institution == "" {
		institution = strings.TrimSpace(settings.InstitutionName)
	}
	if institution == "" {
		return errors.New("-institution (or enablebanking.institution_name) is required")
	}
	country := strings.ToUpper(options.country)
	if country == "" {
		country = strings.ToUpper(strings.TrimSpace(settings.Country))
	}
	if country == "" {
		return errors.New("-country (or enablebanking.country) is required")
	}
	psuType := strings.ToLower(options.psu)
	if psuType == "" {
		psuType = strings.ToLower(strings.TrimSpace(settings.PSUType))
	}
	if psuType == "" {
		psuType = "personal"
	}

	ctx, cancel := commandContext(60 * time.Second)
	defer cancel()

	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := store.Close(); err != nil {
			_, _ = fmt.Fprintf(stderr, "fiscus: close database: %v\n", err)
		}
	}()

	client, err := buildEnableBankingClient(cfg)
	if err != nil {
		return err
	}
	aspsps, err := client.ListASPSPs(ctx, country, psuType)
	if err != nil {
		return err
	}
	aspsp, ok := findASPSP(aspsps, institution)
	if !ok {
		return fmt.Errorf("enablebanking bank %q was not found in %s; run \"fiscus institutions -country %s\"", institution, country, country)
	}

	validDays := settings.ConsentValidityDays
	if validDays <= 0 {
		validDays = 90
	}
	now := time.Now().UTC()
	validUntil := now.AddDate(0, 0, validDays)
	if seconds := aspsp.MaximumConsentValidity; seconds > 0 {
		if maxUntil := now.Add(time.Duration(seconds) * time.Second); validUntil.After(maxUntil) {
			validUntil = maxUntil
		}
	}

	authorization, err := client.CreateAuthorization(ctx, enablebanking.AuthorizationInput{
		ASPSPName:    aspsp.Name,
		ASPSPCountry: firstNonEmpty(aspsp.Country, country),
		PSUType:      psuType,
		RedirectURL:  settings.RedirectURL,
		ValidUntil:   validUntil,
	})
	if err != nil {
		return err
	}

	displayName := options.name
	if displayName == "" {
		displayName = firstNonEmpty(aspsp.Name, institution)
	}
	connection := domain.Connection{
		RequisitionID:    authorization.AuthorizationID,
		InstitutionID:    aspsp.Name,
		InstitutionName:  displayName,
		Status:           "PENDING_AUTHORIZATION",
		Link:             authorization.URL,
		Reference:        authorization.State,
		AgreementID:      authorization.AuthorizationID,
		ConsentExpiresAt: &validUntil,
	}
	if err := store.UpsertConnection(ctx, &connection); err != nil {
		return err
	}

	printConsent(stdout, connection)
	_, _ = fmt.Fprintf(stdout, "After authorizing, the bank redirects to %s.\n", settings.RedirectURL)
	_, _ = fmt.Fprintf(stdout, "Keep \"fiscus serve\" running so Fiscus can complete the consent and sync accounts.\n")
	return nil
}

func printConsent(stdout io.Writer, connection domain.Connection) {
	_, _ = fmt.Fprintf(stdout, "Connection created.\n\n")
	_, _ = fmt.Fprintf(stdout, "  Requisition: %s\n", connection.RequisitionID)
	_, _ = fmt.Fprintf(stdout, "  Institution: %s (%s)\n", connection.InstitutionName, connection.InstitutionID)
	_, _ = fmt.Fprintf(stdout, "  Status:      %s\n\n", connection.Status)
	_, _ = fmt.Fprintf(stdout, "Open this link to authorize access at your bank:\n\n  %s\n\n", connection.Link)
}

func runInstitutions(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("institutions", stderr)
	configPath := fs.String("config", defaultConfigPath(), "path to YAML configuration")
	country := fs.String("country", "", "ISO 3166-1 alpha-2 country code")
	psu := fs.String("psu", "", "PSU type for Enable Banking: personal or business")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := requireBanking(cfg); err != nil {
		return err
	}

	ctx, cancel := commandContext(60 * time.Second)
	defer cancel()

	switch cfg.ProviderName() {
	case "enablebanking":
		return institutionsEnableBanking(ctx, cfg, strings.TrimSpace(*country), strings.TrimSpace(*psu), stdout)
	default:
		return institutionsGoCardless(ctx, cfg, strings.TrimSpace(*country), stdout)
	}
}

func institutionsGoCardless(ctx context.Context, cfg *config.Config, country string, stdout io.Writer) error {
	code := strings.ToUpper(country)
	if code == "" {
		code = strings.ToUpper(strings.TrimSpace(cfg.GoCardless.Country))
	}
	if code == "" {
		return errors.New("-country (or gocardless.country) is required")
	}
	bank, err := buildGoCardlessClient(cfg)
	if err != nil {
		return err
	}
	institutions, err := bank.ListInstitutions(ctx, code)
	if err != nil {
		return err
	}
	if len(institutions) == 0 {
		_, _ = fmt.Fprintf(stdout, "No institutions found for %s.\n", code)
		return nil
	}

	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tNAME\tBIC")
	for _, institution := range institutions {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", institution.ID, institution.Name, institution.BIC)
	}
	return w.Flush()
}

func institutionsEnableBanking(ctx context.Context, cfg *config.Config, country, psu string, stdout io.Writer) error {
	code := strings.ToUpper(country)
	if code == "" {
		code = strings.ToUpper(strings.TrimSpace(cfg.EnableBanking.Country))
	}
	if code == "" {
		return errors.New("-country (or enablebanking.country) is required")
	}
	psuType := strings.ToLower(psu)
	if psuType == "" {
		psuType = strings.ToLower(strings.TrimSpace(cfg.EnableBanking.PSUType))
	}
	client, err := buildEnableBankingClient(cfg)
	if err != nil {
		return err
	}
	aspsps, err := client.ListASPSPs(ctx, code, psuType)
	if err != nil {
		return err
	}
	if len(aspsps) == 0 {
		_, _ = fmt.Fprintf(stdout, "No banks found for %s.\n", code)
		return nil
	}

	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tCOUNTRY\tBIC\tPSU TYPES\tMAX CONSENT")
	for _, aspsp := range aspsps {
		maxDays := "-"
		if aspsp.MaximumConsentValidity > 0 {
			maxDays = fmt.Sprintf("%dd", aspsp.MaximumConsentValidity/(24*60*60))
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			aspsp.Name, aspsp.Country, aspsp.BIC, strings.Join(aspsp.PSUTypes, ","), maxDays)
	}
	return w.Flush()
}

func findASPSP(aspsps []enablebanking.ASPSP, institution string) (enablebanking.ASPSP, bool) {
	institution = strings.TrimSpace(institution)
	for _, aspsp := range aspsps {
		if strings.EqualFold(aspsp.Name, institution) || (aspsp.BIC != "" && strings.EqualFold(aspsp.BIC, institution)) {
			return aspsp, true
		}
	}
	needle := strings.ToLower(institution)
	var match enablebanking.ASPSP
	matches := 0
	for _, aspsp := range aspsps {
		name := strings.ToLower(aspsp.Name)
		if strings.Contains(name, needle) || strings.Contains(needle, name) {
			match = aspsp
			matches++
		}
	}
	if matches == 1 {
		return match, true
	}
	return enablebanking.ASPSP{}, false
}

func runSync(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("sync", stderr)
	configPath := fs.String("config", defaultConfigPath(), "path to YAML configuration")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := requireBanking(cfg); err != nil {
		return err
	}
	logger := newLogger(cfg, stderr)

	timeout := cfg.GoCardless.SyncTimeout.Std()
	if cfg.ProviderName() == "enablebanking" {
		timeout = cfg.EnableBanking.SyncTimeout.Std()
	}
	ctx, cancel := commandContext(timeout)
	defer cancel()

	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := store.Close(); err != nil {
			logger.Error("close database", "error", err)
		}
	}()
	sink, err := buildEventSink(cfg, logger)
	if err != nil {
		return err
	}
	defer sink.Wait()
	engine, err := buildEngine(cfg, store, logger, nil, sink, nil)
	if err != nil {
		return err
	}
	bank, _, err := buildBankProvider(cfg, store, logger)
	if err != nil {
		return err
	}
	worker, err := buildWorker(cfg, store, engine, logger, nil, bank, sink)
	if err != nil {
		return err
	}
	if worker == nil {
		return errors.New("banking client is not configured")
	}

	_, _ = fmt.Fprintln(stdout, "Synchronizing bank connections...")
	if err := worker.SyncAll(ctx); err != nil {
		return fmt.Errorf("sync: %w", err)
	}
	_, _ = fmt.Fprintln(stdout, "Synchronization complete.")
	return nil
}

func runBackup(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("backup", stderr)
	configPath := fs.String("config", defaultConfigPath(), "path to YAML configuration")
	out := fs.String("out", "", "output .tar.gz path (default: timestamped file in backups.directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	ctx, cancel := commandContext(10 * time.Minute)
	defer cancel()
	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := store.Close(); err != nil {
			_, _ = fmt.Fprintf(stderr, "fiscus: close database: %v\n", err)
		}
	}()
	path := strings.TrimSpace(*out)
	if path == "" {
		path = backup.DefaultPath(cfg.Backups.Directory, time.Now().UTC())
	}
	result, err := backup.CreateArchive(ctx, store, cfg.Attachments.Directory, path)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	_, _ = fmt.Fprintf(stdout, "Backup written to %s (%d bytes, %d pages, %d attachment files).\n",
		result.Path, result.CompressedBytes, result.Pages, result.AttachmentFiles)
	return nil
}

func runRecategorize(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("recategorize", stderr)
	configPath := fs.String("config", defaultConfigPath(), "path to YAML configuration")
	dryRun := fs.Bool("dry-run", false, "preview proposed category changes without writing them")
	force := fs.Bool("force", false, "apply proposed category changes immediately")
	suggest := fs.Bool("suggest", false, "print YAML rule suggestions for frequent Uncategorized counterparties")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dryRun && *force {
		return errors.New("recategorize: --dry-run and --force cannot be used together")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	ctx, cancel := commandContext(5 * time.Minute)
	defer cancel()
	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := store.Close(); err != nil {
			_, _ = fmt.Fprintf(stderr, "fiscus: close database: %v\n", err)
		}
	}()

	if *suggest {
		transactions, err := store.ListTransactionsBetween(ctx, time.Time{}, time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC))
		if err != nil {
			return fmt.Errorf("recategorize: load transactions for rule discovery: %w", err)
		}
		suggestions := categories.Suggest(transactions, 3)
		if len(suggestions) == 0 {
			_, _ = fmt.Fprintln(stdout, "No Uncategorized counterparties appeared at least 3 times.")
			return nil
		}
		_, _ = fmt.Fprint(stdout, categories.YAML(suggestions))
		return nil
	}

	if len(cfg.Rules) == 0 {
		return errors.New("recategorize: no categorization rules are configured")
	}
	classifier, err := buildCategoryClassifier(cfg)
	if err != nil {
		return err
	}
	apply := *force && !*dryRun
	result, err := store.Recategorize(ctx, classifier.Classify, apply)
	if err != nil {
		return fmt.Errorf("recategorize: %w", err)
	}
	printRecategorizeResult(stdout, result, apply)
	return nil
}

func printRecategorizeResult(stdout io.Writer, result storage.RecategorizeResult, applied bool) {
	if result.Changed == 0 {
		_, _ = fmt.Fprintf(stdout, "Examined %d transactions; no category changes proposed.\n", result.Examined)
		return
	}
	verb := "proposed"
	if applied {
		verb = "applied"
	}
	_, _ = fmt.Fprintf(stdout, "Examined %d transactions; %d category change(s) %s.\n\n",
		result.Examined, result.Changed, verb)
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "FROM\tTO\tTRANSACTIONS")
	for _, shift := range result.Shifts {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\n", shift.From, shift.To, shift.Count)
	}
	_ = w.Flush()
	if !applied {
		_, _ = fmt.Fprintln(stdout, "\nDry run: no changes committed. Re-run with --force to apply.")
	}
}

func runExport(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("export", stderr)
	configPath := fs.String("config", defaultConfigPath(), "path to YAML configuration")
	formatRaw := fs.String("format", "csv", "export format: csv, hledger, beancount, or tax-summary")
	fromRaw := fs.String("from", "", "include transactions on or after this date (YYYY-MM-DD)")
	toRaw := fs.String("to", "", "include transactions on or before this date (YYYY-MM-DD)")
	yearRaw := fs.String("year", "", "tax-summary year (default: current year)")
	quarterRaw := fs.String("quarter", "", "tax-summary quarter: Q1, Q2, Q3, or Q4")
	includeTransfers := fs.Bool("include-transfers", false, "include internal transfers between own accounts")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	ctx, cancel := commandContext(2 * time.Minute)
	defer cancel()
	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := store.Close(); err != nil {
			_, _ = fmt.Fprintf(stderr, "fiscus: close database: %v\n", err)
		}
	}()

	if strings.EqualFold(strings.TrimSpace(*formatRaw), "tax-summary") {
		return exportTaxSummary(ctx, cfg, store, stdout, *yearRaw, *quarterRaw)
	}

	format, err := treasury.ParseFormat(*formatRaw)
	if err != nil {
		return err
	}
	from, err := parseExportDateFlag(*fromRaw, false)
	if err != nil {
		return fmt.Errorf("export: --from: %w", err)
	}
	to, err := parseExportDateFlag(*toRaw, true)
	if err != nil {
		return fmt.Errorf("export: --to: %w", err)
	}
	if to.IsZero() {
		to = time.Now().UTC().Add(24 * time.Hour)
	}
	if !from.IsZero() && !from.Before(to) {
		return errors.New("export: --from must be before --to")
	}

	transactions, err := store.ListTransactionsBetween(ctx, from, to)
	if err != nil {
		return err
	}
	if !*includeTransfers {
		pairs := treasury.PairTransfers(transactions, 3)
		transactions = treasury.ExcludeTransfers(transactions, pairs)
	}
	accounts, err := store.ListAccounts(ctx)
	if err != nil {
		return err
	}
	data, err := treasury.Export(format, transactions, accounts)
	if err != nil {
		return err
	}
	if _, err := stdout.Write(data); err != nil {
		return fmt.Errorf("export: write output: %w", err)
	}
	return nil
}

func exportTaxSummary(ctx context.Context, cfg *config.Config, store *storage.Store, stdout io.Writer, yearRaw, quarterRaw string) error {
	if !cfg.Tax.Enabled {
		return errors.New("export: tax estimation is disabled")
	}
	service, err := tax.New(store, configuredTaxConfig(cfg), time.Now)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	year := now.Year()
	if strings.TrimSpace(yearRaw) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(yearRaw))
		if err != nil || parsed < 2000 || parsed > 2200 {
			return errors.New("export: --year must be a four-digit year")
		}
		year = parsed
	}
	quarter := service.QuarterNumber(service.QuarterFor(now))
	if strings.TrimSpace(quarterRaw) != "" {
		parsed, err := parseTaxQuarter(quarterRaw)
		if err != nil {
			return err
		}
		quarter = parsed
	}
	shields, err := service.ShieldsForQuarter(ctx, year, quarter)
	if err != nil {
		return err
	}
	data, err := tax.SummaryCSV(shields)
	if err != nil {
		return err
	}
	if _, err := stdout.Write(data); err != nil {
		return fmt.Errorf("export: write tax summary: %w", err)
	}
	return nil
}

func parseTaxQuarter(raw string) (int, error) {
	value := strings.ToUpper(strings.TrimSpace(raw))
	value = strings.TrimPrefix(value, "Q")
	quarter, err := strconv.Atoi(value)
	if err != nil || quarter < 1 || quarter > 4 {
		return 0, errors.New("export: --quarter must be Q1, Q2, Q3, or Q4")
	}
	return quarter, nil
}

func parseExportDateFlag(raw string, endOfDay bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, errors.New("use YYYY-MM-DD")
	}
	if endOfDay {
		parsed = parsed.Add(24 * time.Hour)
	}
	return parsed, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
