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

test("keeps cached Cloud instances visible until native retry guidance clears", async ({
  page,
}) => {
  await page.addInitScript(() => {
    const availableStatus = {
      available: true,
      restoring: false,
      phase: "connected",
      running: true,
      authorizing: false,
      signed_in: true,
      gateway_ready: true,
      auto_start_enabled: true,
      account_email: "operator@example.com",
      cloud_url: "https://console.hecatehq.com",
      base_url: "http://127.0.0.1:8765",
      message: "Remote access is on.",
      last_error: null,
      retry_after_seconds: null,
    };
    const outageMessage =
      "Hecate Cloud is temporarily unavailable. Automatic checks will resume after the waiting period.";
    const state = {
      calls: [] as string[],
      connectionReads: 0,
      cooldown: false,
      recover: false,
      statusReads: 0,
    };
    const connections = [
      {
        id: "runtime-production",
        kind: "hosted_runtime",
        org_id: "org-1",
        project_id: "project-1",
        name: "Production",
        status: "online",
        reachable: true,
        can_start: false,
        remote_enabled: false,
        version: "0.8.0",
        capabilities: [],
        last_seen_at: "2026-09-29T12:00:00Z",
      },
    ];
    let callbackID = 0;
    const callbacks = new Map<number, (...args: unknown[]) => unknown>();

    Object.defineProperty(window, "__desktopCloudRetryTest", {
      configurable: false,
      value: state,
    });
    Object.defineProperty(window, "__TAURI_EVENT_PLUGIN_INTERNALS__", {
      configurable: false,
      value: { unregisterListener: () => undefined },
    });
    Object.defineProperty(window, "__TAURI_INTERNALS__", {
      configurable: false,
      value: {
        invoke: async (command: string) => {
          state.calls.push(command);
          if (command === "cloud_connection_status") {
            state.statusReads += 1;
            if (state.cooldown && !state.recover) {
              return {
                ...availableStatus,
                message: outageMessage,
                last_error: "Hecate Cloud is temporarily unavailable.",
                retry_after_seconds: 60,
              };
            }
            return structuredClone(availableStatus);
          }
          if (command === "cloud_runtime_connections") {
            state.connectionReads += 1;
            if (state.connectionReads === 1) state.cooldown = true;
            return structuredClone(connections);
          }
          if (command === "plugin:updater|check") return null;
          if (command === "plugin:event|listen") return 1;
          if (command === "take_pending_desktop_update_check") return false;
          if (command === "set_update_badge" || command === "plugin:event|unlisten") return null;
          throw new Error(`Unexpected native command: ${command}`);
        },
        transformCallback: (callback: (...args: unknown[]) => unknown) => {
          callbackID += 1;
          callbacks.set(callbackID, callback);
          return callbackID;
        },
        unregisterCallback: (id: number) => callbacks.delete(id),
      },
    });
  });

  await page.reload();
  await page.waitForSelector(".hecate-activitybar");
  await page.locator(".hecate-activitybar [aria-label^='Settings']").click();

  const account = page.getByTestId("desktop-cloud-connection");
  const runtimes = page.getByTestId("desktop-cloud-runtimes");
  const outageMessage =
    "Hecate Cloud is temporarily unavailable. Automatic checks will resume after the waiting period.";
  await expect(account.getByText("operator@example.com")).toBeVisible();
  await expect(account.getByText(outageMessage)).toBeVisible();
  await expect(account.getByRole("button", { name: "Sign out" })).toBeEnabled();
  await expect(runtimes.getByRole("button", { name: "Open Production" })).toBeVisible();
  const refresh = runtimes.getByRole("button", { name: "Refresh" });
  await expect(refresh).toBeDisabled();

  const pausedConnectionReads = await page.evaluate(() => {
    const testWindow = window as typeof window & {
      __desktopCloudRetryTest: { connectionReads: number };
    };
    return testWindow.__desktopCloudRetryTest.connectionReads;
  });
  await page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
  await refresh.evaluate((button: HTMLButtonElement) => button.click());
  await page.waitForTimeout(100);
  await expect
    .poll(() =>
      page.evaluate(() => {
        const testWindow = window as typeof window & {
          __desktopCloudRetryTest: { connectionReads: number };
        };
        return testWindow.__desktopCloudRetryTest.connectionReads;
      }),
    )
    .toBe(pausedConnectionReads);

  await page.evaluate(() => {
    const testWindow = window as typeof window & {
      __desktopCloudRetryTest: { recover: boolean };
    };
    testWindow.__desktopCloudRetryTest.recover = true;
  });
  await expect
    .poll(() =>
      page.evaluate(() => {
        const testWindow = window as typeof window & {
          __desktopCloudRetryTest: { connectionReads: number };
        };
        return testWindow.__desktopCloudRetryTest.connectionReads;
      }),
    )
    .toBe(pausedConnectionReads + 1);
  await expect(account.getByText(outageMessage)).toHaveCount(0);
  await expect(runtimes.getByRole("button", { name: "Open Production" })).toBeVisible();
  await expect(refresh).toBeEnabled();
});
