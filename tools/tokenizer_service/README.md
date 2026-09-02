# Tokenizer Service

这个 sidecar 为 Go Controller 提供真实 Hugging Face tokenizer。它使用现有 Python/Conda 环境中的 `transformers`，默认只监听 `127.0.0.1`，并使用 `local_files_only=True`，不会自动下载模型或 tokenizer。

示例：

```bash
python tools/tokenizer_service/server.py \
  --host 127.0.0.1 \
  --port 18091 \
  --tokenizer-path /data/zhangshenqiang/models/Qwen2.5-0.5B-Instruct \
  --model-id stage3-qwen0.5b \
  --model-revision local-qwen2.5-0.5b \
  --tokenizer-id qwen2.5-tokenizer \
  --tokenizer-revision local-qwen2.5-0.5b \
  --chat-template-version qwen2.5-instruct
```

接口：

- `GET /health`
- `GET /v1/identity`
- `POST /v1/tokenize`

`POST /v1/tokenize` 要求调用方传入 `expected_identity`。如果 identity 不完全匹配，服务返回 409，Controller 会把该请求降级为 cache-unaware，而不是使用错误的 prefix hash。
