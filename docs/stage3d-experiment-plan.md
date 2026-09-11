# Stage 3D experiment plan

Stage 3D 目标是校准 cache-aware scheduling，并用真实双 vLLM worker 做严谨对照。本阶段不实现跨 GPU KV transfer、prefill/decode 分离、自动 GPU 选择、自动 vLLM 生命周期或 Stage 4 弹性资源控制。

真实 GPU 实验必须在代码和无 GPU 测试完成后由用户确认。实验前检查 `nvidia-smi`；有任何其他用户进程的 GPU 不可用。Controller、tokenizer、workeragent 和 vLLM 都绑定 loopback。

## Matrix

核心对照：

```text
Schedulers: round-robin, least-loaded, ect
Workloads: unique, shared-prefix, mixed
Concurrency: 1, 4, 8, 16
Repetitions: 3
Requests per run: 100-300
```

Prefix 长度实验：

```text
Prefix tokens: 256, 1024, 2048, 4096, 8192
Schedulers: least-loaded, ect
Concurrency: 4, 8
Repetitions: 3
```

拥塞切换实验：

```text
Hot worker background concurrency: 0, 1, 2, 4, 8
Prefix tokens: 2048, 4096, 8192
Schedulers: least-loaded, ect
Repetitions: 3
```

如果完整矩阵超过两小时，先拆成：

- A：约 10 分钟正确性实验；
- B：30 到 60 分钟核心实验；
- C：可选完整矩阵。

## Cache state control

Cold 实验中，每个 scheduler 前重启本项目自己的 vLLM 或使用新的模型实例，确认 shadow affinity 为空，并保存 vLLM 初始指标。

Warm 实验中，先只让 Worker A 可调度，经 Controller 发送 warmup；确认 shadow affinity 只在 Worker A；再让 Worker B healthy。RR、least-loaded 和 ECT 必须使用相同缓存初态。RR 不读 shadow affinity，但其 vLLM cache 状态仍要一致。

## Workloads

`tools/generate_stage3d_workload.py` 生成三类 JSONL：

- `unique`：每个请求 prefix 不同；
- `shared`：相同长文档/system prefix，加不同 question suffix；
- `mixed`：40% shared-prefix、40% unique、20% 其他 prefix group。

所有 workload 固定 seed，并保留 prompt 文本在本地 artifact 中；日志和 metrics 不记录 prompt。
