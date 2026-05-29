import {
  expect,
  test as baseTest,
  mockGatewayAPIs,
  MOCK_MODELS,
  MOCK_PROVIDERS,
  MOCK_SETTINGS_CONFIG_WITH_PROVIDERS,
} from "./fixtures";
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
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.project", "proj_e2e");
  });
  await page.goto("/");
  // Chat is the default workspace
  await page.waitForSelector(".hecate-activitybar");
});

async function startHecateChat(page: Page) {
  await page.getByRole("button", { name: "New Hecate chat", exact: true }).click();
  await expect(
    page
      .getByText(
        /Ready when you are|Choose a workspace|No routable model|No models discovered|Nothing runnable yet/,
      )
      .first(),
  ).toBeVisible();
}

async function chooseComposerModel(page: Page, model = "claude-sonnet-4-6") {
  const modelBtn = page.getByRole("button", { name: /model picker/i });
  await expect(modelBtn).toBeVisible();
  if ((await modelBtn.textContent())?.includes(model)) return;

  await modelBtn.click();
  const menu = page.locator(".dropdown-menu").first();
  await expect(menu).toBeVisible();
  const filter = menu.getByPlaceholder("Filter models...");
  if (await filter.isVisible()) {
    await filter.fill(model);
  }
  await menu.locator("[role='option']").filter({ hasText: model }).first().click();
  await expect(modelBtn).toContainText(model);
}

async function switchToModel(page: Page, selectModel = true) {
  await startHecateChat(page);
  const useModel = page.getByRole("button", { name: "Use model", exact: true });
  if (await useModel.isVisible()) {
    await useModel.click();
  }
  if (selectModel) {
    await chooseComposerModel(page);
  }
}

async function mockAvailableAgentAdapters(page: Page) {
  await page.route("/hecate/v1/agent-adapters*", async (route) => {
    if (route.request().method() !== "GET") return route.continue();
    await route.fulfill({
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
            available: true,
            status: "available",
            cost_mode: "external",
          },
          {
            id: "claude_code",
            name: "Claude Code",
            kind: "acp",
            command: "claude-agent-acp",
            available: true,
            status: "available",
            cost_mode: "external",
          },
          {
            id: "cursor_agent",
            name: "Cursor Agent",
            kind: "acp",
            command: "cursor-agent",
            available: true,
            status: "available",
            cost_mode: "external",
          },
          {
            id: "grok_build",
            name: "Grok Build",
            kind: "acp",
            command: "grok",
            args: ["agent", "stdio"],
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
      }),
    });
  });
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

  // Brand-new users land directly on the chat empty state with the
  // onboarding panel — no "click New Hecate chat to get started" detour.
  await expect(page.getByText("Nothing runnable yet")).toBeVisible();
  await page.getByRole("button", { name: "Open Connections" }).click();
  await expect(page.locator(".hecate-activitybar [aria-current='page']")).toHaveAttribute(
    "aria-label",
    /Connections/,
  );
});

test("model picker opens and lists models from mock data", async ({ page }) => {
  await switchToModel(page);
  // Wait for models to load, then open the picker
  const modelBtn = page.getByRole("button", { name: /model picker/i });
  await modelBtn.click();

  for (const m of MOCK_MODELS.filter((m) => m.metadata?.provider === "anthropic")) {
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

test("Hecate composer provider and model controls match shared chat dropdowns", async ({
  page,
}) => {
  await startHecateChat(page);

  const providerBtn = page.getByRole("button", { name: /provider picker/i });
  await expect(providerBtn).toContainText("provider");
  await expect(providerBtn).toContainText("Anthropic");

  const modelBtn = page.getByRole("button", { name: /model picker/i });
  await chooseComposerModel(page);
  await expect(modelBtn).toContainText("claude-sonnet-4-6");
  await modelBtn.click();

  const menu = page.locator(".dropdown-menu");
  await expect(menu.getByPlaceholder("Filter models...")).toBeVisible();
  await menu.getByPlaceholder("Filter models...").fill("claude");
  await expect(menu).toContainText("claude-sonnet-4-6");
  await expect(menu).not.toContainText("gpt");
});

test("selecting a model closes the picker and updates the button label", async ({ page }) => {
  await switchToModel(page, false);
  const modelBtn = page.getByRole("button", { name: /model picker/i });
  await modelBtn.click();

  await page.locator(".dropdown-menu").locator("text=claude-opus-4-7").first().click();

  await expect(page.locator(".dropdown-menu")).not.toBeVisible();
  await expect(modelBtn).toContainText("claude-opus-4-7");
});

test("provider picker shows healthy providers", async ({ page }) => {
  await switchToModel(page, false);
  const healthyProviders = MOCK_PROVIDERS.filter((p) => p.healthy);
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
  await expect(
    page.getByRole("button", { name: /Chat Hecate chat, Hecate/ }).first(),
  ).toBeVisible();
  await expect(page.locator("textarea")).toHaveValue("some prior message");
});

test("New chat creates an external-agent session with controls before the first prompt", async ({
  page,
}) => {
  let createBody: any = null;
  await page.route("/hecate/v1/agent-adapters*", async (route) => {
    if (route.request().method() !== "GET") return route.continue();
    await route.fulfill({
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
            available: true,
            status: "available",
            cost_mode: "external",
          },
          {
            id: "claude_code",
            name: "Claude Code",
            kind: "acp",
            command: "claude-agent-acp",
            available: true,
            status: "available",
            cost_mode: "external",
          },
          {
            id: "cursor_agent",
            name: "Cursor Agent",
            kind: "acp",
            command: "cursor-agent",
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
      }),
    });
  });
  await page.route("/hecate/v1/chat/sessions", async (route) => {
    if (route.request().method() !== "POST")
      return route.fulfill({
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
          agent_id: "codex",
          agent_name: "Codex",
          driver_kind: "acp",
          native_session_id: "native-codex-e2e",
          project_id: "proj_e2e",
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
    window.localStorage.setItem("hecate.project", "proj_e2e");
  });
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await expect(page.getByRole("button", { name: "New Codex chat", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "New Codex chat", exact: true }).click();

  await expect
    .poll(() => createBody)
    .toMatchObject({
      agent_id: "codex",
      workspace: "/tmp/hecate-e2e",
    });
  await expect(page.getByRole("button", { name: "Model", exact: true })).toContainText("Fast");
  await page.getByRole("button", { name: "Choose agent for new chat" }).click();
  await expect(page.getByRole("option", { name: /Codex/ })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page.getByRole("option", { name: /Claude Code/ })).not.toHaveAttribute(
    "aria-disabled",
    "true",
  );
});

test("New Hecate chat asks for workspace before creating a tools-on session", async ({ page }) => {
  let createBody: any = null;
  await page.route("/hecate/v1/chat/sessions", async (route) => {
    if (route.request().method() === "POST") {
      createBody = JSON.parse(route.request().postData() ?? "{}");
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ error: { type: "unexpected", message: "unexpected create" } }),
      });
      return;
    }
    await route.fallback();
  });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.removeItem("hecate.project");
  });
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await expect(page.getByRole("button", { name: "New Hecate chat", exact: true })).toBeDisabled();

  await expect(page.getByText("Choose a workspace", { exact: true })).toBeVisible();
  await expect(page.locator("textarea")).toHaveCount(0);
  await expect(page.getByText("Model required")).toHaveCount(0);
  await expect.poll(() => createBody).toBeNull();
});

test("New external-agent chat asks for workspace without flashing an inline error", async ({
  page,
}) => {
  let createBody: any = null;
  await page.route("/hecate/v1/agent-adapters*", async (route) => {
    if (route.request().method() !== "GET") return route.continue();
    await route.fulfill({
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
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
      }),
    });
  });
  await page.route("/hecate/v1/chat/sessions", async (route) => {
    if (route.request().method() === "POST") {
      createBody = JSON.parse(route.request().postData() ?? "{}");
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ error: { type: "unexpected", message: "unexpected create" } }),
      });
      return;
    }
    await route.fallback();
  });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.agentAdapterID", "codex");
    window.localStorage.removeItem("hecate.project");
  });
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await expect(page.getByRole("button", { name: "New Codex chat", exact: true })).toBeDisabled();

  await expect(page.getByText("Choose a workspace", { exact: true })).toBeVisible();
  await expect(page.locator("textarea")).toHaveCount(0);
  await expect(page.getByText("Workspace required")).toHaveCount(0);
  await expect.poll(() => createBody).toBeNull();
});

