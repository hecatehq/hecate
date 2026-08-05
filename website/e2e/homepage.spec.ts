import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

const latestRelease = "https://github.com/hecatehq/hecate/releases/latest";

test.beforeEach(async ({ page }) => {
  await page.goto("/");
});

test("publishes complete identity and sharing metadata", async ({ page, request }) => {
  await expect(page).toHaveTitle("Hecate — Agent Runtime and Operator Console");

  const description = page.locator('meta[name="description"]');
  await expect(description).toHaveCount(1);
  await expect(description).toHaveAttribute("content", /agent runtime and operator console/i);

  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute("href", "https://hecate.sh/");
  await expect(page.locator('meta[name="theme-color"]')).toHaveAttribute("content", "#070b0c");
  await expect(page.locator('meta[property="og:title"]')).toHaveAttribute(
    "content",
    /Run agents\. Keep control\./,
  );
  await expect(page.locator('meta[property="og:image"]')).toHaveAttribute(
    "content",
    "https://hecate.sh/product/chat-workspace-diff.png",
  );
  await expect(page.locator('meta[name="twitter:card"]')).toHaveAttribute(
    "content",
    "summary_large_image",
  );

  await expect(page.locator('script:not([type="application/ld+json"])')).toHaveCount(0);

  const [robots, sitemap, socialImage] = await Promise.all([
    request.get("/robots.txt"),
    request.get("/sitemap.xml"),
    request.get("/product/chat-workspace-diff.png"),
  ]);
  expect(robots.ok()).toBeTruthy();
  expect(await robots.text()).toContain("Sitemap: https://hecate.sh/sitemap.xml");
  expect(sitemap.ok()).toBeTruthy();
  expect(await sitemap.text()).toContain("<loc>https://hecate.sh/</loc>");
  expect(socialImage.ok()).toBeTruthy();
});

test("keeps navigation semantic, keyboard reachable, and internally valid", async ({ page }) => {
  await expect(page.locator("header")).toHaveCount(1);
  await expect(page.locator("main")).toHaveCount(1);
  await expect(page.locator("footer")).toHaveCount(1);
  await expect(page.getByRole("heading", { level: 1 })).toHaveCount(1);
  await expect(page.getByRole("heading", { level: 1 })).toContainText("Run agents. Keep control.");

  await page.keyboard.press("Tab");
  const skipLink = page.getByRole("link", { name: "Skip to content" });
  await expect(skipLink).toBeFocused();
  await expect(skipLink).toBeVisible();
  await expect(skipLink).toHaveCSS("outline-style", "solid");
  await page.keyboard.press("Enter");
  await expect(page.locator("main")).toBeFocused();

  const fragments = await page
    .locator('a[href^="#"]')
    .evaluateAll((links) => links.map((link) => link.getAttribute("href")));
  for (const fragment of fragments) {
    expect(fragment, "same-page link must have a non-empty fragment").toMatch(/^#[\w-]+$/);
    await expect(page.locator(fragment!)).toHaveCount(1);
  }

  const insecureExternalLinks = await page.locator('a[href^="http:"]').count();
  expect(insecureExternalLinks).toBe(0);
});

test("describes release destinations and platform readiness truthfully", async ({ page }) => {
  const primaryReleaseLinks = page.getByRole("link", { name: "View latest release" });
  await expect(primaryReleaseLinks).toHaveCount(2);
  for (const link of await primaryReleaseLinks.all()) {
    await expect(link).toHaveAttribute("href", latestRelease);
  }

  const platformLinks = page.locator(".download-row");
  await expect(platformLinks).toHaveCount(3);
  for (const link of await platformLinks.all()) {
    await expect(link).toHaveAttribute("href", latestRelease);
    await expect(link).toContainText("View release");
  }

  await expect(page.getByText("macOS Apple Silicon launch-tested", { exact: false })).toBeVisible();
  await expect(page.getByText("Linux and Windows experimental", { exact: false })).toBeVisible();
  await expect(page.getByText("External Agents remain trusted subprocesses.")).toBeVisible();
  await expect(
    page.getByText("Embedded Cairnline owns portable project identity", { exact: false }),
  ).toBeVisible();
});

test("loads bounded, described product imagery without layout overflow", async ({ page }) => {
  await page.locator("footer").scrollIntoViewIfNeeded();
  await expect
    .poll(() =>
      page
        .locator("img")
        .evaluateAll((images) =>
          images.every((image) => (image as HTMLImageElement).naturalWidth > 0),
        ),
    )
    .toBeTruthy();

  const imageState = await page.locator("img").evaluateAll((images) =>
    images.map((image) => ({
      alt: image.getAttribute("alt"),
      complete: (image as HTMLImageElement).complete,
      height: image.getAttribute("height"),
      naturalHeight: (image as HTMLImageElement).naturalHeight,
      naturalWidth: (image as HTMLImageElement).naturalWidth,
      width: image.getAttribute("width"),
    })),
  );
  expect(imageState.length).toBeGreaterThanOrEqual(5);
  for (const image of imageState) {
    expect(image.alt).not.toBeNull();
    expect(Number(image.width)).toBeGreaterThan(0);
    expect(Number(image.height)).toBeGreaterThan(0);
    expect(image.complete).toBeTruthy();
    expect(image.naturalWidth).toBeGreaterThan(0);
    expect(image.naturalHeight).toBeGreaterThan(0);
  }

  await expect(page.locator(".hero__media img")).toHaveAttribute("fetchpriority", "high");
  const belowFoldImages = page.locator(".product-figure img");
  await expect(belowFoldImages).toHaveCount(3);
  for (const image of await belowFoldImages.all()) {
    await expect(image).toHaveAttribute("loading", "lazy");
  }

  const layout = await page.evaluate(() => {
    const interactive = Array.from(document.querySelectorAll<HTMLElement>("a, button"));
    const viewportWidth = document.documentElement.clientWidth;
    return {
      documentWidth: document.documentElement.scrollWidth,
      outside: interactive
        .map((element) => {
          const rect = element.getBoundingClientRect();
          return { label: element.textContent?.trim() ?? "", left: rect.left, right: rect.right };
        })
        .filter(({ left, right }) => left < -1 || right > viewportWidth + 1),
      viewportWidth,
    };
  });
  expect(layout.documentWidth).toBeLessThanOrEqual(layout.viewportWidth + 1);
  expect(layout.outside).toEqual([]);

  await page.evaluate(() => window.scrollTo(0, 0));
  const primaryAction = page.locator(".hero").getByRole("link", { name: "View latest release" });
  await expect(primaryAction).toHaveCount(1);
  const actionBox = await primaryAction.boundingBox();
  expect(actionBox).not.toBeNull();
  expect(actionBox!.y + actionBox!.height).toBeLessThanOrEqual(page.viewportSize()!.height);

  const statusBox = await page.locator(".hero__status").boundingBox();
  expect(statusBox).not.toBeNull();
  expect(statusBox!.y + statusBox!.height).toBeLessThanOrEqual(page.viewportSize()!.height);
});

test("meets automated WCAG A and AA checks", async ({ page }) => {
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();

  expect(results.violations).toEqual([]);
});
