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
## Phase 2 cache failures

Cache metadata is only a performance hint. Event loss, reordering, lease expiry,
eviction, or Worker restart may cause a prediction miss; the Worker performs a local
lookup and safely falls back to full prefill. Instance IDs reject events from replaced
processes. Sequence gaps degrade a view and expired views are not matched. Bounded
queues and indexes prefer observable rejection over unbounded memory growth.
