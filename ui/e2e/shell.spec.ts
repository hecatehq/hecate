import { expect, test } from "./fixtures";

test.beforeEach(async ({ page }) => {
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
});

test("renders the activity bar with all workspace buttons", async ({ page }) => {
  const nav = page.locator(".hecate-activitybar");
  await expect(nav).toBeVisible();

  for (const label of ["Chats", "Connections", "Tasks", "Observability", "Usage", "Settings"]) {
    await expect(nav.locator(`[aria-label^="${label}"]`)).toBeVisible();
  }
});

test("shows the status bar with brand and session label", async ({ page }) => {
  const bar = page.locator(".hecate-statusbar");
  await expect(bar).toBeVisible();
  await expect(bar.locator(".hecate-statusbar__brand")).toHaveText("hecate");
  await expect(bar).toContainText("On Test host");
});

test("status bar shows configured provider count and model count", async ({ page }) => {
  const bar = page.locator(".hecate-statusbar");
  // Wait for dashboard data to load
  await expect(bar).toContainText("configured");
  await expect(bar).toContainText("models");
});

test("adapts shell chrome for a phone viewport", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.route("/healthz", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        status: "ok",
        time: "2026-04-25T00:00:00Z",
        version: "mobile-test",
      }),
    }),
  );
  await page.route("/hecate/v1/whoami", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "session",
        data: {
          role: "operator",
          runtime_host: {
            id: "runtime_phone",
            label: "Pocket test host",
            runtime_mode: "remote_runtime",
            operator_access: "remote_supervision",
            local_only_actions_available: false,
          },
          remote_identity: {
            actor_id: "actor_phone",
            org_id: "org_phone",
            project_id: "project_phone",
            runtime_id: "runtime_phone",
          },
        },
      }),
    }),
  );
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  const statusbar = page.locator(".hecate-statusbar");
  const content = page.locator(".hecate-content");
  const nav = page.locator(".hecate-activitybar");
  const chatSidebar = page.locator(".chat-sidebar");
  const chatMainBody = page.locator(".chat-main-body");
  const sessionIdentity = statusbar.locator(".hecate-statusbar__session");

  await expect(page.locator('meta[name="viewport"]')).toHaveAttribute(
    "content",
    /viewport-fit=cover/,
  );

  await expect(nav).toHaveCSS("flex-direction", "row");
  await expect(statusbar.locator(".hecate-statusbar__brand")).toBeVisible();
  await expect(statusbar.locator(".hecate-statusbar__runtime")).toBeHidden();
  await expect(sessionIdentity).toHaveText("Supervising Pocket test host");
  await expect(sessionIdentity).toHaveAttribute("title", /run on that host/i);
  await expect(statusbar.locator(".hecate-statusbar__providers")).toBeHidden();
  await expect(statusbar.locator(".hecate-statusbar__models")).toBeHidden();

  const chatsButtonBox = await nav.locator('[aria-label="Chats"]').boundingBox();
  expect(chatsButtonBox).not.toBeNull();
  expect(chatsButtonBox!.width).toBeGreaterThanOrEqual(44);
  expect(chatsButtonBox!.height).toBeGreaterThanOrEqual(44);

  const navGeometry = await nav.evaluate((element) => ({
    clientWidth: element.clientWidth,
    scrollWidth: element.scrollWidth,
    children: Array.from(element.querySelectorAll("a, button")).map((child) => {
      const rect = child.getBoundingClientRect();
      return { left: rect.left, right: rect.right };
    }),
    bounds: (() => {
      const rect = element.getBoundingClientRect();
      return { left: rect.left, right: rect.right };
    })(),
  }));
  expect(navGeometry.scrollWidth).toBeLessThanOrEqual(navGeometry.clientWidth + 1);
  for (const child of navGeometry.children) {
    expect(child.left).toBeGreaterThanOrEqual(navGeometry.bounds.left - 1);
    expect(child.right).toBeLessThanOrEqual(navGeometry.bounds.right + 1);
  }

  const statusBox = await statusbar.boundingBox();
  const contentBox = await content.boundingBox();
  const navBox = await nav.boundingBox();
  expect(statusBox).not.toBeNull();
  expect(contentBox).not.toBeNull();
  expect(navBox).not.toBeNull();
  expect(statusBox!.y).toBeLessThan(contentBox!.y);
  expect(navBox!.y).toBeGreaterThan(contentBox!.y);

  const sidebarBox = await chatSidebar.boundingBox();
  expect(sidebarBox).not.toBeNull();
  expect(sidebarBox!.width).toBeGreaterThan(330);
  expect(sidebarBox!.height).toBeGreaterThan(500);
  await expect(page.locator(".chat-view")).toHaveClass(/chat-view--sidebar-open/);
  await expect(chatMainBody).toBeHidden();
  await expect(page.getByRole("button", { name: "Close chats sidebar" })).toHaveCount(0);
});

