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
  ProjectHandoffRecord,
  ProjectMemoryCandidateRecord,
  ProjectMemoryRecord,
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
    .getByRole("button", { name: "Dismiss memory suggestion Guidance source: AGENTS.md" })
    .click();
  await expect(
    page.getByRole("button", { name: "Dismiss memory suggestion Guidance source: AGENTS.md" }),
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
  const preflight = page.getByRole("dialog", { name: /launch details/i });
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
  const evidenceDialog = page.getByRole("dialog", { name: "Record evidence" });
  await evidenceDialog.getByLabel("Title").fill("Launch checklist");
  await evidenceDialog.getByLabel("URL").fill("https://example.test/checklist");
  await evidenceDialog
    .getByLabel("Summary")
    .fill("Operator confirmed the launch checklist evidence.");
  await evidenceDialog.getByRole("button", { name: "Record evidence" }).click();

  await expect(page.getByText("Launch checklist", { exact: true })).toBeVisible();
  await page.getByRole("tab", { name: "Overview" }).click();
  await expect(page.getByText("Close out work item: Verify launch checklist")).toBeVisible();
  await page.getByRole("button", { name: /Open closeout/ }).click();
  await page
    .getByRole("region", { name: "Next work item action" })
    .getByRole("button", { name: "Review closeout" })
    .click();
  await page
    .getByRole("dialog", { name: "Review closeout" })
    .getByRole("button", { name: "Mark work done" })
    .click();
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

test("Projects create keeps keyboard focus inside the pending dialog", async ({ page }) => {
  await mockProjectJourneyAPIs(page);
  let releaseCreate!: () => void;
  const createGate = new Promise<void>((resolve) => {
    releaseCreate = resolve;
  });
  await page.route(/\/hecate\/v1\/projects$/, async (route) => {
    if (route.request().method() !== "POST") {
      await route.fallback();
      return;
    }
    await createGate;
    await route.fulfill({
      status: 500,
      contentType: "application/json",
      body: JSON.stringify({
        error: { type: "create_failed", message: "create failed" },
      }),
    });
  });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.workspace", "projects");
  });

  await page.goto("/");
  await page.getByRole("button", { name: "Add" }).click();
  const dialog = page.getByRole("dialog", { name: "Create project" });
  const name = dialog.getByLabel("Name");
  await name.fill("Focus-safe project");
  await name.press("Enter");

  const pendingButton = dialog.getByRole("button", { name: "Creating..." });
  await expect(pendingButton).toHaveAttribute("aria-disabled", "true");
  await expect(pendingButton).toBeFocused();
  await expect
    .poll(() => dialog.evaluate((element) => element.contains(document.activeElement)))
    .toBe(true);

  releaseCreate();
  await expect(dialog.getByRole("alert")).toContainText("create failed");
  await expect(dialog.getByRole("button", { name: "Create project" })).toBeFocused();
  await expect
    .poll(() => dialog.evaluate((element) => element.contains(document.activeElement)))
    .toBe(true);
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
  await page.getByRole("button", { name: "Review closeout" }).click();
  await page
    .getByRole("dialog", { name: "Review closeout" })
    .getByRole("button", { name: "Mark work done" })
    .click();
  await expect(page.getByRole("article", { name: /Summarize interview themes/ })).toContainText(
    "done",
  );

  expect(state.projects[0]?.roots).toHaveLength(0);
  expect(state.roles).toHaveLength(0);
  expect(state.workItems[0]?.owner_role_id).toBe("");
  expect(state.workItems[0]?.status).toBe("done");
  expect(state.artifacts).toHaveLength(1);
});

test("Projects Human assignment journey: rootless work without launch preflight", async ({
  page,
}) => {
  await page.clock.setFixedTime(new Date(NOW));
  const state = await mockProjectJourneyAPIs(page);
  state.projects = [
    {
      id: "proj_human",
      name: "Research synthesis",
      description: "Coordinate interview synthesis without a code workspace.",
      roots: [],
      context_sources: [],
      created_at: NOW,
      updated_at: NOW,
    },
  ];
  state.roles = [
    {
      id: "researcher",
      project_id: "proj_human",
      name: "Researcher",
      description: "Synthesize interview findings for review.",
      default_driver_kind: "manual",
      skill_ids: [],
      built_in: false,
      created_at: NOW,
      updated_at: NOW,
    },
  ];
  state.workItems = [
    {
      id: "work_human",
      project_id: "proj_human",
      title: "Synthesize interview themes",
      brief: "Turn the interview notes into a reviewable theme summary.",
      status: "ready",
      priority: "high",
      owner_role_id: "researcher",
      created_at: NOW,
      updated_at: NOW,
    },
  ];

  const launchCheckRequests: string[] = [];
  page.on("request", (request) => {
    const path = new URL(request.url()).pathname;
    if (path.endsWith("/launch-readiness") || path.endsWith("/preflight")) {
      launchCheckRequests.push(path);
    }
  });
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.workspace", "projects");
    window.localStorage.setItem("hecate.project", "proj_human");
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.getByRole("tab", { name: /Work/ }).click();

  const detail = page.getByRole("region", { name: "Selected work item" });
  await expect(detail.getByRole("heading", { name: "Synthesize interview themes" })).toBeVisible();
  await detail.getByText("Add manually").click();
  await detail
    .getByRole("group", { name: "Manual work item actions" })
    .getByRole("button", { name: "Assignment" })
    .click();

  const assignmentDialog = page.getByRole("dialog", { name: "Add assignment" });
  await expect(assignmentDialog.getByLabel("Work done by")).toHaveValue("manual");
  await expect(
    assignmentDialog.getByText("Track work completed by a person outside Hecate."),
  ).toBeVisible();
  await expect(assignmentDialog.getByLabel("Workspace (optional)")).toHaveCount(0);

  const createRequestPromise = page.waitForRequest((request) => {
    return (
      request.method() === "POST" &&
      new URL(request.url()).pathname ===
        "/hecate/v1/projects/proj_human/work-items/work_human/assignments"
    );
  });
  await assignmentDialog.getByRole("button", { name: "Add assignment" }).click();
  const createRequest = await createRequestPromise;
  expect(createRequest.postDataJSON()).toEqual({
    role_id: "researcher",
    driver_kind: "manual",
  });

  const executionStory = page.getByRole("article", {
    name: "Researcher assignment execution assign_human",
  });
  await expect(executionStory).toBeVisible();
  await expect(executionStory.getByText("Human", { exact: true })).toBeVisible();
  await expect(executionStory.getByText("Ready", { exact: true })).toBeVisible();
  await expect(executionStory.getByText("Ready for a person to begin.").first()).toBeVisible();
  await expect(executionStory.getByRole("button", { name: "Start work" })).toBeVisible();
  await expect(executionStory.getByText("Launch readiness", { exact: true })).toHaveCount(0);
  await expect(executionStory.getByText(/linked task or chat/i)).toHaveCount(0);

  await page.getByRole("tab", { name: "Overview" }).click();
  const operations = page.getByRole("region", { name: "Project operations" });
  await expect(
    operations.getByText("Human work ready: Synthesize interview themes", { exact: true }),
  ).toBeVisible();
  await expect(
    operations.getByText("This assignment is ready for a person to begin.", { exact: true }),
  ).toBeVisible();
  await operations.getByRole("button", { name: /Open work/ }).click();
  await expect(page.getByRole("tab", { name: /Work/ })).toHaveAttribute("aria-selected", "true");
  await expect(detail).toBeVisible();
  await expect(page.getByRole("dialog", { name: /launch details/i })).toHaveCount(0);

  await executionStory.scrollIntoViewIfNeeded();
  if (process.env.HECATE_CAPTURE_PROJECTS_HUMAN === "1") {
    await page.screenshot({
      path: "../docs/screenshots/projects-human-assignment.jpg",
      type: "jpeg",
      quality: 90,
    });
  }

  await page.setViewportSize({ width: 390, height: 844 });
  await executionStory.scrollIntoViewIfNeeded();
  await expect(executionStory.getByRole("button", { name: "Start work" })).toBeVisible();
  expect(
    await executionStory.evaluate((element) => element.scrollWidth <= element.clientWidth + 1),
  ).toBe(true);
  if (process.env.HECATE_CAPTURE_PROJECTS_HUMAN === "1") {
    await page.screenshot({
      path: "../docs/screenshots/projects-human-assignment-narrow.jpg",
      type: "jpeg",
      quality: 90,
    });
  }

  const startRequestPromise = page.waitForRequest((request) => {
    return (
      request.method() === "POST" &&
      new URL(request.url()).pathname ===
        "/hecate/v1/projects/proj_human/work-items/work_human/assignments/assign_human/start"
    );
  });
  await executionStory.getByRole("button", { name: "Start work" }).click();
  const startRequest = await startRequestPromise;
  expect(startRequest.postDataJSON()).toEqual({ driver_kind: "manual" });
  await expect(executionStory.getByText("Human work is in progress.").first()).toBeVisible();
  await expect(executionStory.getByRole("button", { name: "Mark complete" })).toBeVisible();
  await expect(page.getByRole("dialog", { name: /launch details/i })).toHaveCount(0);
  expect(state.assignments[0]?.execution_ref).toBeUndefined();

  await executionStory.getByText("Assignment details").click();
  await executionStory.getByRole("button", { name: "Edit assignment assign_human" }).click();
  const editDialog = page.getByRole("dialog", { name: "Edit assignment" });
  await expect(editDialog.getByLabel("Responsibility")).toBeDisabled();
  await expect(editDialog.getByLabel("Work done by")).toBeDisabled();
  const reviewRequestPromise = page.waitForRequest((request) => {
    return (
      request.method() === "PATCH" &&
      new URL(request.url()).pathname ===
        "/hecate/v1/projects/proj_human/work-items/work_human/assignments/assign_human"
    );
  });
  await editDialog.getByLabel("Status").selectOption("awaiting_approval");
  await editDialog.getByRole("button", { name: "Save assignment" }).click();
  const reviewRequest = await reviewRequestPromise;
  expect(reviewRequest.postDataJSON()).toEqual({ status: "awaiting_approval" });
  await expect(executionStory.getByText("This work is waiting for review.")).toBeVisible();
  await expect(executionStory.getByRole("button", { name: "Resume work" })).toBeVisible();
  expect(
    await executionStory.evaluate((element) => element.scrollWidth <= element.clientWidth + 1),
  ).toBe(true);

  const resumeRequestPromise = page.waitForRequest((request) => {
    return (
      request.method() === "PATCH" &&
      new URL(request.url()).pathname ===
        "/hecate/v1/projects/proj_human/work-items/work_human/assignments/assign_human"
    );
  });
  await executionStory.getByRole("button", { name: "Resume work" }).click();
  const resumeRequest = await resumeRequestPromise;
  expect(resumeRequest.postDataJSON()).toEqual({ status: "running" });
  await expect(executionStory.getByRole("button", { name: "Mark complete" })).toBeVisible();
  expect(state.assignments[0]?.execution_ref).toBeUndefined();

  const completeRequestPromise = page.waitForRequest((request) => {
    return (
      request.method() === "PATCH" &&
      new URL(request.url()).pathname ===
        "/hecate/v1/projects/proj_human/work-items/work_human/assignments/assign_human"
    );
  });
  await executionStory.getByRole("button", { name: "Mark complete" }).click();
  const completeRequest = await completeRequestPromise;
  expect(completeRequest.postDataJSON()).toEqual({ status: "completed" });
  await expect(
    executionStory.getByText("Human work is complete. Add evidence or choose the follow-through."),
  ).toBeVisible();

  expect(launchCheckRequests).toEqual([]);
  expect(state.projects[0]?.roots).toHaveLength(0);
  expect(state.assignments[0]?.driver_kind).toBe("manual");
  expect(state.assignments[0]?.status).toBe("completed");
  expect(state.assignments[0]?.execution_ref).toBeUndefined();
});

