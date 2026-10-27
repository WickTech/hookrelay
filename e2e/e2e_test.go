//go:build e2e

// Package e2e runs hookrelay in-process against a real httptest target.
// Run with: go test -tags e2e -race ./e2e/...
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WickTech/hookrelay/internal/api"
	"github.com/WickTech/hookrelay/internal/config"
	"github.com/WickTech/hookrelay/internal/hmac"
	"github.com/WickTech/hookrelay/internal/relay"
	"github.com/WickTech/hookrelay/internal/store"
)

const (
	secret      = "e2e-shared-secret"
	targetID    = "e2e-target"
	numEvents   = 10
)

// TestE2E_TenDeliveries sends 10 webhook events and verifies all are delivered.
func TestE2E_TenDeliveries(t *testing.T) {
	// 1. Start mock consumer
	var received atomic.Int32
	consumer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer consumer.Close()

	// 2. Start hookrelay in-process
	s, err := store.Open(filepath.Join(t.TempDir(), "e2e.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	targets := map[string]*config.Target{
		targetID: {
			ID:          targetID,
			URL:         consumer.URL,
			Secret:      secret,
			MaxAttempts: 5,
			TimeoutMS:   2000,
		},
	}
	notify := make(chan struct{}, numEvents)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	worker := relay.NewWorker(targets, s, notify, log)
	go worker.Run(ctx)

	mux := http.NewServeMux()
	h := api.New(targets, s, notify, log)
	h.Register(mux)
	relaySrv := httptest.NewServer(mux)
	defer relaySrv.Close()

	// 3. Send 10 webhook events
	for i := range numEvents {
		body, _ := json.Marshal(map[string]any{"seq": i, "event": "credit.deducted"})
		sig := hmac.Sign(secret, body)
		req, _ := http.NewRequest(http.MethodPost,
			fmt.Sprintf("%s/hooks/%s", relaySrv.URL, targetID),
			bytes.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", sig)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("send event %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("event %d: expected 202, got %d", i, resp.StatusCode)
		}
	}

	// 4. Wait for all deliveries
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if received.Load() == numEvents {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if got := received.Load(); got != numEvents {
		t.Errorf("consumer received %d events, want %d", got, numEvents)
	}

	// 5. Verify all events are marked delivered in the store
	events, _, err := s.ListEvents(targetID, "delivered", 0, "")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != numEvents {
		t.Errorf("store has %d delivered events, want %d", len(events), numEvents)
	}

	// 6. DLQ should be empty
	dlq, _, _ := s.ListDLQ(0, "")
	if len(dlq) != 0 {
		t.Errorf("DLQ has %d events, want 0", len(dlq))
	}
}

// TestE2E_DLQOnConsumerDown: consumer always fails → all events land in DLQ.
func TestE2E_DLQOnConsumerDown(t *testing.T) {
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failSrv.Close()

	s, err := store.Open(filepath.Join(t.TempDir(), "e2e-dlq.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	targets := map[string]*config.Target{
		targetID: {
			ID:          targetID,
			URL:         failSrv.URL,
			Secret:      secret,
			MaxAttempts: 2, // fast to DLQ
			TimeoutMS:   500,
		},
	}
	notify := make(chan struct{}, 5)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	worker := relay.NewWorker(targets, s, notify, log)
	// use syncAfter equivalent: zero-delay retries by patching after
	worker.SetAfter(func(_ time.Duration, f func()) { go f() })
	go worker.Run(ctx)

	mux := http.NewServeMux()
	h := api.New(targets, s, notify, log)
	h.Register(mux)
	relaySrv := httptest.NewServer(mux)
	defer relaySrv.Close()

	const n = 3
	for i := range n {
		body, _ := json.Marshal(map[string]int{"seq": i})
		sig := hmac.Sign(secret, body)
		req, _ := http.NewRequest(http.MethodPost,
			fmt.Sprintf("%s/hooks/%s", relaySrv.URL, targetID),
			bytes.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", sig)
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
	}

	// Wait for DLQ to fill
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		dlq, _, _ := s.ListDLQ(0, "")
		if len(dlq) == n {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	dlq, _, _ := s.ListDLQ(0, "")
	if len(dlq) != n {
		t.Errorf("DLQ has %d events, want %d", len(dlq), n)
	}
}
