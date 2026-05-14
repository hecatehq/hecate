// Capture documentation screenshots against a running gateway on :8765.
//
// Run via the bun script (resolves its own cwd, no `cd ui` needed):
//   bun run capture-screenshots          # from ui/
//   just screenshots                     # from repo root
//
// Prerequisites:
//   1. `just reset-dev && ./hecate &` — gateway running on
//      127.0.0.1:8765 with fresh state.
//   2. ollama running on :11434 with `ollama pull llama3.1:8b` (optional;
//      used only to seed one realistic trace row for the observability
//      screenshot). Set HECATE_SKIP_OLLAMA=1 to skip.
//
// Optional optimize pass — the script auto-detects the best PNG
// optimizer on PATH (preference: pngquant > oxipng > magick) and runs
// it over each captured PNG. None of these are required to take
// captures; the standard "people usually use this for README PNGs"
// install is `brew install oxipng`. Set HECATE_SKIP_OPTIMIZE=1 to skip.
//
// Outputs to docs/screenshots/<name>.png.

import { chromium, type Page, type Route } from "@playwright/test";
import { mkdirSync, statSync } from "node:fs";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";

const BASE_URL = process.env.HECATE_URL ?? "http://127.0.0.1:8765";
const HECATE_API = `${BASE_URL}/hecate/v1`;
const COMPAT_API = `${BASE_URL}/v1`;
const OUT_DIR = resolve(import.meta.dirname, "..", "docs", "screenshots");
mkdirSync(OUT_DIR, { recursive: true });

// 1280×800 is a comfortable docs-rendering size — wide enough to show
// the full sidebar + main pane with no horizontal scrolling, narrow
// enough that GitHub's README column doesn't have to downscale much.
const VIEWPORT = { width: 1280, height: 800 };

async function clearAndNavigate(page: Page, path = "/") {
  await page.context().clearCookies();
  await page.goto(BASE_URL);
  await page.evaluate(() => window.localStorage.clear());
  await page.goto(`${BASE_URL}${path}`);
  await page.waitForSelector(".hecate-activitybar", { timeout: 10_000 });
}

const captured: string[] = [];

async function snap(page: Page, name: string) {
  const path = resolve(OUT_DIR, `${name}.png`);
  await page.screenshot({ path, fullPage: false });
  captured.push(path);
  console.log(`  saved ${path}`);
}

async function openWorkspace(page: Page, id: "overview" | "runs" | "chats" | "providers" | "costs" | "settings") {
  await page.evaluate((workspace) => {
    window.localStorage.setItem("hecate.workspace", workspace);
  }, id);
  await page.reload();
  await page.waitForSelector(".hecate-activitybar", { timeout: 5_000 });
}

type PNGOptimizer = { name: string; args: (path: string) => string[]; lossy: boolean };

function detectOptimizer(): PNGOptimizer | null {
  const candidates: PNGOptimizer[] = [
    {
      name: "pngquant",
      args: path => ["--quality=80-100", "--speed", "1", "--strip", "--ext", ".png", "--force", path],
      lossy: true,
    },
    {
      name: "oxipng",
      args: path => ["-o", "max", "--strip", "safe", path],
      lossy: false,
    },
    {
      name: "magick",
      args: path => [path, "-strip", "-define", "png:compression-level=9", path],
      lossy: false,
    },
  ];
  for (const c of candidates) {
    const probe = spawnSync(c.name, ["--version"], { stdio: "ignore" });
    if (probe.status === 0 || probe.status === 1) return c;
  }
  return null;
}

async function optimize() {
  if (process.env.HECATE_SKIP_OPTIMIZE === "1") {
    console.log("→ skipping optimize (HECATE_SKIP_OPTIMIZE=1)");
    return;
  }
  const tool = detectOptimizer();
  if (!tool) {
    console.log("→ no PNG optimizer found on PATH (checked pngquant, oxipng, magick)");
    console.log("  install one for ~3-4× smaller files — recommended: `brew install pngquant`");
    return;
  }
  console.log(`→ optimizing PNGs (${tool.name}, ${tool.lossy ? "lossy palette" : "lossless"})`);
  const { spawn } = await import("node:child_process");
  await Promise.all(captured.map(path => new Promise<void>(resolve => {
    const before = statSync(path).size;
    const child = spawn(tool.name, tool.args(path), { stdio: ["ignore", "ignore", "pipe"] });
    let stderr = "";
    child.stderr?.on("data", chunk => { stderr += chunk.toString(); });
    child.on("close", code => {
      if (code !== 0) {
        console.warn(`  ${path.split("/").pop()}: ${tool.name} failed (${stderr.trim() || `exit ${code}`}); leaving original`);
        resolve();
        return;
      }
      const after = statSync(path).size;
      const delta = before - after;
      const pct = ((delta / before) * 100).toFixed(0);
      console.log(`  ${path.split("/").pop()}: ${(before / 1024).toFixed(1)} KB → ${(after / 1024).toFixed(1)} KB (-${pct}%)`);
      resolve();
    });
  })));
}

const jsonHeaders = { "Content-Type": "application/json" } as const;

function fulfillJSON(route: Route, data: unknown) {
  return route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify(data),
  });
}

const docsAgentChatSessionID = "agent-docs-session";
const docsApprovalID = "appr_docs_file_write";
const docsHecateChatSessionID = "chat-docs-hecate";
const docsTaskID = "task_docs_git_status";
const docsRunID = "run_docs_git_status";

function docsTimestamp(offsetMinutes = 0): string {
  return new Date(Date.now() + offsetMinutes * 60_000).toISOString();
}

