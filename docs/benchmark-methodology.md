# Benchmark methodology

Build one image/configuration, keep Worker seeds fixed, warm the system, and run each
case at least three times. Retain loadgen JSON, `/metrics`, command lines, Go version,
machine information, scheduler, and Worker configuration. Never combine results from
different hardware.

- Experiment A: `experiments/uniform.sh`; run once with `-scheduler=round-robin` and
  once with `least-loaded`. Uniform 64-token input/output should produce similar results.
- Experiment B: `experiments/mixed.sh`; it generates an 80/20 short/long split. Compare
  throughput, latency percentiles, TTFT, TPOT, and worker selections.
- Experiment C: `experiments/failure.sh`; stop one Worker during the 60-second run.
  Record stop time, Suspect/Unavailable time, errors, retries, P99 spike, and recovery.

The scripts intentionally contain no claimed results because Docker is unavailable in
the current environment. Fixed seeds make simulated timing/failures repeatable, but OS
scheduling still introduces normal measurement variation. The current arrival models
are fixed concurrency, fixed rate, and bursts; Poisson arrivals are future work.

Phase 2 request experiments also cover cold cache, hot shared prefixes,
deterministic Zipf-like input, eviction pressure, metadata staleness, and block-size
sweeps. Report predicted and actual cache-hit tokens separately.

Stage 3 and 4 measurements use the single-node 5×A100 80GB target only. Static scaling
uses 1, 2, 3, 4, and 5 Workers and reports throughput, per-GPU throughput, scaling
efficiency, TTFT, TPOT, P95/P99, cache-hit ratio, and scheduler overhead. Dynamic and
interference experiments are specified in `stage4-benchmark-plan.md`.

Resource-policy tuning starts with sanitized Trace Replay. Real experiments require an
explicit allowed GPU set, valid Lease, and authorized fixed window. Never inject
pressure into another user's live workload. Record hardware/software configuration,
policy values, Lease mode, observation freshness, decision score breakdowns, and raw
outputs; planned defaults are not validated optima.
