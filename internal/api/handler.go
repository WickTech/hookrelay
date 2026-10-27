package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
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
	targets map[string]*config.Target
	store   *store.Store
	notify  chan<- struct{} // wakes the delivery worker
	log     *slog.Logger
}

func New(targets map[string]*config.Target, s *store.Store, notify chan<- struct{}, log *slog.Logger) *Handler {
	return &Handler{targets: targets, store: s, notify: notify, log: log}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /hooks/{targetID}", h.ingest)
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /dlq", h.listDLQ)
	mux.HandleFunc("POST /dlq/{relayID}/replay", h.replayDLQ)
	mux.HandleFunc("DELETE /dlq/{relayID}", h.deleteDLQ)
	mux.HandleFunc("GET /events", h.listEvents)
	mux.HandleFunc("GET /events/{relayID}", h.getEvent)
	mux.Handle("GET /metrics", promhttp.Handler())
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
