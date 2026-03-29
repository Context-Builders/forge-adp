package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

type SlackAdapter struct {
	client          *slack.Client
	socketClient    *socketmode.Client
	signingSecret   string
	orchestratorURL string
	bus             events.Bus
}

func main() {
	logger.Init("slack-adapter")

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	client := slack.New(
		os.Getenv("SLACK_BOT_TOKEN"),
		slack.OptionAppLevelToken(os.Getenv("SLACK_APP_TOKEN")),
	)
	socketClient := socketmode.New(client)

	adapter := &SlackAdapter{
		client:          client,
		socketClient:    socketClient,
		signingSecret:   os.Getenv("SLACK_SIGNING_SECRET"),
		orchestratorURL: os.Getenv("ORCHESTRATOR_URL"),
		bus:             bus,
	}
	if adapter.orchestratorURL == "" {
		adapter.orchestratorURL = "http://localhost:19080"
	}

	go adapter.handleSocketMode()
	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/slack/commands", adapter.HandleSlashCommand)
	mux.HandleFunc("/slack/interactive", adapter.HandleInteractive)

	slog.Info("Slack adapter listening", slog.String("addr", ":19092"))
	http.ListenAndServe(":19092", logger.HTTPMiddleware("slack-adapter", mux))
}

// verifyRequest validates the Slack signing secret on incoming HTTP requests.
// It returns the raw body so callers don't need to re-read it.
func (a *SlackAdapter) verifyRequest(r *http.Request) ([]byte, error) {
	if a.signingSecret == "" {
		return io.ReadAll(r.Body)
	}
	verifier, err := slack.NewSecretsVerifier(r.Header, a.signingSecret)
	if err != nil {
		return nil, fmt.Errorf("create verifier: %w", err)
	}
	body, err := io.ReadAll(io.TeeReader(r.Body, &verifier))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if err := verifier.Ensure(); err != nil {
		return nil, fmt.Errorf("invalid signature: %w", err)
	}
	return body, nil
}

// handleSocketMode processes Socket Mode events — app mentions and interactive
// payloads delivered over the persistent WebSocket connection.
func (a *SlackAdapter) handleSocketMode() {
	for evt := range a.socketClient.Events {
		switch evt.Type {
		case socketmode.EventTypeEventsAPI:
			eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
			if !ok {
				a.socketClient.Ack(*evt.Request)
				continue
			}
			a.socketClient.Ack(*evt.Request)
			if eventsAPIEvent.Type == slackevents.CallbackEvent {
				a.handleInnerEvent(eventsAPIEvent.InnerEvent)
			}

		case socketmode.EventTypeInteractive:
			callback, ok := evt.Data.(slack.InteractionCallback)
			if !ok {
				a.socketClient.Ack(*evt.Request)
				continue
			}
			a.socketClient.Ack(*evt.Request)
			a.handleInteractiveCallback(context.Background(), callback)

		case socketmode.EventTypeSlashCommand:
			cmd, ok := evt.Data.(slack.SlashCommand)
			if !ok {
				a.socketClient.Ack(*evt.Request)
				continue
			}
			a.socketClient.Ack(*evt.Request, a.buildForgeCommandResponse(cmd))
		}
	}
}

// handleInnerEvent processes app_mention and message events from Socket Mode.
func (a *SlackAdapter) handleInnerEvent(evt slackevents.EventsAPIInnerEvent) {
	switch e := evt.Data.(type) {
	case *slackevents.AppMentionEvent:
		text := strings.TrimSpace(strings.TrimPrefix(e.Text, "<@"+e.Text[:strings.Index(e.Text, ">")+1]))
		slog.Info("app mention received",
			slog.String("channel", e.Channel),
			slog.String("text", text))
		_, _, err := a.client.PostMessage(e.Channel,
			slack.MsgOptionText("Got it. Use `/forge` to interact with Forge.", false),
			slack.MsgOptionTS(e.TimeStamp),
		)
		if err != nil {
			slog.Error("failed to reply to mention", slog.Any("error", err))
		}
	}
}