test("New external-agent chat with model setup shows controls and composer together", async ({
  page,
}) => {
  let createBody: any = null;
  let createdSession: any = null;
  await page.route("/hecate/v1/agent-adapters*", async (route) => {
    if (route.request().method() !== "GET") return route.continue();
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "agent_adapters",
        data: [
          {
            id: "grok_build",
            name: "Grok Build",
            kind: "acp",
            command: "grok",
            args: ["agent", "stdio"],
            available: true,
            status: "available",
            cost_mode: "external",
            config_options: [
              {
                id: "model",
                name: "Model",
                category: "model",
                type: "select",
                current_value: "__hecate_no_model_selected__",
                options: [
                  { value: "__hecate_no_model_selected__", name: "Pick a model" },
                  { value: "grok-build-0429", name: "Grok Build 0429" },
                ],
              },
            ],
          },
        ],
      }),
    });
  });
  await page.route("/hecate/v1/chat/sessions", async (route) => {
    if (route.request().method() === "POST") {
      createBody = JSON.parse(route.request().postData() ?? "{}");
      createdSession = {
        id: "grok-build-e2e",
        title: "Grok Build chat",
        agent_id: "grok_build",
        agent_name: "Grok Build",
        driver_kind: "acp",
        native_session_id: "native-grok-build-e2e",
        project_id: "proj_e2e",
        workspace: "/tmp/hecate-e2e",
        status: "idle",
        config_options: createBody.config_options ?? [],
        messages: [],
      };
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          object: "chat_session",
          data: createdSession,
        }),
      });
      return;
    }
    await route.fallback();
  });
  await page.route(
    "/hecate/v1/chat/sessions/grok-build-e2e/config-options/model",
    async (route) => {
      if (route.request().method() !== "POST") return route.fallback();
      const body = JSON.parse(route.request().postData() ?? "{}");
      createdSession = {
        ...createdSession,
        config_options: (createdSession?.config_options ?? []).map((option: any) =>
          option.id === "model" ? { ...option, current_value: String(body.value ?? "") } : option,
        ),
      };
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_session", data: createdSession }),
      });
    },
  );
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.agentAdapterID", "grok_build");
    window.localStorage.setItem("hecate.project", "proj_e2e");
  });
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await page.getByRole("button", { name: "New Grok Build chat", exact: true }).click();

  await expect.poll(() => createBody?.agent_id).toBe("grok_build");
  await expect(
    page.getByRole("button", { name: /Chat Grok Build chat, Grok Build/i }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Model", exact: true })).toContainText(
    "Pick a model",
  );
  await expect(page.locator("textarea")).toBeVisible();
  await page.locator("textarea").fill("Build a tiny app");
  await expect(page.locator("button[type='submit']")).toBeDisabled();

  await page.getByRole("button", { name: "Model", exact: true }).click();
  await page.getByRole("option", { name: "Grok Build 0429" }).click();

  await expect(page.getByRole("button", { name: "Model", exact: true })).toContainText(
    "Grok Build 0429",
  );
  await expect(page.locator("button[type='submit']")).toBeEnabled();
});

test("external-agent chat uses the shared fake lifecycle for transcript and delete", async ({
  page,
}) => {
  await mockAvailableAgentAdapters(page);
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.agentAdapterID", "claude_code");
    window.localStorage.setItem("hecate.project", "proj_e2e");
  });
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await page.getByRole("button", { name: "New Claude Code chat", exact: true }).click();
  const chatRow = page.getByRole("button", {
    name: /Chat Claude Code chat, Claude Code/i,
  });
  await expect(chatRow).toBeVisible();

  await page.locator("textarea").fill("Summarize the current workspace");
  await page.locator("button[type='submit']").click();

  await expect(page.getByText("Summarize the current workspace", { exact: true })).toBeVisible();
  await expect(
    page.getByText("Agent response to: Summarize the current workspace", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("completed").first()).toBeVisible();
  await expect(page.getByText("raw agent output · 2 lines")).toBeVisible();
  await page.getByText("raw agent output · 2 lines").click();
  await expect(page.getByText(/agent_message_chunk/)).toBeVisible();

  await chatRow.hover();
  await page.getByRole("button", { name: "Delete chat Claude Code chat" }).click();
  await page.getByRole("button", { name: "Delete chat", exact: true }).last().click();
  await expect(chatRow).toHaveCount(0);
});

test("external-agent running turns keep controls stable and request cancel through the fake backend", async ({
  page,
}) => {
  await mockAvailableAgentAdapters(page);
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.agentAdapterID", "grok_build");
    window.localStorage.setItem("hecate.project", "proj_e2e");
  });
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  let cancelPosts = 0;
  await page.route("/hecate/v1/chat/sessions/*/cancel", async (route) => {
    cancelPosts += 1;
    await route.fallback();
  });

  await page.getByRole("button", { name: "New Grok Build chat", exact: true }).click();
  await expect(
    page.getByRole("button", { name: /Chat Grok Build chat, Grok Build/i }),
  ).toBeVisible();

  await page.locator("textarea").fill("Keep running while I inspect the controls [[keep-running]]");
  await page.locator("button[type='submit']").click();

  await expect(page.getByRole("button", { name: "Choose agent for new chat" })).toBeVisible();
  await expect(page.locator("textarea")).toBeVisible();
  await expect(page.getByRole("button", { name: "Stop external agent" })).toBeVisible();

  const stop = page.getByRole("button", { name: "Stop external agent" });
  await stop.click();

  await expect.poll(() => cancelPosts).toBe(1);
  await expect(stop).toBeDisabled();
  await expect(stop).toHaveAttribute("title", "Stopping...");
  await expect(page.getByText("Stopping...")).toBeVisible();
});

test("New chat falls back to Hecate when the remembered external agent needs setup", async ({
  page,
}) => {
  let createBody: any = null;
  await page.route("/hecate/v1/agent-adapters*", async (route) => {
    if (route.request().method() !== "GET") return route.continue();
    await route.fulfill({
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
            available: true,
            status: "available",
            cost_mode: "external",
          },
          {
            id: "claude_code",
            name: "Claude Code",
            kind: "acp",
            command: "claude-agent-acp",
            available: true,
            status: "available",
            cost_mode: "external",
          },
          {
            id: "cursor_agent",
            name: "Cursor Agent",
            kind: "acp",
            command: "cursor-agent",
            available: false,
            status: "missing",
            cost_mode: "external",
            error: "forced app CLI missing by HECATE_AGENT_ADAPTER_DEV_OVERRIDES",
          },
        ],
      }),
    });
  });
  await page.route("/hecate/v1/chat/sessions", async (route) => {
    if (route.request().method() !== "POST")
      return route.fulfill({
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
          id: "hecate-fallback-e2e",
          title: "Hecate chat",
          agent_id: "hecate",
          provider: "anthropic",
          model: "claude-sonnet-4-6",
          status: "created",
          messages: [],
        },
      }),
    });
  });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.agentAdapterID", "cursor_agent");
    window.localStorage.setItem("hecate.project", "proj_e2e");
  });
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  const newHecateChat = page.getByRole("button", { name: "New Hecate chat", exact: true });
  await expect(newHecateChat).toBeVisible();
  await expect(newHecateChat).toBeEnabled();

  await page.getByRole("button", { name: "Choose agent for new chat" }).click();
  const cursorOption = page.getByRole("option", { name: /Cursor Agent/ });
  await expect(cursorOption).toHaveAttribute("aria-disabled", "true");
  await expect(cursorOption).toHaveAttribute(
    "title",
    "Open Connections to set up Cursor Agent, then sign in with cursor-agent login.",
  );

  await newHecateChat.click();
  await expect
    .poll(() => createBody)
    .toMatchObject({
      agent_id: "hecate",
    });
});

