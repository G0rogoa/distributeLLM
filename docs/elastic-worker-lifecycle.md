# Elastic Worker lifecycle (planned Stage 4)

```text
Observed --Lease + stable idle--> Borrowable --start--> Starting
Starting --registered + model ready--> Healthy --reclaim--> Draining
Draining --zero active or grace expiry--> Stopping --exit + release--> CoolingDown
CoolingDown --timer--> Observed
```

The Elasticity Manager starts only one Worker for a versioned GPU claim and limits
concurrent model loads. Registration must match the expected Worker and instance IDs;
readiness, not process creation, marks it Healthy. New Workers receive traffic
gradually to avoid a load surge.

On reclaim it first marks the instance Draining in Registry, so scheduling stops. It
then reduces admission, waits for existing requests, and after the grace period cancels
only requests belonging to that DistServe instance. Request and Cache Fill
Reservations release on every exit path.

Stop targets only the exact process handle created by DistServe. After exit, the
manager invalidates that Worker instance in Cache Index, verifies GPU memory release,
records an audit event, and enters cooldown. If exit or release cannot be verified, the
GPU remains unavailable and is never treated as free. The manager never signals,
pauses, reprioritizes, or adopts another user's process.

Start, drain, stop, invalidation, and release verification are idempotent. A Worker
instance version rejects stale or duplicate actions. Lease expiry, a foreign compute
process, GPU/host pressure, Worker failure, or administrator stop can move any active
state toward reclaim. No path skips cleanup merely because a timeout or cancellation
occurred.

