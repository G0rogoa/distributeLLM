package mockworker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"distserve/internal/api"
)

type RegistrationConfig struct {
	ControllerURL string
	ID            string
	InstanceID    string
	Address       string
	Model         string
	Capacity      int
	Interval      time.Duration
}

func (w *Worker) RunRegistration(ctx context.Context, config RegistrationConfig, client *http.Client) error {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	register := api.RegisterWorkerRequest{ID: config.ID, InstanceID: config.InstanceID, Address: config.Address, Models: []string{config.Model}, Capacity: config.Capacity}
	ticker := time.NewTicker(config.Interval)
	defer ticker.Stop()
	registered := false
	for {
		if !registered {
			if err := postJSON(ctx, client, strings.TrimRight(config.ControllerURL, "/")+"/internal/workers/register", register); err == nil {
				registered = true
			}
		} else {
			heartbeat := api.HeartbeatRequest{InstanceID: config.InstanceID, ReportedRunning: int(w.Active()), ReportedQueued: int(w.Queued())}
			if err := postJSON(ctx, client, strings.TrimRight(config.ControllerURL, "/")+"/internal/workers/"+config.ID+"/heartbeat", heartbeat); err != nil {
				registered = false
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func postJSON(ctx context.Context, client *http.Client, url string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode body: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %s", response.Status)
	}
	return nil
}
