# API

## 公开接口

`POST /v1/chat/completions` 接受 `model`、`messages`、`max_tokens`、`temperature` 和 `stream`。Controller 接受配置中的 model；默认是 `mock-llm`。`max_tokens` 范围是 1-4096。流式响应使用 `text/event-stream`，并以 `data: [DONE]` 结束。`X-Request-ID` 可以由客户端传入，也可以由 Controller 生成并返回。

动态调度成功选择 Worker 后，Controller 会在响应头里返回 `X-DistServe-Worker-ID`、`X-DistServe-Instance-ID` 和 `X-DistServe-Backend-Type`。这些头用于本地实验关联 loadgen 输出、Gateway lifecycle 和 Worker logs；不要把它们当作稳定公开 API。

`GET /health` 报告进程健康状态，`GET /metrics` 返回 Prometheus 文本格式指标。

## 内部实验 API

`POST /internal/workers/register`、`POST /internal/workers/{id}/heartbeat`、`POST /internal/workers/{id}/drain`、`GET /internal/workers`、`GET /internal/debug/requests` 和 `GET /internal/debug/decisions` 是未认证的实验室内部端点。生产使用必须把它们放在隔离 listener 后，并加上认证和授权。

注册请求需要 `id`、`instance_id`、`address`、`models` 和 `capacity`。Stage 3 Worker 还可以发送 `backend_type`、`model`、`gpu_index` 和 `labels`。缺失 `backend_type` 时会默认使用 `mock`，以兼容现有 Mock Workers。Heartbeat 需要 `instance_id` 和已报告的 running/queued work。真实 Worker Agent 还可以带一个归一化后的 `load` 对象；其中每个指标都是 optional，缺失的 vLLM 指标表示 unknown，而不是 0。

`GET /internal/debug/decisions` 返回最近的调度决策环形缓冲区。每条记录包含 request ID、策略名、最终选择的 worker/instance、score/reason，以及所有 eligible candidates 的低基数字段和 score breakdown。它不会记录 prompt、backend address 或 Authorization header。

Controller 支持 `-tokenizer-mode=mock|disabled|remote`。Remote mode 需要 `-tokenizer-url` 和正数 `-tokenizer-timeout`，并且必须显式配置完整 cache identity：`-model-revision`、`-tokenizer-id`、`-tokenizer-revision`、`-chat-template-version`、`-cache-format-version`、`-kv-layout` 和 `-cache-block-size`。Remote tokenizer 失败时请求仍会转发到 backend，但 lifecycle 和 metrics 会记录 `tokenizer_fallback`。

Gateway 错误使用类似 OpenAI 的 `{ "error": { "message", "type", "code" } }` envelope。重要状态码包括：400 表示输入/model 无效，429 表示 Controller admission rejection，503 表示无 eligible worker 或 Worker queue full，504 表示 deadline，502 表示 worker transport failure。
