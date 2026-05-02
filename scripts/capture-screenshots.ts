// Capture documentation screenshots against a running gateway on :8765.
//
// Run via the bun script (resolves its own cwd, no `cd ui` needed):
//   bun run capture-screenshots          # from ui/
//   make screenshots                     # from repo root
//
// Prerequisites:
//   1. `make reset-dev && ./hecate &` — gateway running on :8765
//      with fresh state.
//   2. ollama running on :11434 with `ollama pull llama3.1:8b` (used to seed
//      one realistic chat session and produce a trace for the observability
//      screenshot). Set HECATE_SKIP_OLLAMA=1 to skip.
//
// Optional optimize pass — the script auto-detects the best PNG
// optimizer on PATH (preference: oxipng > pngquant > magick) and runs
// it over each captured PNG. None of these are required to take
// captures; the standard "people usually use this for README PNGs"
// install is `brew install oxipng`. Set HECATE_SKIP_OPTIMIZE=1 to skip.
//
// The admin token is read from .data/hecate.bootstrap.json. Outputs to
// docs/screenshots/<name>.png.

import { chromium, type Page } from "@playwright/test";
import { readFileSync, mkdirSync, statSync } from "node:fs";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";

const BASE_URL = process.env.HECATE_URL ?? "http://127.0.0.1:8765";
const OUT_DIR = resolve(import.meta.dirname, "..", "docs", "screenshots");
mkdirSync(OUT_DIR, { recursive: true });

const bootstrap = JSON.parse(
  readFileSync(resolve(import.meta.dirname, "..", ".data", "hecate.bootstrap.json"), "utf8"),
) as { admin_token: string };
const ADMIN_TOKEN = bootstrap.admin_token;

// 1280×800 is a comfortable docs-rendering size — wide enough to show
// the full sidebar + main pane with no horizontal scrolling, narrow
// enough that GitHub's README column doesn't have to downscale much.
const VIEWPORT = { width: 1280, height: 800 };

async function clearAndNavigate(page: Page, path = "/") {
  await page.context().clearCookies();
  await page.goto(BASE_URL);
  await page.evaluate(() => window.localStorage.clear());
  await page.goto(`${BASE_URL}${path}`);
}

async function signIn(page: Page) {
  await page.evaluate((token) => {
    window.localStorage.setItem("hecate.authToken", token);
  }, ADMIN_TOKEN);
  await page.reload();
  await page.waitForSelector(".hecate-activitybar", { timeout: 10_000 });
}

const captured: string[] = [];

async function snap(page: Page, name: string) {
  const path = resolve(OUT_DIR, `${name}.png`);
  await page.screenshot({ path, fullPage: false });
  captured.push(path);
  console.log(`  saved ${path}`);
}

