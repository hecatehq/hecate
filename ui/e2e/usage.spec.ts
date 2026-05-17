import { expect, test } from "./fixtures";

// Usage workspace. Cross-chat Hecate-controlled cloud-provider token
// accounting; active-chat adapter usage lives in ChatView instead.
test.beforeEach(async ({ page }) => {
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.locator(".hecate-activitybar [aria-label^='Usage']").click();
});

test("shows the empty usage state when no cloud calls have been recorded", async ({ page }) => {
  await expect(page.locator("text=No cloud usage recorded yet")).toBeVisible();
});
