package cache

import (
	"sync"
	"time"
)

type AffinityIndex struct {
	mu      sync.RWMutex
	ttl     time.Duration
	now     func() time.Time
	entries map[CacheKey]map[WorkerInstanceKey]affinityEntry
}

type affinityEntry struct {
	tokens    int
	blocks    int
	expiresAt time.Time
}

func NewAffinityIndex(ttl time.Duration) *AffinityIndex {
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &AffinityIndex{ttl: ttl, now: time.Now, entries: map[CacheKey]map[WorkerInstanceKey]affinityEntry{}}
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
	workers[worker] = entry
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
		return match
	}
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
		delete(workers, worker)
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
			removed++
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

func (index *AffinityIndex) SetNowForTest(now func() time.Time) {
	index.mu.Lock()
	index.now = now
	index.mu.Unlock()
}
