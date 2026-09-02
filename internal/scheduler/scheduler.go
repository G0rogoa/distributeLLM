package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"distserve/internal/cache"
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
	Cache           *cache.RequestFeatures
}

type Decision struct {
	WorkerID     string
	InstanceID   string
	Strategy     string
	Score        float64
	Reason       string
	SnapshotAge  time.Duration
	CacheMatch   cache.PrefixMatch
	ScoreDetails ScoreBreakdown
	Candidates   []CandidateScore
}

type ScoreBreakdown struct {
	MatchedTokens int `json:"matched_tokens"`

	CacheBenefit           float64 `json:"cache_benefit"`
	RunningPenalty         float64 `json:"running_penalty"`
	QueuePenalty           float64 `json:"queue_penalty"`
	ReservationPenalty     float64 `json:"reservation_penalty"`
	RemainingTokensPenalty float64 `json:"remaining_tokens_penalty"`
	StalenessPenalty       float64 `json:"staleness_penalty"`
	CapacityPenalty        float64 `json:"capacity_penalty"`
	FillAffinityBonus      float64 `json:"fill_affinity_bonus"`
	FinalScore             float64 `json:"final_score"`
}

type CandidateScore struct {
	WorkerID                 string                `json:"worker_id"`
	InstanceID               string                `json:"instance_id"`
	BackendType              string                `json:"backend_type"`
	Status                   registry.WorkerStatus `json:"status"`
	Capacity                 int                   `json:"capacity"`
	ReportedRunning          int                   `json:"reported_running"`
	ReportedQueued           int                   `json:"reported_queued"`
	LocalReservations        int                   `json:"local_reservations"`
	EstimatedRemainingTokens int64                 `json:"estimated_remaining_tokens"`
	Score                    float64               `json:"score"`
	Reason                   string                `json:"reason"`
	CacheMatch               cache.PrefixMatch     `json:"cache_match"`
	ScoreDetails             ScoreBreakdown        `json:"score_details"`
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
	result := decision(s.Name(), worker, float64(worker.ReportedRunning+worker.LocalReservations), "round-robin eligible worker")
	result.Candidates = candidateSummaries(candidates, func(worker registry.WorkerSnapshot) (float64, string, cache.PrefixMatch, ScoreBreakdown) {
		score := float64(worker.ReportedRunning + worker.LocalReservations)
		return score, "round-robin eligible worker", cache.PrefixMatch{}, ScoreBreakdown{FinalScore: score}
	})
	return result, nil
}

type LeastLoaded struct {
	QueueWeight float64
	TokenWeight float64
	mu          sync.Mutex
	next        uint64
}

type PrefixAware struct {
	CacheWeight, LoadWeight, StalenessWeight, RunningWeight, ReservationWeight, QueueWeight, RemainingTokenWeight, PrefillMSPerToken, DegradedPenalty, FillAffinityBonus float64
	mu                                                                                                                                                                   sync.Mutex
	next                                                                                                                                                                 uint64
}

type ExpectedCompletionTime struct {
	PrefillMSPerToken, DecodeMSPerToken, RunningMS, QueueMS, ReservationMS, RemainingTokenMS, ShadowDiscount, DegradedPenalty float64
	mu                                                                                                                        sync.Mutex
	next                                                                                                                      uint64
}

