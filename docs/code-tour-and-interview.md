# 面向 C++ 开发者的代码导览

从 `cmd/controller/main.go` 开始看：flags 构造具体的 Registry 和 Scheduler，root signal context 拥有 sweeper 生命周期，一个 `http.ServeMux` 同时组合 control API 和 data API。请求进入 `gateway.chatCompletions` 后会被校验和 admission，然后变成 `scheduler.RequestMeta`。Scheduler 读取 deep-copied Registry snapshot；`Registry.Reserve` 会在 write lock 下重新验证 `(ID, InstanceID, Healthy, capacity)`。第二次 commit 可以避免大量请求使用同一个 stale heartbeat。

Gateway 让 `internal/backend` 用 deadline context 创建 Worker HTTP request。Mock workers 接收带内部 cache hint 的 transport shape；`backend_type: vllm` workers 接收普通 OpenAI-compatible JSON。任何 response 写出前，connection/503 retry 可以释放旧 reservation 并重新选择。Status 200 被接受后，SSE lines 会被立即 copy 和 flush；第一个 data event 记录 TTFT。Deferred cleanup 会在 normal、timeout、cancellation 和 error paths 上释放 admission、reservation、response body、metrics gauges 和 lifecycle state。

`cmd/mockworker/main.go` 创建唯一 process instance 并启动 registration。`registration.go` 在 heartbeat failure 后重新注册，并在 root context 结束时退出。`worker.go` 先尝试 running semaphore，否则进入有界 queue semaphore。它用 input length 模拟 prefill，用 active concurrency 模拟 decode，并用 mutex 保护 seeded random generator。Request context cancellation 会中断每个 timer。

`cmd/workeragent/main.go` 是 Stage 3 真实 backend 的伴随进程。它不运行模型推理；它注册一个已经运行的 vLLM OpenAI server，health-check 它，采集 optional normalized metrics，并在 healthy 时 heartbeat。每次启动都会得到新的 `instance_id`，因此旧 shadow affinity 和 stale heartbeats 不能描述新进程。

Metrics 在 `telemetry.Metrics` 中收集；request history 是 `lifecycle.Store` 中有界、mutex-backed ring。故障发现发生在 `Registry.RunSweeper`。Unit tests 覆盖各个 state owner；`tests/integration` 覆盖三个 Workers、round-robin spread、TTL removal 和 zero reservations。Phase 2 cache routing 应把 cache summary fields 加到 snapshots，并实现另一个 Scheduler，而不是把 cache logic 放进 Gateway。

## 面试问题和回答要点

1. **为什么 selection 和 reservation 分开？** Snapshots 会 stale；atomic commit 会验证 identity、health 和 capacity，并计入 concurrent assignments。
2. **这里有什么 thundering herd？** 很多 arrivals 可能在下一次 heartbeat 前看到同一个低 reported load；local reservations 会立刻提高 effective load。
3. **为什么需要 Instance ID？** Stable ID 表示 placement；Instance ID 用来隔离上一个 process incarnation 的 delayed messages。
4. **为什么用 RWMutex 而不是 concurrent maps？** Worker updates 跨多个字段和 state transitions，需要原子观察；一个锁让 invariants 更清晰。
5. **为什么要拷贝 snapshots？** Scheduler 不能在 scoring/network work 期间持有 Registry locks，也不能修改 Registry 拥有的 slices。
6. **Cancellation 如何传播？** Client request context 是 Gateway timeout context 的 parent；该 context 附到 upstream HTTP request 和 Worker timers 上。
7. **为什么调用 SSE Flush？** `Write` 可能 buffer；flush 让 first-token latency 可观测，并防止 intermediaries 把 tokens 合并。
8. **什么时候 retry 是安全的？** 只在 response start 前，只针对 transport failure 或 503，只在原始 deadline 内，并且最多一次。
9. **为什么不 retry partial streams？** Client 可能收到重复 tokens，而且 HTTP headers/status 已经不能安全修改。
10. **Backpressure 如何表示？** Controller admission channel、Worker running channel，以及单独的有界 queue channel；所有 acquisition 都有 release path。
11. **如何防止 reservation leak？** 使用 idempotent `sync.Once` release closure；commit 后 defer，并在 retry 前显式调用。
12. **Worker restart 时发生什么？** 新 registration 以 0 reservations 替换 instance；旧 heartbeats/releases 不能修改它。
13. **为什么 least-loaded 需要 tie-breaking？** 如果没有 tie-break，相同 score 会永久偏向字典序第一个 Worker；mutex-protected rotating index 会分散 ties。
14. **Race testing 覆盖什么？** 并发请求下的 Registry reads/writes、scheduler counters、Worker atomics/random source、metrics 和 lifecycle ring。
15. **为什么 Controller 是模块化单体？** Phase 1 的 state 和 failure boundaries 在没有 distributed coordination 时更容易检查；模块仍然保留独立 owner。
16. **哪些 metrics 避免 high cardinality？** Request IDs、prompts 和 addresses 都不作为 labels；Worker ID/state 是有界 experimental dimensions。
17. **为什么先一张 GPU 一个 vLLM？** 这样 failure、cache affinity、process lifetime 和未来 resource release 都绑定到一个明确 Worker instance。
18. **为什么走 HTTP 而不是 import vLLM？** Controller 保持 Go control plane；vLLM 是外部 OpenAI-compatible server，拥有自己的 runtime 和 GPU memory。
19. **什么是 ShadowEstimated cache affinity？** 一个短生命周期的真实 backend routing hint，不是 exact KV block residency 的证据。
20. **生产化还缺什么？** Authentication、durable Registry、TLS、更丰富 histograms、distributed coordination、automatic resource control 和 overload tuning。
21. **KV-aware scheduling 放在哪里？** 把 cache features 加到 immutable snapshots 和 request metadata，再实现新的 Scheduler；reservation semantics 保持不变。
