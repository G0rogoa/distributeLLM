package api

type RegisterWorkerRequest struct {
	ID          string            `json:"id"`
	InstanceID  string            `json:"instance_id"`
	Address     string            `json:"address"`
	Models      []string          `json:"models"`
	Capacity    int               `json:"capacity"`
	BackendType string            `json:"backend_type,omitempty"`
	Model       string            `json:"model,omitempty"`
	GPUIndex    *int              `json:"gpu_index,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type HeartbeatRequest struct {
	InstanceID               string              `json:"instance_id"`
	ReportedRunning          int                 `json:"reported_running"`
	ReportedQueued           int                 `json:"reported_queued"`
	EstimatedRemainingTokens int64               `json:"estimated_remaining_tokens"`
	Load                     *WorkerLoadSnapshot `json:"load,omitempty"`
}

type DrainWorkerRequest struct {
	InstanceID string `json:"instance_id"`
}

type OptionalFloat64 struct {
	Value float64 `json:"value"`
	Valid bool    `json:"valid"`
}

type OptionalInt64 struct {
	Value int64 `json:"value"`
	Valid bool  `json:"valid"`
}

type WorkerLoadSnapshot struct {
	RunningRequests        OptionalInt64   `json:"running_requests,omitempty"`
	WaitingRequests        OptionalInt64   `json:"waiting_requests,omitempty"`
	GPUKVCacheUsageRatio   OptionalFloat64 `json:"gpu_kv_cache_usage_ratio,omitempty"`
	PrefixCacheHitsTotal   OptionalInt64   `json:"prefix_cache_hits_total,omitempty"`
	PrefixCacheMissesTotal OptionalInt64   `json:"prefix_cache_misses_total,omitempty"`
	Healthy                bool            `json:"healthy"`
}
