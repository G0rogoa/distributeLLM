package cache

import (
	"sync"
	"testing"
	"time"
)

func prefixBlocksFor(t *testing.T, start TokenID, count int) []PrefixBlock {
	t.Helper()
	identity := testCacheIdentity()
	tokens := make([]TokenID, count*identity.BlockSizeTokens)
	for i := range tokens {
		tokens[i] = start + TokenID(i)
	}
	blocks, err := BuildTokenBlocks(tokens, identity.BlockSizeTokens, 1000)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := BuildPrefixChain(identity, blocks)
	if err != nil {
		t.Fatal(err)
	}
	return chain
}

func TestMockCacheContinuousHitFillAndEvents(t *testing.T) {
	worker := WorkerInstanceKey{WorkerID: "worker-1", InstanceID: "instance-1"}
	mock, err := NewMockCache(worker, 1024)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	blocks := prefixBlocksFor(t, 1, 3)
	events, err := mock.Fill(testCacheIdentity(), blocks[:2], 10, now, time.Minute)
	if err != nil || len(events) != 2 || events[0].Type != CacheEventAdd {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	matchedBlocks, matchedTokens, touches, err := mock.Lookup(testCacheIdentity(), blocks, now.Add(time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if matchedBlocks != 2 || matchedTokens != 8 || len(touches) != 2 {
		t.Fatalf("blocks=%d tokens=%d touches=%d", matchedBlocks, matchedTokens, len(touches))
	}
	if events[0].Sequence >= touches[0].Sequence {
		t.Fatal("event sequence did not increase")
	}
	stats := mock.Stats()
	if stats.Entries != 2 || stats.Hits != 2 || stats.Misses != 0 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestMockCacheStopsAtPrefixGap(t *testing.T) {
	mock, _ := NewMockCache(WorkerInstanceKey{WorkerID: "w", InstanceID: "i"}, 1024)
	now := time.Unix(200, 0)
	blocks := prefixBlocksFor(t, 1, 3)
	if _, err := mock.Fill(testCacheIdentity(), blocks[1:], 10, now, time.Minute); err != nil {
		t.Fatal(err)
	}
	matched, _, _, err := mock.Lookup(testCacheIdentity(), blocks, now, time.Minute)
	if err != nil || matched != 0 {
		t.Fatalf("matched=%d err=%v", matched, err)
	}
}

func TestMockCacheLRUEvictionAndReset(t *testing.T) {
	mock, _ := NewMockCache(WorkerInstanceKey{WorkerID: "w", InstanceID: "i"}, 8)
	now := time.Unix(300, 0)
	a := prefixBlocksFor(t, 1, 1)
	b := prefixBlocksFor(t, 10, 1)
	c := prefixBlocksFor(t, 20, 1)
	for _, blocks := range [][]PrefixBlock{a, b} {
		if _, err := mock.Fill(testCacheIdentity(), blocks, 1, now, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	_, _, _, _ = mock.Lookup(testCacheIdentity(), a, now, time.Minute)
	events, err := mock.Fill(testCacheIdentity(), c, 1, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != CacheEventEvict || events[1].Type != CacheEventAdd {
		t.Fatalf("events=%+v", events)
	}
	matched, _, _, _ := mock.Lookup(testCacheIdentity(), b, now, time.Minute)
	if matched != 0 {
		t.Fatal("least recently used entry was not evicted")
	}
	reset := mock.Reset(now)
	if reset.Type != CacheEventReset || mock.Stats().Entries != 0 {
		t.Fatalf("reset=%+v stats=%+v", reset, mock.Stats())
	}
}

func TestMockCacheConcurrentAccess(t *testing.T) {
	mock, _ := NewMockCache(WorkerInstanceKey{WorkerID: "w", InstanceID: "i"}, 1<<20)
	now := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			blocks := prefixBlocksFor(t, TokenID(i*10+1), 1)
			_, _ = mock.Fill(testCacheIdentity(), blocks, 4, now, time.Minute)
			_, _, _, _ = mock.Lookup(testCacheIdentity(), blocks, now, time.Minute)
		}()
	}
	wg.Wait()
	stats := mock.Stats()
	if stats.Entries == 0 || stats.UsedBytes > stats.CapacityBytes {
		t.Fatalf("stats=%+v", stats)
	}
}
