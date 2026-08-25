# DistServe 项目完整导读：面向 C++ 开发者的 Go 版说明

## 1. 项目定位

DistServe 是一个面向学习的分布式 LLM Serving 控制面。它实现了真实系统中常见的请求入口、Worker 注册与心跳、健康检查、调度、容量预留、过载保护、有限重试、流式转发、生命周期记录和指标采集。

当前 Worker 不加载真实模型，也不需要 GPU。它用计时器、并发惩罚和虚拟 Token 模拟 prefill 与 decode。因此，本项目更准确的定位是：

> 单 Controller、多独立 Worker、状态保存在内存中的分布式推理控制面实验平台。

当前项目没有实现真实 LLM、CUDA、continuous batching、KV cache 感知、prefill/decode 分离、持久化 Registry、Controller 高可用、认证、TLS、分布式一致性或 exactly-once。

## 2. 进程与组件总览

系统包含三类可执行程序：

| 程序 | 入口 | 职责 |
| --- | --- | --- |
| Controller | `cmd/controller/main.go` | HTTP 入口、服务发现、调度、准入、代理、观测 |
| Mock Worker | `cmd/mockworker/main.go` | 注册、心跳、容量与队列管理、模拟推理 |
| Load Generator | `cmd/loadgen/main.go` | 并发产生请求并统计吞吐、延迟、TTFT、TPOT |

运行关系：

```text
                         注册 / 心跳
                    ┌───────────────────┐
                    │                   ▼
Client ──HTTP──> Controller ──HTTP──> Worker 1
                    │    │
                    │    ├───────────> Worker 2
                    │    └───────────> Worker N
                    │
                    ├── Gateway
                    ├── Registry
                    ├── Scheduler
                    ├── Admission Limiter
                    ├── Lifecycle Store
                    └── Metrics
```

核心包及其 C++ 类比：

| Go 包 | 作用 | C++ 类比 |
| --- | --- | --- |
| `internal/gateway` | 校验、准入、调度、代理和流式转发 | API Gateway / Reverse Proxy |
| `internal/registry` | Worker 身份、健康状态、负载和预留 | 带 `shared_mutex` 的服务注册表 |
| `internal/scheduler` | 从不可变快照中选择 Worker | Strategy 模式 |
| `internal/admission` | 限制 Controller 在途请求 | 非阻塞 counting semaphore |
| `internal/mockworker` | 模拟计算节点 | 带运行池和等待队列的后端 |
| `internal/lifecycle` | 保存最近请求状态 | 固定容量 ring buffer |
| `internal/telemetry` | 指标计数和 Prometheus 输出 | 原子计数器 + exporter |
| `internal/api` | HTTP JSON wire types | DTO / 协议结构体 |

依赖方向保持为：

```text
cmd -> gateway -> registry/scheduler
                -> admission/lifecycle/telemetry/api

scheduler -> registry.WorkerSnapshot
mockworker -> api
```

Controller 是模块化单体：Gateway、Registry 和 Scheduler 是不同 package，但运行在同一个进程中。

## 3. Controller 启动流程

### 3.1 参数与日志

Controller 默认参数包括：

```text
-listen=:8080
-request-timeout=30s
-scheduler=least-loaded
-max-inflight=128
-retry=false
```

Go 的 `flag.String`、`flag.Int` 等函数返回指针，所以读取值使用 `*listen`、`*maxInFlight`。可将其理解为标准库替你维护命令行参数的存储位置。

日志使用标准库 `log/slog`，通过 JSON Handler 输出结构化日志，不依赖第三方日志库。

### 3.2 根 Context 与信号

```go
ctx, stop := signal.NotifyContext(
    context.Background(),
    os.Interrupt,
    syscall.SIGTERM,
)
defer stop()
```

`ctx` 是 Controller 后台任务的生命周期根节点。收到 `Ctrl+C` 或 `SIGTERM` 时，`ctx.Done()` 变为可读，健康扫描和关闭协程随之退出。

C++ 中可近似理解成 `std::stop_source` 和 `std::stop_token`。Go Context 还可以携带 deadline 和 timeout，并形成父子取消关系。