// subscribeToEvents listens for Forge bus events and posts to the appropriate
// Slack channels.
func (a *SlackAdapter) subscribeToEvents() {
	ctx := context.Background()
	err := a.bus.Subscribe(ctx, []events.EventType{
		events.TaskCompleted,
		events.TaskFailed,
		events.TaskBlocked,
		events.ReviewRequested,
		events.EscalationCreated,
		events.PolicyDenied,
	}, func(e events.Event) error {
		switch e.Type {
		case events.TaskCompleted:
			return a.notifyTaskCompleted(e)
		case events.TaskFailed:
			return a.notifyTaskFailed(e)
		case events.TaskBlocked:
			return a.notifyTaskBlocked(e)
		case events.ReviewRequested:
			return a.sendApprovalRequest(e)
		case events.EscalationCreated:
			return a.sendEscalation(e)
		case events.PolicyDenied:
			return a.notifyPolicyDenied(e)
		}
		return nil
	})
	if err != nil {
		slog.Error("event subscription ended", slog.Any("error", err))
	}
}

func (a *SlackAdapter) notifyTaskCompleted(e events.Event) error {
	var payload struct {
		JiraKey string `json:"jira_key"`
		PRUrl   string `json:"pr_url"`
		Agent   string `json:"agent"`
		Repo    string `json:"repo"`
		Source  string `json:"source"`
	}
	json.Unmarshal(e.Payload, &payload)

	text := fmt.Sprintf("*Task Completed* — %s", coalesce(payload.JiraKey, e.TaskID))
	var fields []*slack.TextBlockObject
	if payload.Agent != "" {
		fields = append(fields, slack.NewTextBlockObject("mrkdwn", "*Agent*\n"+payload.Agent, false, false))
	}
	if payload.Repo != "" {
		fields = append(fields, slack.NewTextBlockObject("mrkdwn", "*Repo*\n"+payload.Repo, false, false))
	}

	blocks := []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject("plain_text", ":white_check_mark: Task Completed", true, false)),
		slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", text, false, false), fields, nil),
	}
	if payload.PRUrl != "" {
		blocks = append(blocks,
			slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*PR:* <%s|View Pull Request>", payload.PRUrl), false, false),
				nil, nil,
			),
		)
	}
	blocks = append(blocks, slack.NewDividerBlock())

	_, _, err := a.client.PostMessage(
		os.Getenv("FORGE_STATUS_CHANNEL"),
		slack.MsgOptionBlocks(blocks...),
	)
	return err
}

func (a *SlackAdapter) notifyTaskFailed(e events.Event) error {
	var payload struct {
		JiraKey    string `json:"jira_key"`
		Agent      string `json:"agent"`
		SkillName  string `json:"skill_name"`
		Reason     string `json:"reason"`
		Source     string `json:"source"`
	}
	json.Unmarshal(e.Payload, &payload)

	text := fmt.Sprintf("*Task Failed* — %s", coalesce(payload.JiraKey, e.TaskID))
	if payload.Reason != "" {
		text += "\n" + payload.Reason
	}

	blocks := []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject("plain_text", ":x: Task Failed", true, false)),
		slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", text, false, false), nil, nil),
	}
	if payload.Agent != "" || payload.SkillName != "" {
		var fields []*slack.TextBlockObject
		if payload.Agent != "" {
			fields = append(fields, slack.NewTextBlockObject("mrkdwn", "*Agent*\n"+payload.Agent, false, false))
		}
		if payload.SkillName != "" {
			fields = append(fields, slack.NewTextBlockObject("mrkdwn", "*Skill*\n"+payload.SkillName, false, false))
		}
		blocks = append(blocks, slack.NewSectionBlock(nil, fields, nil))
	}
	blocks = append(blocks, slack.NewDividerBlock())

	_, _, err := a.client.PostMessage(
		os.Getenv("FORGE_STATUS_CHANNEL"),
		slack.MsgOptionBlocks(blocks...),
	)
	return err
}

