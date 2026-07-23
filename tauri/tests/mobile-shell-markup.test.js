import { describe, expect, it } from "bun:test";

const html = await Bun.file(new URL("../mobile/index.html", import.meta.url)).text();

describe("mobile chat-first shell markup", () => {
  it("puts instance selection before secondary account settings", () => {
    expect(html).toContain('id="home-title">Choose Hecate</h1>');
    expect(html).not.toContain("Cloud companion");
    expect(html.indexOf('id="connectionsSection"')).toBeLessThan(html.indexOf('id="settingsView"'));
  });

  it("makes each instance one semantic row action", async () => {
    const rows = [];
    const transformed = new HTMLRewriter()
      .on("#connectionTemplate .connection-action", {
        element(element) {
          rows.push({
            tagName: element.tagName,
            type: element.getAttribute("type"),
          });
        },
      })
      .transform(new Response(html));

    await transformed.text();
    expect(rows).toEqual([{ tagName: "button", type: "button" }]);
  });

  it("has explicit settings and return navigation", async () => {
    const controls = [];
    const transformed = new HTMLRewriter()
      .on("#settingsButton", {
        element(element) {
          controls.push({
            id: "settings",
            label: element.getAttribute("aria-label"),
            hidden: element.hasAttribute("hidden"),
          });
        },
      })
      .on("#settingsBackButton", {
        element(element) {
          controls.push({
            id: "back",
            label: element.getAttribute("aria-label"),
            hidden: element.hasAttribute("hidden"),
          });
        },
      })
      .transform(new Response(html));

    await transformed.text();
    expect(controls).toEqual([
      { id: "settings", label: "Open settings", hidden: true },
      { id: "back", label: "Back to Hecate instances", hidden: false },
    ]);
  });

  it("announces hosted-runtime start results in the signed-in view", async () => {
    const statuses = [];
    const transformed = new HTMLRewriter()
      .on("#homeStatus", {
        element(element) {
          statuses.push({
            role: element.getAttribute("role"),
            live: element.getAttribute("aria-live"),
            atomic: element.getAttribute("aria-atomic"),
            tabindex: element.getAttribute("tabindex"),
            hidden: element.hasAttribute("hidden"),
          });
        },
      })
      .transform(new Response(html));

    await transformed.text();
    expect(statuses).toEqual([
      {
        role: "status",
        live: "polite",
        atomic: "true",
        tabindex: "-1",
        hidden: true,
      },
    ]);
  });

  it("keeps accessibility zoom available", () => {
    const viewport = html.match(/<meta name="viewport" content="([^"]+)"/)?.[1] ?? "";
    expect(viewport).toContain("width=device-width");
    expect(viewport).not.toContain("maximum-scale");
    expect(viewport).not.toContain("user-scalable=no");
  });
});
