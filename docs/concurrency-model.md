# Concurrency model

The standard HTTP servers create one goroutine per request. DistServe does not create
additional per-request goroutines in Milestone 1.

- Gateway request goroutine: created by `net/http`; exits after completion, upstream
  error, deadline, or client cancellation. Its derived timeout context is cancelled by
  a deferred call. It owns its upstream response body and closes it with `defer`.
- Mock worker request goroutine: created by `net/http`; exits after generation or when
  `Request.Context()` is done. It owns its timer and stops it before returning.
- Server shutdown goroutine: created by each command's `main`; signal cancellation
  triggers bounded `http.Server.Shutdown`, after which it exits.

The gateway has no mutable request-shared state. The mock worker's active-request
counter is atomic and decremented with `defer`, so cancellation cannot leak a slot.
Contexts are request-scoped and are never stored in long-lived structs.

Milestone 2's registry uses one `sync.RWMutex`: writes protect registration, heartbeat,
drain, and health transitions; reads copy snapshots under the read lock and release it
before callers do work. The health sweeper is created by controller `main`, observes
the controller root context, and exits when that context is cancelled. The worker
registration loop is created by worker `main`; its ticker is stopped on return and all
HTTP calls carry that same root context.

Important registry invariants:

- A map entry represents exactly one current instance for a stable worker ID.
- Heartbeats must match that instance ID; delayed heartbeats from replaced processes
  cannot update load or health.
- A valid heartbeat transitions a non-draining worker to Healthy.
- Sweep transitions are monotonic with heartbeat age: Healthy/Starting to Suspect,
  then Unavailable. A later valid heartbeat can recover it.
- Snapshot model slices are copied, so callers cannot mutate registry-owned memory.

The Controller admission limiter is a bounded channel. A successful send owns one slot;
the Gateway immediately defers the matching receive. Registry reservations use the
Registry mutex and return an idempotent release closure. Its invariant is that every
reservation belongs to the current `(worker ID, instance ID)` and never becomes
negative. Replacement starts with zero reservations; a late old release cannot change
the replacement.

Round-robin uses an atomic counter. Least-loaded protects only its tie-break counter
with a small mutex; it works solely on caller-owned snapshots. Mock Worker capacity and
queue channels are semaphores. Request goroutines select on their context while waiting,
and deferred cleanup returns an acquired running slot. The pseudo-random generator is
protected by its own mutex because `math/rand.Rand` is not concurrency-safe.

Telemetry counters are atomic; floating-point observation sums and the lifecycle ring
buffer use separate mutexes. No network I/O or logging occurs while Registry locks are
held.

Phase 2 prompt building, mock tokenization, block construction, and prefix hashing are
pure request-local operations. They own their returned slices and acquire no shared
locks. Context cancellation is checked before and during tokenization/prompt building.
Future Cache Index code must not hold Registry or Cache Index locks while running these
potentially linear-time operations.

The Cache Index has one `sync.RWMutex` protecting both directions of its index, Worker
instance/view state, sequence numbers, entry count, and bounded EventID deduper. Both
directions are updated within one write-lock critical section. Queries copy entries
under a read lock, then sort and truncate after unlocking. Lock rules are:

1. Never hold Registry and Cache Index locks at the same time.
2. Never tokenize, hash, log, or perform HTTP while holding the Cache Index lock.
3. Apply Worker instance changes and Cache events through explicit sequential methods.
4. The cleanup goroutine is created by its owner, stops its ticker on return, and exits
   when its context is cancelled; each cleanup pass removes only a bounded batch.

The Mock Cache mutex covers its map, LRU list, counters, and event sequence. Events are
published only after unlocking through a bounded non-blocking channel. Loss is counted;
a later sequence gap degrades the Controller view. Fill reservations use a separate
mutex and idempotent release closure. Their cleanup goroutine exits on the Controller
context. Registry, CacheIndex, MockCache, and fill locks are never held together.

## Planned Stage 4 loops and ownership

These goroutines are design contracts, not current implementations:

- GPU Observer loop periodically publishes an immutable snapshot, stops its ticker,
  and exits on Node Agent root-context cancellation. Failure is stale/unknown, never
  permission to claim a GPU.
- Lease refresh loop refreshes before TTL expiry and exits on root cancellation.
  Expiry or corrupt input is fail-closed and triggers reclaim evaluation.
- Resource evaluation loop reads copied observer, Lease, demand, and interference
  snapshots and emits explainable, versioned decisions on a bounded cadence.
- Process watcher owns handles only for DistServe-created Workers and exits after the
  child exits or shutdown cancels the watch. It cannot adopt or signal foreign PIDs.
- A drain timer belongs to one Worker instance, ending when work reaches zero or the
  grace context expires; it cancels only that instance's DistServe requests.

A Resource State Store will use one mutex for GPU/Worker state, decision version, and
owned-process identity. All external work occurs after unlocking: never hold this lock
while invoking NVML or `nvidia-smi`, starting/stopping a process, waiting for a child,
performing HTTP, logging, or acquiring Registry/Cache Index locks. Snapshots are copied
under the lock. Observer publication must not wait indefinitely for evaluation.

Start, drain, stop, and cache invalidation are idempotent and keyed by GPU UUID plus
Worker instance. Shutdown disables claims, marks owned Workers Draining in Registry,
stops new admission, waits within the grace period, cancels remaining owned requests,
stops owned processes, invalidates Cache Index instances, verifies GPU release, enters
cooldown, and stops loops. Reservations and Fill Reservations retain release on every
exit path. Restart reconciliation is conservative and never infers ownership from a
PID or GPU usage alone.
