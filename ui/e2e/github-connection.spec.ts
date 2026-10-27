import { expect, test } from "./fixtures";

test("GitHub inspection is explicit and missing checks are not successful", async ({ page }) => {
  let inspections = 0;
  await page.route("/hecate/v1/settings/github", (route) =>
    route.fulfill({
      json: {
        object: "github_connection",
        data: { configured: true, mode: "read_only", repositories: ["owner/repo"], setup_hint: "" },
      },
    }),
  );
  await page.route("/hecate/v1/github/pull-request/inspect", async (route) => {
    inspections += 1;
    expect(route.request().postDataJSON()).toEqual({ url: "https://github.com/owner/repo/pull/7" });
    await route.fulfill({
      json: {
        object: "github_pull_request",
        data: {
          url: "https://github.com/owner/repo/pull/7",
          repository: "owner/repo",
          number: 7,
          head_sha: "a".repeat(40),
          base_sha: "b".repeat(40),
          state: "open",
          draft: false,
          merged: false,
          complete: true,
          checks_state: "not_reported",
          checks: [],
          observed_at: "2026-10-27T12:00:00Z",
        },
      },
    });
  });
  await page.goto("/");
  await page.locator(".hecate-activitybar [aria-label^='Connections']").click();
  const connection = page.getByRole("region", { name: "GitHub", exact: true });
  await expect(connection.getByLabel("Pull request URL")).toBeVisible();
  expect(inspections).toBe(0);
  await connection.getByLabel("Pull request URL").fill("https://github.com/owner/repo/pull/7");
  await connection.getByRole("button", { name: "Inspect pull request" }).click();
  await expect(connection.getByRole("status")).toHaveText("No checks have been reported");
  expect(inspections).toBe(1);
  await page.setViewportSize({ width: 375, height: 900 });
  expect(await connection.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(
    true,
  );
});
