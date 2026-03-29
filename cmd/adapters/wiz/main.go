package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
)

const wizAPIBase = "https://api.us1.app.wiz.io/graphql"
const prismaAPIBase = "https://api.prismacloud.io"

type WizAdapter struct {
	wizClientID     string
	wizClientSecret string
	prismaAccess    string
	prismaSecret    string
	bus             events.Bus
	httpClient      *http.Client
}

type wizFinding struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Severity    string `json:"severity"`
	Status      string `json:"status"`
	Description string `json:"description"`
	Resource    struct {
		Name          string `json:"name"`
		Type          string `json:"type"`
		CloudProvider string `json:"cloudPlatform"`
	} `json:"resource"`
}

type wizWebhookPayload struct {
	Action  string     `json:"action"`
	Finding wizFinding `json:"finding"`
}

type prismaAlert struct {
	ID     string `json:"id"`
	Policy struct {
		Name     string `json:"name"`
		Severity string `json:"severity"`
	} `json:"policy"`
	Status   string `json:"status"`
	Resource struct {
		Name string `json:"name"`
	} `json:"resource"`
}

type prismaWebhookPayload struct {
	Alerts []prismaAlert `json:"alerts"`
}

func main() {
	logger.Init("wiz-adapter")

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &WizAdapter{
		wizClientID:     os.Getenv("WIZ_CLIENT_ID"),
		wizClientSecret: os.Getenv("WIZ_CLIENT_SECRET"),
		prismaAccess:    os.Getenv("PRISMA_ACCESS_KEY"),
		prismaSecret:    os.Getenv("PRISMA_SECRET_KEY"),
		bus:             bus,
		httpClient:      &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook/wiz", adapter.HandleWizWebhook)
	mux.HandleFunc("/webhook/prisma", adapter.HandlePrismaWebhook)
	mux.HandleFunc("/api/v1/findings", adapter.HandleFindings)

	slog.Info("Wiz / Prisma Cloud adapter listening", slog.String("addr", ":19122"))
	http.ListenAndServe(":19122", logger.HTTPMiddleware("wiz-adapter", mux))
}

func (a *WizAdapter) HandleWizWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload wizWebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if payload.Action == "OPEN" && (payload.Finding.Severity == "CRITICAL" || payload.Finding.Severity == "HIGH") {
		ep, _ := json.Marshal(map[string]interface{}{
			"finding_id":  payload.Finding.ID,
			"type":        payload.Finding.Type,
			"severity":    payload.Finding.Severity,
			"description": payload.Finding.Description,
			"resource":    payload.Finding.Resource.Name,
			"cloud":       payload.Finding.Resource.CloudProvider,
			"source":      "wiz",
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
			slog.Error("failed to publish escalation event",
				slog.String("finding_id", payload.Finding.ID),
				slog.Any("error", err))
		}
	} else if payload.Action == "RESOLVED" {
		ep, _ := json.Marshal(map[string]interface{}{
			"finding_id": payload.Finding.ID,
			"source":     "wiz",
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
			slog.Error("failed to publish task completed event",
				slog.String("finding_id", payload.Finding.ID),
				slog.Any("error", err))
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (a *WizAdapter) HandlePrismaWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload prismaWebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	for _, alert := range payload.Alerts {
		if alert.Status == "open" && (alert.Policy.Severity == "critical" || alert.Policy.Severity == "high") {
			ep, _ := json.Marshal(map[string]interface{}{
				"alert_id": alert.ID,
				"policy":   alert.Policy.Name,
				"severity": alert.Policy.Severity,
				"resource": alert.Resource.Name,
				"source":   "prismacloud",
			})
			if err := a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
				slog.Error("failed to publish escalation event",
					slog.String("alert_id", alert.ID),
					slog.Any("error", err))
			}
		}
	}
	w.WriteHeader(http.StatusOK)
}

// HandleFindings returns a message directing callers to use the native APIs directly.
//
//	GET /api/v1/findings
func (a *WizAdapter) HandleFindings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	result := map[string]interface{}{
		"message": "Use Wiz GraphQL API or Prisma Cloud REST API with configured credentials",
		"wiz_api": wizAPIBase,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for EscalationCreated events from security tools so
// the security agent can correlate Wiz/Prisma findings with Forge escalations.
func (a *WizAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.EscalationCreated}, func(e events.Event) error {
		var payload struct {
			Source string `json:"source"`
		}
		json.Unmarshal(e.Payload, &payload)
		if payload.Source == "wiz" || payload.Source == "prismacloud" {
			return nil // avoid loops
		}
		slog.Info("escalation received",
			slog.String("task_id", e.TaskID),
			slog.String("source", payload.Source))
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to escalation events", slog.Any("error", err))
	}
}

// Ensure constants are referenced to prevent unused const errors.
var _ = prismaAPIBase
