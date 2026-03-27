package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"github.com/dotrage/forge-adp/pkg/events"
)

type SlackAdapter struct {
	client       *slack.Client
	socketClient *socketmode.Client
	bus          events.Bus
}

func main() {
	client := slack.New(
		os.Getenv("SLACK_BOT_TOKEN"),
		slack.OptionAppLevelToken(os.Getenv("SLACK_APP_TOKEN")),
	)
	socketClient := socketmode.New(client)

	bus, _ := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")

	adapter := &SlackAdapter{
		client:       client,
		socketClient: socketClient,
		bus:          bus,
	}

	go adapter.handleSocketMode()
	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/slack/commands", adapter.HandleSlashCommand)
	mux.HandleFunc("/slack/interactive", adapter.HandleInteractive)

	log.Printf("Slack adapter listening on :19092")
	http.ListenAndServe(":19092", mux)
}

func (a *SlackAdapter) handleSocketMode() {
	for evt := range a.socketClient.Events {
		switch evt.Type {
		case socketmode.EventTypeEventsAPI:
			a.socketClient.Ack(*evt.Request)
		}
	}
}

func (a *SlackAdapter) subscribeToEvents() {
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

func (a *SlackAdapter) notifyTaskCompleted(e events.Event) error {
	var payload struct {
		JiraKey string `json:"jira_key"`
		PRUrl   string `json:"pr_url"`
	}
	json.Unmarshal(e.Payload, &payload)

	_, _, err := a.client.PostMessage(
		os.Getenv("FORGE_STATUS_CHANNEL"),
		slack.MsgOptionText("Task completed: "+payload.JiraKey+"\nPR: "+payload.PRUrl, false),
	)
	return err
}

func (a *SlackAdapter) sendApprovalRequest(e events.Event) error {
	var payload struct {
		TaskID  string `json:"task_id"`
		JiraKey string `json:"jira_key"`
		PRUrl   string `json:"pr_url"`
		Agent   string `json:"agent"`
	}
	json.Unmarshal(e.Payload, &payload)

	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn",
				"*Review Requested*\nTicket: "+payload.JiraKey+"\nAgent: "+payload.Agent+"\nPR: "+payload.PRUrl,
				false, false),
			nil, nil,
		),
		slack.NewActionBlock(
			payload.TaskID,
			slack.NewButtonBlockElement("approve", payload.TaskID,
				slack.NewTextBlockObject("plain_text", "Approve", false, false)).
				WithStyle(slack.StylePrimary),
			slack.NewButtonBlockElement("reject", payload.TaskID,
				slack.NewTextBlockObject("plain_text", "Request Changes", false, false)).
				WithStyle(slack.StyleDanger),
		),
	}

	_, _, err := a.client.PostMessage(
		os.Getenv("FORGE_APPROVALS_CHANNEL"),
		slack.MsgOptionBlocks(blocks...),
	)
	return err
}

func (a *SlackAdapter) sendEscalation(e events.Event) error {
	var payload struct {
		TaskID  string `json:"task_id"`
		JiraKey string `json:"jira_key"`
		Reason  string `json:"reason"`
	}
	json.Unmarshal(e.Payload, &payload)

	_, _, err := a.client.PostMessage(
		os.Getenv("FORGE_ESCALATIONS_CHANNEL"),
		slack.MsgOptionText("ESCALATION\nTask: "+payload.TaskID+"\nTicket: "+payload.JiraKey+"\nReason: "+payload.Reason, false),
	)
	return err
}

func (a *SlackAdapter) HandleSlashCommand(w http.ResponseWriter, r *http.Request) {
	cmd, err := slack.SlashCommandParse(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch cmd.Command {
	case "/forge":
		a.handleForgeCommand(w, cmd)
	}
}

func (a *SlackAdapter) handleForgeCommand(w http.ResponseWriter, cmd slack.SlashCommand) {
	response := slack.Msg{
		ResponseType: slack.ResponseTypeInChannel,
		Text:         "Forge command received: " + cmd.Text,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (a *SlackAdapter) HandleInteractive(w http.ResponseWriter, r *http.Request) {
	var payload slack.InteractionCallback
	if err := json.Unmarshal([]byte(r.FormValue("payload")), &payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	for _, action := range payload.ActionCallback.BlockActions {
		taskID := action.Value
		switch action.ActionID {
		case "approve":
			a.bus.Publish(r.Context(), events.Event{Type: events.ReviewApproved, TaskID: taskID})
		case "reject":
			a.bus.Publish(r.Context(), events.Event{Type: events.ReviewRejected, TaskID: taskID})
		}
	}
	w.WriteHeader(http.StatusOK)
}
