package budgets

import (
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/pkg/transfers"
)

func isExcludedCategory(category string) bool {
	return transfers.IsPassThroughCategory(category)
}

func isExcludedTransaction(tx domain.Transaction) bool {
	return transfers.IsExcluded(tx)
}
