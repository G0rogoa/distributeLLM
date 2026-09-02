# 架构

## 当前切片

Milestone 1 是一条垂直请求路径：

```text
client -> controller/gateway -> mock worker -> controller/gateway -> client
```

Controller 是模块化单体。Gateway 负责 HTTP 校验、request ID、request deadline、后端转发和 streaming proxy 行为。它只依赖一个 HTTP backend address，不依赖 mock worker 的实现。Mock worker 是独立进程，实现同一个很小的 OpenAI-compatible endpoint。

API wire types 与 gateway/worker 行为分开存放。这样 mock simulation 细节不会泄露到 public API。Stage 3 把这个边界推进为 `internal/backend`，因此 Gateway 依赖 OpenAI-compatible HTTP backend，而不是 mock worker 内部实现。

Milestone 2 在 Gateway 旁边加入 Registry，而不是把它塞进 Gateway。Worker 注册稳定 ID 和进程级 instance ID，然后每两秒报告 load。Controller 每秒 sweep 一次 heartbeat age。调度器消费不可变 Worker snapshots，不访问 Registry map。

内部实验端点包括：

- `POST /internal/workers/register`
- `POST /internal/workers/{id}/heartbeat`
- `POST /internal/workers/{id}/drain`
- `GET /internal/workers`

这些端点目前有意不做认证，不能暴露到公网。

## 请求序列

1. Gateway 校验 JSON 请求和 model。
2. Gateway 创建或转发 `X-Request-ID`，并派生 timeout context。
3. 发往 Worker 的 HTTP 请求使用该 context。
4. 对 streaming response，Gateway 复制每个 SSE event 并立即 flush。
5. completion、error、timeout 或 client cancellation 都会关闭 upstream response。
6. 结构化 completion log 会记录 TTFT 和总 duration，但不记录 prompt。

模块化单体让小项目阶段的状态归属和故障行为保持清晰。Registry、scheduling 和 admission 以后可以变成服务边界，而不改变外部 API。

## 已完成的 Phase 1 模块

- Gateway 负责 validation、admission、timeout、retry boundary、streaming proxy 和 lifecycle completion。
- Registry 负责 Worker identity、health、load report 和 local reservations。
- Scheduler 消费拷贝出来的 snapshots 并返回可解释 decision。它既不修改 Registry，也不执行网络 I/O。
- Mock Worker 负责 running semaphore、queue semaphore、timing simulation、random source 和 heartbeat client。
- Telemetry 负责有界 counters/observations，并渲染 Prometheus exposition。
- Loadgen 是独立 client 进程，没有 control-plane 依赖。

依赖方向是 `cmd -> gateway -> registry/scheduler`，而 `scheduler` 只依赖 Registry snapshot 类型。选择之后还有一次独立的 atomic Reserve commit，因此 stale snapshot 不会把工作提交到已变化或已满的 instance 上。

## Phase 2 prefix 准备

`internal/cache` 提供 request-local 的准备流水线，不改变 Phase 1 调度行为：

```text
messages -> deterministic PromptBuilder -> Tokenizer -> TokenBlock builder
         -> versioned CacheIdentity -> SHA-256 Prefix Hash Chain
```

Tokenization 和 hashing 在获取任何未来 Cache Index lock 之前完成。只有 full blocks 会进入可复用 prefix chain。Mock Tokenizer 是确定性的测试基础设施，不声称兼容真实模型。

内存中的 Cache Index 维护 Worker-to-prefix 和 prefix-to-Worker 两个方向的 map。Cache events 按 Worker instance 做版本化，并按每个 instance 的 sequence 排序；leases 和有界 cleanup 防止 metadata 永久可调度。Index 是 advisory。已完成的 Phase 2 路径是：

```text
Gateway prepare -> CacheIndex.Match -> PrefixAware.Select -> Registry.Reserve
  -> FillReservations.Reserve -> Worker local lookup -> uncached prefill
  -> local LRU fill -> bounded event channel -> CacheIndex.Apply
```

Cache hint 是内部 transport data，不是 public OpenAI DTO 的一部分。Worker 会检查自己的完整本地 chain 并报告 actual hits；stale Controller metadata 可能影响 latency，但不能改变生成结果。Fill reservations 是有 TTL 的 affinity hints，并会在所有 request exit path 上释放。

## Stage 3 真实后端边界

真实 vLLM 支持仍保留相同请求调度路径：

```text
Gateway prepare -> Scheduler.Select -> Registry.Reserve
  -> backend.OpenAIHTTP -> external vLLM OpenAI server -> Gateway proxy
```

轻量 `workeragent` 描述并观察一个已经运行的 vLLM instance。它注册 `backend_type: vllm`、新的进程级 `instance_id`、backend URL、model、声明的 GPU index 和可选 labels。它 health-check vLLM，并只报告归一化后的 optional load fields。Controller 不启动、停止或 signal vLLM。

本阶段一张 GPU 对应一个 vLLM instance。多 GPU 会表现为多个独立注册的 Workers，每个 Worker 有自己的 instance ID 和 loopback port。Stage 3 不做跨 Worker KV transfer、tensor-parallel orchestration、自动 GPU 选择或弹性资源 claim。

Cache evidence 必须显式标注。Mock cache events 产生 `MockExact` metadata。真实请求可以产生短 TTL 的 `ShadowEstimated` affinity，并绑定到 `worker_id + instance_id + cache identity`。未知或过期信息是 `Unknown`。vLLM 聚合 prefix-cache metrics 是观测信号，不证明某个具体 prefix 驻留在某个具体 Worker 上。

## 计划中的单节点资源层

Stage 4 会在现有请求路径周围增加一个更慢的资源控制循环，而不是嵌进请求路径：

```text
Node Agent: GPU Observer + Lease snapshot + host Interference Guard
                              |
                              v
Resource Policy -> Elasticity Manager -> Worker start / drain / stop
                              |
                              v
                         Registry
                              |
                              v
Gateway -> Request Scheduler -> healthy Worker
              ^               |
              +-- Cache Index-+
```

Registry 连接两层调度：生命周期变化决定哪些 Worker instances 是 candidates；request reservations 和 demand 会反馈给 drain 与 capacity decisions。Worker removal 会让该 exact instance 的 Cache Index view 失效。Request Scheduler 接收拷贝出的 load、cache 和 resource-stability features；它绝不依赖 GPU Observer，也不执行系统命令。

计划中的 resource states 是 `Unavailable`、`Observed`、`Borrowable`、`Claimed`、`Reclaiming` 和 `CoolingDown`。Worker lifecycle 会把 claimed resources 细分为 `Starting`、`Healthy`、`Draining` 和 `Stopping`。进入状态需要同时满足授权和稳定 idle window。外部 compute process、Lease expiry、host pressure 或 administrator reclaim 都会把状态推向 release。

初始部署是单台共享服务器上的一个 Controller 和一个 Node Agent。每个真实 Worker 初始会拥有一张 allowed GPU。Multi-node placement、consensus、Slurm、Kubernetes 和跨节点 KV transfer 都不在当前架构范围内。参见 `resource-policy.md`、`cooperative-lease.md` 和 `elastic-worker-lifecycle.md`。
