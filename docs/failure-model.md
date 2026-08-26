# Failure model

- Client cancellation propagates through the Gateway HTTP transport to Worker token
  generation. Admission and reservation release through deferred cleanup.
- A Gateway deadline returns 504 if no response has started and cancels upstream work.
- A connection failure or Worker 503 may be retried once when `-retry` is enabled. The
  first reservation is released before re-scheduling. A 200 response or any streamed
  content is never transparently retried.
- A missed heartbeat becomes Suspect after 5 seconds and Unavailable after 10 seconds.
  Schedulers only select Healthy instances. A valid matching heartbeat permits recovery.
- A restarted Worker replaces the old instance; delayed old heartbeats receive 409.
- Draining blocks new selections. Once reported running and queued work are zero it is
  Unavailable.
- Controller and Worker admission are bounded. Controller rejection is 429; Worker
  queue rejection is 503.

Phase 1 does not solve network partitions, durable state, authentication, exactly-once
execution, distributed consensus, or recovery of in-memory lifecycle history.

Phase 2 Cache Events are idempotent by bounded EventID memory and ordered by a monotonic
per-instance Sequence. Old events are ignored; a sequence gap degrades the cache view
without blocking requests. A Reset clears one instance. Worker restart removes the old
instance view, and late old events are rejected. Expired leases are excluded from lookup
before asynchronous cleanup. Controller restart loses the advisory index and therefore
starts cold rather than claiming false cache hits.

## Planned Stage 4 resource failures

- Observer error or stale data is fail-closed: no new Worker starts. An error never
  proves a GPU idle.
- Lease expiry, refresh failure, corrupt data, version rollback, or conflict blocks
  entry and moves an owned Worker toward Draining.
- Start or model-load failure removes the incomplete Registry instance, invalidates
  its cache view, records the reason, and enters backoff/cooldown.
- Stop timeout escalates only against the exact DistServe-owned process. Failure to
  verify exit or memory release keeps the GPU unavailable and raises an audit event;
  no foreign process is signalled.
- Node Agent restart reconstructs observations and configured Leases but does not
  claim ownership from device activity. Controller restart starts the advisory cache
  cold and reconciles Worker instance IDs.
- Duplicate or reordered decisions are rejected by resource/instance version;
  lifecycle operations remain idempotent.
- During reclaim, client cancellation releases request and cache-fill reservations.
  Draining prevents new selection before cancellation begins.
- A foreign compute process, GPU pressure, host memory/swap pressure, or sustained I/O
  pressure stops starts, tightens admission, and triggers prioritized reclaim.

Safe failure favors capacity loss over unauthorized use. Resource automation does not
kill, pause, reprioritize, or inspect the content of other users' processes.

## Phase 2 cache failures

Cache metadata is only a performance hint. Event loss, reordering, lease expiry,
eviction, or Worker restart may cause a prediction miss; the Worker performs a local
lookup and safely falls back to full prefill. Instance IDs reject events from replaced
processes. Sequence gaps degrade a view and expired views are not matched. Bounded
queues and indexes prefer observable rejection over unbounded memory growth.
