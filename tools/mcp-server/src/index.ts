#!/usr/bin/env node
/**
 * Forge ADP — MCP Server
 *
 * Exposes the Forge Orchestrator, Registry, and Policy Engine APIs as MCP tools,
 * allowing Claude Code (and any other MCP-compatible client) to submit tasks,
 * track progress, and approve / reject agent checkpoints directly from a chat or
 * agentic workflow.
 *
 * Configuration (environment variables):
 *   FORGE_ORCHESTRATOR_URL   Base URL of the Orchestrator  (default: http://localhost:19080)
 *   FORGE_REGISTRY_URL       Base URL of the Registry       (default: http://localhost:19081)
 *   FORGE_POLICY_URL         Base URL of the Policy Engine  (default: http://localhost:19082)
 *   FORGE_API_TOKEN          Bearer token for API auth       (optional for local dev)
 */

import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
  Tool,
} from "@modelcontextprotocol/sdk/types.js";
import { z } from "zod";

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

const ORCHESTRATOR_URL =
  process.env.FORGE_ORCHESTRATOR_URL ?? "http://localhost:19080";
const REGISTRY_URL =
  process.env.FORGE_REGISTRY_URL ?? "http://localhost:19081";
const API_TOKEN = process.env.FORGE_API_TOKEN ?? "";

// ---------------------------------------------------------------------------
// HTTP helper
// ---------------------------------------------------------------------------

async function forgeRequest<T = unknown>(
  baseUrl: string,
  path: string,
  options: {
    method?: string;
    body?: unknown;
  } = {}
): Promise<T> {
  const { method = "GET", body } = options;
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    Accept: "application/json",
  };
  if (API_TOKEN) {
    headers["Authorization"] = `Bearer ${API_TOKEN}`;
  }

  const res = await fetch(`${baseUrl}${path}`, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });

  const text = await res.text();
  if (!res.ok) {
    throw new Error(`Forge API error ${res.status}: ${text}`);
  }
  return text ? (JSON.parse(text) as T) : ({} as T);
}

// ---------------------------------------------------------------------------
// Tool definitions
// ---------------------------------------------------------------------------

