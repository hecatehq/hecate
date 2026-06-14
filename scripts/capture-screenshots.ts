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
// install is `brew install pngquant`. Set HECATE_SKIP_OPTIMIZE=1 to skip.
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
const THEME_KEY = "hecate.theme";
const WORKSPACE_KEY = "hecate.workspace";

async function clearAndNavigate(page: Page, path = "/") {
  await page.context().clearCookies();
  await page.goto(BASE_URL);
  await page.evaluate((themeKey) => {
    window.localStorage.clear();
    window.localStorage.setItem(themeKey, "dark");
  }, THEME_KEY);
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

async function openWorkspace(
  page: Page,
  id: "overview" | "runs" | "chats" | "connections" | "projects" | "usage" | "settings",
) {
  await page.evaluate(
    ({ key, workspace }) => {
      window.localStorage.setItem(key, workspace);
    },
    { key: WORKSPACE_KEY, workspace: id },
  );
  await page.reload();
  await page.waitForSelector(".hecate-activitybar", { timeout: 5_000 });
}

type PNGOptimizer = { name: string; args: (path: string) => string[]; lossy: boolean };

function detectOptimizer(): PNGOptimizer | null {
  const candidates: PNGOptimizer[] = [
    {
      name: "pngquant",
      args: (path) => [
        "--quality=80-100",
        "--speed",
        "1",
        "--strip",
        "--ext",
        ".png",
        "--force",
        path,
      ],
      lossy: true,
    },
    {
      name: "oxipng",
      args: (path) => ["-o", "max", "--strip", "safe", path],
      lossy: false,
    },
    {
      name: "magick",
      args: (path) => [path, "-strip", "-define", "png:compression-level=9", path],
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
  await Promise.all(
    captured.map(
      (path) =>
        new Promise<void>((resolve) => {
          const before = statSync(path).size;
          const child = spawn(tool.name, tool.args(path), { stdio: ["ignore", "ignore", "pipe"] });
          const timeout = setTimeout(() => {
            child.kill("SIGTERM");
            console.warn(
              `  ${path.split("/").pop()}: ${tool.name} timed out; leaving current file`,
            );
            resolve();
          }, 20_000);
          let stderr = "";
          child.stderr?.on("data", (chunk) => {
            stderr += chunk.toString();
          });
          child.on("error", (err) => {
            clearTimeout(timeout);
            console.warn(
              `  ${path.split("/").pop()}: ${tool.name} failed to start: ${err.message}`,
            );
            resolve();
          });
          child.on("close", (code) => {
            clearTimeout(timeout);
            if (code !== 0) {
              console.warn(
                `  ${path.split("/").pop()}: ${tool.name} failed (${stderr.trim() || `exit ${code}`}); leaving original`,
              );
              resolve();
              return;
            }
            const after = statSync(path).size;
            const delta = before - after;
            const pct = ((delta / before) * 100).toFixed(0);
            console.log(
              `  ${path.split("/").pop()}: ${(before / 1024).toFixed(1)} KB → ${(after / 1024).toFixed(1)} KB (-${pct}%)`,
            );
            resolve();
          });
        }),
    ),
  );
}

const jsonHeaders = { "Content-Type": "application/json" } as const;

function fulfillJSON(route: Route, data: unknown) {
  return route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify(data),
  });
}

const docsChatSessionID = "agent-docs-session";
const docsApprovalID = "appr_docs_file_write";
const docsHecateChatSessionID = "chat-docs-hecate";
const docsHecateToolsFallbackSessionID = "chat-docs-hecate-tools-fallback";
const docsTaskID = "task_docs_git_status";
const docsRunID = "run_docs_git_status";
const docsProjectID = "proj_docs_alpha_release";
const docsProjectRootID = "root_docs_main";
const docsProjectWorkItemID = "work_docs_readme_screenshots";
const docsProjectImplementAssignmentID = "assign_docs_readme_refresh";
const docsProjectReviewAssignmentID = "assign_docs_review";
const docsProjectHandoffID = "handoff_docs_review";
const docsProjectArtifactID = "artifact_docs_review_notes";
const docsProjectEvidenceID = "artifact_docs_screenshot_evidence";
const docsProjectMemoryID = "memory_docs_release_baseline";
const docsProjectCandidateID = "candidate_docs_onboarding_gap";

const docsWorkspaceDiffFiles = [
  {
    path: "docs/runtime-api.md",
    status: "modified",
    additions: 5,
    deletions: 1,
  },
  {
    path: "ui/src/features/chats/ChatWorkspaceChangesPanel.tsx",
    status: "modified",
    additions: 18,
    deletions: 4,
  },
];

const docsWorkspaceDiffByPath: Record<string, string> = {
  "docs/runtime-api.md":
    "diff --git a/docs/runtime-api.md b/docs/runtime-api.md\n" +
    "index 1a2b3c4..5d6e7f8 100644\n" +
    "--- a/docs/runtime-api.md\n" +
    "+++ b/docs/runtime-api.md\n" +
    "@@ -42,7 +42,11 @@ Chat sessions keep operator-visible state.\n" +
    " The transcript stores user and assistant turns.\n" +
    "-Changed files are shown inside the assistant message.\n" +
    "+Workspace changes are session context, not just transcript text.\n" +
    "+The right panel can show the current Git diff for the selected workspace.\n" +
    "+Each file can be copied or discarded independently.\n" +
    "+The full patch remains copyable for review outside Hecate.\n" +
    "+Message-level artifacts still stay attached to the turn that produced them.\n" +
    " Approvals remain blocking until the operator decides.\n",
  "ui/src/features/chats/ChatWorkspaceChangesPanel.tsx":
    "diff --git a/ui/src/features/chats/ChatWorkspaceChangesPanel.tsx b/ui/src/features/chats/ChatWorkspaceChangesPanel.tsx\n" +
    "index 7b4d8fe..91bc4aa 100644\n" +
    "--- a/ui/src/features/chats/ChatWorkspaceChangesPanel.tsx\n" +
    "+++ b/ui/src/features/chats/ChatWorkspaceChangesPanel.tsx\n" +
    "@@ -118,10 +118,24 @@ function WorkspaceDiffPanel() {\n" +
    "-  return <RawDiffBlock diff={diff} />;\n" +
    "+  return (\n" +
    "+    <DiffViewer\n" +
    "+      compact\n" +
    "+      embedded\n" +
    "+      diff={diff}\n" +
    "+      showLineNumbers\n" +
    "+    />\n" +
    "+  );\n" +
    " }\n" +
    " \n" +
    " function FileActions() {\n" +
    "-  return <button>Discard</button>;\n" +
    "+  return (\n" +
    '+    <div className="workspace-file-actions">\n' +
    '+      <button aria-label="Copy file patch">Copy</button>\n' +
    '+      <button aria-label="Discard file changes">Revert</button>\n' +
    "+    </div>\n" +
    "+  );\n" +
    " }\n",
};

const docsWorkspaceDiff = docsWorkspaceDiffFiles
  .map((file) => docsWorkspaceDiffByPath[file.path])
  .join("\n");
const docsWorkspaceDiffStat =
  "docs/runtime-api.md | 6 +++++-\n" +
  "ui/src/features/chats/ChatWorkspaceChangesPanel.tsx | 22 ++++++++++++++++++----\n" +
  "2 files changed, 23 insertions(+), 5 deletions(-)";

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
  {
    id: "grok_build",
    name: "Grok Build",
    kind: "acp",
    command: "grok",
    args: ["agent", "stdio"],
    available: true,
    status: "available",
    path: "/Users/alice/.local/bin/grok",
    cost_mode: "external",
    docs_url: "https://docs.x.ai/build/cli/headless-scripting#acp",
    version: "0.8.0",
    supported_range: ">=0.1.0",
    auth_status: "ok",
  },
];

