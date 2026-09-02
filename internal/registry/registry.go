package registry

import (
	"distserve/internal/api"
	"errors"
	"sort"
	"sync"
	"time"
)

type WorkerStatus string

const (
	StatusStarting    WorkerStatus = "starting"
	StatusHealthy     WorkerStatus = "healthy"
	StatusSuspect     WorkerStatus = "suspect"
	StatusDraining    WorkerStatus = "draining"
	StatusUnavailable WorkerStatus = "unavailable"
)

var (
	ErrWorkerNotFound    = errors.New("worker not found")
	ErrStaleInstance     = errors.New("stale worker instance")
	ErrInvalidWorker     = errors.New("invalid worker")
	ErrWorkerUnavailable = errors.New("worker unavailable")
)

type Worker struct {
	ID                       string
	InstanceID               string
	Address                  string
	Models                   []string
	BackendType              string
	Model                    string
	GPUIndex                 *int
	Labels                   map[string]string
	Status                   WorkerStatus
	Capacity                 int
	ReportedRunning          int
	ReportedQueued           int
	EstimatedRemainingTokens int64
	Load                     api.WorkerLoadSnapshot
	LastHeartbeat            time.Time
	Version                  uint64
	localReservations        int
}

type WorkerSnapshot struct {
	ID                       string                 `json:"id"`
	InstanceID               string                 `json:"instance_id"`
	Address                  string                 `json:"address"`
	Models                   []string               `json:"models"`
	BackendType              string                 `json:"backend_type"`
	Model                    string                 `json:"model,omitempty"`
	GPUIndex                 *int                   `json:"gpu_index,omitempty"`
	Labels                   map[string]string      `json:"labels,omitempty"`
	Status                   WorkerStatus           `json:"status"`
	Capacity                 int                    `json:"capacity"`
	ReportedRunning          int                    `json:"reported_running"`
	ReportedQueued           int                    `json:"reported_queued"`
	LocalReservations        int                    `json:"local_reservations"`
	EstimatedRemainingTokens int64                  `json:"estimated_remaining_tokens"`
	Load                     api.WorkerLoadSnapshot `json:"load,omitempty"`
	LastHeartbeat            time.Time              `json:"last_heartbeat"`
	Version                  uint64                 `json:"version"`
}

type Registry struct {
	mu                   sync.RWMutex
	workers              map[string]*Worker
	suspectThreshold     time.Duration
	unavailableThreshold time.Duration
	now                  func() time.Time
}

func New(suspectThreshold, unavailableThreshold time.Duration) *Registry {
	return &Registry{workers: make(map[string]*Worker), suspectThreshold: suspectThreshold, unavailableThreshold: unavailableThreshold, now: time.Now}
}

