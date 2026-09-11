package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"distserve/internal/cache"
	"distserve/internal/costmodel"
	"distserve/internal/registry"
)

func worker(id string, load, reservations int) registry.WorkerSnapshot {
	return registry.WorkerSnapshot{ID: id, InstanceID: id + "-instance", Models: []string{"mock-llm"}, Status: registry.StatusHealthy, Capacity: 8, ReportedRunning: load, LocalReservations: reservations, LastHeartbeat: time.Now()}
}

func TestPrefixAwareTradesCacheBenefitAgainstLoad(t *testing.T) {
	s := &PrefixAware{CacheWeight: 1, LoadWeight: 1, RunningWeight: 20, PrefillMSPerToken: 1}
	a, b := worker("a", 2, 0), worker("b", 0, 0)
	features := &cache.RequestFeatures{Matches: map[cache.WorkerInstanceKey]cache.PrefixMatch{
		{WorkerID: a.ID, InstanceID: a.InstanceID}: {MatchedTokens: 100, MatchedBlocks: 5, CacheViewState: cache.CacheViewReady},
	}}
	got, err := s.Select(context.Background(), RequestMeta{Model: "mock-llm", Cache: features}, []registry.WorkerSnapshot{a, b})
	if err != nil || got.WorkerID != "a" || got.ScoreDetails.CacheBenefit != 100 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	a.ReportedRunning = 7
	got, err = s.Select(context.Background(), RequestMeta{Model: "mock-llm", Cache: features}, []registry.WorkerSnapshot{a, b})
	if err != nil || got.WorkerID != "b" {
		t.Fatalf("load should win: got=%+v err=%v", got, err)
	}
}

func TestPrefixAwarePenalizesDegradedAndHonorsFillAffinity(t *testing.T) {
	s := &PrefixAware{CacheWeight: 1, StalenessWeight: 1, PrefillMSPerToken: 1, DegradedPenalty: 100, FillAffinityBonus: 10}
	a, b := worker("a", 0, 0), worker("b", 0, 0)
	features := &cache.RequestFeatures{Matches: map[cache.WorkerInstanceKey]cache.PrefixMatch{
		{WorkerID: a.ID, InstanceID: a.InstanceID}: {MatchedTokens: 50, CacheViewState: cache.CacheViewDegraded},
	}, FillAffinity: map[cache.WorkerInstanceKey]bool{{WorkerID: b.ID, InstanceID: b.InstanceID}: true}}
	got, err := s.Select(context.Background(), RequestMeta{Model: "mock-llm", Cache: features}, []registry.WorkerSnapshot{a, b})
	if err != nil || got.WorkerID != "b" || got.ScoreDetails.FillAffinityBonus != 10 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestRoundRobinAndChangingList(t *testing.T) {
	s := &RoundRobin{}
	request := RequestMeta{Model: "mock-llm"}
	workers := []registry.WorkerSnapshot{worker("b", 0, 0), worker("a", 0, 0)}
	for _, want := range []string{"a", "b", "a"} {
		got, err := s.Select(context.Background(), request, workers)
		if err != nil || got.WorkerID != want {
			t.Fatalf("got=%+v err=%v want=%s", got, err, want)
		}
	}
	if _, err := s.Select(context.Background(), request, workers[:1]); err != nil {
		t.Fatal(err)
	}
}

func TestLeastLoadedAndTieBreak(t *testing.T) {
	s := &LeastLoaded{QueueWeight: .5, TokenWeight: .1}
	request := RequestMeta{Model: "mock-llm"}
	workers := []registry.WorkerSnapshot{worker("a", 3, 0), worker("b", 0, 1)}
	got, err := s.Select(context.Background(), request, workers)
	if err != nil || got.WorkerID != "b" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	ties := []registry.WorkerSnapshot{worker("a", 0, 0), worker("b", 0, 0)}
	first, _ := s.Select(context.Background(), request, ties)
	second, _ := s.Select(context.Background(), request, ties)
	if first.WorkerID == second.WorkerID {
		t.Fatal("tie-break permanently selected one worker")
	}
}

func TestExpectedCompletionTimeUsesCacheAndLoad(t *testing.T) {
	s := &ExpectedCompletionTime{PrefillMSPerToken: 1, DecodeMSPerToken: 1, RunningMS: 20}
	a, b := worker("a", 1, 0), worker("b", 0, 0)
	features := &cache.RequestFeatures{TotalInputTokens: 120, Matches: map[cache.WorkerInstanceKey]cache.PrefixMatch{
		{WorkerID: a.ID, InstanceID: a.InstanceID}: {MatchedTokens: 100, MatchedBlocks: 5, Evidence: cache.EvidenceShadowEstimated},
	}}
	got, err := s.Select(context.Background(), RequestMeta{Model: "mock-llm", InputTokens: 120, MaxOutputTokens: 10, Cache: features}, []registry.WorkerSnapshot{a, b})
	if err != nil || got.WorkerID != "a" || got.ScoreDetails.MatchedTokens != 100 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	a.ReportedRunning = 8
	got, err = s.Select(context.Background(), RequestMeta{Model: "mock-llm", InputTokens: 120, MaxOutputTokens: 10, Cache: features}, []registry.WorkerSnapshot{a, b})
	if err != nil || got.WorkerID != "b" {
		t.Fatalf("load should win: got=%+v err=%v", got, err)
	}
}

func TestExpectedCompletionTimeUsesVLLMOptionalLoadWhenPresent(t *testing.T) {
	s := &ExpectedCompletionTime{PrefillMSPerToken: 1, DecodeMSPerToken: 1, QueueMS: 100}
	a, b := worker("a", 0, 0), worker("b", 0, 0)
	a.Load.WaitingRequests.Valid = true
	a.Load.WaitingRequests.Value = 2
	got, err := s.Select(context.Background(), RequestMeta{Model: "mock-llm", InputTokens: 10, MaxOutputTokens: 1}, []registry.WorkerSnapshot{a, b})
	if err != nil || got.WorkerID != "b" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("candidates=%+v", got.Candidates)
	}
}

