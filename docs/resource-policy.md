# Resource policy（计划中的 Stage 4）

Resource Policy 会把 immutable GPU、Lease、host-pressure、Worker-demand 和 cache value snapshots 转换成可审计 decisions。它本身不观察设备，也不管理进程。

## 状态和转换

```text
Unavailable -> Observed -> Borrowable -> Claimed -> Reclaiming -> CoolingDown
                     ^                                      |              |
                     +--------------------------------------+--------------+
```

- `Unavailable`：不在 allowed set 中、没有有效 Lease、存在 foreign compute，或设备不健康。
- `Observed`：只监控；禁止自动 start。
- `Borrowable`：有效授权加上连续安全的 observation window。
- `Claimed`：DistServe-owned Worker 正在使用该 GPU。
- `Reclaiming`：Lease expiry、foreign process、pressure 或 administrator signal 正在促使该 Worker drain 并退出。
- `CoolingDown`：release 后的临时 no-start 间隔，用于避免 oscillation。

Policy 进入要慢，退出要快。一次瞬时 idle sample 不够。任何 reclaim trigger 都会覆盖 start value。Decisions 使用 `Observe`、`StartWorker`、`KeepWorker`、`DrainWorker`、`StopWorker` 或 `Cooldown`，并带上 reason、score breakdown、decision time、validity 和 input versions。

## 计划中的只读模型

Observer 会发布 aggregate snapshots；`ForeignProcessCount` 只记录非 DistServe compute process 的存在和数量，绝不记录敏感细节。

```go
type GPUSnapshot struct {
    GPUUUID             string
    DeviceIndex         int
    State               GPUState
    TotalMemoryBytes    int64
    UsedMemoryBytes     int64
    FreeMemoryBytes     int64
    UtilizationPercent  float64
    TemperatureCelsius  int
    PowerUsageWatts     float64
    ForeignProcessCount int
    DistServeProcesses  int
    ObservedAt          time.Time
}

type ResourceStability struct {
    Opportunistic    bool
    ReclaimRisk      float64
    LeaseExpiresAt   time.Time
    ExpectedLifetime time.Duration
    RecentReclaims   int
    ModelLoadCost    time.Duration
    CacheWarmth      float64
}

type ResourceDecision struct {
    GPUUUID    string
    Action     ResourceAction
    Reason     string
    Score      float64
    DecidedAt  time.Time
    ValidUntil time.Time
}
```

Production decisions 还会保留结构化 score breakdown 和 source versions。Request scheduling 会把 `ResourceStability` 作为拷贝后的 features，与 cache/load features 一起接收。计划中的 score 会从 cache benefit 中减去 load、queue、staleness 和 reclaim risk。Draining Workers 不可选；near-expiry Workers 避免长请求；stable Workers 保留 hot prefixes；opportunistic Workers 避免昂贵 warm-up。Healthy Worker set 缩小时 admission 会收缩，新 Workers 会逐步接收流量。任何 Scheduler interface 都不会拿到 Observer 或 device-command capability。

## 计划中的安全配置

这个 schema 尚未实现，不能传给当前 binaries：

```yaml
deployment:
  mode: shared_single_node
  expected_gpu_count: 5

resource_policy:
  enabled: false
  authorization_mode: manual
  allowed_gpu_indices: []
  observation_window: 15m
  enter_gpu_utilization_below: 5
  enter_memory_used_below_gb: 2
  reclaim_on_foreign_process: true
  reclaim_on_lease_expiry: true
  drain_grace_period: 30s
  shutdown_timeout: 60s
  cooldown_after_reclaim: 10m
  max_workers: 5
  max_concurrent_model_loads: 1
```

Startup validation 会拒绝以下配置：enabled policy 但没有非空 allowed set 和有效 Lease、duplicate devices、invalid durations、contradictory thresholds，或 limits 超过 allowed set。Thresholds 是实验起点，不是 measured optima。

只有当 expected safe lifetime 超过 model load、cache warm-up 和 minimum useful service time 时，启动 Worker 才有价值。Explainable score 会把 demand/cache value 与 load、warm-up、reclaim 和 interference costs 分开。初始 policy 使用通过 Trace Replay 调优的显式 heuristics，而不是 opaque predictor。