const docsAgentAdapters = [
  {
    id: "codex",
    name: "Codex",
    kind: "acp",
    command: "codex-acp",
    managed: true,
    managed_package: "@zed-industries/codex-acp",
    available: true,
    status: "available",
    path: "/Users/alice/.cache/hecate/agent-adapters/codex-acp",
    cost_mode: "external",
    docs_url: "https://github.com/zed-industries/codex-acp",
    version: "0.12.0",
    supported_range: ">=0.1.0",
    auth_status: "ok",
  },
  {
    id: "claude_code",
    name: "Claude Code",
    kind: "acp",
    command: "claude-agent-acp",
    managed: true,
    managed_package: "@agentclientprotocol/claude-agent-acp",
    available: true,
    status: "available",
    path: "/Users/alice/.claude/local/claude",
    cost_mode: "external",
    docs_url: "https://github.com/agentclientprotocol/claude-agent-acp",
    version: "2.1.119",
    supported_range: ">=0.1.0",
    auth_status: "ok",
  },
  {
    id: "cursor_agent",
    name: "Cursor Agent",
    kind: "acp",
    command: "cursor-agent",
    available: true,
    status: "available",
    cost_mode: "external",
    docs_url: "https://docs.cursor.com/cli",
    version: "0.47.0",
    supported_range: ">=0.1.0",
    auth_status: "unauthenticated",
    auth_error: "Run cursor-agent login or set CURSOR_API_KEY.",
  },
];

function docsAgentApproval() {
  return {
    id: docsApprovalID,
    approval_id: docsApprovalID,
    session_id: docsAgentChatSessionID,
    adapter_id: "codex",
    workspace: "/Users/alice/dev/hecate",
    tool_kind: "file_write",
    tool_name: "Edit docs/runtime-api.md",
    status: "pending",
    acp_options: [
      { option_id: "allow_once", kind: "allow_once", name: "Allow once" },
      { option_id: "allow_always", kind: "allow_always", name: "Always allow this tool" },
      { option_id: "reject_once", kind: "reject_once", name: "Deny once" },
    ],
    scope_choices: ["once", "session", "workspace_tool", "adapter_tool"],
    created_at: docsTimestamp(-1),
    expires_at: docsTimestamp(4),
  };
}

function docsAgentSession() {
  return {
    id: docsAgentChatSessionID,
    title: "Review API docs update",
    runtime_kind: "external_agent",
    adapter_id: "codex",
    driver_kind: "acp",
    native_session_id: "acp_doc_42",
    workspace: "/Users/alice/dev/hecate",
    workspace_branch: "feature/approval-docs",
    status: "awaiting_approval",
    turns_used: 2,
    max_turns_per_session: 20,
    session_started_at: docsTimestamp(-12),
    max_session_duration_ms: 3_600_000,
    idle_timeout_ms: 900_000,
    created_at: docsTimestamp(-12),
    updated_at: docsTimestamp(-1),
    messages: [
      {
        id: "agent-docs-user-1",
        role: "user",
        content: "Update the runtime API docs with the new approval endpoints.",
        created_at: docsTimestamp(-6),
      },
      {
        id: "agent-docs-assistant-1",
        role: "assistant",
        content: "I found the runtime API section and prepared a small docs patch. Hecate needs your approval before the adapter writes the file.",
        adapter_id: "codex",
        adapter_name: "Codex",
        driver_kind: "acp",
        native_session_id: "acp_doc_42",
        status: "awaiting_approval",
        cost_mode: "external",
        workspace: "/Users/alice/dev/hecate",
        run_id: "agent_run_docs",
        trace_id: "7c5a7e1f8a6d4b31",
        duration_ms: 12_480,
        diff_stat: "docs/runtime-api.md | 18 +++++++++++++-----\n1 file changed, 13 insertions(+), 5 deletions(-)",
        diff: "diff --git a/docs/runtime-api.md b/docs/runtime-api.md\nindex 1a2b3c4..5d6e7f8 100644\n--- a/docs/runtime-api.md\n+++ b/docs/runtime-api.md\n@@ -10,6 +10,9 @@\n+External-agent approvals are visible on the agent-chat stream.\n",
        activities: [
          { id: "plan-1", type: "plan", status: "completed", title: "Inspect runtime API docs", created_at: docsTimestamp(-5) },
          { id: "tool-1", type: "tool_call", status: "completed", kind: "read_file", title: "Read docs/runtime-api.md", created_at: docsTimestamp(-4) },
          { id: "approval-1", type: "approval", status: "running", kind: "file_write", title: "Waiting for file_write approval", created_at: docsTimestamp(-1) },
        ],
        usage: {
          context_size: 200_000,
          context_used: 31_420,
          reported_cost_amount: "0.04",
          reported_cost_currency: "USD",
        },
        raw_output: "request_permission file_write docs/runtime-api.md\nwaiting for operator approval",
        created_at: docsTimestamp(-5),
        started_at: docsTimestamp(-5),
      },
    ],
  };
}

