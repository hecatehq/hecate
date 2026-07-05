import { describe, expect, it } from "vitest";

import {
  projectCoordinationConfigAssignment,
  projectCoordinationConfigBlock,
  projectCoordinationNextActionConfigBlock,
} from "./project-coordination-backend";

describe("project coordination backend helpers", () => {
  it("builds shell-style config assignments and env blocks from hints", () => {
    expect(
      projectCoordinationConfigAssignment({
        env: "HECATE_PROJECTS_COORDINATION_BACKEND",
        value: "cairnline",
      }),
    ).toBe("HECATE_PROJECTS_COORDINATION_BACKEND=cairnline");

    expect(
      projectCoordinationConfigBlock([
        {
          env: "HECATE_PROJECTS_COORDINATION_BACKEND",
          value: "cairnline",
        },
        {
          env: "HECATE_PROJECTS_CAIRNLINE_CONNECTOR",
          value: "embedded",
        },
      ]),
    ).toBe(
      "HECATE_PROJECTS_COORDINATION_BACKEND=cairnline\nHECATE_PROJECTS_CAIRNLINE_CONNECTOR=embedded",
    );
  });

  it("returns an empty block without hints", () => {
    expect(projectCoordinationConfigBlock(undefined)).toBe("");
    expect(projectCoordinationConfigBlock([])).toBe("");
  });

  it("prefers a backend-provided config block and falls back to hints", () => {
    expect(
      projectCoordinationNextActionConfigBlock({
        id: "enable-cairnline-dogfood",
        label: "Enable Cairnline dogfood",
        detail: "Configure the runtime.",
        config_block: "HECATE_PROJECTS_COORDINATION_BACKEND=cairnline",
        config_hints: [
          {
            env: "HECATE_PROJECTS_CAIRNLINE_CONNECTOR",
            value: "embedded",
          },
        ],
      }),
    ).toBe("HECATE_PROJECTS_COORDINATION_BACKEND=cairnline");

    expect(
      projectCoordinationNextActionConfigBlock({
        id: "enable-cairnline-dogfood",
        label: "Enable Cairnline dogfood",
        detail: "Configure the runtime.",
        config_hints: [
          {
            env: "HECATE_PROJECTS_CAIRNLINE_CONNECTOR",
            value: "embedded",
          },
        ],
      }),
    ).toBe("HECATE_PROJECTS_CAIRNLINE_CONNECTOR=embedded");
  });
});