test("sidebar rename works for agent-chat sessions", async ({ page }) => {
  let title = "Codex chat";
  let patchBody: any = null;
  await page.route("/hecate/v1/chat/sessions", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_sessions",
        data: [
          {
            id: "rename-chat-e2e",
            title,
            agent_id: "codex",
            project_id: "proj_e2e",
            workspace: "/tmp/hecate-e2e",
            status: "idle",
            message_count: 0,
            created_at: "2026-05-14T10:00:00Z",
            updated_at: "2026-05-14T10:00:00Z",
          },
        ],
      }),
    });
  });
  await page.route("/hecate/v1/chat/sessions/rename-chat-e2e", async (route) => {
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
          agent_id: "codex",
          project_id: "proj_e2e",
          workspace: "/tmp/hecate-e2e",
          status: "idle",
          messages: [],
          created_at: "2026-05-14T10:00:00Z",
          updated_at: "2026-05-14T10:01:00Z",
        },
      }),
    });
  });
  await page.route("/hecate/v1/chat/sessions/rename-chat-e2e/approvals*", (route) =>
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
  await expect(page.getByText("SYSTEM PROMPT / AGENT INSTRUCTIONS", { exact: true })).toBeVisible();
  await expect(page.locator("textarea").nth(1)).toBeVisible();

  await settingsBtn.click();
  await expect(
    page.getByText("SYSTEM PROMPT / AGENT INSTRUCTIONS", { exact: true }),
  ).not.toBeVisible();
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
  await expect(page.locator(".hecate-activitybar [aria-current='page']")).toHaveAttribute(
    "aria-label",
    /Connections/,
  );

  await page.reload();
  await page.waitForSelector(".hecate-activitybar");
  await expect(page.locator(".hecate-activitybar [aria-current='page']")).toHaveAttribute(
    "aria-label",
    /Connections/,
  );
});

// A failing /v1/chat/completions surfaces inline beneath the chat header.
// Toast is gone for chat errors — the chat surface owns its own banner so a
// single source of truth shows up next to the input. The "api key is
// required for cloud provider X" wire message is humanized into a
// Connections repair action before reaching the DOM.
test("chat error renders inline with the humanized message", async ({ page }) => {
  await switchToModel(page);
  await page.route(/\/hecate\/v1\/chat\/sessions\/[^/]+\/messages$/, (route) =>
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
    agent_id: "hecate",
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
  await page.route("/hecate/v1/chat/sessions/markdown-fence-e2e", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_session", data: session }),
    }),
  );
  await page.route("/hecate/v1/chat/sessions/markdown-fence-e2e/approvals*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "list", data: [] }),
    }),
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
    agent_id: "hecate",
    provider: "ollama",
    model: "qwen2.5-coder",
    workspace: "/tmp/e2e",
    status: "completed",
    message_count: 2,
    messages: [
      {
        id: "m-user",
        role: "user",
        content: "why did git fail?",
        created_at: "2026-04-21T10:00:00Z",
      },
      {
        id: "m-agent",
        role: "assistant",
        execution_mode: "hecate_task",
        content: "The command failed while inspecting git state.",
        provider: "ollama",
        model: "qwen2.5-coder",
        status: "failed",
        task_id: "task_failed",
        run_id: "run_failed",
        created_at: "2026-04-21T10:00:01Z",
        activities: [
          {
            id: "tool_git",
            type: "tool_call",
            title: "git_exec (failed)",
            status: "failed",
            kind: "git",
            detail: "git_exec - failed",
          },
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
  await page.route("/hecate/v1/chat/sessions/failed-tool-output-e2e", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_session", data: session }),
    }),
  );
  await page.route("/hecate/v1/chat/sessions/failed-tool-output-e2e/approvals*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "list", data: [] }),
    }),
  );

  await page.reload();
  await page.waitForSelector(".hecate-activitybar");

  await page.getByText(/1 (failed )?tool/).click();
  await page.getByText("Advanced").first().click();

  await expect(page.getByText(/Preview the related run output/)).toBeVisible();
  await expect(page.getByText("On branch feature/chat-message-queue")).toBeVisible();
  await expect(page.getByText("fatal: not a git repository")).toBeVisible();
  await expect(page.getByText("Open task output")).toBeVisible();
  await expect(page.getByText("No output preview was captured for this snapshot.")).toHaveCount(0);
});

test("empty model chat can add all detected local providers in one click", async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await mockGatewayAPIs(page);
  const created: Array<Record<string, unknown>> = [];
  await page.route("/hecate/v1/settings/providers", async (route) => {
    if (route.request().method() === "POST") {
      created.push(JSON.parse(route.request().postData() ?? "{}"));
    }
    await route.fallback();
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await switchToModel(page, false);

  await expect(page.getByText("Detected locally")).toBeVisible();
  await expect(page.getByText("Ollama", { exact: true })).toBeVisible();
  await expect(page.getByText("LM Studio", { exact: true })).toBeVisible();
  await expect(page.getByText("Installed")).toBeVisible();
  await expect(page.getByText("Running")).toBeVisible();
  await expect(page.getByRole("button", { name: "Open Connections" })).toHaveCount(1);

  await page.getByRole("button", { name: "Add selected" }).click();

  await expect
    .poll(() => created.map((body) => String(body.preset_id)).sort((a, b) => a.localeCompare(b)))
    .toEqual(["lmstudio", "ollama"]);
  await expect(page.getByText("Nothing runnable yet")).toBeVisible();
  await expect(
    page.getByText(
      /Add a model provider or install a supported coding-agent CLI before sending a message/,
    ),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: /Add selected/i })).toHaveCount(0);
});

test("empty Hecate Agent chat can add all detected local providers in one click", async ({
  page,
}) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await mockGatewayAPIs(page);
  const created: Array<Record<string, unknown>> = [];
  await page.route("/hecate/v1/settings/providers", async (route) => {
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
  await expect(page.getByRole("button", { name: "Open Connections" })).toHaveCount(1);

  await page.getByRole("button", { name: "Add selected" }).click();

  await expect
    .poll(() => created.map((body) => String(body.preset_id)).sort((a, b) => a.localeCompare(b)))
    .toEqual(["lmstudio", "ollama"]);
  await expect(page.getByText("Nothing runnable yet")).toBeVisible();
  await expect(page.getByRole("button", { name: /Add selected/i })).toHaveCount(0);
});

test("local provider quick-add makes model chat runnable without detected-provider setup", async ({
  page,
}) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await mockGatewayAPIs(page);
  const createdPresets = new Set<string>();

  await page.route("/hecate/v1/settings/providers", async (route) => {
    if (route.request().method() === "POST") {
      const body = JSON.parse(route.request().postData() ?? "{}") as { preset_id?: string };
      if (body.preset_id) createdPresets.add(body.preset_id);
    }
    await route.fallback();
  });
  await page.route("/hecate/v1/settings/providers/local-discovery", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "local_provider_discovery",
        data: [
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
          },
          {
            preset_id: "ollama",
            name: "Ollama",
            base_url: "http://127.0.0.1:11434/v1",
            probe_url: "http://127.0.0.1:11434/api/tags",
            status: "running",
            command: "ollama",
            command_available: true,
            command_path: "/usr/local/bin/ollama",
            http_available: true,
            model_count: 1,
            models: ["llama3.1:8b"],
          },
        ],
      }),
    });
  });
  await page.route("/hecate/v1/providers/status*", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "list",
        data: [
          ...(createdPresets.has("lmstudio")
            ? [
                {
                  name: "lmstudio",
                  kind: "local",
                  healthy: false,
                  status: "pending",
                  default_model: "",
                  models: [],
                  model_count: 0,
                },
              ]
            : []),
          ...(createdPresets.has("ollama")
            ? [
                {
                  name: "ollama",
                  kind: "local",
                  healthy: true,
                  status: "healthy",
                  default_model: "llama3.1:8b",
                  models: ["llama3.1:8b"],
                  model_count: 1,
                },
              ]
            : []),
        ],
      }),
    });
  });
  await page.route("/v1/models*", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "list",
        data: createdPresets.has("ollama")
          ? [
              {
                id: "llama3.1:8b",
                owned_by: "ollama",
                metadata: {
                  provider: "ollama",
                  provider_kind: "local",
                  default: true,
                  capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
                },
              },
            ]
          : [],
      }),
    });
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.getByRole("button", { name: "New Hecate chat", exact: true }).click();
  await expect(page.getByText("Detected locally")).toBeVisible();

  await page.getByRole("button", { name: "Add selected" }).click();

  await expect.poll(() => [...createdPresets].sort()).toEqual(["lmstudio", "ollama"]);
  await expect(page.getByText("Ready when you are")).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Message" })).toBeVisible();
  await expect(page.getByRole("button", { name: /model picker/i })).toContainText("llama3.1:8b");
  await expect(page.getByText("No routable model")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Add selected/i })).toHaveCount(0);
});

