# Resource Trace format (planned Stage 4)

Trace Replay tests policy before real shared-server automation. A CSV row represents
one device observation at an RFC 3339 timestamp:

```text
timestamp,gpu_uuid,device_index,memory_used_bytes,gpu_utilization_percent,foreign_process_count,lease_active,host_memory_available_bytes,host_swap_used_bytes,disk_read_bytes_per_second,disk_write_bytes_per_second
```

Required rules are nonnegative byte/count fields, utilization in `[0,100]`, stable GPU
UUID/index mapping within a trace, strictly nondecreasing timestamps per GPU, explicit
boolean Lease state, and documented sample interval. Missing, malformed, or stale rows
are unknown/unavailable, never idle.

Traces must not contain usernames, process commands, file paths, prompts, model
contents, account data, addresses, or credentials. GPU UUIDs may be consistently
pseudonymized when hardware identity is unnecessary.

Replay compares `Aggressive`, `ThresholdOnly`, `Hysteresis`,
`LeaseAndHysteresis`, and `LeaseHysteresisAndReclaimRisk` with identical demand and
trace inputs. Report usable GPU-hours, starts, ineffective loads, quick reclaims,
reclaim latency, completed/cancelled requests, cache warm-up loss, transition count,
and interference violations. Preserve configuration, seed, trace hash, policy decision
log, and code revision. Replay results do not prove safe real-world thresholds.

