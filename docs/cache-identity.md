# Cache identity

Phase 2/3 的 cache metadata 会按所有可能改变 prompt tokens 或 KV layout 的值做 namespace：protocol version、model ID 和 revision、tokenizer ID 和 revision、chat-template version、adapter ID、block size、cache-format version，以及 `kv_layout`。必须完全相等；cache entries 不会跨 identity 共享。

`PromptBuilder` 使用确定性、带版本的边界格式。每条 message 都包含 role 和 content，并带显式 byte length，因此类似 marker 的用户内容不会制造歧义 prompt。temperature、stream、max output tokens 这类 sampling 值不包含在 identity 中，因为它们不会改变 prefill input。如果未来某个 engine setting 会改变 prefill graph 或 KV layout，它必须先进入 `CacheIdentity`，然后才能复用。

确定性的 mock tokenizer 会把 Unicode letter/digit/underscore 连续片段切开，并把 punctuation 作为单独 token。每个 piece 都用 SHA-256 映射成 uint32 `TokenID`。这些 ID 是稳定测试数据，不兼容任何真实模型 tokenizer。

真实 vLLM 实验使用 remote tokenizer sidecar。Controller 向 sidecar 发送 deterministic prompt text 和 expected identity；sidecar 使用与 vLLM 相同的本地 tokenizer 文件返回 token IDs。identity mismatch、timeout 或 sidecar 不可用会让本次请求降级为 cache-unaware，并记录 `tokenizer_fallback`，不会静默改用 mock token IDs。
