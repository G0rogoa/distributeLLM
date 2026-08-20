package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"distserve/internal/cache"
	"distserve/internal/mockworker"
)

func main() {
	listen := flag.String("listen", ":9001", "worker listen address")
	controller := flag.String("controller", "http://127.0.0.1:8080", "controller base URL")
	id := flag.String("id", "worker-1", "stable worker ID")
	advertise := flag.String("advertise", "http://127.0.0.1:9001", "worker URL advertised to controller")
	capacity := flag.Int("capacity", 8, "reported worker capacity")
	queueCapacity := flag.Int("queue-capacity", 32, "maximum queued requests")
	prefill := flag.Duration("prefill-delay", 20*time.Millisecond, "simulated prefill delay")
	decode := flag.Duration("decode-interval", 15*time.Millisecond, "simulated decode interval")
	prefillPerToken := flag.Duration("prefill-per-token", 500*time.Microsecond, "additional prefill time per input token")
	penalty := flag.Duration("concurrency-penalty", 3*time.Millisecond, "decode penalty per concurrent request")
	jitter := flag.Duration("jitter", 5*time.Millisecond, "maximum random latency jitter")
	failureRate := flag.Float64("failure-rate", 0, "injected failure probability from 0 to 1")
	seed := flag.Int64("seed", 1, "random seed; zero uses current time")
	cacheCapacity := flag.Int64("cache-capacity-bytes", 512<<20, "mock KV cache capacity")
	cacheBytesPerToken := flag.Int64("cache-bytes-per-token", 16384, "simulated cache bytes per token")
	cacheLease := flag.Duration("cache-lease", 30*time.Second, "cache event lease")
	cacheEventQueue := flag.Int("cache-event-queue", 1024, "bounded cache event queue")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	instanceID := fmt.Sprintf("%s-%d-%d", *id, os.Getpid(), time.Now().UnixNano())
	worker := mockworker.NewWithConfig(mockworker.Config{Model: "mock-llm", Capacity: *capacity, QueueCapacity: *queueCapacity, PrefillDelay: *prefill, PrefillPerToken: *prefillPerToken, DecodeInterval: *decode, ConcurrencyPenalty: *penalty, Jitter: *jitter, FailureRate: *failureRate, Seed: *seed})
	events := make(chan cache.CacheEvent, *cacheEventQueue)
	if err := worker.EnableCache(cache.WorkerInstanceKey{WorkerID: *id, InstanceID: instanceID}, *cacheCapacity, *cacheBytesPerToken, *cacheLease, 100*time.Microsecond, events); err != nil {
		logger.Error("invalid cache configuration", "error", err)
		os.Exit(2)
	}
	go func() {
		err := worker.RunRegistration(ctx, mockworker.RegistrationConfig{ControllerURL: *controller, ID: *id, InstanceID: instanceID, Address: *advertise, Model: "mock-llm", Capacity: *capacity, Interval: 2 * time.Second, CacheEvents: events}, nil)
		if err != nil && ctx.Err() == nil {
			logger.Error("registration loop stopped", "error", err)
		}
	}()
	server := &http.Server{Addr: *listen, Handler: worker.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	logger.Info("mock worker listening", "address", *listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("mock worker stopped", "error", err)
		os.Exit(1)
	}
}
