package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func newTestIndex(t *testing.T, maxEntries, dedup int) *CacheIndex {
	t.Helper()
	index, err := NewCacheIndex(maxEntries, dedup, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func testPrefix(t *testing.T, tokenStart TokenID) (CacheIdentity, PrefixBlock) {
	t.Helper()
	identity := testCacheIdentity()
	tokens := []TokenID{tokenStart, tokenStart + 1, tokenStart + 2, tokenStart + 3}
	blocks, err := BuildTokenBlocks(tokens, 4, 100)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := BuildPrefixChain(identity, blocks)
	if err != nil {
		t.Fatal(err)
	}
	return identity, chain[0]
}

func cacheEvent(t *testing.T, worker, instance, eventID string, sequence uint64, eventType CacheEventType, tokenStart TokenID, observed time.Time) CacheEvent {
	t.Helper()
	identity, prefix := testPrefix(t, tokenStart)
	return CacheEvent{WorkerID: worker, InstanceID: instance, EventID: eventID, Sequence: sequence, Type: eventType, Identity: identity, PrefixHash: prefix.PrefixHash, ParentHash: prefix.ParentHash, BlockIndex: prefix.Index, TokenCount: prefix.TokenCount, SizeBytes: 64, ObservedAt: observed, LeaseDuration: time.Minute}
}

func TestCacheIndexAddTouchEvictResetAndInvariants(t *testing.T) {
	now := time.Unix(100, 0)
	index := newTestIndex(t, 10, 10)
	if err := index.SetWorkerInstance("worker-1", "instance-1"); err != nil {
		t.Fatal(err)
	}
	add := cacheEvent(t, "worker-1", "instance-1", "add", 1, CacheEventAdd, 1, now)
	result, err := index.Apply(add)
	if err != nil || !result.Applied {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if stats := index.Stats(); stats.Entries != 1 || stats.PrefixKeys != 1 {
		t.Fatalf("stats=%+v", stats)
	}
	duplicate, err := index.Apply(add)
	if err != nil || !duplicate.Duplicate || duplicate.Applied {
		t.Fatalf("result=%+v err=%v", duplicate, err)
	}
	touch := add
	touch.EventID = "touch"
	touch.Sequence = 2
	touch.Type = CacheEventTouch
	touch.ObservedAt = now.Add(time.Second)
	if _, err := index.Apply(touch); err != nil {
		t.Fatal(err)
	}
	identityHash, _ := add.Identity.Hash()
	entries := index.WorkersForPrefix(CacheKey{IdentityHash: identityHash, PrefixHash: add.PrefixHash}, 10, now)
	if len(entries) != 1 || !entries[0].LastAccess.Equal(touch.ObservedAt) {
		t.Fatalf("entries=%+v", entries)
	}
	evict := add
	evict.EventID = "evict"
	evict.Sequence = 3
	evict.Type = CacheEventEvict
	if _, err := index.Apply(evict); err != nil {
		t.Fatal(err)
	}
	if index.Stats().Entries != 0 {
		t.Fatal("evict left entry")
	}
	add.EventID = "add-again"
	add.Sequence = 4
	if _, err := index.Apply(add); err != nil {
		t.Fatal(err)
	}
	reset := CacheEvent{WorkerID: "worker-1", InstanceID: "instance-1", EventID: "reset", Sequence: 5, Type: CacheEventReset, ObservedAt: now.Add(2 * time.Second)}
	if _, err := index.Apply(reset); err != nil {
		t.Fatal(err)
	}
	if index.Stats().Entries != 0 {
		t.Fatal("reset left entries")
	}
	if err := index.ValidateInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestCacheIndexSequenceGapOutOfOrderAndRestart(t *testing.T) {
	now := time.Unix(200, 0)
	index := newTestIndex(t, 10, 10)
	_ = index.SetWorkerInstance("worker-1", "old")
	first := cacheEvent(t, "worker-1", "old", "first", 1, CacheEventAdd, 1, now)
	if _, err := index.Apply(first); err != nil {
		t.Fatal(err)
	}
	gap := cacheEvent(t, "worker-1", "old", "gap", 3, CacheEventAdd, 10, now)
	result, err := index.Apply(gap)
	if err != nil || !result.SequenceGap || result.CurrentState != CacheViewDegraded {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	old := gap
	old.EventID = "old"
	old.Sequence = 2
	result, err = index.Apply(old)
	if err != nil || !result.OutOfOrder || result.Applied {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if err := index.SetWorkerInstance("worker-1", "new"); err != nil {
		t.Fatal(err)
	}
	if index.Stats().Entries != 0 {
		t.Fatal("restart retained old cache")
	}
	first.EventID = "late"
	first.Sequence = 4
	if _, err := index.Apply(first); !errors.Is(err, ErrStaleWorkerInstance) {
		t.Fatalf("got %v", err)
	}
	summary, ok := index.Summary(WorkerInstanceKey{WorkerID: "worker-1", InstanceID: "new"}, now)
	if !ok || summary.State != CacheViewReady || summary.BlockCount != 0 {
		t.Fatalf("summary=%+v ok=%v", summary, ok)
	}
	if err := index.ValidateInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestCacheIndexLeaseCleanupAndBoundedDedup(t *testing.T) {
	now := time.Unix(300, 0)
	index := newTestIndex(t, 2, 2)
	_ = index.SetWorkerInstance("worker-1", "instance")
	for i := 0; i < 2; i++ {
		event := cacheEvent(t, "worker-1", "instance", fmt.Sprintf("event-%d", i), uint64(i+1), CacheEventAdd, TokenID(i*10+1), now)
		event.LeaseDuration = time.Second
		if _, err := index.Apply(event); err != nil {
			t.Fatal(err)
		}
	}
	third := cacheEvent(t, "worker-1", "instance", "event-2", 3, CacheEventAdd, 30, now)
	third.LeaseDuration = time.Second
	if _, err := index.Apply(third); !errors.Is(err, ErrCacheIndexFull) {
		t.Fatalf("got %v", err)
	}
	if got := len(index.EntriesForWorker(WorkerInstanceKey{WorkerID: "worker-1", InstanceID: "instance"}, 10, now.Add(2*time.Second))); got != 0 {
		t.Fatalf("expired query entries=%d", got)
	}
	if removed := index.CleanupExpired(now.Add(2*time.Second), 1); removed != 1 {
		t.Fatalf("removed=%d", removed)
	}
	if result, err := index.Apply(third); err != nil || !result.Applied {
		t.Fatalf("failed event was not retryable: result=%+v err=%v", result, err)
	}
	if removed := index.CleanupExpired(now.Add(2*time.Second), 10); removed != 2 {
		t.Fatalf("removed=%d", removed)
	}
	stats := index.Stats()
	if stats.Entries != 0 || stats.SeenEventIDs != 2 || stats.ExpiredEntries != 3 {
		t.Fatalf("stats=%+v", stats)
	}
	if err := index.ValidateInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestCacheIndexSnapshotCopiesAndStaleView(t *testing.T) {
	now := time.Unix(400, 0)
	index := newTestIndex(t, 10, 10)
	_ = index.SetWorkerInstance("worker-1", "instance")
	event := cacheEvent(t, "worker-1", "instance", "event", 1, CacheEventAdd, 1, now)
	if _, err := index.Apply(event); err != nil {
		t.Fatal(err)
	}
	worker := WorkerInstanceKey{WorkerID: "worker-1", InstanceID: "instance"}
	entries := index.EntriesForWorker(worker, 10, now)
	entries[0].SizeBytes = 999
	again := index.EntriesForWorker(worker, 10, now)
	if again[0].SizeBytes == 999 {
		t.Fatal("snapshot exposed mutable entry")
	}
	summary, ok := index.Summary(worker, now.Add(11*time.Second))
	if !ok || summary.State != CacheViewStale {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestCacheIndexConcurrentEventsAndCleanupExit(t *testing.T) {
	index := newTestIndex(t, 1000, 1000)
	_ = index.SetWorkerInstance("worker-1", "instance")
	now := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			event := cacheEvent(t, "worker-1", "instance", fmt.Sprintf("event-%d", i), uint64(i+1), CacheEventAdd, TokenID(i*10+1), now)
			_, _ = index.Apply(event)
			_ = index.EntriesForWorker(WorkerInstanceKey{WorkerID: "worker-1", InstanceID: "instance"}, 10, now)
		}()
	}
	wg.Wait()
	if err := index.ValidateInvariants(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { index.RunCleanup(ctx, time.Millisecond, 10); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup goroutine did not exit")
	}
}