func (r *Registry) Register(worker Worker) error {
	if worker.ID == "" || worker.InstanceID == "" || worker.Address == "" || len(worker.Models) == 0 || worker.Capacity < 1 {
		return ErrInvalidWorker
	}
	if worker.BackendType == "" {
		worker.BackendType = "mock"
	}
	if worker.Model == "" && len(worker.Models) > 0 {
		worker.Model = worker.Models[0]
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	existing := r.workers[worker.ID]
	if existing != nil && existing.InstanceID == worker.InstanceID {
		// Registration is idempotent, but refreshes discoverable metadata.
		existing.Address = worker.Address
		existing.Models = append(existing.Models[:0], worker.Models...)
		existing.BackendType = worker.BackendType
		existing.Model = worker.Model
		existing.GPUIndex = copyGPUIndex(worker.GPUIndex)
		existing.Labels = copyLabels(worker.Labels)
		existing.Capacity = worker.Capacity
		existing.Version++
		return nil
	}
	worker.Models = append([]string(nil), worker.Models...)
	worker.GPUIndex = copyGPUIndex(worker.GPUIndex)
	worker.Labels = copyLabels(worker.Labels)
	worker.Status = StatusStarting
	worker.LastHeartbeat = r.now()
	if existing != nil {
		worker.Version = existing.Version + 1
	} else {
		worker.Version = 1
	}
	r.workers[worker.ID] = &worker
	return nil
}

func (r *Registry) Heartbeat(id, instanceID string, running, queued int, remaining int64) error {
	return r.HeartbeatWithLoad(id, instanceID, running, queued, remaining, nil)
}

func (r *Registry) HeartbeatWithLoad(id, instanceID string, running, queued int, remaining int64, load *api.WorkerLoadSnapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	worker := r.workers[id]
	if worker == nil {
		return ErrWorkerNotFound
	}
	if worker.InstanceID != instanceID {
		return ErrStaleInstance
	}
	if running < 0 || queued < 0 || remaining < 0 {
		return ErrInvalidWorker
	}
	worker.ReportedRunning = running
	worker.ReportedQueued = queued
	worker.EstimatedRemainingTokens = remaining
	if load != nil {
		worker.Load = *load
	}
	worker.LastHeartbeat = r.now()
	if worker.Status != StatusDraining {
		worker.Status = StatusHealthy
	} else if running == 0 && queued == 0 {
		worker.Status = StatusUnavailable
	}
	worker.Version++
	return nil
}

func (r *Registry) Drain(id, instanceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	worker := r.workers[id]
	if worker == nil {
		return ErrWorkerNotFound
	}
	if worker.InstanceID != instanceID {
		return ErrStaleInstance
	}
	worker.Status = StatusDraining
	if worker.ReportedRunning == 0 && worker.ReportedQueued == 0 {
		worker.Status = StatusUnavailable
	}
	worker.Version++
	return nil
}

func (r *Registry) Sweep() {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, worker := range r.workers {
		if worker.Status == StatusDraining || worker.Status == StatusUnavailable {
			continue
		}
		age := now.Sub(worker.LastHeartbeat)
		switch {
		case age >= r.unavailableThreshold:
			worker.Status = StatusUnavailable
			worker.Version++
		case age >= r.suspectThreshold && worker.Status != StatusSuspect:
			worker.Status = StatusSuspect
			worker.Version++
		}
	}
}

func (r *Registry) Snapshots() []WorkerSnapshot {
	r.mu.RLock()
	result := make([]WorkerSnapshot, 0, len(r.workers))
	for _, worker := range r.workers {
		result = append(result, WorkerSnapshot{ID: worker.ID, InstanceID: worker.InstanceID, Address: worker.Address, Models: append([]string(nil), worker.Models...), BackendType: worker.BackendType, Model: worker.Model, GPUIndex: copyGPUIndex(worker.GPUIndex), Labels: copyLabels(worker.Labels), Status: worker.Status, Capacity: worker.Capacity, ReportedRunning: worker.ReportedRunning, ReportedQueued: worker.ReportedQueued, LocalReservations: worker.localReservations, EstimatedRemainingTokens: worker.EstimatedRemainingTokens, Load: worker.Load, LastHeartbeat: worker.LastHeartbeat, Version: worker.Version})
	}
	r.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func copyGPUIndex(index *int) *int {
	if index == nil {
		return nil
	}
	value := *index
	return &value
}

func copyLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	result := make(map[string]string, len(labels))
	for key, value := range labels {
		result[key] = value
	}
	return result
}

// Reserve atomically validates the selected instance and accounts for one request.
// The returned release function is idempotent, so every exit path may safely defer it.
func (r *Registry) Reserve(id, instanceID string) (func(), error) {
	r.mu.Lock()
	worker := r.workers[id]
	if worker == nil {
		r.mu.Unlock()
		return nil, ErrWorkerNotFound
	}
	if worker.InstanceID != instanceID {
		r.mu.Unlock()
		return nil, ErrStaleInstance
	}
	if worker.Status != StatusHealthy || worker.ReportedRunning+worker.localReservations >= worker.Capacity {
		r.mu.Unlock()
		return nil, ErrWorkerUnavailable
	}
	worker.localReservations++
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			current := r.workers[id]
			if current != nil && current.InstanceID == instanceID && current.localReservations > 0 {
				current.localReservations--
			}
			r.mu.Unlock()
		})
	}, nil
}

func (r *Registry) SetNowForTest(now func() time.Time) { r.now = now }

func (r *Registry) CurrentInstance(id string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	worker := r.workers[id]
	if worker == nil {
		return "", false
	}
	return worker.InstanceID, true
}
