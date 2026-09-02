package backend_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"distserve/internal/backend"
	"distserve/internal/registry"
)

func TestOpenAIHTTPForwardsAllowedHeaders(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	worker := registry.WorkerSnapshot{Address: server.URL}
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "text/event-stream")
	headers.Set("X-Request-ID", "req-1")
	headers.Set("Authorization", "Bearer secret")
	headers.Set("Connection", "close")
	resp, err := (backend.OpenAIHTTP{}).ChatCompletions(context.Background(), worker, backend.ChatRequest{Body: strings.NewReader(`{}`), Header: headers, RequestID: "req-1"})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got.Get("Content-Type") != "application/json" || got.Get("Accept") != "text/event-stream" || got.Get("X-Request-ID") != "req-1" {
		t.Fatalf("headers not forwarded: %v", got)
	}
	if got.Get("Authorization") != "Bearer secret" {
		t.Fatal("authorization was not forwarded")
	}
	if got.Get("Connection") != "" {
		t.Fatalf("hop-by-hop header forwarded: %q", got.Get("Connection"))
	}
}

func TestOpenAIHTTPClassifiesStatusAndTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad", http.StatusInternalServerError)
	}))
	defer server.Close()
	worker := registry.WorkerSnapshot{Address: server.URL}
	resp, err := (backend.OpenAIHTTP{}).ChatCompletions(context.Background(), worker, backend.ChatRequest{Body: strings.NewReader(`{}`)})
	if resp != nil {
		resp.Body.Close()
	}
	if backend.Classify(err) != backend.ErrorBackend5xx {
		t.Fatalf("kind=%s err=%v", backend.Classify(err), err)
	}

	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer slowServer.Close()
	client := &http.Client{Timeout: time.Millisecond}
	slowWorker := registry.WorkerSnapshot{Address: slowServer.URL}
	resp, err = (backend.OpenAIHTTP{Client: client}).ChatCompletions(context.Background(), slowWorker, backend.ChatRequest{Body: strings.NewReader(`{}`)})
	if resp != nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) && backend.Classify(err) != backend.ErrorTimeout {
		t.Fatalf("kind=%s err=%v", backend.Classify(err), err)
	}
}
