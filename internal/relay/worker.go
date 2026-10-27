package relay

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/WickTech/hookrelay/internal/config"
	"github.com/WickTech/hookrelay/internal/store"
)

const (
	baseDelay = time.Second
	maxDelay  = 5 * time.Minute
)

type Worker struct {
	targets  map[string]*config.Target
	store    *store.Store
	notify   <-chan struct{}
	log      *slog.Logger
	after    func(time.Duration, func()) // injectable for tests
	inflight sync.Map                    // eventID → struct{}: prevents duplicate concurrent delivery
}

func NewWorker(targets map[string]*config.Target, s *store.Store, notify <-chan struct{}, log *slog.Logger) *Worker {
	w := &Worker{targets: targets, store: s, notify: notify, log: log}
	w.after = func(d time.Duration, f func()) { time.AfterFunc(d, f) }
	return w
}

// Run processes pending events. Exits when ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	w.processPending(ctx)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.notify:
			w.processPending(ctx)
		case <-ticker.C:
			w.processPending(ctx)
		}
	}
}

func (w *Worker) processPending(ctx context.Context) {
	events, err := w.store.ListPendingEvents()
	if err != nil {
		w.log.Error("list pending events", "err", err)
		return
	}
	for _, e := range events {
		if ctx.Err() != nil {
			return
		}
		// skip if already being retried
		if _, loaded := w.inflight.LoadOrStore(e.ID, struct{}{}); loaded {
			continue
		}
		go func(ev *store.Event) {
			defer w.inflight.Delete(ev.ID)
			w.deliver(ctx, ev)
		}(e)
	}
}

func (w *Worker) deliver(ctx context.Context, e *store.Event) {
	if ctx.Err() != nil {
		return
	}

	target, ok := w.targets[e.TargetID]
	if !ok {
		w.log.Error("unknown target for event", "id", e.ID, "target", e.TargetID)
		return
	}

	attempt := &store.Attempt{
		Seq:     e.Attempts + 1,
		TriedAt: time.Now().UTC(),
	}

	start := time.Now()
	statusCode, deliveryErr := w.post(ctx, target, e.Body)
	attempt.DurationMS = time.Since(start).Milliseconds()
	attempt.StatusCode = statusCode
	if deliveryErr != nil {
		attempt.Error = deliveryErr.Error()
	}
	_ = w.store.SaveAttempt(e.ID, attempt)

	newAttempts := e.Attempts + 1

	if deliveryErr == nil && statusCode >= 200 && statusCode < 300 {
		w.log.Info("delivered", "id", e.ID, "target", e.TargetID, "attempt", newAttempts)
		_ = w.store.UpdateEventStatus(e.ID, store.StatusDelivered, newAttempts)
		return
	}

	w.log.Warn("delivery failed", "id", e.ID, "target", e.TargetID,
		"attempt", newAttempts, "status", statusCode, "err", deliveryErr)

	if newAttempts >= target.MaxAttempts {
		w.log.Warn("max attempts reached, moving to DLQ", "id", e.ID)
		_ = w.store.MoveToDLQ(e.ID)
		return
	}

	_ = w.store.UpdateEventStatus(e.ID, store.StatusPending, newAttempts)

	delay := backoff(newAttempts)
	w.inflight.Store(e.ID, struct{}{}) // keep in-flight during backoff
	w.after(delay, func() {
		defer w.inflight.Delete(e.ID)
		if ctx.Err() != nil {
			return
		}
		ev, err := w.store.GetEvent(e.ID)
		if err != nil {
			return // already delivered or DLQ'd by a parallel path
		}
		w.deliver(ctx, ev)
	})
}

func (w *Worker) post(ctx context.Context, target *config.Target, body []byte) (int, error) {
	timeout := time.Duration(target.TimeoutMS) * time.Millisecond
	client := &http.Client{Timeout: timeout}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// backoff returns exponential delay with jitter, capped at maxDelay.
func backoff(attempt int) time.Duration {
	exp := baseDelay
	for i := 1; i < attempt; i++ {
		exp *= 2
		if exp > maxDelay {
			exp = maxDelay
			break
		}
	}
	jitter := time.Duration(rand.Float64()*0.5*float64(exp) - 0.25*float64(exp))
	d := exp + jitter
	if d < time.Second {
		d = time.Second
	}
	return d
}
