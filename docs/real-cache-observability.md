# 真实 Cache 可观测性

DistServe 对 cache routing metadata 使用不同 evidence level：

- `MockExact`：Stage 2 Mock Workers 报告精确的模拟 cache events。Controller Cache Index 可以使用这些精确 mock events 做 prefix-aware scheduling。
- `ShadowEstimated`：一个成功经过真实 backend 的请求，可以为同一个 `(worker_id, instance_id, model/cache identity)` 创建短生命周期的 control-plane affinity hint。这只是软调度信号。
- `Unknown`：没有可信的 per-prefix evidence。

真实 vLLM 路径不声称 Controller 知道 vLLM 内部当前驻留了哪些具体 KV blocks。vLLM aggregate metrics 如果存在，可以用于观测和实验，但不能证明某个具体 prefix 在某个具体 worker 上驻留。

## Cache Index 与 Shadow Affinity

Stage 2 `CacheIndex` 仍然是精确 mock metadata 路径。它由 Mock Worker cache events 驱动；即使在 mock 模式中它也是 advisory，因为 stale metadata 可能 miss。

Stage 3 `AffinityIndex` 是单独、更薄的一层。它只记录某个请求最近成功通过了一个真实 worker instance。Entries 有短 TTL、容量上限和统计，并按 worker instance 和 cache identity 建 key，因此 worker 带着新的 `instance_id` 重启后不会继承旧 affinity。Worker unavailable、draining 或 instance replacement 事件会触发 Controller 清理该 exact instance 的 shadow affinity、mock cache view 和 fill reservations；事件丢失时 TTL 仍是兜底。Shadow matches 会在 lifecycle records 中标记为 `ShadowEstimated`，并且只能作为 scheduler hint。

Stage 3D 的 ECT 不把 `ShadowEstimated` 当作精确 cache hit。它使用 cost profile 中的 `shadow_confidence` 把 matched tokens 折扣为 `confidence_adjusted_cached_tokens`，再计算 uncached prefill 成本。这样可以在弱一致 metadata 和真实 vLLM 黑盒 cache 之间保持保守边界。

## Tokenizer 边界

Tokenizer mode 是显式的：

- `mock`：用于 Mock Workers 和 no-GPU tests 的确定性测试 tokenizer。
- `disabled`：真实 backend 模式，关闭 prefix-aware routing。
- `remote`：使用 HTTP tokenizer sidecar 获取真实 token IDs。

Go Controller 不重新实现 Hugging Face tokenizer。Remote tokenizer 使用显式 tokenizer path、model revision、tokenizer revision 和 chat template identity，sidecar 默认关闭 `trust_remote_code`，并使用本地文件，不自动下载模型。失败、超时或 identity mismatch 时，Controller 回退到 cache-unaware scheduling，并记录 `tokenizer_fallback`。Prompt 内容不能写入日志或指标。

Token usage 也必须标注来源。非流式请求解析 vLLM OpenAI response 的 `usage`；流式请求使用 `stream_options.include_usage=true`，只有后端实际返回 usage chunk 时才标记 `usage_valid=true`。不能把 `[DONE]`、SSE chunk 数或 `max_tokens` 当作实际 completion tokens。
