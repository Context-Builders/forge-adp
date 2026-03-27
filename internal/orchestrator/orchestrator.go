package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

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
	JiraTicketID string          `json:"jira_ticket_id"`
	AgentRole    string          `json:"agent_role"`
	SkillName    string          `json:"skill_name"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description,omitempty"`
	Status       string          `json:"status"`
	Priority     int             `json:"priority"`
	Input        json.RawMessage `json:"input"`
	Dependencies []string        `json:"dependencies,omitempty"`
	// Repo is the primary repository this task operates on.
	Repo string `json:"repo,omitempty"`
	// PlatformRepos lists all sibling repos when the task is part of a
	// multi-repo platform.  Agents use this to load cross-repo context
	// via PlanReader.load_platform_plans().
	PlatformRepos []string `json:"platform_repos,omitempty"`
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
		task.Status = "pending_governance"
		platformReposJSON, _ := json.Marshal(task.PlatformRepos)
		_, err := o.db.ExecContext(ctx,
			`INSERT INTO tasks (id, jira_ticket_id, agent_role, skill_name, status, priority, input_payload, repo, platform_repos)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			task.ID, task.JiraTicketID, task.AgentRole, task.SkillName,
			task.Status, task.Priority, task.Input, task.Repo, platformReposJSON)
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
			Status:    "created",
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
		log.Printf("[orchestrator] high-risk task %s (%s) queued for governance assessment %s",
			task.ID, task.SkillName, assessTask.ID)
		return o.bus.Publish(ctx, events.Event{
			ID:      uuid.New().String(),
			Type:    events.TaskCreated,
			TaskID:  assessTask.ID,
			Payload: payload,
		})
	}

	// Normal (non-high-risk) path
	task.Status = "created"
	platformReposJSON, _ := json.Marshal(task.PlatformRepos)
	_, err := o.db.ExecContext(ctx,
		`INSERT INTO tasks (id, jira_ticket_id, agent_role, skill_name, status, priority, input_payload, repo, platform_repos)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		task.ID, task.JiraTicketID, task.AgentRole, task.SkillName,
		task.Status, task.Priority, task.Input, task.Repo, platformReposJSON)
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
		`UPDATE tasks SET agent_id = $1, status = 'queued' WHERE id = $2`,
		agentID, taskID)
	return err
}

func (o *Orchestrator) GetUnblockedTasks(ctx context.Context, agentRole string) ([]Task, error) {
	rows, err := o.db.QueryContext(ctx, `SELECT t.id, t.jira_ticket_id, t.skill_name, t.status, t.priority, t.input_payload FROM tasks t LEFT JOIN task_dependencies td ON t.id = td.task_id LEFT JOIN tasks dep ON td.depends_on_task_id = dep.id JOIN agents a ON t.agent_id = a.id WHERE a.role = $1 AND t.status = 'queued' GROUP BY t.id HAVING COUNT(CASE WHEN dep.status != 'completed' THEN 1 END) = 0 ORDER BY t.priority DESC LIMIT 10`, agentRole)
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
	_, err := o.db.ExecContext(ctx, `UPDATE tasks SET status = 'completed', completed_at = NOW() WHERE id = $1`, e.TaskID)
	if err != nil {
		return err
	}

	// Reset the failure counter for this role on success.
	var agentRole string
	o.db.QueryRowContext(ctx, `SELECT agent_role FROM tasks WHERE id = $1`, e.TaskID).Scan(&agentRole)
	if agentRole != "" {
		o.mu.Lock()
		o.failureCounts[agentRole] = 0
		o.mu.Unlock()
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
			o.db.ExecContext(ctx, `UPDATE tasks SET status = 'queued' WHERE id = $1`, taskID)
			o.bus.Publish(ctx, events.Event{Type: events.TaskCreated, TaskID: taskID})
		}
	}
	return nil
}

func (o *Orchestrator) handleTaskFailed(ctx context.Context, e events.Event) error {
	_, err := o.db.ExecContext(ctx, `UPDATE tasks SET status = 'failed' WHERE id = $1`, e.TaskID)
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

			log.Printf("[orchestrator] %d consecutive failures for role %s — triggering policy-drift-detection",
				count, agentRole)
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
		Status:    "created",
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
		log.Printf("[orchestrator] governance rejected task %s (risk=%.1f) — cancelling",
			payload.PendingTaskID, payload.RiskScore)
		_, err := o.db.ExecContext(ctx,
			`UPDATE tasks SET status = 'cancelled',
				 output_payload = $1
			 WHERE id = $2 AND status = 'pending_governance'`,
			payload.ReportMarkdown, payload.PendingTaskID)
		return err

	case "approve", "conditional":
		log.Printf("[orchestrator] governance %sd task %s (risk=%.1f) — promoting to queued",
			payload.Recommendation, payload.PendingTaskID, payload.RiskScore)
		_, err := o.db.ExecContext(ctx,
			`UPDATE tasks SET status = 'queued'
			 WHERE id = $1 AND status = 'pending_governance'`,
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
	_, err := o.db.ExecContext(ctx, `UPDATE tasks SET status = 'blocked' WHERE id = $1`, e.TaskID)
	return err
}

func (o *Orchestrator) HandleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		agentRole := r.URL.Query().Get("agent_role")
		tasks, err := o.GetUnblockedTasks(r.Context(), agentRole)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
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
		w.WriteHeader(http.StatusCreated)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
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
