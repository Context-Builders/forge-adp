/**
 * Forge ADP — Orchestrator / Registry HTTP client
 *
 * Thin wrapper around the Forge REST API. Uses VS Code's workspace
 * configuration to resolve base URLs and auth tokens.
 */

import * as vscode from "vscode";
import * as https from "https";
import * as http from "http";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export type TaskStatus =
  | "pending"
  | "running"
  | "blocked"
  | "completed"
  | "failed"
  | "awaiting_approval";

export interface ForgeTask {
  id: string;
  agent_role: string;
  title: string;
  description: string;
  status: TaskStatus;
  ticket_id?: string;
  created_at: string;
  updated_at: string;
  output?: string;
  error?: string;
}

export interface ForgeAgent {
  id: string;
  role: string;
  status: string;
  skills: string[];
}

export interface ForgeSkill {
  role: string;
  name: string;
  version: string;
  description: string;
  autonomy_level: number;
}

export interface SubmitTaskParams {
  agent_role: string;
  title: string;
  description: string;
  ticket_id?: string;
  skills?: string[];
  autonomy_level?: number;
}

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

export class OrchestratorClient {
  private get orchestratorUrl(): string {
    return vscode.workspace
      .getConfiguration("forge")
      .get<string>("orchestratorUrl", "http://localhost:19080")
      .replace(/\/$/, "");
  }

  private get registryUrl(): string {
    return vscode.workspace
      .getConfiguration("forge")
      .get<string>("registryUrl", "http://localhost:19081")
      .replace(/\/$/, "");
  }

  private get apiToken(): string {
    return vscode.workspace
      .getConfiguration("forge")
      .get<string>("apiToken", "");
  }

  // ---- HTTP helper --------------------------------------------------------

  private request<T = unknown>(
    baseUrl: string,
    path: string,
    options: { method?: string; body?: unknown } = {}
  ): Promise<T> {
    return new Promise((resolve, reject) => {
      const { method = "GET", body } = options;
      const url = new URL(`${baseUrl}${path}`);
      const isHttps = url.protocol === "https:";
      const bodyStr = body !== undefined ? JSON.stringify(body) : undefined;

      const reqOptions: http.RequestOptions = {
        hostname: url.hostname,
        port: url.port || (isHttps ? 443 : 80),
        path: url.pathname + url.search,
        method,
        headers: {
          "Content-Type": "application/json",
          Accept: "application/json",
          ...(this.apiToken
            ? { Authorization: `Bearer ${this.apiToken}` }
            : {}),
          ...(bodyStr
            ? { "Content-Length": Buffer.byteLength(bodyStr) }
            : {}),
        },
      };

      const lib = isHttps ? https : http;
      const req = lib.request(reqOptions, (res) => {
        let data = "";
        res.on("data", (chunk) => (data += chunk));
        res.on("end", () => {
          if (!res.statusCode || res.statusCode >= 400) {
            reject(
              new Error(`Forge API ${res.statusCode}: ${data || "(empty)"}`)
            );
            return;
          }
          try {
            resolve(data ? (JSON.parse(data) as T) : ({} as T));
          } catch {
            resolve(data as unknown as T);
          }
        });
      });

      req.on("error", reject);
      if (bodyStr) req.write(bodyStr);
      req.end();
    });
  }

  // ---- Orchestrator -------------------------------------------------------

  async submitTask(params: SubmitTaskParams): Promise<ForgeTask> {
    return this.request<ForgeTask>(this.orchestratorUrl, "/v1/tasks", {
      method: "POST",
      body: params,
    });
  }

  async getTask(taskId: string): Promise<ForgeTask> {
    return this.request<ForgeTask>(
      this.orchestratorUrl,
      `/v1/tasks/${taskId}`
    );
  }

  async listTasks(filters?: {
    agent_role?: string;
    status?: string;
  }): Promise<ForgeTask[]> {
    const params = new URLSearchParams();
    if (filters?.agent_role) params.set("agent_role", filters.agent_role);
    if (filters?.status) params.set("status", filters.status);
    const qs = params.toString();
    const tasks = await this.request<ForgeTask[] | { tasks: ForgeTask[] }>(
      this.orchestratorUrl,
      `/v1/tasks${qs ? `?${qs}` : ""}`
    );
    return Array.isArray(tasks) ? tasks : tasks.tasks ?? [];
  }

  async approveTask(
    taskId: string,
    comment?: string
  ): Promise<ForgeTask> {
    return this.request<ForgeTask>(
      this.orchestratorUrl,
      `/v1/tasks/${taskId}/approve`,
      { method: "POST", body: comment ? { comment } : undefined }
    );
  }

  async rejectTask(taskId: string, reason: string): Promise<ForgeTask> {
    return this.request<ForgeTask>(
      this.orchestratorUrl,
      `/v1/tasks/${taskId}/reject`,
      { method: "POST", body: { reason } }
    );
  }

  async health(): Promise<{ status: string }> {
    return this.request<{ status: string }>(
      this.orchestratorUrl,
      "/v1/health"
    );
  }

  // ---- Registry -----------------------------------------------------------

  async listAgents(): Promise<ForgeAgent[]> {
    const result = await this.request<ForgeAgent[] | { agents: ForgeAgent[] }>(
      this.registryUrl,
      "/v1/agents"
    );
    return Array.isArray(result) ? result : result.agents ?? [];
  }

  async listSkills(role?: string): Promise<ForgeSkill[]> {
    const qs = role ? `?role=${role}` : "";
    const result = await this.request<ForgeSkill[] | { skills: ForgeSkill[] }>(
      this.registryUrl,
      `/v1/skills${qs}`
    );
    return Array.isArray(result) ? result : result.skills ?? [];
  }
}
