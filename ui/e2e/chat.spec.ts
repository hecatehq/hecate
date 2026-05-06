import { expect, test as baseTest, mockGatewayAPIs, MOCK_MODELS, MOCK_PROVIDERS, MOCK_ADMIN_CONFIG_WITH_PROVIDERS } from "./fixtures";
import type { Page } from "@playwright/test";

// Chat tests need a populated provider list — without one, AppShell hides
// the chat workspace behind a "No providers configured" placeholder. Override
// the default empty-list mock with the populated fixture for every chat test.
const test = baseTest.extend<{ page: Page }>({
  page: async ({ page }, use) => {
    await page.unrouteAll({ behavior: "ignoreErrors" });
    await mockGatewayAPIs(page, { adminConfig: MOCK_ADMIN_CONFIG_WITH_PROVIDERS });
    await use(page);
  },
});

test.beforeEach(async ({ page }) => {
  await page.goto("/");
  // Chat is the default workspace
  await page.waitForSelector(".hecate-activitybar");
});

async function switchToModel(page: Page) {
  await page.getByRole("button", { name: "tools off", exact: true }).click();
}

test("renders the message textarea and send button", async ({ page }) => {
  await expect(page.getByRole("button", { name: "Hecate Chat", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "External Agent", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "tools off", exact: true })).toBeVisible();
  await expect(page.locator("textarea")).toBeVisible();
  await expect(page.locator("button[type='submit']")).toBeVisible();
});

test("send button is disabled when message is empty", async ({ page }) => {
  await page.locator("textarea").fill("");
  await expect(page.locator("button[type='submit']")).toBeDisabled();
});

test("send button becomes enabled when message has content", async ({ page }) => {
  await switchToModel(page);
  await page.locator("textarea").fill("Hello");
  await expect(page.locator("button[type='submit']")).toBeEnabled();
});

test("model picker opens and lists models from mock data", async ({ page }) => {
  await switchToModel(page);
  // Wait for models to load, then open the picker
  const modelBtn = page.getByRole("button", { name: /model picker/i });
  await modelBtn.click();

  for (const m of MOCK_MODELS) {
    await expect(page.locator(`.dropdown-menu`)).toContainText(m.id);
  }
});

test("model picker filters by search input", async ({ page }) => {
  await switchToModel(page);
  const modelBtn = page.getByRole("button", { name: /model picker/i });
  await modelBtn.click();

  const menu = page.locator(".dropdown-menu");
  await menu.locator("input").fill("gpt");

  await expect(menu).toContainText("gpt-4o");
  await expect(menu).not.toContainText("claude");
});

test("selecting a model closes the picker and updates the button label", async ({ page }) => {
  await switchToModel(page);
  const modelBtn = page.getByRole("button", { name: /model picker/i });
  await modelBtn.click();

  await page.locator(".dropdown-menu").locator("text=gpt-4o-mini").first().click();

  await expect(page.locator(".dropdown-menu")).not.toBeVisible();
  await expect(modelBtn).toContainText("gpt-4o-mini");
});

test("provider picker shows healthy providers", async ({ page }) => {
  await switchToModel(page);
  const healthyProviders = MOCK_PROVIDERS.filter(p => p.healthy);
  const providerBtn = page.locator("button", { hasText: /all providers/i });
  await providerBtn.click();

  const menu = page.locator(".dropdown-menu").first();
  for (const p of healthyProviders) {
    await expect(menu).toContainText(p.name, { ignoreCase: true });
  }
});

test("New chat button clears the active conversation", async ({ page }) => {
  await switchToModel(page);
  // Fill the message box so we can verify the state resets
  await page.locator("textarea").fill("some prior message");
  await page.getByRole("button", { name: /new chat/i }).click();
  // After starting a new chat, the empty state stays visible and
  // composer state is cleared.
  await expect(page.getByText("Send a message to start this chat.")).toBeVisible();
  await expect(page.locator("textarea")).toHaveValue("");
});

test("system prompt editor opens and closes", async ({ page }) => {
  await switchToModel(page);
  const systemBtn = page.locator("button", { hasText: /system/i });
  await systemBtn.click();
  await expect(page.getByText("SYSTEM PROMPT")).toBeVisible();
  await expect(page.locator("textarea").nth(1)).toBeVisible();

  await systemBtn.click();
  await expect(page.getByText("SYSTEM PROMPT")).not.toBeVisible();
});