test("Hecate Chat sends direct model turns when selected model lacks tools", async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.providerFilter", "ollama");
    window.localStorage.setItem("hecate.model", "smollm2:135m");
  });
  await mockGatewayAPIs(page, {
    settingsConfig: {
      providers: [
        {
          id: "ollama",
          name: "Ollama",
          preset_id: "ollama",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:11434/v1",
          enabled: true,
          credential_configured: false,
        },
      ],
      tenants: [],
      api_keys: [],
      policy_rules: [],
    },
  });
  await page.route("/hecate/v1/providers/status*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "list",
        data: [
          {
            name: "ollama",
            kind: "local",
            healthy: true,
            status: "healthy",
            default_model: "smollm2:135m",
            models: ["smollm2:135m"],
            model_count: 1,
          },
        ],
      }),
    }),
  );
  await page.route("/v1/models*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "list",
        data: [
          {
            id: "smollm2:135m",
            owned_by: "ollama",
            metadata: {
              provider: "ollama",
              provider_kind: "local",
              default: true,
              capabilities: { tool_calling: "none", streaming: true, source: "provider" },
            },
          },
        ],
      }),
    }),
  );
  let messagePayload: Record<string, unknown> | null = null;
  await page.route(/\/hecate\/v1\/chat\/sessions\/[^/]+\/messages$/, async (route) => {
    messagePayload = await route.request().postDataJSON();
    const url = new URL(route.request().url());
    const sessionID = decodeURIComponent(url.pathname.split("/").at(-2) ?? "chat-direct-e2e");
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_session",
        data: {
          id: sessionID,
          title: "Hecate chat",
          agent_id: "hecate",
          provider: "ollama",
          model: "smollm2:135m",
          status: "completed",
          message_count: 2,
          messages: [
            {
              id: "direct-user-e2e",
              execution_mode: "direct_model",
              role: "user",
              content: "tell a tiny joke",
              created_at: "2026-05-14T12:00:00Z",
            },
            {
              id: "direct-assistant-e2e",
              execution_mode: "direct_model",
              role: "assistant",
              content: "Direct response to: tell a tiny joke",
              status: "completed",
              provider: "ollama",
              model: "smollm2:135m",
              run_id: "model_run_direct_e2e",
              request_id: "req_direct_e2e",
              trace_id: "trace_direct_e2e",
              cost_mode: "hecate",
              created_at: "2026-05-14T12:00:01Z",
            },
          ],
          created_at: "2026-05-14T12:00:00Z",
          updated_at: "2026-05-14T12:00:01Z",
        },
      }),
    });
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.getByRole("button", { name: "New Hecate chat", exact: true }).click();
  await expect(page.getByRole("button", { name: /provider picker/i })).toContainText("Ollama");
  await chooseComposerModel(page, "smollm2:135m");
  await expect(page.getByRole("button", { name: /model picker/i })).toContainText("smollm2:135m");

  await page.locator("textarea").fill("tell a tiny joke");
  await page.locator("button[type='submit']").click();

  await expect(page.locator("body")).toContainText("Direct response to: tell a tiny joke");
  await expect(page.locator("body")).not.toContainText("agent run failed");
  await expect
    .poll(() => messagePayload)
    .toMatchObject({
      execution_mode: "direct_model",
      provider: "ollama",
      model: "smollm2:135m",
    });
});

test("Hecate Agent local-provider onboarding renders the real final answer after completion", async ({
  page,
}) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.project", "proj_e2e_workspace");
  });
  await mockGatewayAPIs(page);

  await page.route("/v1/models*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "list",
        data: [
          {
            id: "qwen2.5",
            owned_by: "lm-studio",
            metadata: {
              provider: "lm-studio",
              provider_kind: "local",
              capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
            },
          },
        ],
      }),
    }),
  );

  const sessions: any[] = [];
  await page.route("/hecate/v1/chat/sessions", async (route) => {
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
        agent_id: "hecate",
        project_id: "proj_e2e_workspace",
        provider: body.provider || "",
        model: body.model || "qwen2.5",
        capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
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
  await page.route("/hecate/v1/chat/sessions/chat-hecate-e2e", async (route) => {
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

  await page.route("/hecate/v1/chat/sessions/chat-hecate-e2e/stream", (route) =>
    route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      body: "",
    }),
  );

  let messagePayload: Record<string, unknown> | null = null;
  await page.route("/hecate/v1/chat/sessions/chat-hecate-e2e/messages", async (route) => {
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
          execution_mode: "hecate_task",
          segment_id: "task:task-hecate-e2e",
          task_id: "task-hecate-e2e",
          role: "user",
          content: "show diff",
          created_at: "2026-05-06T10:00:00Z",
        },
        {
          id: "msg-assistant-e2e",
          execution_mode: "hecate_task",
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
            {
              id: "run",
              type: "task_run",
              status: "completed",
              title: "Backing task",
              detail: "completed · task-hecate-e2e · run-hecate-e2e",
            },
            {
              id: "done",
              type: "completed",
              status: "completed",
              title: "Run completed",
              detail: "completed",
            },
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
  await page.getByRole("button", { name: /Chat show diff/ }).click();

  await page.getByRole("button", { name: /model picker/i }).click();
  await page.locator(".dropdown-menu").locator("text=qwen2.5").first().click();
  await expect(page.getByText("Tools on · /tmp/hecate-e2e-workspace")).toBeVisible();

  await page.locator("textarea").fill("show diff");
  await page.locator("button[type='submit']").click();

  await expect(page.locator("body")).toContainText("+changed line");
  await expect(page.locator("body")).not.toContainText("Hecate Agent run completed.");
  await expect
    .poll(() => messagePayload)
    .toMatchObject({
      execution_mode: "hecate_task",
      model: "qwen2.5",
      workspace: "/tmp/hecate-e2e-workspace",
    });
  await expect(page.locator("body")).toContainText("qwen2.5");
});

test("Hecate Chat can move tools on, tools off, then tools on again in one transcript", async ({
  page,
}) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.project", "proj_e2e_workspace");
  });
  await mockGatewayAPIs(page, {
    settingsConfig: {
      providers: [
        {
          id: "lmstudio",
          name: "LM Studio",
          preset_id: "lmstudio",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:1234/v1",
          enabled: true,
          credential_configured: false,
        },
      ],
      tenants: [],
      api_keys: [],
      policy_rules: [],
    },
  });

  await page.route("/v1/models*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "list",
        data: [
          {
            id: "qwen2.5",
            owned_by: "lmstudio",
            metadata: {
              provider: "lmstudio",
              provider_kind: "local",
              capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
            },
          },
        ],
      }),
    }),
  );

  let createSessionCount = 0;
  const submittedTurns: Array<Record<string, unknown>> = [];
  const messages: Array<Record<string, unknown>> = [];
  let session: Record<string, unknown> | null = null;
  let taskApprovalResolved = false;

  function completeFirstTaskApproval() {
    taskApprovalResolved = true;
    const assistant = messages.find((message) => message.id === "msg-assistant-1") as
      | Record<string, unknown>
      | undefined;
    if (!assistant) return;
    assistant.content = "Tools answer one from qwen2.5";
    assistant.status = "completed";
    assistant.activities = [
      {
        id: "approval-1",
        type: "approval",
        status: "approved",
        title: "Approval approved",
        detail: "shell_exec - approved",
        approval_id: "appr-tools-1",
        needs_action: false,
      },
      {
        id: "task-1",
        type: "task_run",
        status: "completed",
        title: "Backing task",
        detail: "completed · task-tools-1 · run-tools-1",
      },
      {
        id: "turns-1",
        type: "thinking",
        status: "completed",
        title: "Model turns",
        detail: "2 turns completed",
      },
    ];
    session = {
      ...session,
      status: "completed",
      message_count: messages.length,
      messages,
    };
  }

  await page.route("/hecate/v1/chat/sessions", async (route) => {
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
        agent_id: body.agent_id || "hecate",
        provider: body.provider || "",
        model: body.model || "qwen2.5",
        capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
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

  await page.route("/hecate/v1/chat/sessions/chat-tools-switch-e2e/stream", (route) =>
    route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      body: "",
    }),
  );

  await page.route("/hecate/v1/chat/sessions/chat-tools-switch-e2e/approvals*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_approvals", data: [] }),
    }),
  );

  await page.route("/hecate/v1/chat/sessions/chat-tools-switch-e2e", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_session", data: session }),
    }),
  );

  await page.route(
    "/hecate/v1/tasks/task-tools-1/approvals/appr-tools-1/resolve",
    async (route) => {
      completeFirstTaskApproval();
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          object: "task_approval",
          data: { id: "appr-tools-1", status: "approved" },
        }),
      });
    },
  );

  await page.route("/hecate/v1/chat/sessions/chat-tools-switch-e2e/messages", async (route) => {
    const body = await route.request().postDataJSON();
    submittedTurns.push(body);
    const turn = submittedTurns.length;
    const executionMode = body.execution_mode || "direct_model";
    const isHecateAgent = executionMode === "hecate_task";
    const agentTurn = submittedTurns.filter((t) => t.execution_mode === "hecate_task").length;
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
        execution_mode: executionMode,
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
        execution_mode: executionMode,
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
              {
                id: "approval-1",
                type: "approval",
                status: "awaiting_approval",
                kind: "agent_loop_tool_call",
                title: "Awaiting approval",
                detail: "Agent requested tools that require approval: shell_exec",
                approval_id: "appr-tools-1",
                needs_action: true,
              },
              {
                id: `task-${turn}`,
                type: "task_run",
                status: "awaiting_approval",
                title: "Backing task",
                detail: `awaiting approval · ${taskID} · ${runID}`,
              },
            ]
          : isHecateAgent
            ? [
                {
                  id: `task-${turn}`,
                  type: "task_run",
                  status: "completed",
                  title: "Backing task",
                  detail: `completed · ${taskID} · ${runID}`,
                },
                {
                  id: `done-${turn}`,
                  type: "completed",
                  status: "completed",
                  title: "Run completed",
                  detail: "completed",
                },
              ]
            : [],
        created_at: `2026-05-06T10:00:0${turn}Z`,
      },
    );

    session = {
      ...session,
      provider: body.provider || "",
      model: body.model,
      capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
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
  expect(submittedTurns.map((turn) => turn.execution_mode)).toEqual([
    "hecate_task",
    "direct_model",
    "hecate_task",
  ]);
  expect(submittedTurns.map((turn) => turn.content)).toEqual([
    "first with tools",
    "direct model turn",
    "tools again",
  ]);
  expect(submittedTurns.filter((turn) => turn.execution_mode === "hecate_task")).toEqual([
    expect.objectContaining({ model: "qwen2.5", workspace: "/tmp/hecate-e2e-workspace" }),
    expect.objectContaining({ model: "qwen2.5", workspace: "/tmp/hecate-e2e-workspace" }),
  ]);
});