test("Projects follow-through journey: review, handoff, evidence, and durable closeout", async ({
  page,
}) => {
  await page.clock.setFixedTime(new Date(NOW));
  const state = await mockProjectJourneyAPIs(page);
  state.projects = [
    {
      id: "proj_follow_through",
      name: "Editorial release",
      description: "Coordinate a reviewed release with explicit evidence and closeout.",
      roots: [],
      context_sources: [],
      created_at: NOW,
      updated_at: NOW,
    },
  ];
  state.roles = [
    {
      id: "role_editor",
      project_id: "proj_follow_through",
      name: "Editor",
      description: "Prepare the release narrative.",
      default_driver_kind: "manual",
      skill_ids: [],
      built_in: false,
      created_at: NOW,
      updated_at: NOW,
    },
    {
      id: "role_reviewer",
      project_id: "proj_follow_through",
      name: "Reviewer",
      description: "Review the release narrative before closeout.",
      default_driver_kind: "manual",
      skill_ids: [],
      built_in: false,
      created_at: NOW,
      updated_at: NOW,
    },
  ];
  state.workItems = [
    {
      id: "work_decoy",
      project_id: "proj_follow_through",
      title: "Unrelated planning note",
      status: "done",
      priority: "low",
      created_at: NOW,
      updated_at: NOW,
    },
    {
      id: "work_follow_through",
      project_id: "proj_follow_through",
      title: "Ship editorial release",
      brief: "Complete review, preserve evidence, resolve the handoff, and close deliberately.",
      status: "ready",
      priority: "high",
      owner_role_id: "role_editor",
      reviewer_role_ids: ["role_reviewer"],
      created_at: NOW,
      updated_at: NOW,
    },
  ];
  state.assignments = [
    {
      id: "assign_decoy",
      project_id: "proj_follow_through",
      work_item_id: "work_decoy",
      role_id: "role_editor",
      driver_kind: "manual",
      status: "completed",
      created_at: NOW,
      updated_at: NOW,
    },
    {
      id: "assign_follow_reviewer",
      project_id: "proj_follow_through",
      work_item_id: "work_follow_through",
      role_id: "role_reviewer",
      driver_kind: "manual",
      status: "completed",
      started_at: NOW,
      completed_at: NOW,
      created_at: NOW,
      updated_at: NOW,
    },
    {
      id: "assign_follow_editor",
      project_id: "proj_follow_through",
      work_item_id: "work_follow_through",
      role_id: "role_editor",
      driver_kind: "manual",
      status: "completed",
      started_at: NOW,
      completed_at: NOW,
      created_at: NOW,
      updated_at: NOW,
    },
  ];
  state.artifacts = [
    {
      id: "artifact_decoy",
      project_id: "proj_follow_through",
      work_item_id: "work_decoy",
      assignment_id: "assign_decoy",
      kind: "evidence_link",
      title: "Unrelated evidence",
      body: "This belongs to another work item.",
      created_at: NOW,
      updated_at: NOW,
    },
    {
      id: "artifact_reviewer_evidence",
      project_id: "proj_follow_through",
      work_item_id: "work_follow_through",
      assignment_id: "assign_follow_reviewer",
      kind: "evidence_link",
      title: "Reviewer notes",
      body: "Reviewer notes are preserved for closeout.",
      evidence_source_kind: "document",
      evidence_url: "https://example.test/reviewer-notes",
      evidence_trust_label: "operator_provided",
      created_at: NOW,
      updated_at: NOW,
    },
  ];
  state.handoffs = [
    {
      id: "handoff_same_work_decoy",
      project_id: "proj_follow_through",
      work_item_id: "work_follow_through",
      title: "Prior editorial handoff",
      summary: "This accepted handoff belongs to the selected work item.",
      recommended_next_action: "Keep the pending editorial sign-off in focus.",
      status: "accepted",
      provenance_kind: "operator",
      trust_label: "operator_provided",
      created_at: NOW,
      updated_at: NOW,
      status_changed_at: NOW,
    },
    {
      id: "handoff_decoy",
      project_id: "proj_follow_through",
      work_item_id: "work_decoy",
      title: "Unrelated handoff",
      summary: "This belongs to another work item.",
      recommended_next_action: "Ignore in the selected-work journey.",
      status: "accepted",
      provenance_kind: "operator",
      trust_label: "operator_provided",
      created_at: NOW,
      updated_at: NOW,
      status_changed_at: NOW,
    },
    {
      id: "handoff_editorial_review",
      project_id: "proj_follow_through",
      work_item_id: "work_follow_through",
      source_assignment_id: "assign_follow_editor",
      target_assignment_id: "assign_follow_reviewer",
      target_role_id: "role_reviewer",
      title: "Editorial sign-off",
      summary: "The reviewer has the release narrative and supporting notes.",
      recommended_next_action: "Accept the completed review handoff before closeout.",
      status: "pending",
      provenance_kind: "operator",
      trust_label: "operator_provided",
      created_at: NOW,
      updated_at: NOW,
      status_changed_at: NOW,
    },
  ];

  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.workspace", "projects");
    window.localStorage.setItem("hecate.project", "proj_follow_through");
  });
  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");

  const operations = page.getByRole("region", { name: "Project operations" });
  await expect(
    operations.getByText("Review pending handoff: Editorial sign-off", { exact: true }),
  ).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  await operations.getByRole("button", { name: /Open handoff/ }).click();
  await expect(page.getByRole("tab", { name: /Work/ })).toHaveAttribute("aria-selected", "true");
  const detail = page.getByRole("region", { name: "Selected work item" });
  const handoffRow = detail.getByRole("group", { name: "Editorial sign-off handoff" });
  const sameWorkHandoffDecoy = detail.getByRole("group", {
    name: "Prior editorial handoff handoff",
  });
  await expect(handoffRow).toBeFocused();
  await expect(sameWorkHandoffDecoy).toBeVisible();
  await expect(sameWorkHandoffDecoy).not.toBeFocused();
  await expect(handoffRow).toBeInViewport();
  await expect(detail.getByText("Unrelated evidence", { exact: true })).toHaveCount(0);
  await expect(detail.getByText("Unrelated handoff", { exact: true })).toHaveCount(0);
  const handoffLayout = await handoffRow.evaluate((element) => ({
    clientWidth: element.clientWidth,
    scrollWidth: element.scrollWidth,
  }));
  expect(handoffLayout.scrollWidth).toBeLessThanOrEqual(handoffLayout.clientWidth + 1);
  const detailLayout = await detail.evaluate((element) => ({
    clientWidth: element.clientWidth,
    scrollWidth: element.scrollWidth,
  }));
  expect(detailLayout.scrollWidth).toBeLessThanOrEqual(detailLayout.clientWidth + 1);
  if (process.env.HECATE_CAPTURE_PROJECTS_FOLLOW_THROUGH === "1") {
    await page.screenshot({
      path: "../docs/screenshots/projects-follow-through-narrow.jpg",
      type: "jpeg",
      quality: 90,
    });
  }

  const acceptRequestPromise = page.waitForRequest((request) => {
    return (
      request.method() === "POST" &&
      new URL(request.url()).pathname ===
        "/hecate/v1/projects/proj_follow_through/work-items/work_follow_through/handoffs/handoff_editorial_review/status"
    );
  });
  await handoffRow.getByRole("button", { name: "Accept" }).click();
  const acceptRequest = await acceptRequestPromise;
  expect(acceptRequest.postDataJSON()).toEqual({ status: "accepted" });
  await expect(handoffRow.getByText("Accepted", { exact: true })).toBeVisible();
  await expect(handoffRow).toBeFocused();
  expect(state.handoffs.find((handoff) => handoff.id === "handoff_editorial_review")?.status).toBe(
    "accepted",
  );

  await page.setViewportSize({ width: 1280, height: 720 });
  const reviewerStory = detail.getByRole("article", {
    name: "Reviewer assignment execution assign_follow_reviewer",
  });
  await reviewerStory
    .getByRole("button", { name: "Record review for assignment assign_follow_reviewer" })
    .click();
  const reviewDialog = page.getByRole("dialog", { name: "Record review" });
  const reviewContext = reviewDialog.getByRole("region", { name: "Review context" });
  await expect(reviewContext.getByText(/Reviewing Editor assignment/)).toBeVisible();
  await expect(reviewContext.getByText(/Review assignment Reviewer/)).toBeVisible();
  await expect(reviewDialog.getByLabel("Review assignment")).toHaveValue("assign_follow_reviewer");
  await expect(reviewDialog.getByRole("button", { name: "Save review" })).toBeDisabled();
  await reviewDialog.getByLabel("Verdict").selectOption("approved");
  await reviewDialog
    .getByLabel("Summary")
    .fill("The release narrative is approved and the reviewer notes are preserved.");
  const reviewRequestPromise = page.waitForRequest((request) => {
    return (
      request.method() === "POST" &&
      new URL(request.url()).pathname ===
        "/hecate/v1/projects/proj_follow_through/work-items/work_follow_through/artifacts"
    );
  });
  await reviewDialog.getByRole("button", { name: "Save review" }).click();
  const reviewRequest = await reviewRequestPromise;
  expect(reviewRequest.postDataJSON()).toEqual(
    expect.objectContaining({
      assignment_id: "assign_follow_reviewer",
      reviewed_assignment_id: "assign_follow_editor",
      review_follow_up_required: false,
      review_verdict: "approved",
    }),
  );
  await expect(detail.getByText("Reviewer review", { exact: true })).toBeVisible();
  await expect(
    detail.getByRole("group", { name: "Reviewer review Review artifact" }),
  ).toBeFocused();

  await page.setViewportSize({ width: 390, height: 844 });
  const reviewArtifact = detail.getByRole("group", {
    name: "Reviewer review Review artifact",
  });
  await reviewArtifact.scrollIntoViewIfNeeded();
  await expect(reviewArtifact).toBeInViewport();
  const reviewArtifactLayout = await reviewArtifact.evaluate((element) => ({
    clientWidth: element.clientWidth,
    scrollWidth: element.scrollWidth,
  }));
  expect(reviewArtifactLayout.scrollWidth).toBeLessThanOrEqual(
    reviewArtifactLayout.clientWidth + 1,
  );
  const narrowReviewDetailLayout = await detail.evaluate((element) => ({
    clientWidth: element.clientWidth,
    scrollWidth: element.scrollWidth,
  }));
  expect(narrowReviewDetailLayout.scrollWidth).toBeLessThanOrEqual(
    narrowReviewDetailLayout.clientWidth + 1,
  );
  await page.setViewportSize({ width: 1280, height: 720 });

  await page.getByRole("tab", { name: "Overview" }).click();
  await expect(
    operations.getByText("Record completion evidence: Ship editorial release", { exact: true }),
  ).toBeVisible();
  await operations.getByRole("button", { name: /Open work/ }).click();
  const editorStory = detail.getByRole("article", {
    name: "Editor assignment execution assign_follow_editor",
  });
  await expect(editorStory).toBeFocused();
  await expect(editorStory).toBeInViewport();

  const nextAction = detail.getByRole("region", { name: "Next work item action" });
  await nextAction.getByRole("button", { name: "Record evidence" }).click();
  const evidenceDialog = page.getByRole("dialog", { name: "Record evidence" });
  await expect(evidenceDialog.getByLabel("Assignment")).toHaveValue("assign_follow_editor");
  await evidenceDialog.getByLabel("Title").fill("Published release narrative");
  await evidenceDialog.getByLabel("URL").fill("https://example.test/editorial-release");
  await evidenceDialog
    .getByLabel("Summary")
    .fill("The approved release narrative is published and ready for closeout.");
  const evidenceRequestPromise = page.waitForRequest((request) => {
    return (
      request.method() === "POST" &&
      new URL(request.url()).pathname ===
        "/hecate/v1/projects/proj_follow_through/work-items/work_follow_through/artifacts"
    );
  });
  await evidenceDialog.getByRole("button", { name: "Record evidence" }).click();
  const evidenceRequest = await evidenceRequestPromise;
  expect(evidenceRequest.postDataJSON()).toEqual(
    expect.objectContaining({
      assignment_id: "assign_follow_editor",
      kind: "evidence_link",
      title: "Published release narrative",
    }),
  );
  const recordedEvidence = detail.getByRole("group", {
    name: "Published release narrative Evidence artifact",
  });
  await expect(recordedEvidence).toBeFocused();
  await expect(
    nextAction.getByText("Close out work item: Ship editorial release", { exact: true }),
  ).toBeVisible();
  await expect(nextAction.getByRole("button", { name: "Review closeout" })).toBeEnabled();

  await page
    .getByRole("region", { name: "Work queue" })
    .getByRole("link", { name: "Open work item Unrelated planning note" })
    .click();
  await expect(detail.getByRole("heading", { name: "Unrelated planning note" })).toBeVisible();
  await page.getByRole("tab", { name: "Overview" }).click();
  await expect(
    operations.getByText("Close out work item: Ship editorial release", { exact: true }),
  ).toBeVisible();
  await operations.getByRole("button", { name: /Open closeout/ }).click();
  await expect(
    detail.getByRole("heading", { name: "Ship editorial release", exact: true }),
  ).toBeVisible();
  const closeout = detail.getByRole("region", { name: "Work closeout" });
  await expect(closeout).toBeFocused();
  await expect(closeout).toBeInViewport();
  await nextAction.getByRole("button", { name: "Review closeout" }).click();
  const closeoutDialog = page.getByRole("dialog", { name: "Review closeout" });
  await expect(closeoutDialog).toContainText(/2\s*Assignments complete/);
  await expect(closeoutDialog).toContainText(/0\s*Review follow-ups/);
  await expect(closeoutDialog).toContainText(/0\s*Open handoffs/);
  await expect(
    closeoutDialog.getByText(/does not delete assignments, linked tasks or chats, reviews/i),
  ).toBeVisible();
  if (process.env.HECATE_CAPTURE_PROJECTS_FOLLOW_THROUGH === "1") {
    await page.screenshot({
      path: "../docs/screenshots/projects-follow-through.jpg",
      type: "jpeg",
      quality: 90,
    });
  }

  const closeoutRequestPromise = page.waitForRequest((request) => {
    return (
      request.method() === "PATCH" &&
      new URL(request.url()).pathname ===
        "/hecate/v1/projects/proj_follow_through/work-items/work_follow_through"
    );
  });
  await closeoutDialog.getByRole("button", { name: "Mark work done" }).click();
  const closeoutRequest = await closeoutRequestPromise;
  expect(closeoutRequest.postDataJSON()).toEqual({ status: "done" });
  await expect(nextAction.getByText("Work closed", { exact: true })).toBeVisible();
  await expect(nextAction).toBeFocused();
  expect(state.workItems.find((item) => item.id === "work_follow_through")?.status).toBe("done");

  await page.reload();
  await page.getByRole("tab", { name: /Work/ }).click();
  await page
    .getByRole("region", { name: "Work queue" })
    .getByRole("link", { name: "Open work item Ship editorial release" })
    .click();
  await expect(detail.getByRole("heading", { name: "Ship editorial release" })).toBeVisible();
  await expect(nextAction.getByText("Work closed", { exact: true })).toBeVisible();
  await expect(detail.getByRole("region", { name: "Add to work item" })).toHaveCount(0);
  await expect(detail.getByRole("button", { name: "Edit", exact: true })).toHaveCount(0);
  await expect(detail.getByRole("button", { name: "Accept", exact: true })).toHaveCount(0);
  await expect(detail.getByRole("button", { name: /Record review for assignment/ })).toHaveCount(0);
  await expect(nextAction.getByRole("button")).toHaveCount(0);
  await expect(page.getByRole("region", { name: "Project Assistant" })).toHaveCount(0);
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
  await page.getByRole("tab", { name: "Overview" }).click();

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
  await page.getByRole("tab", { name: "Overview" }).click();

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

