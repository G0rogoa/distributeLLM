package gateway_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"distserve/internal/cache"
	"distserve/internal/gateway"
	"distserve/internal/mockworker"
	"distserve/internal/registry"
	"distserve/internal/scheduler"
)

func setup(t *testing.T, timeout, prefill, decode time.Duration) (*httptest.Server, *mockworker.Worker) {
	t.Helper()
	worker := mockworker.New("mock-llm", prefill, decode)
	workerServer := httptest.NewServer(worker.Handler())
	t.Cleanup(workerServer.Close)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	controller := httptest.NewServer(gateway.New(workerServer.URL, "mock-llm", timeout, nil, logger).Handler())
	t.Cleanup(controller.Close)
	return controller, worker
}

func TestPrefixCacheColdThenHotEndToEnd(t *testing.T) {
	events := make(chan cache.CacheEvent, 100)
	worker := mockworker.NewWithConfig(mockworker.Config{Model: "mock-llm", Capacity: 2, QueueCapacity: 2, PrefillPerToken: time.Millisecond, Seed: 1})
	workerKey := cache.WorkerInstanceKey{WorkerID: "worker-1", InstanceID: "instance-1"}
	if err := worker.EnableCache(workerKey, 1<<20, 4, time.Minute, 0, events); err != nil {
		t.Fatal(err)
	}
	workerServer := httptest.NewServer(worker.Handler())
	defer workerServer.Close()
	workers := registry.New(time.Second, 2*time.Second)
	if err := workers.Register(registry.Worker{ID: workerKey.WorkerID, InstanceID: workerKey.InstanceID, Address: workerServer.URL, Models: []string{"mock-llm"}, Capacity: 2}); err != nil {
		t.Fatal(err)
	}
	if err := workers.Heartbeat(workerKey.WorkerID, workerKey.InstanceID, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	index, _ := cache.NewCacheIndex(100, 100, time.Minute)
	_ = index.SetWorkerInstance(workerKey.WorkerID, workerKey.InstanceID)
	identity := cache.CacheIdentity{ProtocolVersion: cache.PrefixProtocolVersion, ModelID: "mock-llm", ModelRevision: "v1", TokenizerID: "mock", TokenizerRevision: "v1", ChatTemplateVersion: "chat-v1", BlockSizeTokens: 4, CacheFormatVersion: "mock-kv-v1"}
	runtime := &cache.Runtime{Builder: cache.PromptBuilder{Identity: cache.PromptIdentity{ModelID: identity.ModelID, ModelRevision: identity.ModelRevision, TokenizerID: identity.TokenizerID, TokenizerRevision: identity.TokenizerRevision, ChatTemplateVersion: identity.ChatTemplateVersion}, MaxBytes: 1 << 20}, Tokenizer: &cache.DeterministicMockTokenizer{TokenizerID: cache.TokenizerIdentity{ID: "mock", Revision: "v1"}, MaxInputBytes: 1 << 20, MaxTokens: 1000}, Identity: identity, Index: index}
	strategy := &scheduler.PrefixAware{CacheWeight: 1, LoadWeight: 1, PrefillMSPerToken: 1}
	gw := gateway.NewDynamic(workers, strategy, "mock-llm", 2*time.Second, 8, false, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	gw.ConfigureCache(runtime, cache.NewFillReservations(time.Minute))
	controller := httptest.NewServer(gw.Handler())
	defer controller.Close()
	body := `{"model":"mock-llm","messages":[{"role":"user","content":"one two three four five six seven eight nine ten"}],"max_tokens":1,"stream":false}`
	for request := 0; request < 2; request++ {
		resp, err := http.Post(controller.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d status=%d", request, resp.StatusCode)
		}
		for len(events) > 0 {
			if _, err := index.Apply(<-events); err != nil {
				t.Fatal(err)
			}
		}
	}
	stats, _ := worker.CacheStats()
	if stats.Hits == 0 {
		t.Fatalf("second request did not reuse prefix: %+v", stats)
	}
	resp, err := http.Get(controller.URL + "/internal/debug/requests")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var records []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[1]["cache_actual_tokens"].(float64) == 0 {
		t.Fatalf("records=%v", records)
	}
}

func TestDynamicSchedulingReleasesReservation(t *testing.T) {
	worker := mockworker.New("mock-llm", time.Millisecond, time.Millisecond)
	workerServer := httptest.NewServer(worker.Handler())
	defer workerServer.Close()
	workers := registry.New(time.Second, 2*time.Second)
	if err := workers.Register(registry.Worker{ID: "worker-1", InstanceID: "instance-1", Address: workerServer.URL, Models: []string{"mock-llm"}, Capacity: 2}); err != nil {
		t.Fatal(err)
	}
	if err := workers.Heartbeat("worker-1", "instance-1", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	controller := httptest.NewServer(gateway.NewDynamic(workers, &scheduler.RoundRobin{}, "mock-llm", time.Second, 8, false, nil, logger).Handler())
	defer controller.Close()
	resp, err := http.Post(controller.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"mock-llm","messages":[{"role":"user","content":"hello"}],"max_tokens":2,"stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if got := workers.Snapshots()[0].LocalReservations; got != 0 {
		t.Fatalf("leaked reservations=%d", got)
	}
}

func TestRetriesOnceBeforeResponseStarts(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "busy", http.StatusServiceUnavailable) }))
	defer failing.Close()
	success := httptest.NewServer(mockworker.New("mock-llm", 0, 0).Handler())
	defer success.Close()
	workers := registry.New(time.Second, 2*time.Second)
	for _, item := range []registry.Worker{{ID: "a", InstanceID: "ia", Address: failing.URL, Models: []string{"mock-llm"}, Capacity: 2}, {ID: "b", InstanceID: "ib", Address: success.URL, Models: []string{"mock-llm"}, Capacity: 2}} {
		if err := workers.Register(item); err != nil {
			t.Fatal(err)
		}
		if err := workers.Heartbeat(item.ID, item.InstanceID, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	controller := httptest.NewServer(gateway.NewDynamic(workers, &scheduler.RoundRobin{}, "mock-llm", time.Second, 8, true, nil, logger).Handler())
	defer controller.Close()
	resp, err := http.Post(controller.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"mock-llm","messages":[{"role":"user","content":"hello"}],"max_tokens":1,"stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	for _, snapshot := range workers.Snapshots() {
		if snapshot.LocalReservations != 0 {
			t.Fatalf("worker %s leaked %d", snapshot.ID, snapshot.LocalReservations)
		}
	}
}

func TestNonStreamingEndToEnd(t *testing.T) {
	server, _ := setup(t, time.Second, time.Millisecond, time.Millisecond)
	resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"mock-llm","messages":[{"role":"user","content":"hello"}],"max_tokens":2,"stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("token-1")) {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Request-ID") == "" {
		t.Fatal("missing request ID")
	}
}

