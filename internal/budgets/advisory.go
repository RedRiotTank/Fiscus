package budgets

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/treasury"
	"github.com/RedRiotTank/Fiscus/pkg/housing"
)

// Advisory models.
const (
	AdvisoryModelNormative = "normative"
	AdvisoryModelHeuristic = "heuristic"
	AdvisoryModelHybrid    = "hybrid"
)

// Advisory buckets.
const (
	BucketNeeds       = "needs"
	BucketWants       = "wants"
	BucketSavings     = "savings"
	BucketUnallocated = "unallocated"
)

// Advisory statuses.
const (
	StatusBalanced      = "balanced"
	StatusOverAllocated = "over_allocated"
	StatusUnderBudget   = "under_budget"
	StatusUnallocated   = "unallocated"
)

// AdvisoryConfig controls the budget advisory calculations.
type AdvisoryConfig struct {
	Enabled                    bool
	Model                      string
	NeedsPercent               int
	WantsPercent               int
	SavingsPercent             int
	HeuristicWindowMonths      int
	OverbudgetWarningThreshold float64
	UnderbudgetNoticeThreshold float64
}

// CategoryAdvisory is the recommendation and status for one category.
type CategoryAdvisory struct {
	Category               string `json:"category"`
	Bucket                 string `json:"bucket"`
	Currency               string `json:"currency"`
	CurrentBudgetMinor     int64  `json:"current_budget_minor"`
	HistoricalAverageMinor int64  `json:"historical_average_minor"`
	HistoricalSigmaMinor   int64  `json:"historical_sigma_minor"`
	NormativeMinor         int64  `json:"normative_minor"`
	ConservativeMinor      int64  `json:"conservative_minor"`
	RealisticMinor         int64  `json:"realistic_minor"`
	RecommendedMinor       int64  `json:"recommended_minor"`
	ActualSpendMinor       int64  `json:"actual_spend_minor"`
	MonthsObserved         int    `json:"months_observed"`
	Status                 string `json:"status"`
	DeviationPercent       int64  `json:"deviation_percent"`
	SurplusMinor           int64  `json:"surplus_minor"`
}

// Advisory is the complete 50/30/20 and category recommendation for one
// currency.
type Advisory struct {
	Currency                string             `json:"currency"`
	NeedsPercent            int                `json:"needs_percent"`
	WantsPercent            int                `json:"wants_percent"`
	SavingsPercent          int                `json:"savings_percent"`
	MonthlyNetIncomeMinor   int64              `json:"monthly_net_income_minor"`
	MonthlyInflowMinor      int64              `json:"monthly_inflow_minor"`
	TaxProvisionMinor       int64              `json:"tax_provision_minor"`
	NeedsCapacityMinor      int64              `json:"needs_capacity_minor"`
	WantsCapacityMinor      int64              `json:"wants_capacity_minor"`
	SavingsCapacityMinor    int64              `json:"savings_capacity_minor"`
	NeedsSpentMinor         int64              `json:"needs_spent_minor"`
	WantsSpentMinor         int64              `json:"wants_spent_minor"`
	SavingsSpentMinor       int64              `json:"savings_spent_minor"`
	UnallocatedSpentMinor   int64              `json:"unallocated_spent_minor"`
	UnallocatedNeedsMinor   int64              `json:"unallocated_needs_minor"`
	UnallocatedWantsMinor   int64              `json:"unallocated_wants_minor"`
	UnallocatedSavingsMinor int64              `json:"unallocated_savings_minor"`
	NeedsAllocatedMinor     int64              `json:"needs_allocated_minor"`
	WantsAllocatedMinor     int64              `json:"wants_allocated_minor"`
	SavingsAllocatedMinor   int64              `json:"savings_allocated_minor"`
	Categories              []CategoryAdvisory `json:"categories"`
}

