# 故障模型

- Client cancellation 会通过 Gateway HTTP transport 传播到 Worker token generation。Admission 和 reservation 会通过 deferred cleanup 释放。
- 如果 response 尚未开始，Gateway deadline 会返回 504，并取消 upstream work。
- 启用 `-retry` 时，connection failure 或 Worker 503 可以重试一次。重新调度前会释放第一次 reservation。200 response 或任何已经 streamed 的内容都不会被透明重试。
- 漏掉 heartbeat 后，5 秒变为 Suspect，10 秒变为 Unavailable。Schedulers 只选择 Healthy instances。匹配 instance 的有效 heartbeat 可以恢复。
- 重启的 Worker 会替换旧 instance；延迟到达的旧 heartbeat 会收到 409。
- Draining 会阻止新选择。当 reported running 和 queued work 都为 0 时，状态变为 Unavailable。
- Controller 和 Worker admission 都是有界的。Controller rejection 是 429；Worker queue rejection 是 503。

Phase 1 不解决 network partitions、durable state、authentication、exactly-once execution、distributed consensus，也不恢复内存中的 lifecycle history。

Phase 2 Cache Events 通过有界 EventID memory 实现 idempotent，并按每个 instance 的单调 Sequence 排序。旧 events 会被忽略；sequence gap 会 degrade cache view，但不会阻塞请求。Reset 会清空一个 instance。Worker restart 会移除旧 instance view，late old events 会被拒绝。Expired leases 在异步 cleanup 之前就会被 lookup 排除。Controller restart 会丢失 advisory index，因此从冷缓存开始，而不是声称 false cache hits。

Stage 3 真实 backend 增加 HTTP adapter boundary。Connection failures 和 timeouts 会与 backend 4xx/5xx responses 分开分类。Retry 仍限制在 response-start 前窗口；一旦 SSE stream 开始，Gateway 永远不会切换 Worker。停止或不健康的 `workeragent` 只会停止 heartbeat，Registry TTL transition 会让该 Worker unavailable。Shadow affinity records 会很快过期，并绑定到 `(worker_id, instance_id, cache identity)`，因此它们不声称真实 KV residency，也不会在新 Worker instance 中作为 schedulable truth 存活。

## 计划中的 Stage 4 resource failures

- Observer error 或 stale data 会 fail-closed：不会启动新 Worker。错误绝不证明 GPU idle。
- Lease expiry、refresh failure、corrupt data、version rollback 或 conflict 会阻止进入，并把 owned Worker 推向 Draining。
- Start 或 model-load failure 会移除未完成的 Registry instance，使它的 cache view 失效，记录 reason，并进入 backoff/cooldown。
- Stop timeout 只会针对 exact DistServe-owned process 升级处理。无法验证 exit 或 memory release 会让 GPU 保持 unavailable 并产生 audit event；不会 signal foreign process。
- Node Agent restart 会重建 observations 和 configured Leases，但不会从 device activity 推断 ownership。Controller restart 会让 advisory cache 冷启动，并重新对齐 Worker instance IDs。
- Duplicate 或 reordered decisions 会被 resource/instance version 拒绝；lifecycle operations 保持 idempotent。
- Reclaim 期间，client cancellation 会释放 request 和 cache-fill reservations。Draining 会在 cancellation 开始前阻止新选择。
- Foreign compute process、GPU pressure、host memory/swap pressure 或持续 I/O pressure 会停止 starts、收紧 admission，并触发 prioritized reclaim。

安全故障处理宁可损失 capacity，也不做未授权使用。Resource automation 不会 kill、pause、reprioritize 或检查其他用户进程的内容。

## Phase 2 cache failures

Cache metadata 只是性能 hint。Event loss、reordering、lease expiry、eviction 或 Worker restart 都可能导致 prediction miss；Worker 会执行 local lookup，并安全回退到 full prefill。Instance IDs 会拒绝来自已替换进程的 events。Sequence gaps 会 degrade view，expired views 不参与 match。有界 queues 和 indexes 倾向于可观测 rejection，而不是无界内存增长。