func (s *PrefixAware) Name() string { return "prefix-aware" }
func (s *PrefixAware) Select(ctx context.Context, request RequestMeta, workers []registry.WorkerSnapshot) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	candidates := eligible(request.Model, workers)
	if len(candidates) == 0 {
		return Decision{}, ErrNoWorker
	}
	bestScore := 0.0
	best := make([]Decision, 0)
	summaries := make([]CandidateScore, 0, len(candidates))
	for _, worker := range candidates {
		key := cache.WorkerInstanceKey{WorkerID: worker.ID, InstanceID: worker.InstanceID}
		match := cache.PrefixMatch{}
		affinity := false
		if request.Cache != nil {
			match = request.Cache.Matches[key]
			affinity = request.Cache.FillAffinity[key]
		}
		details := ScoreBreakdown{MatchedTokens: match.MatchedTokens}
		details.CacheBenefit = float64(match.MatchedTokens) * s.PrefillMSPerToken
		details.RunningPenalty = float64(worker.ReportedRunning) * s.RunningWeight
		details.ReservationPenalty = float64(worker.LocalReservations) * s.ReservationWeight
		details.QueuePenalty = float64(worker.ReportedQueued) * s.QueueWeight
		details.RemainingTokensPenalty = float64(worker.EstimatedRemainingTokens) * s.RemainingTokenWeight
		if match.CacheViewState == cache.CacheViewDegraded {
			details.StalenessPenalty = s.DegradedPenalty
		}
		if affinity {
			details.FillAffinityBonus = s.FillAffinityBonus
		}
		load := details.RunningPenalty + details.ReservationPenalty + details.QueuePenalty + details.RemainingTokensPenalty + details.CapacityPenalty
		details.FinalScore = s.CacheWeight*details.CacheBenefit - s.LoadWeight*load - s.StalenessWeight*details.StalenessPenalty + details.FillAffinityBonus
		reason := fmt.Sprintf("selected %s: matched_tokens=%d, cache_benefit_ms=%.3f, load_penalty=%.3f, staleness_penalty=%.3f, fill_affinity=%.3f, final_score=%.3f", worker.ID, match.MatchedTokens, details.CacheBenefit, load, details.StalenessPenalty, details.FillAffinityBonus, details.FinalScore)
		candidate := decision(s.Name(), worker, details.FinalScore, reason)
		candidate.CacheMatch = match
		candidate.ScoreDetails = details
		summaries = append(summaries, candidateSummary(worker, details.FinalScore, reason, match, details))
		if len(best) == 0 || details.FinalScore > bestScore {
			best = []Decision{candidate}
			bestScore = details.FinalScore
		} else if details.FinalScore == bestScore {
			best = append(best, candidate)
		}
	}
	s.mu.Lock()
	chosen := best[int(s.next%uint64(len(best)))]
	s.next++
	s.mu.Unlock()
	chosen.Candidates = summaries
	return chosen, nil
}