// NormalizeAdvisoryConfig applies safe defaults and clamps the window.
func NormalizeAdvisoryConfig(cfg AdvisoryConfig) AdvisoryConfig {
	model := strings.ToLower(strings.TrimSpace(cfg.Model))
	switch model {
	case AdvisoryModelNormative, AdvisoryModelHeuristic, AdvisoryModelHybrid:
		cfg.Model = model
	default:
		cfg.Model = AdvisoryModelHybrid
	}
	if cfg.NeedsPercent <= 0 && cfg.WantsPercent <= 0 && cfg.SavingsPercent <= 0 {
		cfg.NeedsPercent, cfg.WantsPercent, cfg.SavingsPercent = 50, 30, 20
	}
	if cfg.HeuristicWindowMonths <= 0 {
		cfg.HeuristicWindowMonths = 6
	}
	if cfg.HeuristicWindowMonths < 3 {
		cfg.HeuristicWindowMonths = 3
	}
	if cfg.HeuristicWindowMonths > 12 {
		cfg.HeuristicWindowMonths = 12
	}
	if cfg.OverbudgetWarningThreshold <= 0 {
		cfg.OverbudgetWarningThreshold = 0.15
	}
	if cfg.UnderbudgetNoticeThreshold <= 0 {
		cfg.UnderbudgetNoticeThreshold = 0.30
	}
	return cfg
}

// ComputeAdvisory derives 50/30/20 capacities and category recommendations.
// taxProvision is a monthly provision by currency.
func ComputeAdvisory(transactions []domain.Transaction, activeBudgets []Budget, cfg AdvisoryConfig, taxProvision map[string]int64, now time.Time) []Advisory {
	cfg = NormalizeAdvisoryConfig(cfg)
	if !cfg.Enabled {
		return nil
	}
	now = now.UTC()
	windowStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).
		AddDate(0, -(cfg.HeuristicWindowMonths - 1), 0)
	windowTransactions := make([]domain.Transaction, 0, len(transactions))
	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked {
			continue
		}
		if tx.BookingDate.Before(windowStart) || tx.BookingDate.After(now) {
			continue
		}
		windowTransactions = append(windowTransactions, tx)
	}
	if len(windowTransactions) == 0 {
		return nil
	}

	pairs := treasury.PairTransfers(windowTransactions, 3)
	internalIDs := make(map[int64]struct{}, len(pairs)*2)
	for _, pair := range pairs {
		if pair.Debit.ID > 0 {
			internalIDs[pair.Debit.ID] = struct{}{}
		}
		if pair.Credit.ID > 0 {
			internalIDs[pair.Credit.ID] = struct{}{}
		}
	}

	budgetByCategory := make(map[string]Budget, len(activeBudgets))
	for _, budget := range activeBudgets {
		normalized := Normalize(budget)
		budgetByCategory[strings.ToLower(normalized.Category)+"\x00"+normalized.Currency] = normalized
	}
	currencies := make(map[string]struct{})
	for _, tx := range windowTransactions {
		currencies[currencyOf(tx.Currency)] = struct{}{}
	}
	for _, budget := range activeBudgets {
		currencies[Normalize(budget).Currency] = struct{}{}
	}
	for currency := range taxProvision {
		currencies[currencyOf(currency)] = struct{}{}
	}
	currencyList := make([]string, 0, len(currencies))
	for currency := range currencies {
		currencyList = append(currencyList, currency)
	}
	sort.Strings(currencyList)

	out := make([]Advisory, 0, len(currencyList))
	for _, currency := range currencyList {
		advisory := computeCurrencyAdvisory(windowTransactions, internalIDs, pairs, budgetByCategory, cfg, currency, taxProvision[currency], windowStart, now)
		if advisory.Currency == "" || advisory.MonthlyNetIncomeMinor <= 0 {
			continue
		}
		out = append(out, advisory)
	}
	return out
}

