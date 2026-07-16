import { expect, test } from "./fixtures";

// Settings workspace. Connections owns provider/model setup; Usage owns
// cloud-token accounting. Settings is intentionally scoped to maintenance.
test.beforeEach(async ({ page }) => {
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.locator(".hecate-activitybar [aria-label^='Settings']").click();
  await page.waitForSelector("text=Maintenance");
});

test("renders Settings as local maintenance", async ({ page }) => {
  await expect(page.getByText("Maintenance")).toBeVisible();
  await expect(page.getByText("Run cleanup")).toBeVisible();
  await expect(page.getByRole("button", { name: "Retention" })).toHaveCount(0);
  // Removed or relocated tabs: readiness lives in Connections, usage lives
  // in the Usage workspace, and pricing/budgeting is no longer configured.
  for (const removed of [
    "Pricing",
    "Model capabilities",
    "Policy",
    "MCP Cache",
    "Tenants",
    "Keys",
    "Balances",
    "Clients",
  ]) {
    await expect(page.getByRole("button", { name: removed })).toHaveCount(0);
  }
});

test("Settings nav button uses the 'Settings' label, not 'Admin'", async ({ page }) => {
  await expect(page.locator(".hecate-activitybar [aria-label^='Settings']")).toBeVisible();
  await expect(page.locator(".hecate-activitybar [aria-label^='Admin ']")).toHaveCount(0);
});

test("maintenance view shows known cleanup targets", async ({ page }) => {
  for (const sub of ["Trace snapshots", "Usage events", "Audit events"]) {
    await expect(page.getByText(sub).first()).toBeVisible();
  }
});

test("maintenance 'Clean up now' fires POST request", async ({ page }) => {
  let posted = false;
  await page.route("/hecate/v1/system/retention/run*", async (route) => {
    if (route.request().method() === "POST") {
      posted = true;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: '{"object":"retention_run","data":{}}',
      });
    } else {
      await route.continue();
    }
  });

  await page.getByRole("button", { name: /Clean up now/i }).click();
  await expect.poll(() => posted).toBe(true);
});

test("memory backend disables in-process reset", async ({ page }) => {
  await expect(page.getByText("Reset runtime state unavailable")).toBeVisible();
  await expect(
    page.getByText(/Restart the runtime to clear Hecate-owned in-memory state/i),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Unavailable" })).toBeDisabled();
});

test("sqlite backend disables in-process reset", async ({ page }) => {
  await page.route("/hecate/v1/settings*", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "settings",
        data: { backend: "sqlite", providers: [], policy_rules: [], events: [] },
      }),
    });
  });
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.locator(".hecate-activitybar [aria-label^='Settings']").click();
  await expect(page.getByText("Reset local data unavailable")).toBeVisible();
  await expect(
    page.getByText(/Stop the runtime before removing its configured data directory/i),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Unavailable" })).toBeDisabled();
});