test("Projects links restore exact work across reload, history, and narrow widths", async ({
  page,
}) => {
  await page.clock.setFixedTime(new Date(NOW));
  const state = await mockProjectJourneyAPIs(page);
  const { firstWorkItem, project, secondWorkItem } = seedNavigationProject(state);
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.workspace", "chats");
    window.localStorage.setItem("hecate.project", "remembered_elsewhere");
  });

  const workURL = `/projects?project=${project.id}&view=work&work=${secondWorkItem.id}`;
  await page.goto(workURL);
  await expect(page.getByRole("heading", { name: secondWorkItem.title })).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`${workURL.replaceAll("?", "\\?")}$`));
  await expect(page.getByRole("heading", { name: "First queued item" })).toHaveCount(0);

  await page.getByRole("link", { name: `Open work item ${firstWorkItem.title}` }).click();
  await expect(page.getByRole("heading", { name: firstWorkItem.title })).toBeVisible();
  await expect(page).toHaveURL(
    new RegExp(`/projects\\?project=${project.id}&view=work&work=${firstWorkItem.id}$`),
  );
  await page.goBack();
  await expect(page.getByRole("heading", { name: secondWorkItem.title })).toBeVisible();

  await page.getByRole("tab", { name: /Memory/ }).click();
  await expect(page).toHaveURL(new RegExp(`/projects\\?project=${project.id}&view=memory$`));
  await expect(page.getByRole("region", { name: "Project memory" })).toBeVisible();

  await page.goBack();
  await expect(page.getByRole("heading", { name: secondWorkItem.title })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("heading", { name: secondWorkItem.title })).toBeVisible();

  await page.getByRole("link", { name: "Chats" }).click();
  await expect(page).toHaveURL(/\/chats$/);
  await page.goBack();
  await expect(page.getByRole("heading", { name: secondWorkItem.title })).toBeVisible();
  await expect(
    page.getByRole("status").filter({ hasText: `Work item opened: ${secondWorkItem.title}` }),
  ).toHaveText(`Work item opened: ${secondWorkItem.title}`);

  if (process.env.HECATE_CAPTURE_PROJECTS_NAVIGATION === "1") {
    await page.screenshot({
      path: "../docs/screenshots/projects-navigation-work.jpg",
      type: "jpeg",
      quality: 90,
    });
  }

  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  const narrowSelectedHeading = page.getByRole("heading", { name: secondWorkItem.title });
  await expect(narrowSelectedHeading).toBeVisible();
  expect(
    await page
      .locator(".projects-cockpit-shell")
      .evaluate((element) => element.scrollWidth <= element.clientWidth + 1),
  ).toBe(true);
  await narrowSelectedHeading.scrollIntoViewIfNeeded();
  await expect(narrowSelectedHeading).toBeInViewport();
  if (process.env.HECATE_CAPTURE_PROJECTS_NAVIGATION === "1") {
    await page.screenshot({
      path: "../docs/screenshots/projects-navigation-work-narrow.jpg",
      type: "jpeg",
      quality: 90,
    });
  }
});

