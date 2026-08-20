package api

type RegisterWorkerRequest struct {
	ID         string   `json:"id"`
	InstanceID string   `json:"instance_id"`
	Address    string   `json:"address"`
	Models     []string `json:"models"`
	Capacity   int      `json:"capacity"`
}

type HeartbeatRequest struct {
	InstanceID               string `json:"instance_id"`
	ReportedRunning          int    `json:"reported_running"`
	ReportedQueued           int    `json:"reported_queued"`
	EstimatedRemainingTokens int64  `json:"estimated_remaining_tokens"`
}

type DrainWorkerRequest struct {
	InstanceID string `json:"instance_id"`
}
