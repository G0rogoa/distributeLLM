# Cache index

The Phase 2 Controller Cache Index is an in-memory, advisory view of mock Worker cache
locations. It is not GPU memory and is not an absolute source of truth. A Worker must
later validate every predicted hit locally; stale metadata may reduce performance but
must never change model output.

The index owns two maps under one `sync.RWMutex`:

```text
byWorker[worker instance][cache key] -> entry
byPrefix[cache key][worker instance] -> the same entry
```

For every `byWorker[w][k]`, `byPrefix[k][w]` must contain the identical internal entry,
and vice versa. Add updates both directions, Evict removes both, Reset removes every
entry for one instance, and Worker replacement removes the old instance before creating
the new view. `ValidateInvariants` is a test/debug helper rather than a hot-path check.

External queries return copied `CacheEntry` values and never expose internal maps or
pointers. Tokenization, hashing, HTTP, logging, and Registry calls occur outside the
Cache Index lock.

## Events and ordering

Events carry a globally unique EventID and a monotonically increasing per-instance
Sequence. Repeated EventIDs are idempotent. A sequence not greater than the last applied
sequence is ignored as out of order. A forward gap is applied but marks the cache view
Degraded; Reset clears the instance and returns it to Ready. Failed events are not put
in the deduper, so capacity pressure can be corrected and the event retried.

The EventID deduper is a bounded ring: inserting beyond capacity forgets the oldest ID.
This bounds memory but means very old duplicates are then handled by Sequence ordering.

## Leases and cleanup

Each entry expires at `ObservedAt + LeaseDuration`. Queries ignore expired entries even
before physical cleanup. `CleanupExpired` deletes at most a configured batch, and
`RunCleanup` owns a ticker that exits when its context is cancelled. A Worker view can
also report Stale when it has not received an event within the configured view threshold.

The index has a hard maximum entry count. It returns `ErrCacheIndexFull` instead of
growing without bound; rejected events remain retryable and do not advance sequence.
