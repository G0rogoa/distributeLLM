package mockworker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"distserve/internal/api"
	"distserve/internal/backend"
	"distserve/internal/cache"
)

const requestBody = `{"model":"mock-llm","messages":[{"role":"user","content":"hello"}],"max_tokens":1,"stream":false}`

func TestQueueBoundAndSlotRelease(t *testing.T) {
	worker := NewWithConfig(Config{Model: "mock-llm", Capacity: 1, QueueCapacity: 1, PrefillDelay: 100 * time.Millisecond, Seed: 1})
	server := httptest.NewServer(worker.Handler())
	defer server.Close()
	done := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(requestBody))
			if err == nil {
				resp.Body.Close()
			}
			done <- struct{}{}
		}()
	}
	deadline := time.Now().Add(time.Second)
	for (worker.Active() != 1 || worker.Queued() != 1) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if worker.Active() != 1 || worker.Queued() != 1 {
		t.Fatal("worker did not reach full running and queue capacity")
	}
	resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	<-done
	<-done
	if worker.Active() != 0 || worker.Queued() != 0 {
		t.Fatalf("active=%d queued=%d", worker.Active(), worker.Queued())
	}
}

func TestWorkerCacheActualHitAndEvents(t *testing.T) {
	events := make(chan cache.CacheEvent, 20)
	worker := NewWithConfig(Config{Model: "mock-llm", Capacity: 1, QueueCapacity: 1, PrefillDelay: time.Millisecond, PrefillPerToken: time.Millisecond, Seed: 1})
	if err := worker.EnableCache(cache.WorkerInstanceKey{WorkerID: "w", InstanceID: "i"}, 1024, 4, time.Minute, 0, events); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(worker.Handler())
	defer server.Close()
	identity := cache.CacheIdentity{ProtocolVersion: cache.PrefixProtocolVersion, ModelID: "mock-llm", ModelRevision: "v1", TokenizerID: "mock", TokenizerRevision: "v1", ChatTemplateVersion: "v1", BlockSizeTokens: 4, CacheFormatVersion: "mock-v1", KVLayout: "test-fp16"}
	blocks, _ := cache.BuildTokenBlocks([]cache.TokenID{1, 2, 3, 4}, 4, 10)
	chain, _ := cache.BuildPrefixChain(identity, blocks)
	send := func(predicted int) *http.Response {
		payload := backend.ChatCompletionRequest{ChatCompletionRequest: api.ChatCompletionRequest{Model: "mock-llm", Messages: []api.Message{{Role: "user", Content: "long enough input"}}, MaxTokens: 1}, CacheHint: &cache.RoutingHint{Identity: identity, PrefixBlocks: chain, TotalInputTokens: 4, PredictedMatchedBlocks: predicted, PredictedMatchedTokens: predicted * 4}}
		body, _ := json.Marshal(payload)
		response, err := http.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	first := send(0)
	first.Body.Close()
	if first.StatusCode != 200 {
		t.Fatalf("first status=%d", first.StatusCode)
	}
	select {
	case event := <-events:
		if event.Type != cache.CacheEventAdd {
			t.Fatalf("event=%+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("missing add event")
	}
	// Even a predicted miss must discover the Worker's authoritative local hit.
	second := send(0)
	second.Body.Close()
	if second.Header.Get("X-DistServe-Actual-Hit-Blocks") != "1" || second.Header.Get("X-DistServe-Actual-Hit-Tokens") != "4" {
		t.Fatalf("headers=%v", second.Header)
	}
}

func TestWorkerCancellationDoesNotFillCache(t *testing.T) {
	events := make(chan cache.CacheEvent, 10)
	worker := NewWithConfig(Config{Model: "mock-llm", Capacity: 1, PrefillDelay: 200 * time.Millisecond, Seed: 1})
	_ = worker.EnableCache(cache.WorkerInstanceKey{WorkerID: "w", InstanceID: "i"}, 1024, 4, time.Minute, 0, events)
	identity := cache.CacheIdentity{ProtocolVersion: cache.PrefixProtocolVersion, ModelID: "mock-llm", ModelRevision: "v1", TokenizerID: "mock", TokenizerRevision: "v1", ChatTemplateVersion: "v1", BlockSizeTokens: 4, CacheFormatVersion: "mock-v1", KVLayout: "test-fp16"}
	blocks, _ := cache.BuildTokenBlocks([]cache.TokenID{1, 2, 3, 4}, 4, 10)
	chain, _ := cache.BuildPrefixChain(identity, blocks)
	payload := backend.ChatCompletionRequest{ChatCompletionRequest: api.ChatCompletionRequest{Model: "mock-llm", Messages: []api.Message{{Role: "user", Content: "input"}}, MaxTokens: 1}, CacheHint: &cache.RoutingHint{Identity: identity, PrefixBlocks: chain}}
	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)).WithContext(ctx)
	done := make(chan struct{})
	go func() { worker.Handler().ServeHTTP(httptest.NewRecorder(), request); close(done) }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	<-done
	if stats, _ := worker.CacheStats(); stats.Entries != 0 {
		t.Fatalf("cache filled after cancellation: %+v", stats)
	}
	if len(events) != 0 {
		t.Fatalf("events=%d", len(events))
	}
}

func TestCancellationReleasesQueue(t *testing.T) {
	worker := NewWithConfig(Config{Model: "mock-llm", Capacity: 1, QueueCapacity: 1, PrefillDelay: 200 * time.Millisecond, Seed: 1})
	server := httptest.NewServer(worker.Handler())
	defer server.Close()
	go func() {
		resp, _ := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(requestBody))
		if resp != nil {
			resp.Body.Close()
		}
	}()
	deadline := time.Now().Add(time.Second)
	for worker.Active() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(requestBody))
	done := make(chan struct{})
	go func() { http.DefaultClient.Do(req); close(done) }()
	for worker.Queued() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	deadline = time.Now().Add(time.Second)
	for worker.Queued() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if worker.Queued() != 0 {
		t.Fatal("queued slot leaked")
	}
}

func TestFailureInjection(t *testing.T) {
	worker := NewWithConfig(Config{Model: "mock-llm", Capacity: 1, FailureRate: 1, Seed: 7})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(requestBody))
	worker.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", recorder.Code)
	}
}
