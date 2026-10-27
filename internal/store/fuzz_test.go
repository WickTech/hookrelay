package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/WickTech/hookrelay/internal/store"
)

// FuzzSaveEvent ensures SaveEvent never panics on arbitrary target IDs and bodies.
func FuzzSaveEvent(f *testing.F) {
	f.Add("tgt-1", []byte(`{"amount":1}`))
	f.Add("", []byte(nil))
	f.Add("t", []byte(`not-json`))

	f.Fuzz(func(t *testing.T, targetID string, body []byte) {
		s, err := store.Open(filepath.Join(t.TempDir(), "fuzz.db"))
		if err != nil {
			t.Skip("open store:", err)
		}
		defer s.Close()

		e := &store.Event{
			ID:         "fuzz-id",
			TargetID:   targetID,
			Body:       body,
			ReceivedAt: time.Now().UTC(),
			Status:     store.StatusPending,
		}
		_ = s.SaveEvent(e)
	})
}