function docsHecateChatSession() {
  const taskID = "task_4fd22b7c9a1d";
  const runID = "run_f7a8c1910b2e";
  const requestID = "2f4574249a1b4e3f";
  const createdAt = docsTimestamp(-9);
  const updatedAt = docsTimestamp(-1);
  return {
    id: docsHecateChatSessionID,
    title: "Review recent changes",
    runtime_kind: "agent",
    provider: "ollama",
    model: "ministral-3:latest",
    capabilities: {
      tool_calling: "basic",
      streaming: true,
      max_context_tokens: 128_000,
      source: "operator_override",
    },
    task_id: taskID,
    latest_run_id: runID,
    workspace: "/Users/alice/dev/hecate",
    workspace_branch: "feature/local-agent",
    status: "completed",
    created_at: createdAt,
    updated_at: updatedAt,
    segments: [
      {
        id: "model:intro",
        runtime_kind: "model",
        provider: "ollama",
        model: "ministral-3:latest",
        status: "completed",
        message_count: 2,
        started_at: docsTimestamp(-9),
        updated_at: docsTimestamp(-8),
      },
      {
        id: `task:${taskID}`,
        runtime_kind: "agent",
        provider: "ollama",
        model: "ministral-3:latest",
        task_id: taskID,
        latest_run_id: runID,
        workspace: "/Users/alice/dev/hecate",
        status: "completed",
        message_count: 2,
        started_at: docsTimestamp(-5),
        updated_at: updatedAt,
      },
    ],
    messages: [
      {
        id: "hecate-docs-user-1",
        runtime_kind: "model",
        segment_id: "model:intro",
        role: "user",
        content: "summarize what changed today",
        provider: "ollama",
        model: "ministral-3:latest",
        created_at: docsTimestamp(-9),
      },
      {
        id: "hecate-docs-assistant-1",
        runtime_kind: "model",
        segment_id: "model:intro",
        role: "assistant",
        content: "Today focused on polishing Hecate Chat: clearer task links, trace navigation, and smoother task-backed turns.",
        provider: "ollama",
        model: "ministral-3:latest",
        status: "completed",
        request_id: "8b2d6f42c1a0",
        trace_id: "8b2d6f42c1a0d4ac8b7b0",
        duration_ms: 6_200,
        usage: {
          context_size: 128_000,
          context_used: 11_840,
          reported_cost_amount: "0.00",
          reported_cost_currency: "USD",
        },
        created_at: docsTimestamp(-8),
        completed_at: docsTimestamp(-8),
      },
      {
        id: "hecate-docs-user-2",
        runtime_kind: "agent",
        segment_id: `task:${taskID}`,
        task_id: taskID,
        role: "user",
        content: "show last 3 commits",
        provider: "ollama",
        model: "ministral-3:latest",
        workspace: "/Users/alice/dev/hecate",
        created_at: docsTimestamp(-5),
      },
      {
        id: "hecate-docs-assistant-2",
        runtime_kind: "agent",
        segment_id: `task:${taskID}`,
        task_id: taskID,
        run_id: runID,
        request_id: requestID,
        trace_id: `${requestID}d2a88c64e56b`,
        role: "assistant",
        content: "Here are the last 3 commits in the Hecate repository:\n\n- `c3c1e9a` fix(ui): compact chat header identifiers\n- `0fcbc52` fix(ui): stabilize busy chat e2e selectors\n- `f6572e5` fix(runtime): avoid overflowing slice capacity calculations\n\nThe branch is clean after those changes.",
        provider: "ollama",
        model: "ministral-3:latest",
        capabilities: {
          tool_calling: "basic",
          streaming: true,
          max_context_tokens: 128_000,
          source: "operator_override",
        },
        workspace: "/Users/alice/dev/hecate",
        status: "completed",
        duration_ms: 25_400,
        activities: [
          {
            id: "hecate-docs-activity-tool",
            type: "tool_call",
            status: "completed",
            kind: "git_exec",
            title: "git log --oneline -3",
            detail: "3 commits returned",
            created_at: docsTimestamp(-4),
          },
          {
            id: "hecate-docs-activity-task",
            type: "task_run",
            status: "completed",
            title: "Backing task",
            detail: `${taskID} · ${runID}`,
            terminal: true,
            created_at: docsTimestamp(-1),
          },
          {
            id: "hecate-docs-activity-model",
            type: "model_turn",
            status: "completed",
            title: "Model turns",
            detail: "2 turns completed",
            terminal: true,
            created_at: docsTimestamp(-1),
          },
        ],
        raw_output: "c3c1e9a fix(ui): compact chat header identifiers\n0fcbc52 fix(ui): stabilize busy chat e2e selectors\nf6572e5 fix(runtime): avoid overflowing slice capacity calculations",
        usage: {
          context_size: 128_000,
          context_used: 18_320,
          reported_cost_amount: "0.00",
          reported_cost_currency: "USD",
        },
        timing: {
          total_ms: 25_400,
          model_ms: 18_600,
          tool_ms: 1_120,
          overhead_ms: 5_680,
          turn_count: 2,
          tool_count: 1,
          bottleneck: "model",
          bottleneck_ms: 18_600,
        },
        created_at: docsTimestamp(-4),
        started_at: docsTimestamp(-4),
        completed_at: docsTimestamp(-1),
      },
    ],
  };
}

