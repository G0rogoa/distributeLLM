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
	"sync"
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
	identity := cache.CacheIdentity{ProtocolVersion: cache.PrefixProtocolVersion, ModelID: "mock-llm", ModelRevision: "v1", TokenizerID: "mock", TokenizerRevision: "v1", ChatTemplateVersion: "chat-v1", BlockSizeTokens: 4, CacheFormatVersion: "mock-kv-v1", KVLayout: "test-fp16"}
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

func TestVLLMBackendReceivesPlainOpenAIRequest(t *testing.T) {
	var received map[string]any
	vllm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"cmpl","object":"chat.completion","model":"mock-llm","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer vllm.Close()
	workers := registry.New(time.Second, 2*time.Second)
	if err := workers.Register(registry.Worker{ID: "real-1", InstanceID: "instance-1", Address: vllm.URL, Models: []string{"mock-llm"}, BackendType: "vllm", Model: "mock-llm", Capacity: 1}); err != nil {
		t.Fatal(err)
	}
	if err := workers.Heartbeat("real-1", "instance-1", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	identity := cache.CacheIdentity{ProtocolVersion: cache.PrefixProtocolVersion, ModelID: "mock-llm", ModelRevision: "v1", TokenizerID: "mock", TokenizerRevision: "v1", ChatTemplateVersion: "chat-v1", BlockSizeTokens: 4, CacheFormatVersion: "mock-kv-v1", KVLayout: "test-fp16"}
	runtime := &cache.Runtime{Builder: cache.PromptBuilder{Identity: cache.PromptIdentity{ModelID: identity.ModelID, ModelRevision: identity.ModelRevision, TokenizerID: identity.TokenizerID, TokenizerRevision: identity.TokenizerRevision, ChatTemplateVersion: identity.ChatTemplateVersion}, MaxBytes: 1 << 20}, Tokenizer: &cache.DeterministicMockTokenizer{TokenizerID: cache.TokenizerIdentity{ID: "mock", Revision: "v1"}, MaxInputBytes: 1 << 20, MaxTokens: 1000}, Identity: identity}
	gw := gateway.NewDynamic(workers, &scheduler.RoundRobin{}, "mock-llm", time.Second, 8, false, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	gw.ConfigureCache(runtime, nil)
	controller := httptest.NewServer(gw.Handler())
	defer controller.Close()
	resp, err := http.Post(controller.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"mock-llm","messages":[{"role":"user","content":"hello"}],"max_tokens":1,"stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if _, ok := received["distserve_cache"]; ok {
		t.Fatalf("real backend received mock cache hint: %v", received)
	}
}

func TestMultipleVLLMWorkersRoundRobinAndDebugDecisions(t *testing.T) {
	servers := map[string]*countingVLLM{}
	workers := registry.New(time.Second, 2*time.Second)
	for _, id := range []string{"real-a", "real-b", "real-c"} {
		server := newCountingVLLM(t)
		servers[id] = server
		if err := workers.Register(registry.Worker{ID: id, InstanceID: id + "-instance", Address: server.URL(), Models: []string{"mock-llm"}, BackendType: "vllm", Model: "mock-llm", Capacity: 4}); err != nil {
			t.Fatal(err)
		}
		if err := workers.Heartbeat(id, id+"-instance", 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	gw := gateway.NewDynamic(workers, &scheduler.RoundRobin{}, "mock-llm", time.Second, 8, false, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	controller := httptest.NewServer(gw.Handler())
	defer controller.Close()
	seen := map[string]int{}
	for i := 0; i < 6; i++ {
		req, _ := http.NewRequest(http.MethodPost, controller.URL+"/v1/chat/completions", strings.NewReader(`{"model":"mock-llm","messages":[{"role":"user","content":"hello"}],"max_tokens":1,"stream":false}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d", resp.StatusCode)
		}
		workerID := resp.Header.Get("X-DistServe-Worker-ID")
		if workerID == "" {
			t.Fatal("missing selected worker header")
		}
		seen[workerID]++
	}
	for id, server := range servers {
		if seen[id] == 0 || server.Count() == 0 {
			t.Fatalf("worker %s was not selected: headers=%v hits=%d", id, seen, server.Count())
		}
	}
	resp, err := http.Get(controller.URL + "/internal/debug/decisions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var records []gateway.DecisionRecord
	if err := json.NewDecoder(resp.Body).Decode(&records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 6 || len(records[len(records)-1].Candidates) != 3 {
		t.Fatalf("decision records=%+v", records)
	}
}

func TestVLLMWorkerFailureDoesNotLeakReservations(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	success := newCountingVLLM(t)
	workers := registry.New(time.Second, 2*time.Second)
	for _, item := range []registry.Worker{
		{ID: "real-a", InstanceID: "ia", Address: failing.URL, Models: []string{"mock-llm"}, BackendType: "vllm", Model: "mock-llm", Capacity: 1},
		{ID: "real-b", InstanceID: "ib", Address: success.URL(), Models: []string{"mock-llm"}, BackendType: "vllm", Model: "mock-llm", Capacity: 1},
	} {
		if err := workers.Register(item); err != nil {
			t.Fatal(err)
		}
		if err := workers.Heartbeat(item.ID, item.InstanceID, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	gw := gateway.NewDynamic(workers, &scheduler.RoundRobin{}, "mock-llm", time.Second, 8, true, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	controller := httptest.NewServer(gw.Handler())
	defer controller.Close()
	resp, err := http.Post(controller.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"mock-llm","messages":[{"role":"user","content":"hello"}],"max_tokens":1,"stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("X-DistServe-Worker-ID") != "real-b" {
		t.Fatalf("status=%d selected=%s", resp.StatusCode, resp.Header.Get("X-DistServe-Worker-ID"))
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

type countingVLLM struct {
	server *httptest.Server
	mu     sync.Mutex
	count  int
}

func newCountingVLLM(t *testing.T) *countingVLLM {
	t.Helper()
	value := &countingVLLM{}
	value.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value.mu.Lock()
		value.count++
		value.mu.Unlock()
		var received map[string]any
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		if _, ok := received["distserve_cache"]; ok {
			t.Fatalf("vLLM backend received distserve_cache: %v", received)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"cmpl","object":"chat.completion","model":"mock-llm","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(value.server.Close)
	return value
}

func (v *countingVLLM) URL() string { return v.server.URL }

func (v *countingVLLM) Count() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.count
}