### 3.3 Registry 与健康扫描

```go
workerRegistry := registry.New(5*time.Second, 10*time.Second)
go workerRegistry.RunSweeper(ctx, time.Second)
```

后台 goroutine 每秒扫描一次：

- 心跳年龄达到 5 秒：Worker 进入 `suspect`；
- 心跳年龄达到 10 秒：Worker 进入 `unavailable`；
- 根 Context 取消：goroutine 退出并停止 ticker。

`go f()` 会启动 goroutine。它比 OS 线程轻量，由 Go runtime 在一组线程上调度，可近似类比使用 `std::jthread` 启动任务，但两者不是一一对应关系。

### 3.4 调度器选择

Controller 根据参数构造 `RoundRobin` 或 `LeastLoaded`，并保存为 `scheduler.Scheduler` 接口：

```go
type Scheduler interface {
    Name() string
    Select(context.Context, RequestMeta, []registry.WorkerSnapshot) (Decision, error)
}
```

Go 接口采用隐式实现。一个类型只要拥有接口要求的方法，就满足该接口，不需要显式继承或声明 `implements`。它更接近 C++ structural typing/concept，而不是传统虚基类继承。

### 3.5 路由与优雅关闭

Controller 将内部控制接口交给 Registry，其余接口交给 Gateway：

```text
/internal/workers/...       Registry
/internal/debug/requests    Gateway
/v1/chat/completions        Gateway
/health                     Gateway
/metrics                    Gateway
```

收到退出信号后，独立 goroutine 调用 `http.Server.Shutdown`，最多等待 5 秒，让已接受的请求完成，同时停止接收新连接。

## 4. Worker 启动、注册和心跳

Worker 的重要参数：

| 参数 | 默认值 | 含义 |
| --- | ---: | --- |
| `capacity` | 8 | 最大同时运行请求数 |
| `queue-capacity` | 32 | 最大等待请求数 |
| `prefill-delay` | 20ms | 固定 prefill 延时 |
| `prefill-per-token` | 500µs | 每个输入 Token 的附加 prefill 延时 |
| `decode-interval` | 15ms | 每个输出 Token 的基础延时 |
| `concurrency-penalty` | 3ms | 每个额外并发请求带来的 decode 惩罚 |
| `jitter` | 5ms | 随机延时扰动 |
| `failure-rate` | 0 | 注入 503 的概率 |

Worker 同时具有两个身份：

```text
ID         = 稳定逻辑身份，例如 worker-1
InstanceID = 本次进程身份，例如 worker-1-PID-启动时间
```

Instance ID 相当于分布式系统中的 incarnation ID 或 fencing token。相同稳定 ID 的新进程注册后，旧进程的延迟心跳会因 Instance ID 不匹配而得到 HTTP 409，不能覆盖新实例状态。

注册循环状态机：

```text
未注册
  │ POST /internal/workers/register
  ├── 失败：等待下一轮继续注册
  └── 成功
       ↓
已注册
  │ POST /internal/workers/{id}/heartbeat
  ├── 成功：保持已注册
  └── 失败：回到未注册状态
```

默认间隔是 2 秒。注册成功时 Worker 状态为 `starting`，第一次有效心跳后变为 `healthy`。调度器只选择 `healthy` Worker，因此启动 Worker 后通常需要等待约 2 秒才能接收请求。

## 5. Registry：状态所有权与并发规则

Registry 内部保存：

```go
type Registry struct {
    mu      sync.RWMutex
    workers map[string]*Worker
}
```

它近似于：

```cpp
std::shared_mutex mutex;
std::unordered_map<std::string, std::unique_ptr<Worker>> workers;
```

使用单个 `RWMutex` 是因为心跳和状态迁移同时修改多个字段，这些字段必须作为一个一致整体被观察。所有网络 I/O 和日志都在释放 Registry 锁之后执行。

### 5.1 状态机

