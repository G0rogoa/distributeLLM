package cache

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

type CacheEventType string

const (
	CacheEventAdd   CacheEventType = "add"
	CacheEventTouch CacheEventType = "touch"
	CacheEventEvict CacheEventType = "evict"
	CacheEventReset CacheEventType = "reset"
)

type CacheViewState string

const (
	CacheViewReady     CacheViewState = "ready"
	CacheViewDegraded  CacheViewState = "degraded"
	CacheViewStale     CacheViewState = "stale"
	CacheViewResetting CacheViewState = "resetting"
)

var (
	ErrInvalidCacheEvent   = errors.New("invalid cache event")
	ErrStaleWorkerInstance = errors.New("stale cache worker instance")
	ErrCacheEntryNotFound  = errors.New("cache entry not found")
	ErrCacheIndexFull      = errors.New("cache index entry limit reached")
)

type WorkerInstanceKey struct {
	WorkerID   string
	InstanceID string
}

type CacheKey struct {
	IdentityHash BlockHash
	PrefixHash   BlockHash
}

type CacheEvent struct {
	WorkerID      string         `json:"worker_id"`
	InstanceID    string         `json:"instance_id"`
	EventID       string         `json:"event_id"`
	Sequence      uint64         `json:"sequence"`
	Type          CacheEventType `json:"type"`
	Identity      CacheIdentity  `json:"identity"`
	PrefixHash    BlockHash      `json:"prefix_hash"`
	ParentHash    BlockHash      `json:"parent_hash"`
	BlockIndex    int            `json:"block_index"`
	TokenCount    int            `json:"token_count"`
	SizeBytes     int64          `json:"size_bytes"`
	ObservedAt    time.Time      `json:"observed_at"`
	LeaseDuration time.Duration  `json:"lease_duration"`
}

type CacheEntry struct {
	Worker       WorkerInstanceKey
	Identity     CacheIdentity
	IdentityHash BlockHash
	PrefixHash   BlockHash
	ParentHash   BlockHash
	BlockIndex   int
	TokenCount   int
	SizeBytes    int64
	LastAccess   time.Time
	LeaseExpires time.Time
	Sequence     uint64
}

type CacheSummary struct {
	IdentityCount int            `json:"identity_count"`
	BlockCount    int            `json:"block_count"`
	UsedBytes     int64          `json:"used_bytes"`
	CapacityBytes int64          `json:"capacity_bytes"`
	LastSequence  uint64         `json:"last_sequence"`
	LastUpdated   time.Time      `json:"last_updated"`
	State         CacheViewState `json:"state"`
}

type EventResult struct {
	Applied       bool
	Duplicate     bool
	OutOfOrder    bool
	SequenceGap   bool
	PreviousState CacheViewState
	CurrentState  CacheViewState
}

type CacheStats struct {
	Entries          int
	Workers          int
	PrefixKeys       int
	SeenEventIDs     int
	ExpiredEntries   uint64
	AppliedEvents    uint64
	DuplicateEvents  uint64
	OutOfOrderEvents uint64
	SequenceGaps     uint64
	RejectedEvents   uint64
	Resets           uint64
}

type WorkerCacheSnapshot struct {
	Worker  WorkerInstanceKey `json:"worker"`
	Summary CacheSummary      `json:"summary"`
}

type cacheWorkerView struct {
	lastSequence  uint64
	lastUpdated   time.Time
	state         CacheViewState
	capacityBytes int64
}

type eventDeduper struct {
	ids   map[string]struct{}
	ring  []string
	next  int
	count int
}

type CacheIndex struct {
	mu sync.RWMutex

	byPrefix   map[CacheKey]map[WorkerInstanceKey]*CacheEntry
	byWorker   map[WorkerInstanceKey]map[CacheKey]*CacheEntry
	views      map[WorkerInstanceKey]*cacheWorkerView
	instances  map[string]string
	seenEvents eventDeduper

	maxEntries                                                                     int
	staleViewThreshold                                                             time.Duration
	entryCount                                                                     int
	expiredEntries                                                                 uint64
	appliedEvents, duplicateEvents, outOfOrderEvents, sequenceGaps, rejectedEvents uint64
	resets                                                                         uint64
	now                                                                            func() time.Time
}

func NewCacheIndex(maxEntries, eventDedupCapacity int, staleViewThreshold time.Duration) (*CacheIndex, error) {
	if maxEntries < 1 || eventDedupCapacity < 1 || staleViewThreshold <= 0 {
		return nil, fmt.Errorf("cache index limits and stale threshold must be positive")
	}
	return &CacheIndex{byPrefix: make(map[CacheKey]map[WorkerInstanceKey]*CacheEntry), byWorker: make(map[WorkerInstanceKey]map[CacheKey]*CacheEntry), views: make(map[WorkerInstanceKey]*cacheWorkerView), instances: make(map[string]string), seenEvents: eventDeduper{ids: make(map[string]struct{}), ring: make([]string, eventDedupCapacity)}, maxEntries: maxEntries, staleViewThreshold: staleViewThreshold, now: time.Now}, nil
}

