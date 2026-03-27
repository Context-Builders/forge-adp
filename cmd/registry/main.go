package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/dotrage/forge-adp/internal/registry"
	"github.com/dotrage/forge-adp/pkg/config"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg, err := registry.New(registry.Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		S3Bucket:    os.Getenv("SKILLS_S3_BUCKET"),
		S3Region:    os.Getenv("AWS_REGION"),
	})
	if err != nil {
		log.Fatalf("failed to create registry: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v1/agents", reg.HandleAgents)
	mux.HandleFunc("/api/v1/skills", reg.HandleSkills)
	mux.HandleFunc("/api/v1/llm-providers", reg.HandleLLMProviders)

	addr := config.RegistryPort()
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		log.Printf("registry listening on %s", addr)
		server.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("shutting down registry...")
	server.Shutdown(ctx)
}