```text
Register
   ↓
Starting
   ↓ 首次有效心跳
Healthy
   ├── 心跳超过 5 秒 ──> Suspect
   │                         │
   │                         └── 有效心跳 ──> Healthy
   ├── 心跳超过 10 秒 ─> Unavailable
   └── Drain ───────────> Draining
                              │ running=0 且 queued=0
                              └──────────────> Unavailable
```

只有 `healthy` 实例能被调度。

### 5.2 注册替换

- 相同 ID、相同 Instance ID：幂等刷新地址、模型和容量；
- 相同 ID、不同 Instance ID：认为 Worker 已重启，用新实例替换旧实例；
- 新实例的本地 reservation 从 0 开始；
- 旧实例的心跳与延迟释放不能修改新实例。

### 5.3 不可变快照

调度器不直接访问 Registry map，而是调用 `Snapshots()`。Registry 在读锁内复制数据，然后释放锁。

`Models []string` 也需要显式复制。Go slice 本质上类似：

```cpp
struct Slice {
    T* data;
    size_t len;
    size_t capacity;
};
```

直接复制 slice 只复制描述符，底层数组仍可能共享；因此代码使用 `append([]string(nil), worker.Models...)` 创建独立元素副本。

### 5.4 Snapshot、Select、Reserve

仅靠快照选择并不安全。假设容量为 1，两个请求可能同时看到 `running=0` 并选择同一个 Worker。真实流程是：

```text
Snapshot -> Select -> Reserve -> Forward
```

`Reserve` 在 Registry 写锁内重新验证：

```text
ID 存在
Instance ID 仍匹配
Status 仍为 Healthy
ReportedRunning + LocalReservations < Capacity
```

验证成功才增加 `localReservations`。这是“乐观读取 + 原子提交”：快照允许过时，但 Reserve 是真正的 commit point。

### 5.5 Reservation 释放

`Reserve` 返回一个 `release` 闭包。闭包使用 `sync.Once`，因此重复调用也只释放一次。释放时再次核对 Instance ID；如果 Worker 已被新实例替换，旧请求的延迟释放不会减少新实例的 reservation。

`defer release()` 可类比 C++ 的 scope guard/RAII：函数无论从哪个 return 路径退出都会释放资源。

## 6. Scheduler

调度器输入请求元数据和 Registry 快照，输出 `Decision`。它不修改 Registry、不执行网络 I/O，也不负责预留。

候选 Worker 必须满足：

```text
Status == Healthy
ReportedRunning + LocalReservations < Capacity
支持请求的 Model
```

候选项按 ID 排序以保证可解释、可复现的基本顺序。

### 6.1 Round Robin

Round Robin 使用 `atomic.Uint64` 维护索引：

```go
index := int((next.Add(1) - 1) % uint64(len(candidates)))
```

可类比 `std::atomic<uint64_t>::fetch_add`。轮转只发生在当前合格 Worker 之间，因此 Worker 失联、满载、drain 或模型不匹配都会改变候选集合。

### 6.2 Least Loaded

默认分数为：

```text
score = ReportedRunning
      + LocalReservations
      + 0.5 × ReportedQueued
      + 0.1 × EstimatedRemainingTokens / 1000
```

`Reported*` 来自周期心跳，存在延迟；`LocalReservations` 是 Controller 在两次心跳之间的即时修正，可降低大量新请求同时涌向上次心跳中最空闲 Worker 的风险。

最低分相同时，调度器使用受 mutex 保护的轮转计数器分散请求，避免永远偏向字典序第一个 Worker。

## 7. Gateway 请求完整流程

公开请求入口是：

```http
POST /v1/chat/completions
```

### 7.1 Request ID 与生命周期

Gateway 使用客户端的 `X-Request-ID`，或生成 `req-时间戳-原子序号`。它创建生命周期记录并增加请求总数、在途请求数。

函数退出时的 `defer` 统一完成：

- 减少 Gateway inflight；
- 记录总耗时；
- 补充失败或取消状态；
- 将记录写入生命周期 ring buffer。

### 7.2 JSON 解析与校验

请求体最多读取 1 MiB，未知字段会被拒绝。目前仅支持：