func TestStreamingFlushesFirstTokenAndDone(t *testing.T) {
	server, _ := setup(t, time.Second, 5*time.Millisecond, 80*time.Millisecond)
	resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"mock-llm","messages":[{"role":"user","content":"hello"}],"max_tokens":2,"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	first, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(first, "token-0") {
		t.Fatalf("first=%q err=%v", first, err)
	}
	rest, _ := io.ReadAll(reader)
	if !bytes.Contains(rest, []byte("data: [DONE]")) {
		t.Fatalf("missing DONE: %s", rest)
	}
}

func TestTimeoutReleasesWorker(t *testing.T) {
	server, worker := setup(t, 20*time.Millisecond, 200*time.Millisecond, time.Millisecond)
	resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"mock-llm","messages":[{"role":"user","content":"hello"}],"max_tokens":1,"stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	eventuallyInactive(t, worker)
}

func TestClientCancellationPropagates(t *testing.T) {
	server, worker := setup(t, time.Second, 200*time.Millisecond, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"mock-llm","messages":[{"role":"user","content":"hello"}],"max_tokens":1,"stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, _ := http.DefaultClient.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
	}()
	deadline := time.Now().Add(time.Second)
	for worker.Active() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	eventuallyInactive(t, worker)
}

func eventuallyInactive(t *testing.T, worker *mockworker.Worker) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for worker.Active() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if worker.Active() != 0 {
		t.Fatal("worker request did not stop")
	}
}
