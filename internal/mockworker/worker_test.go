package mockworker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
