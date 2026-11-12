package api_test

import (
	"bytes"
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

const adminToken = "test-admin-token"

func newAuthMux(t *testing.T) (*http.ServeMux, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	targets := map[string]*config.Target{
		"tgt": {ID: "tgt", URL: "http://example.com", Secret: "secret123", MaxAttempts: 3, TimeoutMS: 1000},
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	h := api.New(targets, s, make(chan struct{}, 1), log, api.WithAdminToken(adminToken))
	mux := http.NewServeMux()
	h.Register(mux)
	return mux, s
}

// seedDLQ stores an event and moves it to the DLQ.
func seedDLQ(t *testing.T, s *store.Store, id string) {
	t.Helper()
	if err := s.SaveEvent(&store.Event{ID: id, TargetID: "tgt", Body: []byte(`{}`), Status: store.StatusPending}); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveToDLQ(id); err != nil {
		t.Fatal(err)
	}
}

type adminRoute struct {
	method, path string
	okStatus     int
}

func adminRoutes() []adminRoute {
	return []adminRoute{
		{http.MethodGet, "/dlq", http.StatusOK},
		{http.MethodPost, "/dlq/dlq-replay/replay", http.StatusAccepted},
		{http.MethodDelete, "/dlq/dlq-delete", http.StatusNoContent},
		{http.MethodGet, "/events", http.StatusOK},
		{http.MethodGet, "/events/evt-get", http.StatusOK},
		{http.MethodGet, "/metrics", http.StatusOK},
	}
}

func TestAdminRoutes_RequireBearerToken(t *testing.T) {
	headers := map[string]string{
		"missing":      "",
		"wrong token":  "Bearer not-the-token",
		"prefix only":  "Bearer test-admin",
		"empty bearer": "Bearer ",
		"basic scheme": "Basic " + adminToken,
		"no scheme":    adminToken,
	}
	for _, rt := range adminRoutes() {
		for name, auth := range headers {
			t.Run(rt.method+" "+rt.path+"/"+name, func(t *testing.T) {
				mux, _ := newAuthMux(t)
				req := httptest.NewRequest(rt.method, rt.path, nil)
				if auth != "" {
					req.Header.Set("Authorization", auth)
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, req)
				if w.Code != http.StatusUnauthorized {
					t.Fatalf("expected 401, got %d", w.Code)
				}
				if got := w.Header().Get("WWW-Authenticate"); got != `Bearer realm="hookrelay"` {
					t.Errorf("WWW-Authenticate = %q", got)
				}
			})
		}
	}
}

func TestAdminRoutes_AllowCorrectToken(t *testing.T) {
	for _, rt := range adminRoutes() {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			mux, s := newAuthMux(t)
			seedDLQ(t, s, "dlq-replay")
			seedDLQ(t, s, "dlq-delete")
			if err := s.SaveEvent(&store.Event{ID: "evt-get", TargetID: "tgt", Body: []byte(`{}`), Status: store.StatusPending}); err != nil {
				t.Fatal(err)
			}

			req := httptest.NewRequest(rt.method, rt.path, nil)
			req.Header.Set("Authorization", "bearer "+adminToken) // scheme is case-insensitive
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != rt.okStatus {
				t.Fatalf("expected %d, got %d: %s", rt.okStatus, w.Code, w.Body.String())
			}
		})
	}
}

func TestPublicRoutes_NoTokenNeeded(t *testing.T) {
	mux, _ := newAuthMux(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("health: expected 200, got %d", w.Code)
	}

	body := []byte(`{"amount":1}`)
	req = httptest.NewRequest(http.MethodPost, "/hooks/tgt", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", hmac.Sign("secret123", body))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("ingest: expected 202, got %d", w.Code)
	}

	// Ingest still enforces its own HMAC check; the admin token is not a substitute.
	req = httptest.NewRequest(http.MethodPost, "/hooks/tgt", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("ingest without signature: expected 401, got %d", w.Code)
	}
}
