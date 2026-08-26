# Resource policy (planned Stage 4)

Resource Policy converts immutable GPU, Lease, host-pressure, Worker-demand, and cache
value snapshots into auditable decisions. It does not observe devices or manage
processes itself.

## States and transitions

```text
Unavailable -> Observed -> Borrowable -> Claimed -> Reclaiming -> CoolingDown
                     ^                                      |              |
                     +--------------------------------------+--------------+
```

- `Unavailable`: outside the allowed set, no valid Lease, foreign compute present, or
  unhealthy device.
- `Observed`: monitored only; automatic start is forbidden.
- `Borrowable`: valid authorization plus a continuously safe observation window.
- `Claimed`: a DistServe-owned Worker uses the GPU.
- `Reclaiming`: Lease expiry, foreign process, pressure, or administrator signal is
  causing that Worker to drain and exit.
- `CoolingDown`: temporary no-start interval after release to prevent oscillation.

Policy is slow to enter and fast to exit. A momentary idle sample is insufficient.
Any reclaim trigger overrides start value. Decisions use `Observe`, `StartWorker`,
`KeepWorker`, `DrainWorker`, `StopWorker`, or `Cooldown`, with reason, score breakdown,
decision time, validity, and input versions.

## Planned read-only models

The Observer will publish aggregate snapshots; `ForeignProcessCount` records only the
presence/count of non-DistServe compute processes, never their sensitive details.

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

Production decisions will also retain a structured score breakdown and source
versions. Request scheduling receives `ResourceStability` as copied features alongside
cache/load features. Its planned score subtracts load, queue, staleness, and reclaim
risk from cache benefit. Draining Workers are ineligible; near-expiry Workers avoid
long requests; stable Workers retain hot prefixes; opportunistic Workers avoid costly
warm-up. Admission contracts as the healthy Worker set shrinks and new Workers receive
traffic gradually. No Scheduler interface receives an Observer or device-command
capability.

## Planned safe configuration

This schema is not implemented yet and must not be passed to the current binaries:

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

Startup validation will reject enabled policy without a nonempty allowed set and valid
Lease, duplicate devices, invalid durations, contradictory thresholds, or limits above
the allowed set. Thresholds are experimental starting points, not measured optima.

Worker start is valuable only when expected safe lifetime exceeds model load, cache
warm-up, and minimum useful service time. The explainable score separates demand and
cache value from load, warm-up, reclaim, and interference costs. Initial policy uses
explicit heuristics tuned through Trace Replay, not an opaque predictor.
