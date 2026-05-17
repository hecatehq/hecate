import { expect, test as baseTest, mockGatewayAPIs, MOCK_MODELS, MOCK_PROVIDERS, MOCK_SETTINGS_CONFIG_WITH_PROVIDERS } from "./fixtures";
import type { Page } from "@playwright/test";

// Chat tests need a populated provider list — without one, AppShell hides
// the chat workspace behind a "No model providers configured" placeholder. Override
// the default empty-list mock with the populated fixture for every chat test.
const test = baseTest.extend<{ page: Page }>({
  page: async ({ page }, use) => {
    await page.unrouteAll({ behavior: "ignoreErrors" });
    await mockGatewayAPIs(page, { settingsConfig: MOCK_SETTINGS_CONFIG_WITH_PROVIDERS });
    await use(page);
  },
});

test.beforeEach(async ({ page }) => {
  await page.goto("/");
  // Chat is the default workspace
  await page.waitForSelector(".hecate-activitybar");
});

async function startHecateChat(page: Page) {
  await page.getByRole("button", { name: "New Hecate chat", exact: true }).click();
  await expect(page.getByText(/Ready when you are|Choose a workspace|No routable model|No models discovered|Nothing runnable yet/).first()).toBeVisible();
}

async function switchToModel(page: Page) {
  await startHecateChat(page);
  const useModel = page.getByRole("button", { name: "Use model", exact: true });
  if (await useModel.isVisible()) {
    await useModel.click();
  }
}

test("renders the message textarea and send button", async ({ page }) => {
  await startHecateChat(page);
  await expect(page.locator("textarea")).toBeVisible();
  await expect(page.locator("button[type='submit']")).toBeVisible();
});

test("send button is disabled when message is empty", async ({ page }) => {
  await startHecateChat(page);
  await page.locator("textarea").fill("");
  await expect(page.locator("button[type='submit']")).toBeDisabled();
});

test("send button becomes enabled when message has content", async ({ page }) => {
  await switchToModel(page);
  await page.locator("textarea").fill("Hello");
  await expect(page.locator("button[type='submit']")).toBeEnabled();
});

test("empty Hecate Chat points operators to Connections before send", async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await mockGatewayAPIs(page);
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await expect(page.getByText("No chats yet").first()).toBeVisible();
  await expect(page.getByText("Start your first Hecate chat from the sidebar.")).toBeVisible();
  await page.getByRole("button", { name: "New Hecate chat", exact: true }).click();
  await expect(page.getByText("Nothing runnable yet")).toBeVisible();
  await page.getByRole("button", { name: "Open Connections" }).click();
  await expect(page.locator(".hecate-activitybar [aria-current='page']")).toHaveAttribute("aria-label", /Connections/);
});

test("model picker opens and lists models from mock data", async ({ page }) => {
  await switchToModel(page);
  // Wait for models to load, then open the picker
  const modelBtn = page.getByRole("button", { name: /model picker/i });
  await modelBtn.click();

  for (const m of MOCK_MODELS.filter(m => m.metadata?.provider === "anthropic")) {
    await expect(page.locator(`.dropdown-menu`)).toContainText(m.id);
  }
});

test("model picker filters by search input", async ({ page }) => {
  await switchToModel(page);
  const modelBtn = page.getByRole("button", { name: /model picker/i });
  await modelBtn.click();

  const menu = page.locator(".dropdown-menu");
  await menu.locator("input").fill("claude");

  await expect(menu).toContainText("claude-sonnet-4-6");
  await expect(menu).not.toContainText("gpt");
});

test("Hecate composer provider and model controls match shared chat dropdowns", async ({ page }) => {
  await startHecateChat(page);

  const providerBtn = page.getByRole("button", { name: /provider picker/i });
  await expect(providerBtn).toContainText("provider");
  await expect(providerBtn).toContainText("Anthropic");

  const modelBtn = page.getByRole("button", { name: /model picker/i });
  await expect(modelBtn).toContainText("model");
  await modelBtn.click();

  const menu = page.locator(".dropdown-menu");
  await expect(menu.getByPlaceholder("Filter models...")).toBeVisible();
  await menu.getByPlaceholder("Filter models...").fill("claude");
  await expect(menu).toContainText("claude-sonnet-4-6");
  await expect(menu).not.toContainText("gpt");
});

test("selecting a model closes the picker and updates the button label", async ({ page }) => {
  await switchToModel(page);
  const modelBtn = page.getByRole("button", { name: /model picker/i });
  await modelBtn.click();

  await page.locator(".dropdown-menu").locator("text=claude-opus-4-7").first().click();

  await expect(page.locator(".dropdown-menu")).not.toBeVisible();
  await expect(modelBtn).toContainText("claude-opus-4-7");
});

test("provider picker shows healthy providers", async ({ page }) => {
  await switchToModel(page);
  const healthyProviders = MOCK_PROVIDERS.filter(p => p.healthy);
  const providerBtn = page.getByRole("button", { name: /provider picker/i });
  await providerBtn.click();

  const menu = page.locator(".dropdown-menu").first();
  for (const p of healthyProviders) {
    await expect(menu).toContainText(p.name, { ignoreCase: true });
  }
});

test("New chat keeps an unsent draft on the active empty chat", async ({ page }) => {
  await switchToModel(page);
  // Fill the message box so we can verify the state resets
  await page.locator("textarea").fill("some prior message");
  await page.getByRole("button", { name: "New Hecate chat", exact: true }).click();
  // The current empty chat is still the target, so an unsent draft is
  // preserved rather than discarded.
  await expect(page.getByRole("button", { name: "Chat Hecate chat, anthropic" })).toBeVisible();
  await expect(page.locator("textarea")).toHaveValue("some prior message");
});

test("New chat creates an external-agent session with controls before the first prompt", async ({ page }) => {
  let createBody: any = null;
  await page.route("/hecate/v1/agent-adapters*", async route => {
    if (route.request().method() !== "GET") return route.continue();
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "agent_adapters",
        data: [
          { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available", cost_mode: "external" },
          { id: "claude_code", name: "Claude Code", kind: "acp", command: "claude-agent-acp", available: true, status: "available", cost_mode: "external" },
          { id: "cursor_agent", name: "Cursor", kind: "acp", command: "cursor-agent", available: true, status: "available", cost_mode: "external" },
        ],
      }),
    });
  });
  await page.route("/hecate/v1/chat/sessions", async route => {
    if (route.request().method() !== "POST") return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_sessions", data: [] }),
    });
    createBody = JSON.parse(route.request().postData() ?? "{}");
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_session",
        data: {
          id: "agent-chat-codex-e2e",
          title: "Codex chat",
          runtime_kind: "external_agent",
          adapter_id: "codex",
          adapter_name: "Codex",
          driver_kind: "acp",
          native_session_id: "native-codex-e2e",
          workspace: "/tmp/hecate-e2e",
          status: "idle",
          config_options: [
            {
              id: "model",
              name: "Model",
              category: "model",
              type: "select",
              current_value: "fast",
              options: [
                { value: "fast", name: "Fast" },
                { value: "smart", name: "Smart" },
              ],
            },
          ],
          messages: [],
        },
      }),
    });
  });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.agentAdapterID", "codex");
    window.localStorage.setItem("hecate.agentWorkspace", "/tmp/hecate-e2e");
  });
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await expect(page.getByRole("button", { name: "New Codex chat", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "New Codex chat", exact: true }).click();

  await expect.poll(() => createBody).toMatchObject({
    runtime_kind: "external_agent",
    adapter_id: "codex",
    workspace: "/tmp/hecate-e2e",
  });
  await expect(page.getByRole("button", { name: "Model" })).toContainText("Fast");
  await page.getByRole("button", { name: "Choose agent for new chat" }).click();
  await expect(page.getByRole("option", { name: /Codex/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("option", { name: /Claude Code/ })).not.toHaveAttribute("aria-disabled", "true");
});

test("sidebar rename works for agent-chat sessions", async ({ page }) => {
  let title = "Codex chat";
  let patchBody: any = null;
  await page.route("/hecate/v1/chat/sessions", async route => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_sessions",
        data: [{
          id: "rename-chat-e2e",
          title,
          runtime_kind: "external_agent",
          adapter_id: "codex",
          workspace: "/tmp/hecate-e2e",
          status: "idle",
          message_count: 0,
          created_at: "2026-05-14T10:00:00Z",
          updated_at: "2026-05-14T10:00:00Z",
        }],
      }),
    });
  });
  await page.route("/hecate/v1/chat/sessions/rename-chat-e2e", async route => {
    if (route.request().method() === "PATCH") {
      patchBody = JSON.parse(route.request().postData() ?? "{}");
      title = patchBody.title;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_session",
        data: {
          id: "rename-chat-e2e",
          title,
          runtime_kind: "external_agent",
          adapter_id: "codex",
          workspace: "/tmp/hecate-e2e",
          status: "idle",
          messages: [],
          created_at: "2026-05-14T10:00:00Z",
          updated_at: "2026-05-14T10:01:00Z",
        },
      }),
    });
  });
  await page.route("/hecate/v1/chat/sessions/rename-chat-e2e/approvals*", route =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_approvals", data: [] }),
    }),
  );
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.chatSessionID", "rename-chat-e2e");
  });
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await page.getByRole("button", { name: "Rename chat Codex chat" }).click();
  const input = page.locator("input").nth(1);
  await expect(input).toHaveValue("Codex chat");
  await input.fill("Release notes review");
  await input.press("Enter");

  await expect.poll(() => patchBody).toEqual({ title: "Release notes review" });
  await expect(page.getByText("Release notes review").first()).toBeVisible();
});

