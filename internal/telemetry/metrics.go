package telemetry

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"distserve/internal/cache"
	"distserve/internal/registry"
)

type Metrics struct {
	Requests, Inflight, GeneratedTokens, Errors, Retries                     atomic.Int64
	AdmissionRejections, SchedulerDecisions, SchedulerFailures, Reservations atomic.Int64
	CachePredictedTokens, CacheActualTokens, CachePredictionMisses           atomic.Int64
	ShadowAffinityMatches                                                    atomic.Int64
	TokenizerFallbacks                                                       atomic.Int64
	mu                                                                       sync.Mutex
	requestDurationSum, ttftSum                                              float64
	requestDurationCount, ttftCount                                          int64
	selected                                                                 map[string]int64
}

func (m *Metrics) RecordSelection(workerID string) {
	m.mu.Lock()
	if m.selected == nil {
		m.selected = map[string]int64{}
	}
	m.selected[workerID]++
	m.mu.Unlock()
}

func (m *Metrics) ObserveRequest(seconds float64) {
	m.mu.Lock()
	m.requestDurationSum += seconds
	m.requestDurationCount++
	m.mu.Unlock()
}
func (m *Metrics) ObserveTTFT(seconds float64) {
	m.mu.Lock()
	m.ttftSum += seconds
	m.ttftCount++
	m.mu.Unlock()
}