const TOOLS: Tool[] = [
  {
    name: "forge_submit_task",
    description:
      "Submit a new task to the Forge Orchestrator. This is the primary way to trigger agent work without going through Jira. Returns the created task object including its ID.",
    inputSchema: {
      type: "object",
      properties: {
        agent_role: {
          type: "string",
          description:
            "The agent role to assign the task to (e.g. backend-developer, frontend-developer, architect, pm, qa, devops, sre, secops, dba, governance).",
        },
        title: {
          type: "string",
          description: "Short description of the work to be done.",
        },
        description: {
          type: "string",
          description:
            "Full context for the agent: what needs to be built/changed, acceptance criteria, relevant links.",
        },
        ticket_id: {
          type: "string",
          description:
            "Optional Jira/Linear ticket ID to link this task to (e.g. AUTH-42).",
        },
        skills: {
          type: "array",
          items: { type: "string" },
          description:
            "Optional list of specific skills the agent should apply (e.g. ['api-implementation', 'test-generation']).",
        },
        autonomy_level: {
          type: "number",
          description:
            "Override the project-level autonomy level (0–3). 0 = full human approval, 3 = fully autonomous for low-risk changes.",
          minimum: 0,
          maximum: 3,
        },
      },
      required: ["agent_role", "title", "description"],
    },
  },
  {
    name: "forge_get_task",
    description:
      "Get the current status and output of a Forge task by its ID. Use this to check whether an agent has finished, is blocked, or needs approval.",
    inputSchema: {
      type: "object",
      properties: {
        task_id: {
          type: "string",
          description: "The task ID returned by forge_submit_task.",
        },
      },
      required: ["task_id"],
    },
  },
  {
    name: "forge_list_tasks",
    description:
      "List Forge tasks, optionally filtered by agent role and/or status.",
    inputSchema: {
      type: "object",
      properties: {
        agent_role: {
          type: "string",
          description:
            "Filter by agent role (e.g. backend-developer, qa, devops).",
        },
        status: {
          type: "string",
          enum: [
            "pending",
            "running",
            "blocked",
            "completed",
            "failed",
            "awaiting_approval",
          ],
          description: "Filter by task status.",
        },
        limit: {
          type: "number",
          description: "Maximum number of results to return (default 20).",
          default: 20,
        },
      },
      required: [],
    },
  },
  {
    name: "forge_approve_task",
    description:
      "Approve a pending human-in-the-loop checkpoint for a Forge task. The agent will continue execution after approval.",
    inputSchema: {
      type: "object",
      properties: {
        task_id: {
          type: "string",
          description: "The task ID to approve.",
        },
        comment: {
          type: "string",
          description: "Optional comment or approval note.",
        },
      },
      required: ["task_id"],
    },
  },
  {
    name: "forge_reject_task",
    description:
      "Reject a pending human-in-the-loop checkpoint for a Forge task. The agent will stop and the task will be marked as blocked.",
    inputSchema: {
      type: "object",
      properties: {
        task_id: {
          type: "string",
          description: "The task ID to reject.",
        },
        reason: {
          type: "string",
          description: "Required reason for rejection. This is fed back to the agent.",
        },
      },
      required: ["task_id", "reason"],
    },
  },
  {
    name: "forge_list_agents",
    description:
      "List all registered agent instances in the Forge registry, including their current availability and assigned skills.",
    inputSchema: {
      type: "object",
      properties: {},
      required: [],
    },
  },
  {
    name: "forge_list_skills",
    description:
      "List available skills in the Forge registry, optionally filtered by agent role.",
    inputSchema: {
      type: "object",
      properties: {
        role: {
          type: "string",
          description:
            "Filter skills by agent role (e.g. backend-developer, qa).",
        },
      },
      required: [],
    },
  },
  {
    name: "forge_get_skill",
    description:
      "Get the full manifest and documentation for a specific agent skill.",
    inputSchema: {
      type: "object",
      properties: {
        role: {
          type: "string",
          description: "Agent role the skill belongs to (e.g. backend-developer).",
        },
        name: {
          type: "string",
          description: "Skill name (e.g. api-implementation).",
        },
      },
      required: ["role", "name"],
    },
  },
  {
    name: "forge_list_plans",
    description:
      "List all indexed plan documents (PRODUCT.md, ARCHITECTURE.md, etc.) that agents read for project context.",
    inputSchema: {
      type: "object",
      properties: {},
      required: [],
    },
  },
  {
    name: "forge_bootstrap_project",
    description:
      "Bootstrap a new project by triggering the PM agent's project-bootstrap skill. " +
      "This populates the seeded .forge/ plan documents (PRODUCT.md, ARCHITECTURE.md, etc.) " +
      "with real content derived from the product brief. The PM agent generates product docs " +
      "and delegates technical docs to the Architect agent. Returns a task ID to track progress.",
    inputSchema: {
      type: "object",
      properties: {
        repo: {
          type: "string",
          description:
            "GitHub repository in org/repo format (e.g. acme-corp/my-project). Must already contain a seeded .forge/ directory.",
        },
        product_brief: {
          type: "string",
          description:
            "Detailed product description including: what the product does, who it's for, " +
            "core features, key constraints, and success criteria. The richer this is, " +
            "the better the generated documents will be. Minimum ~50 words recommended.",
        },
        confluence_prd_url: {
          type: "string",
          description:
            "Optional URL to a Confluence PRD page for additional product context.",
        },
        ticket_id: {
          type: "string",
          description:
            "Optional Jira ticket ID to link this bootstrap task to (e.g. PROJ-1).",
        },
      },
      required: ["repo", "product_brief"],
    },
  },
  {
    name: "forge_get_plan_document",
    description:
      "Retrieve the content of a specific .forge/ plan document from a repository. " +
      "Use this to review generated documents after a bootstrap or to check current plan state. " +
      "Common documents: PRODUCT.md, ARCHITECTURE.md, API_CONTRACTS.md, DATA_MODEL.md, CONTRIBUTING.md, config.yaml.",
    inputSchema: {
      type: "object",
      properties: {
        repo: {
          type: "string",
          description: "GitHub repository in org/repo format.",
        },
        document: {
          type: "string",
          description:
            "Document filename (e.g. PRODUCT.md, ARCHITECTURE.md, config.yaml).",
        },
        branch: {
          type: "string",
          description:
            "Branch to read from (default: main). Use the bootstrap PR branch to review generated content before merge.",
        },
      },
      required: ["repo", "document"],
    },
  },
  {
    name: "forge_bootstrap_platform",
    description:
      "Bootstrap a multi-repo platform (API + workers + UI, etc.) by triggering the PM agent's " +
      "project-bootstrap skill in platform mode. All repos/directories must already be seeded with .forge/ " +
      "directories containing a platform section in config.yaml. The PM and Architect agents " +
      "will design a holistic architecture spanning all repos, with shared API contracts and " +
      "event schemas. Pass any one of the platform repos or local paths — siblings are auto-discovered from config.yaml.",
    inputSchema: {
      type: "object",
      properties: {
        repo: {
          type: "string",
          description:
            "Any source in the platform: either a GitHub repo (org/repo) or an absolute local directory path " +
            "(e.g. /Users/me/projects/api). Sibling sources are discovered from the platform section in config.yaml.",
        },
        product_brief: {
          type: "string",
          description:
            "Detailed product description for the entire platform. Describe all services " +
            "(API, workers, UI) and how they relate. Include user-facing features, " +
            "async processing needs, and frontend requirements.",
        },
        confluence_prd_url: {
          type: "string",
          description:
            "Optional URL to a Confluence PRD page.",
        },
        ticket_id: {
          type: "string",
          description:
            "Optional Jira ticket ID to link this bootstrap task to.",
        },
      },
      required: ["repo", "product_brief"],
    },
  },
  {
    name: "forge_health",
    description:
      "Check the health of all Forge control plane services (Orchestrator, Registry, Policy Engine).",
    inputSchema: {
      type: "object",
      properties: {},
      required: [],
    },
  },
];

