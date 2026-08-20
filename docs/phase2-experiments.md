# Phase 2 experiment report

No performance result is committed because it must be measured on the target machine.
Use `experiments/cache-aware.sh MODE OUTPUT`, and retain Controller/Worker flags and the
Git commit with JSON/CSV output. Compare at least three repeated runs.

The six modes are cold, hot shared-prefix, deterministic Zipf approximation, capacity
pressure, event staleness, and block-size sweep. Capacity experiments lower Worker
cache bytes; staleness experiments delay or drop event delivery; block-size experiments
restart all processes for every size. The current load generator cannot synthesize an
exact Zipf corpus, so that mode must not be reported as a measured Zipf exponent.

Report throughput, success rate, latency/TTFT percentiles, predicted and actual hit
tokens, prediction misses, evictions, cache bytes, and view states. Metadata hit ratio,
actual Worker hit ratio, and latency improvement are different quantities.
