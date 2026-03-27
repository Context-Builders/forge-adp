package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/dotrage/forge-adp/internal/policy"
	"github.com/dotrage/forge-adp/pkg/config"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine, err := policy.NewEngine(policy.Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		OPABundle:   os.Getenv("OPA_BUNDLE_PATH"),
	})
	if err != nil {
		log.Fatalf("failed to create policy engine: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v1/authorize", engine.HandleAuthorize)
	mux.HandleFunc("/api/v1/policies", engine.HandlePolicies)

	addr := config.PolicyEnginePort()
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		log.Printf("policy engine listening on %s", addr)
		server.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("shutting down policy engine...")
	server.Shutdown(ctx)
}
