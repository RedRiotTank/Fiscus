// Package observability exposes Prometheus metrics for ingestion and alerting.
package observability

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// Metrics implements the ingestion, engine, and delivery observer interfaces and
// owns a private Prometheus registry. A private registry keeps tests isolated and
// avoids accidental duplicate registrations from the default global registry.
type Metrics struct {
	registry *prometheus.Registry

	syncTotal         *prometheus.CounterVec
	syncDuration      *prometheus.HistogramVec
	transactionsTotal *prometheus.CounterVec
	balanceMinor      *prometheus.GaugeVec
	alertsTotal       *prometheus.CounterVec
	alertDispatch     *prometheus.CounterVec
	deliveriesTotal   *prometheus.CounterVec
}

// New creates a Metrics instance and registers Go runtime and process
// collectors. Version is exposed through the fiscus_build_info gauge.
func New(version string) *Metrics {
	if version == "" {
		version = "dev"
	}
	registry := prometheus.NewRegistry()
	m := &Metrics{
		registry: registry,
		syncTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fiscus_sync_total",
			Help: "Total number of bank synchronization attempts.",
		}, []string{"requisition", "result"}),
		syncDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "fiscus_sync_duration_seconds",
			Help:    "Duration of bank synchronization attempts in seconds.",
			Buckets: []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		}, []string{"requisition"}),
		transactionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fiscus_transactions_ingested_total",
			Help: "Total number of newly inserted transactions.",
		}, []string{"account"}),
		balanceMinor: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "fiscus_account_balance_minor_units",
			Help: "Latest observed account balance in integer minor units.",
		}, []string{"account", "currency", "type"}),
		alertsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fiscus_alerts_total",
			Help: "Total number of persisted alerts by rule and severity.",
		}, []string{"rule", "severity"}),
		alertDispatch: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fiscus_alert_dispatch_total",
			Help: "Total alert dispatch attempts by rule and result.",
		}, []string{"rule", "result"}),
		deliveriesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fiscus_alert_deliveries_total",
			Help: "Total notification deliveries by channel and result.",
		}, []string{"channel", "result"}),
	}
	registry.MustRegister(
		m.syncTotal,
		m.syncDuration,
		m.transactionsTotal,
		m.balanceMinor,
		m.alertsTotal,
		m.alertDispatch,
		m.deliveriesTotal,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fiscus_build_info",
		Help: "Build information for the running Fiscus binary.",
	}, []string{"version"})
	buildInfo.WithLabelValues(version).Set(1)
	registry.MustRegister(buildInfo)
	return m
}

// Handler returns the Prometheus exposition handler for this registry.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// DeliveryObserver returns an adapter for the alert dispatcher's per-channel
// observer interface.
func (m *Metrics) DeliveryObserver() *DeliveryObserver { return &DeliveryObserver{metrics: m} }

// ObserveSync records a synchronization attempt.
func (m *Metrics) ObserveSync(requisitionID string, duration time.Duration, err error) {
	result := "success"
	if err != nil {
		result = "error"
	}
	m.syncTotal.WithLabelValues(requisitionID, result).Inc()
	m.syncDuration.WithLabelValues(requisitionID).Observe(duration.Seconds())
}

// ObserveTransactions records newly inserted transactions for an account.
func (m *Metrics) ObserveTransactions(accountID string, inserted int) {
	if inserted <= 0 {
		return
	}
	m.transactionsTotal.WithLabelValues(accountID).Add(float64(inserted))
}

// ObserveBalance records the latest balance for an account.
func (m *Metrics) ObserveBalance(account domain.Account) {
	balanceType := account.BalanceType
	if balanceType == "" {
		balanceType = "unknown"
	}
	m.balanceMinor.WithLabelValues(account.ID, account.Currency, balanceType).Set(float64(account.BalanceMinor))
}

// ObserveAlert records a persisted alert.
func (m *Metrics) ObserveAlert(alert domain.Alert) {
	m.alertsTotal.WithLabelValues(alert.Rule, string(alert.Severity)).Inc()
}

// ObserveDelivery records the aggregate dispatch result for an alert.
func (m *Metrics) ObserveDelivery(alert domain.Alert, err error) {
	result := "success"
	if err != nil {
		result = "error"
	}
	m.alertDispatch.WithLabelValues(alert.Rule, result).Inc()
}

// DeliveryObserver adapts per-channel delivery outcomes to labels.
type DeliveryObserver struct {
	metrics *Metrics
}

// ObserveDelivery records a single channel delivery outcome.
func (d *DeliveryObserver) ObserveDelivery(channel string, success bool) {
	result := "success"
	if !success {
		result = "error"
	}
	d.metrics.deliveriesTotal.WithLabelValues(channel, result).Inc()
}
