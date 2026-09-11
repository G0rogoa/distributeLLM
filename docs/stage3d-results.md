# Stage 3D baseline and Stage 3E result protocol

Stage 3D validated calibrated cost estimation, load-aware routing, experiment automation and congestion avoidance. It did not validate longest-prefix cache locality because the first Shadow Affinity implementation indexed only the terminal full-prompt prefix hash.

现存 Stage 3D 原始 artifacts 保留为只读 baseline。它们可以支持 Prefill/Decode 校准、RR/least-loaded 负载基线、并发扩展、拥塞避让和实验框架验证。它们不能支持长公共 prefix 命中、KV-aware 显著加速、精确 KV residency 或 cache/load 切换点的结论。审计发现这些 run 来自 dirty worktree/旧 commit，manifest 多项缺失，worker samples 为空，因此不能直接作为正式 Stage 3E 结果。

Stage 3E 修复为 longest-full-prefix shadow directory，并要求由 `python3 tools/analyze_experiment.py --matrix artifacts/stage3e-... --output experiment-summaries/stage3e` 自动生成所有正式表格。真实 GPU 数据尚未在本轮运行；硬件、软件、置信区间、性能对比和切换曲线必须等获批的双卡实验后由生成结果补入，不手工选择单次最好结果。

Stage 3D 结果必须分 workload 报告，不能混成一个总平均数：

```text
Unique
Shared-prefix
Mixed
Congestion
```

每组至少报告：

- 请求数和成功率；
- throughput；
- TTFT p50/p90/p95/p99；
- total latency p50/p90/p95/p99；
- TPOT p50/p95；
- 每 worker 请求分布；
- Jain fairness index；
- tokenizer fallback rate；
- shadow affinity match rate；
- 估计缓存 token 比例；
- vLLM aggregate prefix hit delta；
- admission rejection；
- retry；
- 三次重复均值和标准差；
- 样本足够时的 95% bootstrap confidence interval；
- 相对 round-robin 和 least-loaded 的提升。

如果样本量不足，报告“样本不足以给出可靠置信区间”，不要输出伪精确结论。

## Correctness checks

分析工具和人工复盘需要覆盖：

- RR 分布基本均衡；
- least-loaded 避开拥塞 worker；
- ECT 无 affinity 时接近 least-loaded；
- ECT 在长 prefix 且负载接近时偏向缓存 worker；
- ECT 在缓存 worker 严重拥塞时切换到空闲 worker；
- worker 重启后旧 affinity 不参与选择；
- TTL 过期后不参与选择；
- tokenizer 降级时 ECT 退化为负载调度；
- 所有 reservation 最终回到 0；
- 所有实验请求能关联 decision；
- decision worker 与响应头一致。
