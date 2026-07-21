import { expect, mockGatewayAPIs, test } from "./fixtures";

test("keeps transcript actions visible on hybrid touch devices", async ({ browser }) => {
  const context = await browser.newContext({
    baseURL: test.info().project.use.baseURL,
    hasTouch: true,
    viewport: { height: 900, width: 1440 },
  });
  const page = await context.newPage();
  try {
    await mockGatewayAPIs(page);
    await page.goto("/");
    await page.evaluate(() => {
      document.body.innerHTML = `
        <div class="transcript-message-row">
          <div class="transcript-message-actions">Read aloud</div>
        </div>`;
    });

    expect(await page.evaluate(() => matchMedia("(any-pointer: coarse)").matches)).toBe(true);
    await expect(page.locator(".transcript-message-actions")).toHaveCSS("opacity", "1");
    expect(
      await page.evaluate(() =>
        Array.from(document.styleSheets).some((sheet) =>
          Array.from(sheet.cssRules).some(
            (rule) =>
              rule instanceof CSSMediaRule &&
              rule.conditionText.includes("(any-pointer: coarse)") &&
              Array.from(rule.cssRules).some(
                (nested) =>
                  nested instanceof CSSStyleRule &&
                  nested.selectorText === ".transcript-message-actions" &&
                  nested.style.opacity === "1",
              ),
          ),
        ),
      ),
    ).toBe(true);
  } finally {
    await context.close();
  }
});
