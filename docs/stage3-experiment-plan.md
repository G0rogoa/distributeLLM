# Stage 3 实验计划

Stage 3 验证一个真实 vLLM backend，同时保持 Stage 4 resource automation 不在范围内。第一次真实运行必须只使用一张明确授权的 GPU，并且这张 GPU 上不能有其他用户拥有的进程。

## No-GPU 验收

先在本地运行：

```bash
GOCACHE=/tmp/distserve-go-build go test ./...
GOCACHE=/tmp/distserve-go-build go vet ./...
GOCACHE=/tmp/distserve-go-build go test -race ./...
```

这些测试使用 Mock Workers 和 fake OpenAI-compatible `httptest.Server` backends。它们不能下载模型，也不能依赖 GPU。

## 真实单 GPU Smoke

真实运行前，先报告 exact GPU、model、预计显存使用量和预计时长，然后等待确认。最小真实序列是：

```bash
nvidia-smi
GPU_INDEX=0 MODEL=example-model PORT=8100 scripts/run-vllm-single-gpu.sh
go run ./cmd/controller -listen=127.0.0.1:8080 -model=example-model -tokenizer-mode=disabled
go run ./cmd/workeragent -worker-id=worker-gpu0 -gpu-index=0 -model=example-model -backend-url=http://127.0.0.1:8100
MODEL=example-model scripts/smoke-real-backend.sh
nvidia-smi
```

需要记录：

- 前后的 `nvidia-smi` snapshots；
- vLLM、PyTorch、CUDA、model 和 tokenizer versions；
- GPU index 和 loopback ports；
- Controller 和 Agent command lines；
- 一次 non-streaming response；
- 一次 streaming SSE response；
- Controller-observed TTFT 和 total latency；
- Agent stop 和 Registry TTL expiry 行为；
- project processes 已退出的确认。

不要从这个 smoke test 报告 throughput、cache hit rate、scaling efficiency 或 multi-GPU 结论。

## Stage 3C 双卡 Cache-aware 对照

真实 cache-aware 实验必须先完成 no-GPU 测试，然后人工确认两张候选 GPU 都没有其他用户进程。实验只使用手动启动的 vLLM servers、tokenizer sidecar、Controller 和 workeragents；不启动 Stage 4 自动资源层。

最小 case：

- 单卡 baseline：一个 vLLM Worker，`-scheduler=round-robin`。
- 双卡 baseline：两个 vLLM Workers，`-scheduler=round-robin`。
- 双卡 cache-aware：两个 vLLM Workers，`-scheduler=ect`，`-tokenizer-mode=remote`。

每个 case 使用同一个 workload JSONL，至少重复三次。Workload 行应包含 `group`、`prompt` 或 `input_tokens`、`output_tokens`；hot-prefix 组用于观察 shadow affinity，cold/uniform 组用于确认调度没有只优化单一模式。

必须保存：

- 前后 `nvidia-smi`；
- tokenizer sidecar identity；
- Controller、workeragent、vLLM command lines；
- loadgen summary 和 per-request JSONL；
- `/metrics` 和 `/internal/debug/decisions`；
- Controller/agent/vLLM logs；
- tokenizer fallback 总数和比例；
- worker selection 分布、TTFT、TPOT、总延迟和 HTTP status。

如果 tokenizer fallback 非零，需要把对应请求单独列出。只有在有单卡 baseline、双卡 baseline、双卡 cache-aware、重复运行和完整采集时，才可以写性能优化结论。