test("system prompt editor opens and closes", async ({ page }) => {
  await switchToModel(page);
  const settingsBtn = page.getByRole("button", { name: "Chat settings" });
  await settingsBtn.click();
  await expect(page.getByText("SYSTEM PROMPT / INSTRUCTIONS", { exact: true })).toBeVisible();
  await expect(page.locator("textarea").nth(1)).toBeVisible();

  await settingsBtn.click();
  await expect(page.getByText("SYSTEM PROMPT / INSTRUCTIONS", { exact: true })).not.toBeVisible();
});

test("Enter-switch toggle is visible in the input toolbar and clickable", async ({ page }) => {
  await switchToModel(page);
  // The label is one of "↵ to send" or "⌘+↵ to send" / "Ctrl+↵ to send" depending on OS.
  const toggle = page.locator("button").filter({ hasText: /↵ to send/ });
  await expect(toggle).toBeVisible();
  const before = await toggle.textContent();
  await toggle.click();
  // After click, label should change.
  await expect(toggle).not.toHaveText(before ?? "");
});

test("Enter-switch preference persists across reload via localStorage", async ({ page }) => {
  await switchToModel(page);
  const toggle = page.locator("button").filter({ hasText: /↵ to send/ });
  const initial = await toggle.textContent();
  await toggle.click();
  const after = await toggle.textContent();
  expect(after).not.toBe(initial);

  await page.reload();
  await page.waitForSelector(".hecate-activitybar");
  await switchToModel(page);
  const reloaded = page.locator("button").filter({ hasText: /↵ to send/ });
  await expect(reloaded).toHaveText(after ?? "");
});

test("workspace selection persists across reload", async ({ page }) => {
  await page.locator(".hecate-activitybar [aria-label^='Connections']").click();
  await expect(page.locator(".hecate-activitybar [aria-current='page']")).toHaveAttribute("aria-label", /Connections/);

  await page.reload();
  await page.waitForSelector(".hecate-activitybar");
  await expect(page.locator(".hecate-activitybar [aria-current='page']")).toHaveAttribute("aria-label", /Connections/);
});

// A failing /v1/chat/completions surfaces inline beneath the chat header.
// Toast is gone for chat errors — the chat surface owns its own banner so a
// single source of truth shows up next to the input. The "api key is
// required for cloud provider X" wire message is humanized into a
// Connections repair action before reaching the DOM.
test("chat error renders inline with the humanized message", async ({ page }) => {
  await switchToModel(page);
  await page.route("/hecate/v1/chat/sessions", async route => {
    if (route.request().method() !== "POST") {
      await route.continue();
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_session",
        data: {
          id: "chat_err_e2e",
          title: "x",
          runtime_kind: "model",
          status: "created",
          provider: "anthropic",
          model: "claude-sonnet-4-6",
          messages: [],
          created_at: "2026-04-21T00:00:00Z",
          updated_at: "2026-04-21T00:00:00Z",
        },
      }),
    });
  });
  await page.route("/hecate/v1/chat/sessions/chat_err_e2e/stream", route =>
    route.fulfill({ status: 200, contentType: "text/event-stream", body: "" }),
  );
  await page.route("/hecate/v1/chat/sessions/chat_err_e2e/messages", route =>
    route.fulfill({
      status: 400,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          type: "gateway_error",
          message: "api key is required for cloud provider anthropic when stub mode is disabled",
        },
      }),
    }),
  );

  await page.locator("textarea").first().fill("hello");
  await page.locator("button[type='submit']").click();

  // Inline banner under the chat header carries the humanized message.
  await expect(page.getByText(/has no API key/i).first()).toBeVisible();
});

test("agent chat renders indented fenced code blocks as code", async ({ page }) => {
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.chatSessionID", "markdown-fence-e2e");
  });

  const session = {
    id: "markdown-fence-e2e",
    title: "Markdown fence",
    runtime_kind: "agent",
    provider: "ollama",
    model: "qwen2.5-coder",
    workspace: "/tmp/e2e",
    status: "completed",
    message_count: 2,
    messages: [
      { id: "m-user", role: "user", content: "show commands", created_at: "2026-04-21T10:00:00Z" },
      {
        id: "m-agent",
        role: "assistant",
        content: [
          "Next steps:",
          "",
          "1. Review local changes:",
          "  ```sh",
          "git status",
          "git diff -- README.md",
          "  ```",
        ].join("\n"),
        provider: "ollama",
        model: "qwen2.5-coder",
        status: "completed",
        created_at: "2026-04-21T10:00:01Z",
      },
    ],
  };

  await page.route("/hecate/v1/chat/sessions", (route) => {
    if (route.request().method() !== "GET") {
      void route.fallback();
      return;
    }
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_sessions",
        data: [{ ...session, messages: undefined }],
      }),
    });
  });
  await page.route("/hecate/v1/chat/sessions/markdown-fence-e2e", route =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_session", data: session }),
    }),
  );
  await page.route("/hecate/v1/chat/sessions/markdown-fence-e2e/approvals*", route =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ object: "list", data: [] }) }),
  );

  await page.reload();
  await page.waitForSelector(".hecate-activitybar");

  const codeBlock = page.locator("pre").filter({ hasText: "git status" });
  await expect(codeBlock).toBeVisible();
  await expect(codeBlock).toContainText("git diff -- README.md");
  await expect(page.locator("code").filter({ hasText: "sh" })).toHaveCount(0);
});

test("agent chat previews failed tool stdout and stderr in Advanced details", async ({ page }) => {
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.chatSessionID", "failed-tool-output-e2e");
  });

  const session = {
    id: "failed-tool-output-e2e",
    title: "Failed tool",
    runtime_kind: "agent",
    provider: "ollama",
    model: "qwen2.5-coder",
    workspace: "/tmp/e2e",
    status: "completed",
    message_count: 2,
    messages: [
      { id: "m-user", role: "user", content: "why did git fail?", created_at: "2026-04-21T10:00:00Z" },
      {
        id: "m-agent",
        role: "assistant",
        runtime_kind: "agent",
        content: "The command failed while inspecting git state.",
        provider: "ollama",
        model: "qwen2.5-coder",
        status: "failed",
        task_id: "task_failed",
        run_id: "run_failed",
        created_at: "2026-04-21T10:00:01Z",
        activities: [
          { id: "tool_git", type: "tool_call", title: "git_exec (failed)", status: "failed", kind: "git", detail: "git_exec - failed" },
          {
            id: "stdout",
            type: "artifact",
            title: "git-stdout.txt",
            status: "ready",
            artifact_id: "art_stdout",
            artifact_size_bytes: 41,
            artifact_preview: "On branch feature/chat-message-queue",
          },
          {
            id: "stderr",
            type: "artifact",
            title: "git-stderr.txt",
            status: "ready",
            artifact_id: "art_stderr",
            artifact_size_bytes: 57,
            artifact_preview: "fatal: not a git repository",
          },
          {
            id: "empty-stderr",
            type: "artifact",
            title: "shell-stderr.txt",
            status: "ready",
            artifact_id: "art_empty_stderr",
            artifact_size_bytes: 0,
          },
          { id: "terminal", type: "failed", title: "Run failed", status: "failed", terminal: true },
        ],
      },
    ],
  };

  await page.route("/hecate/v1/chat/sessions", (route) => {
    if (route.request().method() !== "GET") {
      void route.fallback();
      return;
    }
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_sessions",
        data: [{ ...session, messages: undefined }],
      }),
    });
  });
  await page.route("/hecate/v1/chat/sessions/failed-tool-output-e2e", route =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_session", data: session }),
    }),
  );
  await page.route("/hecate/v1/chat/sessions/failed-tool-output-e2e/approvals*", route =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ object: "list", data: [] }) }),
  );

  await page.reload();
  await page.waitForSelector(".hecate-activitybar");

  await page.getByText(/1 (failed )?tool/).click();
  await page.getByText("Advanced").first().click();

  await expect(page.getByText(/Preview the related run output/)).toBeVisible();
  await expect(page.getByText("On branch feature/chat-message-queue")).toBeVisible();
  await expect(page.getByText("fatal: not a git repository")).toBeVisible();
  await expect(page.getByText("Open task output")).toBeVisible();
  await expect(page.getByText("Preview unavailable in this snapshot.")).toHaveCount(0);
});

