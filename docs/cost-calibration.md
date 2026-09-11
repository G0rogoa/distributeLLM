# Cost calibration

Stage 3C 的 `ect` 使用固定常量估计 prefill、decode 和排队成本。它足以验证调度链路，但不能支持强性能结论，因为不同模型、GPU、vLLM 版本和 prompt 长度都会改变真实成本。Stage 3D 把成本模型显式写成版本化 profile，并把 profile 作为实验 artifact 保存。

## Profile

Cost profile 是 JSON 文件：

```json
{
  "version": 1,
  "model_identity_hash": "...",
  "hardware_class": "A100-80GB",
  "prefill_fixed_ms": 0,
  "prefill_ms_per_token": 0,
  "decode_fixed_ms": 0,
  "decode_ms_per_token": 0,
  "running_request_ms": 0,
  "waiting_request_ms": 0,
  "reservation_ms": 0,
  "shadow_confidence": 0.5,
  "sample_count": 0,
  "prefill_sample_count": 0,
  "decode_sample_count": 0,
  "prefill_r_squared": 0,
  "decode_r_squared": 0,
  "prefill_intercept_clipped": false,
  "decode_intercept_clipped": false,
  "prefill_source": "calibrated",
  "decode_source": "calibrated",
  "queue_source": "default",
  "calibrated_at": "2026-09-03T00:00:00Z",
  "valid_token_range": [256, 8192],
  "source": "calibrated"
}
```

Calibrator 默认要求非空 `-model-identity-hash`；`-allow-empty-identity` 只允许测试使用。`valid_token_range` 来自实际有效 prefill 样本的最小/最大 token 数。没有有效 prefill 或 decode 样本时拒绝生成 profile；R² 低于阈值时警告；负 intercept 截断为 0 时在 profile 中显式标记。当前 queue 参数仍是默认值，不能描述为 calibrated。

`model_identity_hash` 来自完整 `CacheIdentity`，包括 model/tokenizer/chat template/cache format/kv layout。Controller 默认拒绝加载 identity 不匹配的 profile；只有显式使用 `-allow-cost-profile-mismatch` 才允许跳过。profile 加载失败默认让 Controller 启动失败；只有 `-allow-cost-profile-fallback` 会回退到默认值，并在 debug 输出里标记 `source: fallback`。

## Offline calibration

`cmd/calibrator` 读取 JSONL 样本并拟合 profile。Prefill 样本使用不同长度的 unique prompt，避免 prefix cache 复用：

```text
256, 512, 1024, 2048, 4096, 8192 tokens
```

拟合：

```text
T_prefill(n) = prefill_fixed_ms + prefill_ms_per_token * n
```

Decode 样本使用短固定 prompt 和不同输出长度：

```text
16, 32, 64, 128, 256 output tokens
```

只使用实际 `completion_tokens` 有效的请求：

```text
T_after_first_token = decode_fixed_ms + decode_ms_per_token * completion_tokens
```

如果模型提前 EOS，保留实际 token 数，不用目标 `max_tokens` 替代。

## Queue cost

第一版 queue delay 保持线性：

```text
queue_delay_ms =
  running * running_request_ms
  + waiting * waiting_request_ms
  + local_reservations * reservation_ms
```

vLLM `running/waiting` metrics 可能有约 2 秒滞后；local reservation 更及时。缺失的 vLLM metrics 是 unknown，不等于真实 0。当前实现会退回 Registry heartbeat 字段，并在 decision breakdown 中记录 `running_source` 和 `waiting_source`。
