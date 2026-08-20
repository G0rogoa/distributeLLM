package cache

import (
	"container/list"
	"errors"
	"strconv"
	"sync"
	"time"
)

var ErrMockCacheEntryTooLarge = errors.New("cache entry exceeds mock cache capacity")

type MockCacheStats struct {
	Entries                 int
	UsedBytes               int64
	CapacityBytes           int64
	Hits, Misses, Evictions uint64
}

type mockCacheEntry struct {
	entry CacheEntry
	lru   *list.Element
}

type MockCache struct {
	mu                      sync.Mutex
	worker                  WorkerInstanceKey
	capacityBytes           int64
	usedBytes               int64
	entries                 map[CacheKey]*mockCacheEntry
	lru                     *list.List
	nextSequence            uint64
	hits, misses, evictions uint64
}

func NewMockCache(worker WorkerInstanceKey, capacityBytes int64) (*MockCache, error) {
	if worker.WorkerID == "" || worker.InstanceID == "" || capacityBytes < 0 {
		return nil, ErrInvalidCacheEvent
	}
	return &MockCache{worker: worker, capacityBytes: capacityBytes, entries: make(map[CacheKey]*mockCacheEntry), lru: list.New()}, nil
}

func (cache *MockCache) Lookup(identity CacheIdentity, blocks []PrefixBlock, now time.Time, lease time.Duration) (int, int, []CacheEvent, error) {
	identityHash, err := identity.Hash()
	if err != nil {
		return 0, 0, nil, err
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	matchedBlocks, matchedTokens := 0, 0
	events := make([]CacheEvent, 0, len(blocks))
	for _, block := range blocks {
		key := CacheKey{IdentityHash: identityHash, PrefixHash: block.PrefixHash}
		item := cache.entries[key]
		if item == nil {
			break
		}
		item.entry.LastAccess = now
		item.entry.LeaseExpires = now.Add(lease)
		cache.lru.MoveToFront(item.lru)
		matchedBlocks++
		matchedTokens += block.TokenCount
		cache.hits++
		events = append(events, cache.eventLocked(CacheEventTouch, item.entry, now, lease))
	}
	if matchedBlocks == 0 {
		cache.misses++
	}
	return matchedBlocks, matchedTokens, events, nil
}

func (cache *MockCache) Fill(identity CacheIdentity, blocks []PrefixBlock, sizePerToken int64, now time.Time, lease time.Duration) ([]CacheEvent, error) {
	if sizePerToken <= 0 || lease <= 0 {
		return nil, ErrInvalidCacheEvent
	}
	identityHash, err := identity.Hash()
	if err != nil {
		return nil, err
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	events := make([]CacheEvent, 0)
	for _, block := range blocks {
		key := CacheKey{IdentityHash: identityHash, PrefixHash: block.PrefixHash}
		if item := cache.entries[key]; item != nil {
			item.entry.LastAccess = now
			item.entry.LeaseExpires = now.Add(lease)
			cache.lru.MoveToFront(item.lru)
			continue
		}
		size := int64(block.TokenCount) * sizePerToken
		if size > cache.capacityBytes {
			return events, ErrMockCacheEntryTooLarge
		}
		for cache.usedBytes+size > cache.capacityBytes {
			oldest := cache.lru.Back()
			if oldest == nil {
				break
			}
			oldKey := oldest.Value.(CacheKey)
			old := cache.entries[oldKey]
			events = append(events, cache.eventLocked(CacheEventEvict, old.entry, now, lease))
			cache.usedBytes -= old.entry.SizeBytes
			delete(cache.entries, oldKey)
			cache.lru.Remove(oldest)
			cache.evictions++
		}
		entry := CacheEntry{Worker: cache.worker, Identity: identity, IdentityHash: identityHash, PrefixHash: block.PrefixHash, ParentHash: block.ParentHash, BlockIndex: block.Index, TokenCount: block.TokenCount, SizeBytes: size, LastAccess: now, LeaseExpires: now.Add(lease)}
		element := cache.lru.PushFront(key)
		cache.entries[key] = &mockCacheEntry{entry: entry, lru: element}
		cache.usedBytes += size
		events = append(events, cache.eventLocked(CacheEventAdd, entry, now, lease))
	}
	return events, nil
}

func (cache *MockCache) Reset(now time.Time) CacheEvent {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.entries = make(map[CacheKey]*mockCacheEntry)
	cache.lru.Init()
	cache.usedBytes = 0
	cache.nextSequence++
	return CacheEvent{WorkerID: cache.worker.WorkerID, InstanceID: cache.worker.InstanceID, EventID: cache.eventID(cache.nextSequence), Sequence: cache.nextSequence, Type: CacheEventReset, ObservedAt: now}
}

func (cache *MockCache) Stats() MockCacheStats {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return MockCacheStats{Entries: len(cache.entries), UsedBytes: cache.usedBytes, CapacityBytes: cache.capacityBytes, Hits: cache.hits, Misses: cache.misses, Evictions: cache.evictions}
}

func (cache *MockCache) eventLocked(eventType CacheEventType, entry CacheEntry, now time.Time, lease time.Duration) CacheEvent {
	cache.nextSequence++
	return CacheEvent{WorkerID: cache.worker.WorkerID, InstanceID: cache.worker.InstanceID, EventID: cache.eventID(cache.nextSequence), Sequence: cache.nextSequence, Type: eventType, Identity: entry.Identity, PrefixHash: entry.PrefixHash, ParentHash: entry.ParentHash, BlockIndex: entry.BlockIndex, TokenCount: entry.TokenCount, SizeBytes: entry.SizeBytes, ObservedAt: now, LeaseDuration: lease}
}
func (cache *MockCache) eventID(sequence uint64) string {
	return cache.worker.WorkerID + ":" + cache.worker.InstanceID + ":" + strconv.FormatUint(sequence, 10)
}
