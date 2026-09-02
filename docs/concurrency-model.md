# 并发模型

标准 HTTP server 会为每个请求创建一个 goroutine。DistServe 在 Milestone 1 不额外创建 per-request goroutine。

- Gateway request goroutine：由 `net/http` 创建；在 completion、upstream error、deadline 或 client cancellation 后退出。派生的 timeout context 通过 deferred call 取消。它拥有 upstream response body，并用 `defer` 关闭。
- Mock worker request goroutine：由 `net/http` 创建；在生成结束或 `Request.Context()` done 时退出。它拥有自己的 timer，并在返回前停止 timer。
- Server shutdown goroutine：由每个 command 的 `main` 创建；signal cancellation 触发有界 `http.Server.Shutdown`，之后退出。

Gateway 没有 request-shared mutable state。Mock worker 的 active-request counter 是 atomic，并通过 `defer` 递减，因此 cancellation 不会泄漏 slot。Contexts 都是 request-scoped，不会存进长生命周期结构。

Milestone 2 的 Registry 使用一个 `sync.RWMutex`：写锁保护 registration、heartbeat、drain 和 health transitions；读锁下拷贝 snapshots，释放后再交给 caller 使用。Health sweeper 由 controller `main` 创建，观察 controller root context，并在 context cancelled 后退出。Worker registration loop 由 worker `main` 创建；ticker 在返回时停止，所有 HTTP calls 都携带同一个 root context。

重要 Registry invariants：

- 一个 map entry 只表示某个稳定 worker ID 的当前一个 instance。
- Heartbeat 必须匹配该 instance ID；来自旧进程的延迟 heartbeat 不能更新 load 或 health。
- 有效 heartbeat 会把非 draining worker 转为 Healthy。
- Sweep transitions 随 heartbeat age 单调变化：Healthy/Starting -> Suspect -> Unavailable。之后的有效 heartbeat 可以恢复。
- Snapshot model slices 会被拷贝，因此 caller 不能修改 Registry 拥有的内存。

Controller admission limiter 是一个有界 channel。成功 send 就拥有一个 slot；Gateway 会立即 defer 对应 receive。Registry reservations 使用 Registry mutex，并返回 idempotent release closure。它的 invariant 是：每个 reservation 都属于当前 `(worker ID, instance ID)`，且永不为负。Replacement 从 0 reservations 开始；旧 instance 的 late release 不能修改 replacement。

Round-robin 使用 atomic counter。Least-loaded 只用一个小 mutex 保护 tie-break counter；它只处理 caller-owned snapshots。Mock Worker capacity 和 queue channels 是 semaphores。Request goroutines 在等待时 select 自己的 context，deferred cleanup 会归还已获得的 running slot。Pseudo-random generator 用自己的 mutex 保护，因为 `math/rand.Rand` 不是 concurrency-safe。

Telemetry counters 是 atomic；floating-point observation sums 和 lifecycle ring buffer 使用独立 mutex。持有 Registry lock 时不会执行网络 I/O 或 logging。

Phase 2 prompt building、mock tokenization、block construction 和 prefix hashing 都是 request-local pure operations。它们拥有返回的 slices，不获取 shared locks。Tokenization/prompt building 之前和过程中都会检查 context cancellation。未来 Cache Index 代码不能在持有 Registry 或 Cache Index lock 时运行这些可能线性耗时的操作。

Cache Index 用一个 `sync.RWMutex` 保护 index 的两个方向、Worker instance/view state、sequence numbers、entry count 和有界 EventID deduper。两个方向都在一个 write-lock critical section 内更新。Queries 在 read lock 下拷贝 entries，解锁后排序和截断。Lock rules：

1. 不要同时持有 Registry 和 Cache Index locks。
2. 持有 Cache Index lock 时不要 tokenize、hash、log 或执行 HTTP。
3. Worker instance changes 和 Cache events 必须通过显式顺序方法应用。
4. Cleanup goroutine 由 owner 创建，退出时停止 ticker，并在 context cancelled 时退出；每轮 cleanup 只移除有界 batch。

Mock Cache mutex 覆盖它的 map、LRU list、counters 和 event sequence。Events 只会在解锁后通过有界 non-blocking channel 发布。Loss 会被计数；后续 sequence gap 会 degrade Controller view。Fill reservations 使用单独 mutex 和 idempotent release closure。它们的 cleanup goroutine 会在 Controller context 结束时退出。Registry、CacheIndex、MockCache 和 fill locks 永不一起持有。

Stage 3 增加两个小循环。`workeragent.Run` 在 command goroutine 中执行：register，使用 timeout context health-check vLLM，仅在 healthy 时发送 heartbeat，然后等待 ticker 或 root context。Heartbeat failure 使用有界 exponential backoff，每次 sleep 都 select context cancellation。Controller 拥有一个 shadow-affinity cleanup goroutine，使用 ticker，并在 Controller root context 结束时退出。

真实 backend HTTP calls 使用 Gateway 提供的 request context。Backend adapter 不会在网络 I/O 时持有 Registry 或 Cache locks。SSE stream 一旦有 bytes 写给 client，就绝不重试。

## 计划中的 Stage 4 循环和归属

这些 goroutines 是设计契约，不是当前实现：

- GPU Observer loop 周期性发布 immutable snapshot，停止 ticker，并在 Node Agent root-context cancellation 时退出。失败表示 stale/unknown，绝不表示可以 claim GPU。
- Lease refresh loop 在 TTL 到期前刷新，并在 root cancellation 时退出。Expiry 或 corrupt input 会 fail-closed，并触发 reclaim evaluation。
- Resource evaluation loop 以有界 cadence 读取拷贝出的 observer、Lease、demand 和 interference snapshots，并发出 explainable、versioned decisions。
- Process watcher 只拥有 DistServe-created Workers 的 handles，并在 child exit 或 shutdown cancels watch 后退出。它不能 adopt 或 signal foreign PIDs。
- Drain timer 属于某个 Worker instance，在 work 归零或 grace context 到期时结束；它只 cancel 该 instance 的 DistServe requests。

Resource State Store 会用一个 mutex 保护 GPU/Worker state、decision version 和 owned-process identity。所有 external work 都在解锁后执行：调用 NVML 或 `nvidia-smi`、start/stop process、等待 child、执行 HTTP、logging 或获取 Registry/Cache Index locks 时，都不能持有该锁。Snapshots 在锁下拷贝。Observer publication 不能无限等待 evaluation。

Start、drain、stop 和 cache invalidation 都是 idempotent，并以 GPU UUID 加 Worker instance 为 key。Shutdown 会 disable claims，把 owned Workers 在 Registry 中标为 Draining，停止 new admission，在 grace period 内等待，cancel remaining owned requests，停止 owned processes，使 Cache Index instances 失效，验证 GPU release，进入 cooldown，并停止 loops。Reservations 和 Fill Reservations 在所有 exit path 上都保留 release。Restart reconciliation 是保守的，绝不从 PID 或 GPU usage 单独推断 ownership。
