package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/WickTech/hookrelay/internal/config"
	"github.com/WickTech/hookrelay/internal/hmac"
	"github.com/WickTech/hookrelay/internal/metrics"
	"github.com/WickTech/hookrelay/internal/store"
)

const version = "0.1.0"

type Handler struct {
	targets    map[string]*config.Target
	store      *store.Store
	notify     chan<- struct{} // wakes the delivery worker
	log        *slog.Logger
	adminToken string
}

// Option configures a Handler.
type Option func(*Handler)

// WithAdminToken requires "Authorization: Bearer <token>" on the admin API
// (/events, /dlq) and /metrics. An empty token leaves those routes open; the
// caller must ensure that only happens on a loopback listener
// (see config.CheckAdminExposure).
func WithAdminToken(token string) Option {
	return func(h *Handler) { h.adminToken = token }
}

func New(targets map[string]*config.Target, s *store.Store, notify chan<- struct{}, log *slog.Logger, opts ...Option) *Handler {
	h := &Handler{targets: targets, store: s, notify: notify, log: log}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

func (h *Handler) Register(mux *http.ServeMux) {
	// Public: ingest is authenticated per target by HMAC signature.
	mux.HandleFunc("POST /hooks/{targetID}", h.ingest)
	mux.HandleFunc("GET /health", h.health)

	// Admin: bearer token.
	mux.Handle("GET /dlq", h.requireAdmin(http.HandlerFunc(h.listDLQ)))
	mux.Handle("POST /dlq/{relayID}/replay", h.requireAdmin(http.HandlerFunc(h.replayDLQ)))
	mux.Handle("DELETE /dlq/{relayID}", h.requireAdmin(http.HandlerFunc(h.deleteDLQ)))
	mux.Handle("GET /events", h.requireAdmin(http.HandlerFunc(h.listEvents)))
	mux.Handle("GET /events/{relayID}", h.requireAdmin(http.HandlerFunc(h.getEvent)))
	mux.Handle("GET /metrics", h.requireAdmin(promhttp.Handler()))
}

// requireAdmin rejects requests without the configured bearer token.
func (h *Handler) requireAdmin(next http.Handler) http.Handler {
	if h.adminToken == "" {
		return next
	}
	// Compare fixed-length digests so the comparison does not leak the token length.
	want := sha256.Sum256([]byte(h.adminToken))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := bearerToken(r.Header.Get("Authorization"))
		gotSum := sha256.Sum256([]byte(got))
		if !ok || subtle.ConstantTimeCompare(gotSum[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="hookrelay"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header.
func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

func (h *Handler) ingest(w http.ResponseWriter, r *http.Request) {
	targetID := r.PathValue("targetID")
	target, ok := h.targets[targetID]
	if !ok {
		http.Error(w, "unknown target", http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB limit
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	sig := r.Header.Get("X-Hub-Signature-256")
	if err := hmac.Verify(target.Secret, sig, body); err != nil {
		h.log.Warn("signature invalid", "target", targetID, "err", err)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	event := &store.Event{
		ID:         uuid.New().String(),
		TargetID:   targetID,
		Body:       body,
		ReceivedAt: time.Now().UTC(),
		Status:     store.StatusPending,
	}
	if err := h.store.SaveEvent(event); err != nil {
		h.log.Error("save event", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	metrics.ReceivedTotal.WithLabelValues(targetID).Inc()

	// non-blocking wake of delivery worker
	select {
	case h.notify <- struct{}{}:
	default:
	}

	h.log.Info("event received", "id", event.ID, "target", targetID)
	w.Header().Set("X-Relay-ID", event.ID)
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": version})
}

func (h *Handler) listDLQ(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	cursor := r.URL.Query().Get("cursor")
	events, next, err := h.store.ListDLQ(limit, cursor)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"events": events, "next_cursor": next})
}

func (h *Handler) replayDLQ(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("relayID")
	// fetch target before removing from DLQ so we can update gauge
	dlqEvents, _, _ := h.store.ListDLQ(0, "")
	var targetID string
	for _, e := range dlqEvents {
		if e.ID == id {
			targetID = e.TargetID
			break
		}
	}
	if err := h.store.ReplayFromDLQ(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if targetID != "" {
		metrics.DLQSize.WithLabelValues(targetID).Dec()
	}
	select {
	case h.notify <- struct{}{}:
	default:
	}
	h.log.Info("dlq event replayed", "id", id)
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) deleteDLQ(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("relayID")
	if err := h.store.DeleteFromDLQ(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	events, next, err := h.store.ListEvents(q.Get("target"), q.Get("status"), limit, q.Get("cursor"))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"events": events, "next_cursor": next})
}

func (h *Handler) getEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("relayID")
	event, err := h.store.GetEvent(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	attempts, err := h.store.ListAttempts(id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"event": event, "attempts": attempts})
}
