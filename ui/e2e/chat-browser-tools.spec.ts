import type { Page } from "@playwright/test";

import {
  expect,
  mockGatewayAPIs,
  MOCK_SETTINGS_CONFIG_WITH_PROVIDERS,
  test as baseTest,
} from "./fixtures";

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
  await page.waitForSelector(".hecate-activitybar");
});

test("previews and freezes Hecate Chat browser grants before browser approval", async ({
  page,
}) => {
  const policy = {
    id: "browser_review",
    name: "Browser review",
    surface: "hecate_chat",
    tools_enabled: true,
    writes_allowed: false,
    network_allowed: false,
    browser_allowed: true,
    browser_interactions_allowed: true,
    browser_allowed_origins: ["https://app.example.test", "https://status.example.test:8443"],
    approval_policy: "require",
    project_memory_policy: "inherit",
    context_source_policy: "inherit",
  };
  let createBody: Record<string, unknown> = {};
  let session: Record<string, unknown> | null = null;

  await page.route(/\/hecate\/v1\/agent-presets(?:\?.*)?$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "agent_presets", data: [policy] }),
    }),
  );
  await page.route(/\/hecate\/v1\/chat\/sessions(?:\/.*)?(?:\?.*)?$/, async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const sessionPath = "/hecate/v1/chat/sessions/chat-browser-policy-e2e";
    if (request.method() === "POST" && path === "/hecate/v1/chat/sessions") {
      createBody = await request.postDataJSON();
      session = {
        id: "chat-browser-policy-e2e",
        title: "Browser review",
        agent_id: "hecate",
        provider: String(createBody.provider ?? ""),
        model: String(createBody.model ?? ""),
        project_id: String(createBody.project_id ?? ""),
        task_id: "task-browser-policy-e2e",
        latest_run_id: "run-browser-policy-e2e",
        workspace: "/tmp/hecate-e2e",
        workspace_mode: "persistent",
        status: "awaiting_approval",
        capabilities: {
          tool_calling: "basic",
          streaming: true,
          source: "provider",
        },
        agent_preset: {
          id: policy.id,
          name: policy.name,
          tools_enabled: policy.tools_enabled,
          writes_allowed: policy.writes_allowed,
          network_allowed: policy.network_allowed,
          browser_allowed: policy.browser_allowed,
          browser_interactions_allowed: policy.browser_interactions_allowed,
          browser_allowed_origins: policy.browser_allowed_origins,
          approval_policy: policy.approval_policy,
        },
        message_count: 1,
        messages: [
          {
            id: "browser-approval-message-e2e",
            role: "assistant",
            content: "",
            status: "awaiting_approval",
            run_id: "run-browser-policy-e2e",
            activities: [
              {
                id: "task:step:browser-approval-e2e",
                type: "approval",
                status: "awaiting_approval",
                kind: "approval",
                title: "Awaiting approval — browser_flow",
                detail:
                  'Agent requested tools that require approval: browser_flow destination=https://app.example.test/account; actions: 1. click button "Delete account"; 2. wait status "Account deleted". Warning: clicks can run page scripts and may change state in the allowed app. - awaiting_approval',
                approval_id: "approval-browser-policy-e2e",
                action_summary: ["browser_flow url=https://app.example.test/account actions=2"],
                action_summary_incomplete: false,
                needs_action: true,
              },
            ],
          },
        ],
      };
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_session", data: session }),
      });
      return;
    }
    if (request.method() === "GET" && path === sessionPath && session) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_session", data: session }),
      });
      return;
    }
    if (request.method() === "GET" && path === `${sessionPath}/stream`) {
      await route.fulfill({ status: 200, contentType: "text/event-stream", body: "" });
      return;
    }
    if (request.method() === "GET" && path === `${sessionPath}/approvals`) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_approvals", data: [] }),
      });
      return;
    }
    await route.fallback();
  });

  const policySelect = page.getByRole("combobox", {
    name: "Work policy for new Hecate chat",
  });
  await policySelect.focus();
  await expect(page.getByRole("option", { name: "Browser review" })).toHaveCount(1);
  await policySelect.selectOption(policy.id);

  const preview = page.getByRole("group", { name: "Browser permissions" });
  await expect(preview.getByText("Configured · per-call approval", { exact: true })).toHaveCount(2);
  const previewOrigins = preview.getByRole("list", { name: "Allowed browser origins" });
  await expect(previewOrigins.getByText("https://app.example.test", { exact: true })).toBeVisible();
  await expect(
    previewOrigins.getByText("https://status.example.test:8443", { exact: true }),
  ).toBeVisible();
  await expect(preview).toContainText("configure a local browser");
  await expect(preview).toContainText("browser readiness is not implied");
  await expect(preview).toContainText("Tools alone does not grant access");

  await page.getByRole("button", { name: "New Hecate chat", exact: true }).click();
  await expect.poll(() => createBody.agent_preset_id).toBe(policy.id);
  expect(createBody).toMatchObject({ agent_id: "hecate" });
  expect(createBody).not.toHaveProperty("browser_allowed");
  expect(createBody).not.toHaveProperty("browser_interactions_allowed");
  expect(createBody).not.toHaveProperty("browser_allowed_origins");

  const approval = page.getByTestId("hecate-task-approval-banner");
  await expect(approval.getByText("Browser interaction", { exact: true })).toBeVisible();
  await expect(approval).toContainText("destination=https://app.example.test/account");
  await expect(approval).toContainText('1. click button "Delete account"');
  await expect(approval).toContainText('2. wait status "Account deleted"');
  await expect(approval).toContainText(
    "clicks can run page scripts and may change state in the allowed app",
  );
  await expect(approval).toContainText(
    "browser_flow url=https://app.example.test/account actions=2",
  );
  await expect(approval.getByRole("button", { name: "Approve Browser interaction" })).toBeEnabled();

  await page.getByRole("button", { name: "Chat settings" }).click();
  const settings = page.getByRole("complementary", { name: "Chat settings panel" });
  const frozen = settings.getByRole("group", { name: "Browser permissions" });
  await expect(
    frozen.getByText("Configured · approval required for each call", { exact: true }),
  ).toHaveCount(2);
  const frozenOrigins = frozen.getByRole("list", { name: "Allowed browser origins" });
  await expect(frozenOrigins.getByText("https://app.example.test", { exact: true })).toBeVisible();
  await expect(
    frozenOrigins.getByText("https://status.example.test:8443", { exact: true }),
  ).toBeVisible();
  await expect(frozen).toContainText("Tools alone does not grant browser access");
  await expect(frozen).toContainText(
    "policy configuration by itself does not confirm that a local browser is ready",
  );
});
