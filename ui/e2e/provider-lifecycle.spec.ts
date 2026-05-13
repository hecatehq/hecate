import { expect, test } from "./fixtures";

// End-to-end UI flow: Chats stays available in first-run mode, while the
// Connections workspace owns provider create/delete state.
// Pure UI — relies on the stateful create/delete mocks in fixtures.ts.

test("adding and deleting a provider keeps chat available", async ({ page }) => {
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  // Default fixture starts empty. Chats should stay useful by showing the
  // provider onboarding surface instead of a disabled composer.
  await expect(page.getByText("Nothing runnable yet")).toBeVisible();
  await expect(page.getByRole("button", { name: /Go to Connections/i })).toBeVisible();
  await expect(page.getByText("Detected locally")).toBeVisible();
  await expect(page.locator("textarea")).toHaveCount(0);

  // Move to Connections and add Ollama.
  await page.locator(".hecate-activitybar [aria-label^='Connections']").click();
  await page.waitForSelector("text=Connections");
  await page.getByRole("button", { name: /add provider/i }).first().click();
  const dlg = page.getByRole("dialog");
  await dlg.getByRole("button", { name: "Local", exact: true }).click();
  await dlg.getByText("Ollama", { exact: true }).click();
  await dlg.getByRole("button", { name: "Add provider", exact: true }).click();
  await expect(page.locator("tbody tr", { hasText: "Ollama" })).toBeVisible();

  // Switch to Chats — the provider-specific troubleshooting surface replaces
  // the first-run provider discovery once configuration exists but no models
  // are routable yet.
  await page.locator(".hecate-activitybar [aria-label^='Chats']").click();
  await expect(page.getByText("Provider is configured")).toBeVisible();
  await expect(page.getByText("none discovered")).toBeVisible();
  await expect(page.locator("textarea")).toHaveCount(0);

  // Back to Connections, delete the row.
  await page.locator(".hecate-activitybar [aria-label^='Connections']").click();
  await page.waitForSelector("text=Connections");
  await page.getByTitle("Remove Ollama").click();
  await expect(page.getByRole("dialog", { name: "Remove provider?" })).toBeVisible();
  await page.getByRole("dialog", { name: "Remove provider?" }).getByRole("button", { name: "Remove provider", exact: true }).click();
  await expect(page.locator("tbody tr", { hasText: "Ollama" })).toHaveCount(0);

  // Chats remains available after deleting the only configured provider by
  // returning to the same first-run setup surface.
  await page.locator(".hecate-activitybar [aria-label^='Chats']").click();
  await expect(page.getByText("Nothing runnable yet")).toBeVisible();
  await expect(page.getByRole("button", { name: /Go to Connections/i })).toBeVisible();
  await expect(page.locator("textarea")).toHaveCount(0);
});
