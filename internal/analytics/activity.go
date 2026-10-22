package analytics

import (
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// ActivityBucket is spending frequency for one amount bracket.
type ActivityBucket struct {
	Label  string   `json:"label"`
	Counts [7]int64 `json:"counts"`
	Totals [7]int64 `json:"totals"`
}

// Activity is the payload for GET /api/analytics/activity.
type Activity struct {
	Currency     string           `json:"currency"`
	From         string           `json:"from"`
	To           string           `json:"to"`
	MinorScale   int              `json:"minor_scale"`
	Weekdays     []string         `json:"weekdays"`
	Buckets      []ActivityBucket `json:"buckets"`
	Transactions int64            `json:"transactions"`
}

// ComputeActivity groups the last 90 days of outgoing transactions by weekday
// and amount bracket.
func ComputeActivity(transactions []domain.Transaction, now time.Time, currency string) Activity {
	currency = normalizeCurrency(currency)
	now = now.UTC()
	to := dayStart(now).AddDate(0, 0, 1)
	from := to.AddDate(0, 0, -90)
	spending := spendingTransactions(transactions, currency)
	scale := money.Scale(currency)
	pow := int64(1)
	for i := 0; i < scale; i++ {
		pow *= 10
	}
	labels := []string{"< 10", "10-30", "30-75", "75-200", "> 200"}
	buckets := make([]ActivityBucket, len(labels))
	for i, label := range labels {
		buckets[i] = ActivityBucket{Label: label}
	}
	var count int64
	for _, tx := range spending {
		if !inRange(tx.BookingDate, from, to) || tx.AmountMinor >= 0 {
			continue
		}
		amount := -tx.AmountMinor
		index := 0
		switch {
		case amount < 10*pow:
			index = 0
		case amount < 30*pow:
			index = 1
		case amount < 75*pow:
			index = 2
		case amount <= 200*pow:
			index = 3
		default:
			index = 4
		}
		weekday := (int(tx.BookingDate.UTC().Weekday()) + 6) % 7
		buckets[index].Counts[weekday]++
		buckets[index].Totals[weekday] += amount
		count++
	}
	return Activity{
		Currency:     currency,
		From:         from.Format("2006-01-02"),
		To:           to.AddDate(0, 0, -1).Format("2006-01-02"),
		MinorScale:   scale,
		Weekdays:     []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"},
		Buckets:      buckets,
		Transactions: count,
	}
}
