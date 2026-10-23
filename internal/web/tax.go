package web

import "context"

func (s *Server) loadTaxShields(ctx context.Context, data *PageData) error {
	if s.tax == nil || data.Workspace.Personal {
		return nil
	}
	shields, err := s.tax.Shields(ctx)
	if err != nil {
		return err
	}
	data.TaxEnabled = true
	for _, shield := range shields {
		data.TaxShields = append(data.TaxShields, TaxShieldView{
			Currency:               shield.Currency,
			TotalBalanceMinor:      shield.TotalBalanceMinor,
			TaxReserveMinor:        shield.TaxReserveMinor,
			TrueLiquidCapitalMinor: shield.TrueLiquidCapitalMinor,
			VATDueMinor:            shield.VATDueMinor,
			VATCreditMinor:         shield.VATCreditMinor,
			IncomeTaxMinor:         shield.IncomeTaxMinor,
			QuarterEnd:             shield.Quarter.End,
		})
	}
	return nil
}