func (m *Metrics) Handler(snapshots func() []registry.WorkerSnapshot, cacheStats func() cache.CacheStats, affinityStats func() cache.AffinityStats) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		m.mu.Lock()
		durationSum, durationCount, ttftSum, ttftCount := m.requestDurationSum, m.requestDurationCount, m.ttftSum, m.ttftCount
		selected := make(map[string]int64, len(m.selected))
		for id, count := range m.selected {
			selected[id] = count
		}
		m.mu.Unlock()
		values := map[string]float64{
			"distserve_requests_total": float64(m.Requests.Load()), "distserve_requests_inflight": float64(m.Inflight.Load()),
			"distserve_request_duration_seconds_sum": durationSum, "distserve_request_duration_seconds_count": float64(durationCount),
			"distserve_time_to_first_token_seconds_sum": ttftSum, "distserve_time_to_first_token_seconds_count": float64(ttftCount),
			"distserve_time_per_output_token_seconds_sum": 0, "distserve_time_per_output_token_seconds_count": 0,
			"distserve_generated_tokens_total": float64(m.GeneratedTokens.Load()), "distserve_request_errors_total": float64(m.Errors.Load()),
			"distserve_request_retries_total": float64(m.Retries.Load()), "distserve_admission_rejections_total": float64(m.AdmissionRejections.Load()),
			"distserve_scheduler_decisions_total": float64(m.SchedulerDecisions.Load()), "distserve_scheduler_failures_total": float64(m.SchedulerFailures.Load()),
			"distserve_scheduler_decision_duration_seconds_sum": 0, "distserve_scheduler_decision_duration_seconds_count": float64(m.SchedulerDecisions.Load()),
			"distserve_worker_reservations":              float64(m.Reservations.Load()),
			"distserve_cache_predicted_hit_tokens_total": float64(m.CachePredictedTokens.Load()),
			"distserve_cache_actual_hit_tokens_total":    float64(m.CacheActualTokens.Load()),
			"distserve_cache_prediction_misses_total":    float64(m.CachePredictionMisses.Load()),
			"distserve_shadow_affinity_matches_total":    float64(m.ShadowAffinityMatches.Load()),
			"distserve_tokenizer_fallbacks_total":        float64(m.TokenizerFallbacks.Load()),
		}
		names := make([]string, 0, len(values))
		for name := range values {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(w, "# TYPE %s gauge\n%s %g\n", name, name, values[name])
		}
		for id, count := range selected {
			fmt.Fprintf(w, "distserve_worker_selected_total{worker_id=%q} %d\n", id, count)
		}
		if cacheStats != nil {
			stats := cacheStats()
			fmt.Fprintf(w, "distserve_cache_entries %d\n", stats.Entries)
			fmt.Fprintf(w, "distserve_cache_events_total %d\n", stats.AppliedEvents)
			fmt.Fprintf(w, "distserve_cache_event_errors_total %d\n", stats.RejectedEvents)
			fmt.Fprintf(w, "distserve_cache_event_duplicates_total %d\n", stats.DuplicateEvents)
			fmt.Fprintf(w, "distserve_cache_event_out_of_order_total %d\n", stats.OutOfOrderEvents)
			fmt.Fprintf(w, "distserve_cache_event_sequence_gaps_total %d\n", stats.SequenceGaps)
			fmt.Fprintf(w, "distserve_cache_entries_expired_total %d\n", stats.ExpiredEntries)
			fmt.Fprintf(w, "distserve_cache_resets_total %d\n", stats.Resets)
		}
		if affinityStats != nil {
			stats := affinityStats()
			fmt.Fprintf(w, "distserve_shadow_affinity_entries %d\n", stats.Entries)
			fmt.Fprintf(w, "distserve_shadow_affinity_hits_total %d\n", stats.Hits)
			fmt.Fprintf(w, "distserve_shadow_affinity_misses_total %d\n", stats.Misses)
			fmt.Fprintf(w, "distserve_shadow_affinity_expired_total %d\n", stats.Expired)
			fmt.Fprintf(w, "distserve_shadow_affinity_evicted_total %d\n", stats.Evicted)
			fmt.Fprintf(w, "distserve_shadow_affinity_cleared_on_instance_change_total %d\n", stats.ClearedOnInstanceChange)
		}
		states := map[registry.WorkerStatus]int{}
		if snapshots != nil {
			for _, worker := range snapshots() {
				states[worker.Status]++
				age := time.Since(worker.LastHeartbeat).Seconds()
				if age < 0 {
					age = 0
				}
				fmt.Fprintf(w, "distserve_worker_heartbeat_age_seconds{worker_id=%q} %g\n", worker.ID, age)
				fmt.Fprintf(w, "distserve_worker_reported_running{worker_id=%q} %d\n", worker.ID, worker.ReportedRunning)
				fmt.Fprintf(w, "distserve_worker_reported_queued{worker_id=%q} %d\n", worker.ID, worker.ReportedQueued)
				if worker.Load.RunningRequests.Valid {
					fmt.Fprintf(w, "distserve_worker_vllm_running_requests{worker_id=%q} %d\n", worker.ID, worker.Load.RunningRequests.Value)
				}
				if worker.Load.WaitingRequests.Valid {
					fmt.Fprintf(w, "distserve_worker_vllm_waiting_requests{worker_id=%q} %d\n", worker.ID, worker.Load.WaitingRequests.Value)
				}
				if worker.Load.GPUKVCacheUsageRatio.Valid {
					fmt.Fprintf(w, "distserve_worker_vllm_gpu_kv_cache_usage_ratio{worker_id=%q} %g\n", worker.ID, worker.Load.GPUKVCacheUsageRatio.Value)
				}
				if worker.Load.PrefixCacheHitsTotal.Valid {
					fmt.Fprintf(w, "distserve_worker_vllm_prefix_cache_hits_total{worker_id=%q} %d\n", worker.ID, worker.Load.PrefixCacheHitsTotal.Value)
				}
				if worker.Load.PrefixCacheMissesTotal.Valid {
					fmt.Fprintf(w, "distserve_worker_vllm_prefix_cache_misses_total{worker_id=%q} %d\n", worker.ID, worker.Load.PrefixCacheMissesTotal.Value)
				}
			}
		}
		for _, state := range []registry.WorkerStatus{registry.StatusStarting, registry.StatusHealthy, registry.StatusSuspect, registry.StatusDraining, registry.StatusUnavailable} {
			fmt.Fprintf(w, "distserve_workers_by_state{state=%q} %d\n", state, states[state])
		}
	}
}
