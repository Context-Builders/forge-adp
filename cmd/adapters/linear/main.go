package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
)

const linearAPIURL = "https://api.linear.app/graphql"

// LinearAdapter handles bidirectional communication with Linear via
// the Linear GraphQL API (outbound) and webhook events (inbound).
type LinearAdapter struct {
	apiKey        string
	webhookSecret string
	bus           events.Bus
	http          *http.Client
}

// WebhookPayload is the structure Linear sends for issue events.
type WebhookPayload struct {
	Action string                 `json:"action"`
	Type   string                 `json:"type"`
	Data   map[string]interface{} `json:"data"`
}

// IssueInput represents fields for creating or updating a Linear issue.
type IssueInput struct {
	TeamID      string   `json:"teamId"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Priority    int      `json:"priority,omitempty"`
	StateID     string   `json:"stateId,omitempty"`
	AssigneeID  string   `json:"assigneeId,omitempty"`
	LabelIDs    []string `json:"labelIds,omitempty"`
}

func main() {
	logger.Init("linear-adapter")

	apiKey := os.Getenv("LINEAR_API_KEY")
	if apiKey == "" {
		slog.Error("LINEAR_API_KEY is required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &LinearAdapter{
		apiKey:        apiKey,
		webhookSecret: os.Getenv("LINEAR_WEBHOOK_SECRET"),
		bus:           bus,
		http:          &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/issues", adapter.HandleIssues)
	mux.HandleFunc("/api/v1/transitions", adapter.HandleTransitions)

	slog.Info("Linear adapter listening", slog.String("addr", ":19097"))
	http.ListenAndServe(":19097", logger.HTTPMiddleware("linear-adapter", mux))
}

func (a *LinearAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if a.webhookSecret != "" {
		sig := r.Header.Get("Linear-Signature")
		mac := hmac.New(sha256.New, []byte(a.webhookSecret))
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))
		if sig != expected {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	var evt WebhookPayload
	if err := json.Unmarshal(body, &evt); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch evt.Type {
	case "Issue":
		a.handleIssueEvent(r.Context(), evt)
	case "Comment":
		a.handleCommentEvent(r.Context(), evt)
	}

	w.WriteHeader(http.StatusOK)
}

func (a *LinearAdapter) handleIssueEvent(ctx context.Context, evt WebhookPayload) {
	data := evt.Data
	labels, _ := data["labels"].([]interface{})
	forgeEligible := false
	for _, l := range labels {
		if lm, ok := l.(map[string]interface{}); ok {
			if lm["name"] == "forge" {
				forgeEligible = true
				break
			}
		}
	}

	switch evt.Action {
	case "create":
		if !forgeEligible {
			return
		}
		payload, _ := json.Marshal(map[string]interface{}{
			"linear_id":   data["id"],
			"identifier":  data["identifier"],
			"title":       data["title"],
			"description": data["description"],
			"priority":    data["priority"],
			"team":        data["team"],
		})
		a.bus.Publish(ctx, events.Event{
			Type:    events.TaskCreated,
			Payload: payload,
		})
	case "update":
		if state, ok := data["state"].(map[string]interface{}); ok {
			if state["type"] == "completed" {
				payload, _ := json.Marshal(map[string]interface{}{
					"linear_id":  data["id"],
					"identifier": data["identifier"],
					"state":      state["name"],
				})
				a.bus.Publish(ctx, events.Event{
					Type:    events.TaskCompleted,
					Payload: payload,
				})
			}
		}
	case "remove":
		payload, _ := json.Marshal(map[string]interface{}{
			"linear_id":  data["id"],
			"identifier": data["identifier"],
		})
		a.bus.Publish(ctx, events.Event{
			Type:    events.TaskFailed,
			Payload: payload,
		})
	}
}

func (a *LinearAdapter) handleCommentEvent(_ context.Context, _ WebhookPayload) {
	// Handle comment events — blocking comments may trigger escalations.
}

// subscribeToEvents listens for TaskBlocked and EscalationCreated events and
// updates the corresponding Linear issue state or adds a comment.
func (a *LinearAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{
		events.TaskBlocked,
		events.EscalationCreated,
	}, func(e events.Event) error {
		switch e.Type {
		case events.TaskBlocked:
			return a.updateIssueState(ctx, e)
		case events.EscalationCreated:
			return a.addComment(ctx, e)
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to events", slog.Any("error", err))
	}
}

func (a *LinearAdapter) updateIssueState(ctx context.Context, e events.Event) error {
	var payload struct {
		LinearID string `json:"linear_id"`
		StateID  string `json:"state_id"`
	}
	if err := json.Unmarshal(e.Payload, &payload); err != nil {
		return err
	}
	if payload.LinearID == "" || payload.StateID == "" {
		return nil
	}

	mutation := `
		mutation IssueUpdate($id: String!, $stateId: String!) {
			issueUpdate(id: $id, input: { stateId: $stateId }) {
				success
			}
		}`
	return a.graphql(ctx, mutation, map[string]interface{}{
		"id":      payload.LinearID,
		"stateId": payload.StateID,
	}, nil)
}

func (a *LinearAdapter) addComment(ctx context.Context, e events.Event) error {
	var payload struct {
		LinearID string `json:"linear_id"`
		Message  string `json:"message"`
	}
	if err := json.Unmarshal(e.Payload, &payload); err != nil {
		return err
	}
	if payload.LinearID == "" {
		return nil
	}

	mutation := `
		mutation CommentCreate($issueId: String!, $body: String!) {
			commentCreate(input: { issueId: $issueId, body: $body }) {
				success
			}
		}`
	return a.graphql(ctx, mutation, map[string]interface{}{
		"issueId": payload.LinearID,
		"body":    payload.Message,
	}, nil)
}

// HandleIssues fetches or creates Linear issues via the GraphQL API.
//
//	GET  /api/v1/issues?id=<id>
//	POST /api/v1/issues
func (a *LinearAdapter) HandleIssues(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		issueID := r.URL.Query().Get("id")
		if issueID == "" {
			http.Error(w, "id query param required", http.StatusBadRequest)
			return
		}
		query := `
			query Issue($id: String!) {
				issue(id: $id) {
					id identifier title description priority
					state { id name type }
					team { id name }
					assignee { id name email }
					labels { nodes { id name } }
				}
			}`
		var result map[string]interface{}
		if err := a.graphql(r.Context(), query, map[string]interface{}{"id": issueID}, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(result)

	case http.MethodPost:
		var input IssueInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mutation := `
			mutation IssueCreate($input: IssueCreateInput!) {
				issueCreate(input: $input) {
					success
					issue { id identifier title url }
				}
			}`
		var result map[string]interface{}
		if err := a.graphql(r.Context(), mutation, map[string]interface{}{"input": input}, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(result)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleTransitions updates a Linear issue's workflow state.
//
//	POST /api/v1/transitions  {"issue_id":"...","state_id":"..."}
func (a *LinearAdapter) HandleTransitions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		IssueID string `json:"issue_id"`
		StateID string `json:"state_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	mutation := `
		mutation IssueUpdate($id: String!, $stateId: String!) {
			issueUpdate(id: $id, input: { stateId: $stateId }) {
				success
				issue { id identifier state { name } }
			}
		}`
	var result map[string]interface{}
	if err := a.graphql(r.Context(), mutation, map[string]interface{}{
		"id":      req.IssueID,
		"stateId": req.StateID,
	}, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(result)
}

// graphql executes a GraphQL request against the Linear API.
func (a *LinearAdapter) graphql(ctx context.Context, query string, variables map[string]interface{}, out interface{}) error {
	body, err := json.Marshal(map[string]interface{}{
		"query":     query,
		"variables": variables,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, linearAPIURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", a.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("linear API error %d: %s", resp.StatusCode, string(respBody))
	}

	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