test("empty model chat can add all detected local providers in one click", async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await mockGatewayAPIs(page);
  const created: Array<Record<string, unknown>> = [];
  await page.route("/hecate/v1/settings/providers", async route => {
    if (route.request().method() === "POST") {
      created.push(JSON.parse(route.request().postData() ?? "{}"));
    }
    await route.fallback();
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await switchToModel(page);

  await expect(page.getByText("Detected locally")).toBeVisible();
  await expect(page.getByText("Ollama", { exact: true })).toBeVisible();
  await expect(page.getByText("LM Studio", { exact: true })).toBeVisible();
  await expect(page.getByText("Installed")).toBeVisible();
  await expect(page.getByText("Running")).toBeVisible();

  await page.getByRole("button", { name: "Add selected" }).click();

  await expect.poll(() => created.map(body => body.preset_id).sort()).toEqual(["lmstudio", "ollama"]);
  await expect(page.getByText("Nothing runnable yet")).toBeVisible();
  await expect(page.getByText(/Add a model provider or install a supported coding-agent CLI before sending a message/)).toBeVisible();
  await expect(page.getByRole("button", { name: /Add selected/i })).toHaveCount(0);
});

test("empty Hecate Agent chat can add all detected local providers in one click", async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await mockGatewayAPIs(page);
  const created: Array<Record<string, unknown>> = [];
  await page.route("/hecate/v1/settings/providers", async route => {
    if (route.request().method() === "POST") {
      created.push(JSON.parse(route.request().postData() ?? "{}"));
    }
    await route.fallback();
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.getByRole("button", { name: "New Hecate chat", exact: true }).click();

  await expect(page.getByText("Detected locally")).toBeVisible();
  await expect(page.getByText("Ollama", { exact: true })).toBeVisible();
  await expect(page.getByText("LM Studio", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Add selected" }).click();

  await expect.poll(() => created.map(body => body.preset_id).sort()).toEqual(["lmstudio", "ollama"]);
  await expect(page.getByText("Nothing runnable yet")).toBeVisible();
  await expect(page.getByRole("button", { name: /Add selected/i })).toHaveCount(0);
});

test("Hecate Agent local-provider onboarding renders the real final answer after completion", async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.agentWorkspace", "/tmp/hecate-e2e-workspace");
  });
  await mockGatewayAPIs(page);

  await page.route("/v1/models*", route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({
      object: "list",
      data: [{
        id: "qwen2.5",
        owned_by: "lm-studio",
        metadata: {
          provider: "lm-studio",
          provider_kind: "local",
          capabilities: { tool_calling: "basic", streaming: true, source: "operator_override" },
        },
      }],
    }),
  }));

  const sessions: any[] = [];
  await page.route("/hecate/v1/chat/sessions", async route => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_sessions", data: sessions }),
      });
      return;
    }
    if (route.request().method() === "POST") {
      const body = await route.request().postDataJSON();
      const session = {
        id: "chat-hecate-e2e",
        title: body.title || "show diff",
        runtime_kind: "agent",
        provider: body.provider || "",
        model: body.model || "qwen2.5",
        capabilities: { tool_calling: "basic", streaming: true, source: "operator_override" },
        workspace: body.workspace,
        status: "created",
        message_count: 0,
        messages: [],
      };
      sessions.splice(0, sessions.length, session);
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_session", data: session }),
      });
      return;
    }
    await route.fulfill({ status: 405, body: "" });
  });
  await page.route("/hecate/v1/chat/sessions/chat-hecate-e2e", async route => {
    if (route.request().method() !== "GET") {
      await route.fulfill({ status: 405, body: "" });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_session", data: sessions[0] }),
    });
  });

  await page.route("/hecate/v1/chat/sessions/chat-hecate-e2e/stream", route => route.fulfill({
    status: 200,
    contentType: "text/event-stream",
    body: "",
  }));

  let messagePayload: Record<string, unknown> | null = null;
  await page.route("/hecate/v1/chat/sessions/chat-hecate-e2e/messages", async route => {
    messagePayload = await route.request().postDataJSON();
    const completed = {
      ...sessions[0],
      task_id: "task-hecate-e2e",
      latest_run_id: "run-hecate-e2e",
      status: "completed",
      message_count: 2,
      messages: [
        {
          id: "msg-user-e2e",
          runtime_kind: "agent",
          segment_id: "task:task-hecate-e2e",
          task_id: "task-hecate-e2e",
          role: "user",
          content: "show diff",
          created_at: "2026-05-06T10:00:00Z",
        },
        {
          id: "msg-assistant-e2e",
          runtime_kind: "agent",
          segment_id: "task:task-hecate-e2e",
          task_id: "task-hecate-e2e",
          run_id: "run-hecate-e2e",
          role: "assistant",
          content: "Command output:\n\n```diff\n+changed line\n```",
          provider: "lmstudio",
          model: "qwen2.5",
          status: "completed",
          cost_mode: "hecate",
          activities: [
            { id: "run", type: "task_run", status: "completed", title: "Backing task", detail: "completed · task-hecate-e2e · run-hecate-e2e" },
            { id: "done", type: "completed", status: "completed", title: "Run completed", detail: "completed" },
          ],
          created_at: "2026-05-06T10:00:01Z",
        },
      ],
    };
    sessions.splice(0, sessions.length, completed);
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_session", data: completed }),
    });
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await page.getByRole("button", { name: "New Hecate chat", exact: true }).click();
  await page.getByRole("button", { name: "Add selected" }).click();
  await expect(page.getByRole("button", { name: /Add selected/i })).toHaveCount(0);
  await expect(page.getByText("2 configured")).toBeVisible();
  await page.getByRole("button", { name: /Chat show diff, Hecate/ }).click();

  await page.getByRole("button", { name: /model picker/i }).click();
  await page.locator(".dropdown-menu").locator("text=qwen2.5").first().click();
  await expect(page.getByText("Tools on · /tmp/hecate-e2e-workspace")).toBeVisible();

  await page.locator("textarea").fill("show diff");
  await page.locator("button[type='submit']").click();

  await expect(page.locator("body")).toContainText("+changed line");
  await expect(page.locator("body")).not.toContainText("Hecate Agent run completed.");
  await expect.poll(() => messagePayload).toMatchObject({
    runtime_kind: "agent",
    model: "qwen2.5",
    workspace: "/tmp/hecate-e2e-workspace",
  });
  await expect(page.locator("body")).toContainText("qwen2.5");
});