async function openWorkspace(page: Page, id: "overview" | "runs" | "chats" | "providers" | "costs" | "admin") {
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

const adminHeaders = {
  "Content-Type": "application/json",
  "Authorization": `Bearer ${ADMIN_TOKEN}`,
} as const;

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
    headers: adminHeaders,
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
      headers: adminHeaders,
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
      headers: adminHeaders,
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

  // ── 1. Login screen ────────────────────────────────────────────────────────
  // The bootstrap-token handshake auto-skips TokenGate when the
  // browser is on the same loopback host as the gateway. To force
  // the manual gate for documentation purposes we override the
  // bootstrap fetch to return 403 before the page mounts.
  console.log("→ onboard-wizard");
  await clearAndNavigate(page);
  await page.route("**/v1/bootstrap-token", route =>
    route.fulfill({ status: 403, body: "forbidden", headers: { "Content-Type": "text/plain" } }),
  );
  await page.reload();
  await page.waitForSelector("text=Admin token required", { timeout: 5_000 });
  await snap(page, "onboard-wizard");
  await page.unroute("**/v1/bootstrap-token");

  // ── 2. Empty providers list ─────────────────────────────────────────────────
  // Sign in and land on the Providers tab before any providers exist.
  console.log("→ providers-empty");
  await signIn(page);
  await openWorkspace(page, "providers");
  await page.waitForSelector("text=No providers configured", { timeout: 5_000 });
  await snap(page, "providers-empty");

  // ── 3. Cloud presets in the Add modal ───────────────────────────────────────
  console.log("→ providers-presets (Add modal, Cloud tab)");
  // Click the first "Add provider" button — both the header and empty-state
  // CTAs render with the same label; either opens the modal.
  await page.getByRole("button", { name: "Add provider" }).first().click();
  // Cloud tab is the default. Wait for a recognizable cloud preset to
  // confirm the modal is rendered before snapping.
  await page.waitForSelector("text=Anthropic", { timeout: 5_000 });
  await page.waitForTimeout(300);
  await snap(page, "providers-presets");
  // Close the modal — Esc dismisses it.
  await page.keyboard.press("Escape");
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
    api_key: "sk-live-••••••••••••••••••••" });

  // ── 5. Populated providers table ────────────────────────────────────────────
  console.log("→ providers (populated table)");
  await page.reload();
  await page.waitForSelector("text=Cloud providers", { timeout: 5_000 });
  // Give the runtime a moment to run an initial probe so the Health
  // and Models cells aren't all "Pending".
  await page.waitForTimeout(2_000);
  await snap(page, "providers");

  // ── 6. Chat: seed sessions + one real completion ────────────────────────────
  console.log("→ seeding chat sessions");
  const { firstID } = await seedChatSessions();

  console.log("→ chat (with seeded sessions)");
  await openWorkspace(page, "chats");
  await page.waitForTimeout(500);
  // Click the seeded session by its title so the main pane renders the
  // user/assistant turn from the ollama completion above.
  await page.getByText("Go interfaces vs structs").first().click();
  await page.waitForTimeout(1500);
  await snap(page, "chat");

  // ── 7. Tasks ────────────────────────────────────────────────────────────────
  console.log("→ tasks (do echo 42 + approval seeded)");
  await seedTask();
  await page.reload();
  await page.waitForSelector(".hecate-activitybar", { timeout: 5_000 });
  await openWorkspace(page, "runs");
  // Wait long enough for the task list fetch + a soft moment for the
  // run state to surface. The task is a simple shell echo; if the
  // runner is wired the row will show "completed", otherwise it shows
  // its pending state — either renders a usable list view.
  await page.waitForTimeout(2_000);
  await snap(page, "tasks");

  // ── 8. Observability — pick a trace first ───────────────────────────────────
  console.log("→ observe (trace selected)");
  await openWorkspace(page, "overview");
  await page.waitForTimeout(800);
  // Click the first row in the request ledger / trace list. Selector
  // strategy: the ledger renders rows with a monospace request id; the
  // first selectable row gets clicked. Fall back to no-op if the list
  // is empty (e.g. ollama wasn't running, no traces produced).
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
  // Lifted from the old Admin → Balances tab. Visible to every
  // authenticated role (admin + tenant); shows the balance card on
  // top and the per-key usage table on the bottom.
  console.log("→ costs");
  await openWorkspace(page, "costs");
  await page.waitForTimeout(500);
  await snap(page, "costs");

  // ── 10. Settings panels — Pricing ──────────────────
  await openWorkspace(page, "admin");
  await page.waitForTimeout(500);

  console.log("→ settings / pricebook");
  await page.getByRole("button", { name: /pricing/i }).click();
  await page.waitForTimeout(800);
  await snap(page, "admin-pricebook");

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
    headers: adminHeaders,
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

  // Try to start the task (and resolve any pending approval) so the
  // row in the tasks list shows real progress. Best-effort — if the
  // endpoints aren't wired in this build, the row still renders.
  try {
    await fetch(`${BASE_URL}/v1/tasks/${taskID}/start`, { method: "POST", headers: adminHeaders });
  } catch (err) {
    console.warn(`  task start skipped: ${(err as Error).message}`);
    return;
  }
  // Pause briefly so an approval request has time to surface, then auto-resolve
  // any pending approvals as "approve" so the run can proceed.
  await new Promise(r => setTimeout(r, 600));
  try {
    const approvalsRes = await fetch(`${BASE_URL}/v1/tasks/${taskID}/approvals`, { headers: adminHeaders });
    if (approvalsRes.ok) {
      const approvals = (await approvalsRes.json()) as { data?: Array<{ id: string }> };
      for (const a of approvals.data ?? []) {
        await fetch(`${BASE_URL}/v1/tasks/${taskID}/approvals/${a.id}/resolve`, {
          method: "POST",
          headers: adminHeaders,
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
