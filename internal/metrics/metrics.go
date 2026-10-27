package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	ReceivedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hookrelay_received_total",
		Help: "Total webhook events received.",
	}, []string{"target_id"})

	DeliveredTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hookrelay_delivered_total",
		Help: "Total webhook events successfully delivered.",
	}, []string{"target_id"})

	FailedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hookrelay_failed_total",
		Help: "Total delivery attempts that failed.",
	}, []string{"target_id"})

	DLQSize = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hookrelay_dlq_size",
		Help: "Current number of events in the Dead Letter Queue.",
	}, []string{"target_id"})

	DeliveryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hookrelay_delivery_duration_seconds",
		Help:    "Delivery attempt duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"target_id"})
)
