package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/dotrage/forge-adp/pkg/events"
)

// GoogleChatAdapter handles bidirectional communication with Google Chat via
// the Chat API event endpoint (inbound) and Space Webhooks (outbound).
type GoogleChatAdapter struct {
	webhookURL        string
	verificationToken string
	bus               events.Bus
}

type ChatEvent struct {
	Type    string      `json:"type"`
	Message ChatMessage `json:"message,omitempty"`
	Action  ChatAction  `json:"action,omitempty"`
	Space   ChatSpace   `json:"space,omitempty"`
	User    ChatUser    `json:"user,omitempty"`
}

type ChatMessage struct {
	Name         string `json:"name"`
	Text         string `json:"text"`
	ArgumentText string `json:"argumentText,omitempty"`
}

type ChatAction struct {
	ActionMethodName string            `json:"actionMethodName"`
	Parameters       []ActionParameter `json:"parameters,omitempty"`
}

type ActionParameter struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ChatSpace struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
}

type ChatUser struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
}

type ChatOutboundMessage struct {
	Text  string     `json:"text,omitempty"`
	Cards []ChatCard `json:"cards,omitempty"`
}

type ChatCard struct {
	Header   *ChatCardHeader `json:"header,omitempty"`
	Sections []ChatSection   `json:"sections,omitempty"`
}

type ChatCardHeader struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
}

type ChatSection struct {
	Widgets []ChatWidget `json:"widgets"`
}

type ChatWidget struct {
	TextParagraph *TextParagraph `json:"textParagraph,omitempty"`
	Buttons       []ChatButton   `json:"buttons,omitempty"`
	KeyValue      *KeyValue      `json:"keyValue,omitempty"`
}

type TextParagraph struct {
	Text string `json:"text"`
}

type KeyValue struct {
	TopLabel         string `json:"topLabel"`
	Content          string `json:"content"`
	ContentMultiline bool   `json:"contentMultiline,omitempty"`
}

type ChatButton struct {
	TextButton *TextButton `json:"textButton,omitempty"`
}

type TextButton struct {
	Text    string   `json:"text"`
	OnClick *OnClick `json:"onClick"`
}

type OnClick struct {
	Action *CardAction `json:"action,omitempty"`
}

type CardAction struct {
	ActionMethodName string            `json:"actionMethodName"`
	Parameters       []ActionParameter `json:"parameters,omitempty"`
}

