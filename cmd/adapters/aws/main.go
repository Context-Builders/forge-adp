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

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
)

// AWSAdapter integrates with AWS via SNS HTTP subscriptions.
// CloudWatch Alarms → SNS → this adapter → Forge event bus.
// CodePipeline state changes → SNS → this adapter → Forge event bus.
// Forge DeploymentRequested events are forwarded to an SNS topic for CodePipeline ingestion.

type AWSAdapter struct {
	region           string
	accessKeyID      string
	secretAccessKey  string
	snsWebhookSecret string
	deployTopicARN   string
	orchestratorURL  string
	bus              events.Bus
	httpClient       *http.Client
}

type snsNotification struct {
	Type             string `json:"Type"`
	MessageID        string `json:"MessageId"`
	TopicArn         string `json:"TopicArn"`
	Subject          string `json:"Subject"`
	Message          string `json:"Message"`
	SubscribeURL     string `json:"SubscribeURL"`
	Token            string `json:"Token"`
	MessageAttribute map[string]struct {
		Type  string `json:"Type"`
		Value string `json:"Value"`
	} `json:"MessageAttributes"`
}

type cloudWatchAlarm struct {
	AlarmName        string `json:"AlarmName"`
	AlarmDescription string `json:"AlarmDescription"`
	NewStateValue    string `json:"NewStateValue"`
	OldStateValue    string `json:"OldStateValue"`
	NewStateReason   string `json:"NewStateReason"`
	Region           string `json:"Region"`
	AWSAccountID     string `json:"AWSAccountId"`
}

type codePipelineEvent struct {
	Version    string `json:"version"`
	Source     string `json:"source"`
	DetailType string `json:"detail-type"`
	Detail     struct {
		Pipeline        string `json:"pipeline"`
		ExecutionID     string `json:"execution-id"`
		State           string `json:"state"`
		Stage           string `json:"stage"`
		Action          string `json:"action"`
		ExternalEntityLink string `json:"externalEntityLink"`
	} `json:"detail"`
	Region string `json:"region"`
}

