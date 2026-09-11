# ECT scheduler

Stage 3D 的 `ect` 使用统一毫秒单位：

```text
ECT(worker, request) =
    queue_delay_ms
  + prefill_fixed_ms
  + uncached_tokens * prefill_ms_per_token
  + decode_fixed_ms
  + expected_output_tokens * decode_ms_per_token
  + instability_penalty_ms
  + reclaim_risk_penalty_ms
```

当前 `instability_penalty_ms` 来自 stale/degraded cache metadata penalty。`reclaim_risk_penalty_ms` 预留给 Stage 4，本阶段为 0。

## Shadow affinity confidence

真实 vLLM 路径没有精确 per-prefix KV residency。`ShadowEstimated` 只说明某个 prompt 最近成功经过某个 worker instance，因此不能像 `MockExact` 一样完全扣除 matched tokens。

```text
confidence_adjusted_cached_tokens =
  shadow_matched_tokens * shadow_confidence

uncached_tokens =
  total_prompt_tokens - confidence_adjusted_cached_tokens
```

例如 shadow match 为 4096 tokens、confidence 为 0.5，则 ECT 只按 2048 cached tokens 计算。这个保守折扣避免弱一致 metadata 造成过度粘滞。

## Expected output

调度器优先使用外部 workload/profile 提供的历史输出 token 估计；没有时使用 `max_tokens`。Decision 会记录：

- `expected_output_tokens`
- `expected_output_source`

正式对照实验应冻结 cost profile，避免 ECT 在实验过程中学习而 RR/least-loaded 不学习。

Stage 3E 正式实验使用 `-online-cost-learning=false`。OnlineStore 在开启时只收集 EWMA 观测，当前 scheduler 不读取它，因此不能称为 adaptive ECT；`-online-cost-alpha` 和 `-online-cost-min-samples` 只控制观测存储。若未来让在线估计参与 decision，必须增加 instance 隔离和明确的 offline/online debug source。

缓存 worker A 仅在 `QueueA + RemainingPrefillA < QueueB + FullPrefillB` 时胜出。等号仍进入普通 tie-break；队列代价超过缓存节省后选择 B，不增加额外权重。

## Debug fields

`/internal/debug/decisions` 的每个 candidate 会包含：

- `queue_delay_ms`
- `prefill_fixed_ms`
- `decode_fixed_ms`
- `adjusted_cached_tokens`
- `uncached_tokens`
- `shadow_confidence`
- `cost_profile_source`
- `running_source`
- `waiting_source`
- `expected_output_source`

这些字段用于解释“等待已有 cache”与“去空闲卡重新 prefill”的切换点。