func (index *CacheIndex) SetWorkerInstance(workerID, instanceID string) error {
	if workerID == "" || instanceID == "" {
		return ErrInvalidCacheEvent
	}
	index.mu.Lock()
	defer index.mu.Unlock()
	if old := index.instances[workerID]; old != "" && old != instanceID {
		index.removeWorkerLocked(WorkerInstanceKey{WorkerID: workerID, InstanceID: old})
	}
	index.instances[workerID] = instanceID
	key := WorkerInstanceKey{WorkerID: workerID, InstanceID: instanceID}
	if index.views[key] == nil {
		index.views[key] = &cacheWorkerView{state: CacheViewReady, lastUpdated: index.now()}
	}
	return nil
}

func (index *CacheIndex) RemoveWorkerInstance(workerID, instanceID string) {
	index.mu.Lock()
	defer index.mu.Unlock()
	if index.instances[workerID] != instanceID {
		return
	}
	index.removeWorkerLocked(WorkerInstanceKey{WorkerID: workerID, InstanceID: instanceID})
	delete(index.instances, workerID)
}

func (index *CacheIndex) SetWorkerCapacity(workerID, instanceID string, capacityBytes int64) error {
	if capacityBytes < 0 {
		return ErrInvalidCacheEvent
	}
	key := WorkerInstanceKey{WorkerID: workerID, InstanceID: instanceID}
	index.mu.Lock()
	defer index.mu.Unlock()
	if index.instances[workerID] != instanceID {
		return ErrStaleWorkerInstance
	}
	view := index.views[key]
	if view == nil {
		view = &cacheWorkerView{state: CacheViewReady}
		index.views[key] = view
	}
	view.capacityBytes = capacityBytes
	return nil
}

func (index *CacheIndex) Apply(event CacheEvent) (EventResult, error) {
	prepared, cacheKey, err := prepareEvent(event, index.now())
	if err != nil {
		index.mu.Lock()
		index.rejectedEvents++
		index.mu.Unlock()
		return EventResult{}, err
	}
	workerKey := WorkerInstanceKey{WorkerID: event.WorkerID, InstanceID: event.InstanceID}
	index.mu.Lock()
	defer index.mu.Unlock()
	if index.instances[event.WorkerID] != event.InstanceID {
		index.rejectedEvents++
		return EventResult{}, ErrStaleWorkerInstance
	}
	view := index.views[workerKey]
	if view == nil {
		view = &cacheWorkerView{state: CacheViewReady}
		index.views[workerKey] = view
	}
	result := EventResult{PreviousState: view.state, CurrentState: view.state}
	if index.seenEvents.contains(event.EventID) {
		index.duplicateEvents++
		result.Duplicate = true
		return result, nil
	}
	if event.Sequence <= view.lastSequence {
		index.outOfOrderEvents++
		index.seenEvents.add(event.EventID)
		result.OutOfOrder = true
		return result, nil
	}
	if event.Sequence > view.lastSequence+1 {
		result.SequenceGap = true
		index.sequenceGaps++
	}
	if event.Type == CacheEventReset {
		index.removeEntriesLocked(workerKey)
		view.state = CacheViewReady
		index.resets++
	} else {
		switch event.Type {
		case CacheEventAdd:
			if err := index.addLocked(workerKey, cacheKey, prepared); err != nil {
				return result, err
			}
		case CacheEventTouch:
			entry := index.byWorker[workerKey][cacheKey]
			if entry == nil {
				return result, ErrCacheEntryNotFound
			}
			entry.LastAccess = prepared.LastAccess
			entry.LeaseExpires = prepared.LeaseExpires
			entry.Sequence = event.Sequence
		case CacheEventEvict:
			index.removeEntryLocked(workerKey, cacheKey)
		default:
			return result, ErrInvalidCacheEvent
		}
	}
	index.seenEvents.add(event.EventID)
	if result.SequenceGap && event.Type != CacheEventReset {
		view.state = CacheViewDegraded
	}
	view.lastSequence = event.Sequence
	view.lastUpdated = prepared.LastAccess
	result.Applied = true
	index.appliedEvents++
	result.CurrentState = view.state
	return result, nil
}

