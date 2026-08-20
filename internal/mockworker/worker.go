package mockworker

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"distserve/internal/api"
	"distserve/internal/backend"
	"distserve/internal/cache"
)

type Worker struct {
	Model                string
	PrefillDelay         time.Duration
	DecodeInterval       time.Duration
	PrefillPerToken      time.Duration
	ConcurrencyPenalty   time.Duration
	Jitter               time.Duration
	FailureRate          float64
	capacity             chan struct{}
	queueSlots           chan struct{}
	active               atomic.Int64
	queued               atomic.Int64
	randomMu             sync.Mutex
	random               *rand.Rand
	mockCache            *cache.MockCache
	cacheBytesPerToken   int64
	cacheLease           time.Duration
	cacheLookupDelay     time.Duration
	cacheEvents          chan<- cache.CacheEvent
	droppedCacheEvents   atomic.Int64
	prefillTokens        atomic.Int64
	prefillTokensSkipped atomic.Int64
}

type Config struct {
	Model              string
	Capacity           int
	QueueCapacity      int
	PrefillDelay       time.Duration
	PrefillPerToken    time.Duration
	DecodeInterval     time.Duration
	ConcurrencyPenalty time.Duration
	Jitter             time.Duration
	FailureRate        float64
	Seed               int64
}

func New(model string, prefillDelay, decodeInterval time.Duration) *Worker {
	return NewWithConfig(Config{Model: model, Capacity: 8, QueueCapacity: 32, PrefillDelay: prefillDelay, DecodeInterval: decodeInterval, Seed: 1})
}

func NewWithConfig(config Config) *Worker {
	if config.Capacity < 1 {
		config.Capacity = 1
	}
	if config.QueueCapacity < 0 {
		config.QueueCapacity = 0
	}
	if config.Seed == 0 {
		config.Seed = time.Now().UnixNano()
	}
	return &Worker{Model: config.Model, PrefillDelay: config.PrefillDelay, PrefillPerToken: config.PrefillPerToken, DecodeInterval: config.DecodeInterval, ConcurrencyPenalty: config.ConcurrencyPenalty, Jitter: config.Jitter, FailureRate: config.FailureRate, capacity: make(chan struct{}, config.Capacity), queueSlots: make(chan struct{}, config.QueueCapacity), random: rand.New(rand.NewSource(config.Seed))}
}

func (w *Worker) Active() int64 { return w.active.Load() }
func (w *Worker) Queued() int64 { return w.queued.Load() }

func (w *Worker) EnableCache(worker cache.WorkerInstanceKey, capacityBytes, bytesPerToken int64, lease, lookupDelay time.Duration, events chan<- cache.CacheEvent) error {
	if bytesPerToken <= 0 || lease <= 0 {
		return cache.ErrInvalidCacheEvent
	}
	mock, err := cache.NewMockCache(worker, capacityBytes)
	if err != nil {
		return err
	}
	w.mockCache = mock
	w.cacheBytesPerToken = bytesPerToken
	w.cacheLease = lease
	w.cacheLookupDelay = lookupDelay
	w.cacheEvents = events
	return nil
}

func (w *Worker) CacheStats() (cache.MockCacheStats, bool) {
	if w.mockCache == nil {
		return cache.MockCacheStats{}, false
	}
	return w.mockCache.Stats(), true
}
func (w *Worker) DroppedCacheEvents() int64 { return w.droppedCacheEvents.Load() }

func (w *Worker) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(out http.ResponseWriter, _ *http.Request) { out.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /v1/chat/completions", w.chatCompletions)
	mux.HandleFunc("GET /metrics", w.metrics)
	return mux
}

func (w *Worker) metrics(out http.ResponseWriter, _ *http.Request) {
	out.Header().Set("Content-Type", "text/plain; version=0.0.4")
	stats, _ := w.CacheStats()
	fmt.Fprintf(out, "distserve_mock_cache_hits_total %d\n", stats.Hits)
	fmt.Fprintf(out, "distserve_mock_cache_misses_total %d\n", stats.Misses)
	fmt.Fprintf(out, "distserve_mock_cache_evictions_total %d\n", stats.Evictions)
	fmt.Fprintf(out, "distserve_mock_cache_used_bytes %d\n", stats.UsedBytes)
	fmt.Fprintf(out, "distserve_mock_cache_capacity_bytes %d\n", stats.CapacityBytes)
	fmt.Fprintf(out, "distserve_mock_cache_events_dropped_total %d\n", w.DroppedCacheEvents())
	fmt.Fprintf(out, "distserve_mock_prefill_tokens_total %d\n", w.prefillTokens.Load())
	fmt.Fprintf(out, "distserve_mock_prefill_tokens_skipped_total %d\n", w.prefillTokensSkipped.Load())
}