test("Hecate Chat falls back to direct chat when the selected model has no tools", async ({
  page,
}) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.providerFilter", "ollama");
    window.localStorage.setItem("hecate.model", "qwen2.5-coder");
  });
  await mockGatewayAPIs(page, {
    settingsConfig: {
      providers: [
        {
          id: "ollama",
          name: "Ollama",
          preset_id: "ollama",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:11434/v1",
          enabled: true,
          credential_configured: false,
        },
      ],
      tenants: [],
      api_keys: [],
      policy_rules: [],
    },
  });

  await page.route("/v1/models*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "list",
        data: [
          {
            id: "qwen2.5-coder",
            owned_by: "ollama",
            metadata: {
              provider: "ollama",
              provider_kind: "local",
              capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
            },
          },
          {
            id: "smollm2:135m",
            owned_by: "ollama",
            metadata: {
              provider: "ollama",
              provider_kind: "local",
              capabilities: { tool_calling: "none", streaming: true, source: "provider" },
            },
          },
        ],
      }),
    }),
  );
  await page.route("/hecate/v1/providers/status*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "list",
        data: [
          {
            name: "ollama",
            kind: "local",
            healthy: true,
            status: "healthy",
            default_model: "qwen2.5-coder",
            models: ["qwen2.5-coder", "smollm2:135m"],
            model_count: 2,
          },
        ],
      }),
    }),
  );

  const submittedTurns: Array<Record<string, unknown>> = [];
  let session: Record<string, unknown> | null = null;

  await page.route("/hecate/v1/chat/sessions", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_sessions", data: session ? [session] : [] }),
      });
      return;
    }
    if (route.request().method() === "POST") {
      const body = await route.request().postDataJSON();
      session = {
        id: "chat-no-tools-model-e2e",
        title: body.title || "plain model",
        agent_id: "hecate",
        provider: body.provider || "",
        model: body.model,
        capabilities: { tool_calling: "none", streaming: true, source: "provider" },
        status: "created",
        message_count: 0,
        messages: [],
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

  await page.route("/hecate/v1/chat/sessions/chat-no-tools-model-e2e/stream", (route) =>
    route.fulfill({ status: 200, contentType: "text/event-stream", body: "" }),
  );
  await page.route("/hecate/v1/chat/sessions/chat-no-tools-model-e2e", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_session", data: session }),
    }),
  );
  await page.route("/hecate/v1/chat/sessions/chat-no-tools-model-e2e/messages", async (route) => {
    const body = await route.request().postDataJSON();
    submittedTurns.push(body);
    session = {
      ...session,
      status: "completed",
      message_count: 2,
      messages: [
        {
          id: "msg-user-direct",
          execution_mode: body.execution_mode,
          provider: body.provider || "",
          model: body.model,
          role: "user",
          content: body.content,
          created_at: "2026-05-06T10:00:00Z",
        },
        {
          id: "msg-assistant-direct",
          execution_mode: body.execution_mode,
          provider: body.provider || "",
          model: body.model,
          role: "assistant",
          content: `Direct answer from ${body.model}`,
          status: "completed",
          cost_mode: "provider",
          created_at: "2026-05-06T10:00:01Z",
        },
      ],
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
  await page.locator(".dropdown-menu").locator("text=smollm2:135m").first().click();
  await expect(page.getByText("Direct chat · tools unavailable")).toBeVisible();

  await page.getByRole("textbox", { name: "Message" }).fill("tell a joke");
  await page.locator("button[type='submit']").click();
  await expect(page.locator("body")).toContainText("Direct answer from smollm2:135m");

  expect(submittedTurns).toEqual([
    expect.objectContaining({
      execution_mode: "direct_model",
      provider: "ollama",
      model: "smollm2:135m",
      content: "tell a joke",
    }),
  ]);
  expect(submittedTurns[0]).not.toHaveProperty("workspace");
});

test("Hecate Chat rehydrates an active task and blocks direct sends after refresh", async ({
  page,
}) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.chatSessionID", "chat-busy-e2e");
    window.localStorage.setItem(
      "hecate.chatTargetBySessionID",
      JSON.stringify({ "chat-busy-e2e": "model" }),
    );
    window.localStorage.setItem("hecate.project", "proj_e2e_workspace");
  });
  await mockGatewayAPIs(page, {
    settingsConfig: {
      providers: [
        {
          id: "lmstudio",
          name: "LM Studio",
          preset_id: "lmstudio",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:1234/v1",
          enabled: true,
          credential_configured: false,
        },
      ],
      tenants: [],
      api_keys: [],
      policy_rules: [],
    },
  });

  const session = {
    id: "chat-busy-e2e",
    title: "busy tools turn",
    agent_id: "hecate",
    provider: "lmstudio",
    model: "qwen2.5",
    capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
    workspace: "/tmp/hecate-e2e-workspace",
    task_id: "task-busy-e2e",
    latest_run_id: "run-busy-e2e",
    status: "running",
    message_count: 2,
    segments: [
      {
        id: "model:first",
        execution_mode: "direct_model",
        provider: "lmstudio",
        model: "qwen2.5",
        status: "completed",
        message_count: 2,
      },
      {
        id: "task:task-busy-e2e",
        execution_mode: "hecate_task",
        provider: "lmstudio",
        model: "qwen2.5",
        task_id: "task-busy-e2e",
        latest_run_id: "run-busy-e2e",
        status: "running",
        message_count: 1,
      },
    ],
    messages: [
      {
        id: "msg-user",
        execution_mode: "hecate_task",
        segment_id: "task:task-busy-e2e",
        task_id: "task-busy-e2e",
        role: "user",
        content: "inspect",
        created_at: "2026-05-06T10:00:00Z",
      },
      {
        id: "msg-assistant",
        execution_mode: "hecate_task",
        segment_id: "task:task-busy-e2e",
        task_id: "task-busy-e2e",
        run_id: "run-busy-e2e",
        role: "assistant",
        content: "",
        status: "running",
        model: "qwen2.5",
        created_at: "2026-05-06T10:00:01Z",
        activities: [
          {
            id: "hecate_task_run:run-busy-e2e",
            type: "task_run",
            status: "running",
            title: "Backing task",
            detail: "running",
          },
        ],
      },
    ],
  };
  let messagePosts = 0;

  await page.route("/v1/models*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "list",
        data: [
          {
            id: "qwen2.5",
            owned_by: "lmstudio",
            metadata: {
              provider: "lmstudio",
              provider_kind: "local",
              capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
            },
          },
        ],
      }),
    }),
  );
  await page.route("/hecate/v1/chat/sessions", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_sessions", data: [session] }),
    }),
  );
  await page.route("/hecate/v1/chat/sessions/chat-busy-e2e", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_session", data: session }),
    }),
  );
  await page.route("/hecate/v1/chat/sessions/chat-busy-e2e/stream", (route) =>
    route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      body: "",
    }),
  );
  await page.route("/hecate/v1/chat/sessions/chat-busy-e2e/approvals*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_approvals", data: [] }),
    }),
  );
  await page.route("/hecate/v1/chat/sessions/chat-busy-e2e/messages", (route) => {
    messagePosts += 1;
    return route.fulfill({ status: 500, body: "send should be blocked by the UI" });
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await expect(page.getByText("Tools on · /tmp/hecate-e2e-workspace")).toBeVisible();
  await expect(page.getByRole("button", { name: "Fixed model: qwen2.5" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Stop active task" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Open task", exact: true })).toBeVisible();
  await expect(page.getByText("Backing task")).toBeVisible();

  await page.locator("textarea").fill("try direct while busy");
  await expect(page.getByRole("button", { name: "Queue message" })).toBeVisible();
  await page.getByRole("button", { name: "Queue message" }).click();
  await expect(page.getByLabel("Queued messages")).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Queued message 1" })).toHaveValue(
    "try direct while busy",
  );
  expect(messagePosts).toBe(0);
});

