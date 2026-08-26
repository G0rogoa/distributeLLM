# Stage 4 benchmark plan

This is a plan, not a result report. Mock snapshots and sanitized Trace Replay precede
real runs. Real runs use only explicitly allowed GPUs under a valid Lease and an
authorized fixed window; no experiment injects load into another user's task.

## Experiments

1. Static scale, `1→2→3→4→5` Workers: throughput, per-GPU throughput, scaling
   efficiency, TTFT, TPOT, P95/P99, cache-hit ratio, and scheduling overhead.
2. Contraction, `5→4→3→2`: drain and GPU-release time, cancellations, TTFT/P99 spike,
   cache loss, reservation leaks, and Worker-set convergence.
3. Expansion, `1→3→5`: model-load and registration time, cache warm-up, time to
   throughput gain, load oscillation, and progressive traffic-ramp rate.
4. Reclaim-risk routing: compare prefix-aware with prefix-plus-risk-aware scheduling;
   report long-request reclaim ratio, cancellations, TTFT, locality, and opportunistic
   Worker utilization.
5. Interference protection: compare `NoGuard`, `ThresholdOnly`, `Hysteresis`, and
   `LeaseAndHysteresis`; report obtained GPU-hours, authorized background-workload
   impact, reclaim response, false starts, ineffective load time, and violations.

For resource traces, also compare the aggressive and full Lease/hysteresis/risk
policies defined in `resource-trace-format.md`. Request-level baselines retain uniform,
mixed-length, Worker failure, cold/hot/Zipf-like prefixes, eviction, and cache-aware
comparisons.

Each case uses the same model/configuration and request corpus, fixed seeds where
possible, warm-up rules, at least three repetitions, and separate raw results. Record
GPU and host configuration, software revision, policy/Lease settings, event timeline,
observer freshness, model-load overlap, and score breakdown. Never invent or combine
results across hardware. Define scaling efficiency relative to the measured one-Worker
baseline and publish uncertainty rather than only the best run.