func (a *SlackAdapter) notifyTaskBlocked(e events.Event) error {
	var payload struct {
		JiraKey string `json:"jira_key"`
		Agent   string `json:"agent"`
		Reason  string `json:"reason"`
	}
	json.Unmarshal(e.Payload, &payload)

	text := fmt.Sprintf("*Task Blocked* — %s", coalesce(payload.JiraKey, e.TaskID))
	if payload.Reason != "" {
		text += "\n" + payload.Reason
	}

	blocks := []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject("plain_text", ":warning: Task Blocked", true, false)),
		slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", text, false, false), nil, nil),
		slack.NewDividerBlock(),
	}

	_, _, err := a.client.PostMessage(
		os.Getenv("FORGE_STATUS_CHANNEL"),
		slack.MsgOptionBlocks(blocks...),
	)
	return err
}

func (a *SlackAdapter) sendApprovalRequest(e events.Event) error {
	var payload struct {
		TaskID    string `json:"task_id"`
		JiraKey   string `json:"jira_key"`
		PRUrl     string `json:"pr_url"`
		Agent     string `json:"agent"`
		SkillName string `json:"skill_name"`
		Repo      string `json:"repo"`
	}
	json.Unmarshal(e.Payload, &payload)
	taskID := coalesce(payload.TaskID, e.TaskID)

	summary := fmt.Sprintf("*Ticket:* %s", coalesce(payload.JiraKey, "_unknown_"))
	if payload.Agent != "" {
		summary += fmt.Sprintf("\n*Agent:* %s", payload.Agent)
	}
	if payload.SkillName != "" {
		summary += fmt.Sprintf("\n*Skill:* %s", payload.SkillName)
	}
	if payload.Repo != "" {
		summary += fmt.Sprintf("\n*Repo:* %s", payload.Repo)
	}
	if payload.PRUrl != "" {
		summary += fmt.Sprintf("\n*PR:* <%s|View Pull Request>", payload.PRUrl)
	}

	blocks := []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject("plain_text", ":mag: Review Requested", true, false)),
		slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", summary, false, false), nil, nil),
		slack.NewDividerBlock(),
		slack.NewActionBlock(
			taskID,
			slack.NewButtonBlockElement("approve", taskID,
				slack.NewTextBlockObject("plain_text", "Approve", false, false)).
				WithStyle(slack.StylePrimary),
			slack.NewButtonBlockElement("reject", taskID,
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
		TaskID      string `json:"task_id"`
		JiraKey     string `json:"jira_key"`
		Reason      string `json:"reason"`
		AlarmName   string `json:"alarm_name"`
		IncidentID  string `json:"incident_id"`
		Policy      string `json:"policy"`
		Source      string `json:"source"`
	}
	json.Unmarshal(e.Payload, &payload)

	title := coalesce(payload.AlarmName, payload.Reason, "Escalation")
	details := fmt.Sprintf("*Task:* %s", coalesce(payload.TaskID, e.TaskID, "_unknown_"))
	if payload.JiraKey != "" {
		details += fmt.Sprintf("\n*Ticket:* %s", payload.JiraKey)
	}
	if payload.IncidentID != "" {
		details += fmt.Sprintf("\n*Incident:* %s", payload.IncidentID)
	}
	if payload.Source != "" {
		details += fmt.Sprintf("\n*Source:* %s", payload.Source)
	}
	if payload.Reason != "" && payload.Reason != title {
		details += fmt.Sprintf("\n*Reason:* %s", payload.Reason)
	}

	blocks := []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject("plain_text", ":rotating_light: Escalation: "+title, true, false)),
		slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", details, false, false), nil, nil),
		slack.NewDividerBlock(),
	}

	_, _, err := a.client.PostMessage(
		os.Getenv("FORGE_ESCALATIONS_CHANNEL"),
		slack.MsgOptionBlocks(blocks...),
	)
	return err
}