func main() {
	logger.Init("aws-adapter")

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &AWSAdapter{
		region:           os.Getenv("AWS_REGION"),
		accessKeyID:      os.Getenv("AWS_ACCESS_KEY_ID"),
		secretAccessKey:  os.Getenv("AWS_SECRET_ACCESS_KEY"),
		snsWebhookSecret: os.Getenv("AWS_SNS_WEBHOOK_SECRET"),
		deployTopicARN:   os.Getenv("AWS_DEPLOY_TOPIC_ARN"),
		orchestratorURL:  os.Getenv("ORCHESTRATOR_URL"),
		bus:              bus,
		httpClient:       &http.Client{},
	}
	if adapter.orchestratorURL == "" {
		adapter.orchestratorURL = "http://localhost:19080"
	}
	if adapter.region == "" {
		adapter.region = "us-east-1"
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook/sns", adapter.HandleSNS)
	mux.HandleFunc("/api/v1/alarms", adapter.HandleAlarms)
	mux.HandleFunc("/api/v1/stacks", adapter.HandleStacks)

	slog.Info("AWS adapter listening", slog.String("addr", ":19118"))
	http.ListenAndServe(":19118", logger.HTTPMiddleware("aws-adapter", mux))
}

func (a *AWSAdapter) HandleSNS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var notification snsNotification
	if err := json.NewDecoder(r.Body).Decode(&notification); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Confirm SNS subscription handshake
	if notification.Type == "SubscriptionConfirmation" {
		go func() {
			resp, err := a.httpClient.Get(notification.SubscribeURL)
			if err != nil {
				slog.Error("failed to confirm SNS subscription", slog.Any("error", err))
				return
			}
			resp.Body.Close()
			slog.Info("confirmed SNS subscription", slog.String("topic_arn", notification.TopicArn))
		}()
		w.WriteHeader(http.StatusOK)
		return
	}

	if notification.Type != "Notification" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Try CloudWatch alarm first
	var alarm cloudWatchAlarm
	if err := json.Unmarshal([]byte(notification.Message), &alarm); err == nil && alarm.AlarmName != "" {
		switch alarm.NewStateValue {
		case "ALARM":
			a.handleAlarmTriggered(r.Context(), alarm)
		case "OK":
			a.handleAlarmResolved(r.Context(), alarm)
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	// Try CodePipeline event
	var pipeline codePipelineEvent
	if err := json.Unmarshal([]byte(notification.Message), &pipeline); err == nil && pipeline.Detail.Pipeline != "" {
		a.handleCodePipeline(r.Context(), pipeline)
	}

	w.WriteHeader(http.StatusOK)
}

func (a *AWSAdapter) handleAlarmTriggered(ctx context.Context, alarm cloudWatchAlarm) {
	ep, _ := json.Marshal(map[string]interface{}{
		"alarm_name":   alarm.AlarmName,
		"description":  alarm.AlarmDescription,
		"reason":       alarm.NewStateReason,
		"region":       alarm.Region,
		"account_id":   alarm.AWSAccountID,
		"source":       "aws_cloudwatch",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
		slog.Error("failed to publish escalation event",
			slog.String("alarm_name", alarm.AlarmName),
			slog.Any("error", err))
	}
}

func (a *AWSAdapter) handleAlarmResolved(ctx context.Context, alarm cloudWatchAlarm) {
	ep, _ := json.Marshal(map[string]interface{}{
		"alarm_name": alarm.AlarmName,
		"region":     alarm.Region,
		"source":     "aws_cloudwatch",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
		slog.Error("failed to publish alarm resolved event",
			slog.String("alarm_name", alarm.AlarmName),
			slog.Any("error", err))
	}
}

func (a *AWSAdapter) handleCodePipeline(ctx context.Context, e codePipelineEvent) {
	slog.Info("codepipeline state change",
		slog.String("pipeline", e.Detail.Pipeline),
		slog.String("execution_id", e.Detail.ExecutionID),
		slog.String("state", e.Detail.State))

	ep, _ := json.Marshal(map[string]interface{}{
		"pipeline":     e.Detail.Pipeline,
		"execution_id": e.Detail.ExecutionID,
		"state":        e.Detail.State,
		"region":       e.Region,
		"source":       "aws_codepipeline",
	})

	switch e.Detail.State {
	case "SUCCEEDED":
		if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
			slog.Error("failed to publish pipeline succeeded event", slog.Any("error", err))
		}
	case "FAILED", "STOPPED":
		if err := a.bus.Publish(ctx, events.Event{Type: events.TaskFailed, Payload: ep}); err != nil {
			slog.Error("failed to publish pipeline failed event", slog.Any("error", err))
		}
	}
}

// HandleAlarms proxies CloudWatch alarm queries.
// In production, swap the stub response for an AWS SDK call using a.accessKeyID / a.secretAccessKey.
//
//	GET /api/v1/alarms[?name=<alarm-name>]
func (a *AWSAdapter) HandleAlarms(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := r.URL.Query().Get("name")
	result := map[string]interface{}{
		"region":      a.region,
		"filter_name": name,
		"note":        "use AWS SDK (github.com/aws/aws-sdk-go-v2) with AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY to query CloudWatch alarms directly",
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleStacks proxies CloudFormation stack queries.
// In production, swap the stub response for an AWS SDK call.
//
//	GET /api/v1/stacks[?name=<stack-name>]
func (a *AWSAdapter) HandleStacks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := r.URL.Query().Get("name")
	result := map[string]interface{}{
		"region":     a.region,
		"filter_name": name,
		"note":       "use AWS SDK (github.com/aws/aws-sdk-go-v2) with AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY to query CloudFormation stacks directly",
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for DeploymentRequested events and forwards them
// to the orchestrator for AWS deployment processing.
func (a *AWSAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.DeploymentRequested}, func(e events.Event) error {
		slog.Info("received deployment requested event, forwarding to orchestrator")

		var payload map[string]interface{}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal deployment payload: %w", err)
		}
		payload["provider"] = "aws"
		payload["region"] = a.region

		body, _ := json.Marshal(payload)
		url := fmt.Sprintf("%s/api/v1/deployments", a.orchestratorURL)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create deployment request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("forward deployment: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			b, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("orchestrator returned %d: %s", resp.StatusCode, string(b))
		}
		slog.Info("forwarded deployment to orchestrator", slog.Int("status", resp.StatusCode))
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to deployment events", slog.Any("error", err))
	}
}
