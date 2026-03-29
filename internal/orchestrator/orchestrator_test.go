package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/google/uuid"
)

// --- fake event bus ---------------------------------------------------------

type fakeEvent struct {
	event events.Event
}

type fakeBus struct {
	published []fakeEvent
}

func (b *fakeBus) Publish(_ context.Context, e events.Event) error {
	b.published = append(b.published, fakeEvent{e})
	return nil
}

func (b *fakeBus) Subscribe(_ context.Context, _ []events.EventType, _ func(events.Event) error) error {
	return nil
}

func (b *fakeBus) Close() error { return nil }

// --- helpers ----------------------------------------------------------------

func newTestOrchestrator(db *sql.DB, bus events.Bus) *Orchestrator {
	return &Orchestrator{
		db:            db,
		bus:           bus,
		projectID:     "proj-1",
		companyID:     "co-1",
		failureCounts: make(map[string]int),
	}
}

// --- CreateTask: normal (non-high-risk) path --------------------------------

func TestCreateTask_Normal(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	bus := &fakeBus{}
	o := newTestOrchestrator(db, bus)

	mock.ExpectExec(`INSERT INTO tasks`).
		WillReturnResult(sqlmock.NewResult(1, 1))

	task := Task{
		AgentRole: "backend-developer",
		SkillName: "api-implementation",
		Priority:  1,
	}
	if err := o.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if len(bus.published) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(bus.published))
	}
	if bus.published[0].event.Type != events.TaskCreated {
		t.Errorf("expected task.created event, got %q", bus.published[0].event.Type)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled sqlmock expectations: %v", err)
	}
}

// --- CreateTask: high-risk path creates governance assessment first ---------

func TestCreateTask_HighRisk_CreatesGovernanceTask(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	bus := &fakeBus{}
	o := newTestOrchestrator(db, bus)

	// Expect original task INSERT (awaiting_approval)
	mock.ExpectExec(`INSERT INTO tasks`).WillReturnResult(sqlmock.NewResult(1, 1))
	// Expect governance assessment task INSERT
	mock.ExpectExec(`INSERT INTO tasks`).WillReturnResult(sqlmock.NewResult(1, 1))

	task := Task{
		AgentRole: "devops",
		SkillName: "deployment", // high-risk
		Priority:  2,
	}
	if err := o.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Only the governance assessment event should be published, not the original task.
	if len(bus.published) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(bus.published))
	}
	var payload Task
	if err := json.Unmarshal(bus.published[0].event.Payload, &payload); err != nil {
		t.Fatalf("unmarshal published payload: %v", err)
	}
	if payload.AgentRole != "governance" {
		t.Errorf("expected governance assessment to be published, got role=%q", payload.AgentRole)
	}
	if payload.SkillName != "change-risk-assessment" {
		t.Errorf("expected change-risk-assessment skill, got %q", payload.SkillName)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled sqlmock expectations: %v", err)
	}
}

// --- CreateTask: governance agent is never high-risk gated -----------------

func TestCreateTask_GovernanceAgentNotGated(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	bus := &fakeBus{}
	o := newTestOrchestrator(db, bus)

	mock.ExpectExec(`INSERT INTO tasks`).WillReturnResult(sqlmock.NewResult(1, 1))

	task := Task{
		AgentRole: "governance",
		SkillName: "deployment", // high-risk skill, but governance agent — should not gate
		Priority:  5,
	}
	if err := o.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// Exactly one INSERT (no governance assessment created) and one event published.
	if len(bus.published) != 1 {
		t.Fatalf("expected 1 event, got %d", len(bus.published))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled sqlmock expectations: %v", err)
	}
}

// --- requeueStaleTasks: tasks under retry cap are re-queued ----------------

