package registry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"distserve/internal/api"
)

func (r *Registry) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/workers/register", r.handleRegister)
	mux.HandleFunc("POST /internal/workers/{id}/heartbeat", r.handleHeartbeat)
	mux.HandleFunc("POST /internal/workers/{id}/drain", r.handleDrain)
	mux.HandleFunc("GET /internal/workers", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, r.Snapshots()) })
	return mux
}

func (r *Registry) RunSweeper(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Sweep()
		}
	}
}

func (r *Registry) handleRegister(w http.ResponseWriter, request *http.Request) {
	var input api.RegisterWorkerRequest
	if json.NewDecoder(request.Body).Decode(&input) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	err := r.Register(Worker{ID: input.ID, InstanceID: input.InstanceID, Address: input.Address, Models: input.Models, Capacity: input.Capacity})
	writeRegistryResult(w, err)
}

func (r *Registry) handleHeartbeat(w http.ResponseWriter, request *http.Request) {
	var input api.HeartbeatRequest
	if json.NewDecoder(request.Body).Decode(&input) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	err := r.Heartbeat(request.PathValue("id"), input.InstanceID, input.ReportedRunning, input.ReportedQueued, input.EstimatedRemainingTokens)
	writeRegistryResult(w, err)
}

func (r *Registry) handleDrain(w http.ResponseWriter, request *http.Request) {
	var input api.DrainWorkerRequest
	if json.NewDecoder(request.Body).Decode(&input) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	writeRegistryResult(w, r.Drain(request.PathValue("id"), input.InstanceID))
}

func writeRegistryResult(w http.ResponseWriter, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	status := http.StatusBadRequest
	if errors.Is(err, ErrWorkerNotFound) {
		status = http.StatusNotFound
	}
	if errors.Is(err, ErrStaleInstance) {
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
