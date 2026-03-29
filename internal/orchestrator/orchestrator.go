package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

// highRiskSkills is the set of skill names that require a governance
// change-risk-assessment before the task is allowed to proceed.
var highRiskSkills = map[string]bool{
	"deployment":          true,
	"schema-migration":    true,
	"migration-execution": true,
	"infrastructure":      true,
	"vulnerability-scan":  true,
}

// failureThresholdForDrift is the number of consecutive task failures for
// the same agent role within a project that triggers automatic
// policy-drift-detection.
const failureThresholdForDrift = 5

type Config struct {
	DatabaseURL string
	EventBus    events.Bus
	// ProjectID and CompanyID are used when creating governance tasks.
	ProjectID string
	CompanyID string
}

type Orchestrator struct {
	db        *sql.DB
	bus       events.Bus
	projectID string
	companyID string

	// failureCounts tracks consecutive task failures per agent role for
	// automatic drift-detection triggering.
	mu            sync.Mutex
	failureCounts map[string]int // key: agentRole
}

func New(cfg Config) (*Orchestrator, error) {
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Orchestrator{
		db:            db,
		bus:           cfg.EventBus,
		projectID:     cfg.ProjectID,
		companyID:     cfg.CompanyID,
		failureCounts: make(map[string]int),
	}, nil
}

type Task struct {
	ID           string          `json:"id"`
	JiraTicketID string          `json:"jira_ticket_id,omitempty"`
	AgentRole    string          `json:"agent_role"`
	SkillName    string          `json:"skill_name,omitempty"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description,omitempty"`
	Status       string          `json:"status"`
	Priority     int             `json:"priority"`
	Input        json.RawMessage `json:"input,omitempty"`
	Output       string          `json:"output,omitempty"`
	Error        string          `json:"error,omitempty"`
	Dependencies []string        `json:"dependencies,omitempty"`
	// Repo is the primary repository this task operates on.
	Repo string `json:"repo,omitempty"`
	// PlatformRepos lists all sibling repos when the task is part of a
	// multi-repo platform.  Agents use this to load cross-repo context
	// via PlanReader.load_platform_plans().
	PlatformRepos []string `json:"platform_repos,omitempty"`
	CreatedAt     string   `json:"created_at,omitempty"`
	UpdatedAt     string   `json:"updated_at,omitempty"`
}

