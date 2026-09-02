# Phase 2 实验报告

这里不提交 performance result，因为结果必须在目标机器上测量。使用 `experiments/cache-aware.sh MODE OUTPUT`，并保留 Controller/Worker flags、Git commit、JSON/CSV output。至少比较三次重复运行。

六种模式分别是 cold、hot shared-prefix、deterministic Zipf approximation、capacity pressure、event staleness 和 block-size sweep。Capacity experiments 会降低 Worker cache bytes；staleness experiments 会延迟或丢弃 event delivery；block-size experiments 每个 size 都会重启所有进程。当前 load generator 不能合成精确 Zipf corpus，因此该模式不能报告为已测量的 Zipf exponent。

报告 throughput、success rate、latency/TTFT percentiles、predicted 和 actual hit tokens、prediction misses、evictions、cache bytes 和 view states。Metadata hit ratio、actual Worker hit ratio 和 latency improvement 是三个不同量。
