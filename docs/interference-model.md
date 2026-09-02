# Interference model（计划中的 Stage 4）

“Low interference” 的意思是把对其他科研任务的影响控制在约定阈值以下，并在 reclaim signal 后快速且可验证地释放资源。它不表示隐蔽使用。

Interference Guard 消费 aggregate、sanitized measurements：

- GPU compute utilization、memory use、temperature、power，以及是否存在 non-DistServe compute process；
- host CPU load、available memory、swap activity 和 storage read/write rates；
- DistServe model-loading bandwidth、Worker load 和 measured release time。

它不收集 usernames、full commands、environments、files、prompts、model contents、tokens 或 credentials。它永远不会向 foreign processes 发送 signals。

当 pressure 上升时，响应顺序是：停止 new starts，降低 admission capacity，drain low-value/cold-cache Workers，必要时释放更多 DistServe GPUs。Foreign compute presence 或 Lease expiry 是 fast reclaim trigger。Model loads 默认串行化，因为它们可能同时压 CPU、memory、disk 和 GPU。

每次 violation 都包含 threshold、observed aggregate、duration、input freshness、action 和 release latency。Thresholds 保持可配置，必须通过 Trace Replay 和授权实验校准，不能声称是 universal optima。