test("Enter-switch toggle is visible in the input toolbar and clickable", async ({ page }) => {
  // The label is one of "↵ to send" or "⌘+↵ to send" / "Ctrl+↵ to send" depending on OS.
  const toggle = page.locator("button").filter({ hasText: /↵ to send/ });
  await expect(toggle).toBeVisible();
  const before = await toggle.textContent();
  await toggle.click();
  // After click, label should change.
  await expect(toggle).not.toHaveText(before ?? "");
});

test("Enter-switch preference persists across reload via localStorage", async ({ page }) => {
  const toggle = page.locator("button").filter({ hasText: /↵ to send/ });
  const initial = await toggle.textContent();
  await toggle.click();
  const after = await toggle.textContent();
  expect(after).not.toBe(initial);

  await page.reload();
  await page.waitForSelector(".hecate-activitybar");
  const reloaded = page.locator("button").filter({ hasText: /↵ to send/ });
  await expect(reloaded).toHaveText(after ?? "");
});

test("workspace selection persists across reload", async ({ page }) => {
  await page.keyboard.press("2"); // Providers
  await expect(page.locator(".hecate-activitybar [aria-current='page']")).toHaveAttribute("aria-label", /Providers/);

  await page.reload();
  await page.waitForSelector(".hecate-activitybar");
  await expect(page.locator(".hecate-activitybar [aria-current='page']")).toHaveAttribute("aria-label", /Providers/);
});