test("Hecate Chat can move tools on, tools off, then tools on again in one transcript", async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.agentWorkspace", "/tmp/hecate-e2e-workspace");
  });
  await mockGatewayAPIs(page, {
    settingsConfig: {
      providers: [
        { id: "lmstudio", name: "LM Studio", preset_id: "lmstudio", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:1234/v1", enabled: true, credential_configured: false },
      ],
      tenants: [],
      api_keys: [],
      policy_rules: [],
    },
  });

  await page.route("/v1/models*", route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({
      object: "list",
      data: [{
        id: "qwen2.5",
        owned_by: "lmstudio",
        metadata: {
          provider: "lmstudio",
          provider_kind: "local",
          capabilities: { tool_calling: "basic", streaming: true, source: "operator_override" },
        },
      }],
    }),
  }));

  let createSessionCount = 0;
  const submittedTurns: Array<Record<string, unknown>> = [];
  const messages: Array<Record<string, unknown>> = [];
  let session: Record<string, unknown> | null = null;
  let taskApprovalResolved = false;

  function completeFirstTaskApproval() {
    taskApprovalResolved = true;
    const assistant = messages.find(message => message.id === "msg-assistant-1") as Record<string, unknown> | undefined;
    if (!assistant) return;
    assistant.content = "Tools answer one from qwen2.5";
    assistant.status = "completed";
    assistant.activities = [
      { id: "approval-1", type: "approval", status: "approved", title: "Approval approved", detail: "shell_exec - approved", approval_id: "appr-tools-1", needs_action: false },
      { id: "task-1", type: "task_run", status: "completed", title: "Backing task", detail: "completed · task-tools-1 · run-tools-1" },
      { id: "turns-1", type: "thinking", status: "completed", title: "Model turns", detail: "2 turns completed" },
    ];
    session = {
      ...(session ?? {}),
      status: "completed",
      message_count: messages.length,
      messages,
    };
  }

  await page.route("/hecate/v1/chat/sessions", async route => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_sessions", data: session ? [session] : [] }),
      });
      return;
    }
    if (route.request().method() === "POST") {
      createSessionCount += 1;
      const body = await route.request().postDataJSON();
      session = {
        id: "chat-tools-switch-e2e",
        title: body.title || "tools switch",
        runtime_kind: body.runtime_kind,
        provider: body.provider || "",
        model: body.model || "qwen2.5",
        capabilities: { tool_calling: "basic", streaming: true, source: "operator_override" },
        workspace: body.workspace,
        status: "created",
        message_count: 0,
        messages,
      };
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_session", data: session }),
      });
      return;
    }
    await route.fulfill({ status: 405, body: "" });
  });

  await page.route("/hecate/v1/chat/sessions/chat-tools-switch-e2e/stream", route => route.fulfill({
    status: 200,
    contentType: "text/event-stream",
    body: "",
  }));

  await page.route("/hecate/v1/chat/sessions/chat-tools-switch-e2e/approvals*", route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({ object: "chat_approvals", data: [] }),
  }));

  await page.route("/hecate/v1/chat/sessions/chat-tools-switch-e2e", route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({ object: "chat_session", data: session }),
  }));

  await page.route("/hecate/v1/tasks/task-tools-1/approvals/appr-tools-1/resolve", async route => {
    completeFirstTaskApproval();
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "task_approval", data: { id: "appr-tools-1", status: "approved" } }),
    });
  });

  await page.route("/hecate/v1/chat/sessions/chat-tools-switch-e2e/messages", async route => {
    const body = await route.request().postDataJSON();
    submittedTurns.push(body);
    const turn = submittedTurns.length;
    const runtimeKind = body.runtime_kind || "model";
    const isHecateAgent = runtimeKind === "agent";
    const agentTurn = submittedTurns.filter(t => t.runtime_kind === "agent").length;
    const taskID = isHecateAgent ? `task-tools-${agentTurn}` : "";
    const runID = isHecateAgent ? `run-tools-${agentTurn}` : "";
    const firstTaskNeedsApproval = isHecateAgent && agentTurn === 1 && !taskApprovalResolved;
    const assistantContent = firstTaskNeedsApproval
      ? ""
      : isHecateAgent
      ? `Tools answer ${taskID.endsWith("-1") ? "one" : "two"} from ${body.model}`
      : `Direct model answer from ${body.model}`;

    messages.push(
      {
        id: `msg-user-${turn}`,
        runtime_kind: runtimeKind,
        segment_id: isHecateAgent ? `task:${taskID}` : `model:${turn}`,
        task_id: isHecateAgent ? taskID : undefined,
        provider: body.provider || "",
        model: body.model,
        role: "user",
        content: body.content,
        created_at: `2026-05-06T10:00:0${turn}Z`,
      },
      {
        id: `msg-assistant-${turn}`,
        runtime_kind: runtimeKind,
        segment_id: isHecateAgent ? `task:${taskID}` : `model:${turn}`,
        task_id: isHecateAgent ? taskID : undefined,
        run_id: isHecateAgent ? runID : undefined,
        role: "assistant",
        content: assistantContent,
        provider: body.provider || "",
        model: body.model,
        status: firstTaskNeedsApproval ? "awaiting_approval" : "completed",
        cost_mode: isHecateAgent ? "hecate" : "provider",
        activities: firstTaskNeedsApproval
          ? [
              { id: "approval-1", type: "approval", status: "awaiting_approval", kind: "agent_loop_tool_call", title: "Awaiting approval", detail: "Agent requested tools that require approval: shell_exec", approval_id: "appr-tools-1", needs_action: true },
              { id: `task-${turn}`, type: "task_run", status: "awaiting_approval", title: "Backing task", detail: `awaiting approval · ${taskID} · ${runID}` },
            ]
          : isHecateAgent
          ? [
              { id: `task-${turn}`, type: "task_run", status: "completed", title: "Backing task", detail: `completed · ${taskID} · ${runID}` },
              { id: `done-${turn}`, type: "completed", status: "completed", title: "Run completed", detail: "completed" },
            ]
          : [],
        created_at: `2026-05-06T10:00:0${turn}Z`,
      },
    );

    session = {
      ...(session ?? {}),
      runtime_kind: runtimeKind,
      provider: body.provider || "",
      model: body.model,
      capabilities: { tool_calling: "basic", streaming: true, source: "operator_override" },
      workspace: body.workspace || "/tmp/hecate-e2e-workspace",
      task_id: isHecateAgent ? taskID : session?.task_id,
      latest_run_id: isHecateAgent ? runID : session?.latest_run_id,
      status: firstTaskNeedsApproval ? "awaiting_approval" : "completed",
      message_count: messages.length,
      messages,
    };

    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_session", data: session }),
    });
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.getByRole("button", { name: "New Hecate chat", exact: true }).click();

  await page.getByRole("button", { name: /model picker/i }).click();
  await page.locator(".dropdown-menu").locator("text=qwen2.5").first().click();
  await expect(page.getByText("Tools on · /tmp/hecate-e2e-workspace")).toBeVisible();

  await page.getByRole("textbox", { name: "Message" }).fill("first with tools");
  await page.locator("button[type='submit']").click();
  await expect(page.getByTestId("hecate-task-approval-banner")).toBeVisible();
  await page.getByRole("button", { name: /Approve Agent tool call/i }).click();
  await expect(page.getByTestId("hecate-task-approval-banner")).toBeHidden();
  await expect(page.locator("body")).toContainText("Tools answer one from qwen2.5");

  await page.getByRole("button", { name: "Chat settings" }).click();
  await page.getByRole("button", { name: "Tools on", exact: true }).click();
  await expect(page.getByText("Tools off · /tmp/hecate-e2e-workspace")).toBeVisible();
  await page.getByRole("textbox", { name: "Message" }).fill("direct model turn");
  await page.locator("button[type='submit']").click();
  await expect(page.locator("body")).toContainText("Direct model answer from qwen2.5");

  await page.getByRole("button", { name: "Tools off", exact: true }).click();
  await expect(page.getByText("Tools on · /tmp/hecate-e2e-workspace")).toBeVisible();
  await page.getByRole("textbox", { name: "Message" }).fill("tools again");
  await page.locator("button[type='submit']").click();
  await expect(page.locator("body")).toContainText("Tools answer two from qwen2.5");
  await expect(page.getByLabel("Tools on segment using qwen2.5")).toHaveCount(2);
  await expect(page.getByLabel("Tools off segment using qwen2.5")).toHaveCount(1);

  expect(createSessionCount).toBe(1);
  expect(submittedTurns.map(turn => turn.runtime_kind)).toEqual(["agent", "model", "agent"]);
  expect(submittedTurns.map(turn => turn.content)).toEqual(["first with tools", "direct model turn", "tools again"]);
  expect(submittedTurns.filter(turn => turn.runtime_kind === "agent")).toEqual([
    expect.objectContaining({ model: "qwen2.5", workspace: "/tmp/hecate-e2e-workspace" }),
    expect.objectContaining({ model: "qwen2.5", workspace: "/tmp/hecate-e2e-workspace" }),
  ]);
});

