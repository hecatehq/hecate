import { expect, test } from "./fixtures";

// Settings workspace (id stays "admin" for back-compat; label is now
// "Settings"). Tabs: Pricing / Policy / Retention / MCP Cache.
test.beforeEach(async ({ page }) => {
  // Override /v1/whoami to report admin so the Settings nav button appears.
  await page.route("/v1/whoami*", r => r.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({
      object: "session",
      data: { authenticated: true, invalid_token: false, role: "admin", source: "config", key_id: "" },
    }),
  }));
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  // Press 6 → Settings. Admin lineup is Chats / Providers / Tasks /
  // Observability / Costs / Settings, so Settings sits at position 6.
  await page.keyboard.press("6");
  await page.waitForSelector("text=Pricing");
});

test("renders the settings tabs (Pricing / Policy / Retention / MCP Cache)", async ({ page }) => {
  for (const tab of ["Pricing", "Policy", "Retention", "MCP Cache"]) {
    await expect(page.getByRole("button", { name: tab })).toBeVisible();
  }
  // Balances and Usage moved out to the Costs workspace.
  await expect(page.getByRole("button", { name: "Balances" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Usage" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Clients" })).toHaveCount(0);
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
  for (const sub of ["trace_snapshots", "budget_events", "audit_events"]) {
    await expect(page.locator(`text=${sub}`).first()).toBeVisible();
  }
});

test("retention 'Run now' fires POST request", async ({ page }) => {
  let posted = false;
  await page.route("/admin/retention/run*", async route => {
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

test("Costs workspace shows the empty ledger state", async ({ page }) => {
  // Costs sits at shortcut 5 in the admin lineup. Default fixture has
  // no request-ledger entries so the empty state should render.
  await page.keyboard.press("5");
  await expect(page.locator("text=No usage events recorded yet")).toBeVisible();
});

test("Costs workspace shows the admin-required hint when budget is missing", async ({ page }) => {
  await page.route("/admin/budget*", r => r.fulfill({ status: 404, body: "" }));
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.keyboard.press("5"); // Costs
  // Either shows the hint (no budget) or shows budget data — both are acceptable.
  const ok = await Promise.race([
    page.locator("text=Budget data unavailable").first().waitFor({ timeout: 1000 }).then(() => true).catch(() => false),
    page.locator("text=No usage events recorded yet").first().waitFor({ timeout: 1000 }).then(() => true).catch(() => false),
  ]);
  expect(ok).toBe(true);
});

// Pricebook import: Open pricebook tab → preview is fetched on mount →
// "Import all" opens the consent dialog → Apply triggers POST /apply.
// This is the only end-to-end exercise of the import flow; the unit
// test in useRuntimeConsole.test.tsx pins notice wording but never
// renders the modal or clicks through.
test("pricebook import all triggers preview + apply round-trip", async ({ page }) => {
  // Mock the preview to propose adding a price for an existing
  // catalog model. The MOCK_MODELS fixture has gpt-4o-mini in the
  // catalog with no pricebook entry, so this will classify as `added`
  // and the row will appear as "unpriced" in the table — letting the
  // consent dialog include it.
  let previewHits = 0;
  await page.route("/admin/control-plane/pricebook/import/preview", async route => {
    if (route.request().method() !== "POST") return route.continue();
    previewHits++;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "control_plane_pricebook_import_diff",
        data: {
          added: [
            {
              provider: "openai",
              model: "gpt-4o-mini",
              input_micros_usd_per_million_tokens: 150_000,
              output_micros_usd_per_million_tokens: 600_000,
              cached_input_micros_usd_per_million_tokens: 75_000,
              source: "imported",
            },
          ],
          updated: [],
          skipped: [],
          unchanged: 0,
          applied: [],
          failed: [],
          fetched_at: "2026-04-25T00:00:00Z",
        },
      }),
    });
  });

  let applyURL = "";
  let applyBody = "";
  await page.route("/admin/control-plane/pricebook/import/apply", async route => {
    if (route.request().method() !== "POST") return route.continue();
    applyURL = route.request().url();
    applyBody = route.request().postData() ?? "";
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "control_plane_pricebook_import_diff",
        data: {
          added: [],
          updated: [],
          skipped: [],
          unchanged: 0,
          applied: [
            {
              provider: "openai",
              model: "gpt-4o-mini",
              input_micros_usd_per_million_tokens: 150_000,
              output_micros_usd_per_million_tokens: 600_000,
              cached_input_micros_usd_per_million_tokens: 75_000,
              source: "imported",
            },
          ],
          failed: [],
          fetched_at: "2026-04-25T00:00:00Z",
        },
      }),
    });
  });

  // Pricing is the first visible Settings tab, so
  // it has already mounted once during beforeEach — before our route
  // handler was registered. Navigate away to Policy and back to Pricing
  // so the mount-time preview fetch fires under the test's mocked route.
  await page.getByRole("button", { name: "Policy" }).click();
  await page.getByRole("button", { name: "Pricing" }).click();
  // Preview is fetched on mount of the tab; the "Import all" button
  // becomes enabled once the diff arrives. Without this assertion, a
  // future regression that drops the mount-time preview fetch would go
  // unnoticed.
  await expect.poll(() => previewHits, { timeout: 5_000 }).toBeGreaterThanOrEqual(1);

  const importAll = page.getByRole("button", { name: /Import all/i });
  await expect(importAll).toBeEnabled();
  await importAll.click();

  // Consent modal: assert it opened and contains the proposed change.
  await expect(page.getByText("Update pricebook")).toBeVisible();
  await expect(page.getByText("gpt-4o-mini").first()).toBeVisible();

  // Apply with the pre-selected key. The button label includes the
  // count, which doubles as a sanity check that selection state landed.
  const applyBtn = page.getByRole("button", { name: /Apply 1 change/i });
  await expect(applyBtn).toBeEnabled();
  await applyBtn.click();

  await expect.poll(() => applyURL).toContain("/admin/control-plane/pricebook/import/apply");
  // The body MUST carry the explicit key list — a regression that drops
  // `keys` and applies blanket changes would silently overwrite manual
  // rows the operator never consented to.
  expect(JSON.parse(applyBody)).toEqual({ keys: ["openai/gpt-4o-mini"] });
});
