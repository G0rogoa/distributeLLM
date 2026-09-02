package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"distserve/internal/admission"
	"distserve/internal/api"
	"distserve/internal/backend"
	"distserve/internal/cache"
	"distserve/internal/lifecycle"
	"distserve/internal/registry"
	"distserve/internal/scheduler"
	"distserve/internal/telemetry"
)

type Gateway struct {
	backendURL       string
	model            string
	timeout          time.Duration
	client           *http.Client
	log              *slog.Logger
	sequence         atomic.Uint64
	registry         *registry.Registry
	scheduler        scheduler.Scheduler
	admission        *admission.Limiter
	requests         *lifecycle.Store
	retry            bool
	metrics          *telemetry.Metrics
	cacheRuntime     *cache.Runtime
	fillReservations *cache.FillReservations
	shadowAffinity   *cache.AffinityIndex
	backend          backend.Backend
	decisions        *decisionStore
}

func (g *Gateway) ConfigureCache(runtime *cache.Runtime, fills *cache.FillReservations) {
	g.cacheRuntime = runtime
	g.fillReservations = fills
}

func (g *Gateway) ConfigureShadowAffinity(index *cache.AffinityIndex) {
	g.shadowAffinity = index
}

func NewDynamic(workerRegistry *registry.Registry, strategy scheduler.Scheduler, model string, timeout time.Duration, maxInFlight int, retry bool, client *http.Client, logger *slog.Logger) *Gateway {
	g := New("", model, timeout, client, logger)
	g.registry = workerRegistry
	g.scheduler = strategy
	g.admission = admission.New(maxInFlight)
	g.retry = retry
	return g
}

func New(backendURL, model string, timeout time.Duration, client *http.Client, logger *slog.Logger) *Gateway {
	if client == nil {
		client = &http.Client{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Gateway{backendURL: strings.TrimRight(backendURL, "/"), model: model, timeout: timeout, client: client, log: logger, admission: admission.New(128), requests: lifecycle.New(1024), metrics: &telemetry.Metrics{}, backend: backend.OpenAIHTTP{Client: client}, decisions: newDecisionStore(1024)}
}

func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/chat/completions", g.chatCompletions)
	mux.HandleFunc("GET /internal/debug/requests", func(w http.ResponseWriter, _ *http.Request) {
		g.DebugRequests(w)
	})
	mux.HandleFunc("GET /internal/debug/decisions", func(w http.ResponseWriter, _ *http.Request) {
		g.DebugDecisions(w)
	})
	mux.HandleFunc("GET /internal/cache/requests/{id}", func(w http.ResponseWriter, r *http.Request) {
		request, ok := g.requests.Find(r.PathValue("id"))
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "request_not_found", "request not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"snapshot_time": time.Now(), "stale_possible": true, "request": request})
	})
	var snapshots func() []registry.WorkerSnapshot
	if g.registry != nil {
		snapshots = g.registry.Snapshots
	}
	var cacheStats func() cache.CacheStats
	if g.cacheRuntime != nil && g.cacheRuntime.Index != nil {
		cacheStats = g.cacheRuntime.Index.Stats
	}
	mux.HandleFunc("GET /metrics", g.metrics.Handler(snapshots, cacheStats))
	return mux
}

func (g *Gateway) DebugRequests(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, g.requests.Snapshot())
}

func (g *Gateway) DebugDecisions(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, g.decisions.Snapshot())
}

