import { test as base, type Page } from "@playwright/test";

// ── Mock data ─────────────────────────────────────────────────────────────────

export const MOCK_PROVIDERS = [
  { name: "anthropic", kind: "cloud", healthy: true,  status: "healthy", default_model: "claude-sonnet-4-6", models: ["claude-opus-4-7", "claude-sonnet-4-6", "claude-opus-4-6"] },
  { name: "openai",    kind: "cloud", healthy: true,  status: "healthy", default_model: "gpt-4o",            models: ["gpt-4o", "gpt-4o-mini"] },
  { name: "ollama",    kind: "local", healthy: false, status: "open",    default_model: "llama3.1:8b",       models: [] },
  { name: "llamacpp",  kind: "local", healthy: false, status: "open",    default_model: "llama-3.2",         models: [] },
];

export const MOCK_PRESETS = [
  { id: "anthropic", name: "Anthropic", kind: "cloud", protocol: "anthropic", base_url: "https://api.anthropic.com/v1",  description: "Anthropic's Claude models." },
  { id: "openai",    name: "OpenAI",    kind: "cloud", protocol: "openai",    base_url: "https://api.openai.com/v1",     description: "OpenAI's GPT models." },
  { id: "ollama",    name: "Ollama",    kind: "local", protocol: "openai",    base_url: "http://127.0.0.1:11434/v1",     description: "Local inference via Ollama." },
  { id: "llamacpp",  name: "llama.cpp", kind: "local", protocol: "openai",    base_url: "http://127.0.0.1:8080/v1",      description: "Local inference via llama.cpp." },
];

export const MOCK_MODELS = [
  { id: "claude-opus-4-7",  owned_by: "anthropic", metadata: { provider: "anthropic", provider_kind: "cloud", default: false } },
  { id: "claude-sonnet-4-6", owned_by: "anthropic", metadata: { provider: "anthropic", provider_kind: "cloud", default: true } },
  { id: "gpt-4o",           owned_by: "openai",    metadata: { provider: "openai",    provider_kind: "cloud", default: true } },
  { id: "gpt-4o-mini",      owned_by: "openai",    metadata: { provider: "openai",    provider_kind: "cloud", default: false } },
];

export const MOCK_AGENT_ADAPTERS = [
  {
    id: "codex",
    name: "Codex",
    kind: "acp",
    command: "codex-acp",
    managed: true,
    managed_package: "@zed-industries/codex-acp",
    available: false,
    status: "missing",
    error: "no local package runner found for @zed-industries/codex-acp",
    description: "Run Codex through its ACP adapter as a long-lived external coding-agent session supervised by Hecate.",
    cost_mode: "external",
    docs_url: "https://github.com/zed-industries/codex-acp",
  },
  {
    id: "claude_code",
    name: "Claude Code",
    kind: "acp",
    command: "claude-agent-acp",
    managed: true,
    managed_package: "@agentclientprotocol/claude-agent-acp",
    available: false,
    status: "missing",
    error: "no local package runner found for @agentclientprotocol/claude-agent-acp",
    description: "Run Claude Agent through ACP as a long-lived external coding-agent session supervised by Hecate.",
    cost_mode: "external",
    docs_url: "https://github.com/agentclientprotocol/claude-agent-acp",
  },
  {
    id: "cursor_agent",
    name: "Cursor Agent",
    kind: "acp",
    command: "cursor-agent",
    available: false,
    status: "missing",
    error: "cursor-agent executable not found in PATH",
    description: "Run Cursor Agent through ACP as a long-lived external coding-agent session supervised by Hecate.",
    cost_mode: "external",
    docs_url: "https://cursor.com/cli",
  },
];

// New model: providers are explicit. The list starts empty and stays empty
// until the operator adds at least one via POST /admin/control-plane/providers.
// Tests that need an existing provider opt into MOCK_ADMIN_CONFIG_WITH_PROVIDERS.
export const MOCK_ADMIN_CONFIG = {
  providers: [] as Array<{
    id: string;
    name: string;
    preset_id?: string;
    custom_name?: string;
    kind: string;
    protocol: string;
    base_url: string;
    enabled: boolean;
    credential_configured: boolean;
    credential_source?: string;
  }>,
  tenants: [],
  api_keys: [],
  policy_rules: [],
};