func prepareEvent(event CacheEvent, now time.Time) (*CacheEntry, CacheKey, error) {
	if event.WorkerID == "" || event.InstanceID == "" || event.EventID == "" || event.Sequence == 0 {
		return nil, CacheKey{}, ErrInvalidCacheEvent
	}
	if event.Type != CacheEventAdd && event.Type != CacheEventTouch && event.Type != CacheEventEvict && event.Type != CacheEventReset {
		return nil, CacheKey{}, ErrInvalidCacheEvent
	}
	if event.ObservedAt.IsZero() {
		event.ObservedAt = now
	}
	if event.Type != CacheEventReset && (event.LeaseDuration <= 0 || event.BlockIndex < 0 || event.TokenCount < 1 || event.SizeBytes < 0) {
		return nil, CacheKey{}, ErrInvalidCacheEvent
	}
	identityHash, err := event.Identity.Hash()
	if err != nil && event.Type != CacheEventReset {
		return nil, CacheKey{}, fmt.Errorf("hash cache identity: %w", err)
	}
	key := CacheKey{IdentityHash: identityHash, PrefixHash: event.PrefixHash}
	entry := &CacheEntry{Worker: WorkerInstanceKey{WorkerID: event.WorkerID, InstanceID: event.InstanceID}, Identity: event.Identity, IdentityHash: identityHash, PrefixHash: event.PrefixHash, ParentHash: event.ParentHash, BlockIndex: event.BlockIndex, TokenCount: event.TokenCount, SizeBytes: event.SizeBytes, LastAccess: event.ObservedAt, LeaseExpires: event.ObservedAt.Add(event.LeaseDuration), Sequence: event.Sequence}
	return entry, key, nil
}

func (index *CacheIndex) addLocked(worker WorkerInstanceKey, key CacheKey, entry *CacheEntry) error {
	workerEntries := index.byWorker[worker]
	if existing := workerEntries[key]; existing != nil {
		*existing = *entry
		return nil
	}
	if index.entryCount >= index.maxEntries {
		return ErrCacheIndexFull
	}
	if workerEntries == nil {
		workerEntries = make(map[CacheKey]*CacheEntry)
		index.byWorker[worker] = workerEntries
	}
	prefixEntries := index.byPrefix[key]
	if prefixEntries == nil {
		prefixEntries = make(map[WorkerInstanceKey]*CacheEntry)
		index.byPrefix[key] = prefixEntries
	}
	workerEntries[key] = entry
	prefixEntries[worker] = entry
	index.entryCount++
	return nil
}

func (index *CacheIndex) removeEntryLocked(worker WorkerInstanceKey, key CacheKey) {
	workerEntries := index.byWorker[worker]
	if workerEntries == nil || workerEntries[key] == nil {
		return
	}
	delete(workerEntries, key)
	if len(workerEntries) == 0 {
		delete(index.byWorker, worker)
	}
	prefixEntries := index.byPrefix[key]
	delete(prefixEntries, worker)
	if len(prefixEntries) == 0 {
		delete(index.byPrefix, key)
	}
	index.entryCount--
}
func (index *CacheIndex) removeEntriesLocked(worker WorkerInstanceKey) {
	for key := range index.byWorker[worker] {
		index.removeEntryLocked(worker, key)
	}
}
func (index *CacheIndex) removeWorkerLocked(worker WorkerInstanceKey) {
	index.removeEntriesLocked(worker)
	delete(index.views, worker)
}

func (index *CacheIndex) EntriesForWorker(worker WorkerInstanceKey, limit int, now time.Time) []CacheEntry {
	if limit < 1 {
		return nil
	}
	index.mu.RLock()
	entries := index.byWorker[worker]
	result := make([]CacheEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.LeaseExpires.After(now) {
			continue
		}
		result = append(result, *entry)
	}
	index.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].BlockIndex == result[j].BlockIndex {
			return result[i].PrefixHash.String() < result[j].PrefixHash.String()
		}
		return result[i].BlockIndex < result[j].BlockIndex
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func (index *CacheIndex) WorkersForPrefix(key CacheKey, limit int, now time.Time) []CacheEntry {
	if limit < 1 {
		return nil
	}
	index.mu.RLock()
	entries := index.byPrefix[key]
	result := make([]CacheEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.LeaseExpires.After(now) {
			continue
		}
		result = append(result, *entry)
	}
	index.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].Worker.WorkerID == result[j].Worker.WorkerID {
			return result[i].Worker.InstanceID < result[j].Worker.InstanceID
		}
		return result[i].Worker.WorkerID < result[j].Worker.WorkerID
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func (index *CacheIndex) Summary(worker WorkerInstanceKey, now time.Time) (CacheSummary, bool) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	view := index.views[worker]
	if view == nil {
		return CacheSummary{}, false
	}
	summary := CacheSummary{CapacityBytes: view.capacityBytes, LastSequence: view.lastSequence, LastUpdated: view.lastUpdated, State: view.state}
	identities := map[BlockHash]struct{}{}
	for _, entry := range index.byWorker[worker] {
		if !entry.LeaseExpires.After(now) {
			continue
		}
		summary.BlockCount++
		summary.UsedBytes += entry.SizeBytes
		identities[entry.IdentityHash] = struct{}{}
	}
	summary.IdentityCount = len(identities)
	if now.Sub(view.lastUpdated) >= index.staleViewThreshold {
		summary.State = CacheViewStale
	}
	return summary, true
}