func (g *Gateway) chatCompletions(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = fmt.Sprintf("req-%d-%d", started.UnixNano(), g.sequence.Add(1))
	}
	w.Header().Set("X-Request-ID", requestID)
	record := lifecycle.Request{RequestID: requestID, ReceivedAt: started}
	g.metrics.Requests.Add(1)
	g.metrics.Inflight.Add(1)
	defer func() {
		g.metrics.Inflight.Add(-1)
		g.metrics.ObserveRequest(time.Since(started).Seconds())
		if record.FinalStatus == "" {
			record.FinalStatus = "failed"
			record.FailedAt = time.Now()
			if r.Context().Err() != nil {
				record.FinalStatus = "cancelled"
			}
		}
		g.requests.Add(record)
	}()

	var input api.ChatCompletionRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid_json", "invalid request body")
		return
	}
	if input.Model != g.model {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "model_not_supported", "unsupported model")
		return
	}
	if len(input.Messages) == 0 || input.MaxTokens < 1 || input.MaxTokens > 4096 {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid_parameters", "messages are required and max_tokens must be between 1 and 4096")
		return
	}
	releaseAdmission, admitted := g.admission.Acquire()
	if !admitted {
		g.metrics.AdmissionRejections.Add(1)
		record.FinalStatus = "admission_rejected"
		record.FailedAt = time.Now()
		writeError(w, http.StatusTooManyRequests, "admission_error", "controller_overloaded", "controller in-flight limit reached")
		return
	}
	defer releaseAdmission()
	record.AdmittedAt = time.Now()

	ctx, cancel := context.WithTimeout(r.Context(), g.timeout)
	defer cancel()
	record.InputTokens = approximateInputTokens(input.Messages)
	var features *cache.RequestFeatures
	var err error
	if g.cacheRuntime != nil {
		messages := make([]cache.PromptMessage, len(input.Messages))
		for i, message := range input.Messages {
			messages[i] = cache.PromptMessage{Role: message.Role, Content: message.Content}
		}
		features, err = g.cacheRuntime.Prepare(ctx, messages)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request_error", "prompt_processing_failed", err.Error())
			return
		}
		if features.TotalInputTokens > 0 {
			record.InputTokens = features.TotalInputTokens
		}
		record.CacheFullBlocks = len(features.PrefixBlocks)
	}
	var resp *http.Response
	var release func()
	scheduleFailed := false
	maxAttempts := 1
	if g.retry {
		maxAttempts = 2
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		worker, decision, selectedRelease, selectErr := g.selectWorker(ctx, requestID, input, started, features)
		if selectErr != nil {
			err = selectErr
			scheduleFailed = true
			break
		}
		release = selectedRelease
		record.ScheduledAt = time.Now()
		record.SelectedWorker, record.SelectedInstance, record.SchedulerStrategy = decision.WorkerID, decision.InstanceID, decision.Strategy
		record.BackendType = worker.BackendType
		if record.BackendType == "" {
			record.BackendType = string(backend.TypeMock)
		}
		record.CachePredictedBlocks, record.CachePredictedTokens = decision.CacheMatch.MatchedBlocks, decision.CacheMatch.MatchedTokens
		record.CacheEvidence = string(decision.CacheMatch.Evidence)
		record.ShadowAffinityMatch = decision.CacheMatch.Evidence == cache.EvidenceShadowEstimated
		if record.ShadowAffinityMatch {
			g.metrics.ShadowAffinityMatches.Add(1)
		}
		record.CacheViewState = string(decision.CacheMatch.CacheViewState)
		w.Header().Set("X-DistServe-Worker-ID", decision.WorkerID)
		w.Header().Set("X-DistServe-Instance-ID", decision.InstanceID)
		w.Header().Set("X-DistServe-Backend-Type", record.BackendType)
		bodyValue := any(input)
		if worker.BackendType == "" || worker.BackendType == string(backend.TypeMock) {
			transport := backend.ChatCompletionRequest{ChatCompletionRequest: input}
			if features != nil {
				transport.CacheHint = &cache.RoutingHint{Identity: features.Identity, PrefixBlocks: features.PrefixBlocks, TotalInputTokens: features.TotalInputTokens, PredictedMatchedBlocks: decision.CacheMatch.MatchedBlocks, PredictedMatchedTokens: decision.CacheMatch.MatchedTokens}
			}
			bodyValue = transport
		}
		body, marshalErr := json.Marshal(bodyValue)
		if marshalErr != nil {
			release()
			err = marshalErr
			break
		}
		var releaseFill func()
		if g.fillReservations != nil && features != nil && len(features.PrefixBlocks) > 0 && decision.CacheMatch.MatchedBlocks < len(features.PrefixBlocks) {
			identityHash, hashErr := features.Identity.Hash()
			if hashErr == nil {
				last := features.PrefixBlocks[len(features.PrefixBlocks)-1]
				if done, ok := g.fillReservations.Reserve(cache.CacheKey{IdentityHash: identityHash, PrefixHash: last.PrefixHash}, cache.WorkerInstanceKey{WorkerID: decision.WorkerID, InstanceID: decision.InstanceID}, requestID); ok {
					releaseFill = done
					record.CacheFillReserved = true
				}
			}
		}
		header := r.Header.Clone()
		header.Set("Content-Type", "application/json")
		header.Set("X-Request-ID", requestID)
		resp, err = g.backend.ChatCompletions(ctx, worker, backend.ChatRequest{Body: bytes.NewReader(body), Header: header, RequestID: requestID})
		if releaseFill != nil {
			releaseFill()
		}
		record.ForwardedAt = time.Now()
		retryable := backend.Classify(err) == backend.ErrorConnection || backend.Classify(err) == backend.ErrorTimeout || (resp != nil && resp.StatusCode == http.StatusServiceUnavailable)
		if !retryable || attempt+1 == maxAttempts || ctx.Err() != nil {
			break
		}
		if resp != nil {
			resp.Body.Close()
			resp = nil
		}
		release()
		release = nil
		record.RetryCount++
		g.metrics.Retries.Add(1)
	}
	if scheduleFailed {
		writeError(w, http.StatusServiceUnavailable, "scheduler_error", "no_available_worker", "no available worker")
		g.logResult(requestID, started, time.Time{}, err)
		return
	}
	if err != nil {
		g.metrics.Errors.Add(1)
		if release != nil {
			release()
		}
		if resp != nil {
			defer resp.Body.Close()
			w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
			if w.Header().Get("Content-Type") == "" {
				w.Header().Set("Content-Type", "application/json")
			}
			w.WriteHeader(resp.StatusCode)
			_, _ = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
			g.logResult(requestID, started, time.Time{}, err)
			return
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "timeout_error", "request_timeout", "request timed out")
		} else if r.Context().Err() == nil {
			writeError(w, http.StatusBadGateway, "upstream_error", "worker_unavailable", "worker request failed")
		}
		g.logResult(requestID, started, time.Time{}, err)
		return
	}
	defer release()
	defer resp.Body.Close()
	record.CacheActualBlocks = parseNonNegativeHeader(resp.Header.Get("X-DistServe-Actual-Hit-Blocks"))
	record.CacheActualTokens = parseNonNegativeHeader(resp.Header.Get("X-DistServe-Actual-Hit-Tokens"))
	record.CachePredictionMiss = record.CacheActualBlocks < record.CachePredictedBlocks
	g.metrics.CachePredictedTokens.Add(int64(record.CachePredictedTokens))
	g.metrics.CacheActualTokens.Add(int64(record.CacheActualTokens))
	if record.CachePredictionMiss {
		g.metrics.CachePredictionMisses.Add(1)
	}
	if resp.StatusCode != http.StatusOK {
		g.metrics.Errors.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
		g.logResult(requestID, started, time.Time{}, fmt.Errorf("worker status: %s", resp.Status))
		return
	}

	var firstToken time.Time
	if input.Stream {
		firstToken, err = proxySSE(w, resp.Body)
		record.FirstTokenAt = firstToken
		if !firstToken.IsZero() {
			g.metrics.ObserveTTFT(firstToken.Sub(started).Seconds())
		}
		record.ResponseStarted = !firstToken.IsZero()
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err = io.Copy(w, resp.Body)
		record.ResponseStarted = true
	}
	if err == nil {
		record.FinalStatus = "completed"
		record.CompletedAt = time.Now()
		if record.BackendType == string(backend.TypeMock) {
			record.OutputTokens = input.MaxTokens
			g.metrics.GeneratedTokens.Add(int64(input.MaxTokens))
		}
		if g.shadowAffinity != nil && features != nil && record.BackendType == string(backend.TypeVLLM) {
			g.shadowAffinity.RecordShadow(cache.WorkerInstanceKey{WorkerID: record.SelectedWorker, InstanceID: record.SelectedInstance}, features.Identity, features.PrefixBlocks, features.TotalInputTokens)
		}
	} else {
		g.metrics.Errors.Add(1)
		record.FinalStatus = "failed"
		record.FailedAt = time.Now()
	}
	g.logResult(requestID, started, firstToken, err)
}

