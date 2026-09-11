# Experiment data schema

每次 Stage 3D 实验写入独立目录：

```text
artifacts/<experiment-id>/
  manifest.json
  requests.jsonl
  decisions.json
  worker-samples.jsonl
  controller-metrics-before.txt
  controller-metrics-after.txt
  vllm-metrics-before/
  vllm-metrics-after/
  nvidia-before.txt
  nvidia-after.txt
  summary.json
  analysis.json
  logs/
```

`artifacts/` 不提交 Git。原始 `remote-results/` 只作历史输入，不覆盖、不删除、不直接提交。

正式运行前脚本要求 clean committed worktree，并用 `tools/write_manifest.py` 做 JSON 序列化和必填字段校验。`tools/sample_workers.py` 每秒从 Controller 内部 API 和 metrics 保存脱敏 worker 快照；缺失指标写 `null`，进程在实验退出时停止。分析器将空时间序列判为 data-quality failure。

可提交的精简结果放在 `experiment-summaries/stage3e/`，只包含矩阵生成的 aggregate JSON/CSV、data-quality 报告和复现说明，不包含原始 prompts、backend URL、日志、进程清单或完整 `remote-results/`。

## Manifest

Manifest 记录实验可复现信息，但不记录 IP、用户名、密码、token、Authorization、完整模型访问凭据或其他用户进程命令行。

关键字段包括：

```json
{
  "experiment_id": "...",
  "git_commit": "...",
  "dirty_worktree": false,
  "start_time": "...",
  "end_time": "...",
  "model_id": "...",
  "model_revision": "...",
  "tokenizer_id": "...",
  "tokenizer_revision": "...",
  "chat_template_version": "...",
  "vllm_version": "...",
  "torch_version": "...",
  "cuda_build_version": "...",
  "driver_version": "...",
  "gpu_indices": [1, 2],
  "scheduler": "ect",
  "cost_profile": "...",
  "workload": "...",
  "prefix_tokens": 2048,
  "output_tokens": 64,
  "requests": 100,
  "concurrency": 8,
  "request_rate": null,
  "warmup_requests": 10,
  "seed": 20260903,
  "repetition": 1,
  "controller_flags": [],
  "worker_flags": []
}
```

## Request records

`requests.jsonl` 每行是一条请求结果。Usage 字段只在后端实际返回 usage 时标记有效：

```text
prompt_tokens
completion_tokens
total_tokens
usage_source
usage_valid
prefill_observation_valid
decode_observation_valid
```

`usage_source` 可为 `vllm_response`、`tokenizer`、`estimated` 或 `unknown`。流式请求必须请求 `stream_options.include_usage=true`；后端不返回 usage 时保持 unknown，不能把 SSE chunk 数或 `[DONE]` 当 token 数。
