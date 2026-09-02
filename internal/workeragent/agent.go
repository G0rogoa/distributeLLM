package workeragent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"distserve/internal/api"
	"distserve/internal/backend"
	"distserve/internal/registry"
)

type Agent struct {
	Config Config
	Client *http.Client
	Log    *slog.Logger
}

func (a *Agent) Run(ctx context.Context) error {
	config := a.Config
	if err := config.Validate(); err != nil {
		return err
	}
	client := a.Client
	if client == nil {
		client = &http.Client{}
	}
	log := a.Log
	if log == nil {
		log = slog.Default()
	}
	controller := strings.TrimRight(config.ControllerURL, "/")
	worker := registry.WorkerSnapshot{ID: config.WorkerID, InstanceID: config.InstanceID, Address: config.BackendURL, Models: []string{config.Model}, BackendType: string(backend.TypeVLLM), Model: config.Model, GPUIndex: &config.GPUIndex, Labels: config.Labels, Capacity: config.Capacity}
	register := api.RegisterWorkerRequest{ID: config.WorkerID, InstanceID: config.InstanceID, Address: config.BackendURL, Models: []string{config.Model}, BackendType: string(backend.TypeVLLM), Model: config.Model, GPUIndex: &config.GPUIndex, Labels: config.Labels, Capacity: config.Capacity}
	transport := backend.VLLM{OpenAIHTTP: backend.OpenAIHTTP{Client: client}}
	ticker := time.NewTicker(config.HeartbeatInterval)
	defer ticker.Stop()
	backoff := time.Second
	registered := false
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if !registered {
			if err := postJSON(ctx, client, controller+"/internal/workers/register", register); err != nil {
				log.Warn("worker registration failed", "worker_id", config.WorkerID, "error", err)
				if !sleepContext(ctx, backoff) {
					return nil
				}
				backoff = nextBackoff(backoff, 30*time.Second)
				continue
			}
			registered = true
			backoff = time.Second
		}
		if err := a.heartbeat(ctx, client, controller, transport, worker, config.HealthTimeout); err != nil {
			log.Warn("worker heartbeat failed", "worker_id", config.WorkerID, "error", err)
			registered = false
			if !sleepContext(ctx, backoff) {
				return nil
			}
			backoff = nextBackoff(backoff, 30*time.Second)
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (a *Agent) heartbeat(ctx context.Context, client *http.Client, controller string, transport backend.VLLM, worker registry.WorkerSnapshot, timeout time.Duration) error {
	healthCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := transport.Health(healthCtx, worker); err != nil {
		return fmt.Errorf("backend health: %w", err)
	}
	load, err := transport.Metrics(healthCtx, worker)
	if err != nil {
		load = api.WorkerLoadSnapshot{Healthy: true}
	}
	running, queued := 0, 0
	if load.RunningRequests.Valid {
		running = int(load.RunningRequests.Value)
	}
	if load.WaitingRequests.Valid {
		queued = int(load.WaitingRequests.Value)
	}
	heartbeat := api.HeartbeatRequest{InstanceID: worker.InstanceID, ReportedRunning: running, ReportedQueued: queued, Load: &load}
	return postJSON(ctx, client, controller+"/internal/workers/"+worker.ID+"/heartbeat", heartbeat)
}

func postJSON(ctx context.Context, client *http.Client, url string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %s", response.Status)
	}
	return nil
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func nextBackoff(current, max time.Duration) time.Duration {
	current *= 2
	if current > max {
		return max
	}
	return current
}