```json
{
  "model": "mock-llm",
  "messages": [{"role": "user", "content": "hello"}],
  "max_tokens": 4,
  "temperature": 0,
  "stream": true
}
```

约束包括：模型必须为 `mock-llm`、消息不能为空、`max_tokens` 必须处于 1 到 4096。

### 7.3 Controller 准入

Admission Limiter 内部是带缓冲 channel：

```go
slots := make(chan struct{}, limit)
```

向 channel 发送一个 `struct{}{}` 表示占用许可，从 channel 接收表示释放。`struct{}` 是零大小类型，只表达事件或许可，不携带数据。

Acquire 使用带 `default` 的 `select` 执行非阻塞获取。容量满时立即返回 HTTP 429，而不是继续排队。它可近似理解为 `counting_semaphore::try_acquire()`。

### 7.4 端到端超时与取消

Gateway 从客户端请求 Context 派生超时 Context：

```text
Client request Context
        └── Gateway timeout Context
                └── Worker HTTP request
```

客户端断开、客户端取消或 Gateway deadline 到达时，取消会传播到 Worker，Worker 的队列等待和推理 timer 会被中断。

### 7.5 调度和转发

Gateway 估算输入 Token 数，当前算法约为每 4 个字节一个 Token，不是真实 tokenizer，对中文并不准确。

随后执行：

```text
Snapshots -> Scheduler.Select -> Registry.Reserve
```

如果选择后实例或容量发生变化，Reserve 会失败；Gateway 最多重新获取一次快照再选择。成功后向 Worker 的 `/v1/chat/completions` 转发相同 JSON，并携带 Request ID 和 Worker ID 请求头。

### 7.6 有限重试

启用 `-retry` 后最多尝试两次。仅以下情况可重试：

- 连接 Worker 失败；
- Worker 返回 HTTP 503；
- 原请求 Context 尚未结束；
- 尚未开始向客户端发送响应。

重试前会关闭旧响应体、释放旧 Worker reservation，然后重新调度和预留。

一旦接受 HTTP 200 或流内容已经开始，就不能透明重试，否则客户端可能收到重复 Token，且已写出的 HTTP 头和状态不能安全撤回。

### 7.7 SSE 流式代理

Worker 的流式响应形如：

```text
data: {...token-0...}

data: {...token-1...}

data: [DONE]
```

Gateway 按行读取、写给客户端，并在每次写入后调用 `http.Flusher.Flush()`。`Write` 可能被缓冲，Flush 可以使 Token 尽快到达客户端，并让 TTFT 具有实际意义。

第一个非 `[DONE]` 的 `data:` 行到达时，Gateway 记录 First Token 时间和 TTFT。非流式请求则在 Worker 完整返回 JSON 后通过 `io.Copy` 转发。

### 7.8 资源清理

所有退出路径都必须处理：

```text
Controller admission slot
Registry reservation
Worker response body
timeout cancel function
inflight metric
lifecycle record
```

项目主要依靠 `defer` 和幂等 release closure 保证正常、错误、超时和取消路径不会泄漏资源。

## 8. Mock Worker 推理流程

Worker 使用两个 channel 实现两级容量控制：

```text
capacity   = 正在运行的槽位
queueSlots = 等待队列的槽位
```

请求流程：

```text
尝试获取 running slot
  ├── 成功：立即运行
  └── 失败
       ↓
     尝试获取 queue slot
       ├── 失败：HTTP 503
       └── 成功：等待 running slot
                    ├── 获得：离开队列并运行
                    └── Context 取消：离开队列并退出
```

Controller 满载返回 429；Worker 的运行槽和等待队列都满时返回 503。这是两层不同的背压。

`active` 和 `queued` 使用 `atomic.Int64`，因为 HTTP 请求 goroutine 会修改它们，注册 goroutine 会同时读取它们用于心跳。

### 8.1 Prefill 模拟

```text
prefill = PrefillDelay
        + InputTokens × PrefillPerToken
        + Jitter
```

### 8.2 Decode 模拟

```text
decodeDelay = DecodeInterval
            + (ActiveRequests - 1) × ConcurrencyPenalty
            + Jitter
```

