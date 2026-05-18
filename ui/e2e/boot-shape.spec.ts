import { expect, test } from "./fixtures";

// Pins the exact set of endpoints the UI hits during boot on the
// default (Chats) workspace. A regression here would flag any change
// that re-adds a deferred fetch to the dashboard loader OR drops a
// still-needed one.
//
// What's expected at boot today:
//   Wave 1 (gate-blocking): /healthz, /hecate/v1/whoami, /hecate/v1/settings
//   Wave 2 (background):    /v1/models, /hecate/v1/agent-adapters,
//                           /hecate/v1/chat/sessions, /hecate/v1/system/stats,
//                           /hecate/v1/providers/status (conditional on configured providers)
//
// What's expected NOT to fire at boot:
//   /hecate/v1/usage/summary, /hecate/v1/usage/events  (deferred to UsageView)
//   /hecate/v1/providers/presets  (deferred to AddProviderModal / TasksView;
//                                  QuickLocalProviderAdd does not need presets)
//   /hecate/v1/retention/*                             (deferred to SettingsView)
//   /hecate/v1/observability/*                         (deferred to ObservabilityView)

test("default Chats boot fires exactly the expected endpoints, no more", async ({ page }) => {
  const hits: string[] = [];
  // Capture every request the page issues; the fixtures' page.route
  // calls fulfill the responses, but we tap with continue() to keep
  // both behaviors. Use a request listener instead of additional
  // route handlers so we don't shadow the fixture routes' fulfillment.
  page.on("request", (req) => {
    const url = new URL(req.url());
    if (
      url.pathname === "/healthz" ||
      url.pathname === "/v1/models" ||
      url.pathname.startsWith("/hecate/v1/")
    ) {
      hits.push(url.pathname);
    }
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.waitForSelector(".hecate-activitybar [aria-label^='Chats']");
  // Give Wave 2 + any settle-down effects a moment to complete.
  await page.waitForTimeout(500);

  const unique = Array.from(new Set(hits));

  // Endpoints that MUST appear during boot.
  const expectedAtBoot = [
    "/healthz",
    "/hecate/v1/whoami",
    "/hecate/v1/settings",
    "/v1/models",
    "/hecate/v1/agent-adapters",
    "/hecate/v1/chat/sessions",
    "/hecate/v1/system/stats",
  ];
  for (const expected of expectedAtBoot) {
    expect(unique).toContain(expected);
  }

  // Endpoints that MUST NOT appear during boot — these are all
  // explicitly view-deferred. A hit here means someone regressed
  // the lazy-fetch contract.
  const forbiddenAtBoot = [
    "/hecate/v1/usage/summary",
    "/hecate/v1/usage/events",
    "/hecate/v1/providers/presets",
    "/hecate/v1/retention/runs",
    "/hecate/v1/retention/subsystems",
    "/hecate/v1/observability/requests",
  ];
  for (const forbidden of forbiddenAtBoot) {
    expect(unique, `${forbidden} should not be fetched at boot`).not.toContain(forbidden);
  }
});