async function routeAgentDocsFixtures(page: Page) {
  const fulfillJSON = (route: Route, data: unknown) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(data),
    });

  await page.route(`${HECATE_API}/agent-adapters`, (route) => {
    fulfillJSON(route, { object: "agent_adapters", data: docsAgentAdapters });
  });
  await page.route(`${HECATE_API}/agent-chat/sessions`, (route) => {
    const session = docsAgentSession();
    fulfillJSON(route, {
      object: "agent_chat_sessions",
      data: [{
        id: session.id,
        title: session.title,
        runtime_kind: session.runtime_kind,
        adapter_id: session.adapter_id,
        driver_kind: session.driver_kind,
        native_session_id: session.native_session_id,
        workspace: session.workspace,
        workspace_branch: session.workspace_branch,
        status: session.status,
        message_count: session.messages.length,
        created_at: session.created_at,
        updated_at: session.updated_at,
      }],
    });
  });
  await page.route(`${HECATE_API}/agent-chat/sessions/${docsAgentChatSessionID}`, (route) => {
    fulfillJSON(route, { object: "agent_chat_session", data: docsAgentSession() });
  });
  await page.route(`${HECATE_API}/agent-chat/sessions/${docsAgentChatSessionID}/approvals?status=pending`, (route) => {
    fulfillJSON(route, { object: "agent_chat_approvals", data: [docsAgentApproval()] });
  });
  await page.route(`${HECATE_API}/agent-chat/sessions/${docsAgentChatSessionID}/approvals/${docsApprovalID}`, (route) => {
    fulfillJSON(route, { object: "agent_chat_approval", data: docsAgentApproval() });
  });
  await page.route(`${HECATE_API}/agent-chat/grants`, (route) => {
    fulfillJSON(route, {
      object: "agent_chat_grants",
      data: [
        {
          id: "grant_docs_session",
          scope: "workspace_tool",
          adapter_id: "codex",
          tool_kind: "read_file",
          workspace: "/Users/alice/dev/hecate",
          decision: "approve",
          granted_by: "operator",
          granted_at: docsTimestamp(-35),
        },
        {
          id: "grant_docs_adapter",
          scope: "adapter_tool",
          adapter_id: "claude_code",
          tool_kind: "read_file",
          decision: "approve",
          granted_by: "operator",
          granted_at: docsTimestamp(-120),
        },
      ],
    });
  });
  await page.route(`${HECATE_API}/system/stats`, (route) => {
    fulfillJSON(route, {
      object: "runtime_stats",
      data: {
        uptime_seconds: 120,
        requests_total: 32,
        awaiting_approval_runs: 1,
        agent_adapter_approval_mode: "prompt",
      },
    });
  });
}

async function routeHecateChatDocsFixture(page: Page) {
  const fulfillJSON = (route: Route, data: unknown) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(data),
    });

  const session = docsHecateChatSession();
  await page.route(`${COMPAT_API}/models`, (route) => {
    fulfillJSON(route, {
      object: "list",
      data: [
        {
          id: "ministral-3:latest",
          owned_by: "ollama",
          metadata: {
            provider: "ollama",
            provider_kind: "local",
            default: true,
            discovery_source: "provider",
            capabilities: {
              tool_calling: "basic",
              streaming: true,
              max_context_tokens: 128_000,
              source: "operator_override",
            },
          },
        },
        {
          id: "smollm2:135m",
          owned_by: "ollama",
          metadata: {
            provider: "ollama",
            provider_kind: "local",
            discovery_source: "provider",
            capabilities: { tool_calling: "unknown", streaming: true, source: "provider" },
          },
        },
      ],
    });
  });
  await page.route(`${HECATE_API}/settings`, (route) => {
    fulfillJSON(route, {
      object: "settings",
      data: {
        backend: "memory",
        providers: [
          {
            id: "ollama",
            name: "Ollama",
            preset_id: "ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            default_model: "ministral-3:latest",
            credential_configured: true,
          },
        ],
        policy_rules: [],
        events: [],
      },
    });
  });
  await page.route(`${HECATE_API}/providers/status`, (route) => {
    fulfillJSON(route, {
      object: "providers",
      data: [
        {
          name: "ollama",
          kind: "local",
          base_url: "http://127.0.0.1:11434/v1",
          credential_state: "not_required",
          credential_ready: true,
          healthy: true,
          status: "ok",
          routing_ready: true,
          default_model: "ministral-3:latest",
          models: ["ministral-3:latest", "smollm2:135m"],
          model_count: 2,
          discovery_source: "provider",
        },
      ],
    });
  });
  await page.route(`${HECATE_API}/agent-chat/sessions`, (route) => {
    fulfillJSON(route, {
      object: "agent_chat_sessions",
      data: [{
        id: session.id,
        title: session.title,
        runtime_kind: session.runtime_kind,
        task_id: session.task_id,
        latest_run_id: session.latest_run_id,
        provider: session.provider,
        model: session.model,
        capabilities: session.capabilities,
        workspace: session.workspace,
        workspace_branch: session.workspace_branch,
        status: session.status,
        message_count: session.messages.length,
        created_at: session.created_at,
        updated_at: session.updated_at,
      }],
    });
  });
  await page.route(`${HECATE_API}/agent-chat/sessions/${docsHecateChatSessionID}`, (route) => {
    fulfillJSON(route, { object: "agent_chat_session", data: session });
  });
  await page.route(`${HECATE_API}/agent-chat/sessions/${docsHecateChatSessionID}/approvals?status=pending`, (route) => {
    fulfillJSON(route, { object: "agent_chat_approvals", data: [] });
  });
}

async function unrouteHecateChatDocsFixture(page: Page) {
  await page.unroute(`${COMPAT_API}/models`);
  await page.unroute(`${HECATE_API}/settings`);
  await page.unroute(`${HECATE_API}/providers/status`);
  await page.unroute(`${HECATE_API}/agent-chat/sessions`);
  await page.unroute(`${HECATE_API}/agent-chat/sessions/${docsHecateChatSessionID}`);
  await page.unroute(`${HECATE_API}/agent-chat/sessions/${docsHecateChatSessionID}/approvals?status=pending`);
}

