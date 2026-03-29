/**
 * Forge ADP — Agent Panel (WebviewView)
 *
 * A rich sidebar panel rendered in VS Code's secondary (right) side bar.
 * Displays live task cards with inline approve/reject actions, a compose
 * form for submitting new tasks, and a Bootstrap tab for seeding and
 * bootstrapping new projects from a product brief file.
 */

import * as vscode from "vscode";
import * as https from "https";
import { execFileSync } from "child_process";
import { OrchestratorClient, ForgeTask, SubmitTaskParams } from "./orchestratorClient";

// ---------------------------------------------------------------------------
// Message types (webview ↔ extension host)
// ---------------------------------------------------------------------------

type MsgFromWebview =
  | { type: "refresh" }
  | { type: "submitTask"; payload: SubmitTaskParams }
  | { type: "approveTask"; payload: { id: string } }
  | { type: "rejectTask"; payload: { id: string; reason: string } }
  | { type: "openTask"; payload: { id: string } }
  | { type: "openFilePicker" }
  | { type: "bootstrapProject"; payload: BootstrapPayload }
  | { type: "pmChatSend"; message: string }
  | { type: "pmChatReset" }
  | { type: "pmChatSubmit"; payload: { repo: string; profile: string; ticket_id?: string } };

type MsgToWebview =
  | { type: "update"; tasks: ForgeTask[]; loading: boolean; error: string | null }
  | { type: "submitStart" }
  | { type: "submitSuccess" }
  | { type: "submitError"; message: string }
  | { type: "fileContent"; content: string; filename: string }
  | { type: "bootstrapStart" }
  | { type: "bootstrapSuccess"; taskId: string }
  | { type: "bootstrapError"; message: string }
  | { type: "pmChatReply"; content: string }
  | { type: "pmChatThinking" }
  | { type: "pmChatBriefReady"; brief: string }
  | { type: "pmChatError"; message: string }
  | { type: "pmChatSubmitSuccess"; taskId: string }
  | { type: "pmChatSubmitError"; message: string };

interface BootstrapPayload {
  repo: string;
  product_brief: string;
  profile: string;
  ticket_id?: string;
  /** If set, run the seeder against this local directory before bootstrapping. */
  local_path?: string;
  project_name?: string;
  company_id?: string;
  project_id?: string;
}

// ---------------------------------------------------------------------------
// Provider
// ---------------------------------------------------------------------------

const PM_INTERVIEW_SYSTEM_PROMPT = `You are a senior product manager conducting a project discovery interview. Your goal is to gather enough information to write a product brief that AI development agents will use to build software.

Interview the user conversationally to learn:
1. What the product does and why it exists
2. Who the target users are and their key needs
3. The 3–5 core features
4. Technical preferences (language, framework, database — only if the user has opinions)
5. Key success metrics or constraints

Ask at most two questions at a time. Follow up on vague answers. Be concise.

Once you have gathered enough information (usually after 4–8 exchanges), write a structured product brief wrapped in <brief>...</brief> tags, then invite the user to submit it. The brief should cover: overview, target users, core value proposition, key features, and success metrics. Be concrete and specific — this goes directly to AI agents building the software.

Start by greeting the user and asking what they are building.`;

export class AgentPanelProvider implements vscode.WebviewViewProvider {
  public static readonly viewId = "forge.agentPanel";

  private _view?: vscode.WebviewView;
  private _chatHistory: { role: "user" | "assistant"; content: string }[] = [];
  private _assembledBrief = "";

  constructor(
    private readonly client: OrchestratorClient,
    /** Called whenever the panel triggers a data-changing action (submit/approve/reject/refresh). */
    private readonly onRefresh: () => Promise<void>
  ) {}

  resolveWebviewView(
    webviewView: vscode.WebviewView,
    _context: vscode.WebviewViewResolveContext,
    _token: vscode.CancellationToken
  ): void {
    this._view = webviewView;
    webviewView.webview.options = { enableScripts: true };
    webviewView.webview.html = this._buildHtml();

    webviewView.webview.onDidReceiveMessage(async (msg: MsgFromWebview) => {
      switch (msg.type) {
        case "refresh":
          await this.onRefresh();
          break;

        case "submitTask":
          await this._handleSubmit(msg.payload);
          break;

        case "approveTask":
          await this._handleApprove(msg.payload.id);
          break;

        case "rejectTask":
          await this._handleReject(msg.payload.id, msg.payload.reason);
          break;

        case "openTask":
          await vscode.commands.executeCommand("forge.getTask", msg.payload.id);
          break;

        case "openFilePicker":
          await this._handleFilePicker();
          break;

        case "bootstrapProject":
          await this._handleBootstrap(msg.payload);
          break;

        case "pmChatSend":
          await this._handlePmChatSend(msg.message);
          break;

        case "pmChatReset":
          this._chatHistory = [];
          this._assembledBrief = "";
          break;

        case "pmChatSubmit":
          await this._handlePmChatSubmit(msg.payload);
          break;
      }
    });

    // Paint a loading skeleton immediately; real data arrives via update()
    this._post({ type: "update", tasks: [], loading: true, error: null });
  }

  /** Push fresh data into the panel. Called from the polling loop. */
  update(tasks: ForgeTask[], loading: boolean, error: string | null): void {
    this._post({ type: "update", tasks, loading, error });
  }

  // ---- action handlers -----------------------------------------------------

  private async _handleSubmit(payload: SubmitTaskParams): Promise<void> {
    this._post({ type: "submitStart" });
    try {
      await this.client.submitTask(payload);
      this._post({ type: "submitSuccess" });
      await this.onRefresh();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      this._post({ type: "submitError", message });
    }
  }

  private async _handleApprove(id: string): Promise<void> {
    try {
      await this.client.approveTask(id);
      await this.onRefresh();
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      vscode.window.showErrorMessage(`Forge: approve failed — ${msg}`);
    }
  }

  private async _handleReject(id: string, reason: string): Promise<void> {
    try {
      await this.client.rejectTask(id, reason);
      await this.onRefresh();
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      vscode.window.showErrorMessage(`Forge: reject failed — ${msg}`);
    }
  }