func (w *Worker) chatCompletions(out http.ResponseWriter, r *http.Request) {
	var input backend.ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Model != w.Model || input.MaxTokens < 1 {
		http.Error(out, "invalid request", http.StatusBadRequest)
		return
	}
	select {
	case w.capacity <- struct{}{}:
	default:
		select {
		case w.queueSlots <- struct{}{}:
		default:
			http.Error(out, "worker queue full", http.StatusServiceUnavailable)
			return
		}
		w.queued.Add(1)
		select {
		case w.capacity <- struct{}{}:
			w.queued.Add(-1)
			<-w.queueSlots
		case <-r.Context().Done():
			w.queued.Add(-1)
			<-w.queueSlots
			return
		}
	}
	w.active.Add(1)
	defer func() { w.active.Add(-1); <-w.capacity }()
	if w.shouldFail() {
		http.Error(out, "injected worker failure", http.StatusServiceUnavailable)
		return
	}
	inputTokens := 0
	for _, message := range input.Messages {
		inputTokens += (len(message.Content) + 3) / 4
	}
	if input.CacheHint != nil {
		inputTokens = input.CacheHint.TotalInputTokens
	}
	actualMatchedBlocks, actualMatchedTokens := 0, 0
	if w.mockCache != nil && input.CacheHint != nil {
		var events []cache.CacheEvent
		// The Controller hint is a prediction. The Worker checks every requested
		// block because its local cache is the final authority.
		actualMatchedBlocks, actualMatchedTokens, events, _ = w.mockCache.Lookup(input.CacheHint.Identity, input.CacheHint.PrefixBlocks, time.Now(), w.cacheLease)
		w.publishCacheEvents(events)
	}
	out.Header().Set("X-DistServe-Actual-Hit-Blocks", fmt.Sprint(actualMatchedBlocks))
	out.Header().Set("X-DistServe-Actual-Hit-Tokens", fmt.Sprint(actualMatchedTokens))
	uncachedTokens := inputTokens - actualMatchedTokens
	if uncachedTokens < 0 {
		uncachedTokens = 0
	}
	w.prefillTokens.Add(int64(uncachedTokens))
	w.prefillTokensSkipped.Add(int64(actualMatchedTokens))
	prefill := w.PrefillDelay + time.Duration(uncachedTokens)*w.PrefillPerToken + w.cacheLookupDelay + w.jitter()
	if !wait(r, prefill) {
		return
	}
	if w.mockCache != nil && input.CacheHint != nil {
		events, _ := w.mockCache.Fill(input.CacheHint.Identity, input.CacheHint.PrefixBlocks, w.cacheBytesPerToken, time.Now(), w.cacheLease)
		w.publishCacheEvents(events)
	}
	if input.Stream {
		w.stream(out, r, input.ChatCompletionRequest)
		return
	}
	if !wait(r, time.Duration(input.MaxTokens)*w.decodeDelay()) {
		return
	}
	finish := "stop"
	response := api.ChatCompletionResponse{ID: r.Header.Get("X-Request-ID"), Object: "chat.completion", Model: w.Model, Choices: []api.Choice{{Index: 0, Message: &api.Message{Role: "assistant", Content: tokenText(input.MaxTokens)}, FinishReason: &finish}}}
	out.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(out).Encode(response)
}

func (w *Worker) publishCacheEvents(events []cache.CacheEvent) {
	for _, event := range events {
		select {
		case w.cacheEvents <- event:
		default:
			w.droppedCacheEvents.Add(1)
		}
	}
}

func (w *Worker) stream(out http.ResponseWriter, r *http.Request, input api.ChatCompletionRequest) {
	flusher, ok := out.(http.Flusher)
	if !ok {
		http.Error(out, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	out.Header().Set("Content-Type", "text/event-stream")
	out.Header().Set("Cache-Control", "no-cache")
	for i := 0; i < input.MaxTokens; i++ {
		if i > 0 && !wait(r, w.decodeDelay()) {
			return
		}
		chunk := api.ChatCompletionResponse{ID: r.Header.Get("X-Request-ID"), Object: "chat.completion.chunk", Model: w.Model, Choices: []api.Choice{{Index: 0, Delta: &api.Message{Role: "assistant", Content: fmt.Sprintf("token-%d ", i)}}}}
		encoded, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(out, "data: %s\n\n", encoded)
		flusher.Flush()
	}
	_, _ = fmt.Fprint(out, "data: [DONE]\n\n")
	flusher.Flush()
}

func (w *Worker) decodeDelay() time.Duration {
	delay := w.DecodeInterval + time.Duration(w.Active()-1)*w.ConcurrencyPenalty + w.jitter()
	if delay < 0 {
		return 0
	}
	return delay
}

func (w *Worker) jitter() time.Duration {
	if w.Jitter <= 0 {
		return 0
	}
	w.randomMu.Lock()
	value := time.Duration(w.random.Int63n(int64(w.Jitter)*2+1)) - w.Jitter
	w.randomMu.Unlock()
	return value
}

func (w *Worker) shouldFail() bool {
	if w.FailureRate <= 0 {
		return false
	}
	w.randomMu.Lock()
	value := w.random.Float64()
	w.randomMu.Unlock()
	return value < w.FailureRate
}

func wait(r *http.Request, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-r.Context().Done():
		return false
	}
}

func tokenText(count int) string {
	parts := make([]string, count)
	for i := range parts {
		parts[i] = fmt.Sprintf("token-%d", i)
	}
	return strings.Join(parts, " ")
}