func (a *SlackAdapter) notifyPolicyDenied(e events.Event) error {
	var payload struct {
		TaskID    string `json:"task_id"`
		JiraKey   string `json:"jira_key"`
		SkillName string `json:"skill_name"`
		Policy    string `json:"policy"`
		Reason    string `json:"reason"`
	}
	json.Unmarshal(e.Payload, &payload)

	text := fmt.Sprintf("*Policy Denied* — %s", coalesce(payload.JiraKey, e.TaskID))
	if payload.SkillName != "" {
		text += fmt.Sprintf("\n*Skill:* %s", payload.SkillName)
	}
	if payload.Policy != "" {
		text += fmt.Sprintf("\n*Policy:* %s", payload.Policy)
	}
	if payload.Reason != "" {
		text += fmt.Sprintf("\n*Reason:* %s", payload.Reason)
	}

	blocks := []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject("plain_text", ":no_entry: Policy Denied", true, false)),
		slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", text, false, false), nil, nil),
		slack.NewDividerBlock(),
	}

	_, _, err := a.client.PostMessage(
		os.Getenv("FORGE_ESCALATIONS_CHANNEL"),
		slack.MsgOptionBlocks(blocks...),
	)
	return err
}

// HandleSlashCommand processes incoming /forge slash commands from Slack's
// HTTP endpoint (used when not running Socket Mode, or as a fallback).
func (a *SlackAdapter) HandleSlashCommand(w http.ResponseWriter, r *http.Request) {
	if _, err := a.verifyRequest(r); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Re-parse after body has been consumed by verifyRequest via TeeReader.
	// verifyRequest returns the body but SlashCommandParse reads r.Body — we
	// restore it here.
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cmd, err := slack.SlashCommandParse(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if cmd.Command != "/forge" {
		w.WriteHeader(http.StatusOK)
		return
	}

	resp := a.buildForgeCommandResponse(cmd)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// buildForgeCommandResponse parses a /forge slash command and calls the
// orchestrator for approve/reject sub-commands, or returns a help message.
//
//	/forge approve <task_id>
//	/forge reject  <task_id> [reason...]
//	/forge status  <task_id>
//	/forge list
func (a *SlackAdapter) buildForgeCommandResponse(cmd slack.SlashCommand) slack.Msg {
	parts := strings.Fields(cmd.Text)
	if len(parts) == 0 {
		return slack.Msg{
			ResponseType: slack.ResponseTypeEphemeral,
			Text: "Usage:\n• `/forge approve <task_id>`\n• `/forge reject <task_id> [reason]`\n• `/forge status <task_id>`\n• `/forge list`",
		}
	}

	sub := strings.ToLower(parts[0])
	ctx := context.Background()

	switch sub {
	case "approve":
		if len(parts) < 2 {
			return slack.Msg{ResponseType: slack.ResponseTypeEphemeral, Text: "Usage: `/forge approve <task_id>`"}
		}
		taskID := parts[1]
		if err := a.orchestratorPost(ctx, fmt.Sprintf("/api/v1/tasks/%s/approve", taskID), nil); err != nil {
			slog.Error("failed to approve task", slog.String("task_id", taskID), slog.Any("error", err))
			return slack.Msg{ResponseType: slack.ResponseTypeEphemeral, Text: fmt.Sprintf("Failed to approve task %s: %v", taskID, err)}
		}
		return slack.Msg{ResponseType: slack.ResponseTypeInChannel, Text: fmt.Sprintf(":white_check_mark: Task `%s` approved by <@%s>", taskID, cmd.UserID)}

	case "reject":
		if len(parts) < 2 {
			return slack.Msg{ResponseType: slack.ResponseTypeEphemeral, Text: "Usage: `/forge reject <task_id> [reason]`"}
		}
		taskID := parts[1]
		reason := strings.Join(parts[2:], " ")
		if reason == "" {
			reason = fmt.Sprintf("rejected via Slack by %s", cmd.UserName)
		}
		body := map[string]string{"reason": reason}
		if err := a.orchestratorPost(ctx, fmt.Sprintf("/api/v1/tasks/%s/reject", taskID), body); err != nil {
			slog.Error("failed to reject task", slog.String("task_id", taskID), slog.Any("error", err))
			return slack.Msg{ResponseType: slack.ResponseTypeEphemeral, Text: fmt.Sprintf("Failed to reject task %s: %v", taskID, err)}
		}
		return slack.Msg{ResponseType: slack.ResponseTypeInChannel, Text: fmt.Sprintf(":x: Task `%s` rejected by <@%s>: %s", taskID, cmd.UserID, reason)}

	case "status":
		if len(parts) < 2 {
			return slack.Msg{ResponseType: slack.ResponseTypeEphemeral, Text: "Usage: `/forge status <task_id>`"}
		}
		taskID := parts[1]
		var task map[string]interface{}
		if err := a.orchestratorGet(ctx, fmt.Sprintf("/api/v1/tasks/%s", taskID), &task); err != nil {
			return slack.Msg{ResponseType: slack.ResponseTypeEphemeral, Text: fmt.Sprintf("Could not retrieve task %s: %v", taskID, err)}
		}
		status, _ := task["status"].(string)
		agent, _ := task["agent_role"].(string)
		return slack.Msg{
			ResponseType: slack.ResponseTypeEphemeral,
			Text:         fmt.Sprintf("*Task* `%s`\n*Status:* %s\n*Agent:* %s", taskID, status, agent),
		}

	case "list":
		var result map[string]interface{}
		if err := a.orchestratorGet(ctx, "/api/v1/tasks?status=pending", &result); err != nil {
			return slack.Msg{ResponseType: slack.ResponseTypeEphemeral, Text: fmt.Sprintf("Could not list tasks: %v", err)}
		}
		return slack.Msg{ResponseType: slack.ResponseTypeEphemeral, Text: fmt.Sprintf("Active tasks: %v", result)}

	default:
		return slack.Msg{
			ResponseType: slack.ResponseTypeEphemeral,
			Text:         fmt.Sprintf("Unknown sub-command `%s`. Try `approve`, `reject`, `status`, or `list`.", sub),
		}
	}
}

// HandleInteractive processes Slack interactive payloads (button clicks) from
// the HTTP endpoint. Approve/reject actions call the orchestrator directly.
func (a *SlackAdapter) HandleInteractive(w http.ResponseWriter, r *http.Request) {
	body, err := a.verifyRequest(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Interactive payloads arrive as form-encoded "payload" field
	formPayload := r.FormValue("payload")
	if formPayload == "" {
		// Fall back to raw body if verifyRequest already consumed the form
		var form map[string][]string
		if err := json.Unmarshal(body, &form); err == nil {
			if v, ok := form["payload"]; ok && len(v) > 0 {
				formPayload = v[0]
			}
		}
	}

	var callback slack.InteractionCallback
	if err := json.Unmarshal([]byte(formPayload), &callback); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	a.handleInteractiveCallback(r.Context(), callback)
	w.WriteHeader(http.StatusOK)
}

func (a *SlackAdapter) handleInteractiveCallback(ctx context.Context, callback slack.InteractionCallback) {
	for _, action := range callback.ActionCallback.BlockActions {
		taskID := action.Value
		switch action.ActionID {
		case "approve":
			if err := a.orchestratorPost(ctx, fmt.Sprintf("/api/v1/tasks/%s/approve", taskID), nil); err != nil {
				slog.Error("failed to approve task from interactive", slog.String("task_id", taskID), slog.Any("error", err))
				return
			}
			a.bus.Publish(ctx, events.Event{Type: events.ReviewApproved, TaskID: taskID})
			slog.Info("task approved via Slack button",
				slog.String("task_id", taskID),
				slog.String("user", callback.User.Name))

		case "reject":
			reason := fmt.Sprintf("rejected via Slack by %s", callback.User.Name)
			if err := a.orchestratorPost(ctx, fmt.Sprintf("/api/v1/tasks/%s/reject", taskID),
				map[string]string{"reason": reason}); err != nil {
				slog.Error("failed to reject task from interactive", slog.String("task_id", taskID), slog.Any("error", err))
				return
			}
			a.bus.Publish(ctx, events.Event{Type: events.ReviewRejected, TaskID: taskID})
			slog.Info("task rejected via Slack button",
				slog.String("task_id", taskID),
				slog.String("user", callback.User.Name))
		}
	}
}

func (a *SlackAdapter) orchestratorPost(ctx context.Context, path string, body interface{}) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.orchestratorURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("orchestrator returned %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (a *SlackAdapter) orchestratorGet(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.orchestratorURL+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("orchestrator returned %d: %s", resp.StatusCode, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// coalesce returns the first non-empty string from the provided values.
func coalesce(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
