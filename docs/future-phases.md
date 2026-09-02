# 后续阶段

DistServe 的路线图有意限制在单节点。Resource elasticity 是对现有 LLM request scheduler 的扩展，不是一个单独的 GPU monitoring 产品。

## Stage 1：distributed inference control plane，已完成

Gateway、独立 Mock Workers、Registry 和 heartbeats、round-robin 与 least-loaded scheduling、atomic reservations、SSE proxying、有界 admission、failure handling、telemetry、lifecycle records，以及 Go load generator。

## Stage 2：KV Cache-aware scheduling，Mock 模式已完成

Versioned prompt identity、deterministic mock tokenization、token blocks 和 prefix hash chains、基于 lease 的有界 Cache Index、Worker cache events、Mock Worker LRU、prefix-aware scheduling、eviction 和 Cache Fill Reservations。Metadata 是 advisory，Workers 会本地验证 hits。

## Stage 3：真实单节点 backend integration，进行中

- OpenAI-compatible/vLLM HTTP adapter 和轻量 Worker Agent。
- 显式 tokenizer modes：mock、disabled 和 remote placeholder。
- 第一次 smoke 使用一张已授权空闲 GPU 上手动启动的 vLLM instance。
- ShadowEstimated real-backend affinity 与 MockExact cache events 分开。
- Registry metadata 已为 static multi-instance expansion 留接口，但不做自动化。

## Stage 4：shared-resource elasticity，计划中

- Node Agent、GPU Observer、Cooperative Lease、Resource Policy 和 Interference Guard。
- Elasticity Manager，包含 idempotent start、drain、stop、release verification，以及 DistServe-owned Workers 的 cooldown。
- 真实设备集成前先做 Mock observation 和 sanitized Trace Replay。
- 用于 request scoring 的 immutable resource-stability features；Scheduler 永远不调用 NVML 或 `nvidia-smi`。
- Dynamic contraction/expansion、reclaim-risk 和 interference experiments。

Automatic mode 会保持 disabled，除非配置了 allowed GPU set 和 valid Lease。Kubernetes、Slurm、multi-node placement 和 general-purpose cluster scheduling 都不属于这个阶段。

## Stage 5：可选的单节点 prefill/decode separation

用显式 local KV transfer 和 failure semantics 评估 1P+3D、2P+2D、3P+1D 的聚合 Worker 方案。本阶段不声称 cross-node RDMA。

可能的后续工作包括 authenticated control APIs、durable state、fairness 和 quotas。这些目前都不是已有能力。
