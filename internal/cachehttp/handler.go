package cachehttp

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"distserve/internal/cache"
	"distserve/internal/registry"
)

type Handler struct {
	Index    *cache.CacheIndex
	Registry *registry.Registry
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/workers/{id}/cache/events", h.event)
	mux.HandleFunc("GET /internal/cache/stats", h.stats)
	mux.HandleFunc("GET /internal/cache/workers", h.workers)
	mux.HandleFunc("GET /internal/cache/workers/{id}", h.worker)
	mux.HandleFunc("GET /internal/cache/prefixes/{hash}", h.prefix)
	return mux
}

func (h *Handler) event(w http.ResponseWriter, r *http.Request) {
	var event cache.CacheEvent
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&event) != nil {
		write(w, 400, map[string]string{"error": "invalid JSON"})
		return
	}
	id := r.PathValue("id")
	if event.WorkerID != id {
		write(w, 400, map[string]string{"error": "worker ID mismatch"})
		return
	}
	instance, ok := h.Registry.CurrentInstance(id)
	if !ok {
		write(w, 404, map[string]string{"error": "worker not found"})
		return
	}
	if instance != event.InstanceID {
		write(w, 409, map[string]string{"error": "stale worker instance"})
		return
	}
	if err := h.Index.SetWorkerInstance(id, instance); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	result, err := h.Index.Apply(event)
	if err != nil {
		status := 400
		if errors.Is(err, cache.ErrCacheIndexFull) {
			status = 503
		}
		write(w, status, map[string]string{"error": err.Error()})
		return
	}
	write(w, 200, result)
}
func (h *Handler) stats(w http.ResponseWriter, _ *http.Request) {
	write(w, 200, map[string]any{"snapshot_time": time.Now(), "stale_possible": true, "stats": h.Index.Stats()})
}
func (h *Handler) workers(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{"snapshot_time": time.Now(), "stale_possible": true, "workers": h.Index.WorkerSummaries(limit(r), time.Now())})
}
func (h *Handler) worker(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	instance, ok := h.Registry.CurrentInstance(id)
	if !ok {
		write(w, 404, map[string]string{"error": "worker not found"})
		return
	}
	write(w, 200, map[string]any{"snapshot_time": time.Now(), "stale_possible": true, "entries": h.Index.EntriesForWorker(cache.WorkerInstanceKey{WorkerID: id, InstanceID: instance}, limit(r), time.Now())})
}
func (h *Handler) prefix(w http.ResponseWriter, r *http.Request) {
	decoded, err := hex.DecodeString(r.PathValue("hash"))
	if err != nil || len(decoded) != 32 {
		write(w, 400, map[string]string{"error": "hash must be 64 hex characters"})
		return
	}
	var hash cache.BlockHash
	copy(hash[:], decoded)
	write(w, 200, map[string]any{"snapshot_time": time.Now(), "stale_possible": true, "entries": h.Index.FindPrefixHash(hash, limit(r), time.Now())})
}
func limit(r *http.Request) int {
	value, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if value < 1 {
		value = 100
	}
	if value > 1000 {
		value = 1000
	}
	return value
}
func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
