import { describe, expect, it } from "vitest";

import type {
  ProjectActivityItemRecord,
  ProjectAssignmentRecord,
  ProjectCollaborationArtifactRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import {
  assignmentStatusFromValue,
  assignmentUpdatePayloadFromForm,
  handoffFormFromAssignment,
  handoffFormFromReviewArtifact,
  handoffPayloadFromForm,
  handoffStatusFromValue,
  reviewHandoffFormFromAssignment,
  reviewArtifactFormFromAssignment,
  reviewArtifactPayloadFromForm,
  workItemCreatePayloadFromForm,
  workItemPriorityFromValue,
  workItemStatusFromValue,
} from "./projectWorkForms";

describe("projectWorkForms", () => {
  it("builds work item create payloads with trimmed optional fields", () => {
    expect(
      workItemCreatePayloadFromForm({
        title: "  Build cockpit  ",
        brief: "  Ship the UI slice  ",
        priority: "normal",
        ownerRoleID: "",
        rootID: " root_main ",
      }),
    ).toEqual({
      title: "Build cockpit",
      brief: "Ship the UI slice",
      status: "ready",
      priority: "normal",
      owner_role_id: undefined,
      root_id: "root_main",
    });
  });

  it("builds canonical assignment execution refs from edit form fields", () => {
    expect(
      assignmentUpdatePayloadFromForm({
        id: "assign_1",
        roleID: " developer ",
        driverKind: "",
        rootID: " root_main ",
        status: "queued",
        taskID: " task_1 ",
        runID: " run_1 ",
        chatSessionID: "",
        messageID: "",
        contextSnapshotID: " ctx_1 ",
      }),
    ).toEqual({
      role_id: "developer",
      root_id: "root_main",
      driver_kind: "hecate_task",
      status: "queued",
      execution_ref: {
        kind: "task_run",
        task_id: "task_1",
        run_id: "run_1",
        context_snapshot_id: "ctx_1",
      },
    });
  });

  it("builds handoff payloads with split reference lists and defaults", () => {
    expect(
      handoffPayloadFromForm({
        id: "handoff_1",
        sourceAssignmentID: " assign_1 ",
        sourceRunID: " run_1 ",
        sourceChatSessionID: " chat_1 ",
        sourceMessageID: " msg_1 ",
        targetRoleID: " reviewer ",
        targetAssignmentID: " assign_2 ",
        title: " Review it ",
        summary: " Summary ",
        recommendedNextAction: " Test it ",
        linkedArtifactIDs: " art_1, art_2 ",
        linkedMemoryIDs: " mem_1 ",
        contextRefs: " ctx_1, task_1 ",
        status: "pending",
        provenanceKind: "",
        trustLabel: "",
      }),
    ).toEqual({
      source_assignment_id: "assign_1",
      source_run_id: "run_1",
      source_chat_session_id: "chat_1",
      source_message_id: "msg_1",
      target_role_id: "reviewer",
      target_assignment_id: "assign_2",
      title: "Review it",
      summary: "Summary",
      recommended_next_action: "Test it",
      linked_artifact_ids: ["art_1", "art_2"],
      linked_memory_ids: ["mem_1"],
      context_refs: ["ctx_1", "task_1"],
      status: "pending",
      provenance_kind: "operator",
      trust_label: "operator_reviewed",
    });
  });

  it("normalizes backend status and priority strings into form-safe options", () => {
    expect(workItemStatusFromValue(" review ")).toBe("review");
    expect(workItemStatusFromValue("unknown")).toBe("ready");
    expect(workItemPriorityFromValue("urgent")).toBe("urgent");
    expect(workItemPriorityFromValue("unknown")).toBe("normal");
    expect(assignmentStatusFromValue("awaiting_approval")).toBe("awaiting_approval");
    expect(assignmentStatusFromValue("paused")).toBe("queued");
    expect(handoffStatusFromValue("accepted")).toBe("accepted");
    expect(handoffStatusFromValue("unknown")).toBe("pending");
  });

  it("drafts handoff forms from assignment execution evidence", () => {
    const assignment: ProjectAssignmentRecord = {
      id: "assign_1234567890",
      project_id: "proj_1",
      work_item_id: "work_1",
      role_id: "developer",
      driver_kind: "hecate_task",
      status: "completed",
      execution_ref: {
        kind: "task_run",
        task_id: "task_1",
        run_id: "run_1",
        context_snapshot_id: "ctx_1",
      },
      created_at: "2026-06-12T00:00:00Z",
      updated_at: "2026-06-12T00:00:00Z",
    };
    const activity: ProjectActivityItemRecord = {
      id: "activity_1",
      project_id: "proj_1",
      work_item: {
        id: "work_1",
        title: "Build cockpit",
        status: "ready",
        priority: "normal",
      },
      assignment,
      role: { id: "developer", project_id: "proj_1", name: "Developer", built_in: false },
      status: "completed",
      blocking_signal: "completed",
      status_summary: "completed",
      linked_message_id: "msg_1",
      artifact_summary: { count: 0 },
      updated_at: "2026-06-12T00:00:00Z",
    };

    expect(
      handoffFormFromAssignment(
        assignment,
        { id: "developer", project_id: "proj_1", name: "Developer", built_in: false },
        activity,
      ),
    ).toMatchObject({
      sourceAssignmentID: "assign_1234567890",
      sourceRunID: "run_1",
      sourceMessageID: "msg_1",
      title: "Developer handoff",
      contextRefs: "ctx_1, task_1, run_1, msg_1",
      status: "pending",
    });
  });

  it("drafts reviewer handoffs with target role and source evidence", () => {
    const assignment: ProjectAssignmentRecord = {
      id: "assign_1234567890",
      project_id: "proj_1",
      work_item_id: "work_1",
      role_id: "developer",
      driver_kind: "hecate_task",
      status: "completed",
      execution_ref: {
        kind: "task_run",
        task_id: "task_1",
        run_id: "run_1",
        context_snapshot_id: "ctx_1",
      },
      created_at: "2026-06-12T00:00:00Z",
      updated_at: "2026-06-12T00:00:00Z",
    };
    const sourceRole: ProjectWorkRoleRecord = {
      id: "developer",
      project_id: "proj_1",
      name: "Developer",
      built_in: false,
    };
    const reviewRole: ProjectWorkRoleRecord = {
      id: "reviewer_qa",
      project_id: "proj_1",
      name: "QA reviewer",
      built_in: false,
    };
    const workItem: ProjectWorkItemRecord = {
      id: "work_1",
      project_id: "proj_1",
      title: "Build cockpit",
      status: "review",
      priority: "normal",
      reviewer_role_ids: ["reviewer_qa"],
      created_at: "2026-06-12T00:00:00Z",
      updated_at: "2026-06-12T00:00:00Z",
    };

    expect(
      reviewHandoffFormFromAssignment(assignment, sourceRole, reviewRole, workItem),
    ).toMatchObject({
      sourceAssignmentID: "assign_1234567890",
      sourceRunID: "run_1",
      targetRoleID: "reviewer_qa",
      title: "QA reviewer review request",
      summary: 'Review Developer\'s assignment for "Build cockpit".',
      contextRefs: "ctx_1, task_1, run_1",
      status: "pending",
      provenanceKind: "operator",
      trustLabel: "operator_reviewed",
    });
  });

  it("builds review artifact payloads with a consistent body template", () => {
    const assignment: ProjectAssignmentRecord = {
      id: "assign_review",
      project_id: "proj_1",
      work_item_id: "work_1",
      role_id: "reviewer_qa",
      driver_kind: "hecate_task",
      status: "completed",
      created_at: "2026-06-12T00:00:00Z",
      updated_at: "2026-06-12T00:00:00Z",
    };
    const workItem: ProjectWorkItemRecord = {
      id: "work_1",
      project_id: "proj_1",
      title: "Build cockpit",
      status: "review",
      priority: "normal",
      created_at: "2026-06-12T00:00:00Z",
      updated_at: "2026-06-12T00:00:00Z",
    };
    const form = reviewArtifactFormFromAssignment(
      assignment,
      { id: "reviewer_qa", project_id: "proj_1", name: "QA reviewer", built_in: false },
      workItem,
    );

    expect(
      reviewArtifactPayloadFromForm({
        ...form,
        verdict: "changes_requested",
        risk: "medium",
        summary: "Behavior is mostly correct, but empty state needs polish.",
        verification: "Ran focused UI tests.",
        followUp: "Fix empty state copy.",
      }),
    ).toEqual({
      assignment_id: "assign_review",
      author_role_id: "reviewer_qa",
      kind: "review",
      title: "QA reviewer review",
      body: [
        "Verdict: Changes requested",
        "Risk: Medium",
        "",
        "Summary:",
        "Behavior is mostly correct, but empty state needs polish.",
        "",
        "Verification:",
        "Ran focused UI tests.",
        "",
        "Follow-up:",
        "Fix empty state copy.",
      ].join("\n"),
    });
  });

  it("drafts follow-up handoffs from review artifacts", () => {
    const artifact: ProjectCollaborationArtifactRecord = {
      id: "art_review",
      project_id: "proj_1",
      work_item_id: "work_1",
      assignment_id: "assign_review",
      kind: "review",
      title: "QA reviewer review",
      body: "Verdict: Changes requested",
      author_role_id: "reviewer_qa",
      created_at: "2026-06-12T00:00:00Z",
      updated_at: "2026-06-12T00:00:00Z",
    };
    const workItem: ProjectWorkItemRecord = {
      id: "work_1",
      project_id: "proj_1",
      title: "Build cockpit",
      status: "review",
      priority: "normal",
      owner_role_id: "software_developer",
      created_at: "2026-06-12T00:00:00Z",
      updated_at: "2026-06-12T00:00:00Z",
    };

    expect(handoffFormFromReviewArtifact(artifact, workItem)).toMatchObject({
      sourceAssignmentID: "assign_review",
      targetRoleID: "software_developer",
      title: "QA reviewer review follow-up",
      linkedArtifactIDs: "art_review",
      status: "pending",
    });
  });
});