这模拟并发升高后单请求 Token 延迟增加，但并不是 GPU、batching 或 KV cache 的准确模型。

### 8.3 可取消等待

Worker 不直接调用 `time.Sleep`，而是让 timer 与请求 Context 竞争：

```go
select {
case <-timer.C:
    return true
case <-request.Context().Done():
    return false
}
```

因此客户端取消后，Worker 不必等待完整模拟延时，能够及时释放运行槽。它近似于支持 stop token 的 `condition_variable::wait_until`。

Worker 的 `math/rand.Rand` 由独立 mutex 保护，因为该对象本身不保证并发安全。

## 9. Go HTTP 并发模型

`net/http` 会为请求启动 goroutine，然后调用匹配的 Handler。因此业务代码中没有显式 `go chatCompletions()`，但 Handler 仍会被多个请求并发调用。

共享状态分别由以下机制保护：

| 状态 | 并发机制 |
| --- | --- |
| Registry 多字段状态 | `sync.RWMutex` |
| Round Robin 索引 | `atomic.Uint64` |
| Least Loaded 平分轮转 | `sync.Mutex` |
| Worker active/queued | `atomic.Int64` |
| Worker 随机源 | `sync.Mutex` |
| Admission/Worker 容量 | buffered channel |
| Metrics 整数计数 | atomics |
| Metrics sum/map | `sync.Mutex` |
| Lifecycle ring | `sync.Mutex` |

每个项目显式创建的长期 goroutine 都有 Context 取消或 channel 关闭等明确退出条件。

## 10. Lifecycle 与 Metrics

Lifecycle Store 是容量为 1024 的内存 ring buffer，记录：

```text
ReceivedAt / AdmittedAt / ScheduledAt / ForwardedAt
FirstTokenAt / CompletedAt / FailedAt
SelectedWorker / SelectedInstance / SchedulerStrategy
RetryCount / InputTokens / OutputTokens / FinalStatus
```

访问接口：

```bash
curl http://127.0.0.1:8080/internal/debug/requests
```

Metrics 使用原子计数器和 mutex，并手工输出 Prometheus 文本：

```bash
curl http://127.0.0.1:8080/metrics
```

主要指标包括请求数、在途请求、错误、重试、准入拒绝、调度决策、reservation、Worker 状态、心跳年龄、Worker reported running/queued 等。TPOT 和调度耗时指标目前仍是占位值，不能视为完整生产指标实现。

## 11. Load Generator

Loadgen 使用典型 producer/worker/results 结构：

```text
Producer goroutine
       ↓
    jobs channel
       ├── client goroutine 1
       ├── client goroutine 2
       └── client goroutine N
                 ↓
           results channel
                 ↓
              汇总输出
```

`sync.WaitGroup` 可近似类比 `std::latch`。它支持 fixed-concurrency、fixed-rate 和 burst 三种到达模式，并统计成功率、吞吐、平均延迟、P50/P95/P99、TTFT、TPOT、状态码、错误和拒绝数。

仓库没有提交虚构的 benchmark 数字；任何性能结论都必须在实际环境运行实验得到。

## 12. 状态码与失败传播

| 场景 | 状态码/行为 |
| --- | --- |
| JSON、模型或参数非法 | 400 |
| Controller admission 满载 | 429 |
| 没有合格 Worker | 503 |
| Worker 队列满或注入失败 | 503 |
| Worker 连接失败 | 502 |
| Gateway deadline 且响应未开始 | 504 |
| Worker 旧实例心跳 | 409 |
| 客户端取消 | Context 逐级取消，释放容量和预留 |

如果 SSE 已经开始，再发生超时或传输错误，Gateway 无法可靠地把响应状态改为 504/502，客户端通常表现为流提前中断。

## 13. C++ 开发者需要掌握的 Go 写法

### 13.1 `defer` 与 RAII

```go
resource := acquire()
defer resource.Release()
```

可类比 `scope_exit`。不同之处是 `defer` 在函数返回时执行，多个 defer 按后进先出顺序执行，而不是在任意花括号作用域结束时执行。

### 13.2 `(value, error)` 与 `std::expected`

