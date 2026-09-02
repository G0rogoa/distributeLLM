# Stage 4 benchmark 计划

这是计划，不是结果报告。Mock snapshots 和 sanitized Trace Replay 要先于真实运行。真实运行只使用有效 Lease 下明确 allowed 的 GPUs，并且必须在授权 fixed window 内执行；任何实验都不会向其他用户任务注入 load。

## 实验

1. Static scale，`1->2->3->4->5` Workers：throughput、per-GPU throughput、scaling efficiency、TTFT、TPOT、P95/P99、cache-hit ratio 和 scheduling overhead。
2. Contraction，`5->4->3->2`：drain 和 GPU-release time、cancellations、TTFT/P99 spike、cache loss、reservation leaks 和 Worker-set convergence。
3. Expansion，`1->3->5`：model-load 和 registration time、cache warm-up、time to throughput gain、load oscillation 和 progressive traffic-ramp rate。
4. Reclaim-risk routing：比较 prefix-aware 与 prefix-plus-risk-aware scheduling；报告 long-request reclaim ratio、cancellations、TTFT、locality 和 opportunistic Worker utilization。
5. Interference protection：比较 `NoGuard`、`ThresholdOnly`、`Hysteresis` 和 `LeaseAndHysteresis`；报告 obtained GPU-hours、authorized background-workload impact、reclaim response、false starts、ineffective load time 和 violations。

对于 resource traces，还要比较 `resource-trace-format.md` 中定义的 aggressive policy 和完整 Lease/hysteresis/risk policy。Request-level baselines 保留 uniform、mixed-length、Worker failure、cold/hot/Zipf-like prefixes、eviction 和 cache-aware comparisons。

每个 case 使用同一个 model/configuration 和 request corpus，尽可能固定 seeds，采用一致 warm-up rules，至少三次重复，并分开保留 raw results。记录 GPU 和 host configuration、software revision、policy/Lease settings、event timeline、observer freshness、model-load overlap 和 score breakdown。不要编造结果，也不要混合不同硬件上的结果。Scaling efficiency 相对已测量的 one-Worker baseline 定义，并发布 uncertainty，而不是只发布 best run。
