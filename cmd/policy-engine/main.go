package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/dotrage/forge-adp/internal/policy"
	"github.com/dotrage/forge-adp/pkg/config"
	"github.com/dotrage/forge-adp/pkg/logger"
)

func main() {
	logger.Init("policy-engine")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine, err := policy.NewEngine(policy.Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		OPABundle:   os.Getenv("OPA_BUNDLE_PATH"),
	})
	if err != nil {
		slog.Error("failed to create policy engine", slog.Any("error", err))
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v1/authorize", engine.HandleAuthorize)
	mux.HandleFunc("/api/v1/policies", engine.HandlePolicies)
	mux.HandleFunc("/api/v1/policies/{id}", engine.HandlePolicy)

	addr := config.PolicyEnginePort()
	server := &http.Server{
		Addr:    addr,
		Handler: logger.HTTPMiddleware("policy-engine", mux),
	}

	go func() {
		slog.Info("policy engine listening", slog.String("addr", addr))
		server.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	slog.Info("shutting down policy engine")
	server.Shutdown(ctx)
}
