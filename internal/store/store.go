package store

import (
	"encoding/json"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	bucketEvents   = []byte("events")
	bucketDLQ      = []byte("dlq")
	bucketAttempts = []byte("attempts")
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusDelivered Status = "delivered"
	StatusDLQ       Status = "dlq"
)

type Event struct {
	ID         string    `json:"id"`
	TargetID   string    `json:"target_id"`
	Body       []byte    `json:"body"`
	ReceivedAt time.Time `json:"received_at"`
	Status     Status    `json:"status"`
	Attempts   int       `json:"attempts"`
}

type Attempt struct {
	Seq        int       `json:"seq"`
	TriedAt    time.Time `json:"tried_at"`
	StatusCode int       `json:"status_code,omitempty"`
	Error      string    `json:"error,omitempty"`
	DurationMS int64     `json:"duration_ms"`
}

type Store struct {
	db *bolt.DB
}

func Open(path string) (*Store, error) {
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open bbolt: %w", err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bucketEvents, bucketDLQ, bucketAttempts} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("init buckets: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) SaveEvent(e *Event) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketEvents).Put([]byte(e.ID), data)
	})
}

func (s *Store) GetEvent(id string) (*Event, error) {
	var e Event
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketEvents).Get([]byte(id))
		if v == nil {
			return fmt.Errorf("event not found: %s", id)
		}
		return json.Unmarshal(v, &e)
	})
	return &e, err
}

func (s *Store) UpdateEventStatus(id string, status Status, attempts int) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketEvents)
		v := b.Get([]byte(id))
		if v == nil {
			return fmt.Errorf("event not found: %s", id)
		}
		var e Event
		if err := json.Unmarshal(v, &e); err != nil {
			return err
		}
		e.Status = status
		e.Attempts = attempts
		data, err := json.Marshal(&e)
		if err != nil {
			return err
		}
		return b.Put([]byte(id), data)
	})
}

// MoveToDLQ copies event to dlq bucket and removes from events.
func (s *Store) MoveToDLQ(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		eb := tx.Bucket(bucketEvents)
		v := eb.Get([]byte(id))
		if v == nil {
			return fmt.Errorf("event not found: %s", id)
		}
		var e Event
		if err := json.Unmarshal(v, &e); err != nil {
			return err
		}
		e.Status = StatusDLQ
		data, err := json.Marshal(&e)
		if err != nil {
			return err
		}
		if err := tx.Bucket(bucketDLQ).Put([]byte(id), data); err != nil {
			return err
		}
		return eb.Delete([]byte(id))
	})
}

// ReplayFromDLQ moves event back to events bucket as pending.
func (s *Store) ReplayFromDLQ(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		db := tx.Bucket(bucketDLQ)
		v := db.Get([]byte(id))
		if v == nil {
			return fmt.Errorf("dlq event not found: %s", id)
		}
		var e Event
		if err := json.Unmarshal(v, &e); err != nil {
			return err
		}
		e.Status = StatusPending
		e.Attempts = 0
		data, err := json.Marshal(&e)
		if err != nil {
			return err
		}
		if err := tx.Bucket(bucketEvents).Put([]byte(id), data); err != nil {
			return err
		}
		return db.Delete([]byte(id))
	})
}

func (s *Store) DeleteFromDLQ(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketDLQ).Delete([]byte(id))
	})
}

// ListPendingEvents returns all events with status=pending.
func (s *Store) ListPendingEvents() ([]*Event, error) {
	var events []*Event
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketEvents).ForEach(func(_, v []byte) error {
			var e Event
			if err := json.Unmarshal(v, &e); err != nil {
				return err
			}
			if e.Status == StatusPending {
				events = append(events, &e)
			}
			return nil
		})
	})
	return events, err
}

// ListDLQ returns all DLQ events, up to limit (0 = all), after cursor.
func (s *Store) ListDLQ(limit int, cursor string) ([]*Event, string, error) {
	var events []*Event
	var nextCursor string
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketDLQ).Cursor()
		var k, v []byte
		if cursor != "" {
			k, v = c.Seek([]byte(cursor))
			if string(k) == cursor {
				k, v = c.Next() // skip the cursor key itself
			}
		} else {
			k, v = c.First()
		}
		for ; k != nil; k, v = c.Next() {
			if limit > 0 && len(events) >= limit {
				nextCursor = string(k)
				break
			}
			var e Event
			if err := json.Unmarshal(v, &e); err != nil {
				return err
			}
			events = append(events, &e)
		}
		return nil
	})
	return events, nextCursor, err
}

func (s *Store) SaveAttempt(eventID string, a *Attempt) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		key := fmt.Sprintf("%s/%04d", eventID, a.Seq)
		data, err := json.Marshal(a)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketAttempts).Put([]byte(key), data)
	})
}

func (s *Store) ListAttempts(eventID string) ([]*Attempt, error) {
	prefix := []byte(eventID + "/")
	var attempts []*Attempt
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketAttempts).Cursor()
		for k, v := c.Seek(prefix); k != nil && len(k) > len(prefix) && string(k[:len(prefix)]) == string(prefix); k, v = c.Next() {
			var a Attempt
			if err := json.Unmarshal(v, &a); err != nil {
				return err
			}
			attempts = append(attempts, &a)
		}
		return nil
	})
	return attempts, err
}

// ListEvents returns events with optional target and status filters, paginated.
func (s *Store) ListEvents(targetID, status string, limit int, cursor string) ([]*Event, string, error) {
	var events []*Event
	var nextCursor string
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketEvents).Cursor()
		var k, v []byte
		if cursor != "" {
			k, v = c.Seek([]byte(cursor))
			if string(k) == cursor {
				k, v = c.Next()
			}
		} else {
			k, v = c.First()
		}
		for ; k != nil; k, v = c.Next() {
			if limit > 0 && len(events) >= limit {
				nextCursor = string(k)
				break
			}
			var e Event
			if err := json.Unmarshal(v, &e); err != nil {
				return err
			}
			if targetID != "" && e.TargetID != targetID {
				continue
			}
			if status != "" && string(e.Status) != status {
				continue
			}
			events = append(events, &e)
		}
		return nil
	})
	return events, nextCursor, err
}

// DLQSize returns count of events in the DLQ bucket.
func (s *Store) DLQSize() (int, error) {
	var n int
	err := s.db.View(func(tx *bolt.Tx) error {
		n = tx.Bucket(bucketDLQ).Stats().KeyN
		return nil
	})
	return n, err
}
