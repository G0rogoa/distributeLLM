package experiment

import "time"

type Manifest struct {
	ExperimentID        string    `json:"experiment_id"`
	GitCommit           string    `json:"git_commit"`
	DirtyWorktree       bool      `json:"dirty_worktree"`
	StartTime           time.Time `json:"start_time"`
	EndTime             time.Time `json:"end_time"`
	ModelID             string    `json:"model_id"`
	ModelIdentityHash   string    `json:"model_identity_hash"`
	ModelRevision       string    `json:"model_revision"`
	TokenizerID         string    `json:"tokenizer_id"`
	TokenizerRevision   string    `json:"tokenizer_revision"`
	ChatTemplateVersion string    `json:"chat_template_version"`
	VLLMVersion         string    `json:"vllm_version"`
	TorchVersion        string    `json:"torch_version"`
	CUDABuildVersion    string    `json:"cuda_build_version"`
	DriverVersion       string    `json:"driver_version"`
	GPUIndices          []int     `json:"gpu_indices"`
	Scheduler           string    `json:"scheduler"`
	CostProfile         string    `json:"cost_profile"`
	Workload            string    `json:"workload"`
	PrefixTokens        int       `json:"prefix_tokens"`
	OutputTokens        int       `json:"output_tokens"`
	Requests            int       `json:"requests"`
	Concurrency         int       `json:"concurrency"`
	RequestRate         *float64  `json:"request_rate"`
	WarmupRequests      int       `json:"warmup_requests"`
	Seed                int64     `json:"seed"`
	Repetition          int       `json:"repetition"`
	CongestionLevel     int       `json:"congestion_level"`
	ShadowConfidence    float64   `json:"shadow_confidence"`
	OnlineLearning      bool      `json:"online_learning"`
	ControllerFlags     []string  `json:"controller_flags"`
	WorkerFlags         []string  `json:"worker_flags"`
}

type RequestRecord struct {
	JobID                   string  `json:"job_id,omitempty"`
	RequestID               string  `json:"request_id,omitempty"`
	Status                  int     `json:"status"`
	LatencyMS               float64 `json:"latency_ms"`
	TTFTMS                  float64 `json:"ttft_ms,omitempty"`
	TPOTMS                  float64 `json:"tpot_ms,omitempty"`
	SelectedWorkerID        string  `json:"selected_worker_id,omitempty"`
	SelectedInstanceID      string  `json:"selected_instance_id,omitempty"`
	BackendType             string  `json:"backend_type,omitempty"`
	Group                   string  `json:"group,omitempty"`
	RequestedInputTokens    int     `json:"requested_input_tokens,omitempty"`
	RequestedOutputTokens   int     `json:"requested_output_tokens,omitempty"`
	PromptTokens            int     `json:"prompt_tokens,omitempty"`
	CompletionTokens        int     `json:"completion_tokens,omitempty"`
	TotalTokens             int     `json:"total_tokens,omitempty"`
	UsageSource             string  `json:"usage_source"`
	UsageValid              bool    `json:"usage_valid"`
	PrefillObservationValid bool    `json:"prefill_observation_valid"`
	DecodeObservationValid  bool    `json:"decode_observation_valid"`
	Error                   string  `json:"error,omitempty"`
}