func TestExpectedCompletionTimeAppliesShadowConfidenceOnce(t *testing.T) {
	s := &ExpectedCompletionTime{Profile: costmodel.Profile{Version: 1, Source: costmodel.SourceCalibrated, PrefillMSPerToken: 1, DecodeMSPerToken: 1, ShadowConfidence: 0.5, ValidTokenRange: [2]int{1, 1000}}}
	a, b := worker("a", 0, 0), worker("b", 0, 0)
	features := &cache.RequestFeatures{TotalInputTokens: 100, Matches: map[cache.WorkerInstanceKey]cache.PrefixMatch{
		{WorkerID: a.ID, InstanceID: a.InstanceID}: {MatchedTokens: 80, MatchedBlocks: 5, Evidence: cache.EvidenceShadowEstimated},
	}}
	got, err := s.Select(context.Background(), RequestMeta{Model: "mock-llm", InputTokens: 100, MaxOutputTokens: 10, Cache: features}, []registry.WorkerSnapshot{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkerID != "a" || got.ScoreDetails.AdjustedCachedTokens != 40 || got.ScoreDetails.UncachedTokens != 60 {
		t.Fatalf("got=%+v", got)
	}
	if got.ScoreDetails.FillAffinityBonus != 0 {
		t.Fatalf("shadow should not be double counted: %+v", got.ScoreDetails)
	}
}

func TestExpectedCompletionTimeClampsUncachedTokens(t *testing.T) {
	s := &ExpectedCompletionTime{Profile: costmodel.Profile{Version: 1, Source: costmodel.SourceCalibrated, PrefillMSPerToken: 1, DecodeMSPerToken: 1, ShadowConfidence: 1, ValidTokenRange: [2]int{1, 1000}}}
	a := worker("a", 0, 0)
	features := &cache.RequestFeatures{TotalInputTokens: 10, Matches: map[cache.WorkerInstanceKey]cache.PrefixMatch{
		{WorkerID: a.ID, InstanceID: a.InstanceID}: {MatchedTokens: 80, MatchedBlocks: 5, Evidence: cache.EvidenceMockExact},
	}}
	got, err := s.Select(context.Background(), RequestMeta{Model: "mock-llm", InputTokens: 10, MaxOutputTokens: 5, Cache: features}, []registry.WorkerSnapshot{a})
	if err != nil {
		t.Fatal(err)
	}
	if got.ScoreDetails.UncachedTokens != 0 {
		t.Fatalf("uncached tokens=%v", got.ScoreDetails.UncachedTokens)
	}
}

func TestExpectedCompletionTimeSwitchesWhenQueueExceedsCacheSaving(t *testing.T) {
	profile := costmodel.Profile{Version: 1, Source: costmodel.SourceCalibrated, PrefillMSPerToken: 1, WaitingRequestMS: 39, ShadowConfidence: 0.5, ValidTokenRange: [2]int{1, 1000}}
	s := &ExpectedCompletionTime{Profile: profile}
	a, b := worker("cached", 0, 0), worker("idle", 0, 0)
	a.Load.WaitingRequests.Valid = true
	a.Load.WaitingRequests.Value = 1
	features := &cache.RequestFeatures{TotalInputTokens: 100, Matches: map[cache.WorkerInstanceKey]cache.PrefixMatch{
		{WorkerID: a.ID, InstanceID: a.InstanceID}: {MatchedTokens: 80, MatchedBlocks: 5, Evidence: cache.EvidenceShadowEstimated},
	}}
	request := RequestMeta{Model: "mock-llm", InputTokens: 100, Cache: features}
	got, err := s.Select(context.Background(), request, []registry.WorkerSnapshot{a, b})
	if err != nil || got.WorkerID != "cached" {
		t.Fatalf("cache saving should win below boundary: got=%+v err=%v", got, err)
	}
	s.Profile.WaitingRequestMS = 41
	got, err = s.Select(context.Background(), request, []registry.WorkerSnapshot{a, b})
	if err != nil || got.WorkerID != "idle" {
		t.Fatalf("queue should win above boundary: got=%+v err=%v", got, err)
	}
}

func TestExpectedCompletionTimeRecordsOutputAndLoadSources(t *testing.T) {
	s := &ExpectedCompletionTime{Profile: costmodel.Profile{Version: 1, Source: costmodel.SourceCalibrated, DecodeMSPerToken: 1, ShadowConfidence: 0.5, ValidTokenRange: [2]int{1, 1000}}}
	a := worker("a", 0, 0)
	got, err := s.Select(context.Background(), RequestMeta{Model: "mock-llm", InputTokens: 1, MaxOutputTokens: 16, ExpectedOutputTokens: 8, ExpectedOutputSource: "workload"}, []registry.WorkerSnapshot{a})
	if err != nil {
		t.Fatal(err)
	}
	if got.ScoreDetails.ExpectedOutputTokens != 8 || got.ScoreDetails.ExpectedOutputSource != "workload" {
		t.Fatalf("details=%+v", got.ScoreDetails)
	}
	if got.ScoreDetails.RunningSource != "registry" || got.ScoreDetails.WaitingSource != "registry" {
		t.Fatalf("load sources=%+v", got.ScoreDetails)
	}
	a.Load.RunningRequests.Valid = true
	a.Load.RunningRequests.Value = 3
	a.Load.WaitingRequests.Valid = true
	a.Load.WaitingRequests.Value = 2
	got, err = s.Select(context.Background(), RequestMeta{Model: "mock-llm", InputTokens: 1, MaxOutputTokens: 16}, []registry.WorkerSnapshot{a})
	if err != nil {
		t.Fatal(err)
	}
	if got.ScoreDetails.RunningSource != "vllm_metrics" || got.ScoreDetails.WaitingSource != "vllm_metrics" || got.ScoreDetails.ExpectedOutputSource != "max_tokens" {
		t.Fatalf("details=%+v", got.ScoreDetails)
	}
}

func TestNoEligibleWorker(t *testing.T) {
	s := &RoundRobin{}
	unavailable := worker("a", 0, 0)
	unavailable.Status = registry.StatusSuspect
	if _, err := s.Select(context.Background(), RequestMeta{Model: "other"}, []registry.WorkerSnapshot{unavailable}); err != ErrNoWorker {
		t.Fatalf("got %v", err)
	}
}

func TestRoundRobinConcurrent(t *testing.T) {
	s := &RoundRobin{}
	workers := []registry.WorkerSnapshot{worker("a", 0, 0), worker("b", 0, 0)}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Select(context.Background(), RequestMeta{Model: "mock-llm"}, workers); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