// A failing /v1/chat/completions surfaces inline beneath the chat header.
// Toast is gone for chat errors — the chat surface owns its own banner so a
// single source of truth shows up next to the input. The "api key is
// required for cloud provider X" wire message is humanized into "X has no
// API key. Open the Providers tab and add one." before reaching the DOM.
test("chat error renders inline with the humanized message", async ({ page }) => {
  await switchToModel(page);
  await page.route("/v1/agent-chat/sessions", async route => {
    if (route.request().method() !== "POST") {
      await route.continue();
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "agent_chat_session",
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
  await page.route("/v1/agent-chat/sessions/chat_err_e2e/stream", route =>
    route.fulfill({ status: 200, contentType: "text/event-stream", body: "" }),
  );
  await page.route("/v1/agent-chat/sessions/chat_err_e2e/messages", route =>
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

test("empty model chat can add all detected local providers in one click", async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await mockGatewayAPIs(page);
  const created: Array<Record<string, unknown>> = [];
  await page.route("/admin/control-plane/providers", async route => {
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

  await page.getByRole("button", { name: "Add detected providers" }).click();

  await expect.poll(() => created.map(body => body.preset_id).sort()).toEqual(["lmstudio", "ollama"]);
  await expect(page.getByText("Provider is configured")).toBeVisible();
  await expect(page.getByText("none discovered")).toBeVisible();
  await expect(page.getByRole("button", { name: /Add detected provider/i })).toHaveCount(0);
});

test("empty Hecate Agent chat can add all detected local providers in one click", async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await mockGatewayAPIs(page);
  const created: Array<Record<string, unknown>> = [];
  await page.route("/admin/control-plane/providers", async route => {
    if (route.request().method() === "POST") {
      created.push(JSON.parse(route.request().postData() ?? "{}"));
    }
    await route.fallback();
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await expect(page.getByText("Detected locally")).toBeVisible();
  await expect(page.getByText("Ollama", { exact: true })).toBeVisible();
  await expect(page.getByText("LM Studio", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Add detected providers" }).click();

  await expect.poll(() => created.map(body => body.preset_id).sort()).toEqual(["lmstudio", "ollama"]);
  await expect(page.getByText("Provider is configured")).toBeVisible();
  await expect(page.getByRole("button", { name: /Add detected provider/i })).toHaveCount(0);
});

test("Hecate Agent local-provider onboarding renders the real final answer and unlocks model choice after completion", async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "hecate_agent");
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
  await page.route("/v1/agent-chat/sessions", async route => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "agent_chat_sessions", data: sessions }),
      });
      return;
    }
    if (route.request().method() === "POST") {
      const body = await route.request().postDataJSON();
      const session = {
        id: "chat-hecate-e2e",
        title: body.title || "show diff",
        runtime_kind: "hecate_agent",
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
        body: JSON.stringify({ object: "agent_chat_session", data: session }),
      });
      return;
    }
    await route.fulfill({ status: 405, body: "" });
  });

  await page.route("/v1/agent-chat/sessions/chat-hecate-e2e/stream", route => route.fulfill({
    status: 200,
    contentType: "text/event-stream",
    body: "",
  }));

  let messagePayload: Record<string, unknown> | null = null;
  await page.route("/v1/agent-chat/sessions/chat-hecate-e2e/messages", async route => {
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
          runtime_kind: "hecate_agent",
          segment_id: "task:task-hecate-e2e",
          task_id: "task-hecate-e2e",
          role: "user",
          content: "show diff",
          created_at: "2026-05-06T10:00:00Z",
        },
        {
          id: "msg-assistant-e2e",
          runtime_kind: "hecate_agent",
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
      body: JSON.stringify({ object: "agent_chat_session", data: completed }),
    });
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await page.getByRole("button", { name: "Add detected providers" }).click();
  await expect(page.getByRole("button", { name: /Add detected provider/i })).toHaveCount(0);
  await expect(page.getByText("2 configured")).toBeVisible();

  await page.getByRole("button", { name: /model picker/i }).click();
  await page.locator(".dropdown-menu").locator("text=qwen2.5").first().click();
  await expect(page.getByRole("button", { name: "tools: basic" })).toBeVisible();

  await page.locator("textarea").fill("show diff");
  await page.locator("button[type='submit']").click();

  await expect(page.locator("body")).toContainText("+changed line");
  await expect(page.locator("body")).not.toContainText("Hecate Agent run completed.");
  await expect.poll(() => messagePayload).toMatchObject({
    runtime_kind: "hecate_agent",
    model: "qwen2.5",
    workspace: "/tmp/hecate-e2e-workspace",
  });
  await expect(page.getByRole("button", { name: "Model picker: qwen2.5" })).toBeEnabled();
});

test("Hecate Chat can move tools on, tools off, then tools on again in one transcript", async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.chatTarget", "hecate_agent");
    window.localStorage.setItem("hecate.agentWorkspace", "/tmp/hecate-e2e-workspace");
  });
  await mockGatewayAPIs(page, {
    adminConfig: {
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

  await page.route("/v1/agent-chat/sessions", async route => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "agent_chat_sessions", data: session ? [session] : [] }),
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
        body: JSON.stringify({ object: "agent_chat_session", data: session }),
      });
      return;
    }
    await route.fulfill({ status: 405, body: "" });
  });

  await page.route("/v1/agent-chat/sessions/chat-tools-switch-e2e/stream", route => route.fulfill({
    status: 200,
    contentType: "text/event-stream",
    body: "",
  }));

  await page.route("/v1/agent-chat/sessions/chat-tools-switch-e2e", route => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({ object: "agent_chat_session", data: session }),
  }));

  await page.route("/v1/agent-chat/sessions/chat-tools-switch-e2e/messages", async route => {
    const body = await route.request().postDataJSON();
    submittedTurns.push(body);
    const turn = submittedTurns.length;
    const runtimeKind = body.runtime_kind || "model";
    const isHecateAgent = runtimeKind === "hecate_agent";
    const taskID = isHecateAgent ? `task-tools-${submittedTurns.filter(t => t.runtime_kind === "hecate_agent").length}` : "";
    const runID = isHecateAgent ? `run-tools-${submittedTurns.filter(t => t.runtime_kind === "hecate_agent").length}` : "";
    const assistantContent = isHecateAgent
      ? `Tools answer ${taskID.endsWith("-1") ? "one" : "two"} from ${body.model}`
      : `Direct model answer from ${body.model}`;

    messages.push(
      {
        id: `msg-user-${turn}`,
        runtime_kind: runtimeKind,
        segment_id: isHecateAgent ? `task:${taskID}` : `model:${turn}`,
        task_id: isHecateAgent ? taskID : undefined,
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
        status: "completed",
        cost_mode: isHecateAgent ? "hecate" : "provider",
        activities: isHecateAgent
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
      status: "completed",
      message_count: messages.length,
      messages,
    };

    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "agent_chat_session", data: session }),
    });
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await page.getByRole("button", { name: /model picker/i }).click();
  await page.locator(".dropdown-menu").locator("text=qwen2.5").first().click();
  await page.getByRole("button", { name: "tools: basic", exact: true }).click();

  await page.locator("textarea").fill("first with tools");
  await page.locator("button[type='submit']").click();
  await expect(page.locator("body")).toContainText("Tools answer one from qwen2.5");

  await page.getByRole("button", { name: "tools off", exact: true }).click();
  await page.locator("textarea").fill("direct model turn");
  await page.locator("button[type='submit']").click();
  await expect(page.locator("body")).toContainText("Direct model answer from qwen2.5");

  await page.getByRole("button", { name: "tools: basic", exact: true }).click();
  await page.locator("textarea").fill("tools again");
  await page.locator("button[type='submit']").click();
  await expect(page.locator("body")).toContainText("Tools answer two from qwen2.5");

  expect(createSessionCount).toBe(1);
  expect(submittedTurns.map(turn => turn.runtime_kind)).toEqual(["hecate_agent", "model", "hecate_agent"]);
  expect(submittedTurns.map(turn => turn.content)).toEqual(["first with tools", "direct model turn", "tools again"]);
  expect(submittedTurns.filter(turn => turn.runtime_kind === "hecate_agent")).toEqual([
    expect.objectContaining({ model: "qwen2.5", workspace: "/tmp/hecate-e2e-workspace" }),
    expect.objectContaining({ model: "qwen2.5", workspace: "/tmp/hecate-e2e-workspace" }),
  ]);
});

