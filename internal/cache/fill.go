package cache

import (
	"context"
	"sync"
	"time"
)

type FillReservation struct {
	Worker    WorkerInstanceKey
	RequestID string
	ExpiresAt time.Time
}

// FillReservations prevents concurrent misses for the same prefix from being
// scattered across workers. It is advisory: expiry or a wrong prediction can
// only cost performance, never affect generated output.
type FillReservations struct {
	mu    sync.Mutex
	items map[CacheKey]FillReservation
	ttl   time.Duration
	now   func() time.Time
}

func NewFillReservations(ttl time.Duration) *FillReservations {
	return &FillReservations{items: make(map[CacheKey]FillReservation), ttl: ttl, now: time.Now}
}

func (f *FillReservations) Affinity(key CacheKey) (WorkerInstanceKey, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.items[key]
	if !ok || !item.ExpiresAt.After(f.now()) {
		delete(f.items, key)
		return WorkerInstanceKey{}, false
	}
	return item.Worker, true
}

func (f *FillReservations) Reserve(key CacheKey, worker WorkerInstanceKey, requestID string) (func(), bool) {
	f.mu.Lock()
	now := f.now()
	if item, ok := f.items[key]; ok && item.ExpiresAt.After(now) && item.RequestID != requestID {
		f.mu.Unlock()
		return func() {}, false
	}
	f.items[key] = FillReservation{Worker: worker, RequestID: requestID, ExpiresAt: now.Add(f.ttl)}
	f.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			if item, ok := f.items[key]; ok && item.RequestID == requestID {
				delete(f.items, key)
			}
			f.mu.Unlock()
		})
	}, true
}

func (f *FillReservations) Invalidate(worker WorkerInstanceKey) {
	f.mu.Lock()
	for key, item := range f.items {
		if item.Worker == worker {
			delete(f.items, key)
		}
	}
	f.mu.Unlock()
}

func (f *FillReservations) Cleanup() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	now, removed := f.now(), 0
	for key, item := range f.items {
		if !item.ExpiresAt.After(now) {
			delete(f.items, key)
			removed++
		}
	}
	return removed
}

func (f *FillReservations) Len() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.items) }

func (f *FillReservations) RunCleanup(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.Cleanup()
		}
	}
}
