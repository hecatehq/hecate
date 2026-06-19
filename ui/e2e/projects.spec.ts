import type { Page, Route } from "@playwright/test";
import { expect, mockGatewayAPIs, MOCK_SETTINGS_CONFIG_WITH_PROVIDERS, test } from "./fixtures";
import type {
  ProjectActivityData,
  ProjectAssignmentRecord,
  ProjectCollaborationArtifactRecord,
  ProjectContextSourceRecord,
  ProjectMemoryCandidateRecord,
  ProjectRecord,
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
  await page.getByRole("button", { name: "Set up project" }).click();
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
