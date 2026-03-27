package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/dotrage/forge-adp/internal/governance"
	"github.com/dotrage/forge-adp/internal/orchestrator"
	"github.com/dotrage/forge-adp/pkg/config"
	"github.com/dotrage/forge-adp/pkg/events"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus, err := events.NewRedisBus(
		os.Getenv("REDIS_ADDR"),
		"forge:events",
	)
	if err != nil {
		log.Fatalf("failed to create event bus: %v", err)
	}
	defer bus.Close()

	projectID := os.Getenv("FORGE_PROJECT_ID")
	companyID := os.Getenv("FORGE_COMPANY_ID")

	orch, err := orchestrator.New(orchestrator.Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		EventBus:    bus,
		ProjectID:   projectID,
		CompanyID:   companyID,
	})
	if err != nil {
		log.Fatalf("failed to create orchestrator: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/v1/tasks", orch.HandleTasks)
	mux.HandleFunc("/api/v1/assign", orch.HandleAssignment)

	addr := config.OrchestratorPort()
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		log.Printf("orchestrator listening on %s", addr)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("http server error: %v", err)
		}
	}()

	go orch.ProcessEvents(ctx)

	// ----------------------------------------------------------------
	// Governance scheduler — fires compliance-report (weekly) and
	// policy-drift-detection (monthly) by creating tasks directly.
	// ----------------------------------------------------------------
	scheduler := governance.New(governance.SchedulerConfig{
		ProjectID: projectID,
		CompanyID: companyID,
		Bus:       bus,
		TaskCreator: func(schedCtx context.Context, st governance.ScheduledTask) error {
			return orch.CreateTask(schedCtx, orchestrator.Task{
				ID:        st.ID,
				AgentRole: st.AgentRole,
				SkillName: st.SkillName,
				Input:     st.Input,
				Priority:  3,
			})
		},
	})
	go scheduler.Run(ctx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("shutting down orchestrator...")
	server.Shutdown(ctx)
}
