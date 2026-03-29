package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	jira "github.com/andygrunwald/go-jira"
	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
)

type JiraAdapter struct {
	client          *jira.Client
	bus             events.Bus
	orchestratorURL string
}

func main() {
	logger.Init("jira-adapter")

	tp := jira.BasicAuthTransport{
		Username: os.Getenv("JIRA_USER_EMAIL"),
		Password: os.Getenv("JIRA_API_TOKEN"),
	}

	client, err := jira.NewClient(tp.Client(), os.Getenv("JIRA_BASE_URL"))
	if err != nil {
		slog.Error("failed to create Jira client", slog.Any("error", err))
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &JiraAdapter{
		client:          client,
		bus:             bus,
		orchestratorURL: os.Getenv("ORCHESTRATOR_URL"),
	}
	if adapter.orchestratorURL == "" {
		adapter.orchestratorURL = "http://localhost:19080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/tickets", adapter.HandleTickets)
	mux.HandleFunc("/api/v1/transitions", adapter.HandleTransitions)

	slog.Info("Jira adapter listening", slog.String("addr", ":19090"))
	http.ListenAndServe(":19090", logger.HTTPMiddleware("jira-adapter", mux))
}

func (a *JiraAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	webhookEvent, _ := payload["webhookEvent"].(string)
	issue, _ := payload["issue"].(map[string]interface{})

	switch webhookEvent {
	case "jira:issue_created":
		a.handleIssueCreated(r.Context(), issue)
	case "jira:issue_updated":
		a.handleIssueUpdated(r.Context(), issue, payload)
	}

	w.WriteHeader(http.StatusOK)
}

func (a *JiraAdapter) handleIssueCreated(ctx context.Context, issue map[string]interface{}) {
	key, _ := issue["key"].(string)
	fields, _ := issue["fields"].(map[string]interface{})

	labels, _ := fields["labels"].([]interface{})
	forgeEligible := false
	for _, l := range labels {
		if l.(string) == "forge" {
			forgeEligible = true
			break
		}
	}

	if !forgeEligible {
		return
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"jira_key":    key,
		"summary":     fields["summary"],
		"description": fields["description"],
		"priority":    fields["priority"],
	})

	a.bus.Publish(ctx, events.Event{
		Type:    events.TaskCreated,
		Payload: payload,
	})
}

func (a *JiraAdapter) handleIssueUpdated(ctx context.Context, issue map[string]interface{}, payload map[string]interface{}) {
	key, _ := issue["key"].(string)
	if key == "" {
		return
	}

	// --- Comment-based commands ------------------------------------------
	// Jira sends a top-level "comment" object when a comment is added/edited.
	// Forge recognises two commands in comment bodies:
	//   !forge approve <task_id>
	//   !forge reject <task_id> [reason...]
	if comment, ok := payload["comment"].(map[string]interface{}); ok {
		body, _ := comment["body"].(string)
		if strings.HasPrefix(strings.TrimSpace(body), "!forge ") {
			a.handleForgeComment(ctx, key, strings.TrimSpace(body))
		}
	}

	// --- Status transitions -----------------------------------------------
	// When the changelog contains a status change we publish the appropriate
	// event so the orchestrator (or any subscriber) can react.
	changelog, _ := payload["changelog"].(map[string]interface{})
	items, _ := changelog["items"].([]interface{})
	for _, raw := range items {
		item, _ := raw.(map[string]interface{})
		if item["field"] != "status" {
			continue
		}
		toStatus := strings.ToLower(fmt.Sprintf("%v", item["toString"]))
		eventPayload, _ := json.Marshal(map[string]interface{}{
			"jira_key": key,
			"status":   toStatus,
		})
		var eventType events.EventType
		switch toStatus {
		case "done", "closed", "resolved":
			eventType = events.TaskCompleted
		case "in progress":
			eventType = events.TaskStarted
		case "blocked", "impediment":
			eventType = events.TaskBlocked
		default:
			continue
		}
		if err := a.bus.Publish(ctx, events.Event{Type: eventType, Payload: eventPayload}); err != nil {
			slog.Error("failed to publish jira status event",
				slog.String("event_type", string(eventType)),
				slog.String("jira_key", key),
				slog.Any("error", err))
		}
	}
}

// handleForgeComment parses !forge commands from Jira comments and calls the
// orchestrator to approve or reject the referenced task.
func (a *JiraAdapter) handleForgeComment(ctx context.Context, jiraKey, body string) {
	parts := strings.Fields(body) // ["!forge", "approve"|"reject", "<task_id>", ...]
	if len(parts) < 3 {
		slog.Warn("malformed forge command", slog.String("jira_key", jiraKey), slog.String("body", body))
		return
	}
	cmd := strings.ToLower(parts[1])
	taskID := parts[2]

	switch cmd {
	case "approve":
		url := fmt.Sprintf("%s/api/v1/tasks/%s/approve", a.orchestratorURL, taskID)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			slog.Error("failed to approve task", slog.String("task_id", taskID), slog.Any("error", err))
			return
		}
		defer resp.Body.Close()
		slog.Info("approved task via jira comment",
			slog.String("task_id", taskID),
			slog.String("jira_key", jiraKey),
			slog.Int("status", resp.StatusCode))

	case "reject":
		reason := strings.Join(parts[3:], " ")
		if reason == "" {
			reason = fmt.Sprintf("rejected via Jira comment on %s", jiraKey)
		}
		body, _ := json.Marshal(map[string]string{"reason": reason})
		url := fmt.Sprintf("%s/api/v1/tasks/%s/reject", a.orchestratorURL, taskID)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			slog.Error("failed to reject task", slog.String("task_id", taskID), slog.Any("error", err))
			return
		}
		defer resp.Body.Close()
		slog.Info("rejected task via jira comment",
			slog.String("task_id", taskID),
			slog.String("jira_key", jiraKey),
			slog.Int("status", resp.StatusCode))

	default:
		slog.Warn("unknown forge command", slog.String("cmd", cmd), slog.String("jira_key", jiraKey))
	}
}

func (a *JiraAdapter) HandleTickets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ticketKey := r.URL.Query().Get("key")
		issue, _, err := a.client.Issue.Get(ticketKey, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(issue)
	case http.MethodPost:
		var issueData jira.Issue
		if err := json.NewDecoder(r.Body).Decode(&issueData); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		created, _, err := a.client.Issue.Create(&issueData)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *JiraAdapter) HandleTransitions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, "key is required", http.StatusBadRequest)
			return
		}
		transitions, _, err := a.client.Issue.GetTransitions(key)
		if err != nil {
			http.Error(w, fmt.Sprintf("get transitions: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(transitions)

	case http.MethodPost:
		var req struct {
			Key          string `json:"key"`
			TransitionID string `json:"transition_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("decode body: %v", err), http.StatusBadRequest)
			return
		}
		if req.Key == "" || req.TransitionID == "" {
			http.Error(w, "key and transition_id are required", http.StatusBadRequest)
			return
		}
		_, err := a.client.Issue.DoTransition(req.Key, req.TransitionID)
		if err != nil {
			http.Error(w, fmt.Sprintf("do transition: %v", err), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
