package relay

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WickTech/hookrelay/internal/config"
	"github.com/WickTech/hookrelay/internal/store"
)

// syncAfter runs f immediately and synchronously, collapsing retry delays to zero.
func syncAfter(_ time.Duration, f func()) { f() }

func newTestWorker(t *testing.T, targets map[string]*config.Target) (*Worker, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	notify := make(chan struct{}, 1)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	w := NewWorker(targets, s, notify, log)
	w.after = syncAfter
	return w, s
}

func seedEvent(t *testing.T, s *store.Store, targetID string) *store.Event {
	t.Helper()
	e := &store.Event{
		ID:         "evt-" + targetID,
		TargetID:   targetID,
		Body:       []byte(`{"test":1}`),
		ReceivedAt: time.Now().UTC(),
		Status:     store.StatusPending,
	}
	if err := s.SaveEvent(e); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	return e
}

func waitStatus(t *testing.T, s *store.Store, id string, want store.Status, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		e, err := s.GetEvent(id)
		if err == nil && e.Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	// check DLQ too
	if want == store.StatusDLQ {
		dlq, _, _ := s.ListDLQ(10, "")
		for _, e := range dlq {
			if e.ID == id {
				return
			}
		}
	}
	t.Fatalf("event %s did not reach status %s within %s", id, want, timeout)
}

// TestDelivery_Success: target returns 200 → event delivered on first attempt.
func TestDelivery_Success(t *testing.T) {
	var received atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	targets := map[string]*config.Target{
		"tgt": {ID: "tgt", URL: srv.URL, MaxAttempts: 3, TimeoutMS: 2000},
	}
	w, s := newTestWorker(t, targets)
	seedEvent(t, s, "tgt")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	w.processPending(ctx)

	waitStatus(t, s, "evt-tgt", store.StatusDelivered, 2*time.Second)

	if received.Load() != 1 {
		t.Errorf("expected 1 delivery attempt, got %d", received.Load())
	}

	attempts, _ := s.ListAttempts("evt-tgt")
	if len(attempts) != 1 {
		t.Errorf("expected 1 attempt record, got %d", len(attempts))
	}
	if attempts[0].StatusCode != 200 {
		t.Errorf("expected status 200, got %d", attempts[0].StatusCode)
	}
}

// TestDelivery_RetryThenSuccess: target fails twice, then succeeds.
func TestDelivery_RetryThenSuccess(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	targets := map[string]*config.Target{
		"tgt": {ID: "tgt", URL: srv.URL, MaxAttempts: 5, TimeoutMS: 2000},
	}
	w, s := newTestWorker(t, targets)
	seedEvent(t, s, "tgt")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.processPending(ctx)

	waitStatus(t, s, "evt-tgt", store.StatusDelivered, 3*time.Second)

	if calls.Load() != 3 {
		t.Errorf("expected 3 calls, got %d", calls.Load())
	}
	attempts, _ := s.ListAttempts("evt-tgt")
	if len(attempts) != 3 {
		t.Errorf("expected 3 attempt records, got %d", len(attempts))
	}
}

// TestDelivery_MaxAttemptsToDLQ: target always fails → DLQ after max_attempts.
func TestDelivery_MaxAttemptsToDLQ(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	targets := map[string]*config.Target{
		"tgt": {ID: "tgt", URL: srv.URL, MaxAttempts: 3, TimeoutMS: 2000},
	}
	w, s := newTestWorker(t, targets)
	seedEvent(t, s, "tgt")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.processPending(ctx)

	waitStatus(t, s, "evt-tgt", store.StatusDLQ, 3*time.Second)

	if calls.Load() != 3 {
		t.Errorf("expected 3 calls (max_attempts), got %d", calls.Load())
	}
	dlq, _, _ := s.ListDLQ(10, "")
	if len(dlq) != 1 || dlq[0].ID != "evt-tgt" {
		t.Errorf("expected 1 DLQ event, got %d", len(dlq))
	}
}

// TestDelivery_NetworkError: target unreachable → treated as failure, retries.
func TestDelivery_NetworkError(t *testing.T) {
	targets := map[string]*config.Target{
		"tgt": {ID: "tgt", URL: "http://127.0.0.1:1", MaxAttempts: 2, TimeoutMS: 500},
	}
	w, s := newTestWorker(t, targets)
	seedEvent(t, s, "tgt")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.processPending(ctx)

	waitStatus(t, s, "evt-tgt", store.StatusDLQ, 4*time.Second)

	attempts, _ := s.ListAttempts("evt-tgt")
	if len(attempts) != 2 {
		t.Errorf("expected 2 attempt records, got %d", len(attempts))
	}
	for _, a := range attempts {
		if a.Error == "" {
			t.Error("expected error string in attempt record")
		}
	}
}

// TestBackoff verifies exponential growth and cap.
func TestBackoff(t *testing.T) {
	cases := []struct {
		attempt int
		minD    time.Duration
		maxD    time.Duration
	}{
		{1, 750 * time.Millisecond, 2 * time.Second},
		{2, 1500 * time.Millisecond, 4 * time.Second},
		{10, 30 * time.Second, maxDelay + maxDelay/4},
	}
	for _, c := range cases {
		d := backoff(c.attempt)
		if d < c.minD || d > c.maxD {
			t.Errorf("attempt %d: backoff %s not in [%s, %s]", c.attempt, d, c.minD, c.maxD)
		}
	}
}