test.describe("coarse-pointer landscape shell", () => {
  test.use({
    hasTouch: true,
    isMobile: true,
    viewport: { width: 844, height: 390 },
  });

  test("keeps touch targets and landscape safe-area layout active", async ({ page }) => {
    const session = {
      id: "landscape-chat-e2e",
      title: "Codex chat",
      agent_id: "codex",
      agent_name: "Codex",
      driver_kind: "acp",
      native_session_id: "native-landscape-e2e",
      project_id: "",
      workspace: "/tmp/hecate-e2e",
      status: "idle",
      message_count: 0,
      messages: [],
      created_at: "2026-05-14T10:00:00Z",
      updated_at: "2026-05-14T10:00:00Z",
    };
    await page.route("/hecate/v1/chat/sessions", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_sessions", data: [session] }),
      }),
    );
    await page.route("/hecate/v1/chat/sessions/landscape-chat-e2e", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_session", data: session }),
      }),
    );
    await page.route("/hecate/v1/chat/sessions/landscape-chat-e2e/approvals*", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "chat_approvals", data: [] }),
      }),
    );
    await page.goto("/");
    await page.waitForSelector(".hecate-activitybar");

    expect(
      await page.evaluate(() => matchMedia("(hover: none) and (pointer: coarse)").matches),
    ).toBe(true);
    const chatsButton = page.locator('.hecate-activitybar [aria-label="Chats"]');
    const chatsButtonBox = await chatsButton.boundingBox();
    expect(chatsButtonBox).not.toBeNull();
    expect(chatsButtonBox!.width).toBeGreaterThanOrEqual(44);
    expect(chatsButtonBox!.height).toBeGreaterThanOrEqual(44);
    await expect(chatsButton).toHaveCSS("touch-action", "manipulation");

    const safeAreaPaddingRule = await page.evaluate(() => {
      for (const sheet of Array.from(document.styleSheets)) {
        for (const rule of Array.from(sheet.cssRules)) {
          if (!(rule instanceof CSSMediaRule) || !rule.conditionText.includes("pointer: coarse")) {
            continue;
          }
          for (const nestedRule of Array.from(rule.cssRules)) {
            if (
              nestedRule instanceof CSSStyleRule &&
              nestedRule.selectorText === ".hecate-workarea"
            ) {
              return nestedRule.style.paddingTop;
            }
          }
        }
      }
      return "";
    });
    expect(safeAreaPaddingRule).toContain("safe-area-inset-top");

    const renameButton = page.getByRole("button", { name: "Rename chat Codex chat" });
    await expect(renameButton).toBeVisible();
    const renameButtonBox = await renameButton.boundingBox();
    expect(renameButtonBox).not.toBeNull();
    expect(renameButtonBox!.width).toBeGreaterThanOrEqual(44);
    expect(renameButtonBox!.height).toBeGreaterThanOrEqual(44);

    await page.getByRole("link", { name: /Chat Codex chat, Codex/i }).click();
    await expect(page.locator(".chat-view")).not.toHaveClass(/chat-view--sidebar-open/);
    await expect(page.getByRole("button", { name: "Open chats sidebar" })).toBeVisible();

    await page.getByRole("button", { name: "Chat settings" }).click();
    const mainBody = page.locator(".chat-main-body");
    const mainContent = page.locator(".chat-main-content");
    const settingsPanel = page.getByLabel("Chat settings panel");
    await expect(mainContent).toHaveCSS("display", "none");
    await expect(page.getByRole("separator", { name: "Resize right panel" })).toBeHidden();
    const mainBodyBox = await mainBody.boundingBox();
    const settingsPanelBox = await settingsPanel.boundingBox();
    expect(mainBodyBox).not.toBeNull();
    expect(settingsPanelBox).not.toBeNull();
    expect(Math.abs(settingsPanelBox!.width - mainBodyBox!.width)).toBeLessThanOrEqual(1);
  });
});

test("stacks settings maintenance controls for a phone viewport", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await page.locator(".hecate-activitybar [aria-label^='Settings']").click();

  const controls = page.locator(".retention-controls");
  const cleanupButton = page.locator(".retention-cleanup-button");
  await expect(controls).toHaveCSS("flex-direction", "column");
  await expect(cleanupButton).toBeVisible();

  const controlsBox = await controls.boundingBox();
  const buttonBox = await cleanupButton.boundingBox();
  expect(controlsBox).not.toBeNull();
  expect(buttonBox).not.toBeNull();
  expect(buttonBox!.width).toBeGreaterThan(280);
  expect(buttonBox!.y).toBeGreaterThan(controlsBox!.y + 40);

  const pluginsHeader = page.locator(".settings-section-header").filter({ hasText: "Plugins" });
  const pluginsCopy = pluginsHeader.locator(".settings-section-header__copy");
  const pluginsAction = pluginsHeader.locator(".settings-section-header__actions");
  await expect(pluginsHeader).toHaveCSS("flex-wrap", "wrap");
  const pluginsCopyBox = await pluginsCopy.boundingBox();
  const pluginsActionBox = await pluginsAction.boundingBox();
  expect(pluginsCopyBox).not.toBeNull();
  expect(pluginsActionBox).not.toBeNull();
  expect(pluginsCopyBox!.width).toBeGreaterThan(300);
  expect(pluginsActionBox!.y).toBeGreaterThanOrEqual(pluginsCopyBox!.y + pluginsCopyBox!.height);
});