function docsAgentApproval() {
  return {
    id: docsApprovalID,
    approval_id: docsApprovalID,
    session_id: docsChatSessionID,
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
    id: docsChatSessionID,
    title: "Review API docs update",
    agent_id: "codex",
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
        content:
          "I found the runtime API section and prepared a small docs patch. Hecate needs your approval before the agent writes the file.",
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
        diff_stat:
          "docs/runtime-api.md | 18 +++++++++++++-----\n1 file changed, 13 insertions(+), 5 deletions(-)",
        diff: "diff --git a/docs/runtime-api.md b/docs/runtime-api.md\nindex 1a2b3c4..5d6e7f8 100644\n--- a/docs/runtime-api.md\n+++ b/docs/runtime-api.md\n@@ -10,6 +10,9 @@\n+External-agent approvals are visible on the chat stream.\n",
        activities: [
          {
            id: "plan-1",
            type: "plan",
            status: "completed",
            title: "Inspect runtime API docs",
            created_at: docsTimestamp(-5),
          },
          {
            id: "tool-1",
            type: "tool_call",
            status: "completed",
            kind: "read_file",
            title: "Read docs/runtime-api.md",
            created_at: docsTimestamp(-4),
          },
          {
            id: "approval-1",
            type: "approval",
            status: "running",
            kind: "file_write",
            title: "Waiting for file_write approval",
            created_at: docsTimestamp(-1),
          },
        ],
        usage: {
          context_size: 200_000,
          context_used: 31_420,
          reported_cost_amount: "0.04",
          reported_cost_currency: "USD",
        },
        raw_output:
          "request_permission file_write docs/runtime-api.md\nwaiting for operator approval",
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
      source: "provider",
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
        content:
          "Today focused on polishing Hecate Chat: clearer task links, trace navigation, and smoother task-backed turns.",
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
        content:
          "Here are the last 3 commits in the Hecate repository:\n\n- `c3c1e9a` fix(ui): compact chat header identifiers\n- `0fcbc52` fix(ui): stabilize busy chat e2e selectors\n- `f6572e5` fix(runtime): avoid overflowing slice capacity calculations\n\nThe branch is clean after those changes.",
        provider: "ollama",
        model: "ministral-3:latest",
        capabilities: {
          tool_calling: "basic",
          streaming: true,
          max_context_tokens: 128_000,
          source: "provider",
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
        raw_output:
          "c3c1e9a fix(ui): compact chat header identifiers\n0fcbc52 fix(ui): stabilize busy chat e2e selectors\nf6572e5 fix(runtime): avoid overflowing slice capacity calculations",
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

function docsHecateToolsFallbackSession() {
  const createdAt = docsTimestamp(-3);
  const updatedAt = docsTimestamp(-1);
  return {
    id: docsHecateToolsFallbackSessionID,
    title: "Ask a tiny local model",
    agent_id: "hecate",
    runtime_kind: "agent",
    provider: "ollama",
    model: "smollm2:135m",
    capabilities: {
      tool_calling: "none",
      streaming: true,
      max_context_tokens: 8_192,
      source: "provider",
    },
    workspace: "/Users/alice/dev/hecate",
    status: "completed",
    created_at: createdAt,
    updated_at: updatedAt,
    segments: [
      {
        id: "model:smollm-joke",
        runtime_kind: "model",
        provider: "ollama",
        model: "smollm2:135m",
        status: "completed",
        message_count: 2,
        started_at: createdAt,
        updated_at: updatedAt,
      },
    ],
    messages: [
      {
        id: "hecate-tools-fallback-user-1",
        runtime_kind: "model",
        execution_mode: "hecate_task",
        tools_enabled: false,
        segment_id: "model:smollm-joke",
        role: "user",
        content: "tell a short terminal joke",
        provider: "ollama",
        model: "smollm2:135m",
        created_at: createdAt,
      },
      {
        id: "hecate-tools-fallback-assistant-1",
        runtime_kind: "model",
        execution_mode: "hecate_task",
        tools_enabled: false,
        segment_id: "model:smollm-joke",
        role: "assistant",
        content:
          "Why did the shell script bring a ladder?\n\nBecause it wanted to reach the next pipeline.",
        provider: "ollama",
        model: "smollm2:135m",
        capabilities: {
          tool_calling: "none",
          streaming: true,
          max_context_tokens: 8_192,
          source: "provider",
        },
        status: "completed",
        request_id: "req_docs_smollm",
        trace_id: "5d3a0e7b9f214d2a9c10",
        duration_ms: 2_800,
        usage: {
          context_size: 8_192,
          context_used: 740,
        },
        created_at: docsTimestamp(-2),
        completed_at: updatedAt,
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
  let session = docsAgentSession();

  await page.route(`${HECATE_API}/agent-adapters`, (route) => {
    fulfillJSON(route, { object: "agent_adapters", data: docsAgentAdapters });
  });
  await page.route(`${HECATE_API}/chat/sessions`, (route) => {
    fulfillJSON(route, {
      object: "chat_sessions",
      data: [
        {
          id: session.id,
          title: session.title,
          agent_id: session.agent_id,
          driver_kind: session.driver_kind,
          native_session_id: session.native_session_id,
          workspace: session.workspace,
          workspace_branch: session.workspace_branch,
          status: session.status,
          message_count: session.messages.length,
          created_at: session.created_at,
          updated_at: session.updated_at,
        },
      ],
    });
  });
  await page.route(`${HECATE_API}/chat/sessions/${docsChatSessionID}`, async (route) => {
    if (route.request().method() === "PATCH") {
      const body = JSON.parse(route.request().postData() || "{}") as { title?: string };
      session = { ...session, title: body.title || session.title, updated_at: docsTimestamp() };
    }
    fulfillJSON(route, { object: "chat_session", data: session });
  });
  await page.route(`${HECATE_API}/chat/sessions/${docsChatSessionID}/approvals*`, (route) => {
    fulfillJSON(route, { object: "chat_approvals", data: [docsAgentApproval()] });
  });
  await page.route(
    `${HECATE_API}/chat/sessions/${docsChatSessionID}/approvals/${docsApprovalID}`,
    (route) => {
      fulfillJSON(route, { object: "chat_approval", data: docsAgentApproval() });
    },
  );
  await page.route(`${HECATE_API}/chat/grants`, (route) => {
    fulfillJSON(route, {
      object: "chat_grants",
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

  let session = docsHecateChatSession();
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
              source: "provider",
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
            capabilities: { tool_calling: "none", streaming: true, source: "provider" },
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
  await page.route(`${HECATE_API}/chat/sessions`, (route) => {
    fulfillJSON(route, {
      object: "chat_sessions",
      data: [
        {
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
        },
      ],
    });
  });
  await page.route(`${HECATE_API}/chat/sessions/${docsHecateChatSessionID}`, async (route) => {
    if (route.request().method() === "PATCH") {
      const body = JSON.parse(route.request().postData() || "{}") as { title?: string };
      session = { ...session, title: body.title || session.title, updated_at: docsTimestamp() };
    }
    fulfillJSON(route, { object: "chat_session", data: session });
  });
  await page.route(
    `${HECATE_API}/chat/sessions/${docsHecateChatSessionID}/approvals?status=pending`,
    (route) => {
      fulfillJSON(route, { object: "chat_approvals", data: [] });
    },
  );
}

async function unrouteHecateChatDocsFixture(page: Page) {
  await page.unroute(`${COMPAT_API}/models`);
  await page.unroute(`${HECATE_API}/settings`);
  await page.unroute(`${HECATE_API}/providers/status`);
  await page.unroute(`${HECATE_API}/chat/sessions`);
  await page.unroute(`${HECATE_API}/chat/sessions/${docsHecateChatSessionID}`);
  await page.unroute(
    `${HECATE_API}/chat/sessions/${docsHecateChatSessionID}/approvals?status=pending`,
  );
}

async function routeWorkspaceDiffDocsFixture(page: Page, sessionID: string) {
  await page.route(`${HECATE_API}/chat/sessions/${sessionID}/workspace-diff`, (route) => {
    fulfillJSON(route, {
      object: "chat_workspace_diff",
      data: {
        workspace: "/Users/alice/dev/hecate",
        diff_stat: docsWorkspaceDiffStat,
        diff: docsWorkspaceDiff,
        has_changes: true,
        files: docsWorkspaceDiffFiles,
      },
    });
  });
  await page.route(`${HECATE_API}/chat/sessions/${sessionID}/workspace-diff/files/**`, (route) => {
    const url = new URL(route.request().url());
    const encodedPath = url.pathname.split("/workspace-diff/files/")[1] ?? "";
    const filePath = decodeURIComponent(encodedPath);
    const file = docsWorkspaceDiffFiles.find((candidate) => candidate.path === filePath);
    const diff = docsWorkspaceDiffByPath[filePath];
    if (!file || !diff) {
      route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({
          error: { message: "File diff not found" },
        }),
      });
      return;
    }
    fulfillJSON(route, {
      object: "chat_workspace_file_diff",
      data: {
        ...file,
        diff,
      },
    });
  });
  await page.route(`${HECATE_API}/chat/sessions/${sessionID}/workspace-diff/revert`, (route) => {
    fulfillJSON(route, {
      object: "chat_workspace_diff",
      data: {
        workspace: "/Users/alice/dev/hecate",
        diff_stat: "",
        diff: "",
        has_changes: false,
        files: [],
      },
    });
  });
}

async function unrouteWorkspaceDiffDocsFixture(page: Page, sessionID: string) {
  await page.unroute(`${HECATE_API}/chat/sessions/${sessionID}/workspace-diff`);
  await page.unroute(`${HECATE_API}/chat/sessions/${sessionID}/workspace-diff/files/**`);
  await page.unroute(`${HECATE_API}/chat/sessions/${sessionID}/workspace-diff/revert`);
}

async function routeHecateToolsFallbackDocsFixture(page: Page) {
  const session = docsHecateToolsFallbackSession();
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
            discovery_source: "provider",
            capabilities: {
              tool_calling: "basic",
              streaming: true,
              max_context_tokens: 128_000,
              source: "provider",
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
            capabilities: {
              tool_calling: "none",
              streaming: true,
              max_context_tokens: 8_192,
              source: "provider",
            },
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
            default_model: "smollm2:135m",
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
          default_model: "smollm2:135m",
          models: ["ministral-3:latest", "smollm2:135m"],
          model_count: 2,
          discovery_source: "provider",
        },
      ],
    });
  });
  await page.route(`${HECATE_API}/chat/sessions`, (route) => {
    fulfillJSON(route, {
      object: "chat_sessions",
      data: [
        {
          id: session.id,
          title: session.title,
          agent_id: session.agent_id,
          runtime_kind: session.runtime_kind,
          provider: session.provider,
          model: session.model,
          capabilities: session.capabilities,
          workspace: session.workspace,
          status: session.status,
          message_count: session.messages.length,
          created_at: session.created_at,
          updated_at: session.updated_at,
        },
      ],
    });
  });
  await page.route(`${HECATE_API}/chat/sessions/${docsHecateToolsFallbackSessionID}`, (route) => {
    fulfillJSON(route, { object: "chat_session", data: session });
  });
  await page.route(
    `${HECATE_API}/chat/sessions/${docsHecateToolsFallbackSessionID}/approvals?status=pending`,
    (route) => {
      fulfillJSON(route, { object: "chat_approvals", data: [] });
    },
  );
}

async function unrouteHecateToolsFallbackDocsFixture(page: Page) {
  await page.unroute(`${COMPAT_API}/models`);
  await page.unroute(`${HECATE_API}/settings`);
  await page.unroute(`${HECATE_API}/providers/status`);
  await page.unroute(`${HECATE_API}/chat/sessions`);
  await page.unroute(`${HECATE_API}/chat/sessions/${docsHecateToolsFallbackSessionID}`);
  await page.unroute(
    `${HECATE_API}/chat/sessions/${docsHecateToolsFallbackSessionID}/approvals?status=pending`,
  );
}

function docsProject() {
  return {
    id: docsProjectID,
    name: "Hecate alpha release",
    description:
      "Coordinate README evidence, screenshot refreshes, and agent-review handoffs for the local runtime console.",
    roots: [
      {
        id: docsProjectRootID,
        path: "/Users/alice/dev/hecate",
        kind: "workspace",
        git_remote: "git@github.com:hecatehq/hecate.git",
        git_branch: "master",
        active: true,
        created_at: docsTimestamp(-180),
        updated_at: docsTimestamp(-5),
      },
    ],
    context_sources: [
      {
        id: "ctx_docs_agents",
        kind: "repo_guidance",
        title: "Agent guidance",
        path: "AGENTS.md",
        enabled: true,
        format: "markdown",
        scope: "repo",
        trust_label: "operator_approved",
        source_category: "guidance",
        metadata: { discovered_by: "project_context_scan" },
        created_at: docsTimestamp(-120),
        updated_at: docsTimestamp(-12),
      },
      {
        id: "ctx_docs_runtime",
        kind: "runtime_doc",
        title: "Runtime API",
        path: "docs/runtime/runtime-api.md",
        enabled: true,
        format: "markdown",
        scope: "project",
        trust_label: "repo",
        source_category: "reference",
        created_at: docsTimestamp(-118),
        updated_at: docsTimestamp(-8),
      },
    ],
    default_root_id: docsProjectRootID,
    default_provider: "ollama",
    default_model: "ministral-3:latest",
    default_agent_profile: "default",
    default_tools_enabled: true,
    default_workspace_mode: "existing",
    default_compact_tool_output: true,
    created_at: docsTimestamp(-180),
    updated_at: docsTimestamp(-4),
    last_opened_at: docsTimestamp(-1),
  };
}

function docsProjectRoles() {
  return [
    {
      id: "role_docs_impl",
      project_id: docsProjectID,
      name: "Implementation",
      description: "Make the code and documentation changes, then leave a concise evidence trail.",
      instructions: "Prefer app seams, keep UI fixtures deterministic, and run related checks.",
      default_driver_kind: "hecate_task",
      default_provider: "ollama",
      default_model: "ministral-3:latest",
      default_agent_profile: "default",
      skill_ids: ["skill_docs_backend", "skill_docs_ui"],
      built_in: false,
      created_at: docsTimestamp(-90),
      updated_at: docsTimestamp(-8),
    },
    {
      id: "role_docs_review",
      project_id: docsProjectID,
      name: "Reviewer",
      description: "Review the artifact evidence and call out release-risk follow-ups.",
      instructions: "Focus on screenshots, stale docs, and test evidence before approval.",
      default_driver_kind: "external_agent",
      default_agent_profile: "codex",
      skill_ids: ["skill_docs_ui"],
      built_in: false,
      created_at: docsTimestamp(-88),
      updated_at: docsTimestamp(-6),
    },
  ];
}

function docsProjectAssignments() {
  return [
    {
      id: docsProjectImplementAssignmentID,
      project_id: docsProjectID,
      work_item_id: docsProjectWorkItemID,
      role_id: "role_docs_impl",
      root_id: docsProjectRootID,
      driver_kind: "hecate_task",
      status: "completed",
      execution_ref: {
        kind: "task_run",
        task_id: "task_docs_readme",
        run_id: "run_docs_readme_1",
        status: "completed",
        trace_id: "trace_docs_project",
      },
      execution: {
        task_id: "task_docs_readme",
        run_id: "run_docs_readme_1",
        task_status: "completed",
        run_status: "completed",
        status: "completed",
        step_count: 6,
        artifact_count: 3,
        approval_count: 0,
        provider: "ollama",
        model: "ministral-3:latest",
        started_at: docsTimestamp(-28),
        finished_at: docsTimestamp(-11),
        trace_id: "trace_docs_project",
      },
      created_at: docsTimestamp(-30),
      updated_at: docsTimestamp(-11),
      started_at: docsTimestamp(-28),
      completed_at: docsTimestamp(-11),
    },
    {
      id: docsProjectReviewAssignmentID,
      project_id: docsProjectID,
      work_item_id: docsProjectWorkItemID,
      role_id: "role_docs_review",
      root_id: docsProjectRootID,
      driver_kind: "external_agent",
      status: "awaiting_approval",
      execution_ref: {
        kind: "chat_session",
        chat_session_id: "chat_docs_review",
        message_id: "msg_docs_review_latest",
        status: "awaiting_approval",
        pending_approval_count: 1,
        trace_id: "trace_docs_review",
      },
      execution: {
        status: "awaiting_approval",
        pending_approval_count: 1,
        artifact_count: 1,
        model: "codex",
        provider: "external_agent",
        started_at: docsTimestamp(-10),
        trace_id: "trace_docs_review",
      },
      created_at: docsTimestamp(-20),
      updated_at: docsTimestamp(-2),
      started_at: docsTimestamp(-10),
    },
  ];
}

function docsProjectWorkItem(assignments = docsProjectAssignments()) {
  return {
    id: docsProjectWorkItemID,
    project_id: docsProjectID,
    title: "Refresh README and screenshots",
    brief:
      "Update the public README to show Projects, promoted memory, handoffs, and workspace evidence after the latest alpha UI cleanup.",
    status: "review",
    priority: "high",
    owner_role_id: "role_docs_impl",
    root_id: docsProjectRootID,
    reviewer_role_ids: ["role_docs_review"],
    assignments,
    created_at: docsTimestamp(-42),
    updated_at: docsTimestamp(-2),
  };
}

function docsProjectArtifacts() {
  return [
    {
      id: docsProjectEvidenceID,
      project_id: docsProjectID,
      work_item_id: docsProjectWorkItemID,
      assignment_id: docsProjectImplementAssignmentID,
      kind: "evidence_link",
      title: "Generated README screenshots",
      body: "Captured chat, task diagnostics, provider settings, Projects, approvals, and usage screenshots from the docs fixture runner.",
      author_role_id: "role_docs_impl",
      evidence_source_kind: "screenshot",
      evidence_url: "docs/screenshots/projects.png",
      evidence_external_id: "projects.png",
      evidence_provider: "local",
      evidence_trust_label: "operator_verified",
      created_at: docsTimestamp(-9),
      updated_at: docsTimestamp(-9),
    },
    {
      id: docsProjectArtifactID,
      project_id: docsProjectID,
      work_item_id: docsProjectWorkItemID,
      assignment_id: docsProjectReviewAssignmentID,
      kind: "review",
      title: "Screenshot QA notes",
      body: "Projects view now shows work queue, assignment evidence, handoff state, and promoted memory without relying on legacy fallback fields.",
      author_role_id: "role_docs_review",
      reviewed_assignment_id: docsProjectImplementAssignmentID,
      review_verdict: "needs_follow_up",
      review_risk: "low",
      review_follow_up_required: true,
      created_at: docsTimestamp(-3),
      updated_at: docsTimestamp(-3),
    },
  ];
}

function docsProjectHandoffs() {
  return [
    {
      id: docsProjectHandoffID,
      project_id: docsProjectID,
      work_item_id: docsProjectWorkItemID,
      source_assignment_id: docsProjectImplementAssignmentID,
      source_run_id: "run_docs_readme_1",
      target_role_id: "role_docs_review",
      target_assignment_id: docsProjectReviewAssignmentID,
      title: "Review release evidence",
      summary:
        "README screenshot refresh is implemented; reviewer should validate layout and stale-doc risk.",
      recommended_next_action:
        "Check the new Projects screenshot and approve the pending review if the README narrative matches the UI.",
      linked_artifact_ids: [docsProjectEvidenceID],
      linked_memory_ids: [docsProjectMemoryID],
      context_refs: ["ctx_docs_agents", "ctx_docs_runtime", "run_docs_readme_1"],
      status: "accepted",
      provenance_kind: "agent_handoff",
      trust_label: "operator_visible",
      created_by_role_id: "role_docs_impl",
      created_at: docsTimestamp(-8),
      updated_at: docsTimestamp(-4),
      status_changed_at: docsTimestamp(-4),
    },
  ];
}

function docsProjectMemoryEntries() {
  return [
    {
      id: docsProjectMemoryID,
      scope: "project",
      project_id: docsProjectID,
      title: "Release screenshot baseline",
      body: "README evidence should include Projects, Hecate Chat, tool fallback, task diagnostics, approvals, usage, and provider configuration screenshots.",
      trust_label: "operator_approved",
      source_kind: "artifact",
      source_id: docsProjectEvidenceID,
      enabled: true,
      created_at: docsTimestamp(-7),
      updated_at: docsTimestamp(-7),
    },
  ];
}

function docsProjectMemoryCandidates() {
  return [
    {
      id: docsProjectCandidateID,
      project_id: docsProjectID,
      title: "Document project onboarding gap",
      body: "Operators may need a short note explaining that project memory candidates stay pending until explicitly promoted.",
      suggested_kind: "follow_up",
      suggested_trust_label: "operator_review",
      suggested_source_kind: "review",
      suggested_source_id: docsProjectArtifactID,
      source_refs: [
        {
          kind: "artifact",
          id: docsProjectArtifactID,
          title: "Screenshot QA notes",
        },
      ],
      status: "pending",
      created_at: docsTimestamp(-2),
      updated_at: docsTimestamp(-2),
    },
  ];
}

function docsProjectSkills() {
  return [
    {
      id: "skill_docs_backend",
      project_id: docsProjectID,
      title: "Backend",
      description: "Runtime, API, task state, event protocol, and app-layer guidance.",
      path: "docs-ai/skills/backend/SKILL.md",
      root_id: docsProjectRootID,
      format: "markdown",
      enabled: true,
      status: "available",
      trust_label: "repo",
      source_context_source_ids: ["ctx_docs_agents"],
      warnings: [],
      discovered_at: docsTimestamp(-14),
      created_at: docsTimestamp(-14),
      updated_at: docsTimestamp(-6),
    },
    {
      id: "skill_docs_ui",
      project_id: docsProjectID,
      title: "UI",
      description: "React operator UI conventions, screenshot fixtures, and project view models.",
      path: "docs-ai/skills/ui/SKILL.md",
      root_id: docsProjectRootID,
      format: "markdown",
      enabled: true,
      status: "available",
      trust_label: "repo",
      source_context_source_ids: ["ctx_docs_agents"],
      warnings: [],
      discovered_at: docsTimestamp(-14),
      created_at: docsTimestamp(-14),
      updated_at: docsTimestamp(-6),
    },
  ];
}

function docsProjectActivity(
  roles = docsProjectRoles(),
  assignments = docsProjectAssignments(),
  artifacts = docsProjectArtifacts(),
  handoffs = docsProjectHandoffs(),
) {
  const workItem = docsProjectWorkItem(assignments);
  const roleByID = new Map(roles.map((role) => [role.id, role]));
  const implementAssignment = assignments[0];
  const reviewAssignment = assignments[1];
  const evidence = artifacts[0];
  const review = artifacts[1];
  const handoff = handoffs[0];
  const completed = {
    id: `${docsProjectImplementAssignmentID}:activity`,
    project_id: docsProjectID,
    work_item: {
      id: workItem.id,
      title: workItem.title,
      status: workItem.status,
      priority: workItem.priority,
    },
    assignment: implementAssignment,
    role: roleByID.get("role_docs_impl"),
    status: "completed",
    blocking_signal: "completed",
    status_summary: "Task run completed with screenshot evidence",
    linked_task_id: "task_docs_readme",
    linked_run_id: "run_docs_readme_1",
    recent_artifacts: [evidence],
    artifact_summary: {
      count: 1,
      latest_kind: evidence.kind,
      latest_title: evidence.title,
      latest_at: evidence.updated_at,
      assignment_id: docsProjectImplementAssignmentID,
    },
    recent_handoffs: [handoff],
    handoff_summary: {
      count: 1,
      accepted_count: 1,
      latest_status: "accepted",
      latest_title: handoff.title,
      latest_at: handoff.updated_at,
      assignment_id: docsProjectImplementAssignmentID,
      target_role_id: "role_docs_review",
    },
    updated_at: implementAssignment.updated_at,
  };
  const blocked = {
    id: `${docsProjectReviewAssignmentID}:activity`,
    project_id: docsProjectID,
    work_item: {
      id: workItem.id,
      title: workItem.title,
      status: workItem.status,
      priority: workItem.priority,
    },
    assignment: reviewAssignment,
    role: roleByID.get("role_docs_review"),
    status: "awaiting_approval",
    blocking_signal: "awaiting_approval",
    status_summary: "External agent review is waiting for operator approval",
    linked_chat_id: "chat_docs_review",
    linked_message_id: "msg_docs_review_latest",
    linked_chat: {
      id: "chat_docs_review",
      title: "README screenshot review",
      agent_id: "codex",
      driver_kind: "external_agent",
      native_session_id: "codex-docs-review-42",
      status: "awaiting_approval",
      latest_message_id: "msg_docs_review_latest",
      latest_role: "assistant",
      latest_status: "awaiting_approval",
      message_count: 5,
      created_at: docsTimestamp(-10),
      updated_at: docsTimestamp(-2),
    },
    recent_artifacts: [review],
    artifact_summary: {
      count: 1,
      latest_kind: review.kind,
      latest_title: review.title,
      latest_at: review.updated_at,
      assignment_id: docsProjectReviewAssignmentID,
    },
    recent_handoffs: [handoff],
    handoff_summary: {
      count: 1,
      accepted_count: 1,
      latest_status: "accepted",
      latest_title: handoff.title,
      latest_at: handoff.updated_at,
      assignment_id: docsProjectImplementAssignmentID,
      target_role_id: "role_docs_review",
    },
    updated_at: reviewAssignment.updated_at,
  };

  return {
    project_id: docsProjectID,
    summary: {
      work_item_count: 1,
      assignment_count: 2,
      active_count: 1,
      blocked_count: 1,
      completed_count: 1,
      recent_count: 2,
    },
    buckets: {
      active: [blocked],
      blocked: [blocked],
      completed: [completed],
      recent: [blocked, completed],
    },
    recent: [blocked, completed],
  };
}

async function routeProjectDocsFixture(page: Page) {
  let project = docsProject();
  const roles = docsProjectRoles();
  const assignments = docsProjectAssignments();
  const workItem = docsProjectWorkItem(assignments);
  const artifacts = docsProjectArtifacts();
  const handoffs = docsProjectHandoffs();
  const memory = docsProjectMemoryEntries();
  const candidates = docsProjectMemoryCandidates();
  const skills = docsProjectSkills();
  const activity = docsProjectActivity(roles, assignments, artifacts, handoffs);

  await page.route(`${HECATE_API}/projects`, (route) => {
    fulfillJSON(route, { object: "projects", data: [project] });
  });
  await page.route(`${HECATE_API}/projects/${docsProjectID}`, async (route) => {
    if (route.request().method() === "PATCH") {
      const body = JSON.parse(route.request().postData() || "{}") as { last_opened_at?: string };
      project = {
        ...project,
        last_opened_at: body.last_opened_at || project.last_opened_at,
        updated_at: docsTimestamp(),
      };
    }
    fulfillJSON(route, { object: "project", data: project });
  });
  await page.route(`${HECATE_API}/projects/${docsProjectID}/activity`, (route) => {
    fulfillJSON(route, { object: "project_activity", data: activity });
  });
  await page.route(`${HECATE_API}/projects/${docsProjectID}/roles`, (route) => {
    fulfillJSON(route, { object: "project_work_roles", data: roles });
  });
  await page.route(`${HECATE_API}/projects/${docsProjectID}/work-items`, (route) => {
    fulfillJSON(route, { object: "project_work_items", data: [workItem] });
  });
  await page.route(
    `${HECATE_API}/projects/${docsProjectID}/work-items/${docsProjectWorkItemID}`,
    (route) => {
      fulfillJSON(route, { object: "project_work_item", data: workItem });
    },
  );
  await page.route(
    `${HECATE_API}/projects/${docsProjectID}/work-items/${docsProjectWorkItemID}/assignments`,
    (route) => {
      fulfillJSON(route, { object: "project_assignments", data: assignments });
    },
  );
  await page.route(
    `${HECATE_API}/projects/${docsProjectID}/work-items/${docsProjectWorkItemID}/artifacts`,
    (route) => {
      fulfillJSON(route, { object: "project_collaboration_artifacts", data: artifacts });
    },
  );
  await page.route(
    `${HECATE_API}/projects/${docsProjectID}/work-items/${docsProjectWorkItemID}/handoffs`,
    (route) => {
      fulfillJSON(route, { object: "project_handoffs", data: handoffs });
    },
  );
  await page.route(
    `${HECATE_API}/projects/${docsProjectID}/memory?include_disabled=true`,
    (route) => {
      fulfillJSON(route, { object: "project_memory", data: memory });
    },
  );
  await page.route(
    `${HECATE_API}/projects/${docsProjectID}/memory/candidates?include_resolved=true`,
    (route) => {
      fulfillJSON(route, { object: "project_memory_candidates", data: candidates });
    },
  );
  await page.route(`${HECATE_API}/projects/${docsProjectID}/skills`, (route) => {
    fulfillJSON(route, { object: "project_skills", data: skills });
  });
}

async function unrouteProjectDocsFixture(page: Page) {
  await page.unroute(`${HECATE_API}/projects`);
  await page.unroute(`${HECATE_API}/projects/${docsProjectID}`);
  await page.unroute(`${HECATE_API}/projects/${docsProjectID}/activity`);
  await page.unroute(`${HECATE_API}/projects/${docsProjectID}/roles`);
  await page.unroute(`${HECATE_API}/projects/${docsProjectID}/work-items`);
  await page.unroute(`${HECATE_API}/projects/${docsProjectID}/work-items/${docsProjectWorkItemID}`);
  await page.unroute(
    `${HECATE_API}/projects/${docsProjectID}/work-items/${docsProjectWorkItemID}/assignments`,
  );
  await page.unroute(
    `${HECATE_API}/projects/${docsProjectID}/work-items/${docsProjectWorkItemID}/artifacts`,
  );
  await page.unroute(
    `${HECATE_API}/projects/${docsProjectID}/work-items/${docsProjectWorkItemID}/handoffs`,
  );
  await page.unroute(`${HECATE_API}/projects/${docsProjectID}/memory?include_disabled=true`);
  await page.unroute(
    `${HECATE_API}/projects/${docsProjectID}/memory/candidates?include_resolved=true`,
  );
  await page.unroute(`${HECATE_API}/projects/${docsProjectID}/skills`);
}

async function routeTaskDiagnosticsDocsFixture(page: Page) {
  const task = {
    id: docsTaskID,
    title: "Inspect Git status",
    prompt: "show current git status",
    execution_kind: "agent_loop",
    execution_profile: "chat_hecate_agent",
    origin_kind: "chat",
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
      output_summary: {
        command: "git status --short",
        exit_code: 128,
        stdout_bytes: 0,
        stderr_bytes: 27,
      },
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
    {
      schema_version: "1",
      event_id: "evt_docs_1",
      task_id: docsTaskID,
      run_id: docsRunID,
      sequence: 1,
      occurred_at: docsTimestamp(-12),
      type: "run.created",
      data: {},
    },
    {
      schema_version: "1",
      event_id: "evt_docs_2",
      task_id: docsTaskID,
      run_id: docsRunID,
      sequence: 2,
      occurred_at: docsTimestamp(-12),
      type: "run.started",
      data: {},
    },
    {
      schema_version: "1",
      event_id: "evt_docs_3",
      task_id: docsTaskID,
      run_id: docsRunID,
      sequence: 3,
      occurred_at: docsTimestamp(-10),
      type: "tool.failed",
      data: { tool: "git_exec" },
    },
    {
      schema_version: "1",
      event_id: "evt_docs_4",
      task_id: docsTaskID,
      run_id: docsRunID,
      sequence: 4,
      occurred_at: docsTimestamp(-3),
      type: "run.failed",
      data: { error: "git_exec failed: not a git repository" },
    },
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

  await page.route(`${HECATE_API}/tasks?*`, (route) =>
    fulfillJSON(route, { object: "tasks", data: [task] }),
  );
  await page.route(`${HECATE_API}/tasks/${docsTaskID}/runs`, (route) =>
    fulfillJSON(route, { object: "task_runs", data: [run] }),
  );
  await page.route(`${HECATE_API}/tasks/${docsTaskID}/approvals`, (route) =>
    fulfillJSON(route, { object: "task_approvals", data: [] }),
  );
  await page.route(`${HECATE_API}/tasks/${docsTaskID}/runs/${docsRunID}/steps`, (route) =>
    fulfillJSON(route, { object: "task_steps", data: steps }),
  );
  await page.route(`${HECATE_API}/tasks/${docsTaskID}/runs/${docsRunID}/artifacts`, (route) =>
    fulfillJSON(route, { object: "task_artifacts", data: artifacts }),
  );
  await page.route(
    `${HECATE_API}/tasks/${docsTaskID}/runs/${docsRunID}/events?after_sequence=0`,
    (route) => fulfillJSON(route, { object: "task_run_events", data: events }),
  );
  await page.route(
    `${HECATE_API}/tasks/${docsTaskID}/runs/${docsRunID}/stream?after_sequence=0`,
    (route) =>
      route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: `event: snapshot\ndata: ${JSON.stringify(snapshot)}\n\n`,
      }),
  );
}

async function unrouteTaskDiagnosticsDocsFixture(page: Page) {
  await page.unroute(`${HECATE_API}/tasks?*`);
  await page.unroute(`${HECATE_API}/tasks/${docsTaskID}/runs`);
  await page.unroute(`${HECATE_API}/tasks/${docsTaskID}/approvals`);
  await page.unroute(`${HECATE_API}/tasks/${docsTaskID}/runs/${docsRunID}/steps`);
  await page.unroute(`${HECATE_API}/tasks/${docsTaskID}/runs/${docsRunID}/artifacts`);
  await page.unroute(`${HECATE_API}/tasks/${docsTaskID}/runs/${docsRunID}/events?after_sequence=0`);
  await page.unroute(`${HECATE_API}/tasks/${docsTaskID}/runs/${docsRunID}/stream?after_sequence=0`);
}

async function routeObservabilityDocsFixture(page: Page) {
  const requestID = "f5894684f2aa8a";
  const traceID = "f5894684f206b30159aa8a92";
  const startedAt = new Date(Date.now() - 15_000);
  const at = (offsetMs: number) => new Date(startedAt.getTime() + offsetMs).toISOString();
  const trace = {
    request_id: requestID,
    trace_id: traceID,
    started_at: startedAt.toISOString(),
    span_count: 7,
    duration_ms: 8550,
    status_code: "ok",
    route: {
      final_provider: "ollama",
      final_provider_kind: "local",
      final_model: "llama3.1:8b",
      final_reason: "pinned_provider_and_model",
      candidates: [
        {
          provider: "ollama",
          provider_kind: "local",
          model: "llama3.1:8b",
          outcome: "selected",
          reason: "pinned_provider_and_model",
          latency_ms: 8550,
        },
        {
          provider: "lmstudio",
          provider_kind: "local",
          model: "ministral-3:latest",
          outcome: "skipped",
          skip_reason: "provider_not_requested",
        },
      ],
    },
  };
  const spans = [
    {
      trace_id: traceID,
      span_id: "span_gateway",
      name: "gateway.request",
      kind: "server",
      start_time: at(0),
      end_time: at(8550),
      status_code: "ok",
      attributes: {
        "http.route": "/v1/chat/completions",
        "gen_ai.request.model": "llama3.1:8b",
        "hecate.request.id": requestID,
      },
      events: [
        {
          name: "gateway.request.started",
          timestamp: at(0),
          attributes: { "hecate.request.id": requestID, "http.route": "/v1/chat/completions" },
        },
        {
          name: "governor.check",
          timestamp: at(1),
          attributes: { "hecate.governor.decision": "allow" },
        },
        {
          name: "router.selected",
          timestamp: at(8),
          attributes: {
            "hecate.route.provider": "ollama",
            "hecate.route.model": "llama3.1:8b",
            "hecate.route.reason": "pinned_provider_and_model",
          },
        },
        {
          name: "provider.response.finished",
          timestamp: at(8548),
          attributes: { "gen_ai.usage.input_tokens": 82, "gen_ai.usage.output_tokens": 36 },
        },
        {
          name: "gateway.response.sent",
          timestamp: at(8550),
          attributes: { "http.status_code": 200 },
        },
      ],
    },
    {
      trace_id: traceID,
      span_id: "span_parse",
      parent_span_id: "span_gateway",
      name: "gateway.request.parse",
      kind: "internal",
      start_time: at(0),
      end_time: at(1),
      status_code: "ok",
    },
    {
      trace_id: traceID,
      span_id: "span_governor",
      parent_span_id: "span_gateway",
      name: "gateway.governor",
      kind: "internal",
      start_time: at(1),
      end_time: at(2),
      status_code: "ok",
    },
    {
      trace_id: traceID,
      span_id: "span_router",
      parent_span_id: "span_gateway",
      name: "gateway.router",
      kind: "internal",
      start_time: at(7),
      end_time: at(9),
      status_code: "ok",
    },
    {
      trace_id: traceID,
      span_id: "span_provider",
      parent_span_id: "span_gateway",
      name: "provider.chat",
      kind: "client",
      start_time: at(10),
      end_time: at(8540),
      status_code: "ok",
      attributes: {
        "gen_ai.system": "ollama",
        "gen_ai.request.model": "llama3.1:8b",
      },
    },
    {
      trace_id: traceID,
      span_id: "span_usage",
      parent_span_id: "span_gateway",
      name: "gateway.usage",
      kind: "internal",
      start_time: at(8541),
      end_time: at(8543),
      status_code: "ok",
    },
    {
      trace_id: traceID,
      span_id: "span_response",
      parent_span_id: "span_gateway",
      name: "gateway.response",
      kind: "internal",
      start_time: at(8548),
      end_time: at(8550),
      status_code: "ok",
    },
  ];

  await page.route(`${HECATE_API}/traces?limit=50`, (route) =>
    fulfillJSON(route, { object: "trace_list", data: [trace] }),
  );
  await page.route(`${HECATE_API}/traces?request_id=${requestID}`, (route) =>
    fulfillJSON(route, {
      object: "trace",
      data: {
        ...trace,
        spans,
      },
    }),
  );
  await page.route(`${HECATE_API}/usage/events?limit=20`, (route) =>
    fulfillJSON(route, {
      object: "usage_events",
      data: [
        {
          type: "chat_completion",
          scope: "provider",
          provider: "ollama",
          model: "llama3.1:8b",
          request_id: requestID,
          actor: "operator",
          amount_micros_usd: 0,
          amount_usd: "0.00000",
          prompt_tokens: 82,
          completion_tokens: 36,
          total_tokens: 118,
          timestamp: at(8550),
        },
      ],
    }),
  );
}

async function unrouteObservabilityDocsFixture(page: Page) {
  await page.unroute(`${HECATE_API}/traces?limit=50`);
  await page.unroute(`${HECATE_API}/traces?request_id=f5894684f2aa8a`);
  await page.unroute(`${HECATE_API}/usage/events?limit=20`);
}

async function routeLocalProviderDiscoveryDocsFixture(page: Page) {
  await page.route(`${HECATE_API}/settings/providers/local-discovery`, (route) =>
    route.fulfill({
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
    }),
  );
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

// seedChatSessions creates a few Hecate-owned sessions through the local
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
      body: JSON.stringify({
        title,
        runtime_kind: "model",
        provider: "ollama",
        model: "llama3.1:8b",
      }),
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
        messages: [
          {
            role: "user",
            content: "In two sentences: when do you reach for a Go interface vs a struct?",
          },
        ],
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
  const context = await browser.newContext({
    viewport: VIEWPORT,
    deviceScaleFactor: 1,
    colorScheme: "dark",
  });
  const page = await context.newPage();

  // ── 1. First-run Chats onboarding ──────────────────────────────────────────
  // No providers are configured yet. Keep this shot deterministic by mocking
  // external-agent availability and local-provider discovery: it should show
  // the one-click local setup path, not the capture machine's real state.
  console.log("→ chat-empty (first-run one-click local setup)");
  const missingAgentAdapters = `${HECATE_API}/agent-adapters`;
  await page.route(missingAgentAdapters, (route) =>
    route.fulfill({
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
    }),
  );
  await routeLocalProviderDiscoveryDocsFixture(page);
  await clearAndNavigate(page);
  await page.evaluate((workspaceKey) => {
    window.localStorage.setItem(workspaceKey, "chats");
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.agentWorkspace", "/Users/alice/dev/hecate");
  }, WORKSPACE_KEY);
  await page.reload();
  await openWorkspace(page, "chats");
  await page.getByRole("button", { name: /New Hecate chat/i }).click();
  await page.waitForSelector("text=Detected locally", { timeout: 5_000 });
  await page.waitForSelector("text=Add selected", { timeout: 5_000 });
  await page.waitForSelector("text=Open Connections", { timeout: 5_000 });
  await snap(page, "chat-empty");
  await page.unroute(missingAgentAdapters);
  await page.unroute(`${HECATE_API}/settings/providers/local-discovery`);

  // ── 2. Empty Connections provider list ──────────────────────────────────────
  // The UI loads directly — no auth gate. Land on the Connections workspace
  // before any providers exist.
  console.log("→ connections-empty");
  await openWorkspace(page, "connections");
  await page.waitForSelector("text=No model providers configured", { timeout: 5_000 });
  await snap(page, "connections-empty");

  // ── 3. Local presets in the Add modal ───────────────────────────────────────
  console.log("→ connections-add-provider (Add modal, Local tab)");
  await routeLocalProviderDiscoveryDocsFixture(page);
  await page.getByRole("button", { name: "Add provider" }).first().click();
  await page.waitForSelector("text=Ollama", { timeout: 5_000 });
  await page.waitForSelector("text=Running", { timeout: 5_000 });
  await page.waitForTimeout(300);
  await snap(page, "connections-add-provider");
  await page.keyboard.press("Escape");
  await page.unroute(`${HECATE_API}/settings/providers/local-discovery`);
  await page.waitForTimeout(300);

  // ── 4. Seed three providers via the API ─────────────────────────────────────
  // These mirror the UI's add flow: one cloud (OpenAI with a fake key), two
  // local (Ollama, LM Studio) on their default ports. The fake OpenAI key is
  // enough to pass the create handler's "cloud-needs-key" guard; an actual
  // round-trip to OpenAI isn't in the screenshot.
  console.log("→ seeding providers");
  await addProvider({ name: "Ollama", preset_id: "ollama", kind: "local" });
  await addProvider({ name: "LM Studio", preset_id: "lmstudio", kind: "local" });
  await addProvider({
    name: "OpenAI",
    preset_id: "openai",
    kind: "cloud",
    api_key: "sk-live-redacted-for-screenshots",
  });

  // ── 5. Populated Connections table ──────────────────────────────────────────
  console.log("→ connections (populated table)");
  await page.reload();
  await page.waitForSelector("text=Cloud providers", { timeout: 5_000 });
  await page.waitForTimeout(2_000);
  await snap(page, "connections");

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
  await page.evaluate(
    ({ sessionID, workspaceKey }) => {
      window.localStorage.setItem(workspaceKey, "chats");
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem(
        "hecate.chatTargetBySessionID",
        JSON.stringify({ [sessionID]: "agent" }),
      );
      window.localStorage.setItem("hecate.chatSessionID", sessionID);
      window.localStorage.setItem("hecate.providerFilter", "ollama");
      window.localStorage.setItem("hecate.model", "ministral-3:latest");
      window.localStorage.setItem("hecate.agentWorkspace", "/Users/alice/dev/hecate");
    },
    { sessionID: docsHecateChatSessionID, workspaceKey: WORKSPACE_KEY },
  );
  await page.reload();
  await page.waitForSelector(".hecate-activitybar", { timeout: 10_000 });
  await openWorkspace(page, "chats");
  await page.waitForSelector("text=Here are the last 3 commits", { timeout: 5_000 });
  await page.waitForSelector("text=Tools on", { timeout: 5_000 });
  await page.waitForTimeout(700);
  await snap(page, "chat");

  // ── 7. Workspace changes panel with rich diff ──────────────────────────────
  console.log("→ chat-workspace-diff (workspace changes panel)");
  await routeWorkspaceDiffDocsFixture(page, docsHecateChatSessionID);
  await page.getByRole("button", { name: "Workspace changes" }).click();
  await page.getByRole("region", { name: "Workspace review" }).waitFor({ timeout: 5_000 });
  await page.getByLabel("Search changed files").fill("runtime");
  await page.getByLabel("Changed files", { exact: true }).waitFor({ timeout: 5_000 });
  await page.getByRole("button", { name: "Copy complete workspace patch" }).waitFor({
    timeout: 5_000,
  });
  await page.getByRole("region", { name: "Diff docs/runtime-api.md" }).waitFor({ timeout: 5_000 });
  await page.waitForTimeout(700);
  await snap(page, "chat-workspace-diff");
  await unrouteWorkspaceDiffDocsFixture(page, docsHecateChatSessionID);
  await unrouteHecateChatDocsFixture(page);

  // ── 8. Hecate Chat with a non-tool model ────────────────────────────────────
  // This captures the fallback path where the chat remains usable, but the
  // header tells the operator the selected model cannot drive task tools.
  console.log("→ chat-tools-fallback (non-tool model, direct chat fallback)");
  await routeHecateToolsFallbackDocsFixture(page);
  await clearAndNavigate(page);
  await page.evaluate(
    ({ sessionID, workspaceKey }) => {
      window.localStorage.setItem(workspaceKey, "chats");
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem(
        "hecate.chatTargetBySessionID",
        JSON.stringify({ [sessionID]: "agent" }),
      );
      window.localStorage.setItem("hecate.chatSessionID", sessionID);
      window.localStorage.setItem("hecate.providerFilter", "ollama");
      window.localStorage.setItem("hecate.model", "smollm2:135m");
      window.localStorage.setItem("hecate.agentWorkspace", "/Users/alice/dev/hecate");
    },
    { sessionID: docsHecateToolsFallbackSessionID, workspaceKey: WORKSPACE_KEY },
  );
  await page.reload();
  await page.waitForSelector("text=Direct chat", { timeout: 5_000 });
  await page.waitForSelector("text=tools unavailable", { timeout: 5_000 });
  await page.waitForTimeout(700);
  await snap(page, "chat-tools-fallback");
  await unrouteHecateToolsFallbackDocsFixture(page);

  // ── 9. Tasks ────────────────────────────────────────────────────────────────
  console.log("→ tasks (failed tool diagnostics fixture)");
  await routeTaskDiagnosticsDocsFixture(page);
  await page.reload();
  await page.waitForSelector(".hecate-activitybar", { timeout: 5_000 });
  await openWorkspace(page, "runs");
  await page.waitForSelector("text=git_exec", { timeout: 5_000 });
  await page.locator("details").evaluateAll((nodes) => {
    for (const node of nodes) {
      const text = node.parentElement?.textContent ?? "";
      (node as HTMLDetailsElement).open = text.includes("Ran git") || text.includes("git_exec");
    }
  });
  await page
    .locator("pre", { hasText: "fatal: not a git repository" })
    .last()
    .waitFor({ state: "visible", timeout: 5_000 });
  await page.waitForTimeout(700);
  await snap(page, "tasks");
  await unrouteTaskDiagnosticsDocsFixture(page);

  // ── 10. Projects ───────────────────────────────────────────────────────────
  console.log("→ projects (work, memory, and handoff fixture)");
  await routeProjectDocsFixture(page);
  await clearAndNavigate(page);
  await page.evaluate(
    ({ projectID, workspaceKey }) => {
      window.localStorage.setItem(workspaceKey, "projects");
      window.localStorage.setItem("hecate.project", projectID);
    },
    { projectID: docsProjectID, workspaceKey: WORKSPACE_KEY },
  );
  await page.reload();
  await page.waitForSelector(".hecate-activitybar", { timeout: 10_000 });
  await openWorkspace(page, "projects");
  await page.waitForSelector("text=Refresh README and screenshots", { timeout: 5_000 });
  await page.waitForSelector("text=Project Assistant", { timeout: 5_000 });
  await page.waitForSelector("text=Assignments", { timeout: 5_000 });
  await page.waitForSelector("text=approval pending", { timeout: 5_000 });
  await page.getByRole("button", { name: "Open task" }).first().scrollIntoViewIfNeeded();
  await page.waitForTimeout(700);
  await snap(page, "projects");
  await unrouteProjectDocsFixture(page);

  // ── 11. Observability — pick a trace first ──────────────────────────────────
  console.log("→ observe (trace selected)");
  await routeObservabilityDocsFixture(page);
  await openWorkspace(page, "overview");
  await page.waitForTimeout(800);
  try {
    const firstRow = page.locator("[data-trace-row], tbody tr").first();
    if ((await firstRow.count()) > 0 && (await firstRow.isVisible())) {
      await firstRow.click({ timeout: 2_000 });
      await page.waitForSelector('[data-testid="trace-event-flow"]', { timeout: 3_000 });
      await page.waitForTimeout(500);
    } else {
      console.warn("  no trace rows found — taking the empty-list shot");
    }
  } catch (err) {
    console.warn(`  trace click skipped: ${(err as Error).message}`);
  }
  await snap(page, "observe");
  await unrouteObservabilityDocsFixture(page);

  // ── 12. Usage workspace ────────────────────────────────────────────
  console.log("→ usage");
  await page.route(`${HECATE_API}/usage/summary`, (route) =>
    fulfillJSON(route, {
      object: "usage_summary",
      data: {
        key: "global",
        scope: "global",
        used_micros_usd: 1600,
        used_usd: "$0.001600",
      },
    }),
  );
  await page.route(`${HECATE_API}/usage/events?limit=20`, (route) =>
    fulfillJSON(route, {
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
    }),
  );
  await openWorkspace(page, "usage");
  await page.waitForTimeout(500);
  await snap(page, "usage");

  // ── 13. Settings — Retention ───────────────────────────────────────
  console.log("→ settings / retention");
  await openWorkspace(page, "settings");
  await page.waitForTimeout(500);
  await page.waitForTimeout(500);
  await snap(page, "settings");

  // ── 14. New external-agent surfaces ────────────────────────────────────────
  // Mock these endpoints so the documentation shots stay deterministic:
  // screenshots should show the intended UI shape, not whatever agent CLIs
  // and auth state happen to exist on the capture machine.
  console.log("→ connections / external agents");
  await routeAgentDocsFixtures(page);
  await clearAndNavigate(page);
  await openWorkspace(page, "connections");
  await page.waitForSelector("text=External agent grants", { timeout: 5_000 });
  await page.waitForTimeout(700);
  await snap(page, "connections-external-agents");

  console.log("→ chat / pending agent approval");
  await page.evaluate(
    ({ sessionID, workspaceKey }) => {
      window.localStorage.setItem(workspaceKey, "chats");
      window.localStorage.setItem("hecate.chatTarget", "external_agent");
      window.localStorage.setItem(
        "hecate.chatTargetBySessionID",
        JSON.stringify({ [sessionID]: "external_agent" }),
      );
      window.localStorage.setItem("hecate.agentAdapterID", "codex");
      window.localStorage.setItem("hecate.agentWorkspace", "/Users/alice/dev/hecate");
      window.localStorage.setItem("hecate.chatSessionID", sessionID);
    },
    { sessionID: docsChatSessionID, workspaceKey: WORKSPACE_KEY },
  );
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
