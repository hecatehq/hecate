import type { Page, Route } from "@playwright/test";
import { expect, mockGatewayAPIs, MOCK_SETTINGS_CONFIG_WITH_PROVIDERS, test } from "./fixtures";
import type {
  ProjectActivityData,
  ProjectAssignmentLaunchReadinessRecord,
  ProjectAssignmentRecord,
  ProjectCollaborationArtifactRecord,
  ProjectContextSourcePayload,
  ProjectContextSourceRecord,
  ProjectHealth,
  ProjectMemoryCandidateRecord,
  ProjectOperationsBriefItem,
  ProjectRecord,
  ProjectRootPayload,
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
  await expect(page.getByRole("region", { name: "Project onboarding" })).toHaveCount(0);
  await expect(page.getByRole("region", { name: "Project Assistant" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Set up project" })).toHaveCount(0);
  await page.getByRole("tab", { name: /Memory/ }).click();
  await expect(page.getByText("Guidance source: AGENTS.md")).toBeVisible();
  await page
    .getByRole("button", { name: "Reject memory candidate Guidance source: AGENTS.md" })
    .click();
  await expect(
    page.getByRole("button", { name: "Reject memory candidate Guidance source: AGENTS.md" }),
  ).toHaveCount(0);
  await page.getByRole("tab", { name: /Work/ }).click();
  await expect(page.getByRole("button", { name: "Create first work" })).toBeVisible();
  await page.getByRole("button", { name: "Create first work" }).click();
  await page.getByLabel("Title").fill("Verify launch checklist");
  await page
    .getByLabel("Brief")
    .fill("Confirm evidence is captured and the first assignment can be closed.");
  await page.getByRole("button", { name: "Create work item" }).click();

  await expect(page.getByRole("heading", { name: "Verify launch checklist" })).toBeVisible();
  await page.getByRole("tab", { name: "Overview" }).click();
  await expect(page.getByText("Prepare first assignment: Verify launch checklist")).toBeVisible();
  await page.getByRole("tab", { name: /Work/ }).click();
  await page.getByRole("button", { name: "Prepare next step" }).click();
  await expect(
    page.getByText("Queue the implementation role for the selected work item."),
  ).toBeVisible();
  await page.getByRole("button", { name: "Apply proposal" }).click();

  await page.getByRole("tab", { name: "Overview" }).click();
  await expect(page.getByText("Review queued assignment: Verify launch checklist")).toBeVisible();
  await page.getByRole("button", { name: /Review start/ }).click();
  const preflight = page.getByRole("dialog", { name: /launch preflight/i });
  await expect(preflight).toBeVisible();
  await expect(preflight.getByText("Launch readiness", { exact: true })).toBeVisible();
  await preflight.getByRole("button", { name: "Start assignment" }).click();

  const executionStory = page.getByRole("article", { name: /assignment execution/i });
  await expect(executionStory).toBeVisible();
  await expect(
    executionStory.locator("header").getByText("approval", { exact: true }),
  ).toBeVisible();
  await expect(executionStory.getByRole("button", { name: "Review in task" })).toBeVisible();
  await expect(executionStory.getByText("Assigned", { exact: true })).toBeVisible();
  await expect(executionStory.getByText("Started", { exact: true })).toBeVisible();
  await expect(executionStory.getByText("1 approval needs operator review.")).toBeVisible();
  await executionStory.scrollIntoViewIfNeeded();
  if (process.env.HECATE_CAPTURE_PROJECTS_EXECUTION === "1") {
    await page.screenshot({
      path: "../docs/screenshots/projects-work-execution.jpg",
      type: "jpeg",
      quality: 90,
    });
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await executionStory.scrollIntoViewIfNeeded();
  await expect(executionStory.getByRole("button", { name: "Review in task" })).toBeVisible();
  await expect(executionStory.getByText("Execution details", { exact: true })).toBeVisible();
  expect(
    await executionStory.evaluate((element) => element.scrollWidth <= element.clientWidth + 1),
  ).toBe(true);
  if (process.env.HECATE_CAPTURE_PROJECTS_EXECUTION === "1") {
    await page.screenshot({
      path: "../docs/screenshots/projects-work-execution-narrow.jpg",
      type: "jpeg",
      quality: 90,
    });
  }
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.getByRole("tab", { name: "Overview" }).click();
  await expect(page.getByText("Review pending approval: Verify launch checklist")).toBeVisible();
  await page.getByRole("button", { name: /Open approval/ }).click();
  await expect(page.getByRole("region", { name: "Selected work item" })).toBeVisible();

  completeProjectJourneyAssignment(state);
  await page.getByRole("button", { name: "Refresh project work" }).click();
  await expect(executionStory.locator("header").getByText("done", { exact: true })).toBeVisible();
  await page.getByRole("tab", { name: "Overview" }).click();
  await expect(page.getByText("Record completion evidence: Verify launch checklist")).toBeVisible();
  await page.getByRole("button", { name: /Open work/ }).click();
  await page.getByRole("button", { name: "Add evidence" }).click();
  await page
    .getByRole("dialog", { name: "Record evidence" })
    .getByLabel("Title")
    .fill("Launch checklist");
  await page.getByLabel("URL").fill("https://example.test/checklist");
  await page.getByLabel("Summary").fill("Operator confirmed the launch checklist evidence.");
  await page.getByRole("button", { name: "Record evidence" }).click();

  await expect(page.getByText("Launch checklist", { exact: true })).toBeVisible();
  await page.getByRole("tab", { name: "Overview" }).click();
  await expect(page.getByText("Close out work item: Verify launch checklist")).toBeVisible();
  await page.getByRole("button", { name: /Open closeout/ }).click();
  await page.getByRole("button", { name: "Mark done" }).click();
  await expect(page.getByRole("article", { name: /Verify launch checklist/ })).toContainText(
    "done",
  );
  await page.getByRole("tab", { name: "Overview" }).click();
  await expect(page.getByText("Open latest work: Verify launch checklist")).toBeVisible();
  await expect(page.getByText("Assignments: 0 active · 0 blocked · 1 completed")).toBeVisible();

  expect(state.projects).toHaveLength(1);
  expect(state.roles).toHaveLength(1);
  expect(state.workItems[0]?.status).toBe("done");
  expect(state.assignments[0]?.status).toBe("completed");
  expect(state.artifacts).toHaveLength(1);
  expect(state.memoryCandidates[0]?.status).toBe("rejected");
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
  await onboarding.getByRole("button", { name: "Create work: First work item" }).click();

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

test("Projects overview is the default ready-project home at desktop and narrow widths", async ({
  page,
}) => {
  await page.clock.setFixedTime(new Date(NOW));
  await mockProjectJourneyAPIs(page);
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.workspace", "projects");
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.getByRole("button", { name: "Add" }).click();
  await page.getByLabel("Name").fill("Editorial planning");
  await page.getByLabel("Purpose").fill("Coordinate a rootless editorial review cycle.");
  await page.getByRole("button", { name: "Create project" }).click();

  const onboarding = page.getByRole("region", { name: "Project onboarding" });
  await onboarding.getByRole("button", { name: "Create work: First work item" }).click();
  await page.getByLabel("Title").fill("Review launch narrative");
  await page.getByLabel("Brief").fill("Check the launch narrative and record evidence.");
  await page.getByRole("button", { name: "Create work item" }).click();
  await expect(page.getByRole("tab", { name: /Work/ })).toHaveAttribute("aria-selected", "true");

  await page.reload();
  await expect(page.getByRole("region", { name: "Project overview" })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Overview" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page.getByRole("region", { name: "Project Assistant" })).toHaveCount(0);
  await expect(page.getByRole("region", { name: "Work queue" })).toHaveCount(0);
  if (process.env.HECATE_CAPTURE_PROJECTS_OVERVIEW === "1") {
    await page.screenshot({
      path: "../docs/screenshots/projects-overview.jpg",
      type: "jpeg",
      quality: 90,
    });
  }

  await page.getByRole("button", { name: "View work" }).click();
  await expect(page.getByRole("tab", { name: /Work/ })).toBeFocused();
  await expect(page.getByRole("article", { name: /Review launch narrative/ })).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  const projectIndex = await page.getByRole("region", { name: "Projects" }).boundingBox();
  const projectMain = await page.locator(".projects-cockpit-main").boundingBox();
  expect(projectIndex?.width).toBeGreaterThan(300);
  expect(projectMain?.width).toBeGreaterThan(300);
  expect(projectMain?.y ?? 0).toBeGreaterThan(projectIndex?.y ?? 0);
  await expect(page.getByRole("region", { name: "Project overview" })).toBeVisible();
  const primaryOperationTitle = page.getByText(
    "Prepare first assignment: Review launch narrative",
    { exact: true },
  );
  await expect(primaryOperationTitle).toBeVisible();
  expect(
    await primaryOperationTitle.evaluate(
      (element) => element.scrollWidth <= element.clientWidth + 1,
    ),
  ).toBe(true);

  const narrowTabs = page
    .getByRole("tablist", { name: "Project workspace views" })
    .getByRole("tab");
  await expect(narrowTabs).toHaveCount(5);
  for (let index = 0; index < 5; index += 1) {
    await expect(narrowTabs.nth(index)).toBeInViewport();
  }

  const operationsBox = await page
    .getByRole("region", { name: "Project operations" })
    .boundingBox();
  const activityBox = await page
    .getByRole("region", { name: "Project activity summary" })
    .boundingBox();
  expect(activityBox?.y ?? 0).toBeGreaterThan(
    (operationsBox?.y ?? 0) + (operationsBox?.height ?? 0),
  );
  if (process.env.HECATE_CAPTURE_PROJECTS_OVERVIEW === "1") {
    await page.screenshot({
      path: "../docs/screenshots/projects-overview-narrow.jpg",
      type: "jpeg",
      quality: 90,
    });
  }
});

test("Projects settings and memory use typed root and source mutations", async ({ page }) => {
  const state = await mockProjectJourneyAPIs(page);
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.workspace", "projects");
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  await page.getByRole("button", { name: "Add" }).click();
  await page.getByLabel("Name").fill("Typed operations");
  await page.getByLabel("Purpose").fill("Exercise typed project metadata mutations.");
  await page.getByRole("button", { name: "Create project" }).click();

  const projectSettingsButton = page.locator('button[aria-label="Project settings"]');
  await projectSettingsButton.click();
  let settings = page.getByRole("complementary", { name: "Project settings panel" });
  await settings.getByRole("button", { name: "Add folder" }).click();
  await expect(settings.getByTitle("/tmp/hecate-e2e-project")).toBeVisible();
  await settings.getByRole("button", { name: "Save defaults" }).click();
  await expect.poll(() => state.rootMutationCalls.map((call) => call.method)).toEqual(["POST"]);
  await expect(settings).toBeHidden();

  await projectSettingsButton.click();
  settings = page.getByRole("complementary", { name: "Project settings panel" });
  await settings
    .getByRole("checkbox", { name: "Active project root /tmp/hecate-e2e-project" })
    .uncheck();
  await settings.getByRole("button", { name: "Save defaults" }).click();
  await expect
    .poll(() => state.rootMutationCalls.map((call) => call.method))
    .toEqual(["POST", "PATCH"]);
  await expect(settings).toBeHidden();

  await page
    .getByRole("region", { name: "Project onboarding" })
    .getByRole("button", { name: "Set up project" })
    .first()
    .click();
  await page.getByRole("button", { name: "Apply proposal" }).click();
  await expect(page.getByText("Applied 3 actions")).toBeVisible();
  await page.getByRole("button", { name: "Dismiss" }).click();

  await page.getByRole("tab", { name: /Memory/ }).click();
  await page.getByRole("button", { name: "Source", exact: true }).click();
  let sourceDialog = page.getByRole("dialog", { name: "New project source" });
  await sourceDialog.getByLabel("Title").fill("Launch brief");
  await sourceDialog.getByLabel("Locator").fill("https://example.test/brief");
  await sourceDialog.getByRole("button", { name: "Create source" }).click();
  await expect(page.getByText("Launch brief", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Edit source Launch brief" }).click();
  sourceDialog = page.getByRole("dialog", { name: "Edit project source" });
  await sourceDialog.getByLabel("Title").fill("Launch brief v2");
  await sourceDialog.getByLabel("Locator").fill("https://example.test/brief-v2");
  await sourceDialog.getByRole("button", { name: "Save source" }).click();
  await expect(page.getByText("Launch brief v2", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Delete source Launch brief v2" }).click();
  await page.getByRole("button", { name: "Delete source", exact: true }).click();
  await expect(page.getByText("Launch brief v2", { exact: true })).toHaveCount(0);

  expect(state.rootMutationCalls).toEqual([
    {
      method: "POST",
      body: expect.objectContaining({
        path: "/tmp/hecate-e2e-project",
        kind: "local",
        git_branch: "main",
        active: true,
      }),
    },
    {
      method: "PATCH",
      rootID: "root_e2e_project",
      body: expect.objectContaining({
        path: "/tmp/hecate-e2e-project",
        kind: "local",
        git_branch: "main",
        active: false,
      }),
    },
  ]);
  expect(state.sourceMutationCalls.map((call) => call.method)).toEqual(["POST", "PATCH", "DELETE"]);
  expect(state.sourceMutationCalls[0]?.body).toEqual(
    expect.objectContaining({
      kind: "url",
      title: "Launch brief",
      path: "https://example.test/brief",
      enabled: true,
      format: "url",
    }),
  );
  expect(state.sourceMutationCalls[1]).toEqual({
    method: "PATCH",
    sourceID: "ctx_source_1",
    body: expect.objectContaining({
      title: "Launch brief v2",
      path: "https://example.test/brief-v2",
    }),
  });
  expect(state.sourceMutationCalls[2]).toEqual({ method: "DELETE", sourceID: "ctx_source_1" });
  expect(
    state.projectPatchBodies.every((body) => !("roots" in body) && !("context_sources" in body)),
  ).toBe(true);
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
    projectPatchBodies: [] as Record<string, unknown>[],
    rootMutationCalls: [] as Array<{
      method: string;
      rootID?: string;
      body?: Partial<ProjectRootPayload>;
    }>,
    sourceMutationCalls: [] as Array<{
      method: string;
      sourceID?: string;
      body?: Partial<ProjectContextSourcePayload>;
    }>,
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

  await page.route(/\/hecate\/v1\/agent-presets(?:\?.*)?$/, (route) =>
    route.fulfill(ok({ object: "agent_presets", data: [] })),
  );

  await page.route("/hecate/v1/workspace-dialog", (route) =>
    route.fulfill(
      ok({
        object: "workspace_dialog",
        data: { path: "/tmp/hecate-e2e-project", branch: "main" },
      }),
    ),
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
    if (!resource && method === "PATCH") {
      const patch = JSON.parse(request.postData() || "{}") as Record<string, unknown>;
      state.projectPatchBodies.push(patch);
      state.projects[0] = { ...state.projects[0], ...patch, updated_at: NOW };
      await route.fulfill(ok({ object: "project", data: state.projects[0] }));
      return;
    }
    if (resource === "roots") {
      await handleProjectRootRoute(route, state, parts, method, ok);
      return;
    }
    if (resource === "context-sources" && parts[2] !== "discover") {
      await handleProjectContextSourceRoute(route, state, parts, method, ok);
      return;
    }
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
        const candidateID = parts[3] || "";
        if (candidateID && parts[4] === "reject" && method === "POST") {
          const candidate = state.memoryCandidates.find((item) => item.id === candidateID);
          if (!candidate) {
            await route.fulfill(ok({ object: "project_memory_candidate", data: null }, 404));
            return;
          }
          const rejected = { ...candidate, status: "rejected", updated_at: NOW } as const;
          state.memoryCandidates = state.memoryCandidates.map((item) =>
            item.id === candidateID ? rejected : item,
          );
          await route.fulfill(ok({ object: "project_memory_candidate", data: rejected }));
          return;
        }
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
        ok({ object: "project_operations_brief", data: projectOperationsBrief(state, projectID) }),
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

async function handleProjectRootRoute(
  route: Route,
  state: ProjectJourneyState,
  parts: string[],
  method: string,
  ok: (body: unknown, status?: number) => { status: number; contentType: string; body: string },
) {
  const request = route.request();
  const project = state.projects[0];
  const rootID = parts[2] || "";
  if (!project) {
    await route.fulfill(ok({ object: "project", data: null }, 404));
    return;
  }
  if (!rootID && method === "POST") {
    const body = JSON.parse(request.postData() || "{}") as ProjectRootPayload;
    state.rootMutationCalls.push({ method, body });
    const root = {
      id: "root_e2e_project",
      path: body.path,
      kind: body.kind || "local",
      git_remote: body.git_remote,
      git_branch: body.git_branch,
      active: body.active ?? true,
      created_at: NOW,
      updated_at: NOW,
    };
    project.roots = [...project.roots, root];
    project.updated_at = NOW;
    await route.fulfill(ok({ object: "project", data: project }, 201));
    return;
  }
  if (rootID && method === "PATCH") {
    const body = JSON.parse(request.postData() || "{}") as ProjectRootPayload;
    state.rootMutationCalls.push({ method, rootID, body });
    project.roots = project.roots.map((root) =>
      root.id === rootID
        ? {
            ...root,
            path: body.path,
            kind: body.kind || root.kind,
            git_remote: body.git_remote,
            git_branch: body.git_branch,
            active: body.active ?? root.active,
            updated_at: NOW,
          }
        : root,
    );
    project.updated_at = NOW;
    await route.fulfill(ok({ object: "project", data: project }));
    return;
  }
  if (rootID && method === "DELETE") {
    state.rootMutationCalls.push({ method, rootID });
    project.roots = project.roots.filter((root) => root.id !== rootID);
    if (project.default_root_id === rootID) project.default_root_id = "";
    project.updated_at = NOW;
    await route.fulfill(ok({ object: "project", data: project }));
    return;
  }
  await route.fallback();
}

async function handleProjectContextSourceRoute(
  route: Route,
  state: ProjectJourneyState,
  parts: string[],
  method: string,
  ok: (body: unknown, status?: number) => { status: number; contentType: string; body: string },
) {
  const request = route.request();
  const project = state.projects[0];
  const sourceID = parts[2] || "";
  if (!project) {
    await route.fulfill(ok({ object: "project", data: null }, 404));
    return;
  }
  if (!sourceID && method === "POST") {
    const body = JSON.parse(request.postData() || "{}") as ProjectContextSourcePayload;
    state.sourceMutationCalls.push({ method, body });
    const source = {
      id: "ctx_source_1",
      kind: body.kind || "url",
      title: body.title,
      path: body.path,
      enabled: body.enabled ?? true,
      format: body.format,
      scope: body.scope,
      trust_label: body.trust_label,
      source_category: body.source_category,
      metadata: body.metadata,
      created_at: NOW,
      updated_at: NOW,
    };
    state.sources = [...state.sources, source];
    project.context_sources = state.sources;
    project.updated_at = NOW;
    await route.fulfill(ok({ object: "project", data: project }, 201));
    return;
  }
  if (sourceID && method === "PATCH") {
    const body = JSON.parse(request.postData() || "{}") as ProjectContextSourcePayload;
    state.sourceMutationCalls.push({ method, sourceID, body });
    state.sources = state.sources.map((source) =>
      source.id === sourceID
        ? {
            ...source,
            kind: body.kind || source.kind,
            title: body.title,
            path: body.path,
            enabled: body.enabled ?? source.enabled,
            format: body.format,
            scope: body.scope,
            trust_label: body.trust_label,
            source_category: body.source_category,
            metadata: body.metadata,
            updated_at: NOW,
          }
        : source,
    );
    project.context_sources = state.sources;
    project.updated_at = NOW;
    await route.fulfill(ok({ object: "project", data: project }));
    return;
  }
  if (sourceID && method === "DELETE") {
    state.sourceMutationCalls.push({ method, sourceID });
    state.sources = state.sources.filter((source) => source.id !== sourceID);
    project.context_sources = state.sources;
    project.updated_at = NOW;
    await route.fulfill(ok({ object: "project", data: project }));
    return;
  }
  await route.fallback();
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
    if (parts[5] === "launch-readiness" && method === "GET") {
      await route.fulfill(
        ok({
          object: "project_assignment_launch_readiness",
          data: assignmentLaunchReadiness(projectID, workItemID, assignmentID),
        }),
      );
      return;
    }
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
        status: "awaiting_approval",
        started_at: NOW,
        updated_at: NOW,
        execution_ref: {
          kind: "task_run",
          task_id: "task_launch",
          run_id: "run_launch",
          status: "awaiting_approval",
          pending_approval_count: 1,
          trace_id: "trace_launch",
        },
        execution: {
          task_id: "task_launch",
          run_id: "run_launch",
          status: "awaiting_approval",
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

function completeProjectJourneyAssignment(state: ProjectJourneyState) {
  const assignment = state.assignments[0];
  if (!assignment) throw new Error("Expected a project assignment to complete.");
  Object.assign(assignment, {
    status: "completed",
    completed_at: NOW,
    updated_at: NOW,
    execution_ref: {
      ...assignment.execution_ref,
      kind: "task_run",
      task_id: "task_launch",
      run_id: "run_launch",
      status: "completed",
      trace_id: "trace_launch",
    },
    execution: {
      ...assignment.execution,
      task_id: "task_launch",
      run_id: "run_launch",
      status: "completed",
      provider: "anthropic",
      model: "claude-sonnet-4-6",
      trace_id: "trace_launch",
    },
  });
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
    memory_candidates: state.memoryCandidates.filter((candidate) => candidate.status === "pending"),
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

function assignmentLaunchReadiness(
  projectID: string,
  workItemID: string,
  assignmentID: string,
): ProjectAssignmentLaunchReadinessRecord {
  return {
    project_id: projectID,
    work_item_id: workItemID,
    assignment_id: assignmentID,
    generated_at: NOW,
    ready: true,
    status: "ready",
    title: "Ready to start",
    detail: "Assignment can start after operator confirmation.",
    blockers: [],
    warnings: [],
    driver_kind: "hecate_task",
    provider: "anthropic",
    model: "claude-sonnet-4-6",
    execution_profile: "implementation",
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
  const status = assignment?.execution_ref?.status || assignment?.status || "";
  const completed = status === "completed";
  const blocked = ["queued", "awaiting_approval", "failed", "cancelled"].includes(status);
  const active = Boolean(assignment) && !completed && !blocked;
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
            blocking_signal: completed
              ? "completed"
              : status === "queued"
                ? "not_started"
                : status || "stale_unknown",
            status_summary: completed ? "completed" : status || "unknown",
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
      active_count: active ? 1 : 0,
      blocked_count: blocked ? 1 : 0,
      completed_count: completed ? 1 : 0,
      recent_count: item.length,
    },
    buckets: {
      active: active ? item : [],
      blocked: blocked ? item : [],
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
  const rejectedMemoryCandidateCount = state.memoryCandidates.filter(
    (candidate) => candidate.status === "rejected",
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
      rejected_memory_candidate_count: rejectedMemoryCandidateCount,
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

function projectOperationsBrief(state: ProjectJourneyState, projectID: string) {
  const workItem = state.workItems[0];
  const assignment = state.assignments.find((candidate) => candidate.work_item_id === workItem?.id);
  const assignmentStatus = assignment?.execution_ref?.status || assignment?.status || "";
  const hasEvidence = state.artifacts.some(
    (artifact) =>
      artifact.work_item_id === workItem?.id &&
      artifact.kind === "evidence_link" &&
      (!artifact.assignment_id || artifact.assignment_id === assignment?.id),
  );
  let items: ProjectOperationsBriefItem[] = [];
  if (!workItem) {
    items = [
      {
        id: `create_first_work_item:${projectID}`,
        kind: "create_first_work_item",
        priority: "medium",
        title: "Create the first work item",
        detail: "Start with one reviewable project work item before queueing assignments.",
        action_label: "Draft work",
        target: { surface: "work", project_id: projectID },
        action: {
          type: "draft_project_proposal",
          project_id: projectID,
          request: "Create the first project work item",
        },
      },
    ];
  } else if (workItem.status !== "done" && !assignment) {
    items = [
      {
        id: `prepare_first_assignment:${projectID}:${workItem.id}`,
        kind: "prepare_first_assignment",
        priority: "medium",
        status: "ready",
        title: `Prepare first assignment: ${workItem.title}`,
        detail: "This work item has no assignment yet.",
        action_label: "Draft assignment",
        target: { surface: "work", project_id: projectID, work_item_id: workItem.id },
        action: {
          type: "draft_project_proposal",
          project_id: projectID,
          work_item_id: workItem.id,
          request: `Draft an assignment for ${workItem.title}.`,
        },
      },
    ];
  } else if (workItem?.status === "done") {
    // Cairnline only adds latest work when no higher-value operation exists.
  } else if (workItem && assignment && assignmentStatus === "queued") {
    items = [
      {
        id: `start_queued_assignment:${projectID}:${assignment.id}`,
        kind: "start_queued_assignment",
        priority: "high",
        status: "not_started",
        title: `Review queued assignment: ${workItem.title}`,
        detail: "Open launch preflight before starting this assignment.",
        action_label: "Review start",
        target: {
          surface: "work",
          project_id: projectID,
          work_item_id: workItem.id,
          assignment_id: assignment.id,
          activity_bucket: "blocked",
        },
        action: {
          type: "open_assignment_preflight",
          project_id: projectID,
          work_item_id: workItem.id,
          assignment_id: assignment.id,
          activity_bucket: "blocked",
        },
      },
    ];
  } else if (
    workItem &&
    assignment &&
    ["awaiting_approval", "failed", "cancelled"].includes(assignmentStatus)
  ) {
    const failed = assignmentStatus === "failed";
    const cancelled = assignmentStatus === "cancelled";
    const titlePrefix = failed
      ? "Review failed assignment"
      : cancelled
        ? "Review cancelled assignment"
        : "Review pending approval";
    items = [
      {
        id: `review_blocked_assignment:${projectID}:${assignment.id}`,
        kind: failed
          ? "review_failed_assignment"
          : cancelled
            ? "review_cancelled_assignment"
            : "approve_assignment",
        priority: cancelled ? "medium" : "high",
        status: assignmentStatus,
        title: `${titlePrefix}: ${workItem.title}`,
        detail: "The assignment needs operator attention before closeout.",
        action_label: assignmentStatus === "awaiting_approval" ? "Open approval" : "Open work",
        target: {
          surface: "work",
          project_id: projectID,
          work_item_id: workItem.id,
          assignment_id: assignment.id,
          activity_bucket: "blocked",
        },
        action: {
          type: "open_work_item",
          project_id: projectID,
          work_item_id: workItem.id,
          activity_bucket: "blocked",
        },
      },
    ];
  } else if (
    workItem &&
    assignment &&
    ["claimed", "running", "review", "awaiting_review"].includes(assignmentStatus)
  ) {
    items = [
      {
        id: `inspect_active_assignment:${projectID}:${assignment.id}`,
        kind: "inspect_active_assignment",
        priority: "low",
        status: "running",
        title: `Inspect active assignment: ${workItem.title}`,
        detail: "The assignment is not terminal yet.",
        action_label: "Inspect work",
        target: {
          surface: "work",
          project_id: projectID,
          work_item_id: workItem.id,
          assignment_id: assignment.id,
          activity_bucket: "active",
        },
        action: {
          type: "open_work_item",
          project_id: projectID,
          work_item_id: workItem.id,
          activity_bucket: "active",
        },
      },
    ];
  } else if (workItem && assignment && assignmentStatus === "completed" && !hasEvidence) {
    items = [
      {
        id: `record_completion_evidence:${projectID}:${assignment.id}`,
        kind: "record_completion_evidence",
        priority: "low",
        status: "completed",
        title: `Record completion evidence: ${workItem.title}`,
        detail: "Completed assignments should leave reviewable evidence before work is closed.",
        action_label: "Open work",
        target: {
          surface: "work",
          project_id: projectID,
          work_item_id: workItem.id,
          assignment_id: assignment.id,
          activity_bucket: "completed",
        },
        action: {
          type: "open_work_item",
          project_id: projectID,
          work_item_id: workItem.id,
          activity_bucket: "completed",
        },
      },
    ];
  } else if (workItem && assignment && assignmentStatus === "completed" && hasEvidence) {
    items = [
      {
        id: `close_work_item:${projectID}:${workItem.id}`,
        kind: "close_work_item",
        priority: "low",
        status: "ready",
        title: `Close out work item: ${workItem.title}`,
        detail:
          "Assignments, evidence, handoffs, and review follow-up are clear. Mark done from selected-work detail.",
        action_label: "Open closeout",
        target: { surface: "work", project_id: projectID, work_item_id: workItem.id },
        action: { type: "open_work_item", project_id: projectID, work_item_id: workItem.id },
      },
    ];
  }
  const pendingMemoryCandidateCount = state.memoryCandidates.filter(
    (candidate) => candidate.status === "pending",
  ).length;
  if (pendingMemoryCandidateCount > 0) {
    items.push({
      id: `review_memory_candidates:${projectID}`,
      kind: "review_memory_candidates",
      priority: "medium",
      status: "pending",
      title:
        pendingMemoryCandidateCount === 1
          ? "Review 1 memory candidate"
          : `Review ${pendingMemoryCandidateCount} memory candidates`,
      detail: "Promote, edit, or reject pending memory candidates before they become stale.",
      action_label: "Review memory",
      target: { surface: "memory", project_id: projectID },
      action: { type: "open_memory_review", project_id: projectID },
      metadata: { candidate_count: String(pendingMemoryCandidateCount) },
    });
  }
  if (items.length === 0 && workItem) {
    items.push({
      id: `open_latest_work:${projectID}:${workItem.id}`,
      kind: "open_latest_work",
      priority: "low",
      status: workItem.status,
      title: `Open latest work: ${workItem.title}`,
      detail: "Review the most recently updated work item.",
      action_label: "Open work",
      target: { surface: "work", project_id: projectID, work_item_id: workItem.id },
      action: { type: "open_work_item", project_id: projectID, work_item_id: workItem.id },
    });
  }
  const priorityRank = { high: 0, medium: 1, low: 2 } as const;
  const kindRank: Record<string, number> = {
    approve_assignment: 0,
    review_failed_assignment: 10,
    start_queued_assignment: 30,
    review_cancelled_assignment: 50,
    prepare_first_assignment: 70,
    create_first_work_item: 80,
    review_memory_candidates: 90,
    inspect_active_assignment: 100,
    record_completion_evidence: 110,
    close_work_item: 120,
    open_latest_work: 130,
  };
  items.sort((left, right) => {
    const byPriority = priorityRank[left.priority] - priorityRank[right.priority];
    if (byPriority !== 0) return byPriority;
    const byKind = (kindRank[left.kind] ?? 1_000) - (kindRank[right.kind] ?? 1_000);
    return byKind !== 0 ? byKind : left.id.localeCompare(right.id);
  });
  return {
    project_id: projectID,
    generated_at: NOW,
    summary: {
      item_count: items.length,
      medium_count: items.filter((item) => item.priority === "medium").length,
      high_count: items.filter((item) => item.priority === "high").length,
      low_count: items.filter((item) => item.priority === "low").length,
      pending_memory_candidate_count: pendingMemoryCandidateCount,
      pending_handoff_count: 0,
    },
    items,
  };
}
