package backend

import (
	"context"
	"distserve/internal/api"
	"distserve/internal/cache"
	"distserve/internal/registry"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// ChatCompletionRequest is Controller-to-Worker transport, not the public API DTO.
type ChatCompletionRequest struct {
	api.ChatCompletionRequest
	CacheHint *cache.RoutingHint `json:"distserve_cache,omitempty"`
}

type Type string

const (
	TypeMock Type = "mock"
	TypeVLLM Type = "vllm"
)

type ErrorKind string

const (
	ErrorConnection ErrorKind = "connection"
	ErrorTimeout    ErrorKind = "timeout"
	ErrorBackend4xx ErrorKind = "backend_4xx"
	ErrorBackend5xx ErrorKind = "backend_5xx"
	ErrorOther      ErrorKind = "other"
)

type BackendError struct {
	Kind       ErrorKind
	StatusCode int
	Err        error
}

func (e *BackendError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("%s: backend status %d", e.Kind, e.StatusCode)
	}
	return fmt.Sprintf("%s: %v", e.Kind, e.Err)
}

func (e *BackendError) Unwrap() error { return e.Err }

func Classify(err error) ErrorKind {
	var backendErr *BackendError
	if errors.As(err, &backendErr) {
		return backendErr.Kind
	}
	return ErrorOther
}

type ChatRequest struct {
	Body      io.Reader
	Header    http.Header
	RequestID string
}

type Backend interface {
	ChatCompletions(ctx context.Context, worker registry.WorkerSnapshot, request ChatRequest) (*http.Response, error)
	Health(ctx context.Context, worker registry.WorkerSnapshot) error
}

type OpenAIHTTP struct {
	Client *http.Client
}

func (b OpenAIHTTP) ChatCompletions(ctx context.Context, worker registry.WorkerSnapshot, request ChatRequest) (*http.Response, error) {
	upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(worker.Address, "/")+"/v1/chat/completions", request.Body)
	if err != nil {
		return nil, &BackendError{Kind: ErrorOther, Err: err}
	}
	copyForwardHeaders(upstream.Header, request.Header)
	if request.RequestID != "" {
		upstream.Header.Set("X-Request-ID", request.RequestID)
	}
	if worker.ID != "" {
		upstream.Header.Set("X-DistServe-Worker-ID", worker.ID)
	}
	resp, err := httpClient(b.Client).Do(upstream)
	if err != nil {
		return nil, classifyDoError(ctx, err)
	}
	if resp.StatusCode >= 400 && resp.StatusCode <= 499 {
		return resp, &BackendError{Kind: ErrorBackend4xx, StatusCode: resp.StatusCode}
	}
	if resp.StatusCode >= 500 {
		return resp, &BackendError{Kind: ErrorBackend5xx, StatusCode: resp.StatusCode}
	}
	return resp, nil
}

func (b OpenAIHTTP) Health(ctx context.Context, worker registry.WorkerSnapshot) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(worker.Address, "/")+"/health", nil)
	if err != nil {
		return &BackendError{Kind: ErrorOther, Err: err}
	}
	resp, err := httpClient(b.Client).Do(request)
	if err != nil {
		return classifyDoError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	if resp.StatusCode >= 400 && resp.StatusCode <= 499 {
		return &BackendError{Kind: ErrorBackend4xx, StatusCode: resp.StatusCode}
	}
	return &BackendError{Kind: ErrorBackend5xx, StatusCode: resp.StatusCode}
}

func copyForwardHeaders(dst, src http.Header) {
	for _, name := range []string{"Content-Type", "Accept", "X-Request-ID"} {
		if value := src.Values(name); len(value) > 0 {
			dst[name] = append([]string(nil), value...)
		}
	}
	if value := src.Values("Authorization"); len(value) > 0 {
		dst["Authorization"] = append([]string(nil), value...)
	}
}

func classifyDoError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &BackendError{Kind: ErrorTimeout, Err: err}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &BackendError{Kind: ErrorTimeout, Err: err}
	}
	return &BackendError{Kind: ErrorConnection, Err: err}
}

func httpClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return http.DefaultClient
}