func main() {
	webhookURL := os.Getenv("GOOGLE_CHAT_WEBHOOK_URL")
	if webhookURL == "" {
		log.Fatal("GOOGLE_CHAT_WEBHOOK_URL is required")
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		log.Fatalf("failed to create event bus: %v", err)
	}

	adapter := &GoogleChatAdapter{
		webhookURL:        webhookURL,
		verificationToken: os.Getenv("GOOGLE_CHAT_VERIFICATION_TOKEN"),
		bus:               bus,
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/googlechat/events", adapter.HandleEvent)

	log.Printf("Google Chat adapter listening on :19094")
	http.ListenAndServe(":19094", mux)
}

func (a *GoogleChatAdapter) HandleEvent(w http.ResponseWriter, r *http.Request) {
	if a.verificationToken != "" {
		if !a.verifyToken(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	var event ChatEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch event.Type {
	case "ADDED_TO_SPACE":
		a.handleAddedToSpace(w, event)
	case "MESSAGE":
		a.handleMessage(r.Context(), w, event)
	case "CARD_CLICKED":
		a.handleCardClicked(r.Context(), w, event)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

func (a *GoogleChatAdapter) handleAddedToSpace(w http.ResponseWriter, event ChatEvent) {
	resp := ChatOutboundMessage{
		Text: fmt.Sprintf("Hi, I'm Forge! I'll keep *%s* updated on task progress.", event.Space.DisplayName),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (a *GoogleChatAdapter) handleMessage(ctx context.Context, w http.ResponseWriter, event ChatEvent) {
	text := strings.TrimSpace(event.Message.ArgumentText)
	resp := ChatOutboundMessage{
		Text: fmt.Sprintf("Forge received: %s", text),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (a *GoogleChatAdapter) handleCardClicked(ctx context.Context, w http.ResponseWriter, event ChatEvent) {
	taskID := ""
	for _, p := range event.Action.Parameters {
		if p.Key == "task_id" {
			taskID = p.Value
		}
	}

	switch event.Action.ActionMethodName {
	case "approve":
		a.bus.Publish(ctx, events.Event{Type: events.ReviewApproved, TaskID: taskID})
	case "reject":
		a.bus.Publish(ctx, events.Event{Type: events.ReviewRejected, TaskID: taskID})
	}

	resp := ChatOutboundMessage{
		Text: fmt.Sprintf("Action *%s* recorded for task `%s`.", event.Action.ActionMethodName, taskID),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (a *GoogleChatAdapter) subscribeToEvents() {
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

func (a *GoogleChatAdapter) notifyTaskCompleted(e events.Event) error {
	var payload struct {
		JiraKey string `json:"jira_key"`
		PRUrl   string `json:"pr_url"`
	}
	json.Unmarshal(e.Payload, &payload)

	msg := ChatOutboundMessage{
		Cards: []ChatCard{
			{
				Header: &ChatCardHeader{Title: "Task Completed", Subtitle: payload.JiraKey},
				Sections: []ChatSection{
					{Widgets: []ChatWidget{
						{TextParagraph: &TextParagraph{Text: "PR: " + payload.PRUrl}},
					}},
				},
			},
		},
	}
	return a.postWebhook(msg)
}

func (a *GoogleChatAdapter) sendApprovalRequest(e events.Event) error {
	var payload struct {
		TaskID  string `json:"task_id"`
		JiraKey string `json:"jira_key"`
		PRUrl   string `json:"pr_url"`
		Agent   string `json:"agent"`
	}
	json.Unmarshal(e.Payload, &payload)

	approveBtn := ChatButton{TextButton: &TextButton{
		Text: "Approve",
		OnClick: &OnClick{Action: &CardAction{
			ActionMethodName: "approve",
			Parameters:       []ActionParameter{{Key: "task_id", Value: payload.TaskID}},
		}},
	}}
	rejectBtn := ChatButton{TextButton: &TextButton{
		Text: "Request Changes",
		OnClick: &OnClick{Action: &CardAction{
			ActionMethodName: "reject",
			Parameters:       []ActionParameter{{Key: "task_id", Value: payload.TaskID}},
		}},
	}}

	msg := ChatOutboundMessage{
		Cards: []ChatCard{
			{
				Header: &ChatCardHeader{Title: "Review Requested", Subtitle: payload.JiraKey},
				Sections: []ChatSection{
					{
						Widgets: []ChatWidget{
							{KeyValue: &KeyValue{TopLabel: "Agent", Content: payload.Agent}},
							{KeyValue: &KeyValue{TopLabel: "PR", Content: payload.PRUrl}},
							{Buttons: []ChatButton{approveBtn, rejectBtn}},
						},
					},
				},
			},
		},
	}
	return a.postWebhook(msg)
}

func (a *GoogleChatAdapter) sendEscalation(e events.Event) error {
	var payload struct {
		TaskID  string `json:"task_id"`
		JiraKey string `json:"jira_key"`
		Reason  string `json:"reason"`
	}
	json.Unmarshal(e.Payload, &payload)

	msg := ChatOutboundMessage{
		Cards: []ChatCard{
			{
				Header: &ChatCardHeader{Title: "ESCALATION", Subtitle: "Task: " + payload.TaskID + " | " + payload.JiraKey},
				Sections: []ChatSection{
					{Widgets: []ChatWidget{
						{TextParagraph: &TextParagraph{Text: payload.Reason}},
					}},
				},
			},
		},
	}
	return a.postWebhook(msg)
}

func (a *GoogleChatAdapter) postWebhook(msg ChatOutboundMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	resp, err := http.Post(a.webhookURL, "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("google chat webhook returned %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (a *GoogleChatAdapter) verifyToken(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(auth) <= len(prefix) {
		return false
	}
	return auth[len(prefix):] == a.verificationToken
}
