package api_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/WickTech/hookrelay/internal/api"
	"github.com/WickTech/hookrelay/internal/config"
	"github.com/WickTech/hookrelay/internal/hmac"
	"github.com/WickTech/hookrelay/internal/store"
)

func newTestHandler(t *testing.T) (*api.Handler, *store.Store, chan struct{}) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	targets := map[string]*config.Target{
		"tgt": {ID: "tgt", URL: "http://example.com", Secret: "secret123", MaxAttempts: 3, TimeoutMS: 1000},
	}
	notify := make(chan struct{}, 1)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	h := api.New(targets, s, notify, log)
	return h, s, notify
}

func TestIngest_202(t *testing.T) {
	h, _, notify := newTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	body := []byte(`{"amount":100}`)
	sig := hmac.Sign("secret123", body)

	req := httptest.NewRequest(http.MethodPost, "/hooks/tgt", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w.Code)
	}
	if w.Header().Get("X-Relay-ID") == "" {
		t.Error("X-Relay-ID header missing")
	}
	// worker should be notified
	select {
	case <-notify:
	default:
		t.Error("worker not notified")
	}
}

func TestIngest_401_BadSig(t *testing.T) {
	h, _, _ := newTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	body := []byte(`{"amount":100}`)
	req := httptest.NewRequest(http.MethodPost, "/hooks/tgt", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=badsig")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestIngest_404_UnknownTarget(t *testing.T) {
	h, _, _ := newTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/hooks/unknown", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHealth(t *testing.T) {
	h, _, _ := newTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("expected ok, got %s", body["status"])
	}
}

func TestDLQReplayDelete(t *testing.T) {
	h, s, _ := newTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)

	// seed a DLQ event directly
	e := &store.Event{ID: "dlq-1", TargetID: "tgt", Body: []byte(`{}`), Status: store.StatusPending}
	_ = s.SaveEvent(e)
	_ = s.MoveToDLQ("dlq-1")

	// list DLQ
	req := httptest.NewRequest(http.MethodGet, "/dlq", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("list dlq: %d", w.Code)
	}

	// replay
	req = httptest.NewRequest(http.MethodPost, "/dlq/dlq-1/replay", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("replay: expected 202, got %d", w.Code)
	}
}
