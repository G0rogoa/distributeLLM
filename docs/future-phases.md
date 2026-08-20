# Future phases

Phase 2 has begun with deterministic prompt building, mock tokenization, fixed token
blocks, cache identity, and a versioned prefix hash chain. The remaining Phase 2 work
adds a bounded in-memory Cache Index, per-worker cache summaries, invalidation, overlap
scoring, mock LRU caches, and a cache-aware Scheduler. `RequestMeta` and immutable
`WorkerSnapshot` remain the extension points; Gateway does not need direct Registry map
access.

Phase 3 adds thin vLLM/SGLang HTTP adapters and Worker roles (`aggregated`, `prefill`,
`decode`). Real prefill/decode separation requires explicit KV transfer and failure
semantics; no fake transfer exists today.

Phase 4 may add SLO-aware admission, fairness/tenant quotas, tiered caches, Kubernetes,
and autoscaling. Registry durability and authenticated control APIs will likely require
interface changes. Scheduler selection and reservation commit remain separate so cache
or SLO scoring cannot bypass concurrency validation.
