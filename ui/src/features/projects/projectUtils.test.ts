import { describe, expect, it } from "vitest";

import { firstNonEmpty, shortID, splitIDs, splitRoleIDs } from "./projectUtils";

describe("projectUtils", () => {
  it("splits comma-separated ids and drops empty entries", () => {
    expect(splitIDs(" role_a, , role_b ,, role_c ")).toEqual(["role_a", "role_b", "role_c"]);
    expect(splitRoleIDs(" reviewer, architect ")).toEqual(["reviewer", "architect"]);
  });

  it("shortens long ids without changing compact ids", () => {
    expect(shortID("assign_123")).toBe("assign_123");
    expect(shortID("assign_1234567890")).toBe("assign_123...");
  });

  it("returns the first non-empty trimmed string", () => {
    expect(firstNonEmpty(undefined, "   ", null, " value ", "later")).toBe("value");
    expect(firstNonEmpty(undefined, "", null)).toBe("");
  });
});
