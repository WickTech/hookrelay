package store_test

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/WickTech/hookrelay/internal/store"
)

func tmpStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSaveAndGetEvent(t *testing.T) {
	s := tmpStore(t)
	e := &store.Event{
		ID:         "evt-1",
		TargetID:   "tgt-a",
		Body:       []byte(`{"x":1}`),
		ReceivedAt: time.Now().UTC(),
		Status:     store.StatusPending,
	}
	if err := s.SaveEvent(e); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetEvent("evt-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.TargetID != "tgt-a" {
		t.Errorf("target mismatch: %s", got.TargetID)
	}
}

func TestUpdateStatus(t *testing.T) {
	s := tmpStore(t)
	e := &store.Event{ID: "e2", TargetID: "t", Body: []byte(`{}`), ReceivedAt: time.Now().UTC(), Status: store.StatusPending}
	_ = s.SaveEvent(e)
	if err := s.UpdateEventStatus("e2", store.StatusDelivered, 1); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetEvent("e2")
	if got.Status != store.StatusDelivered {
		t.Errorf("expected delivered, got %s", got.Status)
	}
}

func TestMoveToDLQAndReplay(t *testing.T) {
	s := tmpStore(t)
	e := &store.Event{ID: "e3", TargetID: "t", Body: []byte(`{}`), ReceivedAt: time.Now().UTC(), Status: store.StatusPending}
	_ = s.SaveEvent(e)

	if err := s.MoveToDLQ("e3"); err != nil {
		t.Fatal(err)
	}
	// should be gone from events
	if _, err := s.GetEvent("e3"); err == nil {
		t.Error("event should not exist in events after DLQ move")
	}

	dlq, _, _ := s.ListDLQ(0, "")
	if len(dlq) != 1 || dlq[0].ID != "e3" {
		t.Errorf("expected 1 dlq event, got %d", len(dlq))
	}

	if err := s.ReplayFromDLQ("e3"); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.ListPendingEvents()
	if len(pending) != 1 {
		t.Errorf("expected 1 pending after replay, got %d", len(pending))
	}
}

func TestSaveAttempt(t *testing.T) {
	s := tmpStore(t)
	a := &store.Attempt{Seq: 1, TriedAt: time.Now().UTC(), StatusCode: 500, DurationMS: 42}
	if err := s.SaveAttempt("evt-x", a); err != nil {
		t.Fatal(err)
	}
	attempts, err := s.ListAttempts("evt-x")
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].StatusCode != 500 {
		t.Errorf("unexpected attempts: %+v", attempts)
	}
}

func TestListEvents(t *testing.T) {
	s := tmpStore(t)
	for i := range 3 {
		_ = s.SaveEvent(&store.Event{
			ID:         fmt.Sprintf("e%d", i),
			TargetID:   "t",
			Body:       []byte(`{}`),
			ReceivedAt: time.Now().UTC(),
			Status:     store.StatusPending,
		})
	}
	events, _, _ := s.ListEvents("t", "pending", 10, "")
	if len(events) != 3 {
		t.Errorf("expected 3, got %d", len(events))
	}
}
