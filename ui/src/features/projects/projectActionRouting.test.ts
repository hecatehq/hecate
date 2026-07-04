import { describe, expect, it } from "vitest";

import type {
  ProjectHealthAttention,
  ProjectOperationsBriefItem,
  ProjectSetupReadinessAction,
} from "../../types/project";
import {
  projectActivityBucket,
  routeProjectHealthAttention,
  routeProjectOperationAction,
  routeProjectSetupAction,
} from "./projectActionRouting";

function operationItem(overrides: Partial<ProjectOperationsBriefItem> = {}) {
  return {
    id: "op_1",
    kind: "assignment_preflight",
    priority: "high",
    title: "Prepare next step",
    detail: "Assignment is ready to launch.",
    action_label: "Prepare",
    target: {
      surface: "work",
      project_id: "proj_1",
      work_item_id: "work_1",
      assignment_id: "assign_1",
    },
    action: {
      type: "open_assignment_preflight",
      project_id: "proj_1",
      work_item_id: "work_1",
      assignment_id: "assign_1",
      activity_bucket: "active",
    },
    ...overrides,
  } satisfies ProjectOperationsBriefItem;
}

describe("projectActionRouting", () => {
  it("routes server operations without client-side derivation", () => {
    expect(routeProjectOperationAction(operationItem(), "proj_1")).toEqual({
      kind: "open_assignment_preflight",
      assignmentID: "assign_1",
      bucket: "active",
      workItemID: "work_1",
    });

    expect(
      routeProjectOperationAction(
        operationItem({
          action: {
            type: "draft_project_proposal",
            project_id: "proj_1",
            work_item_id: "work_2",
            request: "  Queue reviewer  ",
          },
        }),
        "proj_1",
      ),
    ).toEqual({
      kind: "draft_project_proposal",
      request: "Queue reviewer",
      workItemID: "work_2",
    });

    expect(
      routeProjectOperationAction(
        operationItem({ action: { type: "open_memory_review", project_id: "proj_1" } }),
        "proj_1",
      ),
    ).toEqual({ kind: "open_memory_review" });
  });

  it("rejects stale or incomplete server operations", () => {
    expect(
      routeProjectOperationAction(
        operationItem({ action: { type: "open_project_settings", project_id: "other" } }),
        "proj_1",
      ),
    ).toEqual({
      kind: "error",
      message: "Project operation target changed. Refresh project work and try again.",
    });

    expect(
      routeProjectOperationAction(
        operationItem({
          action: { type: "draft_project_proposal", project_id: "proj_1", request: " " },
        }),
        "proj_1",
      ),
    ).toEqual({
      kind: "error",
      message: "Project operation is missing a Project Assistant draft request.",
    });

    expect(
      routeProjectOperationAction(
        operationItem({
          action: { type: "open_assignment_preflight", project_id: "proj_1" },
        }),
        "proj_1",
      ),
    ).toEqual({
      kind: "error",
      message: "Project operation is missing an assignment preflight target.",
    });
  });

  it("routes setup-readiness actions from the server contract", () => {
    const bootstrap: ProjectSetupReadinessAction = {
      type: "bootstrap_project",
      project_id: "proj_1",
      label: "Set up project",
    };
    expect(routeProjectSetupAction(bootstrap, "proj_1")).toEqual({ kind: "bootstrap_project" });
    expect(
      routeProjectSetupAction(
        { type: "create_work_item", project_id: "proj_1", label: "Create work" },
        "proj_1",
      ),
    ).toEqual({ kind: "create_work_item" });
    expect(
      routeProjectSetupAction(
        { type: "open_project_settings", project_id: "proj_1", label: "Set defaults" },
        "proj_1",
      ),
    ).toEqual({ kind: "open_project_settings" });
    expect(routeProjectSetupAction({ ...bootstrap, project_id: "other" }, "proj_1")).toEqual({
      kind: "error",
      message: "Project setup target changed. Refresh the project.",
    });
  });

  it("routes project health attention items to existing cockpit surfaces", () => {
    const attention = (overrides: Partial<ProjectHealthAttention>): ProjectHealthAttention => ({
      id: "attn_1",
      project_id: "proj_1",
      title: "Needs attention",
      detail: "Open the right surface.",
      status: "blocked",
      action: { type: "open_project_settings", project_id: "proj_1" },
      ...overrides,
    });

    expect(
      routeProjectHealthAttention(
        attention({ action: { type: "open_project_settings", project_id: "proj_1" } }),
      ),
    ).toEqual({
      kind: "open_project_settings",
    });
    expect(
      routeProjectHealthAttention(
        attention({
          action: { type: "open_project_settings", project_id: "other" },
          project_id: "other",
        }),
        {
          selectedProjectID: "proj_1",
        },
      ),
    ).toEqual({
      kind: "error",
      message: "Project attention target changed. Refresh project work and try again.",
    });
    expect(
      routeProjectHealthAttention(
        attention({ action: { type: "open_skills", project_id: "proj_1" } }),
      ),
    ).toEqual({
      kind: "open_skills",
    });
    expect(
      routeProjectHealthAttention(
        attention({ action: { type: "open_agent_presets", project_id: "proj_1" } }),
      ),
    ).toEqual({
      kind: "open_agent_presets",
    });
    expect(
      routeProjectHealthAttention(
        attention({
          action: {
            type: "review_memory_candidate",
            project_id: "proj_1",
            candidate_id: "cand_1",
          },
          candidate_id: "cand_1",
        }),
      ),
    ).toEqual({
      kind: "open_memory_review",
    });
    expect(
      routeProjectHealthAttention(
        attention({
          action: {
            type: "review_memory_candidate",
            project_id: "proj_1",
            candidate_id: "cand_1",
          },
          candidate_id: "cand_1",
        }),
        {
          hasMemoryCandidate: true,
        },
      ),
    ).toEqual({ kind: "review_memory_candidate", candidateID: "cand_1" });
    expect(
      routeProjectHealthAttention(
        attention({
          action: {
            type: "open_work_item",
            project_id: "proj_1",
            work_item_id: "work_1",
            activity_bucket: "blocked",
          },
          bucket: "blocked",
          work_item_id: "work_1",
        }),
      ),
    ).toEqual({
      kind: "open_work_item",
      workItemID: "work_1",
      bucket: "blocked",
    });
    expect(
      routeProjectHealthAttention(
        attention({
          action: {
            type: "open_task",
            project_id: "proj_1",
            task_id: "task_1",
            run_id: "run_1",
          },
          run_id: "run_1",
          task_id: "task_1",
        }),
      ),
    ).toEqual({
      kind: "open_task",
      taskID: "task_1",
      runID: "run_1",
    });
  });

  it("accepts only known activity buckets", () => {
    expect(projectActivityBucket("recent")).toBe("recent");
    expect(projectActivityBucket("unexpected")).toBeUndefined();
  });
});
