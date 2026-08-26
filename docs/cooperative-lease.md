# Cooperative Lease (planned Stage 4)

A GPU Lease is group-recognized, temporary authorization; it is not ownership and it
does not override manual coordination. No automatic Worker starts without both a valid
Lease and an allowed GPU entry.

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

The first provider will be `ManualLeaseProvider`: explicit local configuration with a
required expiry. A future `FileLeaseProvider` is permitted only after the group agrees
on the shared protocol and permissions; it must not assume a public directory.

Validation requires a stable Lease ID, exact GPU UUID, recognized priority, increasing
version, creation before expiry, and bounded TTL. Expiry, unreadable/corrupt data,
version rollback, duplicate active owners, or conflicting GPU claims fail closed.
Refresh never silently extends authority. A higher-priority reclaim or expiry causes
Draining; it does not authorize interference with another process.

Lease snapshots expose only authorization metadata needed by policy. Audit records
cover issue/refresh/expiry/conflict and resulting decisions without credentials or
unnecessary personal/process information. Automatic mode remains disabled by default.

