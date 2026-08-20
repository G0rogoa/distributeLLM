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
