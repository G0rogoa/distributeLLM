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
	timeout := flag.Duration("request-timeout", 30*time.Second, "end-to-end request timeout")
	strategyName := flag.String("scheduler", "prefix-aware", "scheduler: prefix-aware, least-loaded or round-robin")
	maxInFlight := flag.Int("max-inflight", 128, "maximum admitted requests")
	retry := flag.Bool("retry", false, "retry once before a response starts")
	cacheMaxEntries := flag.Int("cache-max-entries", 100000, "maximum Controller cache metadata entries")
	cacheDedup := flag.Int("cache-event-dedup", 10000, "remembered cache event IDs")
	blockSize := flag.Int("cache-block-size", 16, "tokens per full cache block")
	fillTTL := flag.Duration("cache-fill-ttl", 30*time.Second, "advisory cache fill reservation TTL")
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
	identity := cache.CacheIdentity{ProtocolVersion: cache.PrefixProtocolVersion, ModelID: "mock-llm", ModelRevision: "v1", TokenizerID: "mock", TokenizerRevision: "v1", ChatTemplateVersion: "chat-v1", BlockSizeTokens: *blockSize, CacheFormatVersion: "mock-kv-v1"}
	runtime := &cache.Runtime{Builder: cache.PromptBuilder{Identity: cache.PromptIdentity{ModelID: identity.ModelID, ModelRevision: identity.ModelRevision, TokenizerID: identity.TokenizerID, TokenizerRevision: identity.TokenizerRevision, ChatTemplateVersion: identity.ChatTemplateVersion}, MaxBytes: 1 << 20}, Tokenizer: &cache.DeterministicMockTokenizer{TokenizerID: cache.TokenizerIdentity{ID: identity.TokenizerID, Revision: identity.TokenizerRevision}, MaxInputBytes: 1 << 20, MaxTokens: 262144}, Identity: identity, Index: cacheIndex}
	var strategy scheduler.Scheduler
	switch *strategyName {
	case "round-robin":
		strategy = &scheduler.RoundRobin{}
	case "least-loaded":
		strategy = &scheduler.LeastLoaded{QueueWeight: 0.5, TokenWeight: 0.1}
	case "prefix-aware":
		strategy = &scheduler.PrefixAware{CacheWeight: 1, LoadWeight: 1, StalenessWeight: 1, RunningWeight: 20, ReservationWeight: 20, QueueWeight: 10, RemainingTokenWeight: 0.01, PrefillMSPerToken: 0.5, DegradedPenalty: 100, FillAffinityBonus: 25}
	default:
		logger.Error("invalid scheduler", "scheduler", *strategyName)
		os.Exit(2)
	}
	mux := http.NewServeMux()
	cacheRoutes := (&cachehttp.Handler{Index: cacheIndex, Registry: workerRegistry}).Routes()
	mux.Handle("/internal/cache/", cacheRoutes)
	mux.Handle("POST /internal/workers/{id}/cache/events", cacheRoutes)
	mux.Handle("/internal/", workerRegistry.Handler())
	gw := gateway.NewDynamic(workerRegistry, strategy, "mock-llm", *timeout, *maxInFlight, *retry, nil, logger)
	gw.ConfigureCache(runtime, fills)
	gatewayRoutes := gw.Handler()
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