func (o *Orchestrator) CreateTask(ctx context.Context, task Task) error {
	task.ID = uuid.New().String()

	// ----------------------------------------------------------------
	// Pre-flight governance check
	// High-risk tasks are created in "pending_governance" state.  A
	// change-risk-assessment governance task is queued first; when it
	// completes the handler below promotes or cancels the original task.
	// ----------------------------------------------------------------
	if highRiskSkills[task.SkillName] && task.AgentRole != "governance" {
		task.Status = "awaiting_approval"
		platformReposJSON, _ := json.Marshal(task.PlatformRepos)
		_, err := o.db.ExecContext(ctx,
			`INSERT INTO tasks (id, jira_ticket_id, agent_role, skill_name, title, description, status, priority, input_payload, repo, platform_repos)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			task.ID, task.JiraTicketID, task.AgentRole, task.SkillName,
			task.Title, task.Description, task.Status, task.Priority, task.Input, task.Repo, platformReposJSON)
		if err != nil {
			return fmt.Errorf("insert task (pending_governance): %w", err)
		}

		assessInput, _ := json.Marshal(map[string]interface{}{
			"task_payload":    task,
			"agent_role":      task.AgentRole,
			"jira_ticket":     task.JiraTicketID,
			"project_id":      o.projectID,
			"pending_task_id": task.ID,
		})
		assessTask := Task{
			ID:        uuid.New().String(),
			AgentRole: "governance",
			SkillName: "change-risk-assessment",
			Status:    "pending",
			Priority:  task.Priority + 1, // assess before the original executes
			Input:     assessInput,
		}
		_, assessErr := o.db.ExecContext(ctx,
			`INSERT INTO tasks (id, agent_role, skill_name, status, priority, input_payload)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			assessTask.ID, assessTask.AgentRole, assessTask.SkillName,
			assessTask.Status, assessTask.Priority, assessTask.Input)
		if assessErr != nil {
			return fmt.Errorf("insert governance assessment task: %w", assessErr)
		}

		payload, _ := json.Marshal(assessTask)
		slog.Info("high-risk task queued for governance assessment",
			slog.String("task_id", task.ID),
			slog.String("skill_name", task.SkillName),
			slog.String("assessment_task_id", assessTask.ID))
		return o.bus.Publish(ctx, events.Event{
			ID:      uuid.New().String(),
			Type:    events.TaskCreated,
			TaskID:  assessTask.ID,
			Payload: payload,
		})
	}

	// Normal (non-high-risk) path
	task.Status = "pending"
	platformReposJSON, _ := json.Marshal(task.PlatformRepos)
	_, err := o.db.ExecContext(ctx,
		`INSERT INTO tasks (id, jira_ticket_id, agent_role, skill_name, title, description, status, priority, input_payload, repo, platform_repos)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		task.ID, task.JiraTicketID, task.AgentRole, task.SkillName,
		task.Title, task.Description, task.Status, task.Priority, task.Input, task.Repo, platformReposJSON)
	if err != nil {
		return fmt.Errorf("insert task: %w", err)
	}
	payload, _ := json.Marshal(task)
	return o.bus.Publish(ctx, events.Event{
		ID:      uuid.New().String(),
		Type:    events.TaskCreated,
		TaskID:  task.ID,
		Payload: payload,
	})
}

func (o *Orchestrator) AssignTask(ctx context.Context, taskID, agentID string) error {
	_, err := o.db.ExecContext(ctx,
		`UPDATE tasks SET agent_id = $1, status = 'running', started_at = NOW(), updated_at = NOW() WHERE id = $2`,
		agentID, taskID)
	return err
}

func (o *Orchestrator) GetUnblockedTasks(ctx context.Context, agentRole string) ([]Task, error) {
	rows, err := o.db.QueryContext(ctx, `SELECT t.id, t.jira_ticket_id, t.skill_name, t.status, t.priority, t.input_payload FROM tasks t LEFT JOIN task_dependencies td ON t.id = td.task_id LEFT JOIN tasks dep ON td.depends_on_task_id = dep.id JOIN agents a ON t.agent_id = a.id WHERE a.role = $1 AND t.status = 'running' GROUP BY t.id HAVING COUNT(CASE WHEN dep.status != 'completed' THEN 1 END) = 0 ORDER BY t.priority DESC LIMIT 10`, agentRole)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.JiraTicketID, &t.SkillName, &t.Status, &t.Priority, &t.Input); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func (o *Orchestrator) ProcessEvents(ctx context.Context) error {
	return o.bus.Subscribe(ctx, []events.EventType{
		events.TaskCompleted,
		events.TaskFailed,
		events.TaskBlocked,
		events.GovernanceAssessmentCompleted,
	}, func(e events.Event) error {
		switch e.Type {
		case events.TaskCompleted:
			return o.handleTaskCompleted(ctx, e)
		case events.TaskFailed:
			return o.handleTaskFailed(ctx, e)
		case events.TaskBlocked:
			return o.handleTaskBlocked(ctx, e)
		case events.GovernanceAssessmentCompleted:
			return o.handleGovernanceAssessmentCompleted(ctx, e)
		}
		return nil
	})
}

func (o *Orchestrator) handleTaskCompleted(ctx context.Context, e events.Event) error {
	// Extract the "output" field from the event payload published by the Python agent:
	// {"task_id": "...", "output": {...}}
	var eventPayload struct {
		Output json.RawMessage `json:"output"`
	}
	var outputJSON json.RawMessage
	if len(e.Payload) > 0 {
		if err := json.Unmarshal(e.Payload, &eventPayload); err == nil && len(eventPayload.Output) > 0 {
			outputJSON = eventPayload.Output
		}
	}

	if len(outputJSON) > 0 {
		_, err := o.db.ExecContext(ctx,
			`UPDATE tasks SET status = 'completed', output_payload = $2, completed_at = NOW(), updated_at = NOW() WHERE id = $1`,
			e.TaskID, outputJSON)
		if err != nil {
			return err
		}
	} else {
		_, err := o.db.ExecContext(ctx,
			`UPDATE tasks SET status = 'completed', completed_at = NOW(), updated_at = NOW() WHERE id = $1`,
			e.TaskID)
		if err != nil {
			return err
		}
	}

	// Reset the failure counter for this role on success.
	var agentRole, skillName string
	o.db.QueryRowContext(ctx, `SELECT agent_role, COALESCE(skill_name, '') FROM tasks WHERE id = $1`, e.TaskID).Scan(&agentRole, &skillName)
	if agentRole != "" {
		o.mu.Lock()
		o.failureCounts[agentRole] = 0
		o.mu.Unlock()
	}

	// When a PM project-bootstrap completes, auto-enqueue the architect tasks
	// it produced in its output_payload.
	if agentRole == "pm" && skillName == "project-bootstrap" {
		if err := o.enqueueArchitectTasks(ctx, e.TaskID); err != nil {
			slog.Error("failed to enqueue architect tasks from PM bootstrap", slog.String("task_id", e.TaskID), slog.Any("error", err))
		}
	}

	rows, err := o.db.QueryContext(ctx, `SELECT DISTINCT t.id, t.agent_id FROM tasks t JOIN task_dependencies td ON t.id = td.task_id WHERE td.depends_on_task_id = $1 AND t.status = 'blocked'`, e.TaskID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var taskID, agentID string
		if err := rows.Scan(&taskID, &agentID); err != nil {
			continue
		}
		var blockedCount int
		o.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dependencies td JOIN tasks dep ON td.depends_on_task_id = dep.id WHERE td.task_id = $1 AND dep.status != 'completed'`, taskID).Scan(&blockedCount)
		if blockedCount == 0 {
			if err := o.enrichWithDependencyOutputs(ctx, taskID); err != nil {
				slog.Warn("failed to enrich dependency outputs", slog.String("task_id", taskID), slog.Any("error", err))
				// Non-fatal: proceed to unblock even if enrichment fails.
			}
			o.db.ExecContext(ctx, `UPDATE tasks SET status = 'running', updated_at = NOW() WHERE id = $1`, taskID)
			o.bus.Publish(ctx, events.Event{Type: events.TaskCreated, TaskID: taskID})
		}
	}
	return nil
}

