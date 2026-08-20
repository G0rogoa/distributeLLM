# Code tour for a C++ developer

Start at `cmd/controller/main.go`: flags build concrete Registry and Scheduler values,
the root signal context owns the sweeper lifetime, and one `http.ServeMux` combines
control and data APIs. A request enters `gateway.chatCompletions`, is validated and
admitted, then becomes a `scheduler.RequestMeta`. Scheduler reads a deep-copied Registry
snapshot; `Registry.Reserve` revalidates `(ID, InstanceID, Healthy, capacity)` under the
write lock. This second commit prevents many requests using the same stale heartbeat.

Gateway creates the Worker HTTP request with its deadline context. Before any response
is written, one connection/503 retry may release the old reservation and repeat
selection. Once status 200 is accepted, SSE lines are copied and flushed immediately;
the first data event records TTFT. Deferred cleanup releases admission, reservation,
response body, metrics gauges, and lifecycle state on normal, timeout, cancellation,
and error paths.

`cmd/mockworker/main.go` creates a unique process instance and starts registration.
`registration.go` re-registers after heartbeat failure and exits on the root context.
`worker.go` first tries a running semaphore, otherwise a bounded queue semaphore. It
models prefill from input length and decode from active concurrency, with a mutex around
the seeded random generator. Request context cancellation interrupts every timer.

Metrics are collected in `telemetry.Metrics`; request history is a bounded mutex-backed
ring in `lifecycle.Store`. Failure discovery happens in `Registry.RunSweeper`. Unit tests
cover each state owner; `tests/integration` covers three Workers, round-robin spread,
TTL removal, and zero reservations. Phase 2 cache routing should add cache summary fields
to snapshots and implement another Scheduler without putting cache logic in Gateway.

## Interview questions and answer points

1. **Why separate selection and reservation?** Snapshots become stale; atomic commit
   validates identity, health, and capacity and accounts for concurrent assignments.
2. **What thundering herd exists here?** Many arrivals can observe one low reported
   load before the next heartbeat; local reservations immediately raise effective load.
3. **Why an Instance ID?** A stable ID identifies placement while Instance ID fences
   delayed messages from a previous process incarnation.
4. **Why RWMutex instead of concurrent maps?** Worker updates span several fields and
   state transitions that must be observed atomically; one lock keeps invariants clear.
5. **Why copy snapshots?** Scheduler must not hold Registry locks or mutate owned slices
   during scoring/network work.
6. **How does cancellation propagate?** Client request context parents the Gateway
   timeout context, which is attached to the upstream HTTP request and Worker timers.
7. **Why call SSE Flush?** `Write` may buffer; flush makes first-token latency observable
   and prevents token batching in intermediaries.
8. **When is retry safe?** Only before a response starts, for transport failure or 503,
   within the original deadline, and at most once.
9. **Why not retry partial streams?** The client may receive duplicated tokens and HTTP
   headers/status can no longer be changed safely.
10. **How is backpressure represented?** A Controller admission channel, Worker running
    channel, and separate bounded queue channel; all acquisitions have release paths.
11. **How are reservation leaks prevented?** An idempotent `sync.Once` release closure is
    deferred after commit and explicitly invoked before retries.
12. **What happens on Worker restart?** New registration replaces the instance with zero
    reservations; old heartbeats/releases cannot mutate it.
13. **Why least-loaded tie-breaking?** Without it equal scores permanently bias the
    lexicographically first Worker; a mutex-protected rotating index spreads ties.
14. **What does race testing cover?** Registry reads/writes, scheduler counters, Worker
    atomics/random source, metrics, and lifecycle ring under concurrent requests.
15. **Why is the Controller a modular monolith?** Phase 1 state and failure boundaries
    stay inspectable without distributed coordination; modules retain separable owners.
16. **Which metrics avoid high cardinality?** No request IDs, prompts, or addresses are
    labels; Worker ID/state are bounded experimental dimensions.
17. **What is missing for production?** Authentication, durable Registry, TLS, richer
    histograms, distributed coordination, real inference adapters, and overload tuning.
18. **Where does KV-aware scheduling fit?** Add cache features to immutable snapshots and
    request metadata, then implement a new Scheduler; reservation semantics remain.
