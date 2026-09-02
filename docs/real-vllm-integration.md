# 真实 vLLM 集成

Stage 3 增加真实 backend 路径，但不替换 Mock Workers。Controller 仍然校验请求、做 admission、让 Scheduler 基于 Registry snapshot 做 decision、reserve 该 exact worker instance，并 proxy response stream。区别是 Worker 现在可以声明 `backend_type: vllm`，此时 Gateway 会把普通 OpenAI-compatible JSON 转发给外部管理的 vLLM server。

```text
client -> controller -> scheduler -> registry reservation
       -> vLLM HTTP adapter -> one vLLM OpenAI server -> SSE/JSON -> client
```

Controller 只通过 HTTP 通信。它不 import Python、不 embed vLLM、不启动 model process、不终止 model process，也不推断 vLLM 内部 KV block placement。这让请求路径可以用 `httptest.Server` 测试，并把资源 ownership 留给后续 cooperative resource layer。

## 一张 GPU，一个 vLLM Instance

计划部署形态是每张已授权 GPU 对应一个独立 vLLM OpenAI server：

```text
GPU 0 -> http://127.0.0.1:8100 -> workeragent worker-gpu0
GPU 1 -> http://127.0.0.1:8101 -> workeragent worker-gpu1
```

每个 agent 注册一个稳定 `worker_id`，并为本次进程启动生成新的 `instance_id`。Scheduler 继续从 Registry snapshots 选择，因此以后增加更多 single-GPU instances 时，不需要在 Controller 中硬编码端口或 GPU indices。

## 无 GPU 本地运行

Mock mode 仍是默认值：

```bash
go run ./cmd/controller
go run ./cmd/mockworker
```

真实 backend 模式下，先确认目标 GPU 空闲，再手动启动 vLLM：

```bash
nvidia-smi
GPU_INDEX=0 MODEL=example-model PORT=8100 scripts/run-vllm-single-gpu.sh
```

然后运行 Controller 和 Agent：

```bash
go run ./cmd/controller -listen=127.0.0.1:8080 -model=example-model -tokenizer-mode=disabled
go run ./cmd/workeragent -worker-id=worker-gpu0 -gpu-index=0 -model=example-model -backend-url=http://127.0.0.1:8100
```

Smoke script 已提供，但不会自动执行：

```bash
MODEL=example-model scripts/smoke-real-backend.sh
```

运行真实 GPU 序列前，必须先和 maintainer 确认 GPU、model、预计显存占用和测试时长。