test("Hecate Chat rehydrates an awaiting-approval task and resolves it after refresh", async ({
  page,
}) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.chatSessionID", "chat-approval-refresh-e2e");
    window.localStorage.setItem("hecate.project", "proj_e2e_workspace");
  });
  await mockGatewayAPIs(page, {
    settingsConfig: {
      providers: [
        {
          id: "lmstudio",
          name: "LM Studio",
          preset_id: "lmstudio",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:1234/v1",
          enabled: true,
          credential_configured: false,
        },
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
    agent_id: "hecate",
    provider: "lmstudio",
    model: "qwen2.5",
    capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
    workspace: "/tmp/hecate-e2e-workspace",
    task_id: "task-approval-refresh-e2e",
    latest_run_id: "run-approval-refresh-e2e",
    status: "awaiting_approval",
    message_count: 2,
    segments: [
      {
        id: "task:task-approval-refresh-e2e",
        execution_mode: "hecate_task",
        provider: "lmstudio",
        model: "qwen2.5",
        task_id: "task-approval-refresh-e2e",
        latest_run_id: "run-approval-refresh-e2e",
        status: "awaiting_approval",
        message_count: 2,
      },
    ],
    messages: [
      {
        id: "msg-user-approval",
        execution_mode: "hecate_task",
        segment_id: "task:task-approval-refresh-e2e",
        task_id: "task-approval-refresh-e2e",
        role: "user",
        content: "show git diff",
        created_at: "2026-05-06T10:00:00Z",
      },
      {
        id: "msg-assistant-approval",
        execution_mode: "hecate_task",
        segment_id: "task:task-approval-refresh-e2e",
        task_id: "task-approval-refresh-e2e",
        run_id: "run-approval-refresh-e2e",
        role: "assistant",
        content: "",
        status: "awaiting_approval",
        model: "qwen2.5",
        created_at: "2026-05-06T10:00:01Z",
        activities: [
          {
            id: "task:approval:appr-refresh-e2e",
            type: "approval",
            status: "awaiting_approval",
            kind: "agent_loop_tool_call",
            title: "Awaiting approval",
            detail: "Agent requested tools that require approval: git_exec",
            approval_id: "appr-refresh-e2e",
            needs_action: true,
          },
          {
            id: "hecate_task_run:run-approval-refresh-e2e",
            type: "task_run",
            status: "awaiting_approval",
            title: "Backing task",
            detail: "awaiting approval",
          },
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
          {
            id: "task:approval:appr-refresh-e2e",
            type: "approval",
            status: "approved",
            kind: "agent_loop_tool_call",
            title: "Approval approved",
            detail: "git_exec - approved",
            approval_id: "appr-refresh-e2e",
            needs_action: false,
          },
          {
            id: "hecate_task_run:run-approval-refresh-e2e",
            type: "task_run",
            status: "completed",
            title: "Backing task",
            detail: "completed",
          },
          {
            id: "turns-refresh",
            type: "thinking",
            status: "completed",
            title: "Model turns",
            detail: "2 turns completed",
          },
        ],
      },
    ],
    segments: [{ ...pendingSession.segments[0], status: "completed" }],
  };
  const currentSession = () => (approvalResolved ? completedSession : pendingSession);

  await page.route("/v1/models*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "list",
        data: [
          {
            id: "qwen2.5",
            owned_by: "lmstudio",
            metadata: {
              provider: "lmstudio",
              provider_kind: "local",
              capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
            },
          },
        ],
      }),
    }),
  );
  await page.route("/hecate/v1/chat/sessions", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_sessions", data: [currentSession()] }),
    }),
  );
  await page.route("/hecate/v1/chat/sessions/chat-approval-refresh-e2e", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_session", data: currentSession() }),
    }),
  );
  await page.route("/hecate/v1/chat/sessions/chat-approval-refresh-e2e/stream", (route) =>
    route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      body: "",
    }),
  );
  await page.route("/hecate/v1/chat/sessions/chat-approval-refresh-e2e/approvals*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_approvals", data: [] }),
    }),
  );
  await page.route(
    "/hecate/v1/tasks/task-approval-refresh-e2e/approvals/appr-refresh-e2e/resolve",
    async (route) => {
      approvalResolved = true;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          object: "task_approval",
          data: { id: "appr-refresh-e2e", status: "approved" },
        }),
      });
    },
  );

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await expect(page.getByTestId("hecate-task-approval-banner")).toBeVisible();
  await expect(
    page
      .getByTestId("hecate-task-approval-banner")
      .getByRole("button", { name: "Open task", exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Send message" })).toBeDisabled();
  await expect(page.getByText("Hecate Chat is working. New messages will queue.")).toBeVisible();

  await page.getByRole("button", { name: /Approve Agent tool call/i }).click();
  await expect(page.getByTestId("hecate-task-approval-banner")).toBeHidden();
  await expect(page.locator("body")).toContainText("The current diff touches ui/e2e/chat.spec.ts.");
  await expect(page.getByRole("button", { name: "Send message" })).toBeVisible();
});

test("configured provider with no models shows troubleshooting, not detected-provider setup", async ({
  page,
}) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await mockGatewayAPIs(page, {
    settingsConfig: {
      providers: [
        {
          id: "ollama",
          name: "Ollama",
          preset_id: "ollama",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:11434/v1",
          enabled: true,
          credential_configured: false,
        },
      ],
      tenants: [],
      api_keys: [],
      policy_rules: [],
    },
  });
  await page.route("/v1/models*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "list", data: [] }),
    }),
  );
  await page.route("/hecate/v1/providers/status*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "provider_status",
        data: [
          {
            name: "ollama",
            kind: "local",
            healthy: true,
            status: "healthy",
            base_url: "http://127.0.0.1:11434/v1",
            models: [],
            model_count: 0,
          },
        ],
      }),
    }),
  );

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await switchToModel(page, false);

  await expect(page.getByText("Nothing runnable yet")).toBeVisible();
  await expect(
    page.getByText(
      /Add a model provider or install a supported coding-agent CLI before sending a message/,
    ),
  ).toBeVisible();
  await expect(page.getByText("Detected locally")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Add selected/i })).toHaveCount(0);
});

