import { expect, test } from "./fixtures";

// Pins the lazy-fetch contract for /hecate/v1/providers/presets:
// the dashboard loader no longer pulls presets at boot; only the
// AddProviderModal mount (and TasksView mount) trigger the fetch.
// A regression here would put presets back in Wave 2 and waste a
// network round-trip on every cold boot.

test.describe("Provider presets lazy-fetch", () => {
  test("does not fetch /providers/presets during boot on the default Chats workspace", async ({ page }) => {
    let count = 0;
    await page.route("/hecate/v1/providers/presets*", async route => {
      count += 1;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "list", data: [] }),
      });
    });

    await page.goto("/");
    await page.waitForSelector(".hecate-activitybar");
    // Default workspace is Chats; assert we landed there.
    await page.waitForSelector(".hecate-activitybar [aria-label^='Chats']");

    // Give Wave 2 time to settle so any stale boot-time call would
    // have fired by now.
    await page.waitForTimeout(300);
    expect(count).toBe(0);
  });

  test("fetches /providers/presets when AddProviderModal opens, caches across re-opens", async ({ page }) => {
    let count = 0;
    await page.route("/hecate/v1/providers/presets*", async route => {
      count += 1;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          object: "list",
          data: [
            { id: "anthropic", name: "Anthropic", kind: "cloud", protocol: "anthropic", base_url: "https://api.anthropic.com/v1" },
            { id: "openai", name: "OpenAI", kind: "cloud", protocol: "openai", base_url: "https://api.openai.com/v1" },
            { id: "ollama", name: "Ollama", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:11434/v1" },
          ],
        }),
      });
    });

    await page.goto("/");
    await page.waitForSelector(".hecate-activitybar");
    expect(count).toBe(0);

    // Navigate to Connections, then click "Add provider" to mount
    // the modal. First mount fires the fetch.
    await page.locator(".hecate-activitybar [aria-label^='Connections']").click();
    await page.locator("button", { hasText: "Add provider" }).first().click();
    // The modal's "Local" tab is selected by default; wait for it.
    await expect(page.locator("[role=dialog]").locator("text=Local").first()).toBeVisible({ timeout: 5_000 });
    await expect.poll(() => count, { timeout: 5_000 }).toBe(1);

    // Close the modal, reopen — the slice's providerPresetsLoaded
    // flag should keep us from re-fetching.
    await page.keyboard.press("Escape");
    await page.locator("button", { hasText: "Add provider" }).first().click();
    await expect(page.locator("[role=dialog]").locator("text=Local").first()).toBeVisible({ timeout: 5_000 });
    // Give the hook a tick to (not) re-fetch.
    await page.waitForTimeout(200);
    expect(count).toBe(1);
  });
});