test("Projects links fail closed for missing projects and work items", async ({ page }) => {
  const state = await mockProjectJourneyAPIs(page);
  const { firstWorkItem, project, secondWorkItem } = seedNavigationProject(state);
  const projectRequests: string[] = [];
  page.on("request", (request) => {
    const pathname = new URL(request.url()).pathname;
    if (pathname.startsWith("/hecate/v1/projects/")) projectRequests.push(pathname);
  });
  await page.addInitScript((projectID) => {
    window.localStorage.setItem("hecate.workspace", "chats");
    window.localStorage.setItem("hecate.project", projectID);
  }, project.id);

  await page.goto("/projects?project=missing_project");
  await expect(page.getByText("Project not found", { exact: true })).toBeVisible();
  await expect(page).toHaveURL(/\/projects\?project=missing_project$/);
  expect(projectRequests.some((path) => path.includes("/missing_project/"))).toBe(false);

  projectRequests.length = 0;
  await page.goto(`/projects?project=${project.id}&view=work&work=missing_work`);
  await expect(
    page.getByRole("status").filter({ hasText: "Work item not found in this project" }),
  ).toBeVisible();
  await expect(page.getByRole("region", { name: "Work queue" })).toBeVisible();
  await expect(
    page.getByRole("link", { name: `Open work item ${firstWorkItem.title}` }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: `Open work item ${secondWorkItem.title}` }),
  ).toBeVisible();
  await expect(page).toHaveURL(
    new RegExp(`/projects\\?project=${project.id}&view=work&work=missing_work$`),
  );
  expect(projectRequests.some((path) => path.includes("/work-items/missing_work"))).toBe(false);
  expect(projectRequests.some((path) => path.includes(`/work-items/${firstWorkItem.id}`))).toBe(
    false,
  );
});

test("Projects catalog retry preserves deliberate browser focus", async ({ page }) => {
  const state = await mockProjectJourneyAPIs(page);
  state.projectCatalogFailuresRemaining = 2;
  let releaseFailedRetry!: () => void;
  state.projectCatalogRetryGate = new Promise<void>((resolve) => {
    releaseFailedRetry = resolve;
  });

  await page.goto("/projects");
  await expect(page.getByText("Projects unavailable", { exact: true })).toBeVisible();
  const retryButton = page.getByRole("button", { name: "Retry" });
  await retryButton.focus();
  await retryButton.click();

  const retryingButton = page.getByRole("button", { name: "Retrying…" });
  await expect(retryingButton).toHaveAttribute("aria-disabled", "true");
  await expect(retryingButton).toBeFocused();
  await retryingButton.press("Enter");
  expect(state.projectCatalogRequestCount).toBe(2);

  releaseFailedRetry();
  await expect(retryButton).toBeFocused();
  await expect(retryButton).not.toHaveAttribute("aria-disabled", "true");
  expect(state.projectCatalogRequestCount).toBe(2);

  state.projectCatalogFailuresRemaining = 1;
  state.projectCatalogRequestCount = 0;
  let releaseSuccessfulRetry!: () => void;
  state.projectCatalogRetryGate = new Promise<void>((resolve) => {
    releaseSuccessfulRetry = resolve;
  });
  await page.reload();
  await expect(page.getByText("Projects unavailable", { exact: true })).toBeVisible();
  await retryButton.focus();
  await retryButton.click();
  await expect(retryingButton).toBeFocused();

  const chatsLink = page.getByRole("link", { name: "Chats" });
  await chatsLink.focus();
  await expect(chatsLink).toBeFocused();
  releaseSuccessfulRetry();

  await expect(page.getByRole("status").filter({ hasText: "Projects loaded." })).toHaveText(
    "Projects loaded.",
  );
  await expect(chatsLink).toBeFocused();
  await expect(page.getByRole("region", { name: "Project workspace content" })).not.toBeFocused();
  expect(state.projectCatalogRequestCount).toBe(2);
});

test("Project scope recovers the catalog outside the Projects workspace", async ({ page }) => {
  const state = await mockProjectJourneyAPIs(page);
  state.projectCatalogFailuresRemaining = 1;
  let releaseRetry!: () => void;
  state.projectCatalogRetryGate = new Promise<void>((resolve) => {
    releaseRetry = resolve;
  });

  await page.goto("/tasks");
  await expect(page.getByText("Projects could not be loaded.")).toBeVisible();
  const retryButton = page.getByRole("button", { name: "Retry" });
  await retryButton.focus();
  await retryButton.click();

  const retryingButton = page.getByRole("button", { name: "Retrying…" });
  await expect(retryingButton).toBeFocused();
  await expect(retryingButton).toHaveAttribute("aria-disabled", "true");
  releaseRetry();

  await expect(page.getByRole("status").filter({ hasText: "Projects loaded." })).toHaveText(
    "Projects loaded.",
  );
  await expect(page.getByRole("button", { name: "Expand projects" })).toBeFocused();
  await expect(page.getByRole("button", { name: /Retry/ })).toHaveCount(0);
  expect(state.projectCatalogRequestCount).toBe(2);
});