async function routeTaskDiagnosticsDocsFixture(page: Page) {
  const task = {
    id: docsTaskID,
    title: "Inspect Git status",
    prompt: "show current git status",
    execution_kind: "agent_loop",
    execution_profile: "chat_hecate_agent",
    origin_kind: "agent_chat",
    origin_id: docsHecateChatSessionID,
    requested_provider: "ollama",
    requested_model: "ministral-3:latest",
    latest_provider: "ollama",
    latest_model: "ministral-3:latest",
    latest_run_id: docsRunID,
    status: "failed",
    step_count: 3,
    artifact_count: 3,
    last_error: "git_exec failed: not a git repository",
    created_at: docsTimestamp(-12),
    updated_at: docsTimestamp(-3),
    started_at: docsTimestamp(-12),
    finished_at: docsTimestamp(-3),
    latest_trace_id: "23a16de3c9014ad29c9f",
    latest_request_id: "req_docs_git_status",
  };
  const run = {
    id: docsRunID,
    task_id: docsTaskID,
    number: 1,
    status: "failed",
    orchestrator: "agent_loop",
    provider: "ollama",
    provider_kind: "local",
    model: "ministral-3:latest",
    step_count: 3,
    approval_count: 0,
    artifact_count: 3,
    total_cost_micros_usd: 0,
    last_error: "git_exec failed: not a git repository",
    started_at: docsTimestamp(-12),
    finished_at: docsTimestamp(-3),
    request_id: "req_docs_git_status",
    trace_id: "23a16de3c9014ad29c9f",
  };
  const steps = [
    {
      id: "step_docs_model",
      task_id: docsTaskID,
      run_id: docsRunID,
      index: 1,
      kind: "builtin.agent_loop_llm",
      title: "Agent turn 1",
      status: "completed",
      started_at: docsTimestamp(-12),
      finished_at: docsTimestamp(-11),
    },
    {
      id: "step_docs_git",
      task_id: docsTaskID,
      run_id: docsRunID,
      index: 2,
      kind: "git_exec",
      title: "git status --short",
      status: "failed",
      tool_name: "git_exec",
      exit_code: 128,
      error: "fatal: not a git repository",
      output_summary: { command: "git status --short", exit_code: 128, stdout_bytes: 0, stderr_bytes: 27 },
      started_at: docsTimestamp(-10),
      finished_at: docsTimestamp(-10),
    },
    {
      id: "step_docs_failed",
      task_id: docsTaskID,
      run_id: docsRunID,
      index: 3,
      kind: "builtin.agent_loop",
      title: "Agent loop failed",
      status: "failed",
      error: "git_exec failed: not a git repository",
      started_at: docsTimestamp(-10),
      finished_at: docsTimestamp(-3),
    },
  ];
  const artifacts = [
    {
      id: "art_docs_conversation",
      task_id: docsTaskID,
      run_id: docsRunID,
      kind: "agent_conversation",
      name: "agent-conversation.json",
      status: "ready",
      size_bytes: 1_204,
      created_at: docsTimestamp(-3),
    },
    {
      id: "art_docs_stdout",
      task_id: docsTaskID,
      run_id: docsRunID,
      step_id: "step_docs_git",
      kind: "stdout",
      name: "git-stdout.txt",
      status: "ready",
      size_bytes: 0,
      content_text: "",
      created_at: docsTimestamp(-10),
    },
    {
      id: "art_docs_stderr",
      task_id: docsTaskID,
      run_id: docsRunID,
      step_id: "step_docs_git",
      kind: "stderr",
      name: "git-stderr.txt",
      status: "ready",
      size_bytes: 27,
      content_text: "fatal: not a git repository\n",
      created_at: docsTimestamp(-10),
    },
  ];
  const activity = [
    {
      id: "activity_docs_model",
      type: "model_turn",
      status: "completed",
      title: "Thinking",
      step_id: "step_docs_model",
      kind: "builtin.agent_loop_llm",
      summary: { turns: 1 },
      occurred_at: docsTimestamp(-11),
    },
    {
      id: "activity_docs_tool",
      type: "tool_call",
      status: "failed",
      title: "git_exec",
      step_id: "step_docs_git",
      tool_name: "git_exec",
      kind: "git_exec",
      summary: { command: "git status --short", exit_code: 128, stdout_bytes: 0, stderr_bytes: 27 },
      occurred_at: docsTimestamp(-10),
    },
    {
      id: "activity_docs_stderr",
      type: "artifact",
      status: "ready",
      title: "git-stderr.txt",
      step_id: "step_docs_git",
      artifact_id: "art_docs_stderr",
      kind: "stderr",
      summary: { size_bytes: 27, content_preview: "fatal: not a git repository\n" },
      occurred_at: docsTimestamp(-10),
    },
    {
      id: "activity_docs_stdout",
      type: "artifact",
      status: "ready",
      title: "git-stdout.txt",
      step_id: "step_docs_git",
      artifact_id: "art_docs_stdout",
      kind: "stdout",
      summary: { size_bytes: 0, content_preview: "" },
      occurred_at: docsTimestamp(-10),
    },
    {
      id: "activity_docs_failed",
      type: "run_state",
      status: "failed",
      title: "Failed",
      terminal: true,
      summary: { error: "git_exec failed: not a git repository" },
      occurred_at: docsTimestamp(-3),
    },
  ];
  const events = [
    { schema_version: "1", event_id: "evt_docs_1", task_id: docsTaskID, run_id: docsRunID, sequence: 1, occurred_at: docsTimestamp(-12), type: "run.created", data: {} },
    { schema_version: "1", event_id: "evt_docs_2", task_id: docsTaskID, run_id: docsRunID, sequence: 2, occurred_at: docsTimestamp(-12), type: "run.started", data: {} },
    { schema_version: "1", event_id: "evt_docs_3", task_id: docsTaskID, run_id: docsRunID, sequence: 3, occurred_at: docsTimestamp(-10), type: "tool.failed", data: { tool: "git_exec" } },
    { schema_version: "1", event_id: "evt_docs_4", task_id: docsTaskID, run_id: docsRunID, sequence: 4, occurred_at: docsTimestamp(-3), type: "run.failed", data: { error: "git_exec failed: not a git repository" } },
  ];
  const snapshot = {
    object: "task_run_event",
    data: {
      sequence: 4,
      terminal: true,
      event_type: "snapshot",
      run,
      steps,
      approvals: [],
      artifacts,
      activity,
    },
  };
  const fulfillJSON = (route: Route, data: unknown) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(data) });

  await page.route(`${HECATE_API}/tasks?limit=30`, route => fulfillJSON(route, { object: "tasks", data: [task] }));
  await page.route(`${HECATE_API}/tasks/${docsTaskID}/runs`, route => fulfillJSON(route, { object: "task_runs", data: [run] }));
  await page.route(`${HECATE_API}/tasks/${docsTaskID}/approvals`, route => fulfillJSON(route, { object: "task_approvals", data: [] }));
  await page.route(`${HECATE_API}/tasks/${docsTaskID}/runs/${docsRunID}/steps`, route => fulfillJSON(route, { object: "task_steps", data: steps }));
  await page.route(`${HECATE_API}/tasks/${docsTaskID}/runs/${docsRunID}/artifacts`, route => fulfillJSON(route, { object: "task_artifacts", data: artifacts }));
  await page.route(`${HECATE_API}/tasks/${docsTaskID}/runs/${docsRunID}/events?after_sequence=0`, route => fulfillJSON(route, { object: "task_run_events", data: events }));
  await page.route(`${HECATE_API}/tasks/${docsTaskID}/runs/${docsRunID}/stream?after_sequence=0`, route => route.fulfill({
    status: 200,
    contentType: "text/event-stream",
    body: `event: snapshot\ndata: ${JSON.stringify(snapshot)}\n\n`,
  }));
}

