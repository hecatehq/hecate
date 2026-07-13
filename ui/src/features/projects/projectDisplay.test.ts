import { afterEach, describe, expect, it, vi } from "vitest";

import type { ProjectRecord } from "../../types/project";
import {
  assignmentStatusLabel,
  formatProjectRowRelativeTime,
  handoffStatusLabel,
  projectRoleLabel,
  projectRootDisplayLabel,
  projectRootTitle,
  workStatusLabel,
} from "./projectDisplay";

function project(): ProjectRecord {
  return {
    id: "proj_1",
    name: "Hecate",
    roots: [
      {
        id: "root_main",
        path: "/workspace/hecate",
        kind: "workspace",
        active: true,
        created_at: "2026-06-12T00:00:00Z",
        updated_at: "2026-06-12T00:00:00Z",
      },
      {
        id: "root_worktree",
        path: "/workspace/hecate-feature",
        kind: "git_worktree",
        git_branch: "feature/project-ui",
        active: true,
        created_at: "2026-06-12T00:00:00Z",
        updated_at: "2026-06-12T00:00:00Z",
      },
    ],
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
  };
}

describe("projectDisplay", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("formats relative project row timestamps", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-06-13T12:00:00Z"));

    expect(formatProjectRowRelativeTime("2026-06-13T11:59:30Z")).toBe("30s ago");
    expect(formatProjectRowRelativeTime("2026-06-13T11:30:00Z")).toBe("30m ago");
    expect(formatProjectRowRelativeTime("2026-06-12T12:00:00Z")).toBe("1d ago");
    expect(formatProjectRowRelativeTime("not-a-date")).toBe("not-a-date");
    expect(formatProjectRowRelativeTime("")).toBe("—");
  });

  it("keeps project status and root labels stable", () => {
    const record = project();

    expect(workStatusLabel("ready_for_review")).toBe("ready for review");
    expect(assignmentStatusLabel("awaiting_approval")).toBe("approval");
    expect(assignmentStatusLabel("completed")).toBe("done");
    expect(projectRoleLabel("release_reviewer", [])).toBe("Release reviewer");
    expect(
      projectRoleLabel("release_reviewer", [
        {
          id: "release_reviewer",
          project_id: "proj_1",
          name: "Release reviewer",
          built_in: false,
        },
      ]),
    ).toBe("Release reviewer");
    expect(handoffStatusLabel("dismissed")).toBe("Dismissed");
    expect(projectRootDisplayLabel(record, "root_main")).toBe("hecate");
    expect(projectRootDisplayLabel(record, "root_worktree")).toBe("feature/project-ui");
    expect(projectRootTitle(record, "root_worktree")).toContain("feature/project-ui");
    expect(projectRootDisplayLabel(record, "root_1234567890abcdef")).toBe("root_12345...");
  });
});
