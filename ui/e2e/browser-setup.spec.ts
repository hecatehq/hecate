import { expect, test } from "./fixtures";
import type { BrowserSettingsData } from "../src/types/browser";

test("selects and enables a browser explicitly, updates policy readiness, and disables it", async ({
  page,
}) => {
  const candidate = {
    id: "candidate-chromium",
    name: "Chromium",
    path: "/Applications/Chromium Browser.app/Contents/MacOS/Chromium Browser",
  };
  let writes = 0;
  let data: BrowserSettingsData = {
    readiness: { available: false, status: "not_configured", message: "No browser selected." },
    source: "none",
    candidates: [candidate],
    backend: "sqlite",
  };
  await page.route("/hecate/v1/settings/browser", async (route) => {
    if (route.request().method() === "PUT") {
      expect(route.request().postDataJSON()).toEqual({ candidate_id: candidate.id });
      writes += 1;
      data = {
        ...data,
        source: "settings",
        selected: candidate,
        readiness: {
          available: true,
          status: "configured",
          message: "Browser selected; not yet used.",
        },
      };
    } else if (route.request().method() === "DELETE") {
      writes += 1;
      data = {
        ...data,
        source: "none",
        selected: undefined,
        readiness: { available: false, status: "not_configured", message: "No browser selected." },
      };
    }
    await route.fulfill({ json: { object: "browser_settings", data } });
  });
  await page.goto("/");
  await page.locator(".hecate-activitybar [aria-label^='Settings']").click();
  const setup = page.getByRole("region", { name: "Browser setup" });
  await expect(setup.getByText("Not configured", { exact: true })).toBeVisible();
  await expect(setup.getByRole("button", { name: "Enable browser" })).toBeDisabled();
  expect(writes).toBe(0);
  await setup.getByRole("combobox", { name: "Installed browser" }).selectOption(candidate.id);
  expect(writes).toBe(0);
  await setup.getByRole("button", { name: "Enable browser" }).click();
  await expect(setup.getByText("Configured (unverified)", { exact: true })).toBeVisible();
  await expect(setup.getByRole("button", { name: "Enabled", exact: true })).toBeDisabled();
  await page.setViewportSize({ width: 375, height: 900 });
  await expect(setup.getByRole("combobox", { name: "Installed browser" })).toBeVisible();
  expect(await setup.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.getByRole("button", { name: "Manage policies" }).click();
  await expect(
    page
      .getByRole("dialog", { name: "Work policies" })
      .getByText(/Browser runtime configured \(unverified\)/),
  ).toBeVisible();
  await page.getByRole("dialog").getByRole("button", { name: "Close", exact: true }).click();
  await setup.getByRole("button", { name: "Refresh discovery" }).click();
  await expect(setup.getByRole("button", { name: "Refresh discovery" })).toBeEnabled();
  expect(writes).toBe(1);
  await setup.getByRole("button", { name: "Disable browser" }).click();
  await expect(setup.getByText("Not configured", { exact: true })).toBeVisible();
  expect(writes).toBe(2);
});

test("remote settings never call browser discovery", async ({ page }) => {
  let discoveryReads = 0;
  await page.route("/hecate/v1/settings/browser", async (route) => {
    discoveryReads += 1;
    await route.fulfill({ status: 403 });
  });
  await page.route("/hecate/v1/whoami", (route) =>
    route.fulfill({
      json: {
        object: "session",
        data: {
          role: "operator",
          remote_identity: {
            actor_id: "operator",
            org_id: "org",
            project_id: "project",
            runtime_id: "remote",
          },
          runtime_host: {
            id: "remote",
            label: "Remote",
            runtime_mode: "remote_runtime",
            operator_access: "remote_supervision",
            local_only_actions_available: false,
          },
        },
      },
    }),
  );
  await page.goto("/");
  await page.locator(".hecate-activitybar [aria-label^='Settings']").click();
  const setup = page.getByRole("region", { name: "Browser setup" });
  await expect(setup.getByText("Local only", { exact: true })).toBeVisible();
  await expect(setup.getByRole("button")).toHaveCount(0);
  expect(discoveryReads).toBe(0);
});
