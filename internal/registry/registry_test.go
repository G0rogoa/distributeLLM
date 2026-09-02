package registry

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func validWorker(instance string) Worker {
	return Worker{ID: "worker-1", InstanceID: instance, Address: "http://worker", Models: []string{"mock-llm"}, Capacity: 4}
}

func TestRegistrationInstanceAndStaleHeartbeat(t *testing.T) {
	r := New(5*time.Second, 10*time.Second)
	if err := r.Register(validWorker("old")); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(validWorker("old")); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(validWorker("new")); err != nil {
		t.Fatal(err)
	}
	if err := r.Heartbeat("worker-1", "old", 1, 0, 10); !errors.Is(err, ErrStaleInstance) {
		t.Fatalf("got %v", err)
	}
	if err := r.Heartbeat("worker-1", "new", 2, 1, 9); err != nil {
		t.Fatal(err)
	}
	snapshot := r.Snapshots()[0]
	if snapshot.InstanceID != "new" || snapshot.Status != StatusHealthy || snapshot.ReportedRunning != 2 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestHealthTransitionsAndRecovery(t *testing.T) {
	base := time.Unix(100, 0)
	now := base
	r := New(5*time.Second, 10*time.Second)
	r.SetNowForTest(func() time.Time { return now })
	if err := r.Register(validWorker("instance")); err != nil {
		t.Fatal(err)
	}
	if err := r.Heartbeat("worker-1", "instance", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	now = base.Add(6 * time.Second)
	r.Sweep()
	if got := r.Snapshots()[0].Status; got != StatusSuspect {
		t.Fatalf("got %s", got)
	}
	if err := r.Heartbeat("worker-1", "instance", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if got := r.Snapshots()[0].Status; got != StatusHealthy {
		t.Fatalf("got %s", got)
	}
	now = base.Add(17 * time.Second)
	r.Sweep()
	if got := r.Snapshots()[0].Status; got != StatusUnavailable {
		t.Fatalf("got %s", got)
	}
}

func TestSnapshotIsDeepCopy(t *testing.T) {
	r := New(time.Second, 2*time.Second)
	if err := r.Register(validWorker("instance")); err != nil {
		t.Fatal(err)
	}
	s := r.Snapshots()
	s[0].Models[0] = "changed"
	if got := r.Snapshots()[0].Models[0]; got != "mock-llm" {
		t.Fatalf("registry mutated: %s", got)
	}
}

func TestStage3MetadataIsCopiedAndMockDefaults(t *testing.T) {
	r := New(time.Second, 2*time.Second)
	gpu := 0
	labels := map[string]string{"role": "real"}
	worker := validWorker("instance")
	worker.BackendType = "vllm"
	worker.Model = "mock-llm"
	worker.GPUIndex = &gpu
	worker.Labels = labels
	if err := r.Register(worker); err != nil {
		t.Fatal(err)
	}
	labels["role"] = "changed"
	gpu = 9
	snapshot := r.Snapshots()[0]
	if snapshot.BackendType != "vllm" || snapshot.Model != "mock-llm" || snapshot.GPUIndex == nil || *snapshot.GPUIndex != 0 || snapshot.Labels["role"] != "real" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	snapshot.Labels["role"] = "mutated"
	if got := r.Snapshots()[0].Labels["role"]; got != "real" {
		t.Fatalf("registry labels were mutated: %q", got)
	}
	mock := validWorker("mock")
	mock.ID = "mock-worker"
	if err := r.Register(mock); err != nil {
		t.Fatal(err)
	}
	for _, worker := range r.Snapshots() {
		if worker.ID == "mock-worker" && worker.BackendType != "mock" {
			t.Fatalf("mock default backend type=%q", worker.BackendType)
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	r := New(time.Hour, 2*time.Hour)
	for i := 0; i < 8; i++ {
		worker := validWorker(fmt.Sprint(i))
		worker.ID = fmt.Sprintf("worker-%d", i)
		if err := r.Register(worker); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		i := i
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = r.Heartbeat(fmt.Sprintf("worker-%d", i), fmt.Sprint(i), j%4, 0, int64(j))
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = r.Snapshots()
			}
		}()
	}
	wg.Wait()
}

func TestReservationLifecycleAndInstanceReplacement(t *testing.T) {
	r := New(time.Hour, 2*time.Hour)
	if err := r.Register(validWorker("instance")); err != nil {
		t.Fatal(err)
	}
	if err := r.Heartbeat("worker-1", "instance", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	release, err := r.Reserve("worker-1", "instance")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Snapshots()[0].LocalReservations; got != 1 {
		t.Fatalf("reservations=%d", got)
	}
	release()
	release()
	if got := r.Snapshots()[0].LocalReservations; got != 0 {
		t.Fatalf("reservations=%d", got)
	}
	release, err = r.Reserve("worker-1", "instance")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Register(validWorker("replacement")); err != nil {
		t.Fatal(err)
	}
	release()
	if got := r.Snapshots()[0].LocalReservations; got != 0 {
		t.Fatalf("replacement reservations=%d", got)
	}
}
