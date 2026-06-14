import { describe, expect, it } from "vitest";

import { createProjectPayloadFromForm } from "./projectSettings";

describe("projectSettings", () => {
  it("maps rootless project creation forms without roots", () => {
    expect(
      createProjectPayloadFromForm({
        name: " Launch plan ",
        description: " Coordinate release work. ",
        rootPath: "",
        rootGitBranch: "",
      }),
    ).toEqual({
      name: "Launch plan",
      description: "Coordinate release work.",
    });
  });

  it("maps optional workspace roots when provided", () => {
    expect(
      createProjectPayloadFromForm({
        name: "Hecate",
        description: "",
        rootPath: " /Users/alice/dev/hecate ",
        rootGitBranch: " main ",
      }),
    ).toEqual({
      name: "Hecate",
      roots: [
        {
          path: "/Users/alice/dev/hecate",
          kind: "local",
          git_branch: "main",
          active: true,
        },
      ],
    });
  });
});