// MOCK_ADMIN_CONFIG_WITH_PROVIDERS — opt-in fixture for tests that need a
// pre-populated provider table (chat surfaces, lifecycle integration). Two
// cloud providers (one with a configured credential, one without) and one
// local provider. Each carries its preset_id so the edit modal hides the
// Name field (preset names are fixed) and the operator reaches for
// custom_name to disambiguate.
export const MOCK_ADMIN_CONFIG_WITH_PROVIDERS = {
  providers: [
    { id: "anthropic", name: "Anthropic", preset_id: "anthropic", kind: "cloud", protocol: "anthropic", base_url: "https://api.anthropic.com/v1", enabled: true, credential_configured: true,  credential_source: "vault" },
    { id: "openai",    name: "OpenAI",    preset_id: "openai",    kind: "cloud", protocol: "openai",    base_url: "https://api.openai.com/v1",    enabled: true, credential_configured: true,  credential_source: "vault" },
    { id: "ollama",    name: "Ollama",    preset_id: "ollama",    kind: "local", protocol: "openai",    base_url: "http://127.0.0.1:11434/v1",    enabled: true, credential_configured: false },
  ],
  tenants: [],
  api_keys: [],
  policy_rules: [],
};

export const MOCK_FULL_PRESETS = [
  ...MOCK_PRESETS,
  { id: "deepseek",  name: "DeepSeek",  kind: "cloud", protocol: "openai", base_url: "https://api.deepseek.com/v1",   description: "DeepSeek hosted models." },
  { id: "gemini",    name: "Google Gemini", kind: "cloud", protocol: "openai", base_url: "https://generativelanguage.googleapis.com/v1beta/openai", description: "Google Gemini." },
  { id: "groq",      name: "Groq",      kind: "cloud", protocol: "openai", base_url: "https://api.groq.com/openai/v1", description: "Groq inference." },
  { id: "mistral",   name: "Mistral",   kind: "cloud", protocol: "openai", base_url: "https://api.mistral.ai/v1",     description: "Mistral hosted models." },
  { id: "together_ai", name: "Together AI", kind: "cloud", protocol: "openai", base_url: "https://api.together.xyz/v1", description: "Together AI hosted models." },
  { id: "xai",       name: "xAI",       kind: "cloud", protocol: "openai", base_url: "https://api.x.ai/v1",           description: "xAI Grok models." },
  { id: "lmstudio",  name: "LM Studio", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:1234/v1",      description: "Local inference via LM Studio." },
  { id: "localai",   name: "LocalAI",   kind: "local", protocol: "openai", base_url: "http://127.0.0.1:8080/v1",      description: "Local inference via LocalAI." },
];

// slugify mirrors the backend's slugify in handler_controlplane.go: lowercase,
// non-alphanumeric → "-", strip leading/trailing "-". Used to derive provider
// IDs at fixture-mock time so the in-memory list mirrors real backend state.
function slugify(name: string): string {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
}

// ── Route mocking ─────────────────────────────────────────────────────────────

type AdminConfig = typeof MOCK_ADMIN_CONFIG_WITH_PROVIDERS;

export type GatewayMockOptions = {
  // Seed the admin-config /admin/control-plane response. Defaults to the
  // empty list — tests that need a populated table pass
  // MOCK_ADMIN_CONFIG_WITH_PROVIDERS (or any custom shape).
  adminConfig?: AdminConfig;
};

export async function mockGatewayAPIs(page: Page, opts: GatewayMockOptions = {}) {
  const ok = (body: unknown) => ({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify(body),
  });

  // Stateful clone — POST/DELETE/PATCH mutate this in place so a single
  // test can add → list → delete in one flow without re-mocking.
  const state: AdminConfig = JSON.parse(JSON.stringify(opts.adminConfig ?? MOCK_ADMIN_CONFIG));

  await page.route("/healthz", r => r.fulfill(ok({ status: "ok", time: "2026-04-25T00:00:00Z" })));

  // Loopback handshake: stub a 403 by default so TokenGate-driven tests
  // see the manual-paste flow. Tests that specifically exercise the
  // auto-skip path can override this route with a 200 of their own.
  await page.route("/v1/whoami", r =>
    r.fulfill(ok({
      object: "session",
      data: { role: "operator" },
    })),
  );

  await page.route("/v1/models*", r =>
    r.fulfill(ok({ object: "list", data: MOCK_MODELS })),
  );

  await page.route("/admin/providers*", r =>
    r.fulfill(ok({ object: "list", data: MOCK_PROVIDERS })),
  );

  await page.route("/v1/provider-presets*", r =>
    r.fulfill(ok({ object: "list", data: MOCK_FULL_PRESETS })),
  );

  await page.route("/v1/agent-adapters*", r =>
    r.fulfill(ok({ object: "agent_adapters", data: MOCK_AGENT_ADAPTERS })),
  );

  await page.route("/v1/agent-chat/sessions*", async route => {
    if (route.request().method() === "GET") {
      await route.fulfill(ok({ object: "agent_chat_sessions", data: [] }));
      return;
    }
    await route.fulfill({
      status: 404,
      contentType: "application/json",
      body: JSON.stringify({ error: { type: "not_found", message: "agent chat session not found" } }),
    });
  });

  await page.route("/v1/agent-chat/sessions/*/approvals*", async route => {
    await route.fulfill(ok({ object: "agent_chat_approvals", data: [] }));
  });

  await page.route("/admin/budget*", r =>
    r.fulfill(ok({
      object: "budget_status",
      data: {
        key: "global", scope: "global", backend: "memory",
        balance_source: "config",
        debited_micros_usd: 0, debited_usd: "0.000000",
        credited_micros_usd: 1_000_000, credited_usd: "1.000000",
        balance_micros_usd: 1_000_000, balance_usd: "1.000000",
        available_micros_usd: 1_000_000, available_usd: "1.000000",
        enforced: false,
      },
    })),
  );

  await page.route("/admin/accounts/summary*", r =>
    r.fulfill(ok({ object: "account_summary", data: null })),
  );

  await page.route("/v1/chat/sessions*", r =>
    r.fulfill(ok({ object: "list", data: [], has_more: false })),
  );

  await page.route("/admin/requests*", r =>
    r.fulfill(ok({ object: "list", data: [] })),
  );

  // Bare /admin/control-plane (status) — register FIRST so the more-specific
  // /admin/control-plane/providers routes registered below win. Playwright
  // matches routes in REVERSE registration order (most recent first), so
  // specifics-last is the right ordering.
  await page.route("/admin/control-plane*", async route => {
    await route.fulfill({ status: 200, contentType: "application/json",
      body: JSON.stringify({ object: "configured_state", data: state }) });
  });

  // POST /admin/control-plane/providers → create. Slugifies the name to id,
  // appends to the in-memory list, and returns 201. Stateful so the next
  // GET /admin/control-plane reflects the new row.
  // DELETE /admin/control-plane/providers/{id} → drops the row.
  // PATCH /admin/control-plane/providers/{id} → applies name/base_url.
  // PUT  /admin/control-plane/providers/{id}/api-key → flips credential_configured.
  await page.route("/admin/control-plane/providers", async route => {
    if (route.request().method() === "POST") {
      const body = JSON.parse(route.request().postData() ?? "{}") as {
        name?: string;
        preset_id?: string;
        custom_name?: string;
        base_url?: string;
        api_key?: string;
        kind?: string;
        protocol?: string;
      };
      const id = slugify([body.name, body.custom_name].filter(Boolean).join(" "));
      if (!id) {
        await route.fulfill({ status: 400, contentType: "application/json",
          body: JSON.stringify({ error: { type: "invalid_request", message: "provider name is required" } }) });
        return;
      }
      if (state.providers.some(p => p.id === id)) {
        await route.fulfill({ status: 409, contentType: "application/json",
          body: JSON.stringify({ error: { type: "invalid_request", message: `provider with id "${id}" already exists` } }) });
        return;
      }
      const trimmedURL = (body.base_url ?? "").trim();
      if (trimmedURL) {
        const dup = state.providers.find(p => (p.base_url ?? "").trim() === trimmedURL);
        if (dup) {
          await route.fulfill({ status: 409, contentType: "application/json",
            body: JSON.stringify({ error: { type: "invalid_request", message: `base URL already used by provider "${dup.name || dup.id}"` } }) });
          return;
        }
      }
      const record = {
        id,
        name: body.name ?? id,
        custom_name: body.custom_name,
        preset_id: body.preset_id,
        kind: body.kind || "cloud",
        protocol: body.protocol || "openai",
        base_url: trimmedURL,
        enabled: true,
        credential_configured: !!body.api_key,
        credential_source: body.api_key ? "vault" : undefined,
      };
      state.providers.push(record);
      await route.fulfill({ status: 201, contentType: "application/json",
        body: JSON.stringify({ object: "control_plane_provider", data: record }) });
      return;
    }
    await route.continue();
  });

  await page.route("/admin/control-plane/providers/*", async route => {
    const url = route.request().url();
    const method = route.request().method();
    const tail = url.split("/admin/control-plane/providers/")[1] ?? "";
    const [rawID, sub] = tail.split("?")[0].split("/");
    const id = decodeURIComponent(rawID);

    if (sub === "api-key" && method === "PUT") {
      const body = JSON.parse(route.request().postData() ?? "{}") as { key?: string };
      const target = state.providers.find(p => p.id === id);
      if (target) {
        if (body.key) {
          target.credential_configured = true;
          target.credential_source = "vault";
        } else {
          target.credential_configured = false;
          target.credential_source = undefined;
        }
      }
      await route.fulfill({ status: 200, contentType: "application/json",
        body: JSON.stringify({ object: "control_plane_provider_api_key", data: { id, status: body.key ? "set" : "cleared" } }) });
      return;
    }

    if (!sub && method === "DELETE") {
      const idx = state.providers.findIndex(p => p.id === id);
      if (idx >= 0) state.providers.splice(idx, 1);
      await route.fulfill({ status: 200, contentType: "application/json",
        body: JSON.stringify({ object: "control_plane_provider", id, deleted: true }) });
      return;
    }

    if (!sub && method === "PATCH") {
      const body = JSON.parse(route.request().postData() ?? "{}") as { name?: string; base_url?: string };
      const target = state.providers.find(p => p.id === id);
      if (target) {
        if (typeof body.name === "string" && body.name.trim() !== "") target.name = body.name.trim();
        if (typeof body.base_url === "string" && body.base_url.trim() !== "") target.base_url = body.base_url.trim();
      }
      await route.fulfill({ status: 200, contentType: "application/json",
        body: JSON.stringify({ object: "control_plane_provider", data: target ?? null }) });
      return;
    }

    await route.continue();
  });

  // Register after the provider wildcard above: Playwright resolves routes in
  // reverse order, and /providers/* would otherwise shadow this exact probe.
  await page.route("/admin/control-plane/providers/local-discovery", async route => {
    await route.fulfill(ok({
      object: "local_provider_discovery",
      data: [
        {
          preset_id: "ollama",
          name: "Ollama",
          base_url: "http://127.0.0.1:11434/v1",
          probe_url: "http://127.0.0.1:11434/api/tags",
          status: "installed",
          command: "ollama",
          command_available: true,
          command_path: "/usr/local/bin/ollama",
          http_available: false,
          model_count: 0,
          models: [],
        },
        {
          preset_id: "lmstudio",
          name: "LM Studio",
          base_url: "http://127.0.0.1:1234/v1",
          probe_url: "http://127.0.0.1:1234/v1/models",
          status: "running",
          command: "lms",
          command_available: true,
          command_path: "/Users/alice/.lmstudio/bin/lms",
          http_available: true,
          model_count: 1,
          models: ["qwen2.5"],
        },
      ],
    }));
  });

  const emptyPricebookImportDiff = {
    fetched_at: "2026-04-25T00:00:00Z",
    added: [],
    updated: [],
    skipped: [],
    unchanged: 0,
    applied: [],
    failed: [],
  };

  await page.route("/admin/control-plane/pricebook/import/preview", async route => {
    if (route.request().method() !== "POST") {
      await route.continue();
      return;
    }
    await route.fulfill(ok({
      object: "control_plane_pricebook_import_diff",
      data: emptyPricebookImportDiff,
    }));
  });

  await page.route("/admin/control-plane/pricebook/import/apply", async route => {
    if (route.request().method() !== "POST") {
      await route.continue();
      return;
    }
    await route.fulfill(ok({
      object: "control_plane_pricebook_import_diff",
      data: emptyPricebookImportDiff,
    }));
  });

  await page.route("/admin/retention/runs*", r =>
    r.fulfill(ok({ object: "list", data: [] })),
  );

  await page.route("/admin/runtime/stats*", r =>
    r.fulfill(ok({ object: "runtime_stats", data: {} })),
  );

  await page.route("/admin/mcp/cache*", r =>
    r.fulfill(ok({
      object: "mcp_cache_stats",
      data: { entries: 0, in_use: 0, idle: 0, max_entries: 0 },
    })),
  );

  await page.route("/admin/traces*", r =>
    r.fulfill(ok({ object: "list", data: [] })),
  );
}

// ── Extended test fixture ─────────────────────────────────────────────────────

async function seedAdminToken(page: Page) {
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.authToken", "e2e-test-token");
  });
}

export const test = base.extend<{ page: Page }>({
  page: async ({ page }, use) => {
    await seedAdminToken(page);
    await mockGatewayAPIs(page);
    await use(page);
  },
});

export { expect } from "@playwright/test";