test("Hecate Chat rehydrates an active task and blocks direct sends after refresh", async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.chatSessionID", "chat-busy-e2e");
    window.localStorage.setItem("hecate.chatTargetBySessionID", JSON.stringify({ "chat-busy-e2e": "model" }));
    window.localStorage.setItem("hecate.agentWorkspace", "/tmp/hecate-e2e-workspace");
  });
  await mockGatewayAPIs(page, {
    settingsConfig: {
      providers: [
        { id: "lmstudio", name: "LM Studio", preset_id: "lmstudio", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:1234/v1", enabled: true, credential_configured: false },
      ],
      tenants: [],
      api_keys: [],
      policy_rules: [],
    },
  });

  const session = {
    id: "chat-busy-e2e",
    title: "busy tools turn",
    runtime_kind: "model",
    provider: "lmstudio",
    model: "qwen2.5",
    capabilities: { tool_calling: "basic", streaming: true, source: "operator_override" },
    workspace: "/tmp/hecate-e2e-workspace",
    task_id: "task-busy-e2e",
    latest_run_id: "run-busy-e2e",
    status: "running",
    message_count: 2,
    segments: [
      { id: "model:first", runtime_kind: "model", provider: "lmstudio", model: "qwen2.5", status: "completed", message_count: 2 },
      { id: "task:task-busy-e2e", runtime_kind: "agent", provider: "lmstudio", model: "qwen2.5", task_id: "task-busy-e2e", latest_run_id: "run-busy-e2e", status: "running", message_count: 1 },
    ],
    messages: [
      { id: "msg-user", runtime_kind: "agent", segment_id: "task:task-busy-e2e", task_id: "task-busy-e2e", role: "user", content: "inspect", created_at: "2026-05-06T10:00:00Z" },
      {
        id: "msg-assistant",
        runtime_kind: "agent",
        segment_id: "task:task-busy-e2e",
        task_id: "task-busy-e2e",
        run_id: "run-busy-e2e",
        role: "assistant",
        content: "",
        status: "running",
        model: "qwen2.5",
        created_at: "2026-05-06T10:00:01Z",
        activities: [
          { id: "hecate_task_run:run-busy-e2e", type: "task_run", status: "running", title: "Backing task", detail: "running" },
        ],
      },
    ],
  };
  let messagePosts = 0;

  await page.route("/v1/models*", route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({
      object: "list",
      data: [{
        id: "qwen2.5",
        owned_by: "lmstudio",
        metadata: { provider: "lmstudio", provider_kind: "local", capabilities: { tool_calling: "basic", streaming: true, source: "operator_override" } },
      }],
    }),
  }));
  await page.route("/hecate/v1/chat/sessions", route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({ object: "chat_sessions", data: [session] }),
  }));
  await page.route("/hecate/v1/chat/sessions/chat-busy-e2e", route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({ object: "chat_session", data: session }),
  }));
  await page.route("/hecate/v1/chat/sessions/chat-busy-e2e/stream", route => route.fulfill({
    status: 200,
    contentType: "text/event-stream",
    body: "",
  }));
  await page.route("/hecate/v1/chat/sessions/chat-busy-e2e/approvals*", route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({ object: "chat_approvals", data: [] }),
  }));
  await page.route("/hecate/v1/chat/sessions/chat-busy-e2e/messages", route => {
    messagePosts += 1;
    return route.fulfill({ status: 500, body: "send should be blocked by the UI" });
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await expect(page.getByText("Tools off · /tmp/hecate-e2e-workspace")).toBeVisible();
  await expect(page.getByRole("button", { name: "Fixed model: qwen2.5" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Stop active task" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Open task" })).toBeVisible();
  await expect(page.getByText(/Hecate Chat is still working on this task/)).toBeVisible();
  await expect(page.getByText("Backing task")).toBeVisible();

  await page.locator("textarea").fill("try direct while busy");
  await expect(page.getByRole("button", { name: "Queue message" })).toBeVisible();
  await page.getByRole("button", { name: "Queue message" }).click();
  await expect(page.getByLabel("Queued messages")).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Queued message 1" })).toHaveValue("try direct while busy");
  expect(messagePosts).toBe(0);
});

test("Hecate Chat rehydrates an awaiting-approval task and resolves it after refresh", async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.chatSessionID", "chat-approval-refresh-e2e");
    window.localStorage.setItem("hecate.agentWorkspace", "/tmp/hecate-e2e-workspace");
  });
  await mockGatewayAPIs(page, {
    settingsConfig: {
      providers: [
        { id: "lmstudio", name: "LM Studio", preset_id: "lmstudio", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:1234/v1", enabled: true, credential_configured: false },
      ],
      tenants: [],
      api_keys: [],
      policy_rules: [],
    },
  });

  let approvalResolved = false;
  const pendingSession = {
    id: "chat-approval-refresh-e2e",
    title: "approval refresh",
    runtime_kind: "agent",
    provider: "lmstudio",
    model: "qwen2.5",
    capabilities: { tool_calling: "basic", streaming: true, source: "operator_override" },
    workspace: "/tmp/hecate-e2e-workspace",
    task_id: "task-approval-refresh-e2e",
    latest_run_id: "run-approval-refresh-e2e",
    status: "awaiting_approval",
    message_count: 2,
    segments: [
      { id: "task:task-approval-refresh-e2e", runtime_kind: "agent", provider: "lmstudio", model: "qwen2.5", task_id: "task-approval-refresh-e2e", latest_run_id: "run-approval-refresh-e2e", status: "awaiting_approval", message_count: 2 },
    ],
    messages: [
      { id: "msg-user-approval", runtime_kind: "agent", segment_id: "task:task-approval-refresh-e2e", task_id: "task-approval-refresh-e2e", role: "user", content: "show git diff", created_at: "2026-05-06T10:00:00Z" },
      {
        id: "msg-assistant-approval",
        runtime_kind: "agent",
        segment_id: "task:task-approval-refresh-e2e",
        task_id: "task-approval-refresh-e2e",
        run_id: "run-approval-refresh-e2e",
        role: "assistant",
        content: "",
        status: "awaiting_approval",
        model: "qwen2.5",
        created_at: "2026-05-06T10:00:01Z",
        activities: [
          { id: "task:approval:appr-refresh-e2e", type: "approval", status: "awaiting_approval", kind: "agent_loop_tool_call", title: "Awaiting approval", detail: "Agent requested tools that require approval: git_exec", approval_id: "appr-refresh-e2e", needs_action: true },
          { id: "hecate_task_run:run-approval-refresh-e2e", type: "task_run", status: "awaiting_approval", title: "Backing task", detail: "awaiting approval" },
        ],
      },
    ],
  };
  const completedSession = {
    ...pendingSession,
    status: "completed",
    messages: [
      pendingSession.messages[0],
      {
        ...pendingSession.messages[1],
        content: "The current diff touches ui/e2e/chat.spec.ts.",
        status: "completed",
        activities: [
          { id: "task:approval:appr-refresh-e2e", type: "approval", status: "approved", kind: "agent_loop_tool_call", title: "Approval approved", detail: "git_exec - approved", approval_id: "appr-refresh-e2e", needs_action: false },
          { id: "hecate_task_run:run-approval-refresh-e2e", type: "task_run", status: "completed", title: "Backing task", detail: "completed" },
          { id: "turns-refresh", type: "thinking", status: "completed", title: "Model turns", detail: "2 turns completed" },
        ],
      },
    ],
    segments: [
      { ...pendingSession.segments[0], status: "completed" },
    ],
  };
  const currentSession = () => approvalResolved ? completedSession : pendingSession;

  await page.route("/v1/models*", route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({
      object: "list",
      data: [{
        id: "qwen2.5",
        owned_by: "lmstudio",
        metadata: { provider: "lmstudio", provider_kind: "local", capabilities: { tool_calling: "basic", streaming: true, source: "operator_override" } },
      }],
    }),
  }));
  await page.route("/hecate/v1/chat/sessions", route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({ object: "chat_sessions", data: [currentSession()] }),
  }));
  await page.route("/hecate/v1/chat/sessions/chat-approval-refresh-e2e", route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({ object: "chat_session", data: currentSession() }),
  }));
  await page.route("/hecate/v1/chat/sessions/chat-approval-refresh-e2e/stream", route => route.fulfill({
    status: 200,
    contentType: "text/event-stream",
    body: "",
  }));
  await page.route("/hecate/v1/chat/sessions/chat-approval-refresh-e2e/approvals*", route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({ object: "chat_approvals", data: [] }),
  }));
  await page.route("/hecate/v1/tasks/task-approval-refresh-e2e/approvals/appr-refresh-e2e/resolve", async route => {
    approvalResolved = true;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "task_approval", data: { id: "appr-refresh-e2e", status: "approved" } }),
    });
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await expect(page.getByTestId("hecate-task-approval-banner")).toBeVisible();
  await expect(page.getByText(/Hecate Chat is still working on this task/)).toBeVisible();
  await expect(page.getByTestId("hecate-task-approval-banner").getByRole("button", { name: "Open task", exact: true })).toBeVisible();
  await expect(page.locator("button[type='submit']")).toHaveCount(0);

  await page.getByRole("button", { name: /Approve Agent tool call/i }).click();
  await expect(page.getByTestId("hecate-task-approval-banner")).toBeHidden();
  await expect(page.locator("body")).toContainText("The current diff touches ui/e2e/chat.spec.ts.");
  await expect(page.getByRole("button", { name: "Send message" })).toBeVisible();
});