func computeCurrencyAdvisory(
	transactions []domain.Transaction,
	internalIDs map[int64]struct{},
	pairs []treasury.TransferPair,
	budgets map[string]Budget,
	cfg AdvisoryConfig,
	currency string,
	taxProvision int64,
	windowStart, now time.Time,
) Advisory {
	var currencyTransactions []domain.Transaction
	for _, tx := range transactions {
		if currencyOf(tx.Currency) == currency {
			currencyTransactions = append(currencyTransactions, tx)
		}
	}
	if len(currencyTransactions) == 0 {
		return Advisory{}
	}
	months := observedMonths(currencyTransactions, windowStart, now, cfg.HeuristicWindowMonths)
	firstMonth := firstObservedMonth(currencyTransactions, windowStart)
	var inflow int64
	for _, tx := range currencyTransactions {
		if tx.AmountMinor <= 0 {
			continue
		}
		if _, internal := internalIDs[tx.ID]; internal {
			continue
		}
		if housing.IsReimbursement(tx) {
			continue
		}
		if excludedIncomeCategory(tx.Category) {
			continue
		}
		inflow += tx.AmountMinor
	}
	monthlyInflow := inflow / int64(months)
	if taxProvision < 0 {
		taxProvision = 0
	}
	netIncome := monthlyInflow - taxProvision
	if netIncome < 0 {
		netIncome = 0
	}

	seriesByCategory := make(map[string][]int64)
	categoryDisplay := make(map[string]string)
	for _, tx := range currencyTransactions {
		if tx.AmountMinor >= 0 {
			continue
		}
		if _, internal := internalIDs[tx.ID]; internal {
			continue
		}
		if excludedSpendingCategory(tx.Category) {
			continue
		}
		display := displayCategory(tx.Category)
		key := strings.ToLower(display)
		index := monthIndex(firstMonth, tx.BookingDate)
		if index < 0 || index >= months {
			continue
		}
		series := seriesByCategory[key]
		if series == nil {
			series = make([]int64, months)
		}
		series[index] += -tx.AmountMinor
		seriesByCategory[key] = series
		categoryDisplay[key] = display
	}
	for _, budget := range budgets {
		if budget.Currency != currency {
			continue
		}
		display := displayCategory(budget.Category)
		key := strings.ToLower(display)
		if _, ok := seriesByCategory[key]; !ok {
			seriesByCategory[key] = make([]int64, months)
			categoryDisplay[key] = display
		}
	}
	netHousingReimbursements(seriesByCategory, currencyTransactions, currency, firstMonth, months)

	var monthlySpendByBucket = map[string]int64{
		BucketNeeds:       0,
		BucketWants:       0,
		BucketSavings:     0,
		BucketUnallocated: 0,
	}
	var monthlySpendByCategory = make(map[string]int64, len(seriesByCategory))
	for key, series := range seriesByCategory {
		mu := averageMinor(series)
		monthlySpendByCategory[categoryDisplay[key]] = mu
		monthlySpendByBucket[BucketForCategory(categoryDisplay[key])] += mu
	}
	for _, pair := range pairs {
		if currencyOf(pair.Debit.Currency) == currency {
			monthlySpendByBucket[BucketSavings] += -pair.Debit.AmountMinor / int64(months)
		}
	}

	needsCapacity := netIncome * int64(cfg.NeedsPercent) / 100
	wantsCapacity := netIncome * int64(cfg.WantsPercent) / 100
	savingsCapacity := netIncome * int64(cfg.SavingsPercent) / 100
	advisory := Advisory{
		Currency:                currency,
		NeedsPercent:            cfg.NeedsPercent,
		WantsPercent:            cfg.WantsPercent,
		SavingsPercent:          cfg.SavingsPercent,
		MonthlyNetIncomeMinor:   netIncome,
		MonthlyInflowMinor:      monthlyInflow,
		TaxProvisionMinor:       taxProvision,
		NeedsCapacityMinor:      needsCapacity,
		WantsCapacityMinor:      wantsCapacity,
		SavingsCapacityMinor:    savingsCapacity,
		NeedsSpentMinor:         monthlySpendByBucket[BucketNeeds],
		WantsSpentMinor:         monthlySpendByBucket[BucketWants],
		SavingsSpentMinor:       monthlySpendByBucket[BucketSavings],
		UnallocatedSpentMinor:   monthlySpendByBucket[BucketUnallocated],
		UnallocatedNeedsMinor:   max(0, needsCapacity-monthlySpendByBucket[BucketNeeds]),
		UnallocatedWantsMinor:   max(0, wantsCapacity-monthlySpendByBucket[BucketWants]),
		UnallocatedSavingsMinor: max(0, savingsCapacity-monthlySpendByBucket[BucketSavings]),
	}
	for _, budget := range budgets {
		if budget.Currency != currency {
			continue
		}
		switch BucketForCategory(budget.Category) {
		case BucketNeeds:
			advisory.NeedsAllocatedMinor += budget.LimitMinor
		case BucketWants:
			advisory.WantsAllocatedMinor += budget.LimitMinor
		case BucketSavings:
			advisory.SavingsAllocatedMinor += budget.LimitMinor
		}
	}

	categories := make([]string, 0, len(seriesByCategory))
	for key := range seriesByCategory {
		categories = append(categories, categoryDisplay[key])
	}
	sort.Slice(categories, func(i, j int) bool {
		left, right := BucketForCategory(categories[i]), BucketForCategory(categories[j])
		if left != right {
			return bucketOrder(left) < bucketOrder(right)
		}
		return categories[i] < categories[j]
	})
	bucketMu := make(map[string]int64)
	for category, mu := range monthlySpendByCategory {
		bucketMu[BucketForCategory(category)] += mu
	}
	overBps := int64(math.Round(cfg.OverbudgetWarningThreshold * 10000))
	underBps := int64(math.Round(cfg.UnderbudgetNoticeThreshold * 10000))
	for _, category := range categories {
		series := seriesByCategory[strings.ToLower(category)]
		mu := averageMinor(series)
		sigma := stddevMinor(series, mu)
		currentBudget := int64(0)
		if budget, ok := budgets[strings.ToLower(category)+"\x00"+currency]; ok {
			currentBudget = budget.LimitMinor
		}
		bucket := BucketForCategory(category)
		bucketCapacity := bucketCapacityFor(bucket, needsCapacity, wantsCapacity, savingsCapacity)
		normative := normativeRecommendation(category, netIncome, bucketCapacity, mu, bucketMu[bucket])
		conservative := roundPercent(mu, 95)
		realistic := mu + sigma/2
		if realistic < 0 {
			realistic = 0
		}
		recommended := chooseRecommendation(cfg.Model, normative, realistic)
		status := StatusUnallocated
		deviation := int64(0)
		if recommended > 0 {
			deviation = (currentBudget - recommended) * 100 / recommended
		}
		if currentBudget > 0 && recommended > 0 {
			overCeiling := recommended + recommended*overBps/10000
			underCeiling := recommended - recommended*underBps/10000
			switch {
			case currentBudget > overCeiling:
				status = StatusOverAllocated
			case currentBudget < underCeiling || mu < underCeiling:
				status = StatusUnderBudget
			default:
				status = StatusBalanced
			}
		}
		advisory.Categories = append(advisory.Categories, CategoryAdvisory{
			Category:               category,
			Bucket:                 bucket,
			Currency:               currency,
			CurrentBudgetMinor:     currentBudget,
			HistoricalAverageMinor: mu,
			HistoricalSigmaMinor:   sigma,
			NormativeMinor:         normative,
			ConservativeMinor:      conservative,
			RealisticMinor:         realistic,
			RecommendedMinor:       recommended,
			ActualSpendMinor:       mu,
			MonthsObserved:         months,
			Status:                 status,
			DeviationPercent:       deviation,
			SurplusMinor:           max(0, max(recommended-mu, recommended-currentBudget)),
		})
	}
	return advisory
}