// enrichWithDependencyOutputs fetches the output_payload of every completed
// dependency task and merges them into the dependent task's input_payload under
// the key "dependency_outputs": { "<skill_name>": <output_payload> }.
// It is a no-op when a task has no completed dependencies with output.
func (o *Orchestrator) enrichWithDependencyOutputs(ctx context.Context, taskID string) error {
	rows, err := o.db.QueryContext(ctx, `
		SELECT dep.skill_name, dep.output_payload
		FROM task_dependencies td
		JOIN tasks dep ON td.depends_on_task_id = dep.id
		WHERE td.task_id = $1
		  AND dep.status = 'completed'
		  AND dep.output_payload IS NOT NULL
	`, taskID)
	if err != nil {
		return fmt.Errorf("query dependency outputs for %s: %w", taskID, err)
	}
	defer rows.Close()

	depOutputs := make(map[string]json.RawMessage)
	for rows.Next() {
		var skillName string
		var outputPayload json.RawMessage
		if err := rows.Scan(&skillName, &outputPayload); err != nil {
			continue
		}
		if skillName != "" && len(outputPayload) > 0 {
			depOutputs[skillName] = outputPayload
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan dependency outputs for %s: %w", taskID, err)
	}
	if len(depOutputs) == 0 {
		return nil
	}

	var currentInput json.RawMessage
	if err := o.db.QueryRowContext(ctx,
		`SELECT COALESCE(input_payload, '{}') FROM tasks WHERE id = $1`, taskID).
		Scan(&currentInput); err != nil {
		return fmt.Errorf("read input_payload for %s: %w", taskID, err)
	}

	var inputMap map[string]json.RawMessage
	if err := json.Unmarshal(currentInput, &inputMap); err != nil || inputMap == nil {
		inputMap = make(map[string]json.RawMessage)
	}

	depOutputsJSON, err := json.Marshal(depOutputs)
	if err != nil {
		return fmt.Errorf("marshal dependency_outputs for %s: %w", taskID, err)
	}
	inputMap["dependency_outputs"] = depOutputsJSON

	enriched, err := json.Marshal(inputMap)
	if err != nil {
		return fmt.Errorf("marshal enriched input_payload for %s: %w", taskID, err)
	}

	if _, err := o.db.ExecContext(ctx,
		`UPDATE tasks SET input_payload = $2, updated_at = NOW() WHERE id = $1`,
		taskID, enriched); err != nil {
		return fmt.Errorf("update enriched input_payload for %s: %w", taskID, err)
	}

	keys := make([]string, 0, len(depOutputs))
	for k := range depOutputs {
		keys = append(keys, k)
	}
	slog.Debug("enriched task with dependency_outputs", slog.String("task_id", taskID), slog.Any("skills", keys))
	return nil
}

func (o *Orchestrator) handleTaskFailed(ctx context.Context, e events.Event) error {
	_, err := o.db.ExecContext(ctx, `UPDATE tasks SET status = 'failed', updated_at = NOW() WHERE id = $1`, e.TaskID)
	if err != nil {
		return err
	}

	// ----------------------------------------------------------------
	// Event-driven drift detection
	// Track consecutive failures per agent role.  When a role crosses
	// the threshold, automatically queue a policy-drift-detection task.
	// ----------------------------------------------------------------
	var agentRole string
	o.db.QueryRowContext(ctx, `SELECT agent_role FROM tasks WHERE id = $1`, e.TaskID).Scan(&agentRole)
	if agentRole != "" && agentRole != "governance" {
		o.mu.Lock()
		o.failureCounts[agentRole]++
		count := o.failureCounts[agentRole]
		o.mu.Unlock()

		if count >= failureThresholdForDrift {
			o.mu.Lock()
			o.failureCounts[agentRole] = 0 // reset counter
			o.mu.Unlock()

			slog.Warn("consecutive failures threshold reached — triggering policy-drift-detection",
				slog.String("agent_role", agentRole),
				slog.Int("failure_count", count))
			_ = o.enqueueDriftDetection(ctx, agentRole)
		}
	}

	return o.bus.Publish(ctx, events.Event{Type: events.EscalationCreated, TaskID: e.TaskID, Payload: e.Payload})
}

// enqueueDriftDetection creates a governance/policy-drift-detection task.
func (o *Orchestrator) enqueueDriftDetection(ctx context.Context, triggerRole string) error {
	input, _ := json.Marshal(map[string]interface{}{
		"project_id":   o.projectID,
		"triggered_by": "consecutive_failures",
		"trigger_role": triggerRole,
		"period":       "P30D",
	})
	driftTask := Task{
		ID:        uuid.New().String(),
		AgentRole: "governance",
		SkillName: "policy-drift-detection",
		Status:    "pending",
		Priority:  5,
		Input:     input,
	}
	_, err := o.db.ExecContext(ctx,
		`INSERT INTO tasks (id, agent_role, skill_name, status, priority, input_payload)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		driftTask.ID, driftTask.AgentRole, driftTask.SkillName,
		driftTask.Status, driftTask.Priority, driftTask.Input)
	if err != nil {
		return fmt.Errorf("insert drift task: %w", err)
	}
	payload, _ := json.Marshal(driftTask)
	return o.bus.Publish(ctx, events.Event{
		ID:      uuid.New().String(),
		Type:    events.TaskCreated,
		TaskID:  driftTask.ID,
		Payload: payload,
	})
}

// enqueueArchitectTasks reads the output_payload of a completed PM project-bootstrap
// task and creates the architect tasks it specified, wiring task_dependencies so
// that dependent tasks start blocked and are unblocked automatically when their
// dependency completes.
func (o *Orchestrator) enqueueArchitectTasks(ctx context.Context, pmTaskID string) error {
	var outputJSON []byte
	var repo string
	err := o.db.QueryRowContext(ctx,
		`SELECT COALESCE(output_payload::text, '{}'), COALESCE(repo, '') FROM tasks WHERE id = $1`,
		pmTaskID).Scan(&outputJSON, &repo)
	if err != nil {
		return fmt.Errorf("read pm task output: %w", err)
	}

	var output struct {
		ArchitectTasks []struct {
			SkillName string                 `json:"skill_name"`
			Input     map[string]interface{} `json:"input"`
			DependsOn string                 `json:"depends_on"`
		} `json:"architect_tasks"`
	}
	if err := json.Unmarshal(outputJSON, &output); err != nil || len(output.ArchitectTasks) == 0 {
		return nil // no architect tasks in output — nothing to do
	}

	// First pass: insert all tasks, tracking skill_name → task_id.
	skillToID := make(map[string]string, len(output.ArchitectTasks))
	for _, at := range output.ArchitectTasks {
		taskID := uuid.New().String()
		inputBytes, _ := json.Marshal(at.Input)
		status := "pending"
		if at.DependsOn != "" {
			status = "blocked"
		}
		_, err := o.db.ExecContext(ctx,
			`INSERT INTO tasks (id, agent_role, skill_name, status, priority, input_payload, repo)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			taskID, "architect", at.SkillName, status, 5, inputBytes, repo)
		if err != nil {
			return fmt.Errorf("insert architect task %s: %w", at.SkillName, err)
		}
		skillToID[at.SkillName] = taskID
		slog.Info("created architect task from PM bootstrap",
			slog.String("task_id", taskID),
			slog.String("skill_name", at.SkillName),
			slog.String("status", status),
			slog.String("pm_task_id", pmTaskID))
	}

	// Second pass: insert task_dependencies for blocked tasks.
	for _, at := range output.ArchitectTasks {
		if at.DependsOn == "" {
			continue
		}
		depID, ok := skillToID[at.DependsOn]
		if !ok {
			slog.Warn("architect task depends_on unknown skill — skipping dependency",
				slog.String("skill_name", at.SkillName),
				slog.String("depends_on", at.DependsOn))
			continue
		}
		_, err := o.db.ExecContext(ctx,
			`INSERT INTO task_dependencies (task_id, depends_on_task_id) VALUES ($1, $2)`,
			skillToID[at.SkillName], depID)
		if err != nil {
			return fmt.Errorf("insert task_dependency %s->%s: %w", at.SkillName, at.DependsOn, err)
		}
	}

	// Publish TaskCreated events only for the initially unblocked (pending) tasks.
	for _, at := range output.ArchitectTasks {
		if at.DependsOn != "" {
			continue
		}
		taskID := skillToID[at.SkillName]
		payload, _ := json.Marshal(Task{ID: taskID, AgentRole: "architect", SkillName: at.SkillName, Status: "pending"})
		if err := o.bus.Publish(ctx, events.Event{
			ID:      uuid.New().String(),
			Type:    events.TaskCreated,
			TaskID:  taskID,
			Payload: payload,
		}); err != nil {
			slog.Error("failed to publish task.created for architect task", slog.String("task_id", taskID), slog.Any("error", err))
		}
	}
	return nil
}

