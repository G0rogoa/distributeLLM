# Interference model (planned Stage 4)

“Low interference” means keeping impact on other research tasks below agreed
thresholds and releasing resources quickly and verifiably after a reclaim signal. It
does not mean hidden use.

The Interference Guard consumes aggregate, sanitized measurements:

- GPU compute utilization, memory use, temperature, power, and presence of a
  non-DistServe compute process;
- host CPU load, available memory, swap activity, and storage read/write rates;
- DistServe model-loading bandwidth, Worker load, and measured release time.

It does not collect usernames, full commands, environments, files, prompts, model
contents, tokens, or credentials. It never sends signals to foreign processes.

When pressure rises, the response order is: stop new starts, reduce admission capacity,
drain low-value/cold-cache Workers, then release additional DistServe GPUs if needed.
Foreign compute presence or Lease expiry is a fast reclaim trigger. Model loads are
serialized by default because they can stress CPU, memory, disk, and GPU concurrently.

Every violation includes threshold, observed aggregate, duration, input freshness,
action, and release latency. Thresholds remain configurable and must be calibrated by
Trace Replay and authorized experiments rather than claimed as universal optima.

