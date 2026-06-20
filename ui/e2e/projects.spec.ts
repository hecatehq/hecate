import type { Page, Route } from "@playwright/test";
import { expect, mockGatewayAPIs, MOCK_SETTINGS_CONFIG_WITH_PROVIDERS, test } from "./fixtures";
import type {
  ProjectActivityData,
  ProjectAssignmentRecord,
  ProjectCollaborationArtifactRecord,
  ProjectContextSourceRecord,
  ProjectHealth,
  ProjectMemoryCandidateRecord,
  ProjectRecord,
  ProjectSetupReadiness,
  ProjectSkillRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../src/types/project";

const NOW = "2026-06-14T10:10:57Z";

test("Projects journey: setup, first work, assignment, evidence, closeout", async ({ page }) => {
  const state = await mockProjectJourneyAPIs(page);
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.workspace", "projects");
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await expect(page.getByText("Add a project to begin")).toBeVisible();
  await page.getByRole("button", { name: "Add" }).click();
  await page.getByLabel("Name").fill("Launch operations");
  await page
    .getByLabel("Purpose")
    .fill("Coordinate launch readiness, evidence, and review follow-up.");
  await page.getByRole("button", { name: "Create project" }).click();

  await expect(page.getByText("Set up Launch operations")).toBeVisible();
  await page
    .getByRole("region", { name: "Project onboarding" })
    .getByRole("button", { name: "Set up project" })
    .first()
    .click();
  await expect(page.getByText("Bootstrap Launch operations guidance")).toBeVisible();
  await page.getByRole("button", { name: "Apply proposal" }).click();

  await expect(page.getByText("Applied 3 actions")).toBeVisible();
  await page.getByRole("button", { name: "Dismiss" }).click();
  await expect(page.getByText("Setup ready")).toBeVisible();
  await expect(page.getByRole("button", { name: "Create first work" })).toBeVisible();
  await page.getByRole("button", { name: "Create first work" }).click();
  await page.getByLabel("Title").fill("Verify launch checklist");
  await page
    .getByLabel("Brief")
    .fill("Confirm evidence is captured and the first assignment can be closed.");
  await page.getByRole("button", { name: "Create work item" }).click();

  await expect(page.getByRole("heading", { name: "Verify launch checklist" })).toBeVisible();
  await page.getByRole("button", { name: "Prepare next step" }).click();
  await expect(
    page.getByText("Queue the implementation role for the selected work item."),
  ).toBeVisible();
  await page.getByRole("button", { name: "Apply proposal" }).click();

  await expect(page.getByRole("button", { name: "Start" })).toBeVisible();
  await page.getByRole("button", { name: "Start" }).click();
  await expect(page.getByRole("dialog", { name: /launch preflight/i })).toBeVisible();
  await expect(page.getByText("Launch readiness")).toBeVisible();
  await page.getByRole("button", { name: "Start assignment" }).click();

  await expect(page.getByText("completed", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Add evidence" }).click();
  await page
    .getByRole("dialog", { name: "Record evidence" })
    .getByLabel("Title")
    .fill("Launch checklist");
  await page.getByLabel("URL").fill("https://example.test/checklist");
  await page.getByLabel("Summary").fill("Operator confirmed the launch checklist evidence.");
  await page.getByRole("button", { name: "Record evidence" }).click();

  await expect(page.getByText("Launch checklist", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Mark done" }).click();
  await expect(page.getByRole("article", { name: /Verify launch checklist/ })).toContainText(
    "done",
  );

  expect(state.projects).toHaveLength(1);
  expect(state.roles).toHaveLength(1);
  expect(state.workItems[0]?.status).toBe("done");
  expect(state.assignments[0]?.status).toBe("completed");
  expect(state.artifacts).toHaveLength(1);
});

test("Projects rootless journey: plan work without setup or workspace", async ({ page }) => {
  const state = await mockProjectJourneyAPIs(page);
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.workspace", "projects");
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await page.getByRole("button", { name: "Add" }).click();
  await page.getByLabel("Name").fill("Research notes");
  await page.getByLabel("Purpose").fill("Coordinate interview synthesis and writing tasks.");
  await expect(page.getByLabel("Folder path")).toHaveCount(0);
  await page.getByRole("button", { name: "Create project" }).click();

  await expect(page.getByText("Set up Research notes")).toBeVisible();
  await expect(
    page.getByText("Optional; attach files when this project needs them."),
  ).toBeVisible();
  const onboarding = page.getByRole("region", { name: "Project onboarding" });
  await onboarding.getByRole("button", { name: "Create work" }).click();

  await page.getByLabel("Title").fill("Summarize interview themes");
  await page
    .getByLabel("Brief")
    .fill("Turn interview notes into a reviewable theme summary for the next planning pass.");
  await expect(page.getByLabel("Owner role")).toHaveValue("");
  await page.getByRole("button", { name: "Create work item" }).click();

  await expect(page.getByRole("heading", { name: "Summarize interview themes" })).toBeVisible();
  await expect(page.getByText("Add a role before assigning work")).toBeVisible();
  await page.getByText("Add manually").click();
  const manualActions = page.getByRole("group", { name: "Manual work item actions" });
  await manualActions.getByRole("button", { name: "Evidence" }).click();
  await page
    .getByRole("dialog", { name: "Record evidence" })
    .getByLabel("Title")
    .fill("Interview source notes");
  await page.getByLabel("URL").fill("https://example.test/interviews");
  await page.getByLabel("Summary").fill("Source notes reviewed for the theme summary.");
  await page.getByRole("button", { name: "Record evidence" }).click();

  await expect(page.getByText("Interview source notes", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Mark done" }).click();
  await expect(page.getByRole("article", { name: /Summarize interview themes/ })).toContainText(
    "done",
  );

  expect(state.projects[0]?.roots).toHaveLength(0);
  expect(state.roles).toHaveLength(0);
  expect(state.workItems[0]?.owner_role_id).toBe("");
  expect(state.workItems[0]?.status).toBe("done");
  expect(state.artifacts).toHaveLength(1);
});

async function mockProjectJourneyAPIs(page: Page) {
  const state = {
    projects: [] as ProjectRecord[],
    sources: [] as ProjectContextSourceRecord[],
    skills: [] as ProjectSkillRecord[],
    roles: [] as ProjectWorkRoleRecord[],
    memoryCandidates: [] as ProjectMemoryCandidateRecord[],
    workItems: [] as ProjectWorkItemRecord[],
    assignments: [] as ProjectAssignmentRecord[],
    artifacts: [] as ProjectCollaborationArtifactRecord[],
  };
  const ok = (body: unknown, status = 200) => ({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });

  await page.unrouteAll({ behavior: "ignoreErrors" });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.authToken", "e2e-test-token");
  });
  await mockGatewayAPIs(page, {
    projects: [],
    settingsConfig: MOCK_SETTINGS_CONFIG_WITH_PROVIDERS,
  });

  await page.route(/\/hecate\/v1\/agent-profiles(?:\?.*)?$/, (route) =>
    route.fulfill(ok({ object: "agent_profiles", data: [] })),
  );

  await page.route(/\/hecate\/v1\/project-assistant\/(?:draft|context|apply)$/, async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path.endsWith("/context")) {
      await route.fulfill(
        ok({
          object: "project_assistant_context",
          data: projectAssistantContext(state),
        }),
      );
      return;
    }
    if (path.endsWith("/draft")) {
      const body = JSON.parse(request.postData() || "{}") as {
        draft_mode?: string;
        project_id?: string;
        work_item_id?: string;
      };
      const proposal =
        body.draft_mode === "bootstrap"
          ? bootstrapProposal(state.projects[0])
          : assignmentProposal(state.projects[0], body.work_item_id || state.workItems[0]?.id);
      await route.fulfill(ok({ object: "project_assistant_proposal", data: proposal }));
      return;
    }
    if (path.endsWith("/apply")) {
      const body = JSON.parse(request.postData() || "{}") as { proposal?: { id?: string } };
      if (body.proposal?.id === "pa_setup") {
        applySetup(state);
        await route.fulfill(
          ok({
            object: "project_assistant_apply_result",
            data: {
              proposal_id: "pa_setup",
              applied: true,
              actions: [
                { kind: "set_project_defaults" },
                {
                  kind: "create_memory_candidate",
                  data: { memory_candidate_id: "memcand_launch" },
                },
                { kind: "create_role", data: { role_id: "implementation" } },
              ],
            },
          }),
        );
        return;
      }
      const assignment = applyAssignment(state);
      await route.fulfill(
        ok({
          object: "project_assistant_apply_result",
          data: {
            proposal_id: "pa_assignment",
            applied: true,
            actions: [{ kind: "create_assignment", data: { assignment_id: assignment.id } }],
          },
        }),
      );
      return;
    }
    await route.fallback();
  });

  await page.route(/\/hecate\/v1\/projects(?:\/.*)?(?:\?.*)?$/, async (route) => {
    const request = route.request();
    const method = request.method();
    const url = new URL(request.url());
    const parts = url.pathname
      .replace(/^\/hecate\/v1\/projects\/?/, "")
      .split("/")
      .filter(Boolean)
      .map(decodeURIComponent);

    if (parts.length === 0) {
      if (method === "GET") {
        await route.fulfill(ok({ object: "projects", data: state.projects }));
        return;
      }
      if (method === "POST") {
        const body = JSON.parse(request.postData() || "{}") as {
          name?: string;
          description?: string;
        };
        const project: ProjectRecord = {
          id: "proj_launch",
          name: body.name || "Launch operations",
          description: body.description || "",
          roots: [],
          context_sources: [],
          created_at: NOW,
          updated_at: NOW,
        };
        state.projects = [project];
        await route.fulfill(ok({ object: "project", data: project }, 201));
        return;
      }
    }

    const projectID = parts[0] || "";
    if (!projectID || projectID !== state.projects[0]?.id) {
      await route.fulfill(ok({ object: "projects", data: state.projects }));
      return;
    }

    const resource = parts[1];
    if (resource === "context-sources" && parts[2] === "discover" && method === "POST") {
      state.sources = [
        {
          id: "ctx_agents",
          kind: "workspace_instruction",
          title: "AGENTS.md",
          path: "AGENTS.md",
          enabled: true,
          format: "agents_md",
          scope: "workspace",
          trust_label: "workspace_guidance",
          created_at: NOW,
          updated_at: NOW,
        },
      ];
      state.projects[0] = { ...state.projects[0], context_sources: state.sources, updated_at: NOW };
      await route.fulfill(ok({ object: "project", data: state.projects[0] }));
      return;
    }
    if (resource === "skills") {
      if (parts[2] === "discover" && method === "POST") {
        state.skills = [
          {
            id: "implementation",
            project_id: projectID,
            title: "Implementation",
            description: "Build and verify changes.",
            path: "docs-ai/skills/backend/SKILL.md",
            format: "skill_md",
            enabled: true,
            status: "available",
            trust_label: "workspace_skill",
            source_context_source_ids: ["ctx_agents"],
            discovered_at: NOW,
            created_at: NOW,
            updated_at: NOW,
          },
        ];
      }
      await route.fulfill(ok({ object: "project_skills", data: state.skills }));
      return;
    }
    if (resource === "memory") {
      if (parts[2] === "candidates") {
        await route.fulfill(
          ok({ object: "project_memory_candidates", data: state.memoryCandidates }),
        );
        return;
      }
      await route.fulfill(ok({ object: "project_memory", data: [] }));
      return;
    }
    if (resource === "roles") {
      await route.fulfill(ok({ object: "project_roles", data: state.roles }));
      return;
    }
    if (resource === "activity") {
      await route.fulfill(ok({ object: "project_activity", data: projectActivity(state) }));
      return;
    }
    if (resource === "health") {
      await route.fulfill(ok({ object: "project_health", data: projectHealth(state, projectID) }));
      return;
    }
    if (resource === "setup-readiness") {
      await route.fulfill(
        ok({ object: "project_setup_readiness", data: projectSetupReadiness(state, projectID) }),
      );
      return;
    }
    if (resource === "operations" && parts[2] === "brief") {
      await route.fulfill(
        ok({ object: "project_operations_brief", data: projectOperationsBrief(projectID) }),
      );
      return;
    }
    if (resource === "work-items") {
      await handleWorkItemRoute(route, state, parts, method, projectID, ok);
      return;
    }

    await route.fulfill(ok({ object: "project", data: state.projects[0] }));
  });

  return state;
}

async function handleWorkItemRoute(
  route: Route,
  state: ProjectJourneyState,
  parts: string[],
  method: string,
  projectID: string,
  ok: (body: unknown, status?: number) => { status: number; contentType: string; body: string },
) {
  const request = route.request();
  const workItemID = parts[2] || "";
  const subresource = parts[3];

  if (!workItemID) {
    if (method === "GET") {
      await route.fulfill(ok({ object: "project_work_items", data: state.workItems }));
      return;
    }
    if (method === "POST") {
      const body = JSON.parse(request.postData() || "{}") as {
        title?: string;
        brief?: string;
        priority?: string;
        owner_role_id?: string;
      };
      const item: ProjectWorkItemRecord = {
        id: "work_launch",
        project_id: projectID,
        title: body.title || "Verify launch checklist",
        brief: body.brief || "",
        status: "ready",
        priority: body.priority || "normal",
        owner_role_id: body.owner_role_id || state.roles[0]?.id || "",
        created_at: NOW,
        updated_at: NOW,
      };
      state.workItems = [item];
      await route.fulfill(ok({ object: "project_work_item", data: item }, 201));
      return;
    }
  }

  const item = state.workItems.find((candidate) => candidate.id === workItemID);
  if (!item) {
    await route.fulfill(ok({ object: "project_work_item", data: null }, 404));
    return;
  }

  if (!subresource) {
    if (method === "GET") {
      await route.fulfill(ok({ object: "project_work_item", data: item }));
      return;
    }
    if (method === "PATCH") {
      const patch = JSON.parse(request.postData() || "{}") as Partial<ProjectWorkItemRecord>;
      Object.assign(item, patch, { updated_at: NOW });
      await route.fulfill(ok({ object: "project_work_item", data: item }));
      return;
    }
  }

  if (subresource === "readiness" && method === "GET") {
    await route.fulfill(
      ok({
        object: "project_work_item_readiness",
        data: projectWorkItemReadiness(state, item),
      }),
    );
    return;
  }

  if (subresource === "assignments") {
    const assignmentID = parts[4] || "";
    if (!assignmentID) {
      await route.fulfill(ok({ object: "project_assignments", data: state.assignments }));
      return;
    }
    const assignment = state.assignments.find((candidate) => candidate.id === assignmentID);
    if (parts[5] === "preflight") {
      await route.fulfill(
        ok({
          object: "context_packet",
          data: assignmentPreflight(projectID, workItemID, assignmentID),
        }),
      );
      return;
    }
    if (parts[5] === "start" && method === "POST" && assignment) {
      Object.assign(assignment, {
        status: "completed",
        started_at: NOW,
        completed_at: NOW,
        updated_at: NOW,
        execution_ref: {
          kind: "task_run",
          task_id: "task_launch",
          run_id: "run_launch",
          status: "completed",
          trace_id: "trace_launch",
        },
        execution: {
          task_id: "task_launch",
          run_id: "run_launch",
          status: "completed",
          provider: "anthropic",
          model: "claude-sonnet-4-6",
          trace_id: "trace_launch",
        },
      });
      await route.fulfill(ok({ object: "project_assignment", data: assignment }));
      return;
    }
    await route.fulfill(ok({ object: "project_assignment", data: assignment ?? null }));
    return;
  }

  if (subresource === "artifacts") {
    if (method === "GET") {
      await route.fulfill(ok({ object: "project_collaboration_artifacts", data: state.artifacts }));
      return;
    }
    if (method === "POST") {
      const body = JSON.parse(
        request.postData() || "{}",
      ) as Partial<ProjectCollaborationArtifactRecord>;
      const artifact: ProjectCollaborationArtifactRecord = {
        id: "artifact_launch",
        project_id: projectID,
        work_item_id: workItemID,
        assignment_id: body.assignment_id,
        kind: body.kind || "evidence_link",
        title: body.title || "Launch checklist",
        body: body.body || "Operator confirmed the launch checklist evidence.",
        evidence_source_kind: body.evidence_source_kind,
        evidence_url: body.evidence_url,
        evidence_provider: body.evidence_provider,
        evidence_trust_label: body.evidence_trust_label,
        created_at: NOW,
        updated_at: NOW,
      };
      state.artifacts = [artifact];
      await route.fulfill(ok({ object: "project_collaboration_artifact", data: artifact }, 201));
      return;
    }
  }

  if (subresource === "handoffs") {
    await route.fulfill(ok({ object: "project_handoffs", data: [] }));
    return;
  }

  await route.fallback();
}

type ProjectJourneyState = Awaited<ReturnType<typeof mockProjectJourneyAPIs>>;

function applySetup(state: ProjectJourneyState) {
  state.projects[0] = {
    ...state.projects[0],
    default_provider: "anthropic",
    default_model: "claude-sonnet-4-6",
    default_agent_profile: "implementation",
    default_workspace_mode: "in_place",
    updated_at: NOW,
  };
  state.roles = [
    {
      id: "implementation",
      project_id: state.projects[0].id,
      name: "Implementation",
      description: "Build and verify the next project change.",
      default_driver_kind: "hecate_task",
      default_provider: "anthropic",
      default_model: "claude-sonnet-4-6",
      default_agent_profile: "implementation",
      skill_ids: ["implementation"],
      built_in: false,
      created_at: NOW,
      updated_at: NOW,
    },
  ];
  state.memoryCandidates = [
    {
      id: "memcand_launch",
      project_id: state.projects[0].id,
      title: "Guidance source: AGENTS.md",
      body: "Review project guidance before promoting durable memory.",
      suggested_trust_label: "workspace_guidance",
      suggested_source_kind: "context_source",
      suggested_source_id: "ctx_agents",
      status: "pending",
      created_at: NOW,
      updated_at: NOW,
    },
  ];
}

function applyAssignment(state: ProjectJourneyState): ProjectAssignmentRecord {
  const assignment: ProjectAssignmentRecord = {
    id: "assign_launch",
    project_id: state.projects[0].id,
    work_item_id: state.workItems[0].id,
    role_id: state.roles[0].id,
    driver_kind: "hecate_task",
    status: "queued",
    created_at: NOW,
    updated_at: NOW,
  };
  state.assignments = [assignment];
  return assignment;
}

function bootstrapProposal(project?: ProjectRecord) {
  return {
    id: "pa_setup",
    title: `Bootstrap ${project?.name || "project"} guidance`,
    summary: "Prepare setup defaults, memory candidates, and roles for review.",
    requires_confirmation: true,
    actions: [
      { kind: "set_project_defaults", patch: { default_provider: "anthropic" } },
      { kind: "create_memory_candidate", target: { project_id: project?.id || "" } },
      { kind: "create_role", patch: { id: "implementation", name: "Implementation" } },
    ],
  };
}

function assignmentProposal(project?: ProjectRecord, workItemID?: string) {
  return {
    id: "pa_assignment",
    title: "Create assignment",
    summary: "Queue the implementation role for the selected work item.",
    requires_confirmation: true,
    actions: [
      {
        kind: "create_assignment",
        target: { project_id: project?.id || "", work_item_id: workItemID || "" },
        patch: { role_id: "implementation", driver_kind: "hecate_task" },
      },
    ],
  };
}

function projectAssistantContext(state: ProjectJourneyState) {
  return {
    project: state.projects[0],
    request: "Set up project guidance",
    roles: state.roles,
    skills: state.skills,
    memory: [],
    memory_candidates: state.memoryCandidates,
    recent_activity: [],
    budget: {
      memory_body_max_bytes: 12288,
      memory_candidate_body_max_bytes: 12288,
      body_original_bytes: 0,
      body_returned_bytes: 0,
      body_tokens_estimate: 0,
      body_truncated_count: 0,
    },
    selection: {
      driver_kind: "hecate_task",
      driver_source: "fallback",
      reason: "E2E setup context.",
    },
  };
}

function assignmentPreflight(projectID: string, workItemID: string, assignmentID: string) {
  return {
    id: "ctx_launch",
    version: "project_assignment_launch.v1",
    provider: "anthropic",
    model: "claude-sonnet-4-6",
    refs: { project_id: projectID, work_item_id: workItemID, assignment_id: assignmentID },
    items: [
      {
        section: "runtime_evidence",
        kind: "launch_readiness",
        trust_level: "system",
        origin: "hecate",
        title: "Launch readiness",
        body: "Provider: anthropic\nModel: claude-sonnet-4-6\nReady: true",
        included: true,
      },
    ],
  };
}

function projectWorkItemReadiness(state: ProjectJourneyState, workItem: ProjectWorkItemRecord) {
  const assignments = state.assignments.filter(
    (assignment) => assignment.work_item_id === workItem.id,
  );
  const artifacts = state.artifacts.filter((artifact) => artifact.work_item_id === workItem.id);
  const completedAssignments = assignments.filter(
    (assignment) => (assignment.execution_ref?.status || assignment.status) === "completed",
  );
  const blockers: string[] = [];
  const warnings: string[] = [];
  if (workItem.status === "done") {
    return {
      project_id: workItem.project_id,
      work_item_id: workItem.id,
      ready: false,
      status: "done",
      title: "Work item is done",
      detail: "This work item has already been marked done by the operator.",
      blockers,
      warnings,
      assignment_count: assignments.length,
      completed_assignments: completedAssignments.length,
      review_follow_up_count: 0,
    };
  }

  const activeAssignments = assignments.filter((assignment) =>
    ["queued", "running", "awaiting_approval"].includes(
      assignment.execution_ref?.status || assignment.status,
    ),
  ).length;
  const missingEvidenceAssignments = completedAssignments.filter(
    (assignment) =>
      !artifacts.some(
        (artifact) =>
          artifact.kind === "evidence_link" &&
          (!artifact.assignment_id || artifact.assignment_id === assignment.id),
      ),
  );
  if (activeAssignments > 0) {
    blockers.push(
      `${activeAssignments} assignment${activeAssignments === 1 ? " is" : "s are"} still active`,
    );
  }
  if (missingEvidenceAssignments.length > 0) {
    blockers.push(
      `${missingEvidenceAssignments.length} completed assignment${
        missingEvidenceAssignments.length === 1 ? " is" : "s are"
      } missing evidence`,
    );
  }
  if (assignments.length === 0) {
    warnings.push("No assignments are linked to this work item; closeout is manual.");
  }

  const ready = blockers.length === 0;
  return {
    project_id: workItem.project_id,
    work_item_id: workItem.id,
    ready,
    status: ready ? "ready" : "blocked",
    title: ready ? "Ready to mark done" : "Closeout is blocked",
    detail: ready
      ? "Assignments, evidence, handoffs, and review follow-up are clear. The operator can mark this work item done."
      : "Resolve the listed assignment, evidence, handoff, or review follow-up items before marking this work done.",
    blockers,
    warnings,
    assignment_count: assignments.length,
    completed_assignments: completedAssignments.length,
    review_follow_up_count: 0,
    missing_evidence_assignment_ids: missingEvidenceAssignments.map((assignment) => assignment.id),
  };
}

function projectActivity(state: ProjectJourneyState): ProjectActivityData {
  const assignment = state.assignments[0];
  const role = state.roles[0];
  const workItem = state.workItems[0];
  const completed = assignment?.status === "completed";
  const item =
    assignment && role && workItem
      ? [
          {
            id: assignment.id,
            project_id: state.projects[0].id,
            work_item: {
              id: workItem.id,
              title: workItem.title,
              status: workItem.status,
              priority: workItem.priority,
            },
            assignment,
            role,
            status: assignment.status,
            blocking_signal: completed ? "completed" : "not_started",
            status_summary: completed ? "completed" : "queued",
            linked_task_id: assignment.execution_ref?.task_id,
            linked_run_id: assignment.execution_ref?.run_id,
            artifact_summary: {
              count: state.artifacts.length,
              latest_kind: state.artifacts[0]?.kind,
              latest_title: state.artifacts[0]?.title,
              latest_at: state.artifacts[0]?.updated_at,
            },
            handoff_summary: { count: 0 },
            recent_artifacts: state.artifacts,
            updated_at: assignment.updated_at,
          },
        ]
      : [];
  return {
    project_id: state.projects[0]?.id || "proj_launch",
    summary: {
      work_item_count: state.workItems.length,
      assignment_count: state.assignments.length,
      active_count: completed || !assignment ? 0 : 1,
      blocked_count: 0,
      completed_count: completed ? 1 : 0,
      recent_count: item.length,
    },
    buckets: {
      active: completed ? [] : item,
      blocked: [],
      completed: completed ? item : [],
      recent: item,
    },
    recent: item,
  };
}

function projectHealth(state: ProjectJourneyState, projectID: string): ProjectHealth {
  const enabledContextSourceCount = state.sources.filter((source) => source.enabled).length;
  const pendingMemoryCandidateCount = state.memoryCandidates.filter(
    (candidate) => candidate.status === "pending",
  ).length;
  return {
    project_id: projectID,
    generated_at: NOW,
    summary: {
      attention_count: 0,
      available_attention_count: 0,
      omitted_attention_count: 0,
      attention_limit: 5,
      missing_defaults: !(state.projects[0]?.default_provider && state.projects[0]?.default_model),
      missing_project_root: !(state.projects[0]?.roots ?? []).some((root) => root.active),
      enabled_memory_count: 0,
      saved_memory_count: 0,
      enabled_context_source_count: enabledContextSourceCount,
      pending_memory_candidate_count: pendingMemoryCandidateCount,
      promoted_memory_candidate_count: 0,
      rejected_memory_candidate_count: 0,
      pending_handoff_count: 0,
      accepted_handoff_count: 0,
      superseded_handoff_count: 0,
      dismissed_handoff_count: 0,
      review_follow_up_count: 0,
      blocked_review_count: 0,
      changes_requested_review_count: 0,
      stale_or_unknown_assignment_count: 0,
    },
    attention: [],
  };
}

function projectSetupReadiness(
  state: ProjectJourneyState,
  projectID: string,
): ProjectSetupReadiness {
  const project = state.projects.find((item) => item.id === projectID) ?? state.projects[0];
  const enabledContextSourceCount = state.sources.filter((source) => source.enabled).length;
  const pendingMemoryCandidateCount = state.memoryCandidates.filter(
    (candidate) => candidate.status === "pending",
  ).length;
  const roleCount = state.roles.filter((role) => !role.built_in).length;
  const skillCount = state.skills.length;
  const workItemCount = state.workItems.filter((item) => item.project_id === projectID).length;
  const hasActiveRoot = Boolean(project?.roots?.some((root) => root.active && root.path));
  const missingDefaults = !(project?.default_provider && project?.default_model);
  const setupStarted =
    enabledContextSourceCount > 0 ||
    roleCount > 0 ||
    skillCount > 0 ||
    pendingMemoryCandidateCount > 0;
  return {
    project_id: projectID,
    generated_at: NOW,
    show_onboarding: workItemCount === 0 && !setupStarted,
    setup_started: setupStarted,
    first_work_ready: workItemCount === 0 && setupStarted,
    summary: {
      work_item_count: workItemCount,
      role_count: roleCount,
      skill_count: skillCount,
      enabled_context_source_count: enabledContextSourceCount,
      saved_memory_count: 0,
      pending_memory_candidate_count: pendingMemoryCandidateCount,
      has_purpose: Boolean(project?.description?.trim()),
      has_active_root: hasActiveRoot,
      missing_defaults: missingDefaults,
    },
    primary_action: {
      type: "bootstrap_project",
      project_id: projectID,
      label: "Set up project",
    },
    checks: [
      {
        id: "purpose",
        label: "Project purpose",
        detail: project?.description || "Add a short purpose.",
        status: project?.description?.trim() ? "ready" : "todo",
        action: project?.description?.trim()
          ? undefined
          : { type: "open_project_settings", project_id: projectID, label: "Add purpose" },
      },
      {
        id: "workspace_source",
        label: "Workspace source",
        detail:
          project?.roots?.find((root) => root.active && root.path)?.path ||
          "Optional; attach files when this project needs them.",
        status: hasActiveRoot ? "ready" : "optional",
        optional: !hasActiveRoot,
      },
      {
        id: "launch_defaults",
        label: "Provider and model",
        detail: missingDefaults
          ? "Not set"
          : `${project?.default_provider} / ${project?.default_model}`,
        status: missingDefaults ? "todo" : "ready",
        action: missingDefaults
          ? { type: "open_project_settings", project_id: projectID, label: "Set defaults" }
          : undefined,
      },
      {
        id: "sources_memory",
        label: "Sources and memory",
        detail:
          enabledContextSourceCount > 0 || pendingMemoryCandidateCount > 0 || skillCount > 0
            ? `${enabledContextSourceCount} source(s), ${pendingMemoryCandidateCount} memory candidate(s), ${skillCount} skill(s)`
            : "Attach a workspace when files matter, or add sources later.",
        status:
          enabledContextSourceCount > 0 || pendingMemoryCandidateCount > 0 || skillCount > 0
            ? "ready"
            : "todo",
        action:
          enabledContextSourceCount > 0 || pendingMemoryCandidateCount > 0 || skillCount > 0
            ? undefined
            : { type: "bootstrap_project", project_id: projectID, label: "Set up project" },
      },
      {
        id: "roles",
        label: "Roles",
        detail:
          roleCount > 0
            ? `${roleCount} role(s) configured.`
            : "Set up project can suggest roles from skills.",
        status: roleCount > 0 ? "ready" : "todo",
        action:
          roleCount > 0
            ? undefined
            : { type: "bootstrap_project", project_id: projectID, label: "Set up project" },
      },
      {
        id: "first_work_item",
        label: "First work item",
        detail:
          workItemCount > 0
            ? `${workItemCount} work item(s) created.`
            : "Create the first reviewable task after setup.",
        status: workItemCount > 0 ? "ready" : "todo",
        action:
          workItemCount > 0
            ? undefined
            : { type: "create_work_item", project_id: projectID, label: "Create work" },
      },
    ],
  };
}

function projectOperationsBrief(projectID: string) {
  return {
    project_id: projectID,
    generated_at: NOW,
    summary: {
      item_count: 0,
      high_count: 0,
      medium_count: 0,
      low_count: 0,
      pending_memory_candidate_count: 0,
      pending_handoff_count: 0,
    },
    items: [],
  };
}
