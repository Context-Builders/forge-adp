package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"

	jira "github.com/andygrunwald/go-jira"
	"github.com/dotrage/forge-adp/pkg/events"
)

type JiraAdapter struct {
	client *jira.Client
	bus    events.Bus
}

func main() {
	tp := jira.BasicAuthTransport{
		Username: os.Getenv("JIRA_USERNAME"),
		Password: os.Getenv("JIRA_API_TOKEN"),
	}

	client, err := jira.NewClient(tp.Client(), os.Getenv("JIRA_BASE_URL"))
	if err != nil {
		log.Fatalf("failed to create Jira client: %v", err)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		log.Fatalf("failed to create event bus: %v", err)
	}

	adapter := &JiraAdapter{client: client, bus: bus}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/tickets", adapter.HandleTickets)
	mux.HandleFunc("/api/v1/transitions", adapter.HandleTransitions)

	log.Printf("Jira adapter listening on :19090")
	http.ListenAndServe(":19090", mux)
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
		a.handleIssueUpdated(r.Context(), issue)
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

func (a *JiraAdapter) handleIssueUpdated(ctx context.Context, issue map[string]interface{}) {
	// Handle status transitions, comment additions, etc.
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
		json.NewEncoder(w).Encode(created)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *JiraAdapter) HandleTransitions(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
