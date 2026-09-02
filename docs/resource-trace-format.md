# Resource Trace format（计划中的 Stage 4）

Trace Replay 会在真实共享服务器自动化之前测试 policy。CSV 的一行表示 RFC 3339 timestamp 上的一次 device observation：

```text
timestamp,gpu_uuid,device_index,memory_used_bytes,gpu_utilization_percent,foreign_process_count,lease_active,host_memory_available_bytes,host_swap_used_bytes,disk_read_bytes_per_second,disk_write_bytes_per_second
```

必要规则包括：byte/count 字段非负、utilization 在 `[0,100]` 内、同一 trace 中 GPU UUID/index mapping 稳定、每张 GPU 的 timestamps 严格非递减、显式 boolean Lease state，以及已文档化的 sample interval。Missing、malformed 或 stale rows 都表示 unknown/unavailable，绝不表示 idle。

Traces 不能包含 usernames、process commands、file paths、prompts、model contents、account data、addresses 或 credentials。GPU UUIDs 在不需要硬件身份时可以做一致 pseudonymization。

Replay 会用相同 demand 和 trace inputs 比较 `Aggressive`、`ThresholdOnly`、`Hysteresis`、`LeaseAndHysteresis` 和 `LeaseHysteresisAndReclaimRisk`。报告 usable GPU-hours、starts、ineffective loads、quick reclaims、reclaim latency、completed/cancelled requests、cache warm-up loss、transition count 和 interference violations。保留 configuration、seed、trace hash、policy decision log 和 code revision。Replay results 不能证明真实世界 thresholds 安全。