  private async _handleFilePicker(): Promise<void> {
    const uris = await vscode.window.showOpenDialog({
      canSelectMany: false,
      canSelectFolders: false,
      filters: { "Markdown & Text": ["md", "txt"] },
      title: "Select Product Brief",
    });
    if (uris && uris[0]) {
      const bytes = await vscode.workspace.fs.readFile(uris[0]);
      const content = Buffer.from(bytes).toString("utf8");
      const filename = uris[0].path.split("/").pop() ?? "brief";
      this._post({ type: "fileContent", content, filename });
    }
  }

  private async _handleBootstrap(payload: BootstrapPayload): Promise<void> {
    this._post({ type: "bootstrapStart" });
    try {
      // Optionally run seeder first for local repos
      if (payload.local_path && payload.project_name) {
        this._runSeeder(payload);
      }

      const task = await this.client.bootstrapProject({
        repo: payload.repo,
        product_brief: payload.product_brief,
        profile: payload.profile || undefined,
        ticket_id: payload.ticket_id || undefined,
      });

      this._post({ type: "bootstrapSuccess", taskId: task.id });
      await this.onRefresh();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      this._post({ type: "bootstrapError", message });
    }
  }

  private _runSeeder(payload: BootstrapPayload): void {
    const seederPath = vscode.workspace
      .getConfiguration("forge")
      .get<string>("seederPath", "");

    if (!seederPath) {
      throw new Error(
        'forge.seederPath is not configured. Set it in VS Code settings (Forge ADP → Seeder Path) to enable local seeding.'
      );
    }

    const args = [
      "-name", payload.project_name!,
      "-company", payload.company_id || payload.project_name!,
      "-project", payload.project_id || payload.repo.split("/")[1] || "project",
      "-github-repo", payload.repo,
      "-output", payload.local_path!,
    ];

    if (payload.profile) {
      args.push("-profile", payload.profile);
    }

    execFileSync(seederPath, args, { encoding: "utf8" });
  }

  // ---- PM chat -------------------------------------------------------------

  private async _handlePmChatSend(userMessage: string): Promise<void> {
    this._chatHistory.push({ role: "user", content: userMessage });
    this._post({ type: "pmChatThinking" });

    try {
      const reply = await this._callAnthropicChat(this._chatHistory);
      this._chatHistory.push({ role: "assistant", content: reply });

      // Extract <brief>...</brief> if present
      const briefMatch = reply.match(/<brief>([\s\S]*?)<\/brief>/i);
      if (briefMatch) {
        this._assembledBrief = briefMatch[1].trim();
        const displayContent = reply.replace(/<brief>[\s\S]*?<\/brief>/i, "").trim();
        this._post({ type: "pmChatReply", content: displayContent });
        this._post({ type: "pmChatBriefReady", brief: this._assembledBrief });
      } else {
        this._post({ type: "pmChatReply", content: reply });
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      this._chatHistory.pop(); // remove the user message so it can be retried
      this._post({ type: "pmChatError", message });
    }
  }

  private _callAnthropicChat(
    messages: { role: string; content: string }[]
  ): Promise<string> {
    return new Promise((resolve, reject) => {
      const apiKey = vscode.workspace
        .getConfiguration("forge")
        .get<string>("anthropicApiKey", "");
      if (!apiKey) {
        reject(
          new Error(
            "Anthropic API key not configured. Set forge.anthropicApiKey in VS Code settings."
          )
        );
        return;
      }
      const model = vscode.workspace
        .getConfiguration("forge")
        .get<string>("plannerModel", "claude-opus-4-6");

      const body = JSON.stringify({
        model,
        max_tokens: 1024,
        system: PM_INTERVIEW_SYSTEM_PROMPT,
        messages,
      });

      const req = https.request(
        {
          hostname: "api.anthropic.com",
          path: "/v1/messages",
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "x-api-key": apiKey,
            "anthropic-version": "2023-06-01",
            "Content-Length": Buffer.byteLength(body),
          },
        },
        (res) => {
          let data = "";
          res.on("data", (chunk) => (data += chunk));
          res.on("end", () => {
            if (res.statusCode && res.statusCode >= 400) {
              reject(new Error(`Anthropic API ${res.statusCode}: ${data}`));
              return;
            }
            try {
              const parsed = JSON.parse(data) as {
                content: { text: string }[];
              };
              resolve(parsed.content[0].text);
            } catch {
              reject(new Error("Failed to parse Anthropic response"));
            }
          });
        }
      );
      req.on("error", reject);
      req.write(body);
      req.end();
    });
  }

  private async _handlePmChatSubmit(payload: {
    repo: string;
    profile: string;
    ticket_id?: string;
  }): Promise<void> {
    if (!this._assembledBrief) {
      this._post({ type: "pmChatSubmitError", message: "No brief assembled yet — keep chatting with the PM agent." });
      return;
    }
    try {
      const task = await this.client.bootstrapProject({
        repo: payload.repo,
        product_brief: this._assembledBrief,
        profile: payload.profile || undefined,
        ticket_id: payload.ticket_id || undefined,
      });
      this._post({ type: "pmChatSubmitSuccess", taskId: task.id });
      await this.onRefresh();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      this._post({ type: "pmChatSubmitError", message });
    }
  }

  // ---- helpers -------------------------------------------------------------

  private _post(msg: MsgToWebview): void {
    this._view?.webview.postMessage(msg);
  }

  // ---- HTML ----------------------------------------------------------------

