import { expect, test } from "./fixtures";

test("renders remote Settings as controlled-instance context without host-local panels", async ({
  page,
}) => {
  await page.route("/hecate/v1/whoami", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        object: "session",
        data: {
          role: "operator",
          runtime_host: {
            id: "runtime_dogfood",
            label: "Dogfood Runtime",
            runtime_mode: "remote_runtime",
            operator_access: "remote_supervision",
            local_only_actions_available: false,
          },
          remote_identity: {
            actor_id: "operator_1",
            org_id: "org_1",
            project_id: "project_1",
            runtime_id: "runtime_dogfood",
          },
        },
      }),
    }),
  );

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.locator(".hecate-activitybar [aria-label^='Settings']").click();

  await expect(page.getByTestId("remote-runtime-settings")).toBeVisible();
  await expect(page.getByText("Controlled instance")).toBeVisible();
  await expect(page.getByText(/This window supervises Dogfood Runtime/i)).toBeVisible();
  await expect(
    page.getByText(/Host-local controls, including Hecate Cloud connection/i),
  ).toBeVisible();
  await expect(page.getByText(/Clean up old runtime data on Dogfood Runtime/i)).toBeVisible();
  await expect(page.getByText("Plugins", { exact: true })).toHaveCount(0);
  await expect(page.getByText("Hecate Cloud", { exact: true })).toHaveCount(0);
});
