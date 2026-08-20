package mockworker

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"distserve/internal/registry"
)

func TestRegistrationHeartbeatAndExit(t *testing.T) {
	workers := registry.New(time.Second, 2*time.Second)
	controller := httptest.NewServer(workers.Handler())
	defer controller.Close()
	worker := New("mock-llm", 0, 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- worker.RunRegistration(ctx, RegistrationConfig{ControllerURL: controller.URL, ID: "worker-1", InstanceID: "instance-1", Address: "http://worker", Model: "mock-llm", Capacity: 2, Interval: 5 * time.Millisecond}, nil)
	}()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshots := workers.Snapshots()
		if len(snapshots) == 1 && snapshots[0].Status == registry.StatusHealthy {
			break
		}
		time.Sleep(time.Millisecond)
	}
	snapshots := workers.Snapshots()
	if len(snapshots) != 1 || snapshots[0].Status != registry.StatusHealthy {
		t.Fatalf("snapshots=%+v", snapshots)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("registration loop did not exit")
	}
}
