/**
 * Forge ADP — Tasks Tree View Provider
 *
 * Renders a tree of agent tasks in the Forge ADP activity bar panel.
 * Tasks are grouped by status. Top-level nodes are status groups; each
 * child is an individual task.
 */

import * as vscode from "vscode";
import { ForgeTask, OrchestratorClient, TaskStatus } from "./orchestratorClient";

// ---------------------------------------------------------------------------
// Status display helpers
// ---------------------------------------------------------------------------

const STATUS_LABELS: Record<TaskStatus, string> = {
  pending: "Pending",
  running: "Running",
  blocked: "Blocked",
  completed: "Completed",
  failed: "Failed",
  awaiting_approval: "Awaiting Approval",
};

const STATUS_ICONS: Record<TaskStatus, vscode.ThemeIcon> = {
  pending: new vscode.ThemeIcon("clock"),
  running: new vscode.ThemeIcon("sync~spin"),
  blocked: new vscode.ThemeIcon("warning"),
  completed: new vscode.ThemeIcon("check"),
  failed: new vscode.ThemeIcon("error"),
  awaiting_approval: new vscode.ThemeIcon("bell"),
};

// Status sort order (most important first)
const STATUS_ORDER: TaskStatus[] = [
  "awaiting_approval",
  "blocked",
  "running",
  "pending",
  "failed",
  "completed",
];

// ---------------------------------------------------------------------------
// Tree items
// ---------------------------------------------------------------------------

export class StatusGroupItem extends vscode.TreeItem {
  constructor(
    public readonly status: TaskStatus,
    public readonly tasks: ForgeTask[]
  ) {
    super(
      `${STATUS_LABELS[status]} (${tasks.length})`,
      tasks.length > 0
        ? vscode.TreeItemCollapsibleState.Expanded
        : vscode.TreeItemCollapsibleState.Collapsed
    );
    this.iconPath = STATUS_ICONS[status];
    this.contextValue = "statusGroup";
  }
}

export class TaskItem extends vscode.TreeItem {
  constructor(public readonly task: ForgeTask) {
    super(task.title, vscode.TreeItemCollapsibleState.None);
    this.description = `[${task.agent_role}]${task.ticket_id ? ` ${task.ticket_id}` : ""}`;
    this.tooltip = new vscode.MarkdownString(
      `**${task.title}**\n\n` +
        `- **Role:** ${task.agent_role}\n` +
        `- **Status:** ${task.status}\n` +
        (task.ticket_id ? `- **Ticket:** ${task.ticket_id}\n` : "") +
        (task.output ? `\n---\n${task.output}` : "")
    );
    this.iconPath = STATUS_ICONS[task.status];
    this.contextValue = task.status; // used by menu `when` clauses
    this.command = {
      command: "forge.showTaskDetail",
      title: "Show Task Detail",
      arguments: [task],
    };
  }
}

type TaskTreeItem = StatusGroupItem | TaskItem;

// ---------------------------------------------------------------------------
// Provider
// ---------------------------------------------------------------------------

export class TasksProvider
  implements vscode.TreeDataProvider<TaskTreeItem>
{
  private _onDidChangeTreeData = new vscode.EventEmitter<
    TaskTreeItem | undefined | null | void
  >();
  readonly onDidChangeTreeData = this._onDidChangeTreeData.event;

  private tasks: ForgeTask[] = [];
  private loading = false;
  private lastError: string | null = null;

  constructor(private readonly client: OrchestratorClient) {}

  refresh(): void {
    this._onDidChangeTreeData.fire();
  }

  async load(): Promise<void> {
    this.loading = true;
    this.lastError = null;
    this.refresh();
    try {
      this.tasks = await this.client.listTasks();
    } catch (err) {
      this.lastError =
        err instanceof Error ? err.message : "Unknown error fetching tasks";
      this.tasks = [];
    } finally {
      this.loading = false;
      this.refresh();
    }
  }

  getTreeItem(element: TaskTreeItem): vscode.TreeItem {
    return element;
  }

  getChildren(element?: TaskTreeItem): vscode.ProviderResult<TaskTreeItem[]> {
    if (element instanceof StatusGroupItem) {
      return element.tasks.map((t) => new TaskItem(t));
    }

    // Root — show status groups
    if (this.loading) {
      const item = new vscode.TreeItem("Loading…");
      item.iconPath = new vscode.ThemeIcon("sync~spin");
      return [item as TaskTreeItem];
    }

    if (this.lastError) {
      const item = new vscode.TreeItem(`Error: ${this.lastError}`);
      item.iconPath = new vscode.ThemeIcon("error");
      item.tooltip = "Check that make run-local is running and try refreshing.";
      return [item as TaskTreeItem];
    }

    if (this.tasks.length === 0) {
      const item = new vscode.TreeItem("No tasks found");
      item.iconPath = new vscode.ThemeIcon("info");
      item.description = "Submit a task to get started";
      return [item as TaskTreeItem];
    }

    const grouped = new Map<TaskStatus, ForgeTask[]>();
    for (const status of STATUS_ORDER) grouped.set(status, []);
    for (const task of this.tasks) {
      const bucket = grouped.get(task.status) ?? [];
      bucket.push(task);
      grouped.set(task.status, bucket);
    }

    const groups: StatusGroupItem[] = [];
    for (const status of STATUS_ORDER) {
      const bucket = grouped.get(status) ?? [];
      if (bucket.length > 0) {
        groups.push(new StatusGroupItem(status, bucket));
      }
    }
    return groups;
  }
}
