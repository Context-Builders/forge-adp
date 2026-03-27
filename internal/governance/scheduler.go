// Package governance provides the cron-based scheduler that automatically
// triggers governance agent tasks at configured intervals.
//
// Schedules:
//   - compliance-report      : every Monday at 08:00 UTC (weekly)
//   - policy-drift-detection : 1st of every month at 06:00 UTC (monthly)
package governance

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/google/uuid"
)

// SchedulerConfig holds the configuration for the governance scheduler.
type SchedulerConfig struct {
	ProjectID   string
	CompanyID   string
	TaskCreator TaskCreatorFunc
	Bus         events.Bus
}

// TaskCreatorFunc is a callback that creates a task in the orchestrator.
// It matches the signature of orchestrator.Orchestrator.CreateTask so the
// scheduler can be cleanly wired without an import cycle.
type TaskCreatorFunc func(ctx context.Context, task ScheduledTask) error

// ScheduledTask is a minimal task descriptor passed to TaskCreatorFunc.
type ScheduledTask struct {
	ID        string
	AgentRole string
	SkillName string
	Input     json.RawMessage
}

// Scheduler fires governance tasks on a cron-like schedule.
type Scheduler struct {
	cfg SchedulerConfig
}

// New creates a new Scheduler.
func New(cfg SchedulerConfig) *Scheduler {
	return &Scheduler{cfg: cfg}
}

// Run blocks until ctx is cancelled, firing scheduled governance tasks.
func (s *Scheduler) Run(ctx context.Context) {
	log.Println("[governance-scheduler] started")
	complianceTicker := s.nextTicker(ctx, s.nextWeeklyMonday)
	driftTicker := s.nextTicker(ctx, s.nextMonthlyFirst)
	for {
		select {
		case <-ctx.Done():
			log.Println("[governance-scheduler] stopped")
			return
		case <-complianceTicker:
			s.fire(ctx, "compliance-report")
			complianceTicker = s.nextTicker(ctx, s.nextWeeklyMonday)
		case <-driftTicker:
			s.fire(ctx, "policy-drift-detection")
			driftTicker = s.nextTicker(ctx, s.nextMonthlyFirst)
		}
	}
}

// fire enqueues a governance task and publishes a trigger event.
func (s *Scheduler) fire(ctx context.Context, skillName string) {
	input, _ := json.Marshal(map[string]string{
		"project_id": s.cfg.ProjectID,
		"triggered":  "scheduled",
	})
	task := ScheduledTask{
		ID:        uuid.New().String(),
		AgentRole: "governance",
		SkillName: skillName,
		Input:     input,
	}
	if err := s.cfg.TaskCreator(ctx, task); err != nil {
		log.Printf("[governance-scheduler] failed to create %s task: %v", skillName, err)
		return
	}
	log.Printf("[governance-scheduler] queued governance/%s task %s", skillName, task.ID)
	payload, _ := json.Marshal(map[string]string{
		"skill_name": skillName,
		"task_id":    task.ID,
		"project_id": s.cfg.ProjectID,
	})
	_ = s.cfg.Bus.Publish(ctx, events.Event{
		ID:      uuid.New().String(),
		Type:    events.GovernanceScheduledTrigger,
		Payload: payload,
	})
}

// nextTicker returns a channel that fires once at the next scheduled time.
func (s *Scheduler) nextTicker(ctx context.Context, next func(time.Time) time.Time) <-chan struct{} {
	ch := make(chan struct{}, 1)
	fireAt := next(time.Now().UTC())
	go func() {
		timer := time.NewTimer(time.Until(fireAt))
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
			ch <- struct{}{}
		}
	}()
	return ch
}

// nextWeeklyMonday returns the next Monday at 08:00 UTC on or after now.
func (s *Scheduler) nextWeeklyMonday(now time.Time) time.Time {
	daysUntil := (int(time.Monday) - int(now.Weekday()) + 7) % 7
	if daysUntil == 0 && now.Hour() >= 8 {
		daysUntil = 7
	}
	t := now.AddDate(0, 0, daysUntil)
	return time.Date(t.Year(), t.Month(), t.Day(), 8, 0, 0, 0, time.UTC)
}

// nextMonthlyFirst returns the 1st of the next month at 06:00 UTC.
func (s *Scheduler) nextMonthlyFirst(now time.Time) time.Time {
	year, month, _ := now.Date()
	if now.Day() == 1 && now.Hour() < 6 {
		return time.Date(year, month, 1, 6, 0, 0, 0, time.UTC)
	}
	return time.Date(year, month+1, 1, 6, 0, 0, 0, time.UTC)
}
