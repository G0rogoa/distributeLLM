package workeragent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"distserve/internal/registry"
	"distserve/internal/workeragent"
)

func TestAgentRegistersAndHeartbeatsHealthyBackend(t *testing.T) {
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/metrics":
			w.Write([]byte("vllm:num_requests_running 1\nvllm:num_requests_waiting 2\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer backendServer.Close()
	workers := registry.New(100*time.Millisecond, 200*time.Millisecond)
	controller := httptest.NewServer(workers.Handler())
	defer controller.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agent := workeragent.Agent{Config: workeragent.Config{WorkerID: "worker-gpu0", InstanceID: "instance-1", ControllerURL: controller.URL, BackendURL: backendServer.URL, Model: "model-a", GPUIndex: 0, HeartbeatInterval: 5 * time.Millisecond, HealthTimeout: 50 * time.Millisecond, Capacity: 1}}
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshots := workers.Snapshots()
		if len(snapshots) == 1 && snapshots[0].Status == registry.StatusHealthy && snapshots[0].ReportedRunning == 1 && snapshots[0].ReportedQueued == 2 {
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	t.Fatalf("agent did not publish healthy snapshot: %+v", workers.Snapshots())
}

func TestAgentDoesNotHeartbeatUnhealthyBackend(t *testing.T) {
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer backendServer.Close()
	registers := 0
	heartbeats := 0
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/workers/register" {
			registers++
			w.WriteHeader(http.StatusOK)
			return
		}
		heartbeats++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer controller.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	agent := workeragent.Agent{Config: workeragent.Config{WorkerID: "worker-gpu0", InstanceID: "instance-1", ControllerURL: controller.URL, BackendURL: backendServer.URL, Model: "model-a", GPUIndex: 0, HeartbeatInterval: 5 * time.Millisecond, HealthTimeout: 5 * time.Millisecond, Capacity: 1}}
	if err := agent.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if registers == 0 {
		t.Fatal("agent never registered")
	}
	if heartbeats != 0 {
		t.Fatalf("unhealthy backend received heartbeats=%d", heartbeats)
	}
}
