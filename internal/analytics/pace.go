package analytics

import (
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// Pace is the payload for GET /api/analytics/pace.
type Pace struct {
	Currency   string         `json:"currency"`
	Month      string         `json:"month"`
	Today      int            `json:"today"`
	MinorScale int            `json:"minor_scale"`
	SpentMinor int64          `json:"spent_minor"`
	Current    []*int64       `json:"current"`
	Average    []*int64       `json:"average"`
	Baseline   []PaceBaseline `json:"baseline"`
	Velocity   string         `json:"velocity"`
	DeltaBP    int64          `json:"delta_bp"`
}

// PaceBaseline is one historical cumulative spending curve.
type PaceBaseline struct {
	Month  string   `json:"month"`
	Label  string   `json:"label"`
	Points []*int64 `json:"points"`
}

// ComputePace builds the current month cumulative pace and the average of the
// previous six complete months.
func ComputePace(transactions []domain.Transaction, now time.Time, currency string) Pace {
	currency = normalizeCurrency(currency)
	now = now.UTC()
	today := now.Day()
	month := monthStart(now)
	spending := spendingTransactions(transactions, currency)

	current := make([]*int64, 31)
	var cumulative int64
	for day := 1; day <= today && day <= 31; day++ {
		for _, tx := range spending {
			if !inRange(tx.BookingDate, month, month.AddDate(0, 1, 0)) || tx.AmountMinor >= 0 {
				continue
			}
			if tx.BookingDate.UTC().Day() == day {
				cumulative += -tx.AmountMinor
			}
		}
		value := cumulative
		current[day-1] = &value
	}
	spent := cumulative

	baselines := make([]PaceBaseline, 0, 6)
	for i := 6; i >= 1; i-- {
		start := month.AddDate(0, -i, 0)
		days := start.AddDate(0, 1, 0).AddDate(0, 0, -1).Day()
		points := make([]*int64, 31)
		var total int64
		for day := 1; day <= days; day++ {
			for _, tx := range spending {
				if !inRange(tx.BookingDate, start, start.AddDate(0, 1, 0)) || tx.AmountMinor >= 0 {
					continue
				}
				if tx.BookingDate.UTC().Day() == day {
					total += -tx.AmountMinor
				}
			}
			value := total
			points[day-1] = &value
		}
		baselines = append(baselines, PaceBaseline{
			Month:  start.Format("2006-01"),
			Label:  start.Format("Jan 06"),
			Points: points,
		})
	}

	average := make([]*int64, 31)
	for day := 0; day < 31; day++ {
		var sum int64
		var count int64
		for _, baseline := range baselines {
			if day < len(baseline.Points) && baseline.Points[day] != nil {
				sum += *baseline.Points[day]
				count++
			}
		}
		if count > 0 {
			value := sum / count
			average[day] = &value
		}
	}

	var currentValue, averageValue int64
	if today >= 1 && today <= len(current) && current[today-1] != nil {
		currentValue = *current[today-1]
	}
	if today >= 1 && today <= len(average) && average[today-1] != nil {
		averageValue = *average[today-1]
	}
	velocity := "on_track"
	var deltaBP int64
	if averageValue != 0 {
		deltaBP = (currentValue - averageValue) * 10000 / averageValue
		switch {
		case deltaBP > 500:
			velocity = "above"
		case deltaBP < -500:
			velocity = "below"
		}
	}

	return Pace{
		Currency:   currency,
		Month:      month.Format("2006-01"),
		Today:      today,
		MinorScale: money.Scale(currency),
		SpentMinor: spent,
		Current:    current,
		Average:    average,
		Baseline:   baselines,
		Velocity:   velocity,
		DeltaBP:    deltaBP,
	}
}
