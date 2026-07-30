import type { Page } from "@playwright/test";

import { expect, mockGatewayAPIs, MOCK_SETTINGS_CONFIG_WITH_PROVIDERS, test } from "./fixtures";

async function installMobileCompanionContext(page: Page) {
  await page.addInitScript(() => {
    Reflect.set(window, "__TAURI__", {});
    Object.defineProperty(navigator, "userAgent", {
      configurable: true,
      value: `${navigator.userAgent} HecateMobile`,
    });
    window.localStorage.setItem("hecate.workspace", "settings");
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.providerFilter", "openai");
    window.localStorage.setItem("hecate.model", "gpt-4o");
    window.localStorage.removeItem("hecate.project");
  });
}

test("runs and continues a rootless chat in the mobile companion console", async ({ page }) => {
  await page.unrouteAll({ behavior: "ignoreErrors" });
  const gateway = await mockGatewayAPIs(page, {
    settingsConfig: MOCK_SETTINGS_CONFIG_WITH_PROVIDERS,
  });
  let createRequest: Record<string, unknown> | null = null;
  await page.route("/hecate/v1/chat/sessions", async (route) => {
    if (route.request().method() === "POST") {
      createRequest = await route.request().postDataJSON();
    }
    await route.fallback();
  });
  await installMobileCompanionContext(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  const navigation = page.getByRole("navigation", { name: "Workspace navigation" });
  for (const label of ["Chats", "Projects", "Tasks"]) {
    await expect(navigation.getByRole("link", { name: label })).toBeVisible();
  }
  await expect(page.getByRole("link", { name: "Switch Hecate instance" })).toHaveAttribute(
    "href",
    "hecate-mobile://connections/",
  );

  await navigation.getByRole("button", { name: "More" }).click();
  const moreScreens = page.getByRole("dialog", { name: "More Hecate screens" });
  for (const label of ["Connections", "Observability", "Usage", "Settings"]) {
    await expect(moreScreens.getByRole("link", { name: label })).toBeVisible();
  }
  await page.keyboard.press("Escape");

  await page.getByRole("button", { name: "New Hecate chat", exact: true }).click();
  await expect(page.getByText(/Tools off/).first()).toBeVisible();
  await expect.poll(() => createRequest).not.toBeNull();
  expect(createRequest).not.toHaveProperty("workspace");

  const providerPicker = page.getByRole("button", { name: /provider picker/i });
  if (!(await providerPicker.textContent())?.includes("OpenAI")) {
    await providerPicker.click();
    await page
      .locator(".dropdown-menu")
      .first()
      .locator("[role='option']")
      .filter({ hasText: "OpenAI" })
      .first()
      .click();
  }
  const modelPicker = page.getByRole("button", { name: /model picker/i });
  if (!(await modelPicker.textContent())?.includes("gpt-4o")) {
    await modelPicker.click();
    await page
      .locator(".dropdown-menu")
      .first()
      .locator("[role='option']")
      .filter({ hasText: "gpt-4o" })
      .first()
      .click();
  }

  const composer = page.getByRole("textbox", { name: "Message" });
  await composer.fill("hello from iPhone");
  await page.getByRole("button", { name: "Send message" }).click();

  await expect(page.getByText("Direct response to: hello from iPhone")).toBeVisible();
  await expect.poll(() => gateway.chatMessagePayloads.length).toBe(1);
  expect(gateway.chatMessagePayloads[0]).toMatchObject({
    content: "hello from iPhone",
    model: "gpt-4o",
    provider: "openai",
    tools_enabled: false,
  });

  const selectedSession = await page.evaluate(() =>
    window.localStorage.getItem("hecate.chatSessionID"),
  );
  expect(selectedSession).toMatch(/^chat-e2e-/);
  await expect(page).toHaveURL(new RegExp(`chat=${selectedSession}`));

  await page.reload();
  await page.waitForSelector(".hecate-activitybar");
  await page.getByRole("link", { name: /Chat Hecate chat, Hecate/ }).click();
  await expect(page.getByText("hello from iPhone", { exact: true })).toBeVisible();
  await expect(page.getByText("Direct response to: hello from iPhone")).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => window.localStorage.getItem("hecate.chatSessionID")))
    .toBe(selectedSession);
});