func (s *ExpectedCompletionTime) Name() string { return "ect" }
func (s *ExpectedCompletionTime) Select(ctx context.Context, request RequestMeta, workers []registry.WorkerSnapshot) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	candidates := eligible(request.Model, workers)
	if len(candidates) == 0 {
		return Decision{}, ErrNoWorker
	}
	bestScore := 0.0
	best := make([]Decision, 0)
	summaries := make([]CandidateScore, 0, len(candidates))
	for _, worker := range candidates {
		key := cache.WorkerInstanceKey{WorkerID: worker.ID, InstanceID: worker.InstanceID}
		match := cache.PrefixMatch{}
		if request.Cache != nil {
			match = request.Cache.Matches[key]
		}
		running := worker.ReportedRunning
		if worker.Load.RunningRequests.Valid {
			running = int(worker.Load.RunningRequests.Value)
		}
		queued := worker.ReportedQueued
		if worker.Load.WaitingRequests.Valid {
			queued = int(worker.Load.WaitingRequests.Value)
		}
		inputTokens := request.InputTokens
		if request.Cache != nil && request.Cache.TotalInputTokens > 0 {
			inputTokens = request.Cache.TotalInputTokens
		}
		uncachedTokens := inputTokens - match.MatchedTokens
		if uncachedTokens < 0 {
			uncachedTokens = 0
		}
		details := ScoreBreakdown{MatchedTokens: match.MatchedTokens}
		details.CacheBenefit = float64(match.MatchedTokens) * s.prefillMS()
		details.RunningPenalty = float64(running) * defaultFloat(s.RunningMS, 20)
		details.QueuePenalty = float64(queued) * defaultFloat(s.QueueMS, 40)
		details.ReservationPenalty = float64(worker.LocalReservations) * defaultFloat(s.ReservationMS, 20)
		details.RemainingTokensPenalty = float64(worker.EstimatedRemainingTokens) * defaultFloat(s.RemainingTokenMS, 0.01)
		if match.CacheViewState == cache.CacheViewDegraded || match.CacheViewState == cache.CacheViewStale {
			details.StalenessPenalty = defaultFloat(s.DegradedPenalty, 100)
		}
		if match.Evidence == cache.EvidenceShadowEstimated {
			details.FillAffinityBonus = details.CacheBenefit * defaultFloat(s.ShadowDiscount, 0.5)
		}
		ect := float64(uncachedTokens)*s.prefillMS() + float64(request.MaxOutputTokens)*defaultFloat(s.DecodeMSPerToken, 1) + details.RunningPenalty + details.QueuePenalty + details.ReservationPenalty + details.RemainingTokensPenalty + details.StalenessPenalty - details.FillAffinityBonus
		if ect < 0 {
			ect = 0
		}
		details.FinalScore = -ect
		reason := fmt.Sprintf("selected %s: ect_ms=%.3f, uncached_tokens=%d, matched_tokens=%d, running=%d, queued=%d, reservations=%d", worker.ID, ect, uncachedTokens, match.MatchedTokens, running, queued, worker.LocalReservations)
		candidate := decision(s.Name(), worker, -ect, reason)
		candidate.CacheMatch = match
		candidate.ScoreDetails = details
		summaries = append(summaries, candidateSummary(worker, -ect, reason, match, details))
		if len(best) == 0 || -ect > bestScore {
			best = []Decision{candidate}
			bestScore = -ect
		} else if -ect == bestScore {
			best = append(best, candidate)
		}
	}
	s.mu.Lock()
	chosen := best[int(s.next%uint64(len(best)))]
	s.next++
	s.mu.Unlock()
	chosen.Candidates = summaries
	return chosen, nil
}

func (s *ExpectedCompletionTime) prefillMS() float64 { return defaultFloat(s.PrefillMSPerToken, 0.5) }

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
	summaries := make([]CandidateScore, 0, len(candidates))
	for _, worker := range candidates {
		score := float64(worker.ReportedRunning+worker.LocalReservations) + s.QueueWeight*float64(worker.ReportedQueued) + s.TokenWeight*float64(worker.EstimatedRemainingTokens)/1000
		reason := fmt.Sprintf("candidate %s: effective_load=%.3f, healthy=true, model_match=true", worker.ID, score)
		summaries = append(summaries, candidateSummary(worker, score, reason, cache.PrefixMatch{}, ScoreBreakdown{FinalScore: score}))
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
	result := decision(s.Name(), worker, bestScore, reason)
	result.Candidates = summaries
	return result, nil
}

func defaultFloat(value, fallback float64) float64 {
	if value == 0 {
		return fallback
	}
	return value
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

func candidateSummaries(workers []registry.WorkerSnapshot, score func(registry.WorkerSnapshot) (float64, string, cache.PrefixMatch, ScoreBreakdown)) []CandidateScore {
	summaries := make([]CandidateScore, 0, len(workers))
	for _, worker := range workers {
		value, reason, match, details := score(worker)
		summaries = append(summaries, candidateSummary(worker, value, reason, match, details))
	}
	return summaries
}

func candidateSummary(worker registry.WorkerSnapshot, score float64, reason string, match cache.PrefixMatch, details ScoreBreakdown) CandidateScore {
	return CandidateScore{
		WorkerID:                 worker.ID,
		InstanceID:               worker.InstanceID,
		BackendType:              worker.BackendType,
		Status:                   worker.Status,
		Capacity:                 worker.Capacity,
		ReportedRunning:          worker.ReportedRunning,
		ReportedQueued:           worker.ReportedQueued,
		LocalReservations:        worker.LocalReservations,
		EstimatedRemainingTokens: worker.EstimatedRemainingTokens,
		Score:                    score,
		Reason:                   reason,
		CacheMatch:               match,
		ScoreDetails:             details,
	}
}
