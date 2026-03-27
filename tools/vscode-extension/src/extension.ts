/**
 * Forge ADP — VS Code Extension
 *
 * Activation point. Registers all commands, the task tree view, and the
 * status bar item that shows a count of tasks needing attention.
 */

import * as vscode from "vscode";
import { OrchestratorClient } from "./orchestratorClient";
import { ForgeTask } from "./orchestratorClient";
import { TasksProvider, TaskItem } from "./tasksProvider";

let pollTimer: ReturnType<typeof setInterval> | undefined;

export function activate(context: vscode.ExtensionContext): void {
  const client = new OrchestratorClient();
  const tasksProvider = new TasksProvider(client);

  // ---- Tree view -----------------------------------------------------------
  const treeView = vscode.window.createTreeView("forge.tasksView", {
    treeDataProvider: tasksProvider,
    showCollapseAll: true,
  });
  context.subscriptions.push(treeView);

  // ---- Status bar ----------------------------------------------------------
  const statusBarItem = vscode.window.createStatusBarItem(
    vscode.StatusBarAlignment.Left,
    10
  );
  statusBarItem.command = "forge.listTasks";
  statusBarItem.tooltip = "Forge ADP — Click to refresh task list";
  context.subscriptions.push(statusBarItem);

  function updateStatusBar(tasks: ForgeTask[]): void {
    const needAttention = tasks.filter(
      (t) => t.status === "awaiting_approval" || t.status === "blocked"
    ).length;
    const running = tasks.filter((t) => t.status === "running").length;

    if (needAttention > 0) {
      statusBarItem.text = `$(bell) Forge: ${needAttention} need${needAttention === 1 ? "s" : ""} attention`;
      statusBarItem.backgroundColor = new vscode.ThemeColor(
        "statusBarItem.warningBackground"
      );
    } else if (running > 0) {
      statusBarItem.text = `$(sync~spin) Forge: ${running} running`;
      statusBarItem.backgroundColor = undefined;
    } else {
      statusBarItem.text = `$(robot) Forge`;
      statusBarItem.backgroundColor = undefined;
    }
    statusBarItem.show();
  }

  // ---- Auto-refresh --------------------------------------------------------
  async function refreshAll(): Promise<void> {
    await tasksProvider.load();
    // After load, pull the tasks back out to update the status bar
    try {
      const tasks = await client.listTasks();
      updateStatusBar(tasks);
    } catch {
      // ignore status bar update errors
    }
  }

  function startPolling(): void {
    if (pollTimer) clearInterval(pollTimer);
    const intervalMs =
      (vscode.workspace
        .getConfiguration("forge")
        .get<number>("pollIntervalSeconds", 15)) * 1000;
    pollTimer = setInterval(refreshAll, intervalMs);
  }

  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration("forge.pollIntervalSeconds")) {
        startPolling();
      }
    })
  );

  startPolling();
  void refreshAll();

  // ---- Agent role quick-pick -----------------------------------------------
  const AGENT_ROLES = [
    "backend-developer",
    "frontend-developer",
    "dba",
    "devops",
    "sre",
    "secops",
    "qa",
    "pm",
  ];

  // ---- Commands ------------------------------------------------------------

  // forge.submitTask
  context.subscriptions.push(
    vscode.commands.registerCommand("forge.submitTask", async () => {
      const role = await vscode.window.showQuickPick(AGENT_ROLES, {
        placeHolder: "Select agent role",
        title: "Submit Forge Task (1/4) — Agent Role",
      });
      if (!role) return;

      const title = await vscode.window.showInputBox({
        prompt: "Task title",
        placeHolder: "Implement user authentication endpoint",
        title: "Submit Forge Task (2/4) — Title",
        validateInput: (v) => (v.trim() ? null : "Title is required"),
      });
      if (!title) return;

      const description = await vscode.window.showInputBox({
        prompt: "Full task description (context, acceptance criteria, links)",
        placeHolder:
          "Add JWT-based auth to POST /api/v1/auth/login. Accept email+password, return access and refresh tokens…",
        title: "Submit Forge Task (3/4) — Description",
        validateInput: (v) => (v.trim() ? null : "Description is required"),
      });
      if (!description) return;

      const ticketId = await vscode.window.showInputBox({
        prompt: "Linked ticket ID (optional — e.g. AUTH-42)",
        title: "Submit Forge Task (4/4) — Ticket ID",
      });

      await vscode.window.withProgress(
        {
          location: vscode.ProgressLocation.Notification,
          title: `Submitting task to ${role} agent…`,
          cancellable: false,
        },
        async () => {
          try {
            const task = await client.submitTask({
              agent_role: role,
              title: title.trim(),
              description: description.trim(),
              ticket_id: ticketId?.trim() || undefined,
            });
            vscode.window
              .showInformationMessage(
                `Task submitted: ${task.id} — ${task.title}`,
                "Copy ID"
              )
              .then((choice) => {
                if (choice === "Copy ID") {
                  void vscode.env.clipboard.writeText(task.id);
                }
              });
            await refreshAll();
          } catch (err) {
            const msg = err instanceof Error ? err.message : String(err);
            vscode.window.showErrorMessage(`Failed to submit task: ${msg}`);
          }
        }
      );
    })
  );

  // forge.getTask
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "forge.getTask",
      async (taskOrId?: ForgeTask | string) => {
        let taskId: string | undefined;
        if (typeof taskOrId === "string") {
          taskId = taskOrId;
        } else if (taskOrId && typeof taskOrId === "object") {
          taskId = taskOrId.id;
        } else {
          taskId = await vscode.window.showInputBox({
            prompt: "Enter task ID",
            title: "Get Task Status",
          });
        }
        if (!taskId) return;

        try {
          const task = await client.getTask(taskId.trim());
          const panel = vscode.window.createWebviewPanel(
            "forgeTask",
            `Forge Task: ${task.id}`,
            vscode.ViewColumn.Beside,
            { enableScripts: false }
          );
          panel.webview.html = renderTaskHtml(task);
        } catch (err) {
          const msg = err instanceof Error ? err.message : String(err);
          vscode.window.showErrorMessage(`Failed to get task: ${msg}`);
        }
      }
    )
  );

  // forge.showTaskDetail (triggered by clicking a task item in the tree)
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "forge.showTaskDetail",
      (task: ForgeTask) => {
        void vscode.commands.executeCommand("forge.getTask", task);
      }
    )
  );

  // forge.listTasks
  context.subscriptions.push(
    vscode.commands.registerCommand("forge.listTasks", async () => {
      await refreshAll();
      await vscode.commands.executeCommand("forge.tasksView.focus");
    })
  );

  // forge.approveTask
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "forge.approveTask",
      async (itemOrId?: TaskItem | string) => {
        const taskId = resolveTaskId(itemOrId);
        const id =
          taskId ??
          (await vscode.window.showInputBox({
            prompt: "Enter task ID to approve",
            title: "Approve Task",
          }));
        if (!id) return;

        const comment = await vscode.window.showInputBox({
          prompt: "Optional approval comment",
          title: "Approve Task — Comment",
        });

        try {
          await client.approveTask(id.trim(), comment?.trim());
          vscode.window.showInformationMessage(`Task ${id} approved.`);
          await refreshAll();
        } catch (err) {
          const msg = err instanceof Error ? err.message : String(err);
          vscode.window.showErrorMessage(`Failed to approve task: ${msg}`);
        }
      }
    )
  );

  // forge.rejectTask
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "forge.rejectTask",
      async (itemOrId?: TaskItem | string) => {
        const taskId = resolveTaskId(itemOrId);
        const id =
          taskId ??
          (await vscode.window.showInputBox({
            prompt: "Enter task ID to reject",
            title: "Reject Task",
          }));
        if (!id) return;

        const reason = await vscode.window.showInputBox({
          prompt: "Reason for rejection (required — fed back to the agent)",
          title: "Reject Task — Reason",
          validateInput: (v) => (v.trim() ? null : "Reason is required"),
        });
        if (!reason) return;

        try {
          await client.rejectTask(id.trim(), reason.trim());
          vscode.window.showInformationMessage(`Task ${id} rejected.`);
          await refreshAll();
        } catch (err) {
          const msg = err instanceof Error ? err.message : String(err);
          vscode.window.showErrorMessage(`Failed to reject task: ${msg}`);
        }
      }
    )
  );

  // forge.refreshTasks
  context.subscriptions.push(
    vscode.commands.registerCommand("forge.refreshTasks", async () => {
      await refreshAll();
    })
  );

  // forge.openTaskInBrowser
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "forge.openTaskInBrowser",
      async (itemOrId?: TaskItem | string) => {
        const taskId = resolveTaskId(itemOrId);
        const id =
          taskId ??
          (await vscode.window.showInputBox({ prompt: "Enter task ID" }));
        if (!id) return;
        const base = vscode.workspace
          .getConfiguration("forge")
          .get<string>("orchestratorUrl", "http://localhost:19080");
        await vscode.env.openExternal(
          vscode.Uri.parse(`${base}/v1/tasks/${id}`)
        );
      }
    )
  );

  // forge.checkHealth
  context.subscriptions.push(
    vscode.commands.registerCommand("forge.checkHealth", async () => {
      try {
        const result = await client.health();
        vscode.window.showInformationMessage(
          `Forge Orchestrator: ${result.status ?? "OK"}`
        );
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        vscode.window.showErrorMessage(
          `Forge Orchestrator unreachable: ${msg}`
        );
      }
    })
  );
}

