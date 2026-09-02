package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"distserve/internal/cache"
	"distserve/internal/cachehttp"
	"distserve/internal/gateway"
	"distserve/internal/registry"
	"distserve/internal/scheduler"
)

func main() {
	listen := flag.String("listen", ":8080", "controller listen address")
	model := flag.String("model", "mock-llm", "served model name accepted by the controller")
	timeout := flag.Duration("request-timeout", 30*time.Second, "end-to-end request timeout")
	strategyName := flag.String("scheduler", "prefix-aware", "scheduler: prefix-aware, ect, least-loaded or round-robin")
	maxInFlight := flag.Int("max-inflight", 128, "maximum admitted requests")
	retry := flag.Bool("retry", false, "retry once before a response starts")
	cacheMaxEntries := flag.Int("cache-max-entries", 100000, "maximum Controller cache metadata entries")
	cacheDedup := flag.Int("cache-event-dedup", 10000, "remembered cache event IDs")
	blockSize := flag.Int("cache-block-size", 16, "tokens per full cache block")
	fillTTL := flag.Duration("cache-fill-ttl", 30*time.Second, "advisory cache fill reservation TTL")
	shadowTTL := flag.Duration("shadow-affinity-ttl", 60*time.Second, "real backend shadow affinity TTL")
	tokenizerMode := flag.String("tokenizer-mode", string(cache.TokenizerModeMock), "tokenizer mode: mock, remote or disabled")
	tokenizerURL := flag.String("tokenizer-url", "", "remote tokenizer service base URL when tokenizer-mode=remote")
	tokenizerTimeout := flag.Duration("tokenizer-timeout", 2*time.Second, "remote tokenizer request timeout")
	modelRevision := flag.String("model-revision", "v1", "cache identity model revision")
	tokenizerID := flag.String("tokenizer-id", "mock", "cache identity tokenizer ID")
	tokenizerRevision := flag.String("tokenizer-revision", "v1", "cache identity tokenizer revision")
	chatTemplateVersion := flag.String("chat-template-version", "chat-v1", "cache identity chat template version")
	cacheFormatVersion := flag.String("cache-format-version", "mock-kv-v1", "cache identity KV/cache format version")
	kvLayout := flag.String("kv-layout", "mock-fp16", "cache identity KV layout, dtype, or engine setting")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if *fillTTL <= 0 || *blockSize < 1 {
		logger.Error("cache fill TTL and block size must be positive")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerRegistry := registry.New(5*time.Second, 10*time.Second)
	go workerRegistry.RunSweeper(ctx, time.Second)
	cacheIndex, err := cache.NewCacheIndex(*cacheMaxEntries, *cacheDedup, 10*time.Second)
	if err != nil {
		logger.Error("invalid cache index configuration", "error", err)
		os.Exit(2)
	}
	go cacheIndex.RunCleanup(ctx, 2*time.Second, 1000)
	fills := cache.NewFillReservations(*fillTTL)
	go fills.RunCleanup(ctx, time.Second)
	identity := cache.CacheIdentity{ProtocolVersion: cache.PrefixProtocolVersion, ModelID: *model, ModelRevision: *modelRevision, TokenizerID: *tokenizerID, TokenizerRevision: *tokenizerRevision, ChatTemplateVersion: *chatTemplateVersion, BlockSizeTokens: *blockSize, CacheFormatVersion: *cacheFormatVersion, KVLayout: *kvLayout}
	if err := identity.Validate(); err != nil {
		logger.Error("invalid cache identity", "error", err)
		os.Exit(2)
	}
	var tokenizer cache.Tokenizer
	switch cache.TokenizerMode(*tokenizerMode) {
	case cache.TokenizerModeMock:
		tokenizer = &cache.DeterministicMockTokenizer{TokenizerID: cache.TokenizerIdentity{ID: identity.TokenizerID, Revision: identity.TokenizerRevision}, MaxInputBytes: 1 << 20, MaxTokens: 262144}
	case cache.TokenizerModeDisabled:
		identity.TokenizerID = "disabled"
		identity.TokenizerRevision = "none"
		tokenizer = cache.DisabledTokenizer{}
	case cache.TokenizerModeRemote:
		if *tokenizerURL == "" || *tokenizerTimeout <= 0 {
			logger.Error("remote tokenizer requires tokenizer-url and positive tokenizer-timeout")
			os.Exit(2)
		}
		tokenizer = cache.RemoteTokenizer{TokenizerID: cache.TokenizerIdentity{ID: identity.TokenizerID, Revision: identity.TokenizerRevision}, URL: *tokenizerURL, ExpectedIdentity: cache.PromptIdentity{ModelID: identity.ModelID, ModelRevision: identity.ModelRevision, TokenizerID: identity.TokenizerID, TokenizerRevision: identity.TokenizerRevision, ChatTemplateVersion: identity.ChatTemplateVersion}, Timeout: *tokenizerTimeout, MaxTokens: 262144}
	default:
		logger.Error("invalid tokenizer mode", "tokenizer_mode", *tokenizerMode)
		os.Exit(2)
	}
	runtime := &cache.Runtime{Builder: cache.PromptBuilder{Identity: cache.PromptIdentity{ModelID: identity.ModelID, ModelRevision: identity.ModelRevision, TokenizerID: identity.TokenizerID, TokenizerRevision: identity.TokenizerRevision, ChatTemplateVersion: identity.ChatTemplateVersion}, MaxBytes: 1 << 20}, Tokenizer: tokenizer, Identity: identity, Index: cacheIndex}
	var strategy scheduler.Scheduler
	switch *strategyName {
	case "round-robin":
		strategy = &scheduler.RoundRobin{}
	case "least-loaded":
		strategy = &scheduler.LeastLoaded{QueueWeight: 0.5, TokenWeight: 0.1}
	case "prefix-aware":
		strategy = &scheduler.PrefixAware{CacheWeight: 1, LoadWeight: 1, StalenessWeight: 1, RunningWeight: 20, ReservationWeight: 20, QueueWeight: 10, RemainingTokenWeight: 0.01, PrefillMSPerToken: 0.5, DegradedPenalty: 100, FillAffinityBonus: 25}
	case "ect":
		strategy = &scheduler.ExpectedCompletionTime{PrefillMSPerToken: 0.5, DecodeMSPerToken: 1, RunningMS: 20, QueueMS: 40, ReservationMS: 20, RemainingTokenMS: 0.01, ShadowDiscount: 0.5, DegradedPenalty: 100}
	default:
		logger.Error("invalid scheduler", "scheduler", *strategyName)
		os.Exit(2)
	}
	gw := gateway.NewDynamic(workerRegistry, strategy, *model, *timeout, *maxInFlight, *retry, nil, logger)
	gw.ConfigureCache(runtime, fills)
	shadowAffinity := cache.NewAffinityIndex(*shadowTTL)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-workerRegistry.Events():
				if event.CurrentState != registry.StatusUnavailable && event.CurrentState != registry.StatusDraining {
					continue
				}
				worker := cache.WorkerInstanceKey{WorkerID: event.WorkerID, InstanceID: event.InstanceID}
				shadowAffinity.ClearWorker(worker)
				cacheIndex.RemoveWorkerInstance(event.WorkerID, event.InstanceID)
				fills.Invalidate(worker)
				logger.Info("cleared worker temporary cache state", "worker_id", event.WorkerID, "instance_id", event.InstanceID, "previous_state", event.PreviousState, "current_state", event.CurrentState, "reason", event.Reason)
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				shadowAffinity.CleanupExpired(1000)
			}
		}
	}()
	gw.ConfigureShadowAffinity(shadowAffinity)
	mux := http.NewServeMux()
	cacheRoutes := (&cachehttp.Handler{Index: cacheIndex, Registry: workerRegistry}).Routes()
	gatewayRoutes := gw.Handler()
	mux.Handle("/internal/cache/", cacheRoutes)
	mux.Handle("POST /internal/workers/{id}/cache/events", cacheRoutes)
	mux.HandleFunc("GET /internal/debug/requests", func(w http.ResponseWriter, _ *http.Request) {
		gw.DebugRequests(w)
	})
	mux.HandleFunc("GET /internal/debug/decisions", func(w http.ResponseWriter, _ *http.Request) {
		gw.DebugDecisions(w)
	})
	mux.Handle("/internal/", workerRegistry.Handler())
	mux.Handle("GET /internal/cache/requests/{id}", gatewayRoutes)
	mux.Handle("/", gatewayRoutes)
	server := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	logger.Info("controller listening", "address", *listen, "scheduler", strategy.Name())
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("controller stopped", "error", err)
		os.Exit(1)
	}
}
