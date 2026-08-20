# Future phases

Phase 2 adds prompt token blocks, block hashes, a bounded prefix trie/radix tree,
per-worker cache summaries, invalidation, overlap scoring, and a cache-aware Scheduler.
`RequestMeta` and immutable `WorkerSnapshot` are the extension points; Gateway does not
need direct Registry map access.

Phase 3 adds thin vLLM/SGLang HTTP adapters and Worker roles (`aggregated`, `prefill`,
`decode`). Real prefill/decode separation requires explicit KV transfer and failure
semantics; no fake transfer exists today.

Phase 4 may add SLO-aware admission, fairness/tenant quotas, tiered caches, Kubernetes,
and autoscaling. Registry durability and authenticated control APIs will likely require
interface changes. Scheduler selection and reservation commit remain separate so cache
or SLO scoring cannot bypass concurrency validation.