func TestRequeueStaleTasks_RequeuesUnderCap(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	bus := &fakeBus{}
	o := newTestOrchestrator(db, bus)

	taskID := uuid.New().String()

	rows := sqlmock.NewRows([]string{"id", "agent_role", "retry_count"}).
		AddRow(taskID, "backend-developer", 0)
	mock.ExpectQuery(`SELECT id, agent_role, retry_count FROM tasks`).
		WillReturnRows(rows)

	// Expect UPDATE to reset to pending
	mock.ExpectExec(`UPDATE tasks`).
		WithArgs(taskID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := o.requeueStaleTasks(context.Background()); err != nil {
		t.Fatalf("requeueStaleTasks: %v", err)
	}

	if len(bus.published) != 1 {
		t.Fatalf("expected 1 task.created event, got %d", len(bus.published))
	}
	if bus.published[0].event.Type != events.TaskCreated {
		t.Errorf("expected task.created, got %q", bus.published[0].event.Type)
	}
	if bus.published[0].event.TaskID != taskID {
		t.Errorf("expected task ID %s, got %s", taskID, bus.published[0].event.TaskID)
	}
}

// --- requeueStaleTasks: tasks at retry cap are permanently failed -----------

func TestRequeueStaleTasks_FailsAtCap(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	bus := &fakeBus{}
	o := newTestOrchestrator(db, bus)

	taskID := uuid.New().String()

	rows := sqlmock.NewRows([]string{"id", "agent_role", "retry_count"}).
		AddRow(taskID, "backend-developer", maxTaskRetries)
	mock.ExpectQuery(`SELECT id, agent_role, retry_count FROM tasks`).
		WillReturnRows(rows)

	// Expect UPDATE to mark failed
	mock.ExpectExec(`UPDATE tasks`).
		WithArgs(taskID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := o.requeueStaleTasks(context.Background()); err != nil {
		t.Fatalf("requeueStaleTasks: %v", err)
	}

	// Nothing should be published when marking permanently failed.
	if len(bus.published) != 0 {
		t.Errorf("expected no events published for permanently failed task, got %d", len(bus.published))
	}
}

// --- requeueStaleTasks: no stale tasks is a no-op --------------------------

func TestRequeueStaleTasks_NoStaleTasks(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	bus := &fakeBus{}
	o := newTestOrchestrator(db, bus)

	rows := sqlmock.NewRows([]string{"id", "agent_role", "retry_count"})
	mock.ExpectQuery(`SELECT id, agent_role, retry_count FROM tasks`).
		WillReturnRows(rows)

	if err := o.requeueStaleTasks(context.Background()); err != nil {
		t.Fatalf("requeueStaleTasks: %v", err)
	}
	if len(bus.published) != 0 {
		t.Errorf("expected no events, got %d", len(bus.published))
	}
}

// --- handleGovernanceAssessmentCompleted: approve promotes to running -------

func TestHandleGovernanceAssessment_Approve(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	bus := &fakeBus{}
	o := newTestOrchestrator(db, bus)

	pendingTaskID := uuid.New().String()

	mock.ExpectExec(`UPDATE tasks SET status = 'running'`).
		WithArgs(pendingTaskID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	payload, _ := json.Marshal(map[string]interface{}{
		"pending_task_id":  pendingTaskID,
		"recommendation":   "approve",
		"risk_score":       2.5,
		"report_markdown":  "Low risk.",
	})
	e := events.Event{
		Type:    events.GovernanceAssessmentCompleted,
		Payload: payload,
	}

	if err := o.handleGovernanceAssessmentCompleted(context.Background(), e); err != nil {
		t.Fatalf("handleGovernanceAssessmentCompleted: %v", err)
	}
	if len(bus.published) != 1 {
		t.Fatalf("expected task.created re-publish, got %d events", len(bus.published))
	}
	if bus.published[0].event.TaskID != pendingTaskID {
		t.Errorf("expected task ID %s, got %s", pendingTaskID, bus.published[0].event.TaskID)
	}
}

// --- handleGovernanceAssessmentCompleted: reject cancels task ---------------

func TestHandleGovernanceAssessment_Reject(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	bus := &fakeBus{}
	o := newTestOrchestrator(db, bus)

	pendingTaskID := uuid.New().String()

	mock.ExpectExec(`UPDATE tasks SET status = 'failed'`).
		WillReturnResult(sqlmock.NewResult(1, 1))

	payload, _ := json.Marshal(map[string]interface{}{
		"pending_task_id": pendingTaskID,
		"recommendation":  "reject",
		"risk_score":      9.1,
		"report_markdown": "Too risky.",
	})
	e := events.Event{
		Type:    events.GovernanceAssessmentCompleted,
		Payload: payload,
	}

	if err := o.handleGovernanceAssessmentCompleted(context.Background(), e); err != nil {
		t.Fatalf("handleGovernanceAssessmentCompleted: %v", err)
	}
	// Rejected task must NOT republish a task.created event.
	if len(bus.published) != 0 {
		t.Errorf("expected no events published on reject, got %d", len(bus.published))
	}
}

// --- handleTaskFailed: drift detection triggers after threshold -------------

func TestHandleTaskFailed_DriftDetectionTriggered(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	bus := &fakeBus{}
	o := newTestOrchestrator(db, bus)

	// Pre-seed failures just below threshold so the next failure triggers drift.
	o.mu.Lock()
	o.failureCounts["backend-developer"] = failureThresholdForDrift - 1
	o.mu.Unlock()

	taskID := uuid.New().String()

	mock.ExpectExec(`UPDATE tasks SET status = 'failed'`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(`SELECT agent_role FROM tasks`).
		WillReturnRows(sqlmock.NewRows([]string{"agent_role"}).AddRow("backend-developer"))
	// Expect drift detection task INSERT
	mock.ExpectExec(`INSERT INTO tasks`).WillReturnResult(sqlmock.NewResult(1, 1))

	e := events.Event{Type: events.TaskFailed, TaskID: taskID}
	if err := o.handleTaskFailed(context.Background(), e); err != nil {
		t.Fatalf("handleTaskFailed: %v", err)
	}

	// Should have published: escalation.created + policy-drift-detection task.created
	found := false
	for _, pub := range bus.published {
		if pub.event.Type == events.TaskCreated {
			var t Task
			if json.Unmarshal(pub.event.Payload, &t) == nil && t.SkillName == "policy-drift-detection" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected policy-drift-detection task to be published after threshold failures")
	}

	// Counter should be reset after triggering drift.
	o.mu.Lock()
	count := o.failureCounts["backend-developer"]
	o.mu.Unlock()
	if count != 0 {
		t.Errorf("expected failure count reset to 0, got %d", count)
	}
}

// --- AssignTask: sets started_at --------------------------------------------

func TestAssignTask_SetsStartedAt(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	o := newTestOrchestrator(db, &fakeBus{})

	taskID := uuid.New().String()
	agentID := uuid.New().String()

	mock.ExpectExec(`UPDATE tasks SET agent_id`).
		WithArgs(agentID, taskID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := o.AssignTask(context.Background(), taskID, agentID); err != nil {
		t.Fatalf("AssignTask: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("started_at not set: %v", err)
	}
}

// --- watchdogInterval / taskTimeoutDuration sanity --------------------------

func TestWatchdogConstants(t *testing.T) {
	if taskTimeoutDuration < time.Minute {
		t.Error("taskTimeoutDuration is suspiciously short")
	}
	if maxTaskRetries < 1 {
		t.Error("maxTaskRetries must be at least 1")
	}
}
