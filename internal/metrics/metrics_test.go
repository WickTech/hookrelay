package metrics_test

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/WickTech/hookrelay/internal/metrics"
)

func TestReceivedCounter(t *testing.T) {
	metrics.ReceivedTotal.WithLabelValues("test-tgt").Add(3)
	got := testutil.ToFloat64(metrics.ReceivedTotal.WithLabelValues("test-tgt"))
	if got < 3 {
		t.Errorf("want >=3, got %f", got)
	}
}

func TestDeliveredCounter(t *testing.T) {
	metrics.DeliveredTotal.WithLabelValues("test-tgt").Inc()
	got := testutil.ToFloat64(metrics.DeliveredTotal.WithLabelValues("test-tgt"))
	if got < 1 {
		t.Errorf("want >=1, got %f", got)
	}
}

func TestFailedCounter(t *testing.T) {
	metrics.FailedTotal.WithLabelValues("test-tgt").Inc()
	got := testutil.ToFloat64(metrics.FailedTotal.WithLabelValues("test-tgt"))
	if got < 1 {
		t.Errorf("want >=1, got %f", got)
	}
}

func TestDLQGauge(t *testing.T) {
	metrics.DLQSize.WithLabelValues("test-tgt2").Set(5)
	got := testutil.ToFloat64(metrics.DLQSize.WithLabelValues("test-tgt2"))
	if got != 5 {
		t.Errorf("want 5, got %f", got)
	}
	metrics.DLQSize.WithLabelValues("test-tgt2").Dec()
	got = testutil.ToFloat64(metrics.DLQSize.WithLabelValues("test-tgt2"))
	if got != 4 {
		t.Errorf("after dec: want 4, got %f", got)
	}
}

func TestDeliveryDuration(t *testing.T) {
	// just verify observe doesn't panic
	metrics.DeliveryDuration.WithLabelValues("test-tgt").Observe(0.042)
}
