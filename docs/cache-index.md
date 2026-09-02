# Cache index

Phase 2 的 Controller Cache Index 是内存中的 advisory 视图，用来描述 mock Worker cache locations。它不是 GPU memory，也不是绝对真相来源。Worker 之后必须在本地验证每一个 predicted hit；stale metadata 可能降低性能，但绝不能改变模型输出。

Index 在一个 `sync.RWMutex` 下拥有两张 map：

```text
byWorker[worker instance][cache key] -> entry
byPrefix[cache key][worker instance] -> the same entry
```

对于每个 `byWorker[w][k]`，`byPrefix[k][w]` 必须包含同一个 internal entry，反过来也一样。Add 会同时更新两个方向，Evict 会同时移除两个方向，Reset 会移除一个 instance 的所有 entry，Worker replacement 会先移除旧 instance 再创建新 view。`ValidateInvariants` 是 test/debug helper，不是 hot-path check。

External queries 返回拷贝后的 `CacheEntry` 值，不暴露内部 maps 或 pointers。Tokenization、hashing、HTTP、logging 和 Registry calls 都在 Cache Index lock 之外执行。

## 事件和顺序

Events 携带全局唯一 EventID，以及每个 instance 单调递增的 Sequence。重复 EventID 是 idempotent。Sequence 不大于 last applied sequence 的 event 会作为 out of order 被忽略。向前跳号的 gap 会被应用，但会把 cache view 标记为 Degraded；Reset 会清空 instance 并恢复 Ready。失败 event 不会进入 deduper，因此 capacity pressure 可以被修正，event 也可以重试。

EventID deduper 是有界 ring：超过容量时会忘记最旧 ID。这限制了内存使用，但也意味着很老的 duplicate 之后会交给 Sequence ordering 处理。

## Lease 和 cleanup

每个 entry 在 `ObservedAt + LeaseDuration` 过期。Queries 即使在物理 cleanup 之前也会忽略 expired entries。`CleanupExpired` 最多删除一个配置好的 batch；`RunCleanup` 拥有一个 ticker，并在 context cancelled 时退出。如果 Worker view 在配置的 view threshold 内没有收到 event，也可以报告 Stale。

Index 有硬性的最大 entry count。超过后返回 `ErrCacheIndexFull`，而不是无界增长；被拒绝的 events 仍可重试，并且不会推进 sequence。
