# Forge ADP — VS Code Extension

Submit tasks to Forge AI agents, track their progress, and approve checkpoints — all without leaving your editor.

## Features

### Agent Panel
A rich sidebar panel (secondary sidebar) that provides:

- **Task list** — live-updating cards showing all tasks with status badges
- **Inline approve / reject** — act on tasks awaiting approval without leaving VS Code
- **Submit form** — compose and submit new tasks to any agent role directly from the panel
- **Bootstrap tab** — seed and bootstrap a new project from a product brief file
- **PM Plan tab** — conduct a conversational project discovery interview with a Claude-powered PM agent that produces a structured product brief, then submits it to the orchestrator

### Status Bar Badge
A persistent status bar item shows the current state at a glance:

| State | Display |
|---|---|
| Tasks need attention | `🔔 Forge: N need attention` (warning color) |
| Tasks running | `⟳ Forge: N running` |
| Idle | `🤖 Forge` |

Click the badge to focus the Agent Panel.

### Command Palette Commands

| Command | Description |
|---|---|
| `Forge: Submit Task` | 4-step wizard to submit a task (role → title → description → ticket ID) |
| `Forge: Get Task Status` | Open a detail view for any task by ID |
| `Forge: List Tasks` | Refresh and focus the Agent Panel |
| `Forge: Approve Task` | Approve a task awaiting review (with optional comment) |
| `Forge: Reject Task` | Reject a task and feed back a reason to the agent |
| `Forge: Refresh Task List` | Force-refresh the task list |
| `Forge: Open Task in Browser` | Open a task's API endpoint in the browser |
| `Forge: Bootstrap Project` | Focus the Bootstrap tab in the Agent Panel |
| `Forge: Check Service Health` | Ping the Forge Orchestrator and display its status |

### Agent Roles

Tasks can be assigned to any of the following roles:

`backend-developer` · `frontend-developer` · `dba` · `devops` · `sre` · `secops` · `qa` · `pm`

## Requirements

- VS Code `^1.85.0`
- A running [Forge Orchestrator](../../cmd/orchestrator) service (default: `http://localhost:19080`)
- A running [Forge Registry](../../internal) service (default: `http://localhost:19081`)
- (Optional) [Forge Seeder](../seeder) binary for the "Seed project first" Bootstrap option
- (Optional) Anthropic API key for the PM Plan chat interview

## Installation

Install from the `.vsix` package:

```bash
code --install-extension forge-adp-0.1.1.vsix
```

Or use the VS Code **Extensions** sidebar → `...` menu → **Install from VSIX…**

## Configuration

All settings are under the `forge` namespace in VS Code settings (`Preferences: Open Settings`):

| Setting | Default | Description |
|---|---|---|
| `forge.orchestratorUrl` | `http://localhost:19080` | Base URL of the Forge Orchestrator service |
| `forge.registryUrl` | `http://localhost:19081` | Base URL of the Forge Registry service |
| `forge.apiToken` | *(empty)* | Bearer token for API authentication — leave blank for local dev |
| `forge.pollIntervalSeconds` | `15` | How often (seconds) to refresh the task list; minimum `5` |
| `forge.seederPath` | *(empty)* | Absolute path to the Forge seeder binary (required for Bootstrap seeding) |
| `forge.anthropicApiKey` | *(empty)* | Anthropic API key for the PM Plan chat interview |
| `forge.plannerModel` | `claude-opus-4-6` | Claude model used for the PM Plan interview |

## Development

```bash
# Install dependencies
npm install

# Compile TypeScript
npm run build

# Watch mode
npm run watch

# Lint
npm run lint
```

To test in VS Code, open the extension directory and press `F5` to launch an Extension Development Host.

To package a new `.vsix`:

```bash
npx @vscode/vsce package
```

## License

Apache-2.0 — see [LICENSE](LICENSE).