test("selected-model readiness can switch to the backend-suggested fallback model", async ({
  page,
}) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await mockGatewayAPIs(page, {
    settingsConfig: {
      providers: [
        {
          id: "anthropic",
          name: "Anthropic",
          preset_id: "anthropic",
          kind: "cloud",
          protocol: "anthropic",
          base_url: "https://api.anthropic.com/v1",
          enabled: true,
          credential_configured: false,
        },
        {
          id: "openai",
          name: "OpenAI",
          preset_id: "openai",
          kind: "cloud",
          protocol: "openai",
          base_url: "https://api.openai.com/v1",
          enabled: true,
          credential_configured: true,
        },
      ],
      tenants: [],
      api_keys: [],
      policy_rules: [],
    },
  });
  await page.route("/v1/models*", (route) =>
    route.fulfill({
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
          {
            id: "gpt-4o-mini",
            owned_by: "openai",
            metadata: { provider: "openai", provider_kind: "cloud" },
          },
        ],
      }),
    }),
  );
  await page.route("/hecate/v1/providers/status*", (route) =>
    route.fulfill({
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
    }),
  );
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "model");
    window.localStorage.setItem("hecate.providerFilter", "auto");
    window.localStorage.setItem("hecate.model", "claude-sonnet-4-6");
    window.localStorage.setItem("hecate.project", "proj_e2e");
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.getByRole("button", { name: "New Hecate chat", exact: true }).click();

  await chooseComposerModel(page, "gpt-4o-mini");
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
        data: [
          {
            id: "a-e2e-1",
            title: "E2E approval test",
            agent_id: "codex",
            status: "running",
            message_count: 0,
          },
        ],
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
        data: [
          {
            id: "ap-e2e-1",
            session_id: "a-e2e-1",
            adapter_id: "codex",
            tool_kind: "fs",
            tool_name: "write_file",
            status: "pending",
            acp_options: [{ option_id: "approve_once", kind: "allow_once", name: "Approve once" }],
            scope_choices: ["once", "session"],
            created_at: "2026-04-21T10:00:00Z",
            expires_at: "2026-04-21T10:05:00Z",
          },
        ],
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
          acp_options: [{ option_id: "approve_once", kind: "allow_once", name: "Approve once" }],
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
          agent_id: "codex",
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

test("workspace changes review inspects and discards a current file", async ({ page }) => {
  const readmeDiff = [
    "diff --git a/README.md b/README.md",
    "index 1111111..2222222 100644",
    "--- a/README.md",
    "+++ b/README.md",
    "@@ -1,1 +1,2 @@",
    " old line",
    "+current line",
  ].join("\n");
  const runtimeAPIDiff = [
    "diff --git a/docs/runtime-api.md b/docs/runtime-api.md",
    "index 3333333..4444444 100644",
    "--- a/docs/runtime-api.md",
    "+++ b/docs/runtime-api.md",
    "@@ -1,1 +1,2 @@",
    " existing line",
    "+kept line",
  ].join("\n");

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
        data: [
          {
            id: "a-diff-1",
            title: "Diff review",
            agent_id: "codex",
            status: "completed",
            message_count: 2,
          },
        ],
      }),
    });
  });

  const sessionBody = {
    object: "chat_session",
    data: {
      id: "a-diff-1",
      title: "Diff review",
      agent_id: "codex",
      workspace: "/tmp/e2e",
      status: "completed",
      messages: [
        { id: "m-user", role: "user", content: "update docs", created_at: "2026-04-21T10:00:00Z" },
        {
          id: "m-agent",
          role: "assistant",
          content: "Updated the docs.",
          agent_id: "codex",
          agent_name: "Codex",
          status: "completed",
          diff_stat:
            "README.md | 3 ++-\ndocs/runtime-api.md | 4 ++++\n2 files changed, 6 insertions(+), 1 deletion(-)",
          diff: readmeDiff,
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

  await page.route("/hecate/v1/chat/sessions/a-diff-1/workspace-diff", (route) => {
    if (route.request().method() === "POST") {
      void route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          object: "chat_workspace_diff",
          data: {
            workspace: "/tmp/e2e",
            diff_stat: "docs/runtime-api.md | 4 ++++\n1 file changed, 4 insertions(+)",
            diff: runtimeAPIDiff,
            has_changes: true,
            files: [{ path: "docs/runtime-api.md", additions: 4, deletions: 0, status: "added" }],
          },
        }),
      });
      return;
    }
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_workspace_diff",
        data: {
          workspace: "/tmp/e2e",
          diff_stat:
            "README.md | 3 ++-\ndocs/runtime-api.md | 4 ++++\n2 files changed, 6 insertions(+), 1 deletion(-)",
          diff: `${readmeDiff}\n${runtimeAPIDiff}`,
          has_changes: true,
          files: [
            { path: "README.md", additions: 2, deletions: 1, status: "modified" },
            { path: "docs/runtime-api.md", additions: 4, deletions: 0, status: "added" },
          ],
        },
      }),
    });
  });

  await page.route("/hecate/v1/chat/sessions/a-diff-1/workspace-diff/files/README.md", (route) => {
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_workspace_file_diff",
        data: {
          path: "README.md",
          additions: 2,
          deletions: 1,
          status: "modified",
          diff: readmeDiff,
        },
      }),
    });
  });

  let discardedPaths: string[] | null = null;
  await page.route("/hecate/v1/chat/sessions/a-diff-1/workspace-diff/revert", async (route) => {
    discardedPaths = (await route.request().postDataJSON()).paths;
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_workspace_diff",
        data: {
          workspace: "/tmp/e2e",
          diff_stat: "docs/runtime-api.md | 4 ++++\n1 file changed, 4 insertions(+)",
          diff: runtimeAPIDiff,
          has_changes: true,
          files: [{ path: "docs/runtime-api.md", additions: 4, deletions: 0, status: "added" }],
        },
      }),
    });
  });

  await page.reload();
  await page.waitForSelector(".hecate-activitybar");

  await expect(page.getByText("Updated the docs.")).toBeVisible();
  await expect(page.getByText("Workspace changes")).toBeVisible();
  await page.getByRole("button", { name: "Workspace changes" }).click();
  const workspaceChangesPanel = page.getByLabel("Workspace changes panel");
  await expect(
    workspaceChangesPanel.getByText("2 files changed, 6 insertions(+), 1 deletion(-)").first(),
  ).toBeVisible();

  await expect(page.getByRole("button", { name: "Hide diff README.md" })).toBeVisible();
  await expect(workspaceChangesPanel).toContainText("current line");

  await page.getByRole("button", { name: "Discard README.md" }).click();
  await expect(page.getByRole("button", { name: "Confirm discard README.md" })).toBeVisible();
  await page.getByRole("button", { name: "Confirm discard README.md" }).click();
  await expect.poll(() => discardedPaths).toEqual(["README.md"]);
  await expect(
    workspaceChangesPanel.getByText("1 file changed, 4 insertions(+)").first(),
  ).toBeVisible();
});

type ClaudeAdapterFixture = {
  available?: boolean;
  authStatus?: string;
  healthStatus?: "ready" | "auth_required" | "not_installed" | "error";
};

type ExternalAdapterFixture = ClaudeAdapterFixture & {
  id: string;
  name: string;
  command: string;
  args?: string[];
};

