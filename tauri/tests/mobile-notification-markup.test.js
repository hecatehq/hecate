import { describe, expect, it } from "bun:test";

const html = await Bun.file(new URL("../mobile/index.html", import.meta.url)).text();

describe("mobile run notification markup", () => {
  it("uses a labelled region with announced status and native buttons", async () => {
    const found = {};
    const transformed = new HTMLRewriter()
      .on("#notificationSection", {
        element(element) {
          found.section = {
            tagName: element.tagName,
            labelledBy: element.getAttribute("aria-labelledby"),
            busy: element.getAttribute("aria-busy"),
            hidden: element.hasAttribute("hidden"),
          };
        },
      })
      .on("#notificationMessage", {
        element(element) {
          found.message = {
            role: element.getAttribute("role"),
            live: element.getAttribute("aria-live"),
            atomic: element.getAttribute("aria-atomic"),
          };
        },
      })
      .on("#notificationSection button", {
        element(element) {
          found.buttons ??= [];
          found.buttons.push({ tagName: element.tagName, type: element.getAttribute("type") });
        },
      })
      .transform(new Response(html));

    await transformed.text();
    expect(found.section).toEqual({
      tagName: "section",
      labelledBy: "notification-title",
      busy: "false",
      hidden: true,
    });
    expect(found.message).toEqual({ role: "status", live: "polite", atomic: "true" });
    expect(found.buttons).toHaveLength(3);
    expect(
      found.buttons.every((button) => button.tagName === "button" && button.type === "button"),
    ).toBe(true);
  });
});
