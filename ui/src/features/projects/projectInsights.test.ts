import { describe, expect, it } from "vitest";

import { buildProjectHealthSummary, projectHealthMetrics } from "./projectInsights";
import type {
  ProjectActivityData,
  ProjectActivityItemRecord,
  ProjectHandoffRecord,
  ProjectRecord,
} from "../../types/project";

describe("projectInsights", () => {
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
      "handoffs",
      "stale",
    ]);
    expect(metric?.label).toBe("Recent handoffs");
    expect(metric?.value).toBe(1);
    expect(metric?.detail).toBe("1 recent accepted, 1 superseded, 1 dismissed");
  });
});

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