func (g *Gateway) selectWorker(ctx context.Context, requestID string, input api.ChatCompletionRequest, arrival time.Time, features *cache.RequestFeatures) (registry.WorkerSnapshot, scheduler.Decision, func(), error) {
	if g.registry == nil || g.scheduler == nil {
		return registry.WorkerSnapshot{Address: g.backendURL, BackendType: string(backend.TypeMock), Models: []string{g.model}, Capacity: 1, Status: registry.StatusHealthy}, scheduler.Decision{}, func() {}, nil
	}
	meta := scheduler.RequestMeta{RequestID: requestID, Model: input.Model, InputTokens: approximateInputTokens(input.Messages), MaxOutputTokens: input.MaxTokens, Streaming: input.Stream, ArrivalTime: arrival, Cache: features}
	if deadline, ok := ctx.Deadline(); ok {
		meta.Deadline = deadline
	}
	// A worker may change instance between snapshot and commit. Re-snapshot once.
	for attempt := 0; attempt < 2; attempt++ {
		snapshots := g.registry.Snapshots()
		if features != nil && g.cacheRuntime != nil && g.cacheRuntime.Index != nil {
			features.Matches = make(map[cache.WorkerInstanceKey]cache.PrefixMatch, len(snapshots))
			features.FillAffinity = make(map[cache.WorkerInstanceKey]bool, len(snapshots))
			var affinity cache.WorkerInstanceKey
			if g.fillReservations != nil && len(features.PrefixBlocks) > 0 {
				identityHash, hashErr := features.Identity.Hash()
				if hashErr == nil {
					affinity, _ = g.fillReservations.Affinity(cache.CacheKey{IdentityHash: identityHash, PrefixHash: features.PrefixBlocks[len(features.PrefixBlocks)-1].PrefixHash})
				}
			}
			for _, worker := range snapshots {
				key := cache.WorkerInstanceKey{WorkerID: worker.ID, InstanceID: worker.InstanceID}
				features.Matches[key] = g.cacheRuntime.Index.Match(key, features.Identity, features.PrefixBlocks, features.TotalInputTokens, time.Now())
				if worker.BackendType == string(backend.TypeVLLM) && g.shadowAffinity != nil {
					shadow := g.shadowAffinity.Match(key, features.Identity, features.PrefixBlocks, features.TotalInputTokens)
					if shadow.MatchedTokens > features.Matches[key].MatchedTokens {
						features.Matches[key] = shadow
					}
				}
				features.FillAffinity[key] = key == affinity
			}
		}
		decision, err := g.scheduler.Select(ctx, meta, snapshots)
		if err != nil {
			g.metrics.SchedulerFailures.Add(1)
			return registry.WorkerSnapshot{}, scheduler.Decision{}, nil, fmt.Errorf("schedule request: %w", err)
		}
		release, err := g.registry.Reserve(decision.WorkerID, decision.InstanceID)
		if err != nil {
			continue
		}
		for _, worker := range snapshots {
			if worker.ID == decision.WorkerID && worker.InstanceID == decision.InstanceID {
				g.log.Info("scheduler decision", "request_id", requestID, "worker_id", decision.WorkerID, "instance_id", decision.InstanceID, "strategy", decision.Strategy, "score", decision.Score, "reason", decision.Reason)
				g.decisions.Add(DecisionRecord{RequestID: requestID, DecidedAt: time.Now(), Strategy: decision.Strategy, WorkerID: decision.WorkerID, InstanceID: decision.InstanceID, Score: decision.Score, Reason: decision.Reason, Candidates: decision.Candidates, SnapshotAge: decision.SnapshotAge.String()})
				g.metrics.SchedulerDecisions.Add(1)
				g.metrics.RecordSelection(worker.ID)
				g.metrics.Reservations.Add(1)
				var once sync.Once
				trackedRelease := func() { once.Do(func() { release(); g.metrics.Reservations.Add(-1) }) }
				worker.Address = strings.TrimRight(worker.Address, "/")
				return worker, decision, trackedRelease, nil
			}
		}
		release()
	}
	return registry.WorkerSnapshot{}, scheduler.Decision{}, nil, registry.ErrWorkerUnavailable
}

