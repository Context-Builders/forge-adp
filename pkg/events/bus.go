package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type EventType string

const (
	TaskCreated         EventType = "task.created"
	TaskStarted         EventType = "task.started"
	TaskBlocked         EventType = "task.blocked"
	TaskCompleted       EventType = "task.completed"
	TaskFailed          EventType = "task.failed"
	ReviewRequested     EventType = "review.requested"
	ReviewApproved      EventType = "review.approved"
	ReviewRejected      EventType = "review.rejected"
	DeploymentRequested EventType = "deployment.requested"
	DeploymentApproved  EventType = "deployment.approved"
	EscalationCreated   EventType = "escalation.created"

	// Governance events
	GovernanceAssessmentCompleted EventType = "governance.assessment.completed"
	GovernanceScheduledTrigger    EventType = "governance.scheduled.trigger"
	PolicyDenied                  EventType = "policy.denied"
)

type Event struct {
	ID        string          `json:"id"`
	Type      EventType       `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	AgentID   string          `json:"agent_id,omitempty"`
	TaskID    string          `json:"task_id,omitempty"`
	Payload   json.RawMessage `json:"payload"`
}

type Bus interface {
	Publish(ctx context.Context, event Event) error
	Subscribe(ctx context.Context, types []EventType, handler func(Event) error) error
	Close() error
}

type RedisBus struct {
	client *redis.Client
	stream string
}

func NewRedisBus(addr, stream string) (*RedisBus, error) {
	client := redis.NewClient(&redis.Options{
		Addr: addr,
	})

	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis connection failed: %w", err)
	}

	return &RedisBus{
		client: client,
		stream: stream,
	}, nil
}

func (b *RedisBus) Publish(ctx context.Context, event Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	return b.client.XAdd(ctx, &redis.XAddArgs{
		Stream: b.stream,
		Values: map[string]interface{}{
			"type": string(event.Type),
			"data": data,
		},
	}).Err()
}

func (b *RedisBus) Subscribe(ctx context.Context, types []EventType, handler func(Event) error) error {
	typeSet := make(map[EventType]bool)
	for _, t := range types {
		typeSet[t] = true
	}

	lastID := "$"
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		streams, err := b.client.XRead(ctx, &redis.XReadArgs{
			Streams: []string{b.stream, lastID},
			Block:   5 * time.Second,
			Count:   10,
		}).Result()

		if err == redis.Nil {
			continue
		}
		if err != nil {
			return fmt.Errorf("xread: %w", err)
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				lastID = msg.ID

				var event Event
				if data, ok := msg.Values["data"].(string); ok {
					if err := json.Unmarshal([]byte(data), &event); err != nil {
						continue
					}
				}

				if len(typeSet) == 0 || typeSet[event.Type] {
					if err := handler(event); err != nil {
						// Log error but continue processing
						fmt.Printf("handler error: %v\n", err)
					}
				}
			}
		}
	}
}

func (b *RedisBus) Close() error {
	return b.client.Close()
}
