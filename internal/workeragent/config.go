package workeragent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

type Config struct {
	WorkerID          string
	InstanceID        string
	ControllerURL     string
	BackendURL        string
	Model             string
	GPUIndex          int
	HeartbeatInterval time.Duration
	HealthTimeout     time.Duration
	Capacity          int
	Labels            map[string]string
}

func (c Config) Validate() error {
	if c.WorkerID == "" || c.InstanceID == "" || c.ControllerURL == "" || c.BackendURL == "" || c.Model == "" {
		return fmt.Errorf("worker_id, instance_id, controller_url, backend_url and model are required")
	}
	if c.HeartbeatInterval <= 0 || c.HealthTimeout <= 0 || c.Capacity < 1 {
		return fmt.Errorf("heartbeat_interval, health_timeout and capacity must be positive")
	}
	if c.GPUIndex < 0 {
		return fmt.Errorf("gpu_index must be non-negative")
	}
	return nil
}

func NewInstanceID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return "agent-" + hex.EncodeToString(bytes[:]), nil
}
