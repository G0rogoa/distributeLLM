package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"distserve/internal/registry"
)

var ErrNoWorker = errors.New("no eligible worker")

type RequestMeta struct {
	RequestID       string
	Model           string
	InputTokens     int
	MaxOutputTokens int
	Streaming       bool
	ArrivalTime     time.Time
	Deadline        time.Time
	TenantID        string
}

type Decision struct {
	WorkerID    string
	InstanceID  string
	Strategy    string
	Score       float64
	Reason      string
	SnapshotAge time.Duration
}

type Scheduler interface {
	Name() string
	Select(context.Context, RequestMeta, []registry.WorkerSnapshot) (Decision, error)
}

type RoundRobin struct{ next atomic.Uint64 }

func (s *RoundRobin) Name() string { return "round-robin" }

func (s *RoundRobin) Select(ctx context.Context, request RequestMeta, workers []registry.WorkerSnapshot) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	candidates := eligible(request.Model, workers)
	if len(candidates) == 0 {
		return Decision{}, ErrNoWorker
	}
	index := int((s.next.Add(1) - 1) % uint64(len(candidates)))
	worker := candidates[index]
	return decision(s.Name(), worker, float64(worker.ReportedRunning+worker.LocalReservations), "round-robin eligible worker"), nil
}

type LeastLoaded struct {
	QueueWeight float64
	TokenWeight float64
	mu          sync.Mutex
	next        uint64
}

func (s *LeastLoaded) Name() string { return "least-loaded" }

func (s *LeastLoaded) Select(ctx context.Context, request RequestMeta, workers []registry.WorkerSnapshot) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	candidates := eligible(request.Model, workers)
	if len(candidates) == 0 {
		return Decision{}, ErrNoWorker
	}
	best := make([]registry.WorkerSnapshot, 0, len(candidates))
	bestScore := 0.0
	for _, worker := range candidates {
		score := float64(worker.ReportedRunning+worker.LocalReservations) + s.QueueWeight*float64(worker.ReportedQueued) + s.TokenWeight*float64(worker.EstimatedRemainingTokens)/1000
		if len(best) == 0 || score < bestScore {
			best, bestScore = []registry.WorkerSnapshot{worker}, score
		} else if score == bestScore {
			best = append(best, worker)
		}
	}
	s.mu.Lock()
	index := int(s.next % uint64(len(best)))
	s.next++
	s.mu.Unlock()
	worker := best[index]
	reason := fmt.Sprintf("selected %s: effective_load=%.3f, healthy=true, model_match=true", worker.ID, bestScore)
	return decision(s.Name(), worker, bestScore, reason), nil
}

func eligible(model string, workers []registry.WorkerSnapshot) []registry.WorkerSnapshot {
	result := make([]registry.WorkerSnapshot, 0, len(workers))
	for _, worker := range workers {
		if worker.Status != registry.StatusHealthy || worker.ReportedRunning+worker.LocalReservations >= worker.Capacity || !supports(worker.Models, model) {
			continue
		}
		result = append(result, worker)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func supports(models []string, model string) bool {
	for _, candidate := range models {
		if candidate == model {
			return true
		}
	}
	return false
}

func decision(strategy string, worker registry.WorkerSnapshot, score float64, reason string) Decision {
	age := time.Since(worker.LastHeartbeat)
	if age < 0 {
		age = 0
	}
	return Decision{WorkerID: worker.ID, InstanceID: worker.InstanceID, Strategy: strategy, Score: score, Reason: reason, SnapshotAge: age}
}