func approximateInputTokens(messages []api.Message) int {
	characters := 0
	for _, message := range messages {
		characters += len(message.Content)
	}
	if characters == 0 {
		return 0
	}
	return (characters + 3) / 4
}

func parseNonNegativeHeader(value string) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func proxySSE(w http.ResponseWriter, body io.Reader) (time.Time, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return time.Time{}, fmt.Errorf("streaming unsupported")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	reader := bufio.NewReader(body)
	var first time.Time
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			if first.IsZero() && strings.HasPrefix(line, "data:") && !strings.Contains(line, "[DONE]") {
				first = time.Now()
			}
			if _, writeErr := io.WriteString(w, line); writeErr != nil {
				return first, fmt.Errorf("write SSE: %w", writeErr)
			}
			flusher.Flush()
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return first, nil
			}
			return first, fmt.Errorf("read SSE: %w", err)
		}
	}
}

func (g *Gateway) logResult(requestID string, started, firstToken time.Time, err error) {
	args := []any{"request_id", requestID, "event", "request_complete", "duration", time.Since(started)}
	if !firstToken.IsZero() {
		args = append(args, "ttft", firstToken.Sub(started))
	}
	if err != nil {
		args = append(args, "error", err)
	}
	g.log.Info("gateway request", args...)
}

func writeError(w http.ResponseWriter, status int, typ, code, message string) {
	writeJSONStatus(w, status, api.ErrorResponse{Error: api.APIError{Message: message, Type: typ, Code: code}})
}

func writeJSON(w http.ResponseWriter, status int, value any) { writeJSONStatus(w, status, value) }

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
