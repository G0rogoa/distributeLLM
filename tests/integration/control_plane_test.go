package integration

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"distserve/internal/gateway"
	"distserve/internal/mockworker"
	"distserve/internal/registry"
	"distserve/internal/scheduler"
)

func TestControllerWithThreeWorkersAndFailureExpiry(t *testing.T) {
	workers := registry.New(20*time.Millisecond, 40*time.Millisecond)
	servers := make([]*httptest.Server, 0, 3)
	counts := make([]atomic.Int64, 3)
	for i := 0; i < 3; i++ {
		index := i
		backend := mockworker.New("mock-llm", time.Millisecond, time.Millisecond).Handler()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { counts[index].Add(1); backend.ServeHTTP(w, r) }))
		servers = append(servers, server)
		defer server.Close()
		id := string(rune('a' + i))
		if err := workers.Register(registry.Worker{ID: id, InstanceID: "instance-" + id, Address: server.URL, Models: []string{"mock-llm"}, Capacity: 4}); err != nil {
			t.Fatal(err)
		}
		if err := workers.Heartbeat(id, "instance-"+id, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	controller := httptest.NewServer(gateway.NewDynamic(workers, &scheduler.RoundRobin{}, "mock-llm", time.Second, 16, true, nil, logger).Handler())
	defer controller.Close()
	for i := 0; i < 6; i++ {
		response, err := http.Post(controller.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"mock-llm","messages":[{"role":"user","content":"hello"}],"max_tokens":1,"stream":true}`))
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status=%d", response.StatusCode)
		}
	}
	for i := range counts {
		if counts[i].Load() != 2 {
			t.Fatalf("worker %d requests=%d", i, counts[i].Load())
		}
	}
	time.Sleep(45 * time.Millisecond)
	workers.Sweep()
	for _, snapshot := range workers.Snapshots() {
		if snapshot.Status != registry.StatusUnavailable {
			t.Fatalf("worker %s status=%s", snapshot.ID, snapshot.Status)
		}
		if snapshot.LocalReservations != 0 {
			t.Fatalf("worker %s reservations=%d", snapshot.ID, snapshot.LocalReservations)
		}
	}
	response, err := http.Post(controller.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"mock-llm","messages":[{"role":"user","content":"hello"}],"max_tokens":1,"stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status after expiry=%d", response.StatusCode)
	}
}
