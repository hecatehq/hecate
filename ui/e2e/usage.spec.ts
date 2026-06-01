import { expect, test } from "./fixtures";

// Usage workspace. Cross-chat Hecate-controlled cloud-provider token
// accounting; active-chat adapter usage lives in ChatView instead.
test("shows the empty usage state when no cloud calls have been recorded", async ({ page }) => {
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.locator(".hecate-activitybar [aria-label^='Usage']").click();
  await expect(page.locator("text=No cloud usage recorded yet")).toBeVisible();
});

// The two specs below pin the lazy-fetch contract: Usage data is NOT
// pulled at boot; UsageView fetches both summary + events on mount
// and caches via a slice-level `loaded` flag so in-session navigation
// doesn't re-fetch. A full page reload resets the slice.
test.describe("Usage lazy-fetch", () => {
  test.beforeEach(async ({ page }) => {
    // Replace the default empty-list routes with counters. The default
    // routes registered in mockGatewayAPIs would have already
    // satisfied any boot-time fetch, so we re-register OURS on top
    // (Playwright matches routes in reverse registration order, so
    // most-recent wins) before navigating.
    let summaryCount = 0;
    let eventsCount = 0;
    await page.route("/hecate/v1/usage/summary*", async (route) => {
      summaryCount += 1;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          object: "usage_summary",
          data: {
            key: "global",
            scope: "global",
            backend: "memory",
            used_micros_usd: 0,
            used_usd: "$0.000000",
          },
        }),
      });
    });
    await page.route("/hecate/v1/usage/events*", async (route) => {
      eventsCount += 1;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ object: "usage_events", data: [] }),
      });
    });
    // Expose counters to the page-level tests via window-style refs.
    (
      page as unknown as { usageRouteCounts: { summary: () => number; events: () => number } }
    ).usageRouteCounts = { summary: () => summaryCount, events: () => eventsCount };
  });

  test("does not fetch /usage/{summary,events} during boot", async ({ page }) => {
    await page.goto("/");
    // Wait for the shell to be interactive — Connecting gate has
    // cleared, activity bar is mounted, default Chats workspace is
    // rendered.
    await page.waitForSelector(".hecate-activitybar");
    await page.waitForSelector(".hecate-activitybar [aria-label^='Chats']");

    const counts = (
      page as unknown as { usageRouteCounts: { summary: () => number; events: () => number } }
    ).usageRouteCounts;
    expect(counts.summary()).toBe(0);
    expect(counts.events()).toBe(0);
  });

  test("fetches /usage/{summary,events} on first UsageView mount, caches across re-visits", async ({
    page,
  }) => {
    await page.goto("/");
    await page.waitForSelector(".hecate-activitybar");

    const counts = (
      page as unknown as { usageRouteCounts: { summary: () => number; events: () => number } }
    ).usageRouteCounts;

    // First mount fires both fetches once each.
    await page.locator(".hecate-activitybar [aria-label^='Usage']").click();
    await expect(page.locator("text=No cloud usage recorded yet")).toBeVisible();
    expect(counts.summary()).toBe(1);
    expect(counts.events()).toBe(1);

    // Navigate to Chats and back; the slice's `loaded` flag should
    // keep us from re-fetching.
    await page.locator(".hecate-activitybar [aria-label^='Chats']").click();
    await page.locator(".hecate-activitybar [aria-label^='Usage']").click();
    await expect(page.locator("text=No cloud usage recorded yet")).toBeVisible();
    expect(counts.summary()).toBe(1);
    expect(counts.events()).toBe(1);
  });
});
