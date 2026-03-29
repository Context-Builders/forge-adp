package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/dotrage/forge-adp/internal/governance"
	"github.com/dotrage/forge-adp/internal/orchestrator"
	"github.com/dotrage/forge-adp/pkg/config"
	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
)

func main() {
	logger.Init("orchestrator")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus, err := events.NewRedisBus(
		os.Getenv("REDIS_ADDR"),
		"forge:events",
	)
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
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
		slog.Error("failed to create orchestrator", slog.Any("error", err))
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/v1/tasks", orch.HandleTasks)
	mux.HandleFunc("GET /api/v1/tasks/{id}", orch.HandleGetTask)
	mux.HandleFunc("POST /api/v1/tasks/{id}/approve", orch.HandleApproveTask)
	mux.HandleFunc("POST /api/v1/tasks/{id}/reject", orch.HandleRejectTask)
	mux.HandleFunc("/api/v1/assign", orch.HandleAssignment)

	addr := config.OrchestratorPort()
	server := &http.Server{
		Addr:    addr,
		Handler: logger.HTTPMiddleware("orchestrator", mux),
	}

	go func() {
		slog.Info("orchestrator listening", slog.String("addr", addr))
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			slog.Error("http server error", slog.Any("error", err))
			os.Exit(1)
		}
	}()

	go orch.ProcessEvents(ctx)
	go orch.RunWatchdog(ctx)

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

	slog.Info("shutting down orchestrator")
	server.Shutdown(ctx)
}
