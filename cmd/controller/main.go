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

	"distserve/internal/gateway"
	"distserve/internal/registry"
	"distserve/internal/scheduler"
)

func main() {
	listen := flag.String("listen", ":8080", "controller listen address")
	timeout := flag.Duration("request-timeout", 30*time.Second, "end-to-end request timeout")
	strategyName := flag.String("scheduler", "least-loaded", "scheduler: least-loaded or round-robin")
	maxInFlight := flag.Int("max-inflight", 128, "maximum admitted requests")
	retry := flag.Bool("retry", false, "retry once before a response starts")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerRegistry := registry.New(5*time.Second, 10*time.Second)
	go workerRegistry.RunSweeper(ctx, time.Second)
	var strategy scheduler.Scheduler
	switch *strategyName {
	case "round-robin":
		strategy = &scheduler.RoundRobin{}
	case "least-loaded":
		strategy = &scheduler.LeastLoaded{QueueWeight: 0.5, TokenWeight: 0.1}
	default:
		logger.Error("invalid scheduler", "scheduler", *strategyName)
		os.Exit(2)
	}
	mux := http.NewServeMux()
	mux.Handle("/internal/", workerRegistry.Handler())
	mux.Handle("/", gateway.NewDynamic(workerRegistry, strategy, "mock-llm", *timeout, *maxInFlight, *retry, nil, logger).Handler())
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