async function openExternalAgentReadinessFixture(page: Page, fixture: ExternalAdapterFixture) {
  const sessionID = `${fixture.id}-onboarding-e2e`;
  const adapter = {
    id: fixture.id,
    name: fixture.name,
    kind: "acp",
    command: fixture.command,
    args: fixture.args,
    available: fixture.available ?? true,
    status: fixture.available === false ? "missing" : "available",
    error: fixture.available === false ? `${fixture.command} command not found` : undefined,
    description: `Run ${fixture.name} through ACP as a long-lived external coding-agent session supervised by Hecate.`,
    cost_mode: "external",
    auth_status: fixture.authStatus ?? "unknown",
    auth_error:
      fixture.authStatus === "unauthenticated"
        ? `Run ${fixture.command.replace(/\s+.*/, "")} login`
        : undefined,
  };
  const status = fixture.available === false ? "not_installed" : (fixture.healthStatus ?? "ready");

  await page.route("/hecate/v1/agent-adapters*", async (route) => {
    const method = route.request().method();
    const url = route.request().url();
    if (method === "POST" && url.includes(`/${fixture.id}/probe`)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          object: "agent_adapter_probe",
          data: {
            adapter:
              status === "ready"
                ? { ...adapter, auth_status: "ok", auth_error: undefined }
                : adapter,
            health: {
              adapter_id: fixture.id,
              status,
              stage:
                status === "ready"
                  ? "ready"
                  : status === "not_installed"
                    ? "lookup"
                    : "new_session",
              path: fixture.available === false ? undefined : `/usr/local/bin/${fixture.command}`,
              hint:
                status === "auth_required"
                  ? `${fixture.name} needs local sign-in before use.`
                  : undefined,
              error:
                status === "not_installed" ? `${fixture.command} command not found` : undefined,
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
    id: sessionID,
    title: `${fixture.name} chat`,
    agent_id: fixture.id,
    agent_name: fixture.name,
    driver_kind: "acp",
    native_session_id: `native-${fixture.id}-e2e`,
    project_id: "proj_e2e",
    workspace: "/tmp/hecate-e2e",
    status: "idle",
    message_count: 0,
    config_options: [],
    messages: [],
    created_at: "2026-05-14T10:00:00Z",
    updated_at: "2026-05-14T10:00:00Z",
  };
  await page.route(/\/hecate\/v1\/chat\/sessions(?:\/.*)?(?:\?.*)?$/, async (route) => {
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
    if (method === "GET" && path === `/hecate/v1/chat/sessions/${sessionID}`) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_session", data: session }),
      });
      return;
    }
    if (method === "GET" && path === `/hecate/v1/chat/sessions/${sessionID}/stream`) {
      await route.fulfill({ status: 200, contentType: "text/event-stream", body: "" });
      return;
    }
    if (method === "GET" && path === `/hecate/v1/chat/sessions/${sessionID}/approvals`) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_approvals", data: [] }),
      });
      return;
    }
    await route.continue();
  });

  await page.addInitScript(
    ({ adapterID, chatSessionID }) => {
      window.localStorage.setItem("hecate.chatTarget", "external_agent");
      window.localStorage.setItem("hecate.agentAdapterID", adapterID);
      window.localStorage.setItem("hecate.project", "proj_e2e");
      window.localStorage.setItem("hecate.chatSessionID", chatSessionID);
    },
    { adapterID: fixture.id, chatSessionID: sessionID },
  );
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page
    .getByRole("button", { name: new RegExp(`Chat ${fixture.name} chat, ${fixture.name}`) })
    .click();
}

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
    description:
      "Run Claude Agent through ACP as a long-lived external coding-agent session supervised by Hecate.",
    cost_mode: "external",
    auth_status: fixture.authStatus ?? "unknown",
    auth_error: fixture.authStatus === "unauthenticated" ? "Run claude /login" : undefined,
    claude_code_cli:
      fixture.available === false
        ? { available: false }
        : { available: true, path: "/usr/local/bin/claude" },
  };

  await page.route("/hecate/v1/agent-adapters*", async (route) => {
    const method = route.request().method();
    const url = route.request().url();
    if (method === "POST" && url.includes("/claude_code/probe")) {
      const status =
        fixture.available === false ? "not_installed" : (fixture.healthStatus ?? "ready");
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          object: "agent_adapter_probe",
          data: {
            adapter:
              status === "ready"
                ? { ...adapter, auth_status: "ok", auth_error: undefined }
                : adapter,
            health: {
              adapter_id: "claude_code",
              status,
              stage:
                status === "ready"
                  ? "ready"
                  : status === "not_installed"
                    ? "lookup"
                    : "new_session",
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
    agent_id: "claude_code",
    agent_name: "Claude Code",
    driver_kind: "acp",
    native_session_id: "native-claude-code-e2e",
    project_id: "proj_e2e",
    workspace: "/tmp/hecate-e2e",
    status: "idle",
    message_count: 0,
    config_options: [],
    messages: [],
    created_at: "2026-05-14T10:00:00Z",
    updated_at: "2026-05-14T10:00:00Z",
  };
  await page.route("/hecate/v1/chat/sessions*", async (route) => {
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
      method === "GET" &&
      path.startsWith("/hecate/v1/chat/sessions/claude-code-onboarding-e2e") &&
      !path.endsWith("/stream") &&
      !path.endsWith("/approvals")
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
    if (
      method === "GET" &&
      path === "/hecate/v1/chat/sessions/claude-code-onboarding-e2e/approvals"
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_approvals", data: [] }),
      });
      return;
    }
    await route.continue();
  });
  await page.route("/hecate/v1/chat/sessions/claude-code-onboarding-e2e", async (route) => {
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
  await page.route("/hecate/v1/chat/sessions/claude-code-onboarding-e2e/stream", (route) =>
    route.fulfill({ status: 200, contentType: "text/event-stream", body: "" }),
  );
  await page.route("/hecate/v1/chat/sessions/claude-code-onboarding-e2e/approvals*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "chat_approvals", data: [] }),
    }),
  );

  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.agentAdapterID", "claude_code");
    window.localStorage.setItem("hecate.project", "proj_e2e");
    window.localStorage.setItem("hecate.chatSessionID", "claude-code-onboarding-e2e");
  });
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.getByRole("button", { name: /Chat Claude Code chat, Claude Code/ }).click();
}

test("Claude Code setup appears when the adapter is not installed", async ({ page }) => {
  await openClaudeExternalAgent(page, { available: false, healthStatus: "not_installed" });

  await expect(page.getByText("Claude Code is unavailable")).toBeVisible();
  await expect(page.getByText(/Install Claude Code, then sign in with Claude Code/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Install" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Auth" })).toBeVisible();
  await page.locator("textarea").fill("hello from Claude Code");
  await expect(page.getByRole("button", { name: "Send message" })).toBeDisabled();
});

test("Claude Code setup is cleared after a ready probe", async ({ page }) => {
  await openClaudeExternalAgent(page, {
    available: true,
    authStatus: "unknown",
    healthStatus: "ready",
  });

  await expect(page.getByText("Set up Claude Code")).toHaveCount(0);
  await expect(page.locator("textarea")).toBeVisible();
});

test("Claude Code setup stays visible when the probe requires local auth", async ({ page }) => {
  await openClaudeExternalAgent(page, {
    available: true,
    authStatus: "unauthenticated",
    healthStatus: "auth_required",
  });

  await expect(page.getByText("Set up Claude Code")).toBeVisible();
  await expect(page.getByText(/Claude Code needs local CLI sign-in/)).toBeVisible();
  await page.locator("textarea").fill("hello from Claude Code");
  await expect(page.getByRole("button", { name: "Send message" })).toBeDisabled();
});

test("Cursor Agent setup explains CLI sign-in without launching the CLI", async ({ page }) => {
  await openExternalAgentReadinessFixture(page, {
    id: "cursor_agent",
    name: "Cursor Agent",
    command: "cursor-agent",
    available: true,
    authStatus: "unauthenticated",
    healthStatus: "auth_required",
  });

  await expect(page.getByText("Set up Cursor Agent")).toBeVisible();
  await expect(page.getByText(/cursor-agent login/)).toBeVisible();
  await page.locator("textarea").fill("hello from Cursor Agent");
  await expect(page.getByRole("button", { name: "Send message" })).toBeDisabled();
});

test("Grok Build setup mentions CLI sign-in and model selection without launching the CLI", async ({
  page,
}) => {
  await openExternalAgentReadinessFixture(page, {
    id: "grok_build",
    name: "Grok Build",
    command: "grok",
    args: ["agent", "stdio"],
    available: true,
    authStatus: "unauthenticated",
    healthStatus: "auth_required",
  });

  await expect(page.getByText("Set up Grok Build")).toBeVisible();
  await expect(page.getByText(/grok login/)).toBeVisible();
  await expect(page.getByText(/model selected/)).toBeVisible();
  await page.locator("textarea").fill("hello from Grok Build");
  await expect(page.getByRole("button", { name: "Send message" })).toBeDisabled();
});

test("Claude Code chat is enabled when local CLI auth is verified", async ({ page }) => {
  await openClaudeExternalAgent(page, {
    available: true,
    authStatus: "ok",
    healthStatus: "ready",
  });

  await expect(page.getByText("Set up Claude Code")).toHaveCount(0);
  await expect(page.locator("textarea")).toBeVisible();
  await page.locator("textarea").fill("hello from Claude Code");
  await expect(page.locator("button[type='submit']")).toBeEnabled();
});