test("configured provider with no models shows troubleshooting, not detected-provider setup", async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await mockGatewayAPIs(page, {
    settingsConfig: {
      providers: [
        { id: "ollama", name: "Ollama", preset_id: "ollama", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:11434/v1", enabled: true, credential_configured: false },
      ],
      tenants: [],
      api_keys: [],
      policy_rules: [],
    },
  });
  await page.route("/v1/models*", route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({ object: "list", data: [] }),
  }));
  await page.route("/hecate/v1/providers/status*", route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({
      object: "provider_status",
      data: [{
        name: "ollama",
        kind: "local",
        healthy: true,
        status: "healthy",
        base_url: "http://127.0.0.1:11434/v1",
        models: [],
        model_count: 0,
      }],
    }),
  }));

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await switchToModel(page);

  await expect(page.getByText("Nothing runnable yet")).toBeVisible();
  await expect(page.getByText(/Add a model provider or install a supported coding-agent CLI before sending a message/)).toBeVisible();
  await expect(page.getByText("Detected locally")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Add selected/i })).toHaveCount(0);
});

test("selected-model readiness can switch to the backend-suggested fallback model", async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await mockGatewayAPIs(page, {
    settingsConfig: {
      providers: [
        { id: "anthropic", name: "Anthropic", preset_id: "anthropic", kind: "cloud", protocol: "anthropic", base_url: "https://api.anthropic.com/v1", enabled: true, credential_configured: false },
        { id: "openai", name: "OpenAI", preset_id: "openai", kind: "cloud", protocol: "openai", base_url: "https://api.openai.com/v1", enabled: true, credential_configured: true },
      ],
      tenants: [],
      api_keys: [],
      policy_rules: [],
    },
  });
  await page.route("/v1/models*", route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({
      object: "list",
      data: [
        {
          id: "claude-sonnet-4-6",
          owned_by: "anthropic",
          metadata: {
            provider: "anthropic",
            provider_kind: "cloud",
            readiness: {
              ready: false,
              status: "blocked",
              reason: "credential_missing",
              message: "Anthropic needs credentials before this model can route.",
              operator_action: "Add an Anthropic API key, or use a routable fallback.",
              suggested_models: ["gpt-4o-mini"],
            },
          },
        },
        { id: "gpt-4o-mini", owned_by: "openai", metadata: { provider: "openai", provider_kind: "cloud" } },
      ],
    }),
  }));
  await page.route("/hecate/v1/providers/status*", route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({
      object: "provider_status",
      data: [
        {
          name: "anthropic",
          kind: "cloud",
          healthy: true,
          status: "healthy",
          models: ["claude-sonnet-4-6"],
          model_count: 1,
          routing_ready: false,
          routing_blocked_reason: "credential_missing",
        },
        {
          name: "openai",
          kind: "cloud",
          healthy: true,
          status: "healthy",
          models: ["gpt-4o-mini"],
          model_count: 1,
          routing_ready: true,
        },
      ],
    }),
  }));
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "model");
    window.localStorage.setItem("hecate.providerFilter", "anthropic");
    window.localStorage.setItem("hecate.model", "claude-sonnet-4-6");
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.getByRole("button", { name: "New Hecate chat", exact: true }).click();

  await expect(page.getByRole("button", { name: /model picker/i })).toContainText("gpt-4o-mini");
  await page.locator("textarea").fill("hello");
  await expect(page.locator("button[type='submit']")).toBeEnabled();
});

// External-agent approval happy path. Seeds an active session with one
// pending approval, then exercises the operator path: catch-up refetch
// populates the banner, Review opens the modal, Allow resolves, and the
// banner clears.
test("agent approval banner: review, allow, banner clears", async ({ page }) => {
  // Seed the persisted active session before the page loads, so the
  // dashboard fan-out runs the catch-up refetch on mount.
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatSessionID", "a-e2e-1");
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
  });

  // The dashboard fan-out asks for /hecate/v1/chat/sessions on mount
  // and prunes any stored activeSessionID that isn't in the list. So
  // a-e2e-1 must appear here for the catch-up refetch to fire.
  await page.route("/hecate/v1/chat/sessions", (route) => {
    if (route.request().method() !== "GET") {
      void route.fulfill({ status: 405, body: "" });
      return;
    }
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_sessions",
        data: [{
          id: "a-e2e-1",
          title: "E2E approval test",
          runtime_kind: "external_agent",
          adapter_id: "codex",
          status: "running",
          message_count: 0,
        }],
      }),
    });
  });

  // Approvals refetch — returns the pending row until the resolve
  // POST fires, then returns an empty list. This matches what a
  // backend would actually do (the row is the source of truth, not
  // the call count) and stays stable under React 19 strict-mode
  // double-fires of the catch-up effect.
  let approvalResolved = false;
  await page.route("/hecate/v1/chat/sessions/a-e2e-1/approvals*", (route) => {
    if (approvalResolved) {
      void route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "list", data: [] }),
      });
      return;
    }
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "list",
        data: [{
          id: "ap-e2e-1",
          session_id: "a-e2e-1",
          adapter_id: "codex",
          tool_kind: "fs",
          tool_name: "write_file",
          status: "pending",
          acp_options: [
            { option_id: "approve_once", kind: "allow_once", name: "Approve once" },
          ],
          scope_choices: ["once", "session"],
          created_at: "2026-04-21T10:00:00Z",
          expires_at: "2026-04-21T10:05:00Z",
        }],
      }),
    });
  });

  await page.route("/hecate/v1/chat/sessions/a-e2e-1/approvals/ap-e2e-1", (route) => {
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_approval",
        data: {
          id: "ap-e2e-1",
          session_id: "a-e2e-1",
          adapter_id: "codex",
          tool_kind: "fs",
          tool_name: "write_file",
          status: "pending",
          acp_options: [
            { option_id: "approve_once", kind: "allow_once", name: "Approve once" },
          ],
          scope_choices: ["once", "session"],
          created_at: "2026-04-21T10:00:00Z",
          expires_at: "2026-04-21T10:05:00Z",
        },
      }),
    });
  });

  let resolveCalls = 0;
  await page.route("/hecate/v1/chat/sessions/a-e2e-1/approvals/ap-e2e-1/resolve", (route) => {
    resolveCalls += 1;
    approvalResolved = true;
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_approval",
        data: {
          id: "ap-e2e-1",
          session_id: "a-e2e-1",
          adapter_id: "codex",
          tool_kind: "fs",
          status: "resolved",
          acp_options: [],
          decision: "approve",
          scope: "once",
          path: "operator",
          created_at: "2026-04-21T10:00:00Z",
          expires_at: "2026-04-21T10:05:00Z",
          resolved_at: "2026-04-21T10:00:30Z",
        },
      }),
    });
  });

  // Override the default agent-chat route so the active session
  // resolves to a real record (the default mock returns 404 for any
  // POST/PATCH/etc.; GET-by-id is unstubbed and we want a 200 here).
  await page.route("/hecate/v1/chat/sessions/a-e2e-1", (route) => {
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_session",
        data: {
          id: "a-e2e-1",
          title: "E2E approval test",
          runtime_kind: "external_agent",
          adapter_id: "codex",
          workspace: "/tmp/e2e",
          status: "running",
          messages: [],
        },
      }),
    });
  });

  await page.reload();
  await page.waitForSelector(".hecate-activitybar");

  // Pending banner shows up after the catch-up refetch fires.
  const banner = page.getByTestId("agent-approval-banner");
  await expect(banner).toBeVisible();
  await expect(banner.getByTestId("agent-approval-banner-review")).toBeVisible();

  // Click Review — modal opens and fetches the full row.
  await banner.getByTestId("agent-approval-banner-review").click();
  await expect(page.getByTestId("agent-approval-modal-submit")).toBeVisible();
  await expect(page.getByTestId("agent-approval-modal-loading")).toBeHidden();

  // Allow with the seeded defaults.
  await page.getByTestId("agent-approval-modal-submit").click();

  // Modal closes; banner clears (second refetch returned an empty
  // list, and the optimistic remove also fires).
  await expect(page.getByTestId("agent-approval-modal-submit")).toBeHidden();
  await expect(page.getByTestId("agent-approval-banner")).toBeHidden();
  expect(resolveCalls).toBe(1);
});

