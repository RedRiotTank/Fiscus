package tax

import (
	"context"
	"fmt"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// QuarterlyRule warns when the fiscal quarter is ending and the estimated tax
// reserve is non-zero.
type QuarterlyRule struct {
	service *Service
	now     func() time.Time
}

// NewQuarterlyRule constructs the tax-reserve alert rule.
func NewQuarterlyRule(service *Service, now func() time.Time) *QuarterlyRule {
	if now == nil {
		now = time.Now
	}
	return &QuarterlyRule{service: service, now: now}
}

// Name implements alerts.AnalysisRule.
func (r *QuarterlyRule) Name() string { return "tax_reserve" }

// Evaluate implements alerts.AnalysisRule.
func (r *QuarterlyRule) Evaluate(ctx context.Context) ([]domain.Alert, error) {
	if r.service == nil {
		return nil, nil
	}
	now := r.now().UTC()
	shields, err := r.service.ShieldsAt(ctx, now)
	if err != nil {
		return nil, err
	}
	var out []domain.Alert
	for _, shield := range shields {
		if shield.EstimatedTaxDueMinor <= 0 || !shield.Quarter.AlertWindow(now) {
			continue
		}
		message := fmt.Sprintf(
			"Reserve %s %s before %s: VAT %s + income tax %s. True liquid capital after reserve: %s %s.",
			money.FormatGrouped(shield.TaxReserveMinor, shield.Currency), shield.Currency,
			shield.Quarter.End.Format("02 Jan 2006"),
			money.FormatGrouped(shield.VATDueMinor, shield.Currency),
			money.FormatGrouped(shield.IncomeTaxMinor, shield.Currency),
			money.FormatGrouped(shield.TrueLiquidCapitalMinor, shield.Currency), shield.Currency,
		)
		out = append(out, domain.Alert{
			Rule:     r.Name(),
			Severity: domain.SeverityWarning,
			Title:    "Quarterly tax settlement approaching",
			Message:  message,
			DedupKey: fmt.Sprintf("tax_reserve:%s:%s", shield.Quarter.Start.Format("2006-01-02"), shield.Currency),
		})
	}
	return out, nil
}
