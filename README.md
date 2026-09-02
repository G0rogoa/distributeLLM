# DistServe

DistServe 是面向共享单节点多 GPU 服务器的弹性 LLM 推理控制面。它把资源层的 Worker 生命周期调度和请求层的 KV Cache 感知路由结合起来。当某块已授权 GPU 需要归还给主要科研任务时，规划中的资源层会协调 drain、缓存失效、降低准入，以及快速释放 DistServe 拥有的资源。

目标环境是一台共享的 Ubuntu 24.04 LTS KVM 虚拟机，配有 5 张 NVIDIA A100 80GB GPU。用户直接运行进程并手动协调使用权；Slurm、Kubernetes 和容器编排都不是这里的资源分配器。这个项目仍然是 LLM serving 项目，不是通用集群调度器或 GPU 监控封装。

## 调度模型

```text
GPU observation + cooperative authorization
                  |
                  v
Resource Scheduler: Worker start, drain, stop, cooldown
                  |
                  v
Registry: the currently usable Worker set
                  |
                  v
Request Scheduler: cache locality, load, queue, stability
```

资源层以秒到分钟为尺度工作；请求路由以毫秒到秒为尺度工作。每张 GPU 对应一个独立 Worker，可以让故障、生命周期、缓存归属和资源释放都保持清晰。资源调度会改变 Worker 集合，但不会替代请求调度。

## 当前状态和路线图

- Stage 1，已完成：流式 Gateway、Mock Workers、Registry、健康跟踪、round-robin 和 least-loaded 调度、reservation、有界准入、重试、指标、生命周期记录和负载生成。
- Stage 2，Mock 模式已完成：prompt identity、token blocks、prefix hashing、有界 Cache Index、Mock Worker LRU、cache events、prefix-aware scheduling、eviction 和 Cache Fill Reservations。
- Stage 3，进行中：OpenAI-compatible/vLLM HTTP 后端适配器、轻量 Worker Agent、显式 tokenizer mode、vLLM 指标归一化、shadow cache affinity，以及静态多 vLLM Worker 的调度/debug/loadgen 支持。
- Stage 4，计划中：Node Agent、GPU Observer、cooperative Lease、Resource Policy、Elasticity Manager、reclaim/cooldown、Interference Guard、Trace Replay、reclaim-risk-aware routing 和动态伸缩实验。
- Stage 5，可选：单节点 prefill/decode 池和本地 KV transfer。跨节点 RDMA 和多节点 serving 不是当前目标。

Stage 4 会先用 Mock GPU snapshots 和脱敏 Trace Replay 验证，再在授权实验窗口中接入真实 observer。计划实验包括静态 1->2->3->4->5 扩容、5->4->3->2 缩容，以及 1->3->5 扩容。项目不会声称未测量的性能结果。

## 资源安全边界

自动资源使用仍处于计划阶段，并默认关闭。计划中的安全默认值是 `resource_policy.enabled: false` 和 `allowed_gpu_indices: []`。启用它需要显式 allowed set 和经过组内认可的有效 Lease；瞬时空闲不等于授权。

DistServe 只观察聚合资源信号，只管理自己创建的 Workers，并且除了检测是否存在外部 compute 进程之外，不会 signal、检查或干扰其他用户的进程。主要科研任务优先。策略进入要慢，回收要快，每个生命周期决策都应可审计。“Low interference” 指低于约定影响阈值，并在 reclaim 信号后快速且可验证地释放资源。

仓库不包含服务器地址、凭据、Wi-Fi 信息、联系方式或敏感共享目录信息。

## Stage 3 真实 vLLM 路径

Stage 3 让 vLLM 留在 Controller 外部。先在已授权且空闲的 GPU 上手动启动一个或多个 vLLM OpenAI servers，再为每个 server 运行一个 `workeragent` 注册和观察它：

```bash
go run ./cmd/controller -listen=127.0.0.1:8080 -model=example-model -tokenizer-mode=disabled
go run ./cmd/workeragent -worker-id=worker-gpu0 -gpu-index=0 -model=example-model -backend-url=http://127.0.0.1:8100
go run ./cmd/workeragent -worker-id=worker-gpu1 -gpu-index=1 -model=example-model -backend-url=http://127.0.0.1:8101
```

Controller 会向 `backend_type: vllm` 的 Worker 转发普通 OpenAI-compatible JSON，并保留 SSE streaming。响应头会带上本次选择的 Worker，`/internal/debug/decisions` 会保留最近的候选分数，便于静态多实例实验复盘。它不会启动 vLLM、选择 GPU、kill 进程，也不会声称精确知道真实 KV block 驻留情况。更多说明见 `docs/real-vllm-integration.md`、`docs/real-cache-observability.md` 和 `docs/stage3-experiment-plan.md`。

真实 cache-aware 实验需要额外启动 tokenizer sidecar，并用 `-tokenizer-mode=remote` 和 `-scheduler=ect`：

```bash
PYTHON="/opt/anaconda3/bin/conda run -n zsq python" \
TOKENIZER_PATH=/path/to/model \
MODEL_ID=example-model MODEL_REVISION=local-v1 \
TOKENIZER_ID=example-tokenizer TOKENIZER_REVISION=local-v1 \
CHAT_TEMPLATE_VERSION=chat-v1 \
scripts/run-tokenizer-service.sh
```

## 运行当前 Mock 控制面

需要 Go 1.23 或更新版本：

```bash
go run ./cmd/controller
go run ./cmd/mockworker
go run ./cmd/mockworker -listen=:9002 -id=worker-2 -advertise=http://127.0.0.1:9002
```

发送一个流式请求：

```bash
curl -N http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"mock-llm","messages":[{"role":"user","content":"hello"}],"max_tokens":4,"stream":true}'
```

默认调度器是 prefix-aware；可用 `-scheduler=round-robin`、`-scheduler=least-loaded` 或 `-scheduler=ect` 跑 baseline/对照。指标在 `/metrics`；内部检查端点见 `docs/api.md`。

如果安装了 Docker，可以运行：

```bash
docker compose -f deployments/docker-compose.yml up --build
```

这会启动 Controller、三个异构 Mock Workers 和 Prometheus。

## 测试和 benchmark

```bash
go test ./...
go vet ./...
go test -race ./...
```

运行 load generator：

```bash
go run ./cmd/loadgen -model=mock-llm -requests=100 -concurrency=8 -stream -format=json -output=results.jsonl
```

请求实验脚本在 `experiments/` 下。参见 `docs/benchmark-methodology.md` 和 `docs/stage4-benchmark-plan.md`。当前系统使用 timing-simulator Workers：它不会运行真实模型、分配 GPU KV tensors、自动 claim GPU、提供已认证控制 API，也没有实现 Stage 3-5 的全部能力。公开 Mock API 只支持 `model`、`messages`、`max_tokens`、`temperature` 和 `stream`；Controller 状态仍保存在内存中，内部 API 未认证。真实 vLLM 路径已经完成单卡 smoke test，当前只支持手动启动的静态多实例实验，不是弹性 GPU 自动化或多 GPU benchmark 结果。
