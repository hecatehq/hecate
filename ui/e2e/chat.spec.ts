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

test("renders the message textarea and send button", async ({ page }) => {
  await expect(page.locator("textarea")).toBeVisible();
  await expect(page.locator("button[type='submit']")).toBeVisible();
});

test("send button is disabled when message is empty", async ({ page }) => {
  await page.locator("textarea").fill("");
  await expect(page.locator("button[type='submit']")).toBeDisabled();
});

test("send button becomes enabled when message has content", async ({ page }) => {
  await page.locator("textarea").fill("Hello");
  await expect(page.locator("button[type='submit']")).toBeEnabled();
});

test("model picker opens and lists models from mock data", async ({ page }) => {
  // Wait for models to load, then open the picker
  const modelBtn = page.getByRole("button", { name: /model picker/i });
  await modelBtn.click();

  for (const m of MOCK_MODELS) {
    await expect(page.locator(`.dropdown-menu`)).toContainText(m.id);
  }
});

test("model picker filters by search input", async ({ page }) => {
  const modelBtn = page.getByRole("button", { name: /model picker/i });
  await modelBtn.click();

  const menu = page.locator(".dropdown-menu");
  await menu.locator("input").fill("gpt");

  await expect(menu).toContainText("gpt-4o");
  await expect(menu).not.toContainText("claude");
});

test("selecting a model closes the picker and updates the button label", async ({ page }) => {
  const modelBtn = page.getByRole("button", { name: /model picker/i });
  await modelBtn.click();

  await page.locator(".dropdown-menu").locator("text=gpt-4o-mini").first().click();

  await expect(page.locator(".dropdown-menu")).not.toBeVisible();
  await expect(modelBtn).toContainText("gpt-4o-mini");
});

test("provider picker shows healthy providers", async ({ page }) => {
  const healthyProviders = MOCK_PROVIDERS.filter(p => p.healthy);
  const providerBtn = page.locator("button", { hasText: /all providers/i });
  await providerBtn.click();

  const menu = page.locator(".dropdown-menu").first();
  for (const p of healthyProviders) {
    await expect(menu).toContainText(p.name, { ignoreCase: true });
  }
});

test("New chat button clears the active conversation", async ({ page }) => {
  // Fill the message box so we can verify the state resets
  await page.locator("textarea").fill("some prior message");
  await page.getByRole("button", { name: /new chat/i }).click();
  // After starting a new chat, the empty state stays visible and
  // composer state is cleared.
  await expect(page.getByText("Send a message to start this chat.")).toBeVisible();
  await expect(page.locator("textarea")).toHaveValue("");
});

test("system prompt editor opens and closes", async ({ page }) => {
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
  await page.route("/v1/chat/sessions", r =>
    r.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "chat_session",
        data: {
          id: "chat_err_e2e",
          title: "x",
          turns: [],
          created_at: "2026-04-21T00:00:00Z",
          updated_at: "2026-04-21T00:00:00Z",
        },
      }),
    }),
  );
  await page.route("/v1/chat/completions", r =>
    r.fulfill({
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