test("agent changed-files review inspects and reverts a captured file", async ({ page }) => {
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatSessionID", "a-diff-1");
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
  });

  await page.route("/hecate/v1/chat/sessions", (route) => {
    if (route.request().method() !== "GET") {
      void route.fulfill({ status: 405, body: "" });
      return;
    }
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_sessions",
        data: [{
          id: "a-diff-1",
          title: "Diff review",
          runtime_kind: "external_agent",
          adapter_id: "codex",
          status: "completed",
          message_count: 2,
        }],
      }),
    });
  });

  const sessionBody = {
    object: "chat_session",
    data: {
      id: "a-diff-1",
      title: "Diff review",
      runtime_kind: "external_agent",
      adapter_id: "codex",
      workspace: "/tmp/e2e",
      status: "completed",
      messages: [
        { id: "m-user", role: "user", content: "update docs", created_at: "2026-04-21T10:00:00Z" },
        {
          id: "m-agent",
          role: "assistant",
          content: "Updated the docs.",
          adapter_id: "codex",
          adapter_name: "Codex",
          status: "completed",
          diff_stat: "README.md | 3 ++-\ndocs/runtime-api.md | 4 ++++\n2 files changed, 6 insertions(+), 1 deletion(-)",
          diff: "diff --git a/README.md b/README.md\n+new line",
          created_at: "2026-04-21T10:00:01Z",
        },
      ],
    },
  };

  await page.route("/hecate/v1/chat/sessions/a-diff-1", (route) => {
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(sessionBody),
    });
  });

  await page.route("/hecate/v1/chat/sessions/a-diff-1/approvals*", (route) => {
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "list", data: [] }),
    });
  });

  await page.route("/hecate/v1/chat/sessions/a-diff-1/messages/m-agent/files", (route) => {
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_changed_files",
        data: [
          { path: "README.md", additions: 2, deletions: 1, status: "modified" },
          { path: "docs/runtime-api.md", additions: 4, deletions: 0, status: "added" },
        ],
      }),
    });
  });

  await page.route("/hecate/v1/chat/sessions/a-diff-1/messages/m-agent/files/README.md", (route) => {
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_changed_file_diff",
        data: {
          path: "README.md",
          additions: 2,
          deletions: 1,
          status: "modified",
          diff: "diff --git a/README.md b/README.md\n+new line",
        },
      }),
    });
  });

  let revertedPaths: string[] | null = null;
  await page.route("/hecate/v1/chat/sessions/a-diff-1/messages/m-agent/revert", async (route) => {
    revertedPaths = (await route.request().postDataJSON()).paths;
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_revert",
        data: {
          reverted: true,
          paths: revertedPaths,
          diff_stat: "docs/runtime-api.md | 4 ++++\n1 file changed, 4 insertions(+)",
          files: [{ path: "docs/runtime-api.md", additions: 4, deletions: 0, status: "added" }],
        },
      }),
    });
  });

  await page.reload();
  await page.waitForSelector(".hecate-activitybar");

  await expect(page.getByText("Updated the docs.")).toBeVisible();
  await page.getByText("files changed · 2 files changed, 6 insertions(+), 1 deletion(-)").click();
  await expect(page.getByText("2 changed files")).toBeVisible();

  await page.getByRole("button", { name: "Inspect README.md" }).click();
  await expect(page.getByText("diff · README.md")).toBeVisible();
  await expect(page.locator("body")).toContainText("+new line");

  await page.getByRole("button", { name: "Revert README.md" }).click();
  await expect(page.getByRole("button", { name: "Confirm revert README.md" })).toBeVisible();
  await page.getByRole("button", { name: "Confirm revert README.md" }).click();
  await expect.poll(() => revertedPaths).toEqual(["README.md"]);
});

type ClaudeAdapterFixture = {
  available?: boolean;
  authStatus?: string;
  credentialConfigured?: boolean;
  healthStatus?: "ready" | "auth_required" | "not_installed" | "error";
  credentialPreview?: string;
};

async function openClaudeExternalAgent(page: Page, fixture: ClaudeAdapterFixture = {}) {
  const adapter = {
    id: "claude_code",
    name: "Claude Code",
    kind: "acp",
    command: "claude-agent-acp",
    managed: true,
    managed_package: "@agentclientprotocol/claude-agent-acp",
    available: fixture.available ?? true,
    status: fixture.available === false ? "missing" : "available",
    error: fixture.available === false ? "claude command not found" : undefined,
    description: "Run Claude Agent through ACP as a long-lived external coding-agent session supervised by Hecate.",
    cost_mode: "external",
    auth_status: fixture.authStatus ?? "unknown",
    auth_error: fixture.authStatus === "unauthenticated" ? "Run claude auth login" : undefined,
    credential_configured: fixture.credentialConfigured ?? false,
    credential_preview: fixture.credentialPreview,
    claude_code_cli: fixture.available === false ? { available: false } : { available: true, path: "/usr/local/bin/claude" },
  };

  await page.route("/hecate/v1/agent-adapters*", async route => {
    const method = route.request().method();
    const url = route.request().url();
    if (method === "POST" && url.includes("/claude_code/probe")) {
      const status = fixture.available === false ? "not_installed" : (fixture.healthStatus ?? "ready");
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          object: "agent_adapter_probe",
          data: {
            adapter: status === "ready" && fixture.credentialConfigured
              ? { ...adapter, auth_status: "ok", auth_error: undefined }
              : adapter,
            health: {
              adapter_id: "claude_code",
              status,
              stage: status === "ready" ? "ready" : status === "not_installed" ? "lookup" : "new_session",
              path: fixture.available === false ? undefined : "/usr/local/bin/claude-agent-acp",
              hint: status === "auth_required" ? "Claude Code isn't signed in." : undefined,
              error: status === "not_installed" ? "claude command not found" : undefined,
              duration_ms: 20,
            },
          },
        }),
      });
      return;
    }
    if (method === "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "agent_adapters", data: [adapter] }),
      });
      return;
    }
    await route.continue();
  });

  const session = {
    id: "claude-code-onboarding-e2e",
    title: "Claude Code chat",
    runtime_kind: "external_agent",
    adapter_id: "claude_code",
    adapter_name: "Claude Code",
    driver_kind: "acp",
    native_session_id: "native-claude-code-e2e",
    workspace: "/tmp/hecate-e2e",
    status: "idle",
    message_count: 0,
    config_options: [],
    messages: [],
    created_at: "2026-05-14T10:00:00Z",
    updated_at: "2026-05-14T10:00:00Z",
  };
  await page.route("/hecate/v1/chat/sessions*", async route => {
    const method = route.request().method();
    const url = new URL(route.request().url());
    const path = url.pathname;
    if (method === "GET" && path === "/hecate/v1/chat/sessions") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_sessions", data: [session] }),
      });
      return;
    }
    if (
      method === "GET"
      && path.startsWith("/hecate/v1/chat/sessions/claude-code-onboarding-e2e")
      && !path.endsWith("/stream")
      && !path.endsWith("/approvals")
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_session", data: session }),
      });
      return;
    }
    if (method === "GET" && path === "/hecate/v1/chat/sessions/claude-code-onboarding-e2e/stream") {
      await route.fulfill({ status: 200, contentType: "text/event-stream", body: "" });
      return;
    }
    if (method === "GET" && path === "/hecate/v1/chat/sessions/claude-code-onboarding-e2e/approvals") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_approvals", data: [] }),
      });
      return;
    }
    await route.continue();
  });
  await page.route("/hecate/v1/chat/sessions/claude-code-onboarding-e2e", async route => {
    if (route.request().method() !== "GET") {
      await route.fulfill({ status: 405, body: "" });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_session", data: session }),
    });
  });
  await page.route("/hecate/v1/chat/sessions/claude-code-onboarding-e2e/stream", route => (
    route.fulfill({ status: 200, contentType: "text/event-stream", body: "" })
  ));
  await page.route("/hecate/v1/chat/sessions/claude-code-onboarding-e2e/approvals*", route => (
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_approvals", data: [] }),
    })
  ));

  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.agentAdapterID", "claude_code");
    window.localStorage.setItem("hecate.agentWorkspace", "/tmp/hecate-e2e");
    window.localStorage.setItem("hecate.chatSessionID", "claude-code-onboarding-e2e");
  });
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.getByRole("button", { name: /Chat Claude Code chat, Claude Code/ }).click();
}

