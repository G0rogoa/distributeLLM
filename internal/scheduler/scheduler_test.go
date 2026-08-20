package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"distserve/internal/registry"
)

func worker(id string, load, reservations int) registry.WorkerSnapshot {
	return registry.WorkerSnapshot{ID: id, InstanceID: id + "-instance", Models: []string{"mock-llm"}, Status: registry.StatusHealthy, Capacity: 8, ReportedRunning: load, LocalReservations: reservations, LastHeartbeat: time.Now()}
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
