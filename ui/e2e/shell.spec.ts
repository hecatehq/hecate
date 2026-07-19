import { expect, test } from "./fixtures";

test.beforeEach(async ({ page }) => {
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
});

test("renders the activity bar with all workspace buttons", async ({ page }) => {
  const nav = page.locator(".hecate-activitybar");
  await expect(nav).toBeVisible();

  for (const label of ["Chats", "Connections", "Tasks", "Observability", "Usage", "Settings"]) {
    await expect(nav.locator(`[aria-label^="${label}"]`)).toBeVisible();
  }
});

test("shows the status bar with brand and session label", async ({ page }) => {
  const bar = page.locator(".hecate-statusbar");
  await expect(bar).toBeVisible();
  await expect(bar.locator(".hecate-statusbar__brand")).toHaveText("hecate");
  await expect(bar).toContainText("Local");
});

test("status bar shows configured provider count and model count", async ({ page }) => {
  const bar = page.locator(".hecate-statusbar");
  // Wait for dashboard data to load
  await expect(bar).toContainText("configured");
  await expect(bar).toContainText("models");
});

test("clicking a nav button switches the active workspace", async ({ page }) => {
  await page.locator(".hecate-activitybar [aria-label^='Observability']").click();
  await expect(page.locator(".hecate-activitybar [aria-current='page']")).toHaveAttribute(
    "aria-label",
    /Observability/,
  );
});

test("workspace navigation keeps the current view visible while the next chunk loads", async ({
  page,
}) => {
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await expect(page.getByText("Connect a model or agent")).toBeVisible();

  let releaseUsageChunk: (() => void) | null = null;
  const usageChunkRequested = new Promise<void>((resolve) => {
    void page.route("**/src/features/usage/UsageView.tsx*", async (route) => {
      resolve();
      await new Promise<void>((release) => {
        releaseUsageChunk = release;
      });
      await route.continue();
    });
  });

  await page.locator(".hecate-activitybar [aria-label^='Usage']").click();
  await usageChunkRequested;

  await expect(page.getByText("Connect a model or agent")).toBeVisible();
  await expect(page.getByText("Loading workspace…")).toHaveCount(0);

  releaseUsageChunk?.();
  await expect(page.getByText("Usage", { exact: true })).toBeVisible();
});

test("cold workspace loading fallback is centered in the content area", async ({ page }) => {
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.workspace", "usage");
  });

  let releaseUsageChunk: (() => void) | null = null;
  await page.route("**/src/features/usage/UsageView.tsx*", async (route) => {
    await new Promise<void>((release) => {
      releaseUsageChunk = release;
    });
    await route.continue();
  });

  await page.goto("/");

  const content = page.locator(".console-content");
  const fallback = page.locator(".workspace-fallback");
  const label = page.locator(".workspace-fallback__label");
  await expect(label).toHaveText("Loading workspace…");
  await expect(page.getByText("Loading…", { exact: true })).toHaveCount(0);

  const contentBox = await content.boundingBox();
  const labelBox = await label.boundingBox();
  expect(contentBox).not.toBeNull();
  expect(labelBox).not.toBeNull();

  const contentCenterX = contentBox!.x + contentBox!.width / 2;
  const contentCenterY = contentBox!.y + contentBox!.height / 2;
  const labelCenterX = labelBox!.x + labelBox!.width / 2;
  const labelCenterY = labelBox!.y + labelBox!.height / 2;
  expect(Math.abs(labelCenterX - contentCenterX)).toBeLessThan(12);
  expect(Math.abs(labelCenterY - contentCenterY)).toBeLessThan(12);

  releaseUsageChunk?.();
  await expect(fallback).toHaveCount(0);
  await expect(page.getByText("Usage", { exact: true })).toBeVisible();
});

test("number keys do not switch workspaces while the app is focused", async ({ page }) => {
  await page.locator(".hecate-activitybar [aria-label^='Connections']").click();
  await expect(page.locator(".hecate-activitybar [aria-current='page']")).toHaveAttribute(
    "aria-label",
    /Connections/,
  );
  await page.keyboard.press("1");
  await expect(page.locator(".hecate-activitybar [aria-current='page']")).toHaveAttribute(
    "aria-label",
    /Connections/,
  );
});

test("Usage nav button activates the Usage workspace", async ({ page }) => {
  await page.locator(".hecate-activitybar [aria-label^='Usage']").click();
  await expect(page.locator(".hecate-activitybar [aria-current='page']")).toHaveAttribute(
    "aria-label",
    /Usage/,
  );
});

test("Settings nav button activates the Settings workspace", async ({ page }) => {
  await page.locator(".hecate-activitybar [aria-label^='Settings']").click();
  await expect(page.locator(".hecate-activitybar [aria-current='page']")).toHaveAttribute(
    "aria-label",
    /Settings/,
  );
});
