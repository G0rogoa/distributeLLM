# Benchmark 方法

构建同一个 image/configuration，保持 Worker seeds 固定，先 warm up 系统，然后每个 case 至少运行三次。保留 loadgen summary、per-request JSONL、`/metrics`、`/internal/debug/decisions`、命令行、Go version、机器信息、scheduler 和 Worker configuration。不要混合不同硬件上的结果。

- Experiment A：`experiments/uniform.sh`；分别用 `-scheduler=round-robin` 和 `least-loaded` 跑一次。Uniform 64-token input/output 应该得到相近结果。
- Experiment B：`experiments/mixed.sh`；生成 80/20 short/long split。比较 throughput、latency percentiles、TTFT、TPOT 和 worker selections。
- Experiment C：`experiments/failure.sh`；在 60 秒运行期间停止一个 Worker。记录 stop time、Suspect/Unavailable time、errors、retries、P99 spike 和 recovery。

脚本有意不包含任何 claimed results，因为当前环境没有 Docker。固定 seeds 让模拟 timing/failures 可复现，但 OS scheduling 仍会引入正常测量波动。当前 arrival models 是 fixed concurrency、fixed rate 和 bursts；Poisson arrivals 是未来工作。

Phase 2 request experiments 也覆盖 cold cache、hot shared prefixes、deterministic Zipf-like input、eviction pressure、metadata staleness 和 block-size sweeps。Predicted cache-hit tokens 和 actual cache-hit tokens 必须分开报告。

Stage 3 和 4 的测量只使用单节点 5xA100 80GB 目标环境。Static scaling 使用 1、2、3、4、5 个 Workers，并报告 throughput、per-GPU throughput、scaling efficiency、TTFT、TPOT、P95/P99、cache-hit ratio、worker selection 分布和 scheduler overhead。Stage 3B 的 static multi-vLLM 实验必须先手动确认每张候选 GPU 空闲，再手动启动一个 vLLM server 和一个 `workeragent`；Controller/loadgen 只连接这些已经存在的 loopback 服务。Dynamic 与 interference experiments 在 `stage4-benchmark-plan.md` 中说明。

Stage 3C 的 cache-aware 对照必须至少包含单卡 baseline、双卡 round-robin、双卡 `ect`。Workload 需要固定 JSONL，包含 `group`、`prompt` 或 `input_tokens`、`output_tokens`，并覆盖 hot-prefix 与 cold/uniform 请求。每个 case 至少重复三次，保存 per-request JSONL、summary、`/metrics`、`/internal/debug/decisions`、Controller/agent/vLLM logs 和前后 `nvidia-smi`。只有当 tokenizer fallback 率、worker selection、TTFT/TPOT/latency 和 vLLM load 都被记录后，才可以讨论性能优化收益。

Resource-policy tuning 从 sanitized Trace Replay 开始。真实实验需要显式 allowed GPU set、有效 Lease 和授权 fixed window。不要向其他用户的 live workload 注入 pressure。记录 hardware/software configuration、policy values、Lease mode、observation freshness、decision score breakdowns 和 raw outputs；计划默认值不是已经验证的最优值。