test("Claude Code onboarding appears when the adapter is not installed", async ({ page }) => {
  await openClaudeExternalAgent(page, { available: false, healthStatus: "not_installed" });

  await expect(page.getByText("Set up Claude Code")).toBeVisible();
  await expect(page.getByText(/Prepare Claude Code/).first()).toBeVisible();
  await expect(page.getByRole("button", { name: "npx -y @anthropic-ai/claude-code --version" })).toBeVisible();
  await expect(page.locator("button[type='submit']")).toHaveCount(0);
});

test("Claude Code onboarding stays visible after a ready handshake without auth", async ({ page }) => {
  await openClaudeExternalAgent(page, { available: true, authStatus: "unknown", credentialConfigured: false, healthStatus: "ready" });

  await expect(page.getByTestId("claude-code-preflight")).toBeVisible();
  await page.getByRole("button", { name: "Check auth" }).click();
  await expect(page.getByTestId("claude-code-preflight")).toBeVisible();
  await expect(page.getByText(/Claude Code needs its own adapter-visible credential/)).toBeVisible();
  await expect(page.locator("button[type='submit']")).toHaveCount(0);
});

test("Claude Code onboarding stays visible when a saved token fails auth", async ({ page }) => {
  await openClaudeExternalAgent(page, {
    available: true,
    authStatus: "unknown",
    credentialConfigured: true,
    credentialPreview: "sk-a...bad1",
    healthStatus: "auth_required",
  });

  await expect(page.getByTestId("claude-code-preflight")).toBeVisible();
  await expect(page.getByText("Set up Claude Code")).toBeVisible();
  await expect(page.getByText(/Claude Code needs sign-in|Claude Code needs its own adapter-visible credential/)).toBeVisible();
  await expect(page.locator("button[type='submit']")).toHaveCount(0);
});

test("Claude Code onboarding stays visible when only CLI auth is verified", async ({ page }) => {
  await openClaudeExternalAgent(page, {
    available: true,
    authStatus: "ok",
    credentialConfigured: false,
    healthStatus: "ready",
  });

  await expect(page.getByTestId("claude-code-preflight")).toBeVisible();
  await expect(page.getByText("adapter installed")).toBeVisible();
  await expect(page.getByText("token not saved")).toBeVisible();
  await expect(page.getByText("CLI signed in")).toBeVisible();
  await expect(page.locator("button[type='submit']")).toHaveCount(0);
});

test("Claude Code rejects malformed token saves before hiding onboarding", async ({ page }) => {
  await page.route("/hecate/v1/agent-adapters/claude_code/credentials", async route => {
    if (route.request().method() !== "PUT") return route.continue();
    await route.fulfill({
      status: 400,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          type: "invalid_request",
          message: "Claude Code setup tokens start with sk- and are printed by `claude setup-token`",
          user_message: "That does not look like a Claude Code setup token, so Hecate did not save it.",
        },
      }),
    });
  });
  await openClaudeExternalAgent(page, { available: true, authStatus: "unknown", credentialConfigured: false, healthStatus: "ready" });

  await page.getByLabel("Claude Code OAuth token").fill("random text");
  await page.getByRole("button", { name: "Save" }).click();

  await expect(page.getByText(/does not look like a Claude Code setup token|setup tokens start with sk-/)).toBeVisible();
  await expect(page.getByTestId("claude-code-preflight")).toBeVisible();
  await expect(page.locator("button[type='submit']")).toHaveCount(0);
});

test("Claude Code valid token save clears onboarding and enables chat", async ({ page }) => {
  let saved = false;
  await page.route("/hecate/v1/agent-adapters/claude_code/credentials", async route => {
    if (route.request().method() !== "PUT") return route.continue();
    const body = JSON.parse(route.request().postData() ?? "{}") as { value?: string };
    if (!body.value?.startsWith("sk-")) {
      await route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: { type: "invalid_request", message: "bad token" } }) });
      return;
    }
    saved = true;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "agent_adapter_credential", data: { adapter_id: "claude_code", name: "CLAUDE_CODE_OAUTH_TOKEN", configured: true, preview: "sk-v...7890" } }),
    });
  });
  await page.route("/hecate/v1/agent-adapters*", async route => {
    const method = route.request().method();
    const url = route.request().url();
    const configured = saved;
    const adapter = {
      id: "claude_code",
      name: "Claude Code",
      kind: "acp",
      command: "claude-agent-acp",
      managed: true,
      managed_package: "@agentclientprotocol/claude-agent-acp",
      available: true,
      status: "available",
      cost_mode: "external",
      auth_status: configured ? "ok" : "unknown",
      credential_configured: configured,
      credential_preview: configured ? "sk-v...7890" : undefined,
      claude_code_cli: { available: true, path: "/usr/local/bin/claude" },
    };
    if (method === "POST" && url.includes("/claude_code/probe")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "agent_adapter_probe", data: { adapter, health: { adapter_id: "claude_code", status: "ready", stage: "ready", path: "/usr/local/bin/claude-agent-acp", duration_ms: 20 } } }),
      });
      return;
    }
    if (method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ object: "agent_adapters", data: [adapter] }) });
      return;
    }
    await route.continue();
  });
  const session = {
    id: "claude-code-token-e2e",
    title: "Claude Code chat",
    runtime_kind: "external_agent",
    adapter_id: "claude_code",
    adapter_name: "Claude Code",
    driver_kind: "acp",
    native_session_id: "native-claude-token-e2e",
    workspace: "/tmp/hecate-e2e",
    status: "idle",
    message_count: 0,
    config_options: [],
    messages: [],
    created_at: "2026-05-14T10:00:00Z",
    updated_at: "2026-05-14T10:00:00Z",
  };
  await page.route("/hecate/v1/chat/sessions*", async route => {
    const method = route.request().method();
    const path = new URL(route.request().url()).pathname;
    if (method === "GET" && path === "/hecate/v1/chat/sessions") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_sessions", data: [session] }),
      });
      return;
    }
    if (
      method === "GET"
      && path.startsWith("/hecate/v1/chat/sessions/claude-code-token-e2e")
      && !path.endsWith("/stream")
      && !path.endsWith("/approvals")
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_session", data: session }),
      });
      return;
    }
    if (method === "GET" && path === "/hecate/v1/chat/sessions/claude-code-token-e2e/stream") {
      await route.fulfill({ status: 200, contentType: "text/event-stream", body: "" });
      return;
    }
    if (method === "GET" && path === "/hecate/v1/chat/sessions/claude-code-token-e2e/approvals") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_approvals", data: [] }),
      });
      return;
    }
    await route.continue();
  });
  await page.route("/hecate/v1/chat/sessions/claude-code-token-e2e", async route => {
    if (route.request().method() !== "GET") {
      await route.fulfill({ status: 405, body: "" });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_session", data: session }),
    });
  });
  await page.route("/hecate/v1/chat/sessions/claude-code-token-e2e/stream", route => (
    route.fulfill({ status: 200, contentType: "text/event-stream", body: "" })
  ));
  await page.route("/hecate/v1/chat/sessions/claude-code-token-e2e/approvals*", route => (
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_approvals", data: [] }),
    })
  ));
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.agentAdapterID", "claude_code");
    window.localStorage.setItem("hecate.agentWorkspace", "/tmp/hecate-e2e");
    window.localStorage.setItem("hecate.chatSessionID", "claude-code-token-e2e");
  });
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.getByRole("button", { name: /Chat Claude Code chat, Claude Code/ }).click();

  await expect(page.getByTestId("claude-code-preflight")).toBeVisible();
  await page.getByLabel("Claude Code OAuth token").fill("sk-valid-token-1234567890");
  await page.getByRole("button", { name: "Save" }).click();

  await expect(page.getByTestId("claude-code-preflight")).toHaveCount(0);
  await expect(page.locator("textarea")).toBeVisible();
  await page.locator("textarea").fill("hello from Claude Code");
  await expect(page.locator("button[type='submit']")).toBeEnabled();
});
