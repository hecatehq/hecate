import { describe, expect, it } from "vitest";

import {
  buildProjectHealthSummary,
  buildProjectWorkCloseoutReadiness,
  projectHealthMetrics,
  reviewArtifactNeedsFollowUpPath,
  reviewArtifactRequiresFollowUp,
} from "./projectInsights";
import type { AgentProfileRecord } from "../../types/agent-profile";
import type {
  ProjectActivityData,
  ProjectActivityItemRecord,
  ProjectAssignmentRecord,
  ProjectCollaborationArtifactRecord,
  ProjectHandoffRecord,
  ProjectRecord,
  ProjectSkillRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";

describe("projectInsights", () => {
  it("calls out projects without an active root", () => {
    const project = {
      id: "proj_no_root",
      name: "Project",
      roots: [],
      default_provider: "openai",
      default_model: "gpt-4.1",
      context_sources: [],
      created_at: "2026-06-04T10:00:00Z",
      updated_at: "2026-06-04T10:00:00Z",
    } satisfies ProjectRecord;

    const health = buildProjectHealthSummary(project, null, [], [], []);

    expect(health.attention.some((item) => item.title === "No project root configured")).toBe(true);
  });

  it("surfaces unresolved and disabled project skill references", () => {
    const project = readyProject();
    const role = projectRole({
      skill_ids: ["backend", "review"],
    });
    const skills = [
      projectSkill({
        id: "backend",
        enabled: false,
      }),
    ];

    const health = buildProjectHealthSummary(project, null, [], [], [], {
      roles: [role],
      skills,
    });

    expect(health.attention).toContainEqual(
      expect.objectContaining({
        title: "Project skills need review",
        detail: expect.stringContaining("unresolved: review"),
        action: "skills",
      }),
    );
    expect(health.attention).toContainEqual(
      expect.objectContaining({
        title: "Project skills need review",
        detail: expect.stringContaining("disabled: backend"),
        action: "skills",
      }),
    );
  });

  it("surfaces enabled project skills that are not available", () => {
    const project = readyProject();
    const health = buildProjectHealthSummary(project, null, [], [], [], {
      skills: [
        projectSkill({
          id: "backend",
          status: "conflict",
        }),
      ],
    });

    expect(health.attention).toContainEqual(
      expect.objectContaining({
        title: "Project skills need review",
        detail: expect.stringContaining("backend"),
        action: "skills",
      }),
    );
  });

  it("surfaces missing agent profile references when the profile catalog is loaded", () => {
    const project = {
      ...readyProject(),
      default_agent_profile: "missing_profile",
    };
    const health = buildProjectHealthSummary(project, null, [], [], [], {
      agentProfiles: [agentProfile("implementation")],
      roles: [projectRole({ default_agent_profile: "implementation" })],
    });

    expect(health.attention).toContainEqual(
      expect.objectContaining({
        title: "Agent profile reference missing",
        detail: expect.stringContaining("missing_profile"),
        action: "profiles",
      }),
    );
  });

  it("labels handoff health metrics as recent when they come from bounded activity handoffs", () => {
    const project = {
      id: "proj_1",
      name: "Project",
      roots: [],
      default_provider: "openai",
      default_model: "gpt-4.1",
      context_sources: [],
      created_at: "2026-06-04T10:00:00Z",
      updated_at: "2026-06-04T10:00:00Z",
    } satisfies ProjectRecord;
    const recentHandoffs = [
      handoff("handoff_pending", "pending"),
      handoff("handoff_accepted", "accepted"),
      handoff("handoff_superseded", "superseded"),
      handoff("handoff_dismissed", "dismissed"),
    ];
    const activity = {
      project_id: project.id,
      summary: {
        work_item_count: 1,
        assignment_count: 1,
        active_count: 1,
        blocked_count: 0,
        completed_count: 0,
        recent_count: 1,
      },
      buckets: {
        active: [activityItem(project.id, recentHandoffs)],
        blocked: [],
        completed: [],
        recent: [],
      },
      recent: [],
    } as ProjectActivityData;

    const health = buildProjectHealthSummary(project, activity, [], [], []);
    const metrics = projectHealthMetrics(health);
    const metric = metrics.find((item) => item.key === "handoffs");

    expect(metrics.map((item) => item.key)).toEqual([
      "defaults",
      "context",
      "memory_candidates",
      "reviews",
      "handoffs",
      "stale",
    ]);
    expect(metric?.label).toBe("Recent handoffs");
    expect(metric?.value).toBe(1);
    expect(metric?.detail).toBe("1 recent accepted, 1 superseded, 1 dismissed");
  });

  it("surfaces structured review follow-up artifacts in health", () => {
    const project = readyProject();
    const work = {
      id: "work_review",
      project_id: project.id,
      title: "Review cockpit flow",
      status: "review",
      priority: "normal",
      created_at: "2026-06-04T10:00:00Z",
      updated_at: "2026-06-04T10:00:00Z",
    };
    const health = buildProjectHealthSummary(project, null, [work], [], [], {
      artifacts: [
        reviewArtifact({
          id: "art_review_1",
          work_item_id: work.id,
          title: "QA review",
          reviewed_assignment_id: "asgn_impl",
          review_verdict: "changes_requested",
          review_risk: "medium",
          review_follow_up_required: true,
        }),
      ],
    });
    const reviewMetric = projectHealthMetrics(health).find((item) => item.key === "reviews");

    expect(health.reviews).toMatchObject({
      total: 1,
      followUpRequired: 1,
      changesRequested: 1,
    });
    expect(reviewMetric?.value).toBe(1);
    expect(health.attention).toContainEqual(
      expect.objectContaining({
        title: "Review follow-up: Review cockpit flow",
        detail: expect.stringContaining("changes requested"),
        workItemID: work.id,
      }),
    );
  });

  it("surfaces blocked external-agent assignments with chat refs", () => {
    const project = {
      id: "proj_external",
      name: "Project",
      roots: [
        {
          id: "root_1",
          path: "/tmp/project",
          kind: "git",
          active: true,
          created_at: "2026-06-04T10:00:00Z",
          updated_at: "2026-06-04T10:00:00Z",
        },
      ],
      default_provider: "openai",
      default_model: "gpt-4.1",
      context_sources: [],
      created_at: "2026-06-04T10:00:00Z",
      updated_at: "2026-06-04T10:00:00Z",
    } satisfies ProjectRecord;
    const item = activityItem(project.id, []);
    item.assignment.driver_kind = "external_agent";
    item.assignment.execution_ref = { kind: "chat_session", chat_session_id: "chat_failed" };
    item.blocking_signal = "failed";
    item.linked_chat_id = "chat_failed";
    const activity = {
      project_id: project.id,
      summary: {
        work_item_count: 1,
        assignment_count: 1,
        active_count: 0,
        blocked_count: 1,
        completed_count: 0,
        recent_count: 1,
      },
      buckets: {
        active: [],
        blocked: [item],
        completed: [],
        recent: [],
      },
      recent: [],
    } as ProjectActivityData;

    const health = buildProjectHealthSummary(project, activity, [], [], []);
    const attention = health.attention.find((entry) =>
      entry.title.startsWith("External assignment needs review"),
    );

    expect(attention?.chatID).toBe("chat_failed");
    expect(attention?.actionLabel).toBe("View blocked");
  });

  it("marks work closeout ready when assignments and follow-up are complete", () => {
    const readiness = buildProjectWorkCloseoutReadiness({
      workItem: workItemRecord(),
      assignments: [assignmentRecord({ status: "completed" })],
      artifacts: [],
      handoffs: [],
    });

    expect(readiness).toMatchObject({
      ready: true,
      status: "ready",
      completedAssignments: 1,
      assignmentCount: 1,
      blockers: [],
    });
  });

  it("blocks work closeout on active assignments and pending handoffs", () => {
    const readiness = buildProjectWorkCloseoutReadiness({
      workItem: workItemRecord(),
      assignments: [assignmentRecord({ status: "running" })],
      artifacts: [],
      handoffs: [handoff("handoff_pending", "pending")],
    });

    expect(readiness.ready).toBe(false);
    expect(readiness.blockers).toEqual(["1 assignment is still active", "1 handoff is pending"]);
  });

  it("blocks work closeout on unknown assignment states", () => {
    const readiness = buildProjectWorkCloseoutReadiness({
      workItem: workItemRecord(),
      assignments: [assignmentRecord({ status: "stale_unknown" })],
      artifacts: [],
      handoffs: [],
    });

    expect(readiness.ready).toBe(false);
    expect(readiness.blockers).toEqual(["1 assignment is not complete"]);
  });

  it("blocks guided work closeout on failed and cancelled assignments", () => {
    const readiness = buildProjectWorkCloseoutReadiness({
      workItem: workItemRecord(),
      assignments: [
        assignmentRecord({ id: "asgn_failed", status: "failed" }),
        assignmentRecord({ id: "asgn_cancelled", status: "cancelled" }),
      ],
      artifacts: [],
      handoffs: [],
    });

    expect(readiness.ready).toBe(false);
    expect(readiness.blockers).toEqual(["1 assignment failed", "1 assignment was cancelled"]);
  });

  it("shows already-done work as closed without requiring readiness", () => {
    const readiness = buildProjectWorkCloseoutReadiness({
      workItem: workItemRecord({ status: "done" }),
      assignments: [assignmentRecord({ status: "failed" })],
      artifacts: [],
      handoffs: [],
    });

    expect(readiness).toMatchObject({
      ready: false,
      status: "done",
      title: "Work item is done",
      blockers: [],
    });
  });

  it("blocks review follow-up until a linked follow-up assignment is complete", () => {
    const review = reviewArtifact({
      id: "art_review_required",
      review_verdict: "changes_requested",
      review_follow_up_required: true,
    });
    const blocked = buildProjectWorkCloseoutReadiness({
      workItem: workItemRecord(),
      assignments: [assignmentRecord({ id: "asgn_impl", status: "completed" })],
      artifacts: [review],
      handoffs: [],
    });

    expect(blocked.ready).toBe(false);
    expect(blocked.blockers).toContain('Review follow-up "Review" is not triaged');

    const ready = buildProjectWorkCloseoutReadiness({
      workItem: workItemRecord(),
      assignments: [
        assignmentRecord({ id: "asgn_impl", status: "completed" }),
        assignmentRecord({ id: "asgn_followup", status: "completed" }),
      ],
      artifacts: [review],
      handoffs: [
        {
          ...handoff("handoff_review", "accepted"),
          linked_artifact_ids: [review.id],
          target_assignment_id: "asgn_followup",
        },
      ],
    });

    expect(ready.ready).toBe(true);
    expect(ready.blockers).toEqual([]);
  });

  it("shares review follow-up path detection with closeout surfaces", () => {
    const review = reviewArtifact({
      id: "art_review_required",
      review_verdict: "changes_requested",
    });

    expect(reviewArtifactRequiresFollowUp(review)).toBe(true);
    expect(reviewArtifactNeedsFollowUpPath(review, [])).toBe(true);
    expect(
      reviewArtifactNeedsFollowUpPath(review, [
        {
          ...handoff("handoff_review", "pending"),
          linked_artifact_ids: [review.id],
        },
      ]),
    ).toBe(false);
    expect(reviewArtifactRequiresFollowUp(reviewArtifact({ review_verdict: "approved" }))).toBe(
      false,
    );
  });
});

function readyProject(): ProjectRecord {
  return {
    id: "proj_ready",
    name: "Project",
    roots: [
      {
        id: "root_1",
        path: "/tmp/project",
        kind: "git",
        active: true,
        created_at: "2026-06-04T10:00:00Z",
        updated_at: "2026-06-04T10:00:00Z",
      },
    ],
    default_provider: "openai",
    default_model: "gpt-4.1",
    context_sources: [
      {
        id: "ctx_1",
        kind: "workspace_instruction",
        title: "AGENTS.md",
        path: "AGENTS.md",
        enabled: true,
        created_at: "2026-06-04T10:00:00Z",
        updated_at: "2026-06-04T10:00:00Z",
      },
    ],
    created_at: "2026-06-04T10:00:00Z",
    updated_at: "2026-06-04T10:00:00Z",
  };
}

function projectRole(patch: Partial<ProjectWorkRoleRecord> = {}): ProjectWorkRoleRecord {
  return {
    id: "role_1",
    project_id: "proj_ready",
    name: "Developer",
    built_in: false,
    created_at: "2026-06-04T10:00:00Z",
    updated_at: "2026-06-04T10:00:00Z",
    ...patch,
  };
}

function projectSkill(patch: Partial<ProjectSkillRecord> = {}): ProjectSkillRecord {
  return {
    id: "backend",
    project_id: "proj_ready",
    title: "Backend",
    description: "Backend project skill.",
    path: ".hecate/skills/backend/SKILL.md",
    root_id: "root_1",
    format: "skill_md",
    enabled: true,
    status: "available",
    trust_label: "workspace_skill",
    source_context_source_ids: ["ctx_1"],
    warnings: [],
    discovered_at: "2026-06-04T10:00:00Z",
    created_at: "2026-06-04T10:00:00Z",
    updated_at: "2026-06-04T10:00:00Z",
    ...patch,
  };
}

function reviewArtifact(
  patch: Partial<ProjectCollaborationArtifactRecord> = {},
): ProjectCollaborationArtifactRecord {
  return {
    id: "art_review",
    project_id: "proj_ready",
    work_item_id: "work_1",
    assignment_id: "asgn_review",
    kind: "review",
    title: "Review",
    body: "Verdict: Changes requested",
    author_role_id: "reviewer_qa",
    created_at: "2026-06-04T10:00:00Z",
    updated_at: "2026-06-04T10:00:00Z",
    ...patch,
  };
}

function workItemRecord(patch: Partial<ProjectWorkItemRecord> = {}): ProjectWorkItemRecord {
  return {
    id: "work_1",
    project_id: "proj_ready",
    title: "Coordinate release",
    status: "review",
    priority: "normal",
    created_at: "2026-06-04T10:00:00Z",
    updated_at: "2026-06-04T10:00:00Z",
    ...patch,
  };
}

function assignmentRecord(patch: Partial<ProjectAssignmentRecord> = {}): ProjectAssignmentRecord {
  return {
    id: "asgn_1",
    project_id: "proj_ready",
    work_item_id: "work_1",
    role_id: "role_1",
    status: "queued",
    driver_kind: "hecate_task",
    created_at: "2026-06-04T10:00:00Z",
    updated_at: "2026-06-04T10:00:00Z",
    ...patch,
  };
}

function agentProfile(id: string): AgentProfileRecord {
  return {
    id,
    name: id,
    surface: "any",
    tools_enabled: true,
    writes_allowed: true,
    network_allowed: false,
    approval_policy: "inherit",
    project_memory_policy: "inherit",
    context_source_policy: "inherit",
    skill_ids: [],
  };
}

function activityItem(
  projectID: string,
  recentHandoffs: ProjectHandoffRecord[],
): ProjectActivityItemRecord {
  return {
    id: "asgn_1",
    project_id: projectID,
    work_item: {
      id: "work_1",
      title: "Coordinate release",
      status: "running",
      priority: "normal",
    },
    assignment: {
      id: "asgn_1",
      project_id: projectID,
      work_item_id: "work_1",
      role_id: "role_1",
      status: "running",
      driver_kind: "hecate_task",
      created_at: "2026-06-04T10:00:00Z",
      updated_at: "2026-06-04T10:00:00Z",
    },
    role: {
      id: "role_1",
      project_id: projectID,
      name: "Developer",
      built_in: false,
      created_at: "2026-06-04T10:00:00Z",
      updated_at: "2026-06-04T10:00:00Z",
    },
    status: "running",
    blocking_signal: "running",
    status_summary: "work recently updated",
    artifact_summary: { count: 0 },
    handoff_summary: {
      count: 12,
      pending_count: 8,
      accepted_count: 4,
    },
    recent_handoffs: recentHandoffs,
    updated_at: "2026-06-04T10:00:00Z",
  };
}

function handoff(id: string, status: ProjectHandoffRecord["status"]): ProjectHandoffRecord {
  return {
    id,
    project_id: "proj_1",
    work_item_id: "work_1",
    title: id,
    summary: "Handoff summary",
    recommended_next_action: "Review it.",
    status,
    provenance_kind: "operator",
    trust_label: "operator_reviewed",
    created_at: "2026-06-04T10:00:00Z",
    updated_at: "2026-06-04T10:00:00Z",
    status_changed_at: "2026-06-04T10:00:00Z",
  };
}