// netHousingReimbursements offsets flatmate transfers against the housing
// series without treating them as income. The series is floored at zero so an
// over-refunded month cannot create negative spending.
func netHousingReimbursements(seriesByCategory map[string][]int64, transactions []domain.Transaction, currency string, firstMonth time.Time, months int) {
	key := strings.ToLower(housing.Category)
	series, ok := seriesByCategory[key]
	if !ok {
		return
	}
	for _, tx := range transactions {
		if currencyOf(tx.Currency) != currency || !housing.IsReimbursement(tx) {
			continue
		}
		index := monthIndex(firstMonth, tx.BookingDate)
		if index < 0 || index >= months {
			continue
		}
		series[index] -= tx.AmountMinor
		if series[index] < 0 {
			series[index] = 0
		}
	}
	seriesByCategory[key] = series
}

func observedMonths(transactions []domain.Transaction, windowStart, now time.Time, maximum int) int {
	first := firstObservedMonth(transactions, windowStart)
	months := (now.Year()-first.Year())*12 + int(now.Month()-first.Month()) + 1
	if months < 1 {
		months = 1
	}
	if months > maximum {
		months = maximum
	}
	return months
}

func firstObservedMonth(transactions []domain.Transaction, windowStart time.Time) time.Time {
	var first time.Time
	for _, tx := range transactions {
		month := time.Date(tx.BookingDate.Year(), tx.BookingDate.Month(), 1, 0, 0, 0, 0, time.UTC)
		if first.IsZero() || month.Before(first) {
			first = month
		}
	}
	if first.IsZero() || first.Before(windowStart) {
		return windowStart
	}
	return first
}