test("uses a single task pane and viewport-contained task sheet on a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.route("**/hecate/v1/tasks?*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "list", data: [] }),
    }),
  );
  await page.route("**/hecate/v1/task-schedules?*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ object: "task_schedules", data: [] }),
    }),
  );
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await page.locator('.hecate-activitybar [aria-label="Tasks"]').click();
  const workspace = page.locator(".tasks-workspace");
  await expect(workspace).toHaveClass(/tasks-workspace--index-open/);
  await expect(page.getByText("Start a task")).toBeHidden();

  await page.getByRole("button", { name: "New task" }).click();
  const dialog = page.getByRole("dialog", { name: "New task" });
  await expect(dialog).toBeVisible();
  const dialogBox = await dialog.boundingBox();
  expect(dialogBox).not.toBeNull();
  expect(dialogBox!.x).toBeGreaterThanOrEqual(0);
  expect(dialogBox!.x + dialogBox!.width).toBeLessThanOrEqual(390);

  const command = page.getByRole("textbox", { name: "Shell command" });
  await expect(command).toHaveCSS("font-size", "16px");
  const commandBox = await command.boundingBox();
  expect(commandBox).not.toBeNull();
  expect(commandBox!.height).toBeGreaterThanOrEqual(44);
});

test("clicking a nav button switches the active workspace", async ({ page }) => {
  await page.locator(".hecate-activitybar [aria-label^='Observability']").click();
  await expect(page.locator(".hecate-activitybar [aria-current='page']")).toHaveAttribute(
    "aria-label",
    /Observability/,
  );
});

test("workspace navigation keeps the current view visible while the next chunk loads", async ({
  page,
}) => {
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  const main = page.getByRole("main");
  await expect(page.getByText("Connect a model or agent")).toBeVisible();

  let releaseUsageChunk: (() => void) | null = null;
  const usageChunkRequested = new Promise<void>((resolve) => {
    void page.route("**/src/features/usage/UsageView.tsx*", async (route) => {
      resolve();
      await new Promise<void>((release) => {
        releaseUsageChunk = release;
      });
      await route.continue();
    });
  });

  await page.locator(".hecate-activitybar [aria-label^='Usage']").click();
  await usageChunkRequested;

  await expect(page.getByText("Connect a model or agent")).toBeVisible();
  await expect(page.getByText("Loading workspace…")).toHaveCount(0);

  releaseUsageChunk?.();
  await expect(main.getByText("Usage", { exact: true })).toBeVisible();
});

test("cold workspace loading fallback is centered in the content area", async ({ page }) => {
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.workspace", "usage");
  });

  let releaseUsageChunk: (() => void) | null = null;
  await page.route("**/src/features/usage/UsageView.tsx*", async (route) => {
    await new Promise<void>((release) => {
      releaseUsageChunk = release;
    });
    await route.continue();
  });

  await page.goto("/");

  const main = page.getByRole("main");
  const content = main.locator(".console-content");
  const fallback = main.getByRole("status");
  const label = fallback.getByText("Loading workspace…", { exact: true });
  await expect(label).toHaveText("Loading workspace…");
  await expect(page.getByText("Loading…", { exact: true })).toHaveCount(0);

  const contentBox = await content.boundingBox();
  const labelBox = await label.boundingBox();
  expect(contentBox).not.toBeNull();
  expect(labelBox).not.toBeNull();

  const contentCenterX = contentBox!.x + contentBox!.width / 2;
  const contentCenterY = contentBox!.y + contentBox!.height / 2;
  const labelCenterX = labelBox!.x + labelBox!.width / 2;
  const labelCenterY = labelBox!.y + labelBox!.height / 2;
  expect(Math.abs(labelCenterX - contentCenterX)).toBeLessThan(12);
  expect(Math.abs(labelCenterY - contentCenterY)).toBeLessThan(12);

  releaseUsageChunk?.();
  await expect(fallback).toHaveCount(0);
  await expect(main.getByText("Usage", { exact: true })).toBeVisible();
});

test("number keys do not switch workspaces while the app is focused", async ({ page }) => {
  await page.locator(".hecate-activitybar [aria-label^='Connections']").click();
  await expect(page.locator(".hecate-activitybar [aria-current='page']")).toHaveAttribute(
    "aria-label",
    /Connections/,
  );
  await page.keyboard.press("1");
  await expect(page.locator(".hecate-activitybar [aria-current='page']")).toHaveAttribute(
    "aria-label",
    /Connections/,
  );
});

test("Usage nav button activates the Usage workspace", async ({ page }) => {
  await page.locator(".hecate-activitybar [aria-label^='Usage']").click();
  await expect(page.locator(".hecate-activitybar [aria-current='page']")).toHaveAttribute(
    "aria-label",
    /Usage/,
  );
});

test("Settings nav button activates the Settings workspace", async ({ page }) => {
  await page.locator(".hecate-activitybar [aria-label^='Settings']").click();
  await expect(page.locator(".hecate-activitybar [aria-current='page']")).toHaveAttribute(
    "aria-label",
    /Settings/,
  );
});
