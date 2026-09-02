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