test("configured provider with no models shows troubleshooting, not detected-provider setup", async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  await mockGatewayAPIs(page, {
    adminConfig: {
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
  await page.route("/admin/providers*", route => route.fulfill({
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

  await expect(page.getByText("Provider is configured")).toBeVisible();
  await expect(page.getByText("none discovered")).toBeVisible();
  await expect(page.getByText(/Start the local provider app/)).toBeVisible();
  await expect(page.getByText("Detected locally")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Add detected provider/i })).toHaveCount(0);
});

// External-agent approval happy path. Seeds an active session with one
// pending approval, then exercises the operator path: catch-up refetch
// populates the banner, Review opens the modal, Allow resolves, and the
// banner clears.
test("agent approval banner: review, allow, banner clears", async ({ page }) => {
  // Seed the persisted active session before the page loads, so the
  // dashboard fan-out runs the catch-up refetch on mount.
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.agentChatSessionID", "a-e2e-1");
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
  });

  // The dashboard fan-out asks for /v1/agent-chat/sessions on mount
  // and prunes any stored activeSessionID that isn't in the list. So
  // a-e2e-1 must appear here for the catch-up refetch to fire.
  await page.route("/v1/agent-chat/sessions", (route) => {
    if (route.request().method() !== "GET") {
      void route.fulfill({ status: 405, body: "" });
      return;
    }
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "agent_chat_sessions",
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
  await page.route("/v1/agent-chat/sessions/a-e2e-1/approvals*", (route) => {
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

  await page.route("/v1/agent-chat/sessions/a-e2e-1/approvals/ap-e2e-1", (route) => {
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "agent_chat_approval",
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
  await page.route("/v1/agent-chat/sessions/a-e2e-1/approvals/ap-e2e-1/resolve", (route) => {
    resolveCalls += 1;
    approvalResolved = true;
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "agent_chat_approval",
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
  await page.route("/v1/agent-chat/sessions/a-e2e-1", (route) => {
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "agent_chat_session",
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
    window.localStorage.setItem("hecate.agentChatSessionID", "a-diff-1");
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
  });

  await page.route("/v1/agent-chat/sessions", (route) => {
    if (route.request().method() !== "GET") {
      void route.fulfill({ status: 405, body: "" });
      return;
    }
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "agent_chat_sessions",
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
    object: "agent_chat_session",
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

  await page.route("/v1/agent-chat/sessions/a-diff-1", (route) => {
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(sessionBody),
    });
  });

  await page.route("/v1/agent-chat/sessions/a-diff-1/approvals*", (route) => {
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "list", data: [] }),
    });
  });

  await page.route("/v1/agent-chat/sessions/a-diff-1/messages/m-agent/files", (route) => {
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "agent_chat_changed_files",
        data: [
          { path: "README.md", additions: 2, deletions: 1, status: "modified" },
          { path: "docs/runtime-api.md", additions: 4, deletions: 0, status: "added" },
        ],
      }),
    });
  });

  await page.route("/v1/agent-chat/sessions/a-diff-1/messages/m-agent/files/README.md", (route) => {
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "agent_chat_changed_file_diff",
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
  await page.route("/v1/agent-chat/sessions/a-diff-1/messages/m-agent/revert", async (route) => {
    revertedPaths = (await route.request().postDataJSON()).paths;
    void route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "agent_chat_revert",
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
