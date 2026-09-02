# Cooperative Lease（计划中的 Stage 4）

GPU Lease 是组内认可的临时授权；它不是 ownership，也不会覆盖人工协调。没有有效 Lease 和 allowed GPU entry 时，不会自动启动 Worker。

```go
type GPULease struct {
    LeaseID     string
    GPUUUID     string
    Owner       string
    Purpose     string
    Priority    LeasePriority
    CreatedAt   time.Time
    ExpiresAt   time.Time
    Reclaimable bool
    Version     uint64
}
```

第一个 provider 会是 `ManualLeaseProvider`：显式本地配置，并要求 expiry。未来可以加入 `FileLeaseProvider`，但必须先经过组内对共享协议和权限的确认；它不能假设存在 public directory。

Validation 要求 stable Lease ID、exact GPU UUID、recognized priority、递增 version、creation before expiry，以及有界 TTL。Expiry、unreadable/corrupt data、version rollback、duplicate active owners 或 conflicting GPU claims 都会 fail closed。Refresh 绝不静默延长授权。更高优先级 reclaim 或 expiry 会导致 Draining；它不授权干扰其他进程。

Lease snapshots 只暴露 policy 所需的授权 metadata。Audit records 覆盖 issue/refresh/expiry/conflict 以及 resulting decisions，不包含 credentials 或不必要的 personal/process information。Automatic mode 默认保持关闭。