// ---------------------------------------------------------------------------
// Tool handlers
// ---------------------------------------------------------------------------

const SubmitTaskInput = z.object({
  agent_role: z.string(),
  title: z.string(),
  description: z.string(),
  ticket_id: z.string().optional(),
  skills: z.array(z.string()).optional(),
  autonomy_level: z.number().min(0).max(3).optional(),
});

const TaskIdInput = z.object({ task_id: z.string() });

const ApproveInput = z.object({
  task_id: z.string(),
  comment: z.string().optional(),
});

const RejectInput = z.object({
  task_id: z.string(),
  reason: z.string(),
});

const ListTasksInput = z.object({
  agent_role: z.string().optional(),
  status: z.string().optional(),
  limit: z.number().default(20),
});

const SkillInput = z.object({ role: z.string(), name: z.string() });
const RoleFilterInput = z.object({ role: z.string().optional() });

const BootstrapInput = z.object({
  repo: z.string(),
  product_brief: z.string(),
  confluence_prd_url: z.string().optional(),
  ticket_id: z.string().optional(),
});

const BootstrapPlatformInput = z.object({
  repo: z.string(),
  product_brief: z.string(),
  confluence_prd_url: z.string().optional(),
  ticket_id: z.string().optional(),
});

const PlanDocumentInput = z.object({
  repo: z.string(),
  document: z.string(),
  branch: z.string().optional(),
});

