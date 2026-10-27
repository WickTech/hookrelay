package store_test

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/WickTech/hookrelay/internal/store"
)

func BenchmarkSaveEvent(b *testing.B) {
	s, err := store.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()

	body := []byte(`{"amount":100,"event":"credit.deducted"}`)

	b.ResetTimer()
	for i := range b.N {
		e := &store.Event{
			ID:         fmt.Sprintf("evt-%d", i),
			TargetID:   "tgt",
			Body:       body,
			ReceivedAt: time.Now().UTC(),
			Status:     store.StatusPending,
		}
		if err := s.SaveEvent(e); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListPending(b *testing.B) {
	s, err := store.Open(filepath.Join(b.TempDir(), "bench-list.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()

	// seed 100 events
	for i := range 100 {
		_ = s.SaveEvent(&store.Event{
			ID: fmt.Sprintf("e%d", i), TargetID: "tgt",
			Body: []byte(`{}`), ReceivedAt: time.Now().UTC(), Status: store.StatusPending,
		})
	}

	b.ResetTimer()
	for range b.N {
		if _, err := s.ListPendingEvents(); err != nil {
			b.Fatal(err)
		}
	}
}
