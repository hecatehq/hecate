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
  evidenceLinkPayloadFromForm,
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

  it("rejects stale activity messages when drafting handoff provenance", () => {
    const assignment: ProjectAssignmentRecord = {
      id: "assign_1",
      project_id: "proj_1",
      work_item_id: "work_1",
      role_id: "developer",
      driver_kind: "external_agent",
      status: "running",
      execution_ref: {
        kind: "chat_session",
        chat_session_id: "chat_current",
        context_snapshot_id: "ctx_current",
        status: "running",
      },
      created_at: "2026-06-12T00:00:00Z",
      updated_at: "2026-06-12T02:00:00Z",
    };
    const staleVersionAssignment: ProjectAssignmentRecord = {
      ...assignment,
      updated_at: "2026-06-12T01:00:00Z",
    };
    const staleVersionActivity: ProjectActivityItemRecord = {
      id: assignment.id,
      project_id: assignment.project_id,
      work_item: {
        id: assignment.work_item_id,
        title: "Build cockpit",
        status: "running",
        priority: "normal",
      },
      assignment: staleVersionAssignment,
      role: { id: "developer", project_id: "proj_1", name: "Developer", built_in: false },
      status: "running",
      blocking_signal: "running",
      status_summary: "linked chat running",
      linked_chat_id: "chat_current",
      linked_message_id: "msg_stale_version",
      artifact_summary: { count: 0 },
      updated_at: staleVersionAssignment.updated_at,
    };
    const differentRuntimeAssignment: ProjectAssignmentRecord = {
      ...assignment,
      execution_ref: {
        ...assignment.execution_ref,
        kind: "chat_session",
        chat_session_id: "chat_stale",
      },
    };
    const differentRuntimeActivity: ProjectActivityItemRecord = {
      ...staleVersionActivity,
      assignment: differentRuntimeAssignment,
      linked_chat_id: "chat_stale",
      linked_message_id: "msg_stale_runtime",
      updated_at: assignment.updated_at,
    };

    for (const activity of [staleVersionActivity, differentRuntimeActivity]) {
      expect(handoffFormFromAssignment(assignment, null, activity)).toMatchObject({
        sourceChatSessionID: "chat_current",
        sourceMessageID: "",
        contextRefs: "ctx_current, chat_current",
      });
    }
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
      [
        {
          id: "handoff_review",
          project_id: "proj_1",
          work_item_id: "work_1",
          source_assignment_id: "assign_impl",
          target_assignment_id: "assign_review",
          title: "Review request",
          summary: "Review implementation.",
          recommended_next_action: "Record review.",
          status: "accepted",
          provenance_kind: "operator",
          trust_label: "operator_reviewed",
          created_at: "2026-06-12T00:00:00Z",
          updated_at: "2026-06-12T00:00:00Z",
          status_changed_at: "2026-06-12T00:00:00Z",
        },
      ],
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
      reviewed_assignment_id: "assign_impl",
      review_follow_up_required: true,
      review_risk: "medium",
      review_verdict: "changes_requested",
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

  it("builds evidence link payloads with neutral metadata", () => {
    expect(
      evidenceLinkPayloadFromForm({
        assignmentID: "",
        title: "Research source",
        sourceKind: " source_document ",
        url: " https://example.invalid/research ",
        externalID: " DOC-42 ",
        provider: " docs ",
        trustLabel: "",
        summary: " Source used to validate this work. ",
      }),
    ).toEqual({
      kind: "evidence_link",
      title: "Research source",
      body: "Source used to validate this work.",
      evidence_source_kind: "source_document",
      evidence_url: "https://example.invalid/research",
      evidence_external_id: "DOC-42",
      evidence_provider: "docs",
      evidence_trust_label: "operator_provided",
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
