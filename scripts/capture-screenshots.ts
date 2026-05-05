// Capture documentation screenshots against a running gateway on :8765.
//
// Run via the bun script (resolves its own cwd, no `cd ui` needed):
//   bun run capture-screenshots          # from ui/
//   just screenshots                     # from repo root
//
// Prerequisites:
//   1. `just reset-dev && ./hecate &` — gateway running on
//      127.0.0.1:8765 with fresh state.
//   2. ollama running on :11434 with `ollama pull llama3.1:8b` (used to
//      seed one realistic chat session and produce a trace for the
//      observability screenshot). Set HECATE_SKIP_OLLAMA=1 to skip.
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

const docsAgentChatSessionID = "agent-docs-session";
const docsApprovalID = "appr_docs_file_write";

function docsTimestamp(offsetMinutes = 0): string {
  return new Date(Date.now() + offsetMinutes * 60_000).toISOString();
}

const docsAgentAdapters = [
  {
    id: "codex",
    name: "Codex",
    kind: "process",
    command: "codex",
    managed: true,
    managed_package: "@zed-industries/codex-acp",
    available: true,
    status: "available",
    path: "/Users/alice/.cache/hecate/agent-adapters/codex-acp",
    cost_mode: "external",
    docs_url: "https://github.com/openai/codex",
    version: "0.12.0",
    supported_range: ">=0.1.0",
    auth_status: "ok",
  },
  {
    id: "claude_code",
    name: "Claude Code",
    kind: "process",
    command: "claude",
    managed: true,
    managed_package: "@zed-industries/claude-code-acp",
    available: true,
    status: "available",
    path: "/Users/alice/.claude/local/claude",
    cost_mode: "external",
    docs_url: "https://docs.anthropic.com/claude-code",
    version: "2.1.119",
    supported_range: ">=0.1.0",
    auth_status: "ok",
  },
  {
    id: "cursor_agent",
    name: "Cursor Agent",
    kind: "process",
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

async function routeAgentDocsFixtures(page: Page) {
  const fulfillJSON = (route: Route, data: unknown) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(data),
    });

  await page.route(`${BASE_URL}/v1/agent-adapters`, (route) => {
    fulfillJSON(route, { object: "agent_adapters", data: docsAgentAdapters });
  });
  await page.route(`${BASE_URL}/v1/agent-chat/sessions`, (route) => {
    const session = docsAgentSession();
    fulfillJSON(route, {
      object: "agent_chat_sessions",
      data: [{
        id: session.id,
        title: session.title,
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
  await page.route(`${BASE_URL}/v1/agent-chat/sessions/${docsAgentChatSessionID}`, (route) => {
    fulfillJSON(route, { object: "agent_chat_session", data: docsAgentSession() });
  });
  await page.route(`${BASE_URL}/v1/agent-chat/sessions/${docsAgentChatSessionID}/approvals?status=pending`, (route) => {
    fulfillJSON(route, { object: "agent_chat_approvals", data: [docsAgentApproval()] });
  });
  await page.route(`${BASE_URL}/v1/agent-chat/sessions/${docsAgentChatSessionID}/approvals/${docsApprovalID}`, (route) => {
    fulfillJSON(route, { object: "agent_chat_approval", data: docsAgentApproval() });
  });
  await page.route(`${BASE_URL}/v1/agent-chat/grants`, (route) => {
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
  await page.route(`${BASE_URL}/admin/runtime/stats`, (route) => {
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

async function routeLocalProviderDiscoveryDocsFixture(page: Page) {
  await page.route(`${BASE_URL}/admin/control-plane/providers/local-discovery`, route => route.fulfill({
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
// provider is materialized in the CP store, no auto-discovery.
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
  const res = await fetch(`${BASE_URL}/admin/control-plane/providers`, {
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

// seedChatSessions creates a few chat sessions through Hecate's API so
// the sidebar isn't empty. The first session also gets a real
// completion so the chat pane renders an assistant turn — and produces
// a trace for the observability screenshot.
async function seedChatSessions() {
  const titles = [
    "Go interfaces vs structs",
    "Postgres logical replication",
    "Sort TS array without mutating",
  ];
  const ids: string[] = [];
  for (const title of titles) {
    const res = await fetch(`${BASE_URL}/v1/chat/sessions`, {
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
    const chatRes = await fetch(`${BASE_URL}/v1/chat/completions`, {
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
      console.warn("  (the chat screenshot will show an empty session, observability will have no trace)");
      return { firstID };
    }
    console.log(`  llama replied in ${((Date.now() - start) / 1000).toFixed(1)}s`);
  } catch (err) {
    console.warn(`  chat seed skipped: ${(err as Error).message}`);
    console.warn("  (the chat screenshot will show an empty session)");
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
  const missingAgentAdapters = `${BASE_URL}/v1/agent-adapters`;
  await page.route(missingAgentAdapters, route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({
      object: "agent_adapters",
      data: [
        {
          id: "codex",
          name: "Codex",
          kind: "process",
          command: "codex",
          available: false,
          status: "missing",
          error: "codex was not found on PATH",
          cost_mode: "external",
        },
        {
          id: "claude_code",
          name: "Claude Code",
          kind: "process",
          command: "claude",
          available: false,
          status: "missing",
          error: "claude was not found on PATH",
          cost_mode: "external",
        },
        {
          id: "cursor_agent",
          name: "Cursor Agent",
          kind: "process",
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
  await page.waitForSelector("text=Add detected providers", { timeout: 5_000 });
  await snap(page, "chat-empty");
  await page.unroute(missingAgentAdapters);
  await page.unroute(`${BASE_URL}/admin/control-plane/providers/local-discovery`);

  // ── 2. Empty providers list ─────────────────────────────────────────────────
  // The UI loads directly — no auth gate. Land on the Providers tab
  // before any providers exist.
  console.log("→ providers-empty");
  await openWorkspace(page, "providers");
  await page.waitForSelector("text=No providers configured", { timeout: 5_000 });
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
  await page.unroute(`${BASE_URL}/admin/control-plane/providers/local-discovery`);
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

  // ── 6. Chat: seed sessions + one real completion ────────────────────────────
  console.log("→ seeding chat sessions");
  const { firstID } = await seedChatSessions();

  console.log("→ chat (with seeded sessions)");
  await page.evaluate((sessionID) => {
    window.localStorage.setItem("hecate.chatTarget", "model");
    window.localStorage.setItem("hecate.chatSessionID", sessionID);
  }, firstID);
  await openWorkspace(page, "chats");
  await page.waitForTimeout(500);
  await page.getByText("Go interfaces vs structs").first().click();
  await page.waitForTimeout(1500);
  await snap(page, "chat");

  // ── 7. Tasks ────────────────────────────────────────────────────────────────
  console.log("→ tasks (do echo 42 + approval seeded)");
  await seedTask();
  await page.reload();
  await page.waitForSelector(".hecate-activitybar", { timeout: 5_000 });
  await openWorkspace(page, "runs");
  await page.waitForTimeout(2_000);
  await snap(page, "tasks");

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

  // ── 9. Costs workspace ─────────────────────────────────────────────
  console.log("→ costs");
  await openWorkspace(page, "costs");
  await page.waitForTimeout(500);
  await snap(page, "costs");

  // ── 10. Settings — Pricing + Retention ─────────────────────────────
  console.log("→ settings / pricebook");
  await openWorkspace(page, "settings");
  await page.waitForTimeout(500);
  await page.getByRole("button", { name: /pricing/i }).click();
  await page.waitForTimeout(800);
  await snap(page, "settings-pricebook");

  console.log("→ settings / retention");
  await page.getByRole("button", { name: /retention/i }).click();
  await page.waitForTimeout(500);
  await snap(page, "settings-retention");

  // ── 11. New external-agent surfaces ────────────────────────────────────────
  // Mock these endpoints so the documentation shots stay deterministic:
  // screenshots should show the intended UI shape, not whatever agent CLIs
  // and auth state happen to exist on the capture machine.
  console.log("→ settings / external agents");
  await routeAgentDocsFixtures(page);
  await clearAndNavigate(page);
  await openWorkspace(page, "settings");
  await page.getByRole("button", { name: /external agents/i }).click();
  await page.waitForSelector("text=External agent grants", { timeout: 5_000 });
  await page.waitForTimeout(700);
  await snap(page, "settings-external-agents");

  console.log("→ chat / pending agent approval");
  await page.evaluate((sessionID) => {
    window.localStorage.setItem("hecate.workspace", "chats");
    window.localStorage.setItem("hecate.chatTarget", "agent");
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

// seedTask creates a "do echo 42" task so the runs table has at least
// one row. If the task runtime auto-resolves the implicit approval the
// row will land in a completed state; otherwise it sits in the queue
// until the operator approves it manually. Either renders a usable
// shot of the tasks workspace.
async function seedTask() {
  const res = await fetch(`${BASE_URL}/v1/tasks`, {
    method: "POST",
    headers: jsonHeaders,
    body: JSON.stringify({
      title: "echo 42",
      prompt: "do echo 42",
    }),
  });
  if (!res.ok) {
    console.warn(`  task seed failed: ${res.status}`);
    return;
  }
  const json = (await res.json()) as { data: { id: string } };
  const taskID = json.data.id;
  console.log(`  seeded task ${taskID} (do echo 42)`);

  try {
    await fetch(`${BASE_URL}/v1/tasks/${taskID}/start`, { method: "POST", headers: jsonHeaders });
  } catch (err) {
    console.warn(`  task start skipped: ${(err as Error).message}`);
    return;
  }
  await new Promise(r => setTimeout(r, 600));
  try {
    const approvalsRes = await fetch(`${BASE_URL}/v1/tasks/${taskID}/approvals`, { headers: jsonHeaders });
    if (approvalsRes.ok) {
      const approvals = (await approvalsRes.json()) as { data?: Array<{ id: string }> };
      for (const a of approvals.data ?? []) {
        await fetch(`${BASE_URL}/v1/tasks/${taskID}/approvals/${a.id}/resolve`, {
          method: "POST",
          headers: jsonHeaders,
          body: JSON.stringify({ decision: "approved" }),
        });
        console.log(`  approved task approval ${a.id}`);
      }
    }
  } catch (err) {
    console.warn(`  task approve skipped: ${(err as Error).message}`);
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