test("Projects supporting surfaces stay read-first at desktop and narrow widths", async ({
  page,
}) => {
  await page.clock.setFixedTime(new Date(NOW));
  const state = await mockProjectJourneyAPIs(page);
  await page.addInitScript(() => {
    window.localStorage.setItem("hecate.workspace", "projects");
  });

  await page.goto("/");
  await page.waitForSelector(".hecate-activitybar");
  await page.getByRole("button", { name: "Add" }).click();
  await page.getByLabel("Name").fill("Research operations");
  await page.getByLabel("Purpose").fill("Coordinate research notes and reusable guidance.");
  await page.getByRole("button", { name: "Create project" }).click();

  await page.setViewportSize({ width: 390, height: 844 });
  const onboarding = page.getByRole("region", { name: "Project onboarding" });
  const onboardingSettings = onboarding.getByRole("button", { name: "Project settings" });
  await onboardingSettings.click();
  let settings = page.getByRole("complementary", { name: "Project settings panel" });
  await expect(settings).toBeVisible();
  await expect(settings.getByRole("heading", { level: 1, name: "Project settings" })).toBeFocused();
  await expect(settings.getByRole("button", { name: "Back to project" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Project workspace content" })).toBeHidden();
  expect(await settings.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(
    true,
  );
  await settings.getByRole("button", { name: "Back to project" }).click();
  await expect(settings).toBeHidden();
  await expect(page.getByRole("region", { name: "Project workspace content" })).toBeVisible();
  await expect(onboardingSettings).toBeFocused();

  await onboardingSettings.click();
  settings = page.getByRole("complementary", { name: "Project settings panel" });
  await settings.getByLabel("Workspace behavior").selectOption("persistent");
  await settings.getByRole("button", { name: "Save settings" }).click();
  await expect(settings).toBeHidden();
  await expect(onboardingSettings).toBeFocused();
  expect(state.projects[0]?.roots).toEqual([]);

  await onboarding.getByRole("button", { name: "Create work: First work item" }).click();
  await page.getByLabel("Title").fill("Organize research notes");
  await page.getByLabel("Brief").fill("Collect the confirmed findings and source guidance.");
  await page.getByRole("button", { name: "Create work item" }).click();
  await expect(page.getByRole("tab", { name: /Work/ })).toHaveAttribute("aria-selected", "true");

  await page.getByRole("tab", { name: /Memory/ }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Memory" })).toBeVisible();
  let sourcesDisclosure = page.locator("details.project-support-collection").filter({
    hasText: "Sources",
  });
  await expect(sourcesDisclosure).not.toHaveAttribute("open", "");
  await sourcesDisclosure.locator(":scope > summary").click();
  await expect(sourcesDisclosure.getByRole("button", { name: "Find from folders" })).toBeDisabled();
  await expect(
    sourcesDisclosure.getByRole("button", { name: "Find from folders" }),
  ).toHaveAttribute("title", "Attach or enable a folder first");
  await expect(sourcesDisclosure.getByRole("button", { name: "Add source" })).toBeEnabled();

  await page.getByRole("button", { name: "Add memory" }).click();
  const memoryDialog = page.getByRole("dialog", { name: "New project memory" });
  await memoryDialog.getByLabel("Title").fill("Research principle");
  await memoryDialog
    .getByLabel("Body")
    .fill(
      "Record only findings that the operator confirmed. Keep uncertain notes in review until evidence and provenance are clear, then save reusable guidance for future work.",
    );
  expect(
    await memoryDialog.evaluate((element) => element.scrollWidth <= element.clientWidth + 1),
  ).toBe(true);
  await memoryDialog.getByRole("button", { name: "Create memory" }).click();
  const memoryEntry = page.getByRole("article", { name: "Memory Research principle" });
  await expect(memoryEntry).toBeVisible();
  expect(
    await memoryEntry.evaluate((element) => element.scrollWidth <= element.clientWidth + 1),
  ).toBe(true);
  expect(
    await page
      .getByRole("region", { name: "Project memory" })
      .evaluate((element) => element.scrollWidth <= element.clientWidth + 1),
  ).toBe(true);

  await page.getByRole("tab", { name: /Skills/ }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Skills" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Find skills" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Find skills" })).toHaveAttribute(
    "title",
    "Attach or enable a folder first",
  );
  await expect(page.getByText("No skills found")).toBeVisible();
  await expect(
    page.getByText(
      "Attach or enable a folder to find skills. Existing skills remain available below.",
    ),
  ).toBeVisible();
  expect(
    await page
      .getByRole("region", { name: "Project skills" })
      .evaluate((element) => element.scrollWidth <= element.clientWidth + 1),
  ).toBe(true);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
    ),
  ).toBe(true);
  expect(state.projects[0]?.roots).toEqual([]);

  await page.setViewportSize({ width: 1280, height: 720 });
  const projectSettingsButton = page.getByRole("button", { name: "Project settings" });
  await projectSettingsButton.click();
  settings = page.getByRole("complementary", { name: "Project settings panel" });
  await settings.getByRole("button", { name: "Add folder" }).click();
  await settings.getByRole("button", { name: "Save settings" }).click();
  await expect(settings).toBeHidden();
  await expect(projectSettingsButton).toBeFocused();

  await page.getByRole("tab", { name: /Memory/ }).click();
  sourcesDisclosure = page.locator("details.project-support-collection").filter({
    hasText: "Sources",
  });
  await sourcesDisclosure.locator(":scope > summary").click();
  await sourcesDisclosure.getByRole("button", { name: "Find from folders" }).click();
  await expect(page.getByRole("article", { name: "Source AGENTS.md" })).toBeVisible();
  await sourcesDisclosure.locator(":scope > summary").click();

  if (process.env.HECATE_CAPTURE_PROJECTS_SUPPORTING === "1") {
    await page.screenshot({
      path: "../docs/screenshots/projects-memory.jpg",
      type: "jpeg",
      quality: 90,
    });
  }

  await page.getByRole("tab", { name: /Skills/ }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Skills" })).toBeVisible();
  await page.getByRole("button", { name: "Find skills" }).click();
  const skill = page.getByRole("article", { name: "Skill Implementation" });
  await expect(skill).toBeVisible();
  const skillDetails = skill.getByText("Settings and source");
  await expect(skillDetails.locator("xpath=..")).not.toHaveAttribute("open", "");
  await skillDetails.click();
  await skill.getByLabel("Title for Implementation").fill("Implementation review");
  await skill.getByRole("button", { name: "Save Implementation" }).click();
  await expect
    .poll(() => state.skillMutationCalls)
    .toEqual([
      {
        skillID: "implementation",
        body: expect.objectContaining({ title: "Implementation review" }),
      },
    ]);
  await expect(page.getByRole("article", { name: "Skill Implementation review" })).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  const narrowSkill = page.getByRole("article", { name: "Skill Implementation review" });
  const narrowSkillDetails = narrowSkill.getByText("Settings and source");
  if ((await narrowSkillDetails.locator("xpath=..").getAttribute("open")) === null) {
    await narrowSkillDetails.click();
  }
  expect(
    await narrowSkill.evaluate((element) => element.scrollWidth <= element.clientWidth + 1),
  ).toBe(true);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
    ),
  ).toBe(true);

  await projectSettingsButton.click();
  settings = page.getByRole("complementary", { name: "Project settings panel" });
  await expect(settings).toBeVisible();
  await expect(settings.getByRole("heading", { level: 1, name: "Project settings" })).toBeFocused();
  await expect(settings.getByRole("button", { name: "Back to project" })).toBeVisible();
  await expect(settings.getByLabel("Workspace behavior")).toBeVisible();
  await expect(settings.getByRole("button", { name: "Save settings" })).toBeDisabled();
  await expect(page.getByRole("region", { name: "Project workspace content" })).toBeHidden();
  const settingsBox = await settings.boundingBox();
  expect(settingsBox?.width ?? 0).toBeGreaterThan(300);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
    ),
  ).toBe(true);

  if (process.env.HECATE_CAPTURE_PROJECTS_SUPPORTING === "1") {
    await page.screenshot({
      path: "../docs/screenshots/projects-settings-narrow.jpg",
      type: "jpeg",
      quality: 90,
    });
  }

  await settings.getByLabel("Workspace behavior").selectOption("ephemeral");
  await settings.getByRole("button", { name: "Save settings" }).click();
  await expect(settings).toBeHidden();
  await expect(projectSettingsButton).toBeFocused();

  await page.getByRole("button", { name: "Agent presets" }).click();
  const presets = page.getByRole("dialog", { name: "Agent presets" });
  await expect(presets).toBeVisible();
  expect(await presets.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(
    true,
  );
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
  await settings.getByRole("button", { name: "Save settings" }).click();
  await expect.poll(() => state.rootMutationCalls.map((call) => call.method)).toEqual(["POST"]);
  await expect(settings).toBeHidden();

  await projectSettingsButton.click();
  settings = page.getByRole("complementary", { name: "Project settings panel" });
  await settings
    .getByRole("checkbox", { name: "Active project root /tmp/hecate-e2e-project" })
    .uncheck();
  await settings.getByRole("button", { name: "Save settings" }).click();
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
  await page.getByText("Sources", { exact: true }).click();
  await page.getByRole("button", { name: "Add source", exact: true }).click();
  let sourceDialog = page.getByRole("dialog", { name: "New project source" });
  await sourceDialog.getByLabel("Title").fill("Launch brief");
  await sourceDialog.getByLabel("Locator").fill("https://example.test/brief");
  await sourceDialog.getByRole("button", { name: "Create source" }).click();
  await page.getByText("Sources", { exact: true }).click();
  await expect(page.getByText("Launch brief", { exact: true })).toBeVisible();

  await page
    .getByRole("article", { name: "Source Launch brief" })
    .getByText("Details and actions")
    .click();
  await page.getByRole("button", { name: "Edit source Launch brief" }).click();
  sourceDialog = page.getByRole("dialog", { name: "Edit project source" });
  await sourceDialog.getByLabel("Title").fill("Launch brief v2");
  await sourceDialog.getByLabel("Locator").fill("https://example.test/brief-v2");
  await sourceDialog.getByRole("button", { name: "Save source" }).click();
  await page.getByText("Sources", { exact: true }).click();
  await expect(page.getByText("Launch brief v2", { exact: true })).toBeVisible();

  const updatedSource = page.getByRole("article", { name: "Source Launch brief v2" });
  const updatedSourceDetails = updatedSource.getByText("Details and actions");
  if (
    !(await updatedSourceDetails.evaluate((element) => element.parentElement?.hasAttribute("open")))
  ) {
    await updatedSourceDetails.click();
  }
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
    memoryEntries: [] as ProjectMemoryRecord[],
    memoryCandidates: [] as ProjectMemoryCandidateRecord[],
    skillMutationCalls: [] as Array<{ skillID: string; body: Record<string, unknown> }>,
    workItems: [] as ProjectWorkItemRecord[],
    assignments: [] as ProjectAssignmentRecord[],
    artifacts: [] as ProjectCollaborationArtifactRecord[],
    handoffs: [] as ProjectHandoffRecord[],
    projectPatchBodies: [] as Record<string, unknown>[],
    projectCatalogFailuresRemaining: 0,
    projectCatalogRequestCount: 0,
    projectCatalogRetryGate: null as Promise<void> | null,
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
    if (request.method() !== "POST") {
      failUnexpectedProjectJourneyRequest(route);
    }
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
    failUnexpectedProjectJourneyRequest(route);
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
        state.projectCatalogRequestCount += 1;
        if (state.projectCatalogRequestCount > 1 && state.projectCatalogRetryGate) {
          const gate = state.projectCatalogRetryGate;
          state.projectCatalogRetryGate = null;
          await gate;
        }
        if (state.projectCatalogFailuresRemaining > 0) {
          state.projectCatalogFailuresRemaining -= 1;
          await route.fulfill(
            ok(
              {
                error: {
                  type: "projects_unavailable",
                  message: "Projects are unavailable.",
                },
              },
              503,
            ),
          );
          return;
        }
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
      failUnexpectedProjectJourneyRequest(route);
    }

    const projectID = parts[0] || "";
    if (!projectID || projectID !== state.projects[0]?.id) {
      failUnexpectedProjectJourneyRequest(route);
    }

    const resource = parts[1];
    if (!resource && method === "PATCH") {
      const patch = JSON.parse(request.postData() || "{}") as Record<string, unknown>;
      state.projectPatchBodies.push(patch);
      state.projects[0] = { ...state.projects[0], ...patch, updated_at: NOW };
      await route.fulfill(ok({ object: "project", data: state.projects[0] }));
      return;
    }
    if (!resource) {
      failUnexpectedProjectJourneyRequest(route);
    }
    if (resource === "roots") {
      await handleProjectRootRoute(route, state, parts, method, ok);
      return;
    }
    if (resource === "context-sources") {
      if (parts[2] !== "discover") {
        await handleProjectContextSourceRoute(route, state, parts, method, ok);
        return;
      }
      if (parts.length === 3 && method === "POST") {
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
        state.projects[0] = {
          ...state.projects[0],
          context_sources: state.sources,
          updated_at: NOW,
        };
        await route.fulfill(ok({ object: "project", data: state.projects[0] }));
        return;
      }
      failUnexpectedProjectJourneyRequest(route);
    }
    if (resource === "skills") {
      if (parts.length === 3 && parts[2] === "discover" && method === "POST") {
        state.skills = [
          {
            id: "implementation",
            project_id: projectID,
            title: "Implementation",
            description: "Build and verify changes.",
            path: `skill-${"x".repeat(180)}`,
            root_id: `root_${"r".repeat(180)}`,
            format: "skill_md",
            enabled: true,
            status: "available",
            trust_label: "workspace_skill",
            source_context_source_ids: [`ctx_${"s".repeat(180)}`],
            suggested_tools: [`tool.${"t".repeat(180)}`],
            warnings: [`Review ${"w".repeat(180)}`],
            discovered_at: NOW,
            created_at: NOW,
            updated_at: NOW,
          },
        ];
        await route.fulfill(ok({ object: "project_skills", data: state.skills }));
        return;
      }
      if (parts.length === 2 && method === "GET") {
        await route.fulfill(ok({ object: "project_skills", data: state.skills }));
        return;
      }
      if (parts.length === 3 && method === "PATCH") {
        const skillID = parts[2] || "";
        const body = JSON.parse(request.postData() || "{}") as Record<string, unknown>;
        state.skillMutationCalls.push({ skillID, body });
        const skill = state.skills.find((item) => item.id === skillID);
        if (!skill) {
          await route.fulfill(ok({ object: "project_skill", data: null }, 404));
          return;
        }
        Object.assign(skill, body, { updated_at: NOW });
        await route.fulfill(ok({ object: "project_skill", data: skill }));
        return;
      }
      failUnexpectedProjectJourneyRequest(route);
    }
    if (resource === "memory") {
      if (parts[2] === "candidates") {
        const candidateID = parts[3] || "";
        if (candidateID && parts.length === 5 && parts[4] === "reject" && method === "POST") {
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
        if (!candidateID && parts.length === 3 && method === "GET") {
          await route.fulfill(
            ok({ object: "project_memory_candidates", data: state.memoryCandidates }),
          );
          return;
        }
        failUnexpectedProjectJourneyRequest(route);
      }
      if (parts.length === 2 && method === "GET") {
        await route.fulfill(ok({ object: "project_memory", data: state.memoryEntries }));
        return;
      }
      if (parts.length === 2 && method === "POST") {
        const body = JSON.parse(request.postData() || "{}") as Partial<ProjectMemoryRecord>;
        const entry: ProjectMemoryRecord = {
          id: `mem_${state.memoryEntries.length + 1}`,
          scope: "project",
          project_id: projectID,
          title: body.title || "Project memory",
          body: body.body || "",
          trust_label: body.trust_label || "operator_memory",
          source_kind: body.source_kind || "operator",
          source_id: body.source_id,
          enabled: body.enabled ?? true,
          created_at: NOW,
          updated_at: NOW,
        };
        state.memoryEntries = [...state.memoryEntries, entry];
        await route.fulfill(ok({ object: "project_memory", data: entry }, 201));
        return;
      }
      failUnexpectedProjectJourneyRequest(route);
    }
    if (resource === "roles" && parts.length === 2 && method === "GET") {
      await route.fulfill(ok({ object: "project_roles", data: state.roles }));
      return;
    }
    if (resource === "activity" && parts.length === 2 && method === "GET") {
      await route.fulfill(ok({ object: "project_activity", data: projectActivity(state) }));
      return;
    }
    if (resource === "health" && parts.length === 2 && method === "GET") {
      await route.fulfill(ok({ object: "project_health", data: projectHealth(state, projectID) }));
      return;
    }
    if (resource === "setup-readiness" && parts.length === 2 && method === "GET") {
      await route.fulfill(
        ok({ object: "project_setup_readiness", data: projectSetupReadiness(state, projectID) }),
      );
      return;
    }
    if (
      resource === "operations" &&
      parts.length === 3 &&
      parts[2] === "brief" &&
      method === "GET"
    ) {
      await route.fulfill(
        ok({ object: "project_operations_brief", data: projectOperationsBrief(state, projectID) }),
      );
      return;
    }
    if (resource === "work-items") {
      await handleWorkItemRoute(route, state, parts, method, projectID, ok);
      return;
    }

    failUnexpectedProjectJourneyRequest(route);
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
  failUnexpectedProjectJourneyRequest(route);
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
  failUnexpectedProjectJourneyRequest(route);
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
    failUnexpectedProjectJourneyRequest(route);
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
    failUnexpectedProjectJourneyRequest(route);
  }

  if (subresource === "readiness") {
    if (parts.length === 4 && method === "GET") {
      await route.fulfill(
        ok({
          object: "project_work_item_readiness",
          data: projectWorkItemReadiness(state, item),
        }),
      );
      return;
    }
    failUnexpectedProjectJourneyRequest(route);
  }

  if (subresource === "assignments") {
    const assignmentID = parts[4] || "";
    if (!assignmentID) {
      if (method === "POST") {
        const body = JSON.parse(request.postData() || "{}") as {
          role_id?: string;
          root_id?: string;
          driver_kind?: ProjectAssignmentRecord["driver_kind"];
        };
        const assignment: ProjectAssignmentRecord = {
          id: "assign_human",
          project_id: projectID,
          work_item_id: workItemID,
          role_id: body.role_id || state.roles[0]?.id || "",
          driver_kind: body.driver_kind || "hecate_task",
          status: "queued",
          created_at: NOW,
          updated_at: NOW,
          ...(body.root_id ? { root_id: body.root_id } : {}),
        };
        state.assignments = [...state.assignments, assignment];
        await route.fulfill(ok({ object: "project_assignment", data: assignment }, 201));
        return;
      }
      if (method === "GET") {
        await route.fulfill(
          ok({
            object: "project_assignments",
            data: state.assignments.filter((candidate) => candidate.work_item_id === workItemID),
          }),
        );
        return;
      }
      failUnexpectedProjectJourneyRequest(route);
    }
    const assignment = state.assignments.find(
      (candidate) => candidate.id === assignmentID && candidate.work_item_id === workItemID,
    );
    if (!assignment) {
      await route.fulfill(ok({ object: "project_assignment", data: null }, 404));
      return;
    }
    if (parts.length === 5 && method === "PATCH") {
      const patch = JSON.parse(request.postData() || "{}") as Partial<ProjectAssignmentRecord>;
      Object.assign(assignment, patch, {
        updated_at: NOW,
        ...(patch.status === "completed" ? { completed_at: NOW } : {}),
        ...(patch.status && assignment.driver_kind !== "manual"
          ? {
              execution_ref: {
                ...(assignment.execution_ref || { kind: "none" }),
                status: patch.status,
              },
            }
          : {}),
      });
      if (assignment.driver_kind === "manual") delete assignment.execution_ref;
      await route.fulfill(ok({ object: "project_assignment", data: assignment }));
      return;
    }
    if (parts.length === 6 && parts[5] === "launch-readiness" && method === "GET") {
      await route.fulfill(
        ok({
          object: "project_assignment_launch_readiness",
          data: assignmentLaunchReadiness(projectID, workItemID, assignmentID),
        }),
      );
      return;
    }
    if (parts.length === 6 && parts[5] === "preflight" && method === "GET") {
      await route.fulfill(
        ok({
          object: "context_packet",
          data: assignmentPreflight(projectID, workItemID, assignmentID),
        }),
      );
      return;
    }
    if (parts.length === 6 && parts[5] === "start" && method === "POST") {
      if (assignment.driver_kind === "manual") {
        delete assignment.execution_ref;
        Object.assign(assignment, {
          status: "running",
          started_at: NOW,
          updated_at: NOW,
          execution: undefined,
        });
        await route.fulfill(ok({ object: "project_assignment", data: assignment }));
        return;
      }
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
    failUnexpectedProjectJourneyRequest(route);
  }

  if (subresource === "artifacts") {
    if (parts.length === 4 && method === "GET") {
      await route.fulfill(
        ok({
          object: "project_collaboration_artifacts",
          data: state.artifacts.filter((artifact) => artifact.work_item_id === workItemID),
        }),
      );
      return;
    }
    if (parts.length === 4 && method === "POST") {
      const body = JSON.parse(
        request.postData() || "{}",
      ) as Partial<ProjectCollaborationArtifactRecord>;
      const artifact: ProjectCollaborationArtifactRecord = {
        id:
          state.artifacts.length === 0
            ? "artifact_launch"
            : `artifact_${state.artifacts.length + 1}`,
        project_id: projectID,
        work_item_id: workItemID,
        assignment_id: body.assignment_id,
        kind: body.kind || "evidence_link",
        title: body.title || "Launch checklist",
        body: body.body || "Operator confirmed the launch checklist evidence.",
        author_role_id: body.author_role_id,
        evidence_source_kind: body.evidence_source_kind,
        evidence_url: body.evidence_url,
        evidence_external_id: body.evidence_external_id,
        evidence_provider: body.evidence_provider,
        evidence_trust_label: body.evidence_trust_label,
        reviewed_assignment_id: body.reviewed_assignment_id,
        review_verdict: body.review_verdict,
        review_risk: body.review_risk,
        review_follow_up_required: body.review_follow_up_required,
        created_at: NOW,
        updated_at: NOW,
      };
      state.artifacts = [...state.artifacts, artifact];
      await route.fulfill(ok({ object: "project_collaboration_artifact", data: artifact }, 201));
      return;
    }
    failUnexpectedProjectJourneyRequest(route);
  }

  if (subresource === "handoffs") {
    const handoffID = parts[4] || "";
    if (!handoffID && parts.length === 4 && method === "GET") {
      await route.fulfill(
        ok({
          object: "project_handoffs",
          data: state.handoffs.filter((handoff) => handoff.work_item_id === workItemID),
        }),
      );
      return;
    }
    const handoff = state.handoffs.find(
      (candidate) => candidate.id === handoffID && candidate.work_item_id === workItemID,
    );
    if (handoff && parts.length === 6 && parts[5] === "status" && method === "POST") {
      const body = JSON.parse(request.postData() || "{}") as { status?: string };
      Object.assign(handoff, {
        status: body.status || handoff.status,
        status_changed_at: NOW,
        updated_at: NOW,
      });
      await route.fulfill(ok({ object: "project_handoff", data: handoff }));
      return;
    }
    if (!handoff && handoffID) {
      await route.fulfill(ok({ object: "project_handoff", data: null }, 404));
      return;
    }
    failUnexpectedProjectJourneyRequest(route);
  }

  failUnexpectedProjectJourneyRequest(route);
}

function failUnexpectedProjectJourneyRequest(route: Route): never {
  const request = route.request();
  const url = new URL(request.url());
  throw new Error(
    `Unexpected staged Projects request: ${request.method()} ${url.pathname}${url.search}`,
  );
}

type ProjectJourneyState = Awaited<ReturnType<typeof mockProjectJourneyAPIs>>;

function seedNavigationProject(state: ProjectJourneyState) {
  const project: ProjectRecord = {
    id: "proj_navigation",
    name: "Navigation operations",
    description: "Coordinate shareable project work.",
    roots: [],
    context_sources: [],
    default_provider: "anthropic",
    default_model: "claude-sonnet-4-6",
    created_at: NOW,
    updated_at: NOW,
  };
  const role: ProjectWorkRoleRecord = {
    id: "navigation_operator",
    project_id: project.id,
    name: "Navigation operator",
    description: "Verifies project routing behavior.",
    default_driver_kind: "human",
    created_at: NOW,
    updated_at: NOW,
  };
  const firstWorkItem: ProjectWorkItemRecord = {
    id: "work_first",
    project_id: project.id,
    title: "First queued item",
    brief: "This item must not replace an explicit link target.",
    status: "ready",
    priority: "normal",
    owner_role_id: role.id,
    created_at: NOW,
    updated_at: NOW,
  };
  const secondWorkItem: ProjectWorkItemRecord = {
    ...firstWorkItem,
    id: "work_second",
    title: "Linked review target",
    brief: "Restore this exact work item from its project link.",
    priority: "high",
  };
  state.projects = [project];
  state.roles = [role];
  state.workItems = [firstWorkItem, secondWorkItem];
  return { firstWorkItem, project, secondWorkItem };
}

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
  const handoffs = state.handoffs.filter((handoff) => handoff.work_item_id === workItem.id);
  const completedAssignments = assignments.filter(
    (assignment) => (assignment.execution_ref?.status || assignment.status) === "completed",
  );
  const reviewFollowUps = artifacts.filter(
    (artifact) => artifact.kind === "review" && artifact.review_follow_up_required,
  );
  const openHandoffs = handoffs.filter((handoff) => handoff.status === "pending");
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
  if (openHandoffs.length > 0) {
    blockers.push(
      `${openHandoffs.length} handoff${openHandoffs.length === 1 ? " is" : "s are"} pending`,
    );
  }
  if (reviewFollowUps.length > 0) {
    blockers.push(
      `${reviewFollowUps.length} review follow-up${reviewFollowUps.length === 1 ? " needs" : "s need"} a path`,
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
    review_follow_up_count: reviewFollowUps.length,
    review_follow_up_artifact_ids: reviewFollowUps.map((artifact) => artifact.id),
    review_follow_ups: reviewFollowUps.map((artifact) => ({
      artifact_id: artifact.id,
      title: artifact.title || artifact.id,
      status: "needs_path",
      reviewed_assignment_id: artifact.reviewed_assignment_id,
      review_verdict: artifact.review_verdict,
      review_risk: artifact.review_risk,
    })),
    missing_evidence_assignment_ids: missingEvidenceAssignments.map((assignment) => assignment.id),
    open_handoff_ids: openHandoffs.map((handoff) => handoff.id),
  };
}

function projectActivity(state: ProjectJourneyState): ProjectActivityData {
  const items: ProjectActivityData["recent"] = [];
  for (const assignment of state.assignments) {
    const role = state.roles.find((candidate) => candidate.id === assignment.role_id);
    const workItem = state.workItems.find((candidate) => candidate.id === assignment.work_item_id);
    if (!role || !workItem) continue;
    const status = assignment.execution_ref?.status || assignment.status || "";
    const completed = status === "completed";
    const artifacts = state.artifacts.filter((artifact) => artifact.work_item_id === workItem.id);
    const handoffs = state.handoffs.filter((handoff) => handoff.work_item_id === workItem.id);
    items.push({
      id: assignment.id,
      project_id: state.projects[0]?.id || "proj_launch",
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
        count: artifacts.length,
        latest_kind: artifacts[0]?.kind,
        latest_title: artifacts[0]?.title,
        latest_at: artifacts[0]?.updated_at,
      },
      handoff_summary: { count: handoffs.length },
      recent_artifacts: artifacts,
      updated_at: assignment.updated_at,
    });
  }
  const completed = items.filter((item) => item.blocking_signal === "completed");
  const blocked = items.filter((item) =>
    ["not_started", "awaiting_approval", "failed", "cancelled"].includes(item.blocking_signal),
  );
  const active = items.filter((item) => !completed.includes(item) && !blocked.includes(item));
  return {
    project_id: state.projects[0]?.id || "proj_launch",
    summary: {
      work_item_count: state.workItems.length,
      assignment_count: state.assignments.length,
      active_count: active.length,
      blocked_count: blocked.length,
      completed_count: completed.length,
      recent_count: items.length,
    },
    buckets: {
      active,
      blocked,
      completed,
      recent: items,
    },
    recent: items,
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
  if (state.workItems.length === 0) {
    return projectOperationsBriefForScope(state, projectID);
  }

  const itemsByID = new Map<string, ProjectOperationsBriefItem>();
  for (const workItem of state.workItems) {
    const assignments = state.assignments.filter(
      (assignment) => assignment.work_item_id === workItem.id,
    );
    const assignmentScopes: Array<ProjectAssignmentRecord | null> =
      assignments.length > 0 ? assignments : [null];
    for (const assignment of assignmentScopes) {
      const scopedState: ProjectJourneyState = {
        ...state,
        workItems: [workItem],
        assignments: assignment ? [assignment] : [],
        artifacts: state.artifacts.filter((artifact) => artifact.work_item_id === workItem.id),
        handoffs: state.handoffs.filter((handoff) => handoff.work_item_id === workItem.id),
        memoryCandidates: [],
      };
      const scoped = projectOperationsBriefForScope(scopedState, projectID);
      const readiness = projectWorkItemReadiness(state, workItem);
      for (const item of scoped.items) {
        if (item.kind === "close_work_item" && !readiness.ready) continue;
        itemsByID.set(item.id, item);
      }
    }
  }

  const memoryItems = projectOperationsBriefForScope(
    {
      ...state,
      workItems: [],
      assignments: [],
      artifacts: [],
      handoffs: [],
    },
    projectID,
  ).items.filter((item) => item.kind === "review_memory_candidates");
  for (const item of memoryItems) itemsByID.set(item.id, item);

  let items = [...itemsByID.values()];
  if (items.some((item) => item.kind !== "open_latest_work")) {
    items = items.filter((item) => item.kind !== "open_latest_work");
  } else {
    items = items.slice(0, 1);
  }
  sortProjectOperationItems(items);

  const pendingMemoryCandidateCount = state.memoryCandidates.filter(
    (candidate) => candidate.status === "pending",
  ).length;
  return {
    project_id: projectID,
    generated_at: NOW,
    summary: {
      item_count: items.length,
      medium_count: items.filter((item) => item.priority === "medium").length,
      high_count: items.filter((item) => item.priority === "high").length,
      low_count: items.filter((item) => item.priority === "low").length,
      pending_memory_candidate_count: pendingMemoryCandidateCount,
      pending_handoff_count: state.handoffs.filter((handoff) => handoff.status === "pending")
        .length,
    },
    items,
  };
}

function projectOperationsBriefForScope(state: ProjectJourneyState, projectID: string) {
  const workItem = state.workItems[0];
  const assignment = state.assignments.find((candidate) => candidate.work_item_id === workItem?.id);
  const artifacts = state.artifacts.filter((artifact) => artifact.work_item_id === workItem?.id);
  const pendingHandoff = state.handoffs.find(
    (handoff) => handoff.work_item_id === workItem?.id && handoff.status === "pending",
  );
  const reviewFollowUp = artifacts.find(
    (artifact) => artifact.kind === "review" && artifact.review_follow_up_required,
  );
  const assignmentStatus = assignment?.execution_ref?.status || assignment?.status || "";
  const hasEvidence = artifacts.some(
    (artifact) =>
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
  } else if (workItem.status === "done") {
    // Cairnline only adds latest work when no higher-value operation exists.
  } else if (pendingHandoff) {
    const assignmentID =
      pendingHandoff.target_assignment_id || pendingHandoff.source_assignment_id || undefined;
    items = [
      {
        id: `review_pending_handoff:${projectID}:${pendingHandoff.id}`,
        kind: "review_pending_handoff",
        priority: "medium",
        status: pendingHandoff.status,
        title: `Review pending handoff: ${pendingHandoff.title}`,
        detail:
          pendingHandoff.recommended_next_action ||
          pendingHandoff.summary ||
          "Review the handoff and decide the next assignment.",
        action_label: "Open handoff",
        target: {
          surface: "work",
          project_id: projectID,
          work_item_id: workItem.id,
          assignment_id: assignmentID,
          handoff_id: pendingHandoff.id,
        },
        action: {
          type: "open_work_item",
          project_id: projectID,
          work_item_id: workItem.id,
          assignment_id: assignmentID,
          handoff_id: pendingHandoff.id,
        },
        handoff: pendingHandoff,
      },
    ];
  } else if (reviewFollowUp) {
    items = [
      {
        id: `review_follow_up:${projectID}:${reviewFollowUp.id}`,
        kind: "review_follow_up",
        priority: "medium",
        status: "awaiting_approval",
        title: `Review follow-up: ${workItem.title}`,
        detail: `Review artifact ${reviewFollowUp.title || reviewFollowUp.id} needs a follow-up path before closeout.`,
        action_label: "Open review",
        target: {
          surface: "work",
          project_id: projectID,
          work_item_id: workItem.id,
          artifact_id: reviewFollowUp.id,
        },
        action: {
          type: "open_work_item",
          project_id: projectID,
          work_item_id: workItem.id,
          artifact_id: reviewFollowUp.id,
        },
        metadata: { artifact_id: reviewFollowUp.id },
      },
    ];
  } else if (!assignment) {
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
  } else if (workItem && assignment && assignmentStatus === "queued") {
    const humanAssignment = assignment.driver_kind === "manual";
    items = [
      {
        id: `start_queued_assignment:${projectID}:${assignment.id}`,
        kind: "start_queued_assignment",
        priority: "high",
        status: "not_started",
        title: humanAssignment
          ? `Human work ready: ${workItem.title}`
          : `Review queued assignment: ${workItem.title}`,
        detail: humanAssignment
          ? "This assignment is ready for a person to begin."
          : "Review launch details before starting this assignment.",
        action_label: humanAssignment ? "Open work" : "Review start",
        target: {
          surface: "work",
          project_id: projectID,
          work_item_id: workItem.id,
          assignment_id: assignment.id,
          activity_bucket: "blocked",
        },
        action: humanAssignment
          ? {
              type: "open_work_item",
              project_id: projectID,
              work_item_id: workItem.id,
              assignment_id: assignment.id,
              activity_bucket: "blocked",
            }
          : {
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
          assignment_id: assignment.id,
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
          assignment_id: assignment.id,
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
          assignment_id: assignment.id,
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
    review_pending_handoff: 60,
    review_follow_up: 65,
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
      pending_handoff_count: state.handoffs.filter(
        (handoff) => handoff.work_item_id === workItem?.id && handoff.status === "pending",
      ).length,
    },
    items,
  };
}

function sortProjectOperationItems(items: ProjectOperationsBriefItem[]) {
  const priorityRank = { high: 0, medium: 1, low: 2 } as const;
  const kindRank: Record<string, number> = {
    approve_assignment: 0,
    review_failed_assignment: 10,
    start_queued_assignment: 30,
    review_cancelled_assignment: 50,
    review_pending_handoff: 60,
    review_follow_up: 65,
    prepare_first_assignment: 70,
    create_first_work_item: 80,
    review_memory_candidates: 90,
    inspect_active_assignment: 100,
    record_completion_evidence: 110,
    close_work_item: 120,
    open_latest_work: 130,
  };
  items.sort((left, right) => {
    const leftPriority = priorityRank[left.priority as keyof typeof priorityRank] ?? 3;
    const rightPriority = priorityRank[right.priority as keyof typeof priorityRank] ?? 3;
    const byPriority = leftPriority - rightPriority;
    if (byPriority !== 0) return byPriority;
    const byKind = (kindRank[left.kind] ?? 1_000) - (kindRank[right.kind] ?? 1_000);
    return byKind !== 0 ? byKind : left.id.localeCompare(right.id);
  });
}