export function deactivate(): void {
  if (pollTimer) clearInterval(pollTimer);
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function resolveTaskId(item?: TaskItem | string): string | undefined {
  if (typeof item === "string") return item;
  if (item instanceof TaskItem) return item.task.id;
  return undefined;
}

function renderTaskHtml(task: ForgeTask): string {
  const esc = (s: string) =>
    s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

  const statusColor: Record<string, string> = {
    pending: "#888",
    running: "#4fc3f7",
    blocked: "#ffa726",
    completed: "#66bb6a",
    failed: "#ef5350",
    awaiting_approval: "#ffd54f",
  };

  const color = statusColor[task.status] ?? "#aaa";

  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<title>Forge Task ${esc(task.id)}</title>
<style>
  body { font-family: var(--vscode-font-family); color: var(--vscode-foreground); background: var(--vscode-editor-background); padding: 1.5rem; line-height: 1.6; }
  h1 { font-size: 1.2rem; margin-bottom: 0.25rem; }
  .badge { display: inline-block; padding: 2px 10px; border-radius: 999px; font-size: 0.75rem; font-weight: 600; background: ${color}22; color: ${color}; border: 1px solid ${color}; }
  table { border-collapse: collapse; margin: 1rem 0; }
  td { padding: 4px 12px 4px 0; vertical-align: top; }
  td:first-child { color: var(--vscode-descriptionForeground); white-space: nowrap; }
  pre { background: var(--vscode-textCodeBlock-background); padding: 1rem; border-radius: 4px; overflow-x: auto; white-space: pre-wrap; word-break: break-word; font-size: 0.85rem; }
  hr { border: none; border-top: 1px solid var(--vscode-panel-border); margin: 1.5rem 0; }
</style>
</head>
<body>
<h1>${esc(task.title)}</h1>
<span class="badge">${esc(task.status.replace("_", " ").toUpperCase())}</span>
<hr />
<table>
  <tr><td>ID</td><td><code>${esc(task.id)}</code></td></tr>
  <tr><td>Agent Role</td><td>${esc(task.agent_role)}</td></tr>
  ${task.ticket_id ? `<tr><td>Ticket</td><td>${esc(task.ticket_id)}</td></tr>` : ""}
  <tr><td>Created</td><td>${esc(new Date(task.created_at).toLocaleString())}</td></tr>
  <tr><td>Updated</td><td>${esc(new Date(task.updated_at).toLocaleString())}</td></tr>
</table>
<hr />
<h2 style="font-size:1rem">Description</h2>
<pre>${esc(task.description)}</pre>
${task.output ? `<hr /><h2 style="font-size:1rem">Agent Output</h2><pre>${esc(task.output)}</pre>` : ""}
${task.error ? `<hr /><h2 style="font-size:1rem; color:#ef5350">Error</h2><pre style="color:#ef5350">${esc(task.error)}</pre>` : ""}
</body>
</html>`;
}