func monthIndex(firstMonth, booking time.Time) int {
	return (booking.Year()-firstMonth.Year())*12 + int(booking.Month()-firstMonth.Month())
}

func averageMinor(series []int64) int64 {
	if len(series) == 0 {
		return 0
	}
	var total int64
	for _, value := range series {
		total += value
	}
	return total / int64(len(series))
}

func stddevMinor(series []int64, mean int64) int64 {
	if len(series) < 2 {
		return 0
	}
	var sum float64
	for _, value := range series {
		delta := float64(value - mean)
		sum += delta * delta
	}
	return int64(math.Round(math.Sqrt(sum / float64(len(series)))))
}

func roundPercent(value int64, percent int64) int64 {
	if value <= 0 {
		return 0
	}
	return (value*percent + 50) / 100
}

func bucketCapacityFor(bucket string, needs, wants, savings int64) int64 {
	switch bucket {
	case BucketNeeds:
		return needs
	case BucketWants:
		return wants
	case BucketSavings:
		return savings
	default:
		return 0
	}
}

func chooseRecommendation(model string, normative, realistic int64) int64 {
	switch model {
	case AdvisoryModelNormative:
		if normative > 0 {
			return normative
		}
		return realistic
	case AdvisoryModelHeuristic:
		if realistic > 0 {
			return realistic
		}
		return normative
	default:
		if normative > 0 && realistic > 0 {
			if normative < realistic {
				return normative
			}
			return realistic
		}
		if normative > 0 {
			return normative
		}
		return realistic
	}
}

func normativeRecommendation(category string, income, bucketCapacity, mu, bucketMu int64) int64 {
	if percent, ok := normativeCaps[strings.ToLower(strings.TrimSpace(category))]; ok {
		return income * percent / 100
	}
	if bucketMu <= 0 || bucketCapacity <= 0 {
		return 0
	}
	return bucketCapacity * mu / bucketMu
}

var normativeCaps = map[string]int64{
	"rent": 35, "mortgage": 35, "housing": 35, "housing / rent": 35,
	"groceries": 15, "utilities": 10, "electricity": 10, "gas": 10, "water": 10,
	"insurance": 5, "telecom": 5, "phone": 5, "internet": 5,
	"restaurants": 10, "dining": 10, "entertainment": 10, "ocio": 10,
	"subscriptions": 5, "shopping": 10, "travel": 10,
	"health": 5, "healthcare": 5, "medical": 5,
	"software": 5, "professional services": 5, "office": 5,
}

// BucketForCategory maps a category to a 50/30/20 bucket.
func BucketForCategory(category string) string {
	key := strings.ToLower(strings.TrimSpace(category))
	switch key {
	case "rent", "mortgage", "housing", "housing / rent", "utilities", "electricity", "gas",
		"water", "internet", "telecom", "phone", "groceries", "insurance",
		"health", "healthcare", "medical", "transport", "software",
		"professional services", "office", "hardware":
		return BucketNeeds
	case "restaurants", "dining", "bars", "cafes", "cafés", "coffee",
		"entertainment", "ocio", "leisure", "subscriptions", "shopping",
		"travel", "meals", "health & fitness", "bizum", "transfers / bizum":
		return BucketWants
	case "savings", "investments", "investment", "emergency fund":
		return BucketSavings
	default:
		return BucketUnallocated
	}
}

func bucketOrder(bucket string) int {
	switch bucket {
	case BucketNeeds:
		return 0
	case BucketWants:
		return 1
	case BucketSavings:
		return 2
	default:
		return 3
	}
}

func excludedIncomeCategory(category string) bool {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "transfer", "transfers", "transfers / p2p", "p2p", "housing", "housing / rent", "rent",
		"mortgage", "alquiler", "renta", "loan", "loans", "financing", "tax", "taxes", "vat", "irpf":
		return true
	default:
		return false
	}
}

func excludedSpendingCategory(category string) bool {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "transfer", "transfers", "transfers / p2p", "p2p", "loan", "loans", "financing",
		"tax", "taxes", "vat", "irpf":
		return true
	default:
		return false
	}
}

func displayCategory(category string) string {
	category = strings.TrimSpace(category)
	if housing.IsCategory(category) {
		return housing.Category
	}
	if category == "" {
		return "Uncategorized"
	}
	return category
}

func currencyOf(currency string) string {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return "EUR"
	}
	return currency
}
