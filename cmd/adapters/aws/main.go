package main

import (
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

// AWS adapter integrates with AWS CloudWatch Alarms via SNS HTTP subscriptions.
// It also exposes endpoints to interact with AWS services via the AWS SDK.

type AWSAdapter struct {
	region           string
	snsWebhookSecret string
	bus              events.Bus
	httpClient       *http.Client
}

type snsNotification struct {
	Type         string `json:"Type"`
	MessageID    string `json:"MessageId"`
	TopicArn     string `json:"TopicArn"`
	Subject      string `json:"Subject"`
	Message      string `json:"Message"`
	SubscribeURL string `json:"SubscribeURL"`
	Token        string `json:"Token"`
}

type cloudWatchAlarm struct {
	AlarmName        string `json:"AlarmName"`
	AlarmDescription string `json:"AlarmDescription"`
	NewStateValue    string `json:"NewStateValue"`
	OldStateValue    string `json:"OldStateValue"`
	NewStateReason   string `json:"NewStateReason"`
	Region           string `json:"Region"`
}

func main() {
	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		log.Fatalf("failed to create event bus: %v", err)
	}

	adapter := &AWSAdapter{
		region:           os.Getenv("AWS_REGION"),
		snsWebhookSecret: os.Getenv("AWS_SNS_WEBHOOK_SECRET"),
		bus:              bus,
		httpClient:       &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook/sns", adapter.HandleSNS)
	mux.HandleFunc("/api/v1/alarms", adapter.HandleAlarms)
	mux.HandleFunc("/api/v1/stacks", adapter.HandleStacks)

	log.Printf("AWS adapter listening on :19118")
	http.ListenAndServe(":19118", mux)
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

	// Handle SNS subscription confirmation
	if notification.Type == "SubscriptionConfirmation" {
		go func() {
			resp, err := a.httpClient.Get(notification.SubscribeURL)
			if err != nil {
				log.Printf("failed to confirm SNS subscription: %v", err)
				return
			}
			resp.Body.Close()
			log.Printf("confirmed SNS subscription for topic: %s", notification.TopicArn)
		}()
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse CloudWatch alarm notification
	if notification.Type == "Notification" {
		var alarm cloudWatchAlarm
		if err := json.Unmarshal([]byte(notification.Message), &alarm); err == nil {
			switch alarm.NewStateValue {
			case "ALARM":
				a.handleAlarmTriggered(r.Context(), alarm)
			case "OK":
				a.handleAlarmResolved(r.Context(), alarm)
			}
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (a *AWSAdapter) handleAlarmTriggered(ctx context.Context, alarm cloudWatchAlarm) {
	ep, _ := json.Marshal(map[string]interface{}{
		"alarm_name":  alarm.AlarmName,
		"description": alarm.AlarmDescription,
		"reason":      alarm.NewStateReason,
		"region":      alarm.Region,
		"source":      "aws",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
		log.Printf("failed to publish escalation event: %v", err)
	}
}

func (a *AWSAdapter) handleAlarmResolved(ctx context.Context, alarm cloudWatchAlarm) {
	ep, _ := json.Marshal(map[string]interface{}{
		"alarm_name": alarm.AlarmName,
		"region":     alarm.Region,
		"source":     "aws",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
		log.Printf("failed to publish task completed event: %v", err)
	}
}

func (a *AWSAdapter) HandleAlarms(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Return placeholder — actual CloudWatch calls use the AWS SDK
		result := map[string]interface{}{
			"message": fmt.Sprintf("CloudWatch alarms endpoint — use AWS SDK with region %s", a.region),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *AWSAdapter) HandleStacks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		result := map[string]interface{}{
			"message": fmt.Sprintf("CloudFormation stacks endpoint — use AWS SDK with region %s", a.region),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *AWSAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{events.DeploymentRequested}, func(e events.Event) error {
		return nil
	})
}

// placeholder unused import compliance
var _ = strings.TrimRight
var _ = io.ReadAll