func (index *CacheIndex) CleanupExpired(now time.Time, maxEntries int) int {
	if maxEntries < 1 {
		return 0
	}
	index.mu.Lock()
	defer index.mu.Unlock()
	removed := 0
	for worker, entries := range index.byWorker {
		for key, entry := range entries {
			if entry.LeaseExpires.After(now) {
				continue
			}
			index.removeEntryLocked(worker, key)
			removed++
			index.expiredEntries++
			if removed == maxEntries {
				return removed
			}
		}
	}
	return removed
}

func (index *CacheIndex) RunCleanup(ctx context.Context, interval time.Duration, batchSize int) {
	if interval <= 0 || batchSize < 1 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			index.CleanupExpired(now, batchSize)
		}
	}
}

func (index *CacheIndex) Stats() CacheStats {
	index.mu.RLock()
	defer index.mu.RUnlock()
	return CacheStats{Entries: index.entryCount, Workers: len(index.byWorker), PrefixKeys: len(index.byPrefix), SeenEventIDs: index.seenEvents.count, ExpiredEntries: index.expiredEntries, AppliedEvents: index.appliedEvents, DuplicateEvents: index.duplicateEvents, OutOfOrderEvents: index.outOfOrderEvents, SequenceGaps: index.sequenceGaps, RejectedEvents: index.rejectedEvents, Resets: index.resets}
}

func (index *CacheIndex) WorkerSummaries(limit int, now time.Time) []WorkerCacheSnapshot {
	if limit < 1 {
		return nil
	}
	index.mu.RLock()
	keys := make([]WorkerInstanceKey, 0, len(index.views))
	for key := range index.views {
		keys = append(keys, key)
	}
	index.mu.RUnlock()
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].WorkerID == keys[j].WorkerID {
			return keys[i].InstanceID < keys[j].InstanceID
		}
		return keys[i].WorkerID < keys[j].WorkerID
	})
	if len(keys) > limit {
		keys = keys[:limit]
	}
	result := make([]WorkerCacheSnapshot, 0, len(keys))
	for _, key := range keys {
		if summary, ok := index.Summary(key, now); ok {
			result = append(result, WorkerCacheSnapshot{Worker: key, Summary: summary})
		}
	}
	return result
}

func (index *CacheIndex) FindPrefixHash(prefix BlockHash, limit int, now time.Time) []CacheEntry {
	if limit < 1 {
		return nil
	}
	index.mu.RLock()
	result := make([]CacheEntry, 0)
	for key, workers := range index.byPrefix {
		if key.PrefixHash != prefix {
			continue
		}
		for _, entry := range workers {
			if entry.LeaseExpires.After(now) {
				result = append(result, *entry)
			}
		}
	}
	index.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].Worker.WorkerID == result[j].Worker.WorkerID {
			return result[i].Worker.InstanceID < result[j].Worker.InstanceID
		}
		return result[i].Worker.WorkerID < result[j].Worker.WorkerID
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func (index *CacheIndex) ValidateInvariants() error {
	index.mu.RLock()
	defer index.mu.RUnlock()
	count := 0
	for worker, entries := range index.byWorker {
		for key, entry := range entries {
			count++
			other := index.byPrefix[key][worker]
			if other == nil || other != entry {
				return fmt.Errorf("missing byPrefix entry for worker=%+v key=%s", worker, key.PrefixHash.String())
			}
		}
	}
	for key, entries := range index.byPrefix {
		for worker, entry := range entries {
			other := index.byWorker[worker][key]
			if other == nil || other != entry {
				return fmt.Errorf("missing byWorker entry for worker=%+v key=%s", worker, key.PrefixHash.String())
			}
		}
	}
	if count != index.entryCount {
		return fmt.Errorf("entry count=%d tracked=%d", count, index.entryCount)
	}
	return nil
}

func (deduper *eventDeduper) contains(id string) bool { _, ok := deduper.ids[id]; return ok }
func (deduper *eventDeduper) add(id string) {
	if deduper.contains(id) {
		return
	}
	if deduper.count < len(deduper.ring) {
		deduper.ring[deduper.next] = id
		deduper.next = (deduper.next + 1) % len(deduper.ring)
		deduper.count++
		deduper.ids[id] = struct{}{}
		return
	}
	old := deduper.ring[deduper.next]
	delete(deduper.ids, old)
	deduper.ring[deduper.next] = id
	deduper.next = (deduper.next + 1) % len(deduper.ring)
	deduper.ids[id] = struct{}{}
}