async function handleTool(
  name: string,
  args: Record<string, unknown>
): Promise<string> {
  switch (name) {
    case "forge_submit_task": {
      const input = SubmitTaskInput.parse(args);
      const task = await forgeRequest(ORCHESTRATOR_URL, "/v1/tasks", {
        method: "POST",
        body: input,
      });
      return JSON.stringify(task, null, 2);
    }

    case "forge_get_task": {
      const { task_id } = TaskIdInput.parse(args);
      const task = await forgeRequest(
        ORCHESTRATOR_URL,
        `/v1/tasks/${task_id}`
      );
      return JSON.stringify(task, null, 2);
    }

    case "forge_list_tasks": {
      const input = ListTasksInput.parse(args);
      const params = new URLSearchParams();
      if (input.agent_role) params.set("agent_role", input.agent_role);
      if (input.status) params.set("status", input.status);
      params.set("limit", String(input.limit));
      const tasks = await forgeRequest(
        ORCHESTRATOR_URL,
        `/v1/tasks?${params.toString()}`
      );
      return JSON.stringify(tasks, null, 2);
    }

    case "forge_approve_task": {
      const { task_id, comment } = ApproveInput.parse(args);
      const result = await forgeRequest(
        ORCHESTRATOR_URL,
        `/v1/tasks/${task_id}/approve`,
        { method: "POST", body: comment ? { comment } : undefined }
      );
      return JSON.stringify(result, null, 2);
    }

    case "forge_reject_task": {
      const { task_id, reason } = RejectInput.parse(args);
      const result = await forgeRequest(
        ORCHESTRATOR_URL,
        `/v1/tasks/${task_id}/reject`,
        { method: "POST", body: { reason } }
      );
      return JSON.stringify(result, null, 2);
    }

    case "forge_list_agents": {
      const agents = await forgeRequest(REGISTRY_URL, "/v1/agents");
      return JSON.stringify(agents, null, 2);
    }

    case "forge_list_skills": {
      const { role } = RoleFilterInput.parse(args);
      const path = role ? `/v1/skills?role=${role}` : "/v1/skills";
      const skills = await forgeRequest(REGISTRY_URL, path);
      return JSON.stringify(skills, null, 2);
    }

    case "forge_get_skill": {
      const { role, name } = SkillInput.parse(args);
      const skill = await forgeRequest(
        REGISTRY_URL,
        `/v1/skills/${role}/${name}`
      );
      return JSON.stringify(skill, null, 2);
    }

    case "forge_list_plans": {
      const plans = await forgeRequest(REGISTRY_URL, "/v1/plans");
      return JSON.stringify(plans, null, 2);
    }

    case "forge_bootstrap_project": {
      const input = BootstrapInput.parse(args);
      const task = await forgeRequest(ORCHESTRATOR_URL, "/v1/tasks", {
        method: "POST",
        body: {
          agent_role: "pm",
          skill_name: "project-bootstrap",
          title: `Bootstrap plan documents for ${input.repo}`,
          description: `Populate seeded .forge/ plan documents with content derived from the product brief. Delegate technical documents to the Architect agent.`,
          jira_ticket_id: input.ticket_id,
          repo: input.repo,
          input: {
            product_brief: input.product_brief,
            confluence_prd_url: input.confluence_prd_url,
            repo: input.repo,
          },
        },
      });
      return JSON.stringify(task, null, 2);
    }

    case "forge_get_plan_document": {
      const input = PlanDocumentInput.parse(args);
      const branch = input.branch ?? "main";
      const path = `.forge/${input.document}`;
      const doc = await forgeRequest(
        ORCHESTRATOR_URL,
        `/v1/files?repo=${encodeURIComponent(input.repo)}&path=${encodeURIComponent(path)}&ref=${encodeURIComponent(branch)}`
      );
      return JSON.stringify(doc, null, 2);
    }

    case "forge_bootstrap_platform": {
      const input = BootstrapPlatformInput.parse(args);
      // The PM agent detects platform mode from config.yaml — we just submit
      // the task pointing at any repo in the platform. The agent reads the
      // platform section and orchestrates across all sibling repos.
      const task = await forgeRequest(ORCHESTRATOR_URL, "/v1/tasks", {
        method: "POST",
        body: {
          agent_role: "pm",
          skill_name: "project-bootstrap",
          title: `Bootstrap platform plan documents (via ${input.repo})`,
          description:
            "Populate seeded .forge/ plan documents across all platform repos. " +
            "Design holistic architecture with shared API contracts and event schemas. " +
            "Delegate technical documents to the Architect agent.",
          jira_ticket_id: input.ticket_id,
          repo: input.repo,
          input: {
            product_brief: input.product_brief,
            confluence_prd_url: input.confluence_prd_url,
            repo: input.repo,
          },
        },
      });
      return JSON.stringify(task, null, 2);
    }

    case "forge_health": {
      const [orchestrator, registry] = await Promise.allSettled([
        forgeRequest(ORCHESTRATOR_URL, "/v1/health"),
        forgeRequest(REGISTRY_URL, "/v1/health"),
      ]);
      return JSON.stringify(
        {
          orchestrator:
            orchestrator.status === "fulfilled"
              ? orchestrator.value
              : { error: (orchestrator as PromiseRejectedResult).reason?.message },
          registry:
            registry.status === "fulfilled"
              ? registry.value
              : { error: (registry as PromiseRejectedResult).reason?.message },
        },
        null,
        2
      );
    }

    default:
      throw new Error(`Unknown tool: ${name}`);
  }
}

// ---------------------------------------------------------------------------
// MCP Server bootstrap
// ---------------------------------------------------------------------------

const server = new Server(
  {
    name: "forge-adp",
    version: "0.1.0",
  },
  {
    capabilities: {
      tools: {},
    },
  }
);

server.setRequestHandler(ListToolsRequestSchema, async () => ({
  tools: TOOLS,
}));

server.setRequestHandler(CallToolRequestSchema, async (request) => {
  const { name, arguments: args } = request.params;
  try {
    const result = await handleTool(name, (args ?? {}) as Record<string, unknown>);
    return {
      content: [{ type: "text", text: result }],
    };
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    return {
      content: [{ type: "text", text: `Error: ${message}` }],
      isError: true,
    };
  }
});

const transport = new StdioServerTransport();
await server.connect(transport);