// handleGovernanceAssessmentCompleted reacts to a finished change-risk-assessment.
// It promotes (queued) or cancels (rejected) the pending_governance task.
func (o *Orchestrator) handleGovernanceAssessmentCompleted(ctx context.Context, e events.Event) error {
	var payload struct {
		PendingTaskID  string  `json:"pending_task_id"`
		Recommendation string  `json:"recommendation"` // "approve", "conditional", "reject"
		RiskScore      float64 `json:"risk_score"`
		ReportMarkdown string  `json:"report_markdown"`
	}
	if err := json.Unmarshal(e.Payload, &payload); err != nil || payload.PendingTaskID == "" {
		return nil // not a pre-flight assessment — nothing to do
	}

	switch payload.Recommendation {
	case "reject":
		slog.Warn("governance rejected task",
			slog.String("task_id", payload.PendingTaskID),
			slog.Float64("risk_score", payload.RiskScore))
		_, err := o.db.ExecContext(ctx,
			`UPDATE tasks SET status = 'failed', error_message = $1, updated_at = NOW()
			 WHERE id = $2 AND status = 'awaiting_approval'`,
			payload.ReportMarkdown, payload.PendingTaskID)
		return err

	case "approve", "conditional":
		slog.Info("governance approved task — promoting to running",
			slog.String("task_id", payload.PendingTaskID),
			slog.String("recommendation", payload.Recommendation),
			slog.Float64("risk_score", payload.RiskScore))
		_, err := o.db.ExecContext(ctx,
			`UPDATE tasks SET status = 'running', updated_at = NOW()
			 WHERE id = $1 AND status = 'awaiting_approval'`,
			payload.PendingTaskID)
		if err != nil {
			return err
		}
		return o.bus.Publish(ctx, events.Event{
			ID:     uuid.New().String(),
			Type:   events.TaskCreated,
			TaskID: payload.PendingTaskID,
		})
	}
	return nil
}

