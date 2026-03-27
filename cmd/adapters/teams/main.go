package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"github.com/dotrage/forge-adp/pkg/events"
)

const cardFmt = `{"$schema":"http://adaptivecards.io/schemas/adaptive-card.json","type":"AdaptiveCard","version":"1.4","body":[{"type":"TextBlock","size":"Medium","weight":"Bolder","text":"Review Requested"},{"type":"FactSet","facts":[{"title":"Ticket","value":%q},{"title":"Agent","value":%q},{"title":"PR","value":%q}]}],"actions":[{"type":"Action.Execute","title":"Approve","verb":"approve","data":{"action":"approve","task_id":%q}},{"type":"Action.Execute","title":"Request Changes","verb":"reject","data":{"action":"reject","task_id":%q}}]}`

type TeamsAdapter struct {
	webhookURL    string
	hmacSecret    string
	bus           events.Bus
	httpClient    *http.Client
}

func main() {
	webhookURL := os.Getenv("TEAMS_WEBHOOK_URL")
	if webhookURL == "" {
		log.Fatal("TEAMS_WEBHOOK_URL is required")
	}
	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		log.Fatalf("failed to create event bus: %v", err)
	}
	adapter := &TeamsAdapter{
		webhookURL: webhookURL,
		hmacSecret: os.Getenv("TEAMS_HMAC_SECRET"),
		bus:        bus,
		httpClient: &http.Client{},
	}
	go adapter.subscribeToEvents()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/messages", adapter.HandleMessages)
	log.Printf("Teams adapter listening on :19093")
	http.ListenAndServe(":19093", mux)
}

func (a *TeamsAdapter) verifySignature(r *http.Request, body []byte) bool {
	if a.hmacSecret == "" {
		return true
	}
	sig := r.Header.Get("Authorization")
	if sig == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(a.hmacSecret))
	mac.Write(body)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(sig), []byte("HMAC "+expected))
}

func (a *TeamsAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !a.verifySignature(r, body) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	action, _ := payload["action"].(string)
	data, _ := payload["data"].(map[string]interface{})
	if data == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	taskID, _ := data["task_id"].(string)
	switch action {
	case "approve":
		a.bus.Publish(r.Context(), events.Event{Type: events.ReviewApproved, TaskID: taskID})
	case "reject":
		a.bus.Publish(r.Context(), events.Event{Type: events.ReviewRejected, TaskID: taskID})
	}
	w.WriteHeader(http.StatusOK)
}

func (a *TeamsAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{
		events.TaskCompleted,
		events.ReviewRequested,
		events.EscalationCreated,
	}, func(e events.Event) error {
		switch e.Type {
		case events.TaskCompleted:
			return a.notifyTaskCompleted(e)
		case events.ReviewRequested:
			return a.sendApprovalRequest(e)
		case events.EscalationCreated:
			return a.sendEscalation(e)
		}
		return nil
	})
}

func (a *TeamsAdapter) notifyTaskCompleted(e events.Event) error {
	var payload struct {
		JiraKey string `json:"jira_key"`
		PRUrl   string `json:"pr_url"`
	}
	json.Unmarshal(e.Payload, &payload)
	msg := map[string]interface{}{
		"type": "message",
		"attachments": []map[string]interface{}{
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content":     fmt.Sprintf(`{"type":"AdaptiveCard","version":"1.4","body":[{"type":"TextBlock","text":"Task completed: %s\nPR: %s"}]}`, payload.JiraKey, payload.PRUrl),
			},
		},
	}
	return a.postToTeams(msg)
}

func (a *TeamsAdapter) sendApprovalRequest(e events.Event) error {
	var payload struct {
		TaskID  string `json:"task_id"`
		JiraKey string `json:"jira_key"`
		PRUrl   string `json:"pr_url"`
		Agent   string `json:"agent"`
	}
	json.Unmarshal(e.Payload, &payload)
	card := fmt.Sprintf(cardFmt, payload.JiraKey, payload.Agent, payload.PRUrl, payload.TaskID, payload.TaskID)
	msg := map[string]interface{}{
		"type": "message",
		"attachments": []map[string]interface{}{
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content":     card,
			},
		},
	}
	return a.postToTeams(msg)
}

func (a *TeamsAdapter) sendEscalation(e events.Event) error {
	var payload struct {
		TaskID  string `json:"task_id"`
		JiraKey string `json:"jira_key"`
		Reason  string `json:"reason"`
	}
	json.Unmarshal(e.Payload, &payload)
	msg := map[string]interface{}{
		"type": "message",
		"attachments": []map[string]interface{}{
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content":     fmt.Sprintf(`{"type":"AdaptiveCard","version":"1.4","body":[{"type":"TextBlock","text":"ESCALATION\nTask: %s\nTicket: %s\nReason: %s"}]}`, payload.TaskID, payload.JiraKey, payload.Reason),
			},
		},
	}
	return a.postToTeams(msg)
}

func (a *TeamsAdapter) postToTeams(msg interface{}) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	resp, err := a.httpClient.Post(a.webhookURL, "application/json", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("post to teams: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("teams webhook error %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (a *TeamsAdapter) HandleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	msg := map[string]interface{}{
		"type": "message",
		"attachments": []map[string]interface{}{
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content":     fmt.Sprintf(`{"type":"AdaptiveCard","version":"1.4","body":[{"type":"TextBlock","text":%q}]}`, req.Text),
			},
		},
	}
	if err := a.postToTeams(msg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
