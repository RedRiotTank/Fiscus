// Package events defines the outbound event vocabulary shared by ingestion,
// alerting, and webhook delivery.
package events

import "context"

// Event names supported by generic outbound webhooks.
const (
	TransactionCreated = "transaction.created"
	AlertTriggered     = "alert.triggered"
	SyncCompleted      = "sync.completed"
)

// Sink accepts asynchronous domain events. Implementations must not block the
// caller for the duration of network delivery.
type Sink interface {
	Emit(ctx context.Context, event string, data any)
}