func (o *Orchestrator) handleTaskBlocked(ctx context.Context, e events.Event) error {
	_, err := o.db.ExecContext(ctx, `UPDATE tasks SET status = 'blocked', updated_at = NOW() WHERE id = $1`, e.TaskID)
	return err
}

// maxTaskRetries is the number of times a stale task will be re-queued by the
// watchdog before it is permanently marked failed.
const maxTaskRetries = 3

// taskTimeoutDuration is how long a task may stay in status='running' before
// the watchdog considers it stale and re-queues it.
const taskTimeoutDuration = 15 * time.Minute

// watchdogInterval controls how often the watchdog scans for stale tasks.
const watchdogInterval = 2 * time.Minute

// RunWatchdog periodically detects tasks stuck in status='running' and either
// re-queues them (if under the retry cap) or marks them failed.
// It is intended to be run in its own goroutine.
func (o *Orchestrator) RunWatchdog(ctx context.Context) {
	ticker := time.NewTicker(watchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := o.requeueStaleTasks(ctx); err != nil {
				slog.Error("watchdog error", slog.Any("error", err))
			}
		}
	}
}

func (o *Orchestrator) requeueStaleTasks(ctx context.Context) error {
	rows, err := o.db.QueryContext(ctx, `
		SELECT id, agent_role, retry_count
		FROM tasks
		WHERE status = 'running'
		  AND started_at < NOW() - $1::interval
	`, taskTimeoutDuration.String())
	if err != nil {
		return fmt.Errorf("query stale tasks: %w", err)
	}
	defer rows.Close()

	type staleTask struct {
		id         string
		agentRole  string
		retryCount int
	}
	var stale []staleTask
	for rows.Next() {
		var t staleTask
		if err := rows.Scan(&t.id, &t.agentRole, &t.retryCount); err != nil {
			continue
		}
		stale = append(stale, t)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan stale tasks: %w", err)
	}

	for _, t := range stale {
		if t.retryCount >= maxTaskRetries {
			slog.Warn("task exceeded max retries — marking failed",
				slog.String("task_id", t.id),
				slog.String("agent_role", t.agentRole),
				slog.Int("max_retries", maxTaskRetries))
			_, err := o.db.ExecContext(ctx,
				`UPDATE tasks
				 SET status = 'failed', error_message = 'exceeded max retries after watchdog timeout',
				     updated_at = NOW()
				 WHERE id = $1 AND status = 'running'`,
				t.id)
			if err != nil {
				slog.Error("watchdog failed to mark task as failed", slog.String("task_id", t.id), slog.Any("error", err))
			}
			continue
		}

		slog.Info("requeueing stale task",
			slog.String("task_id", t.id),
			slog.String("agent_role", t.agentRole),
			slog.Int("retry", t.retryCount+1))
		_, err := o.db.ExecContext(ctx,
			`UPDATE tasks
			 SET status = 'pending', agent_id = NULL, started_at = NULL,
			     retry_count = retry_count + 1, updated_at = NOW()
			 WHERE id = $1 AND status = 'running'`,
			t.id)
		if err != nil {
			slog.Error("watchdog failed to requeue task", slog.String("task_id", t.id), slog.Any("error", err))
			continue
		}

		payload, _ := json.Marshal(map[string]string{
			"id":         t.id,
			"agent_role": t.agentRole,
		})
		if err := o.bus.Publish(ctx, events.Event{
			ID:      uuid.New().String(),
			Type:    events.TaskCreated,
			TaskID:  t.id,
			Payload: payload,
		}); err != nil {
			slog.Error("watchdog failed to republish task.created", slog.String("task_id", t.id), slog.Any("error", err))
		}
	}
	return nil
}

