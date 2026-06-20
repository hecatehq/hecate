import { describe, expect, it } from "vitest";

import { projectVisibilityDetail } from "./projectVisibilityDetail";

describe("projectVisibilityDetail", () => {
  it("reconciles shown, total, and server-capped operation counts", () => {
    expect(
      projectVisibilityDetail({
        shownCount: 4,
        totalCount: 9,
        itemLabelSingular: "operation",
        itemLabelPlural: "operations",
        hiddenLabelSingular: "operation",
        hiddenLabelPlural: "operations",
        serverOmittedCount: 1,
      }),
    ).toBe(
      "Showing 4 of 9 operations; 5 lower-priority operations are hidden (1 capped by the server).",
    );
  });

  it("formats attention item copy without server-cap wording", () => {
    expect(
      projectVisibilityDetail({
        shownCount: 5,
        totalCount: 7,
        itemLabelSingular: "attention item",
        itemLabelPlural: "attention items",
        hiddenLabelSingular: "item",
        hiddenLabelPlural: "items",
      }),
    ).toBe("Showing 5 of 7 attention items; 2 lower-priority items are hidden.");
  });

  it("returns an empty detail when nothing is hidden", () => {
    expect(
      projectVisibilityDetail({
        shownCount: 3,
        totalCount: 3,
        itemLabelSingular: "operation",
        itemLabelPlural: "operations",
        hiddenLabelSingular: "operation",
        hiddenLabelPlural: "operations",
      }),
    ).toBe("");
  });
});
