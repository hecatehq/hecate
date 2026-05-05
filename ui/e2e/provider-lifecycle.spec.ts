import { expect, test } from "./fixtures";

// End-to-end UI flow: Chats stays available in first-run mode, while the
// Providers workspace still owns provider create/delete state.
// Pure UI — relies on the stateful create/delete mocks in fixtures.ts.

test("adding and deleting a provider keeps chat available", async ({ page }) => {
  page.on("dialog", d => void d.accept());

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  // Default fixture starts empty, but Chats remains usable for model or
  // external-agent setup flows.
  await expect(page.locator("textarea")).toBeVisible();

  // Move to Providers via the activity bar shortcut and add Ollama.
  await page.keyboard.press("2");
  await page.waitForSelector("text=Providers");
  await page.getByRole("button", { name: /add provider/i }).first().click();
  const dlg = page.getByRole("dialog");
  await dlg.getByRole("button", { name: "Local", exact: true }).click();
  await dlg.getByText("Ollama", { exact: true }).click();
  await dlg.getByRole("button", { name: "Add provider", exact: true }).click();
  await expect(page.locator("tbody tr", { hasText: "Ollama" })).toBeVisible();

  // Switch to Chats — the conversation surface remains available.
  await page.keyboard.press("1");
  await expect(page.locator("textarea")).toBeVisible();

  // Back to Providers, delete the row.
  await page.keyboard.press("2");
  await page.waitForSelector("text=Providers");
  await page.getByTitle("Remove Ollama").click();
  await expect(page.getByRole("dialog", { name: "Remove provider?" })).toBeVisible();
  await page.getByRole("dialog", { name: "Remove provider?" }).getByRole("button", { name: "Remove provider", exact: true }).click();
  await expect(page.locator("tbody tr", { hasText: "Ollama" })).toHaveCount(0);

  // Chats remains available after deleting the only configured provider.
  await page.keyboard.press("1");
  await expect(page.locator("textarea")).toBeVisible();
});