func (o *Orchestrator) HandleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		agentRole := r.URL.Query().Get("agent_role")
		status := r.URL.Query().Get("status")
		tasks, err := o.ListAllTasks(r.Context(), agentRole, status)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tasks)
	case http.MethodPost:
		var task Task
		if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := o.CreateTask(r.Context(), task); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		created, err := o.GetTaskByID(r.Context(), task.ID)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(task)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (o *Orchestrator) ListAllTasks(ctx context.Context, agentRole, status string) ([]Task, error) {
	query := `SELECT id,
	                 COALESCE(jira_ticket_id, ''),
	                 COALESCE(agent_role, ''),
	                 COALESCE(skill_name, ''),
	                 COALESCE(title, ''),
	                 COALESCE(description, ''),
	                 status, priority,
	                 COALESCE(input_payload, '{}'),
	                 COALESCE(output_payload::text, ''),
	                 COALESCE(error_message, ''),
	                 created_at, updated_at
	          FROM tasks WHERE 1=1`
	args := []interface{}{}
	i := 1
	if agentRole != "" {
		query += fmt.Sprintf(" AND agent_role = $%d", i)
		args = append(args, agentRole)
		i++
	}
	if status != "" {
		query += fmt.Sprintf(" AND status = $%d", i)
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC LIMIT 200"

	rows, err := o.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := []Task{}
	for rows.Next() {
		var t Task
		var createdAt, updatedAt time.Time
		if err := rows.Scan(
			&t.ID, &t.JiraTicketID, &t.AgentRole, &t.SkillName,
			&t.Title, &t.Description, &t.Status, &t.Priority,
			&t.Input, &t.Output, &t.Error, &createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		t.CreatedAt = createdAt.Format(time.RFC3339)
		t.UpdatedAt = updatedAt.Format(time.RFC3339)
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (o *Orchestrator) GetTaskByID(ctx context.Context, taskID string) (Task, error) {
	var t Task
	var createdAt, updatedAt time.Time
	err := o.db.QueryRowContext(ctx,
		`SELECT id,
		        COALESCE(jira_ticket_id, ''),
		        COALESCE(agent_role, ''),
		        COALESCE(skill_name, ''),
		        COALESCE(title, ''),
		        COALESCE(description, ''),
		        status, priority,
		        COALESCE(input_payload, '{}'),
		        COALESCE(output_payload::text, ''),
		        COALESCE(error_message, ''),
		        created_at, updated_at
		 FROM tasks WHERE id = $1`, taskID).
		Scan(&t.ID, &t.JiraTicketID, &t.AgentRole, &t.SkillName,
			&t.Title, &t.Description, &t.Status, &t.Priority,
			&t.Input, &t.Output, &t.Error, &createdAt, &updatedAt)
	if err != nil {
		return Task{}, err
	}
	t.CreatedAt = createdAt.Format(time.RFC3339)
	t.UpdatedAt = updatedAt.Format(time.RFC3339)
	return t, nil
}

func (o *Orchestrator) ApproveTask(ctx context.Context, taskID string) (Task, error) {
	_, err := o.db.ExecContext(ctx,
		`UPDATE tasks SET status = 'running', updated_at = NOW()
		 WHERE id = $1 AND status = 'awaiting_approval'`,
		taskID)
	if err != nil {
		return Task{}, err
	}
	t, err := o.GetTaskByID(ctx, taskID)
	if err != nil {
		return Task{}, err
	}
	o.bus.Publish(ctx, events.Event{Type: events.TaskCreated, TaskID: taskID})
	return t, nil
}

func (o *Orchestrator) RejectTask(ctx context.Context, taskID, reason string) (Task, error) {
	_, err := o.db.ExecContext(ctx,
		`UPDATE tasks SET status = 'failed', error_message = $2, updated_at = NOW() WHERE id = $1`,
		taskID, reason)
	if err != nil {
		return Task{}, err
	}
	return o.GetTaskByID(ctx, taskID)
}

func (o *Orchestrator) HandleGetTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	task, err := o.GetTaskByID(r.Context(), taskID)
	if err == sql.ErrNoRows {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

func (o *Orchestrator) HandleApproveTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	task, err := o.ApproveTask(r.Context(), taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

func (o *Orchestrator) HandleRejectTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	var body struct {
		Reason string `json:"reason"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	task, err := o.RejectTask(r.Context(), taskID, body.Reason)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

func (o *Orchestrator) HandleAssignment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		TaskID  string `json:"task_id"`
		AgentID string `json:"agent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := o.AssignTask(r.Context(), req.TaskID, req.AgentID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
