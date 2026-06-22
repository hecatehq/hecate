import { describe, expect, it } from "vitest";

import { ApiError } from "../../lib/api";
import type { ProjectAssistantProposal } from "../../types/project";
import {
  projectAssistantApplyErrorMessage,
  projectAssistantContextPayload,
  projectAssistantDraftPayload,
  projectAssistantResultWorkItemID,
} from "./useProjectAssistantController";

describe("Project Assistant controller helpers", () => {
  it("builds context and draft payloads from panel form state", () => {
    const form = {
      request: "Queue review",
      roleID: "role_review",
      driverKind: "external_agent",
      draftMode: "model" as const,
    };

    expect(projectAssistantContextPayload(form, "proj_1", "work_1")).toEqual({
      project_id: "proj_1",
      work_item_id: "work_1",
      request: "Queue review",
      role_id: "role_review",
      driver_kind: "external_agent",
    });
    expect(projectAssistantDraftPayload(form, "proj_1", "work_1")).toEqual({
      project_id: "proj_1",
      work_item_id: "work_1",
      request: "Queue review",
      role_id: "role_review",
      driver_kind: "external_agent",
      draft_mode: "model",
    });
  });

  it("omits auto role, auto driver, and deterministic draft mode", () => {
    const form = {
      request: "Create work",
      roleID: "__auto__",
      driverKind: "__auto__",
      draftMode: "deterministic" as const,
    };

    expect(projectAssistantDraftPayload(form, "proj_1")).toEqual({
      project_id: "proj_1",
      request: "Create work",
    });
  });

  it("prefers applied work item ids when choosing refresh target", () => {
    expect(
      projectAssistantResultWorkItemID({
        proposal_id: "pa_1",
        status: "applied",
        applied: true,
        actions: [
          { kind: "create_role", id: "role_1", data: { project_id: "proj_1" } },
          {
            kind: "create_assignment",
            id: "asgn_1",
            data: { project_id: "proj_1", work_item_id: "work_2" },
          },
        ],
      }),
    ).toBe("work_2");
  });

  it("renders conflict and partial apply errors with proposal context", () => {
    expect(projectAssistantApplyErrorMessage(new ApiError("conflict", 409, "conflict"))).toContain(
      "proposal is stale",
    );

    const proposal: ProjectAssistantProposal = {
      id: "pa_partial",
      title: "Apply two",
      summary: "",
      requires_confirmation: true,
      actions: [
        { kind: "create_assignment", patch: {} },
        { kind: "create_memory_candidate", patch: {} },
      ],
    };
    const partial = projectAssistantApplyErrorMessage(
      new ApiError("partial", 409, "conflict", {
        fields: {
          failed_action_index: 1,
          partial_result: {
            proposal_id: "pa_partial",
            status: "partial_due_to_runtime_failure",
            applied: false,
            total_action_count: 2,
            committed_action_count: 1,
            failed_action_index: 1,
            resume_action_index: 1,
            actions: [{ kind: "create_assignment", id: "asgn_1" }],
          },
        },
      }),
      proposal,
    );
    expect(partial).toContain("applied 1 of 2 actions");
    expect(partial).toContain("create assignment asgn_1");
    expect(partial).toContain("failed at action 2 (create memory candidate)");

    const blocked = projectAssistantApplyErrorMessage(
      new ApiError("blocked", 404, "not_found", {
        fields: {
          apply_status: "blocked_before_apply",
          failed_action_index: 1,
          total_action_count: 2,
          committed_action_count: 0,
          resume_action_index: 0,
          partial_result: {
            proposal_id: "pa_blocked",
            status: "blocked_before_apply",
            applied: false,
            total_action_count: 2,
            committed_action_count: 0,
            failed_action_index: 1,
            resume_action_index: 0,
            actions: [],
          },
        },
      }),
      proposal,
    );
    expect(blocked).toContain("blocked this proposal before applying any actions");
    expect(blocked).toContain("failed at action 2 (create memory candidate)");
    expect(blocked).not.toContain("applied 0 of 2 actions");

    const blockedResume = projectAssistantApplyErrorMessage(
      new ApiError("blocked", 404, "not_found", {
        fields: {
          apply_status: "blocked_before_apply",
          failed_action_index: 1,
          total_action_count: 2,
          committed_action_count: 1,
          resume_action_index: 1,
          partial_result: {
            proposal_id: "pa_blocked_resume",
            status: "blocked_before_apply",
            applied: false,
            total_action_count: 2,
            committed_action_count: 1,
            failed_action_index: 1,
            resume_action_index: 1,
            actions: [{ kind: "create_assignment", id: "asgn_1" }],
          },
        },
      }),
      proposal,
    );
    expect(blockedResume).toContain("blocked this proposal before applying additional actions");
    expect(blockedResume).toContain("1 of 2 actions was already committed");
    expect(blockedResume).toContain("create assignment asgn_1");

    const serverCountedPartial = projectAssistantApplyErrorMessage(
      new ApiError("partial", 409, "conflict", {
        fields: {
          apply_status: "partial_due_to_runtime_failure",
          failed_action_index: 1,
          total_action_count: 3,
          committed_action_count: 1,
          resume_action_index: 1,
          partial_result: {
            proposal_id: "pa_partial",
            status: "partial_due_to_runtime_failure",
            applied: false,
            total_action_count: 3,
            committed_action_count: 1,
            failed_action_index: 1,
            resume_action_index: 1,
            actions: [{ kind: "create_assignment", id: "asgn_1" }],
          },
        },
      }),
    );
    expect(serverCountedPartial).toContain("applied 1 of 3 actions");
  });
});
