# Future stages

DistServe's roadmap is deliberately single-node. Resource elasticity extends the
existing LLM request scheduler; it is not a separate GPU-monitoring product.

## Stage 1: distributed inference control plane — complete

Gateway, standalone Mock Workers, Registry and heartbeats, round-robin and
least-loaded scheduling, atomic reservations, SSE proxying, bounded admission,
failure handling, telemetry, lifecycle records, and a Go load generator.

## Stage 2: KV Cache-aware scheduling — complete in mock mode

Versioned prompt identity, deterministic mock tokenization, token blocks and prefix
hash chains, a bounded lease-based Cache Index, Worker cache events, Mock Worker LRU,
prefix-aware scheduling, eviction, and Cache Fill Reservations. Metadata is advisory
and Workers verify hits locally.

## Stage 3: real single-node multi-GPU inference — planned

- Thin vLLM adapter and a tokenizer compatible with the served model.
- One to five independent Workers, initially one Worker per A100.
- Real TTFT, TPOT, throughput, tail latency, and prefix-cache observations.
- Static 1→2→3→4→5 Worker scaling on the target single node.

## Stage 4: shared-resource elasticity — planned

- Node Agent, GPU Observer, Cooperative Lease, Resource Policy, and Interference Guard.
- Elasticity Manager with idempotent start, drain, stop, release verification, and
  cooldown for DistServe-owned Workers.
- Mock observation and sanitized Trace Replay before real-device integration.
- Immutable resource-stability features for request scoring; the Scheduler never
  invokes NVML or `nvidia-smi`.
- Dynamic contraction/expansion, reclaim-risk, and interference experiments.

Automatic mode remains disabled unless an allowed GPU set and valid Lease are
configured. Kubernetes, Slurm, multi-node placement, and general-purpose cluster
scheduling are outside this stage.

## Stage 5: optional single-node prefill/decode separation

Evaluate aggregated Workers against 1P+3D, 2P+2D, and 3P+1D pools with explicit local
KV transfer and failure semantics. This stage makes no cross-node RDMA claim.

Possible later work includes authenticated control APIs, durable state, fairness, and
quotas. Those are not current capabilities.