```go
value, err := operation()
if err != nil {
    return err
}
```

可类比 `std::expected<T, Error>`。Go 通常不用异常传播普通业务错误。`fmt.Errorf("...: %w", err)` 可以包装并保留错误链，`errors.Is` 用于匹配底层哨兵错误。

### 13.3 channel

Channel 是带同步语义的类型化通信通道：

- 无缓冲 channel 类似 rendezvous；发送和接收必须会合；
- 带缓冲 channel 类似有界 blocking queue；
- `chan struct{}` 常用于信号或 semaphore；
- `select` 类似同时等待多个 channel 操作；
- 带 `default` 的 `select` 可执行非阻塞尝试。

### 13.4 slice

Slice 是数组视图/描述符，不等同于拥有全部数据的 `std::vector`。复制 slice 不一定复制底层元素，需要根据所有权明确执行深复制。

### 13.5 接口

Go 接口由方法集合隐式满足，不需要继承。接口值内部大致包含动态类型信息和数据指针。除非确实需要多个实现或测试边界，本项目不会为每个结构都抽象接口。

### 13.6 struct tag 与可选字段

```go
Role string `json:"role"`
```

反引号中的 tag 为反射系统提供序列化元数据。指针配合 `omitempty` 常用于表达可选字段，近似 `std::optional<T>`。

## 14. 一次请求时序

```text
Client        Gateway       Admission      Registry/Scheduler       Worker
  │              │              │                  │                  │
  ├── POST ─────>│              │                  │                  │
  │              ├─ validate    │                  │                  │
  │              ├─ Acquire ───>│                  │                  │
  │              │<─ slot ──────┤                  │                  │
  │              ├─ Snapshots/Select/Reserve ─────>│                  │
  │              │<─ worker + release ─────────────┤                  │
  │              ├──────────── POST ─────────────────────────────────>│
  │              │                                   queue/capacity   │
  │              │                                   prefill/decode   │
  │              │<────────── data: token-0 ──────────────────────────┤
  │<─ token-0 ───┤ Flush                                               │
  │              │<────────── data: token-N ──────────────────────────┤
  │<─ token-N ───┤ Flush                                               │
  │              │<────────── data: [DONE] ───────────────────────────┤
  │<─ [DONE] ────┤                                                     │
  │              ├─ release reservation                               │
  │              ├─ release admission                                 │
  │              └─ lifecycle + metrics                               │
```

## 15. 推荐阅读和运行顺序

建议按以下顺序阅读：

1. `cmd/controller/main.go`：了解组件组装和进程生命周期；
2. `internal/api/openai.go`：熟悉 struct、slice、pointer 和 JSON tag；
3. `internal/admission/admission.go`：理解 channel semaphore 和非阻塞 `select`；
4. `internal/registry/registry.go`：理解锁、快照、闭包和 `sync.Once`；
5. `internal/scheduler/scheduler.go`：理解接口和调度策略；
6. `internal/gateway/gateway.go`：串联完整请求路径；
7. `internal/mockworker/worker.go`：理解 Context、Timer、channel 和 atomic；
8. `cmd/loadgen/main.go`：理解 producer/worker/results 并发模型。

本地运行：

```bash
go run ./cmd/controller
go run ./cmd/mockworker
```

等待 Worker 首次心跳后发送流式请求：

```bash
curl -N http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"mock-llm","messages":[{"role":"user","content":"hello"}],"max_tokens":4,"stream":true}'
```

查看内部状态：

```bash
curl http://127.0.0.1:8080/internal/workers
curl http://127.0.0.1:8080/internal/debug/requests
curl http://127.0.0.1:8080/metrics
```

执行测试：

```bash
go test ./...
go vet ./...
go test -race ./...
```

最值得深入理解的三个设计点是：

1. Context 如何把客户端取消和 deadline 一路传播到 Worker；
2. 为什么调度必须经过 Snapshot、Select、Reserve 三步；
3. `defer`、`sync.Once` 和 Instance ID 如何保证所有退出路径正确释放资源，并隔离已被替换的旧进程。
