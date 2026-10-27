package api_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WickTech/hookrelay/internal/hmac"
	"github.com/WickTech/hookrelay/internal/metrics"
)

func TestMetricsEndpoint_AlwaysPresent(t *testing.T) {
	h, _, _ := newTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	// Pre-seed all metrics so they appear in output (Prometheus omits unobserved series).
	metrics.ReceivedTotal.WithLabelValues("__probe__").Add(0)
	metrics.DeliveredTotal.WithLabelValues("__probe__").Add(0)
	metrics.FailedTotal.WithLabelValues("__probe__").Add(0)
	metrics.DLQSize.WithLabelValues("__probe__").Set(0)
	metrics.DeliveryDuration.WithLabelValues("__probe__").Observe(0)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, metric := range []string{
		"hookrelay_received_total",
		"hookrelay_delivered_total",
		"hookrelay_failed_total",
		"hookrelay_dlq_size",
		"hookrelay_delivery_duration_seconds",
	} {
		if !strings.Contains(body, metric) {
			t.Errorf("metric %s missing from /metrics output", metric)
		}
	}
}

func TestIngestIncrementsReceivedCounter(t *testing.T) {
	h, _, _ := newTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	body := []byte(`{"amount":42}`)
	sig := hmac.Sign("secret123", body)

	req := httptest.NewRequest(http.MethodPost, "/hooks/tgt", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)

	if !strings.Contains(w2.Body.String(), "hookrelay_received_total") {
		t.Error("hookrelay_received_total not in /metrics after ingest")
	}
}