async function unrouteTaskDiagnosticsDocsFixture(page: Page) {
  await page.unroute(`${HECATE_API}/tasks?limit=30`);
  await page.unroute(`${HECATE_API}/tasks/${docsTaskID}/runs`);
  await page.unroute(`${HECATE_API}/tasks/${docsTaskID}/approvals`);
  await page.unroute(`${HECATE_API}/tasks/${docsTaskID}/runs/${docsRunID}/steps`);
  await page.unroute(`${HECATE_API}/tasks/${docsTaskID}/runs/${docsRunID}/artifacts`);
  await page.unroute(`${HECATE_API}/tasks/${docsTaskID}/runs/${docsRunID}/events?after_sequence=0`);
  await page.unroute(`${HECATE_API}/tasks/${docsTaskID}/runs/${docsRunID}/stream?after_sequence=0`);
}

async function routeLocalProviderDiscoveryDocsFixture(page: Page) {
  await page.route(`${HECATE_API}/settings/providers/local-discovery`, route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({
      object: "local_provider_discovery",
      data: [
        {
          preset_id: "ollama",
          name: "Ollama",
          base_url: "http://127.0.0.1:11434/v1",
          probe_url: "http://127.0.0.1:11434/api/tags",
          status: "running",
          command: "ollama",
          command_available: true,
          command_path: "/opt/homebrew/bin/ollama",
          http_available: true,
          model_count: 1,
          models: ["llama3.1:8b"],
        },
        {
          preset_id: "lmstudio",
          name: "LM Studio",
          base_url: "http://127.0.0.1:1234/v1",
          probe_url: "http://127.0.0.1:1234/v1/models",
          status: "installed",
          command: "lms",
          command_available: true,
          command_path: "/Users/alice/.lmstudio/bin/lms",
          http_available: false,
          model_count: 0,
          models: [],
          error: "connection refused",
        },
      ],
    }),
  }));
}

// addProvider creates a provider via the same POST endpoint the UI's
// add modal calls. Mirrors the new explicit-add lifecycle: each
// provider is materialized in the settings store, no auto-discovery.
async function addProvider(params: {
  name: string;
  preset_id?: string;
  kind: "cloud" | "local";
  protocol?: string;
  base_url?: string;
  api_key?: string;
}) {
  const body = {
    name: params.name,
    preset_id: params.preset_id,
    kind: params.kind,
    protocol: params.protocol ?? "openai",
    base_url: params.base_url,
    api_key: params.api_key,
  };
  const res = await fetch(`${HECATE_API}/settings/providers`, {
    method: "POST",
    headers: jsonHeaders,
    body: JSON.stringify(body),
  });
  if (!res.ok && res.status !== 409) {
    const text = await res.text();
    console.warn(`  add provider ${params.name} failed: ${res.status} ${text.slice(0, 200)}`);
    return;
  }
  if (res.status === 409) {
    console.log(`  ${params.name} already exists (409) — skipping`);
    return;
  }
  console.log(`  added provider ${params.name} (${params.kind})`);
}

// seedChatSessions creates a few direct model sessions through Hecate's
// API. The first session optionally gets a real completion so the
// observability screenshot has a trace row to open; the main Chats
// screenshot below is fixture-backed so it stays stable without Ollama.
async function seedChatSessions() {
  const titles = [
    "Go interfaces vs structs",
    "Postgres logical replication",
    "Sort TS array without mutating",
  ];
  const ids: string[] = [];
  for (const title of titles) {
    const res = await fetch(`${HECATE_API}/chat/sessions`, {
      method: "POST",
      headers: jsonHeaders,
      body: JSON.stringify({ title }),
    });
    const json = (await res.json()) as { data: { id: string } };
    ids.push(json.data.id);
    console.log(`  seeded session ${json.data.id} — ${title}`);
  }

  const firstID = ids[0];
  if (process.env.HECATE_SKIP_OLLAMA === "1") {
    console.log("  HECATE_SKIP_OLLAMA=1 — leaving the chat session empty");
    return { firstID };
  }
  console.log(`  routing one chat through ollama/llama3.1:8b for ${firstID}…`);
  const start = Date.now();
  try {
    const chatRes = await fetch(`${COMPAT_API}/chat/completions`, {
      method: "POST",
      headers: jsonHeaders,
      body: JSON.stringify({
        model: "llama3.1:8b",
        provider: "ollama",
        session_id: firstID,
        messages: [{
          role: "user",
          content: "In two sentences: when do you reach for a Go interface vs a struct?",
        }],
      }),
    });
    if (!chatRes.ok) {
      const body = await chatRes.text();
      console.warn(`  chat seed skipped: ${chatRes.status} ${body.slice(0, 200)}`);
      console.warn("  (observability will have no seeded model trace)");
      return { firstID };
    }
    console.log(`  llama replied in ${((Date.now() - start) / 1000).toFixed(1)}s`);
  } catch (err) {
    console.warn(`  chat seed skipped: ${(err as Error).message}`);
    console.warn("  (observability will have no seeded model trace)");
  }
  return { firstID };
}

