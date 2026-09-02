package cache

import (
	"sync"
	"time"
)

type AffinityIndex struct {
	mu         sync.RWMutex
	ttl        time.Duration
	maxEntries int
	now        func() time.Time
	entries    map[CacheKey]map[WorkerInstanceKey]affinityEntry
	entryCount int
	hits       uint64
	misses     uint64
	expired    uint64
	evicted    uint64
	cleared    uint64
}

type affinityEntry struct {
	tokens    int
	blocks    int
	expiresAt time.Time
}

type AffinityStats struct {
	Entries                 int    `json:"entries"`
	Hits                    uint64 `json:"hits"`
	Misses                  uint64 `json:"misses"`
	Expired                 uint64 `json:"expired"`
	Evicted                 uint64 `json:"evicted"`
	ClearedOnInstanceChange uint64 `json:"cleared_on_instance_change"`
}

func NewAffinityIndex(ttl time.Duration) *AffinityIndex {
	return NewBoundedAffinityIndex(ttl, 100000)
}

func NewBoundedAffinityIndex(ttl time.Duration, maxEntries int) *AffinityIndex {
	if ttl <= 0 {
		ttl = time.Minute
	}
	if maxEntries < 1 {
		maxEntries = 1
	}
	return &AffinityIndex{ttl: ttl, maxEntries: maxEntries, now: time.Now, entries: map[CacheKey]map[WorkerInstanceKey]affinityEntry{}}
}

func (index *AffinityIndex) RecordShadow(worker WorkerInstanceKey, identity CacheIdentity, blocks []PrefixBlock, totalTokens int) {
	if len(blocks) == 0 || totalTokens <= 0 {
		return
	}
	identityHash, err := identity.Hash()
	if err != nil {
		return
	}
	last := blocks[len(blocks)-1]
	key := CacheKey{IdentityHash: identityHash, PrefixHash: last.PrefixHash}
	entry := affinityEntry{tokens: totalTokens, blocks: len(blocks), expiresAt: index.now().Add(index.ttl)}
	index.mu.Lock()
	workers := index.entries[key]
	if workers == nil {
		workers = map[WorkerInstanceKey]affinityEntry{}
		index.entries[key] = workers
	}
	if _, ok := workers[worker]; !ok {
		index.entryCount++
	}
	workers[worker] = entry
	index.evictLocked()
	index.mu.Unlock()
}

func (index *AffinityIndex) Match(worker WorkerInstanceKey, identity CacheIdentity, blocks []PrefixBlock, totalTokens int) PrefixMatch {
	match := PrefixMatch{WorkerID: worker.WorkerID, InstanceID: worker.InstanceID, Evidence: EvidenceUnknown, TotalFullBlocks: len(blocks), TotalInputTokens: totalTokens}
	if len(blocks) == 0 {
		return match
	}
	identityHash, err := identity.Hash()
	if err != nil {
		return match
	}
	last := blocks[len(blocks)-1]
	key := CacheKey{IdentityHash: identityHash, PrefixHash: last.PrefixHash}
	now := index.now()
	index.mu.RLock()
	entry, ok := index.entries[key][worker]
	index.mu.RUnlock()
	if !ok || !entry.expiresAt.After(now) {
		index.mu.Lock()
		index.misses++
		index.mu.Unlock()
		return match
	}
	index.mu.Lock()
	index.hits++
	index.mu.Unlock()
	match.Evidence = EvidenceShadowEstimated
	match.MatchedBlocks = entry.blocks
	match.MatchedTokens = entry.tokens
	match.OldestLeaseExpiry = entry.expiresAt
	if totalTokens > 0 {
		match.MatchRatio = float64(match.MatchedTokens) / float64(totalTokens)
	}
	return match
}

func (index *AffinityIndex) ClearWorker(worker WorkerInstanceKey) {
	index.mu.Lock()
	defer index.mu.Unlock()
	for key, workers := range index.entries {
		if _, ok := workers[worker]; ok {
			delete(workers, worker)
			index.entryCount--
			index.cleared++
		}
		if len(workers) == 0 {
			delete(index.entries, key)
		}
	}
}

func (index *AffinityIndex) CleanupExpired(limit int) int {
	if limit < 1 {
		return 0
	}
	now := index.now()
	removed := 0
	index.mu.Lock()
	defer index.mu.Unlock()
	for key, workers := range index.entries {
		for worker, entry := range workers {
			if entry.expiresAt.After(now) {
				continue
			}
			delete(workers, worker)
			index.entryCount--
			removed++
			index.expired++
			if removed >= limit {
				return removed
			}
		}
		if len(workers) == 0 {
			delete(index.entries, key)
		}
	}
	return removed
}

func (index *AffinityIndex) Stats() AffinityStats {
	index.mu.RLock()
	defer index.mu.RUnlock()
	return AffinityStats{Entries: index.entryCount, Hits: index.hits, Misses: index.misses, Expired: index.expired, Evicted: index.evicted, ClearedOnInstanceChange: index.cleared}
}

func (index *AffinityIndex) SetNowForTest(now func() time.Time) {
	index.mu.Lock()
	index.now = now
	index.mu.Unlock()
}

func (index *AffinityIndex) evictLocked() {
	for index.entryCount > index.maxEntries {
		var oldestKey CacheKey
		var oldestWorker WorkerInstanceKey
		var oldest time.Time
		found := false
		for key, workers := range index.entries {
			for worker, entry := range workers {
				if !found || entry.expiresAt.Before(oldest) {
					oldestKey, oldestWorker, oldest = key, worker, entry.expiresAt
					found = true
				}
			}
		}
		if !found {
			index.entryCount = 0
			return
		}
		delete(index.entries[oldestKey], oldestWorker)
		if len(index.entries[oldestKey]) == 0 {
			delete(index.entries, oldestKey)
		}
		index.entryCount--
		index.evicted++
	}
}
