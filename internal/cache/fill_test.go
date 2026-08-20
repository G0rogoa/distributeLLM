package cache

import (
	"sync"
	"testing"
	"time"
)

func TestFillReservationAffinityReleaseAndExpiry(t *testing.T) {
	now := time.Unix(100, 0)
	f := NewFillReservations(time.Second)
	f.now = func() time.Time { return now }
	key := CacheKey{PrefixHash: BlockHash{1}}
	worker := WorkerInstanceKey{WorkerID: "w1", InstanceID: "i1"}
	release, ok := f.Reserve(key, worker, "r1")
	if !ok {
		t.Fatal("first reservation rejected")
	}
	if got, ok := f.Affinity(key); !ok || got != worker {
		t.Fatalf("affinity = %#v, %v", got, ok)
	}
	if _, ok := f.Reserve(key, worker, "r2"); ok {
		t.Fatal("overlapping reservation accepted")
	}
	release()
	release()
	if f.Len() != 0 {
		t.Fatal("release leaked")
	}
	_, _ = f.Reserve(key, worker, "r3")
	now = now.Add(2 * time.Second)
	if f.Cleanup() != 1 || f.Len() != 0 {
		t.Fatal("expired reservation not cleaned")
	}
}

func TestFillReservationConcurrentSingleOwner(t *testing.T) {
	f := NewFillReservations(time.Minute)
	key := CacheKey{PrefixHash: BlockHash{2}}
	worker := WorkerInstanceKey{WorkerID: "w", InstanceID: "i"}
	var successes int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := f.Reserve(key, worker, time.Now().String()); ok {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("owners = %d", successes)
	}
	f.Invalidate(worker)
	if f.Len() != 0 {
		t.Fatal("invalidation leaked reservation")
	}
}