async function main() {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: VIEWPORT, deviceScaleFactor: 1 });
  const page = await context.newPage();

  // ── 1. First-run Chats onboarding ──────────────────────────────────────────
  // No providers are configured yet. Keep this shot deterministic by mocking
  // external-agent availability and local-provider discovery: it should show
  // the one-click local setup path, not the capture machine's real state.
  console.log("→ chat-empty (first-run one-click local setup)");
  const missingAgentAdapters = `${HECATE_API}/agent-adapters`;
  await page.route(missingAgentAdapters, route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({
      object: "agent_adapters",
      data: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp",
          available: false,
          status: "missing",
          error: "codex was not found on PATH",
          cost_mode: "external",
        },
        {
          id: "claude_code",
          name: "Claude Code",
          kind: "acp",
          command: "claude-agent-acp",
          available: false,
          status: "missing",
          error: "claude was not found on PATH",
          cost_mode: "external",
        },
        {
          id: "cursor_agent",
          name: "Cursor Agent",
          kind: "acp",
          command: "cursor-agent",
          available: false,
          status: "missing",
          error: "cursor-agent was not found on PATH",
          cost_mode: "external",
        },
      ],
    }),
  }));
  await routeLocalProviderDiscoveryDocsFixture(page);
  await clearAndNavigate(page);
  await page.evaluate(() => {
    window.localStorage.setItem("hecate.workspace", "chats");
    window.localStorage.setItem("hecate.chatTarget", "model");
  });
  await page.reload();
  await openWorkspace(page, "chats");
  await page.waitForSelector("text=Detected locally", { timeout: 5_000 });
  await page.waitForSelector("text=Add selected", { timeout: 5_000 });
  await snap(page, "chat-empty");
  await page.unroute(missingAgentAdapters);
  await page.unroute(`${HECATE_API}/settings/providers/local-discovery`);

  // ── 2. Empty Connections provider list ──────────────────────────────────────
  // The UI loads directly — no auth gate. Land on the Connections workspace
  // before any providers exist.
  console.log("→ providers-empty");
  await openWorkspace(page, "providers");
  await page.waitForSelector("text=No model providers configured", { timeout: 5_000 });
  await snap(page, "providers-empty");

  // ── 3. Local presets in the Add modal ───────────────────────────────────────
  console.log("→ providers-presets (Add modal, Local tab)");
  await routeLocalProviderDiscoveryDocsFixture(page);
  await page.getByRole("button", { name: "Add provider" }).first().click();
  await page.waitForSelector("text=Ollama", { timeout: 5_000 });
  await page.waitForSelector("text=Running", { timeout: 5_000 });
  await page.waitForTimeout(300);
  await snap(page, "providers-presets");
  await page.keyboard.press("Escape");
  await page.unroute(`${HECATE_API}/settings/providers/local-discovery`);
  await page.waitForTimeout(300);

  // ── 4. Seed three providers via the API ─────────────────────────────────────
  // These mirror the UI's add flow: one cloud (OpenAI with a fake key), two
  // local (Ollama, LM Studio) on their default ports. The fake OpenAI key is
  // enough to pass the create handler's "cloud-needs-key" guard; an actual
  // round-trip to OpenAI isn't in the screenshot.
  console.log("→ seeding providers");
  await addProvider({ name: "Ollama",   preset_id: "ollama",   kind: "local" });
  await addProvider({ name: "LM Studio", preset_id: "lmstudio", kind: "local" });
  await addProvider({ name: "OpenAI",   preset_id: "openai",   kind: "cloud",
    api_key: "sk-live-redacted-for-screenshots" });

  // ── 5. Populated providers table ────────────────────────────────────────────
  console.log("→ providers (populated table)");
  await page.reload();
  await page.waitForSelector("text=Cloud providers", { timeout: 5_000 });
  await page.waitForTimeout(2_000);
  await snap(page, "providers");

  // ── 6. Hecate Chat transcript ──────────────────────────────────────────────
  // The README's primary chat screenshot should document the current
  // product shape: one transcript with tools-off model turns and tools-on
  // task-backed Hecate Agent turns. Keep it fixture-backed so the shot
  // doesn't depend on whichever local model happens to be installed.
  console.log("→ seeding chat sessions for observability");
  const { firstID } = await seedChatSessions();

  console.log("→ chat (Hecate Chat, tools on/off transcript)");
  await routeHecateChatDocsFixture(page);
  await clearAndNavigate(page);
  await page.evaluate((sessionID) => {
    window.localStorage.setItem("hecate.workspace", "chats");
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.chatTargetBySessionID", JSON.stringify({ [sessionID]: "agent" }));
    window.localStorage.setItem("hecate.agentChatSessionID", sessionID);
    window.localStorage.setItem("hecate.providerFilter", "ollama");
    window.localStorage.setItem("hecate.model", "ministral-3:latest");
    window.localStorage.setItem("hecate.agentWorkspace", "/Users/alice/dev/hecate");
  }, docsHecateChatSessionID);
  await page.reload();
  await page.waitForSelector(".hecate-activitybar", { timeout: 10_000 });
  await openWorkspace(page, "chats");
  await page.waitForSelector("text=Here are the last 3 commits", { timeout: 5_000 });
  await page.waitForSelector("text=tools:", { timeout: 5_000 });
  await page.waitForSelector("text=on", { timeout: 5_000 });
  await page.waitForTimeout(700);
  await snap(page, "chat");
  await unrouteHecateChatDocsFixture(page);

  // ── 7. Tasks ────────────────────────────────────────────────────────────────
  console.log("→ tasks (failed tool diagnostics fixture)");
  await routeTaskDiagnosticsDocsFixture(page);
  await page.reload();
  await page.waitForSelector(".hecate-activitybar", { timeout: 5_000 });
  await openWorkspace(page, "runs");
  await page.waitForSelector("text=git_exec", { timeout: 5_000 });
  await page.locator("details").evaluateAll(nodes => {
    for (const node of nodes) {
      const text = node.parentElement?.textContent ?? "";
      (node as HTMLDetailsElement).open = text.includes("Ran git") || text.includes("git_exec");
    }
  });
  await page.locator("pre", { hasText: "fatal: not a git repository" }).last().waitFor({ state: "visible", timeout: 5_000 });
  await page.waitForTimeout(700);
  await snap(page, "tasks");
  await unrouteTaskDiagnosticsDocsFixture(page);

  // ── 8. Observability — pick a trace first ───────────────────────────────────
  console.log("→ observe (trace selected)");
  await openWorkspace(page, "overview");
  await page.waitForTimeout(800);
  try {
    const firstRow = page.locator("[data-trace-row], tbody tr").first();
    if (await firstRow.count() > 0 && await firstRow.isVisible()) {
      await firstRow.click({ timeout: 2_000 });
      await page.waitForTimeout(800);
    } else {
      console.warn("  no trace rows found — taking the empty-list shot");
    }
  } catch (err) {
    console.warn(`  trace click skipped: ${(err as Error).message}`);
  }
  await snap(page, "observe");

  // ── 9. Usage workspace ─────────────────────────────────────────────
  console.log("→ usage");
  await page.route(`${HECATE_API}/usage/summary`, route => fulfillJSON(route, {
    object: "usage_summary",
    data: {
      key: "global",
      scope: "global",
      used_micros_usd: 1600,
      used_usd: "$0.001600",
    },
  }));
  await page.route(`${HECATE_API}/usage/events?limit=20`, route => fulfillJSON(route, {
    object: "usage_events",
    data: [
      {
        key: "global:provider:openai",
        type: "usage",
        scope: "provider",
        provider: "openai",
        model: "gpt-5.4-mini",
        request_id: "req_docs_usage_1",
        amount_micros_usd: 1600,
        amount_usd: "$0.001600",
        prompt_tokens: 920,
        completion_tokens: 280,
        total_tokens: 1200,
        timestamp: docsTimestamp(-2),
      },
      {
        key: "global:provider:ollama",
        type: "usage",
        scope: "provider",
        provider: "ollama",
        model: "ministral-3:latest",
        request_id: "req_docs_usage_local",
        amount_micros_usd: 0,
        amount_usd: "$0.000000",
        prompt_tokens: 400,
        completion_tokens: 100,
        total_tokens: 500,
        timestamp: docsTimestamp(-1),
      },
    ],
  }));
  await openWorkspace(page, "costs");
  await page.waitForTimeout(500);
  await snap(page, "costs");

  // ── 10. Settings — Retention ───────────────────────────────────────
  console.log("→ settings / retention");
  await openWorkspace(page, "settings");
  await page.waitForTimeout(500);
  await page.waitForTimeout(500);
  await snap(page, "settings-retention");

  // ── 11. New external-agent surfaces ────────────────────────────────────────
  // Mock these endpoints so the documentation shots stay deterministic:
  // screenshots should show the intended UI shape, not whatever agent CLIs
  // and auth state happen to exist on the capture machine.
  console.log("→ connections / external agents");
  await routeAgentDocsFixtures(page);
  await clearAndNavigate(page);
  await openWorkspace(page, "providers");
  await page.waitForSelector("text=External agent grants", { timeout: 5_000 });
  await page.waitForTimeout(700);
  await snap(page, "connections-external-agents");

  console.log("→ chat / pending agent approval");
  await page.evaluate((sessionID) => {
    window.localStorage.setItem("hecate.workspace", "chats");
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.chatTargetBySessionID", JSON.stringify({ [sessionID]: "external_agent" }));
    window.localStorage.setItem("hecate.agentAdapterID", "codex");
    window.localStorage.setItem("hecate.agentWorkspace", "/Users/alice/dev/hecate");
    window.localStorage.setItem("hecate.agentChatSessionID", sessionID);
  }, docsAgentChatSessionID);
  await page.reload();
  await page.waitForSelector("[data-testid='agent-approval-banner']", { timeout: 5_000 });
  await page.waitForTimeout(700);
  await snap(page, "chat-agent-approval");

  console.log("→ chat / agent approval modal");
  await page.getByTestId("agent-approval-banner-review").first().click();
  await page.waitForSelector("[data-testid='agent-approval-modal-submit']", { timeout: 5_000 });
  await page.waitForTimeout(500);
  await snap(page, "chat-agent-approval-modal");

  // firstID is intentionally unused after the chat snap — captured for
  // future "open this specific session" workflows.
  void firstID;

  await browser.close();
  await optimize();
  console.log("done.");
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
