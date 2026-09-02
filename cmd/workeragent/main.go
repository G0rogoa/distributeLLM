package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"distserve/internal/workeragent"
)

func main() {
	workerID := flag.String("worker-id", "worker-gpu0", "stable worker ID")
	controllerURL := flag.String("controller-url", "http://127.0.0.1:8080", "controller URL")
	backendURL := flag.String("backend-url", "http://127.0.0.1:8100", "already running vLLM OpenAI server URL")
	model := flag.String("model", "example-model", "served model name")
	gpuIndex := flag.Int("gpu-index", 0, "declared GPU index for this backend")
	heartbeatInterval := flag.Duration("heartbeat-interval", 2*time.Second, "heartbeat interval")
	healthTimeout := flag.Duration("health-timeout", 2*time.Second, "vLLM health and metrics timeout")
	capacity := flag.Int("capacity", 1, "controller admission capacity for this backend")
	labels := flag.String("labels", "", "comma-separated key=value labels")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	instanceID, err := workeragent.NewInstanceID()
	if err != nil {
		logger.Error("create instance ID", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	agent := workeragent.Agent{
		Config: workeragent.Config{
			WorkerID:          *workerID,
			InstanceID:        instanceID,
			ControllerURL:     *controllerURL,
			BackendURL:        *backendURL,
			Model:             *model,
			GPUIndex:          *gpuIndex,
			HeartbeatInterval: *heartbeatInterval,
			HealthTimeout:     *healthTimeout,
			Capacity:          *capacity,
			Labels:            parseLabels(*labels),
		},
		Client: &http.Client{},
		Log:    logger,
	}
	logger.Info("workeragent starting", "worker_id", *workerID, "instance_id", instanceID, "backend_url", *backendURL, "gpu_index", *gpuIndex)
	if err := agent.Run(ctx); err != nil {
		logger.Error("workeragent stopped", "error", err)
		os.Exit(1)
	}
	logger.Info("workeragent stopped", "worker_id", *workerID, "instance_id", instanceID)
}

func parseLabels(input string) map[string]string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	result := map[string]string{}
	for _, item := range strings.Split(input, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(item), "=")
		if !ok || key == "" {
			continue
		}
		result[key] = value
	}
	return result
}