  private _buildHtml(): string {
    // Pre-fill repo from the first workspace folder if available
    const workspaceRepo = (() => {
      const folders = vscode.workspace.workspaceFolders;
      if (!folders?.length) return "";
      // Use folder name as a best-guess repo slug; user can override
      return folders[0].name;
    })();

    return /* html */ `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<meta http-equiv="Content-Security-Policy"
  content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline';" />
<title>Forge ADP</title>
<style>
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }

  body {
    font-family: var(--vscode-font-family);
    font-size: var(--vscode-font-size);
    color: var(--vscode-foreground);
    background: var(--vscode-sideBar-background);
    display: flex;
    flex-direction: column;
    height: 100vh;
    overflow: hidden;
  }

  /* ── Header ─────────────────────────────────────────────────────────── */
  .header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 7px 12px 0;
    border-bottom: 1px solid var(--vscode-panel-border);
    flex-shrink: 0;
  }
  .header-title {
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.07em;
    color: var(--vscode-sideBarTitle-foreground);
  }
  .icon-btn {
    background: none;
    border: none;
    cursor: pointer;
    color: var(--vscode-icon-foreground);
    padding: 2px 5px;
    border-radius: 3px;
    font-size: 14px;
    line-height: 1.2;
    margin-bottom: 4px;
  }
  .icon-btn:hover { background: var(--vscode-toolbar-hoverBackground); }

  /* ── Tabs ────────────────────────────────────────────────────────────── */
  .tabs {
    display: flex;
    gap: 2px;
    align-items: flex-end;
  }
  .tab-btn {
    background: none;
    border: none;
    border-bottom: 2px solid transparent;
    cursor: pointer;
    color: var(--vscode-descriptionForeground);
    font-family: var(--vscode-font-family);
    font-size: 11px;
    font-weight: 500;
    padding: 4px 10px 5px;
    letter-spacing: 0.03em;
    transition: color .1s, border-color .1s;
  }
  .tab-btn:hover { color: var(--vscode-foreground); }
  .tab-btn.active {
    color: var(--vscode-foreground);
    border-bottom-color: var(--vscode-focusBorder, #007acc);
  }

  /* ── Summary bar ─────────────────────────────────────────────────────── */
  .summary {
    padding: 5px 12px;
    font-size: 11px;
    color: var(--vscode-descriptionForeground);
    border-bottom: 1px solid var(--vscode-panel-border);
    flex-shrink: 0;
    min-height: 26px;
    display: flex;
    align-items: center;
  }
  .summary.attention {
    color: #fff;
    background: var(--vscode-statusBarItem-warningBackground, #6a4f00);
  }

  /* ── Views ───────────────────────────────────────────────────────────── */
  .view { display: flex; flex-direction: column; flex: 1; overflow: hidden; }
  .view.hidden { display: none; }

  /* ── Task list ───────────────────────────────────────────────────────── */
  .task-list { flex: 1; overflow-y: auto; }

  .empty-state, .error-state, .loading-state {
    padding: 28px 16px;
    text-align: center;
    color: var(--vscode-descriptionForeground);
    font-size: 12px;
    line-height: 1.6;
  }
  .error-state { color: var(--vscode-errorForeground); }

  /* ── Task card ───────────────────────────────────────────────────────── */
  .task-card {
    padding: 8px 12px;
    border-bottom: 1px solid var(--vscode-list-inactiveSelectionBackground, #2a2d2e33);
    cursor: pointer;
  }
  .task-card:hover { background: var(--vscode-list-hoverBackground); }

  .card-row1 {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 3px;
  }
  .task-title {
    font-size: 12px;
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 1;
  }
  .task-meta {
    font-size: 11px;
    color: var(--vscode-descriptionForeground);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    margin-bottom: 2px;
  }

  /* ── Status badge ────────────────────────────────────────────────────── */
  .badge {
    display: inline-block;
    padding: 1px 5px;
    border-radius: 3px;
    font-size: 9px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    flex-shrink: 0;
    white-space: nowrap;
  }
  .s-pending   { background: rgba(128,128,128,.15); color: #888; }
  .s-running   { background: rgba(79,195,247,.15);  color: #4fc3f7; }
  .s-blocked   { background: rgba(255,167,38,.15);  color: #ffa726; }
  .s-completed { background: rgba(102,187,106,.15); color: #66bb6a; }
  .s-failed    { background: rgba(239,83,80,.15);   color: #ef5350; }
  .s-awaiting  { background: rgba(255,213,79,.15);  color: #ffd54f; }

  /* ── Inline action buttons ───────────────────────────────────────────── */
  .task-actions {
    display: flex;
    gap: 5px;
    margin-top: 7px;
    flex-wrap: wrap;
  }
  .btn {
    padding: 3px 10px;
    border-radius: 2px;
    font-size: 11px;
    font-family: var(--vscode-font-family);
    cursor: pointer;
    border: 1px solid transparent;
    line-height: 1.4;
  }
  .btn-primary {
    background: var(--vscode-button-background);
    color: var(--vscode-button-foreground);
    border-color: var(--vscode-button-background);
  }
  .btn-primary:hover { background: var(--vscode-button-hoverBackground); }
  .btn-secondary {
    background: transparent;
    color: var(--vscode-foreground);
    border-color: var(--vscode-button-secondaryBorder, #555);
  }
  .btn-secondary:hover { background: var(--vscode-list-hoverBackground); }
  .btn:disabled { opacity: .45; cursor: not-allowed; }

  /* ── Inline reject form ──────────────────────────────────────────────── */
  .reject-form {
    margin-top: 7px;
    display: flex;
    flex-direction: column;
    gap: 5px;
  }
  .reject-form.hidden { display: none; }
  .reject-form textarea {
    width: 100%;
    background: var(--vscode-input-background);
    color: var(--vscode-input-foreground);
    border: 1px solid var(--vscode-input-border, #555);
    border-radius: 2px;
    padding: 4px 6px;
    font-family: var(--vscode-font-family);
    font-size: 11px;
    resize: vertical;
    min-height: 48px;
    outline: none;
  }
  .reject-form textarea:focus { border-color: var(--vscode-focusBorder); }

  /* ── Compose section ─────────────────────────────────────────────────── */
  .compose { border-top: 1px solid var(--vscode-panel-border); flex-shrink: 0; }

  .compose-toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 7px 12px;
    cursor: pointer;
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.07em;
    color: var(--vscode-sideBarTitle-foreground);
    user-select: none;
  }
  .compose-toggle:hover { background: var(--vscode-list-hoverBackground); }
  .compose-arrow { font-size: 9px; opacity: .7; transition: transform .15s; }
  .compose-arrow.open { transform: rotate(90deg); }

  .compose-body {
    padding: 4px 12px 14px;
    display: flex;
    flex-direction: column;
    gap: 7px;
    max-height: 320px;
    overflow-y: auto;
  }
  .compose-body.hidden { display: none; }

  /* ── Shared form fields ──────────────────────────────────────────────── */
  .field label {
    display: block;
    font-size: 11px;
    color: var(--vscode-descriptionForeground);
    margin-bottom: 3px;
  }
  .field select,
  .field input,
  .field textarea {
    width: 100%;
    background: var(--vscode-input-background);
    color: var(--vscode-input-foreground);
    border: 1px solid var(--vscode-input-border, #555);
    border-radius: 2px;
    padding: 4px 6px;
    font-family: var(--vscode-font-family);
    font-size: 12px;
    outline: none;
  }
  .field select:focus,
  .field input:focus,
  .field textarea:focus { border-color: var(--vscode-focusBorder); }
  .field textarea { resize: vertical; min-height: 58px; }

  .form-error {
    font-size: 11px;
    color: var(--vscode-errorForeground);
  }
  .form-error.hidden { display: none; }

  .btn-submit {
    width: 100%;
    padding: 6px;
    background: var(--vscode-button-background);
    color: var(--vscode-button-foreground);
    border: none;
    border-radius: 2px;
    font-family: var(--vscode-font-family);
    font-size: 12px;
    cursor: pointer;
    margin-top: 2px;
  }
  .btn-submit:hover { background: var(--vscode-button-hoverBackground); }
  .btn-submit:disabled { opacity: .45; cursor: not-allowed; }

  /* ── Bootstrap view ──────────────────────────────────────────────────── */
  .bootstrap-form {
    flex: 1;
    overflow-y: auto;
    padding: 12px 12px 20px;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .seed-toggle {
    display: flex;
    align-items: center;
    gap: 7px;
    font-size: 12px;
    cursor: pointer;
    user-select: none;
    padding: 6px 0 2px;
  }
  .seed-toggle input[type="checkbox"] { cursor: pointer; }

  .seed-fields {
    display: flex;
    flex-direction: column;
    gap: 7px;
    padding: 6px 0 2px;
    border-left: 2px solid var(--vscode-panel-border);
    padding-left: 10px;
    margin-left: 2px;
  }
  .seed-fields.hidden { display: none; }

  .file-row {
    display: flex;
    gap: 6px;
    align-items: flex-end;
  }
  .file-row textarea {
    flex: 1;
    min-height: 80px;
  }
  .btn-file {
    flex-shrink: 0;
    padding: 4px 8px;
    font-size: 11px;
    background: transparent;
    color: var(--vscode-foreground);
    border: 1px solid var(--vscode-button-secondaryBorder, #555);
    border-radius: 2px;
    cursor: pointer;
    font-family: var(--vscode-font-family);
    white-space: nowrap;
    align-self: flex-start;
    margin-top: 18px;
  }
  .btn-file:hover { background: var(--vscode-list-hoverBackground); }

  .file-label {
    font-size: 10px;
    color: var(--vscode-descriptionForeground);
    margin-top: 2px;
    font-style: italic;
  }

  .bootstrap-success {
    padding: 14px;
    background: rgba(102,187,106,.1);
    border: 1px solid rgba(102,187,106,.4);
    border-radius: 3px;
    font-size: 12px;
    color: #66bb6a;
    line-height: 1.5;
  }
  .bootstrap-success.hidden { display: none; }

  /* ── Plan / PM chat view ─────────────────────────────────────────────── */
  .chat-messages {
    flex: 1;
    overflow-y: auto;
    padding: 10px 12px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .chat-empty {
    padding: 28px 16px;
    text-align: center;
    color: var(--vscode-descriptionForeground);
    font-size: 12px;
    line-height: 1.6;
  }
  .chat-msg {
    max-width: 88%;
    padding: 7px 10px;
    border-radius: 6px;
    font-size: 12px;
    line-height: 1.5;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .chat-msg.assistant {
    background: var(--vscode-list-inactiveSelectionBackground, #2a2d2e44);
    align-self: flex-start;
  }
  .chat-msg.user {
    background: var(--vscode-button-background);
    color: var(--vscode-button-foreground);
    align-self: flex-end;
  }
  .chat-msg.thinking {
    align-self: flex-start;
    background: transparent;
    color: var(--vscode-descriptionForeground);
    font-style: italic;
    font-size: 11px;
    padding: 2px 4px;
  }
  .chat-msg.error {
    background: rgba(239,83,80,.1);
    color: var(--vscode-errorForeground);
    align-self: flex-start;
    font-size: 11px;
  }
  .brief-ready {
    border-top: 1px solid var(--vscode-panel-border);
    padding: 10px 12px 14px;
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    gap: 7px;
  }
  .brief-ready.hidden { display: none; }
  .brief-ready-label {
    font-size: 11px;
    font-weight: 600;
    color: #66bb6a;
  }
  .chat-input-area {
    border-top: 1px solid var(--vscode-panel-border);
    padding: 8px 12px 10px;
    display: flex;
    flex-direction: column;
    gap: 6px;
    flex-shrink: 0;
  }
  .chat-input-row {
    display: flex;
    gap: 6px;
    align-items: flex-end;
  }
  .chat-input-row textarea {
    flex: 1;
    background: var(--vscode-input-background);
    color: var(--vscode-input-foreground);
    border: 1px solid var(--vscode-input-border, #555);
    border-radius: 2px;
    padding: 5px 7px;
    font-family: var(--vscode-font-family);
    font-size: 12px;
    resize: none;
    min-height: 38px;
    max-height: 120px;
    outline: none;
  }
  .chat-input-row textarea:focus { border-color: var(--vscode-focusBorder); }
  .chat-footer {
    display: flex;
    justify-content: flex-end;
  }
  .btn-link {
    background: none;
    border: none;
    color: var(--vscode-descriptionForeground);
    font-family: var(--vscode-font-family);
    font-size: 11px;
    cursor: pointer;
    padding: 0;
    text-decoration: underline;
    text-underline-offset: 2px;
  }
  .btn-link:hover { color: var(--vscode-foreground); }
  .plan-success {
    padding: 14px;
    background: rgba(102,187,106,.1);
    border: 1px solid rgba(102,187,106,.4);
    border-radius: 3px;
    font-size: 12px;
    color: #66bb6a;
    line-height: 1.5;
  }
  .plan-success.hidden { display: none; }

  /* ── Scrollbars ──────────────────────────────────────────────────────── */
  ::-webkit-scrollbar { width: 4px; }
  ::-webkit-scrollbar-thumb {
    background: var(--vscode-scrollbarSlider-background);
    border-radius: 2px;
  }
  ::-webkit-scrollbar-track { background: transparent; }
</style>
</head>
<body>

<!-- Header -->
<div class="header">
  <span class="header-title">Forge ADP</span>
  <div class="tabs">
    <button class="tab-btn active" id="tab-tasks">Tasks</button>
    <button class="tab-btn" id="tab-plan">Plan</button>
    <button class="tab-btn" id="tab-bootstrap">Bootstrap</button>
  </div>
  <button class="icon-btn" id="btn-refresh" title="Refresh tasks">↺</button>
</div>

<!-- ── Tasks view ─────────────────────────────────────────────────────────── -->
<div class="view" id="view-tasks">
  <!-- Summary -->
  <div class="summary" id="summary">Loading…</div>

  <!-- Task list -->
  <div class="task-list" id="task-list">
    <div class="loading-state">Loading tasks…</div>
  </div>

  <!-- Compose -->
  <div class="compose">
    <div class="compose-toggle" id="compose-toggle">
      <span class="compose-arrow" id="compose-arrow">▶</span>
      New Task
    </div>
    <div class="compose-body hidden" id="compose-body">
      <div class="field">
        <label>Agent Role</label>
        <select id="f-role">
          <option>backend-developer</option>
          <option>frontend-developer</option>
          <option>dba</option>
          <option>devops</option>
          <option>sre</option>
          <option>secops</option>
          <option>qa</option>
          <option>pm</option>
        </select>
      </div>
      <div class="field">
        <label>Title</label>
        <input id="f-title" type="text" placeholder="Implement user authentication" />
      </div>
      <div class="field">
        <label>Description</label>
        <textarea id="f-desc" placeholder="Acceptance criteria, context, links…"></textarea>
      </div>
      <div class="field">
        <label>Ticket ID <span style="opacity:.6">(optional)</span></label>
        <input id="f-ticket" type="text" placeholder="AUTH-42" />
      </div>
      <div class="form-error hidden" id="form-error"></div>
      <button class="btn-submit" id="btn-submit">Submit Task</button>
    </div>
  </div>
</div>

<!-- ── Bootstrap view ─────────────────────────────────────────────────────── -->
<div class="view hidden" id="view-bootstrap">
  <div class="bootstrap-form">

    <!-- Success banner (hidden until bootstrap succeeds) -->
    <div class="bootstrap-success hidden" id="b-success"></div>

    <!-- Seed toggle -->
    <label class="seed-toggle">
      <input type="checkbox" id="b-seed-check" />
      Seed project first <span style="opacity:.6;font-size:11px">(creates .forge/)</span>
    </label>

    <!-- Seed-only fields (shown when checkbox is checked) -->
    <div class="seed-fields hidden" id="seed-fields">
      <div class="field">
        <label>Project Name</label>
        <input id="b-name" type="text" placeholder="Payments API" />
      </div>
      <div class="field">
        <label>Company ID</label>
        <input id="b-company" type="text" placeholder="acme" />
      </div>
      <div class="field">
        <label>Project ID / slug</label>
        <input id="b-project" type="text" placeholder="acme-payments" />
      </div>
      <div class="field">
        <label>Local directory</label>
        <input id="b-localpath" type="text" placeholder="/Users/me/projects/payments-api" />
      </div>
    </div>

    <!-- Always-shown fields -->
    <div class="field">
      <label>GitHub Repo</label>
      <input id="b-repo" type="text" placeholder="org/repo" value="${workspaceRepo}" />
    </div>
    <div class="field">
      <label>Profile</label>
      <select id="b-profile">
        <option value="">(none)</option>
        <option value="web-fullstack">web-fullstack — Next.js + Go + PostgreSQL</option>
        <option value="api-service">api-service — Go + PostgreSQL, no frontend</option>
        <option value="data-pipeline">data-pipeline — Python + PostgreSQL + workers</option>
      </select>
    </div>
    <div class="field">
      <label>Product Brief</label>
      <div class="file-row">
        <textarea id="b-brief" placeholder="Describe the product: what it does, who it's for, core features, key constraints…"></textarea>
        <button class="btn-file" id="btn-load-file" title="Load from a .md or .txt file">📄 Load file</button>
      </div>
      <div class="file-label hidden" id="b-file-label"></div>
    </div>
    <div class="field">
      <label>Ticket ID <span style="opacity:.6">(optional)</span></label>
      <input id="b-ticket" type="text" placeholder="PROJ-1" />
    </div>
    <div class="form-error hidden" id="b-form-error"></div>
    <button class="btn-submit" id="btn-bootstrap">Bootstrap Project</button>
  </div>
</div>

<!-- ── Plan view ──────────────────────────────────────────────────────────── -->
<div class="view hidden" id="view-plan">

  <!-- Chat transcript -->
  <div class="chat-messages" id="chat-messages">
    <div class="chat-empty" id="chat-empty">
      Chat with the PM agent to define your project.<br/>
      It will ask questions and assemble a product brief<br/>
      ready to submit when you're done.
    </div>
  </div>

  <!-- Submit form — shown once brief is ready -->
  <div class="brief-ready hidden" id="brief-ready">
    <div class="brief-ready-label">✓ Product brief assembled</div>
    <div class="plan-success hidden" id="plan-success"></div>
    <div class="field">
      <label>GitHub Repo</label>
      <input id="c-repo" type="text" placeholder="org/repo" value="${workspaceRepo}" />
    </div>
    <div class="field">
      <label>Profile</label>
      <select id="c-profile">
        <option value="">(none)</option>
        <option value="web-fullstack">web-fullstack — Next.js + Go + PostgreSQL</option>
        <option value="api-service">api-service — Go + PostgreSQL, no frontend</option>
        <option value="data-pipeline">data-pipeline — Python + PostgreSQL + workers</option>
      </select>
    </div>
    <div class="field">
      <label>Ticket ID <span style="opacity:.6">(optional)</span></label>
      <input id="c-ticket" type="text" placeholder="PROJ-1" />
    </div>
    <div class="form-error hidden" id="c-form-error"></div>
    <button class="btn-submit" id="btn-plan-submit">Submit to PM Agent</button>
  </div>

  <!-- Input area -->
  <div class="chat-input-area">
    <div class="chat-input-row">
      <textarea id="chat-input" placeholder="Type a message… (Enter to send, Shift+Enter for newline)" rows="2"></textarea>
      <button class="btn btn-primary" id="btn-chat-send" style="flex-shrink:0;align-self:flex-end">Send</button>
    </div>
    <div class="chat-footer">
      <button class="btn-link" id="btn-chat-reset">Start over</button>
    </div>
  </div>

</div>

<script>
  /* global acquireVsCodeApi */
  const vscode = acquireVsCodeApi();

  // ── State ──────────────────────────────────────────────────────────────────
  let tasks   = [];
  let loading = true;
  let error   = null;
  let composeOpen = false;

  // ── DOM refs — Tasks ───────────────────────────────────────────────────────
  const summaryEl     = document.getElementById('summary');
  const taskListEl    = document.getElementById('task-list');
  const composeToggle = document.getElementById('compose-toggle');
  const composeBody   = document.getElementById('compose-body');
  const composeArrow  = document.getElementById('compose-arrow');
  const btnRefresh    = document.getElementById('btn-refresh');
  const btnSubmit     = document.getElementById('btn-submit');
  const formError     = document.getElementById('form-error');
  const fRole         = document.getElementById('f-role');
  const fTitle        = document.getElementById('f-title');
  const fDesc         = document.getElementById('f-desc');
  const fTicket       = document.getElementById('f-ticket');

  // ── DOM refs — Bootstrap ───────────────────────────────────────────────────
  const bSeedCheck  = document.getElementById('b-seed-check');
  const seedFields  = document.getElementById('seed-fields');
  const bRepo       = document.getElementById('b-repo');
  const bProfile    = document.getElementById('b-profile');
  const bBrief      = document.getElementById('b-brief');
  const bTicket     = document.getElementById('b-ticket');
  const bName       = document.getElementById('b-name');
  const bCompany    = document.getElementById('b-company');
  const bProject    = document.getElementById('b-project');
  const bLocalPath  = document.getElementById('b-localpath');
  const bFormError  = document.getElementById('b-form-error');
  const bSuccess    = document.getElementById('b-success');
  const bFileLabel  = document.getElementById('b-file-label');
  const btnBootstrap   = document.getElementById('btn-bootstrap');
  const btnLoadFile    = document.getElementById('btn-load-file');

  // ── DOM refs — Plan ────────────────────────────────────────────────────────
  const chatMessages  = document.getElementById('chat-messages');
  const chatEmpty     = document.getElementById('chat-empty');
  const chatInput     = document.getElementById('chat-input');
  const btnChatSend   = document.getElementById('btn-chat-send');
  const btnChatReset  = document.getElementById('btn-chat-reset');
  const briefReady    = document.getElementById('brief-ready');
  const cRepo         = document.getElementById('c-repo');
  const cProfile      = document.getElementById('c-profile');
  const cTicket       = document.getElementById('c-ticket');
  const cFormError    = document.getElementById('c-form-error');
  const planSuccess   = document.getElementById('plan-success');
  const btnPlanSubmit = document.getElementById('btn-plan-submit');

  // ── Tabs ───────────────────────────────────────────────────────────────────
  const tabTasks     = document.getElementById('tab-tasks');
  const tabPlan      = document.getElementById('tab-plan');
  const tabBootstrap = document.getElementById('tab-bootstrap');
  const viewTasks    = document.getElementById('view-tasks');
  const viewPlan     = document.getElementById('view-plan');
  const viewBootstrap = document.getElementById('view-bootstrap');

  function switchTab(tab) {
    tabTasks.classList.toggle('active',     tab === 'tasks');
    tabPlan.classList.toggle('active',      tab === 'plan');
    tabBootstrap.classList.toggle('active', tab === 'bootstrap');
    viewTasks.classList.toggle('hidden',     tab !== 'tasks');
    viewPlan.classList.toggle('hidden',      tab !== 'plan');
    viewBootstrap.classList.toggle('hidden', tab !== 'bootstrap');
  }

  tabTasks.addEventListener('click',     () => switchTab('tasks'));
  tabPlan.addEventListener('click',      () => switchTab('plan'));
  tabBootstrap.addEventListener('click', () => switchTab('bootstrap'));

  // ── Seed toggle ────────────────────────────────────────────────────────────
  bSeedCheck.addEventListener('change', () => {
    seedFields.classList.toggle('hidden', !bSeedCheck.checked);
  });

  // ── Compose toggle ─────────────────────────────────────────────────────────
  composeToggle.addEventListener('click', () => {
    composeOpen = !composeOpen;
    composeBody.classList.toggle('hidden', !composeOpen);
    composeArrow.classList.toggle('open', composeOpen);
  });

  // ── Refresh ────────────────────────────────────────────────────────────────
  btnRefresh.addEventListener('click', () => vscode.postMessage({ type: 'refresh' }));

  // ── Submit task ────────────────────────────────────────────────────────────
  btnSubmit.addEventListener('click', () => {
    const title       = fTitle.value.trim();
    const description = fDesc.value.trim();
    if (!title)       { showFormError(formError, 'Title is required.');       return; }
    if (!description) { showFormError(formError, 'Description is required.'); return; }
    hideFormError(formError);
    vscode.postMessage({
      type: 'submitTask',
      payload: {
        agent_role: fRole.value,
        title,
        description,
        ticket_id: fTicket.value.trim() || undefined,
      },
    });
  });

  // ── Load file ──────────────────────────────────────────────────────────────
  btnLoadFile.addEventListener('click', () => {
    vscode.postMessage({ type: 'openFilePicker' });
  });

  // ── Bootstrap ──────────────────────────────────────────────────────────────
  btnBootstrap.addEventListener('click', () => {
    const repo  = bRepo.value.trim();
    const brief = bBrief.value.trim();
    if (!repo)  { showFormError(bFormError, 'GitHub Repo is required (org/repo).'); return; }
    if (!brief) { showFormError(bFormError, 'Product Brief is required.'); return; }
    if (bSeedCheck.checked && !bName.value.trim()) {
      showFormError(bFormError, 'Project Name is required when seeding.'); return;
    }
    if (bSeedCheck.checked && !bLocalPath.value.trim()) {
      showFormError(bFormError, 'Local directory is required when seeding.'); return;
    }
    hideFormError(bFormError);

    vscode.postMessage({
      type: 'bootstrapProject',
      payload: {
        repo,
        product_brief: brief,
        profile: bProfile.value,
        ticket_id: bTicket.value.trim() || undefined,
        local_path: bSeedCheck.checked ? bLocalPath.value.trim() : undefined,
        project_name: bSeedCheck.checked ? bName.value.trim() : undefined,
        company_id: bSeedCheck.checked ? bCompany.value.trim() : undefined,
        project_id: bSeedCheck.checked ? bProject.value.trim() : undefined,
      },
    });
  });

  // ── Helpers ────────────────────────────────────────────────────────────────
  function showFormError(el, msg) {
    el.textContent = msg;
    el.classList.remove('hidden');
  }
  function hideFormError(el) {
    el.classList.add('hidden');
  }

  // ── Render — Tasks ─────────────────────────────────────────────────────────
  const STATUS_ORDER = ['awaiting_approval','blocked','running','pending','failed','completed'];

  const BADGE = {
    pending:           ['s-pending',   'PENDING'],
    running:           ['s-running',   'RUNNING'],
    blocked:           ['s-blocked',   'BLOCKED'],
    completed:         ['s-completed', 'DONE'],
    failed:            ['s-failed',    'FAILED'],
    awaiting_approval: ['s-awaiting',  'APPROVAL'],
  };

  function esc(s) {
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function renderSummary() {
    if (loading) { summaryEl.textContent = 'Loading…'; summaryEl.className = 'summary'; return; }
    if (error)   { summaryEl.textContent = '⚠ ' + error; summaryEl.className = 'summary'; return; }

    const needAttention = tasks.filter(t =>
      t.status === 'awaiting_approval' || t.status === 'blocked'
    ).length;
    const running = tasks.filter(t => t.status === 'running').length;

    if (needAttention > 0) {
      summaryEl.textContent = \`● \${needAttention} task\${needAttention > 1 ? 's' : ''} need\${needAttention === 1 ? 's' : ''} attention\`;
      summaryEl.className = 'summary attention';
    } else if (running > 0) {
      summaryEl.textContent = \`↻ \${running} running\`;
      summaryEl.className = 'summary';
    } else {
      summaryEl.textContent = \`\${tasks.length} task\${tasks.length !== 1 ? 's' : ''}\`;
      summaryEl.className = 'summary';
    }
  }

  function renderTasks() {
    if (loading) { taskListEl.innerHTML = '<div class="loading-state">Loading tasks…</div>'; return; }
    if (error)   { taskListEl.innerHTML = '<div class="error-state">Could not reach the orchestrator.<br/>Is it running?</div>'; return; }
    if (!tasks.length) { taskListEl.innerHTML = '<div class="empty-state">No tasks yet.<br/>Submit one below or bootstrap a project.</div>'; return; }

    const sorted = [...tasks].sort((a, b) =>
      STATUS_ORDER.indexOf(a.status) - STATUS_ORDER.indexOf(b.status)
    );
    taskListEl.innerHTML = sorted.map(taskCard).join('');
    bindCardEvents();
  }

  function taskCard(t) {
    const [cls, label] = BADGE[t.status] || ['s-pending', t.status.toUpperCase()];
    const meta = [t.agent_role, t.ticket_id].filter(Boolean).join(' · ');
    const awaitingApproval = t.status === 'awaiting_approval';

    return \`
<div class="task-card" data-task-id="\${esc(t.id)}">
  <div class="card-row1">
    <span class="badge \${cls}">\${label}</span>
    <span class="task-title" title="\${esc(t.title)}">\${esc(t.title)}</span>
  </div>
  <div class="task-meta">\${esc(meta)}</div>
  \${awaitingApproval ? \`
  <div class="task-actions">
    <button class="btn btn-primary" data-action="approve" data-id="\${esc(t.id)}">✓ Approve</button>
    <button class="btn btn-secondary" data-action="reject-toggle" data-id="\${esc(t.id)}">✗ Reject</button>
  </div>
  <div class="reject-form hidden" id="rf-\${esc(t.id)}">
    <textarea placeholder="Reason for rejection…" id="rt-\${esc(t.id)}"></textarea>
    <div style="display:flex;gap:5px">
      <button class="btn btn-secondary" data-action="reject-cancel" data-id="\${esc(t.id)}" style="flex:1">Cancel</button>
      <button class="btn btn-primary"   data-action="reject-confirm" data-id="\${esc(t.id)}" style="flex:2">Confirm</button>
    </div>
  </div>\` : ''}
</div>\`;
  }

  function bindCardEvents() {
    taskListEl.querySelectorAll('.task-card').forEach(card => {
      card.addEventListener('click', e => {
        if (e.target.closest('[data-action]')) return;
        vscode.postMessage({ type: 'openTask', payload: { id: card.dataset.taskId } });
      });
    });

    taskListEl.querySelectorAll('[data-action]').forEach(btn => {
      btn.addEventListener('click', e => {
        e.stopPropagation();
        const { action, id } = btn.dataset;

        if (action === 'approve') {
          btn.disabled = true;
          btn.textContent = 'Approving…';
          vscode.postMessage({ type: 'approveTask', payload: { id } });

        } else if (action === 'reject-toggle') {
          const form = document.getElementById(\`rf-\${id}\`);
          form && form.classList.toggle('hidden');

        } else if (action === 'reject-cancel') {
          const form = document.getElementById(\`rf-\${id}\`);
          form && form.classList.add('hidden');

        } else if (action === 'reject-confirm') {
          const textarea = document.getElementById(\`rt-\${id}\`);
          const reason = textarea ? textarea.value.trim() : '';
          if (!reason) { textarea && (textarea.style.borderColor = 'var(--vscode-errorForeground)'); return; }
          btn.disabled = true;
          btn.textContent = 'Rejecting…';
          vscode.postMessage({ type: 'rejectTask', payload: { id, reason } });
        }
      });
    });
  }

  function render() {
    renderSummary();
    renderTasks();
  }

  // ── Plan / PM chat ────────────────────────────────────────────────────────
  let chatWaiting = false;

  function appendChatMsg(role, text) {
    chatEmpty.classList.add('hidden');
    const div = document.createElement('div');
    div.className = 'chat-msg ' + role;
    div.textContent = text;
    div.id = role === 'thinking' ? 'msg-thinking' : '';
    chatMessages.appendChild(div);
    chatMessages.scrollTop = chatMessages.scrollHeight;
    return div;
  }

  function removeThinking() {
    const t = document.getElementById('msg-thinking');
    if (t) t.remove();
  }

  function sendChatMessage() {
    if (chatWaiting) return;
    const text = chatInput.value.trim();
    if (!text) return;
    chatInput.value = '';
    chatInput.style.height = '';
    chatWaiting = true;
    btnChatSend.disabled = true;
    appendChatMsg('user', text);
    appendChatMsg('thinking', 'Thinking…');
    vscode.postMessage({ type: 'pmChatSend', message: text });
  }

  btnChatSend.addEventListener('click', sendChatMessage);

  chatInput.addEventListener('keydown', e => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      sendChatMessage();
    }
  });

  // Auto-grow textarea
  chatInput.addEventListener('input', () => {
    chatInput.style.height = 'auto';
    chatInput.style.height = Math.min(chatInput.scrollHeight, 120) + 'px';
  });

  btnChatReset.addEventListener('click', () => {
    chatMessages.innerHTML = '';
    chatMessages.appendChild(chatEmpty);
    chatEmpty.classList.remove('hidden');
    briefReady.classList.add('hidden');
    planSuccess.classList.add('hidden');
    planSuccess.textContent = '';
    hideFormError(cFormError);
    chatWaiting = false;
    btnChatSend.disabled = false;
    btnPlanSubmit.disabled = false;
    btnPlanSubmit.textContent = 'Submit to PM Agent';
    vscode.postMessage({ type: 'pmChatReset' });
  });

  btnPlanSubmit.addEventListener('click', () => {
    const repo = cRepo.value.trim();
    if (!repo) { showFormError(cFormError, 'GitHub Repo is required (org/repo).'); return; }
    hideFormError(cFormError);
    btnPlanSubmit.disabled = true;
    btnPlanSubmit.textContent = 'Submitting…';
    planSuccess.classList.add('hidden');
    vscode.postMessage({
      type: 'pmChatSubmit',
      payload: {
        repo,
        profile: cProfile.value,
        ticket_id: cTicket.value.trim() || undefined,
      },
    });
  });

  // ── Messages from extension host ───────────────────────────────────────────
  window.addEventListener('message', e => {
    const msg = e.data;
    switch (msg.type) {
      case 'update':
        tasks   = msg.tasks || [];
        loading = msg.loading;
        error   = msg.error;
        render();
        break;

      case 'submitStart':
        btnSubmit.disabled    = true;
        btnSubmit.textContent = 'Submitting…';
        break;

      case 'submitSuccess':
        btnSubmit.disabled    = false;
        btnSubmit.textContent = 'Submit Task';
        fTitle.value  = '';
        fDesc.value   = '';
        fTicket.value = '';
        hideFormError(formError);
        composeOpen = false;
        composeBody.classList.add('hidden');
        composeArrow.classList.remove('open');
        break;

      case 'submitError':
        btnSubmit.disabled    = false;
        btnSubmit.textContent = 'Submit Task';
        showFormError(formError, msg.message);
        break;

      case 'fileContent':
        bBrief.value = msg.content;
        bFileLabel.textContent = '📄 ' + msg.filename;
        bFileLabel.classList.remove('hidden');
        break;

      case 'bootstrapStart':
        btnBootstrap.disabled    = true;
        btnBootstrap.textContent = bSeedCheck.checked ? 'Seeding & bootstrapping…' : 'Bootstrapping…';
        bSuccess.classList.add('hidden');
        hideFormError(bFormError);
        break;

      case 'bootstrapSuccess':
        btnBootstrap.disabled    = false;
        btnBootstrap.textContent = 'Bootstrap Project';
        bSuccess.innerHTML = '✓ Bootstrap task submitted — <strong>' + esc(msg.taskId) + '</strong><br/>Switch to the Tasks tab to track progress.';
        bSuccess.classList.remove('hidden');
        // Switch to tasks tab so user can see the new task
        switchTab('tasks');
        break;

      case 'bootstrapError':
        btnBootstrap.disabled    = false;
        btnBootstrap.textContent = 'Bootstrap Project';
        showFormError(bFormError, msg.message);
        break;

      case 'pmChatThinking':
        // thinking bubble already added in sendChatMessage; nothing extra needed
        break;

      case 'pmChatReply':
        removeThinking();
        chatWaiting = false;
        btnChatSend.disabled = false;
        if (msg.content) appendChatMsg('assistant', msg.content);
        break;

      case 'pmChatBriefReady':
        briefReady.classList.remove('hidden');
        break;

      case 'pmChatError':
        removeThinking();
        chatWaiting = false;
        btnChatSend.disabled = false;
        appendChatMsg('error', '⚠ ' + msg.message);
        break;

      case 'pmChatSubmitSuccess':
        btnPlanSubmit.disabled    = false;
        btnPlanSubmit.textContent = 'Submit to PM Agent';
        planSuccess.innerHTML = '✓ Bootstrap task submitted — <strong>' + esc(msg.taskId) + '</strong><br/>Switch to Tasks to track progress.';
        planSuccess.classList.remove('hidden');
        switchTab('tasks');
        break;

      case 'pmChatSubmitError':
        btnPlanSubmit.disabled    = false;
        btnPlanSubmit.textContent = 'Submit to PM Agent';
        showFormError(cFormError, msg.message);
        break;
    }
  });

  render();
</script>
</body>
</html>`;
  }
}
