import { expect, test } from "./fixtures";

// Settings workspace. Connections owns provider/model setup; Usage owns
// cloud-token accounting. Settings is intentionally scoped to maintenance.
test.beforeEach(async ({ page }) => {
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.locator(".hecate-activitybar [aria-label^='Settings']").click();
  await page.waitForSelector("text=Retention");
});

test("renders Settings as retention-only", async ({ page }) => {
  await expect(page.getByRole("button", { name: "Retention" })).toBeVisible();
  // Removed or relocated tabs: readiness lives in Connections, usage lives
  // in the Usage workspace, and pricing/budgeting is no longer configured.
  for (const removed of ["Pricing", "Model capabilities", "Policy", "MCP Cache", "Tenants", "Keys", "Balances", "Clients"]) {
    await expect(page.getByRole("button", { name: removed })).toHaveCount(0);
  }
});

test("Settings nav button uses the 'Settings' label, not 'Admin'", async ({ page }) => {
  await expect(
    page.locator(".hecate-activitybar [aria-label^='Settings']"),
  ).toBeVisible();
  await expect(
    page.locator(".hecate-activitybar [aria-label^='Admin ']"),
  ).toHaveCount(0);
});

test("retention tab shows known subsystem chips", async ({ page }) => {
  await page.getByRole("button", { name: "Retention" }).click();
  for (const sub of ["trace_snapshots", "usage_events", "audit_events"]) {
    await expect(page.locator(`text=${sub}`).first()).toBeVisible();
  }
});

test("retention 'Run now' fires POST request", async ({ page }) => {
  let posted = false;
  await page.route("/hecate/v1/system/retention/run*", async route => {
    if (route.request().method() === "POST") {
      posted = true;
      await route.fulfill({ status: 200, contentType: "application/json", body: '{"object":"retention_run","data":{}}' });
    } else {
      await route.continue();
    }
  });

  await page.getByRole("button", { name: "Retention" }).click();
  await page.getByRole("button", { name: /Run now/i }).click();
  await expect.poll(() => posted).toBe(true);
});

test("Usage workspace shows the empty usage state", async ({ page }) => {
  await page.locator(".hecate-activitybar [aria-label^='Usage']").click();
  await expect(page.locator("text=No cloud usage recorded yet")).toBeVisible();
});
