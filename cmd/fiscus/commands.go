package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/banking/gocardless"
	"github.com/RedRiotTank/Fiscus/internal/domain"
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
	institutionID := fs.String("institution", "", "GoCardless institution ID (see \"fiscus institutions\")")
	name := fs.String("name", "", "human-readable institution name for the dashboard")
	reference := fs.String("reference", "fiscus", "consent reference shown by the bank")
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
	if strings.TrimSpace(cfg.GoCardless.RedirectURL) == "" {
		return errors.New("gocardless.redirect_url is required to create a consent")
	}
	bank, err := buildBankClient(cfg)
	if err != nil {
		return err
	}
	institution := strings.TrimSpace(*institutionID)
	if institution == "" {
		institution = strings.TrimSpace(cfg.GoCardless.InstitutionID)
	}
	if institution == "" {
		return errors.New("-institution (or gocardless.institution_id) is required")
	}
	institutionName := strings.TrimSpace(*name)
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
		Reference:     strings.TrimSpace(*reference),
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

	_, _ = fmt.Fprintf(stdout, "Connection created.\n\n")
	_, _ = fmt.Fprintf(stdout, "  Requisition: %s\n", connection.RequisitionID)
	_, _ = fmt.Fprintf(stdout, "  Institution: %s (%s)\n", connection.InstitutionName, connection.InstitutionID)
	_, _ = fmt.Fprintf(stdout, "  Status:      %s\n\n", connection.Status)
	_, _ = fmt.Fprintf(stdout, "Open this link to authorize access at your bank:\n\n  %s\n\n", connection.Link)
	_, _ = fmt.Fprintf(stdout, "After authorizing, run \"fiscus sync\" or wait for the next scheduled sync.\n")
	return nil
}

func runInstitutions(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("institutions", stderr)
	configPath := fs.String("config", defaultConfigPath(), "path to YAML configuration")
	country := fs.String("country", "", "ISO 3166-1 alpha-2 country code (defaults to gocardless.country)")
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
	code := strings.ToUpper(strings.TrimSpace(*country))
	if code == "" {
		code = strings.ToUpper(strings.TrimSpace(cfg.GoCardless.Country))
	}
	if code == "" {
		return errors.New("-country (or gocardless.country) is required")
	}
	bank, err := buildBankClient(cfg)
	if err != nil {
		return err
	}

	ctx, cancel := commandContext(60 * time.Second)
	defer cancel()

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

	ctx, cancel := commandContext(cfg.GoCardless.SyncTimeout.Std())
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
	engine, err := buildEngine(cfg, store, logger)
	if err != nil {
		return err
	}
	worker, err := buildWorker(cfg, store, engine, logger)
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
