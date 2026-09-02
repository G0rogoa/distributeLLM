# Elastic Worker lifecycle（计划中的 Stage 4）

```text
Observed --Lease + stable idle--> Borrowable --start--> Starting
Starting --registered + model ready--> Healthy --reclaim--> Draining
Draining --zero active or grace expiry--> Stopping --exit + release--> CoolingDown
CoolingDown --timer--> Observed
```

Elasticity Manager 只会为一个 versioned GPU claim 启动一个 Worker，并限制 concurrent model loads。Registration 必须匹配预期的 Worker 和 instance IDs；标记 Healthy 的条件是 readiness，而不是 process creation。New Workers 会逐步接收 traffic，避免 load surge。

Reclaim 时，它会先在 Registry 中把 instance 标记为 Draining，让 scheduling 停止。然后降低 admission，等待 existing requests，并在 grace period 后只 cancel 属于该 DistServe instance 的 requests。Request 和 Cache Fill Reservations 会在每个 exit path 上释放。

Stop 只针对 DistServe 创建的 exact process handle。进程退出后，manager 会使该 Worker instance 在 Cache Index 中失效，验证 GPU memory release，记录 audit event，并进入 cooldown。如果 exit 或 release 无法验证，该 GPU 会保持 unavailable，绝不被视为 free。Manager 永不 signal、pause、reprioritize 或 adopt 其他用户的进程。

Start、drain、stop、invalidation 和 release verification 都是 idempotent。Worker instance version 会拒绝 stale 或 duplicate actions。Lease expiry、foreign compute process、GPU/host pressure、Worker failure 或 administrator stop 都可以把任何 active state 推向 reclaim。没有路径会因为 timeout 或 cancellation 而跳过 cleanup。
