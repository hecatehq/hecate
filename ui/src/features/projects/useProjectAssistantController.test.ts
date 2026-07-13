import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import * as api from "../../lib/api";
import type {
  ProjectAssistantContextRecord,
  ProjectAssistantProposal,
  ProjectRecord,
  ProjectWorkItemRecord,
} from "../../types/project";
import {
  projectAssistantApplyErrorMessage,
  projectAssistantContextPayload,
  projectAssistantDraftPayload,
  projectAssistantResultWorkItemID,
  useProjectAssistantController,
} from "./useProjectAssistantController";

describe("Project Assistant controller helpers", () => {
  afterEach(() => {
    window.sessionStorage.clear();
    vi.restoreAllMocks();
  });

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
    expect(
      projectAssistantApplyErrorMessage(new api.ApiError("conflict", 409, "conflict")),
    ).toContain("proposal is stale");

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
      new api.ApiError("partial", 409, "conflict", {
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
      new api.ApiError("blocked", 404, "not_found", {
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
      new api.ApiError("blocked", 404, "not_found", {
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
      new api.ApiError("partial", 409, "conflict", {
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

  it("restores the last proposal record for the selected project", async () => {
    const proposal: ProjectAssistantProposal = {
      id: "pa_recover",
      title: "Recover proposal",
      summary: "",
      requires_confirmation: true,
      actions: [
        { kind: "create_work_item", patch: {} },
        { kind: "create_assignment", patch: {} },
      ],
    };
    vi.spyOn(api, "getProjectAssistantProposal").mockResolvedValue({
      object: "project_assistant.proposal_record",
      data: {
        id: "pa_recover",
        project_id: "proj_1",
        proposal,
        status: "partial_due_to_runtime_failure",
        latest_result: {
          proposal_id: "pa_recover",
          status: "partial_due_to_runtime_failure",
          applied: false,
          actions: [{ kind: "create_work_item", id: "work_1" }],
          total_action_count: 2,
          committed_action_count: 1,
          failed_action_index: 1,
          resume_action_index: 1,
        },
        apply_attempts: [],
        created_at: "2026-06-22T10:00:00Z",
        updated_at: "2026-06-22T10:01:00Z",
      },
    });
    window.sessionStorage.setItem("hecate.projectAssistant.lastProposal.proj_1", "pa_recover");

    const { result } = renderHook(() =>
      useProjectAssistantController({
        project: {
          id: "proj_1",
          name: "Project",
          roots: [],
          created_at: "2026-06-22T10:00:00Z",
          updated_at: "2026-06-22T10:00:00Z",
        },
        selectedProjectID: "proj_1",
        selectedWorkItemID: "",
        selectedWorkItem: null,
        onProjectDiscovered: vi.fn(),
        onSkillsDiscovered: vi.fn(),
        onSkillsLoadState: vi.fn(),
        onDiscoveringContext: vi.fn(),
        onDiscoveringSkills: vi.fn(),
        onMemoryError: vi.fn(),
        onSkillsError: vi.fn(),
        refreshProjects: vi.fn(async () => undefined),
        loadWorkForProject: vi.fn(async () => ""),
        loadWorkItemDetail: vi.fn(async () => undefined),
        loadProjectMemory: vi.fn(async () => undefined),
      }),
    );

    await waitFor(() => expect(result.current.proposal?.id).toBe("pa_recover"));
    expect(result.current.error).toContain("applied 1 of 2 actions");
    expect(result.current.error).toContain("failed at action 2 (create assignment)");
  });

  it("preserves a targeted proposal through delayed work hydration without showing it on other work", async () => {
    const proposal: ProjectAssistantProposal = {
      ...testProposal("pa_targeted_recovery"),
      actions: [
        {
          kind: "create_assignment",
          patch: { project_id: "proj_a", work_item_id: "work_a" },
        },
      ],
    };
    const proposalSpy = vi.spyOn(api, "getProjectAssistantProposal").mockResolvedValue({
      object: "project_assistant.proposal_record",
      data: {
        id: proposal.id,
        project_id: "proj_a",
        proposal,
        status: "proposed",
        apply_attempts: [],
        created_at: "2026-06-22T10:00:00Z",
        updated_at: "2026-06-22T10:00:00Z",
      },
    });
    window.sessionStorage.setItem("hecate.projectAssistant.lastProposal.proj_a", proposal.id);
    const dependencies = controllerDependencies();
    const { result, rerender } = renderHook(
      (options: ControllerOptions) => useProjectAssistantController(options),
      { initialProps: controllerOptions(dependencies, "proj_a", "") },
    );

    await waitFor(() => expect(proposalSpy).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(
        window.sessionStorage.getItem("hecate.projectAssistant.lastProposalTarget.proj_a"),
      ).toBe("work_a"),
    );
    expect(result.current.proposal).toBeNull();
    expect(window.sessionStorage.getItem("hecate.projectAssistant.lastProposal.proj_a")).toBe(
      proposal.id,
    );

    rerender(controllerOptions(dependencies, "proj_a", "work_b"));
    expect(result.current.proposal).toBeNull();
    expect(proposalSpy).toHaveBeenCalledTimes(1);

    rerender(controllerOptions(dependencies, "proj_a", "work_a"));
    await waitFor(() => expect(result.current.proposal?.id).toBe(proposal.id));
    expect(proposalSpy).toHaveBeenCalledTimes(2);
  });

  it("ignores stale proposal drafts and review follow-ups after selection changes", async () => {
    const proposalDraft = deferred<Awaited<ReturnType<typeof api.draftProjectAssistant>>>();
    const reviewDraft = deferred<Awaited<ReturnType<typeof api.draftProjectAssistant>>>();
    const draftSpy = vi
      .spyOn(api, "draftProjectAssistant")
      .mockImplementationOnce(() => proposalDraft.promise)
      .mockImplementationOnce(() => reviewDraft.promise);
    const dependencies = controllerDependencies();
    const { result, rerender } = renderHook(
      (options: ControllerOptions) => useProjectAssistantController(options),
      { initialProps: controllerOptions(dependencies, "proj_a", "work_a") },
    );

    let pendingProposal!: Promise<void>;
    act(() => {
      pendingProposal = result.current.propose(testDraftForm());
    });
    expect(result.current.status).toBe("proposing");

    rerender(controllerOptions(dependencies, "proj_a", "work_b"));
    rerender(controllerOptions(dependencies, "proj_a", "work_a"));
    await act(async () => {
      proposalDraft.resolve(proposalResponse("pa_stale_proposal"));
      await pendingProposal;
    });

    expect(result.current.proposal).toBeNull();
    expect(result.current.status).toBe("idle");
    expect(result.current.error).toBe("");

    let pendingReview!: Promise<void>;
    act(() => {
      pendingReview = result.current.draftReviewFollowUp("artifact_a", "work_a");
    });
    rerender(controllerOptions(dependencies, "proj_b", "work_b"));
    await act(async () => {
      reviewDraft.resolve(proposalResponse("pa_stale_review"));
      await pendingReview;
    });

    expect(draftSpy).toHaveBeenNthCalledWith(
      1,
      expect.objectContaining({ project_id: "proj_a", work_item_id: "work_a" }),
    );
    expect(draftSpy).toHaveBeenNthCalledWith(2, {
      project_id: "proj_a",
      work_item_id: "work_a",
      request: "Create review follow-up",
      draft_mode: "review_follow_up",
      review_artifact_id: "artifact_a",
    });
    expect(result.current.proposal).toBeNull();
    expect(result.current.status).toBe("idle");
    expect(result.current.error).toBe("");
    expect(window.sessionStorage.getItem("hecate.projectAssistant.lastProposal.proj_b")).toBeNull();
  });

  it("keeps the newest assistant request on the same selection", async () => {
    const firstDraft = deferred<Awaited<ReturnType<typeof api.draftProjectAssistant>>>();
    const secondDraft = deferred<Awaited<ReturnType<typeof api.draftProjectAssistant>>>();
    vi.spyOn(api, "draftProjectAssistant")
      .mockImplementationOnce(() => firstDraft.promise)
      .mockImplementationOnce(() => secondDraft.promise);
    const dependencies = controllerDependencies();
    const { result } = renderHook(() =>
      useProjectAssistantController(controllerOptions(dependencies, "proj_a", "work_a")),
    );

    let firstPending!: Promise<void>;
    let secondPending!: Promise<void>;
    act(() => {
      firstPending = result.current.propose(testDraftForm());
      secondPending = result.current.propose(testDraftForm());
    });
    await act(async () => {
      secondDraft.resolve(proposalResponse("pa_newest"));
      await secondPending;
    });
    expect(result.current.proposal?.id).toBe("pa_newest");

    await act(async () => {
      firstDraft.resolve(proposalResponse("pa_older"));
      await firstPending;
    });
    expect(result.current.proposal?.id).toBe("pa_newest");
    expect(window.sessionStorage.getItem("hecate.projectAssistant.lastProposal.proj_a")).toBe(
      "pa_newest",
    );
  });

  it("keeps context inspection active while drafting a proposal", async () => {
    const contextRequest = deferred<Awaited<ReturnType<typeof api.getProjectAssistantContext>>>();
    const proposalDraft = deferred<Awaited<ReturnType<typeof api.draftProjectAssistant>>>();
    vi.spyOn(api, "getProjectAssistantContext").mockImplementation(() => contextRequest.promise);
    vi.spyOn(api, "draftProjectAssistant").mockImplementation(() => proposalDraft.promise);
    const dependencies = controllerDependencies();
    const { result } = renderHook(() =>
      useProjectAssistantController(controllerOptions(dependencies, "proj_a", "work_a")),
    );

    let pendingContext!: Promise<void>;
    act(() => {
      pendingContext = result.current.inspectContext(testDraftForm());
    });
    expect(result.current.contextStatus).toBe("loading");

    let pendingProposal!: Promise<void>;
    act(() => {
      pendingProposal = result.current.propose(testDraftForm());
    });
    await act(async () => {
      proposalDraft.reject(new Error("Draft service unavailable"));
      await pendingProposal;
    });

    expect(result.current.status).toBe("idle");
    expect(result.current.error).toBe("Draft service unavailable");
    expect(result.current.contextStatus).toBe("loading");

    await act(async () => {
      contextRequest.resolve({
        object: "project_assistant.context",
        data: testAssistantContext("proj_a", "work_a"),
      });
      await pendingContext;
    });

    expect(result.current.contextStatus).toBe("loaded");
    expect(result.current.context?.selected_work?.id).toBe("work_a");
  });

  it("keeps a target-qualified draft through its planned work selection change", async () => {
    const proposalDraft = deferred<Awaited<ReturnType<typeof api.draftProjectAssistant>>>();
    vi.spyOn(api, "draftProjectAssistant").mockImplementation(() => proposalDraft.promise);
    const dependencies = controllerDependencies();
    const { result, rerender } = renderHook(
      (options: ControllerOptions) => useProjectAssistantController(options),
      { initialProps: controllerOptions(dependencies, "proj_a", "work_a") },
    );

    let pending!: Promise<void>;
    act(() => {
      pending = result.current.propose(testDraftForm(), "work_b");
    });
    rerender(controllerOptions(dependencies, "proj_a", "work_b"));
    expect(result.current.status).toBe("proposing");
    await act(async () => {
      proposalDraft.resolve(proposalResponse("pa_work_b"));
      await pending;
    });

    expect(result.current.proposal?.id).toBe("pa_work_b");
    expect(result.current.status).toBe("idle");
    expect(window.sessionStorage.getItem("hecate.projectAssistant.lastProposal.proj_a")).toBe(
      "pa_work_b",
    );
  });

  it("ignores stale context inspection after leaving and returning to a selection", async () => {
    const contextRequest = deferred<Awaited<ReturnType<typeof api.getProjectAssistantContext>>>();
    const contextSpy = vi
      .spyOn(api, "getProjectAssistantContext")
      .mockImplementation(() => contextRequest.promise);
    const dependencies = controllerDependencies();
    const { result, rerender } = renderHook(
      (options: ControllerOptions) => useProjectAssistantController(options),
      { initialProps: controllerOptions(dependencies, "proj_a", "work_a") },
    );

    let pending!: Promise<void>;
    act(() => {
      pending = result.current.inspectContext(testDraftForm());
    });
    expect(result.current.contextStatus).toBe("loading");

    rerender(controllerOptions(dependencies, "proj_b", "work_b"));
    rerender(controllerOptions(dependencies, "proj_a", "work_a"));
    await act(async () => {
      contextRequest.resolve({
        object: "project_assistant.context",
        data: testAssistantContext("proj_a", "work_a"),
      });
      await pending;
    });

    expect(contextSpy).toHaveBeenCalledWith(
      expect.objectContaining({ project_id: "proj_a", work_item_id: "work_a" }),
    );
    expect(result.current.context).toBeNull();
    expect(result.current.contextStatus).toBe("idle");
    expect(result.current.contextError).toBe("");
  });

  it("stops stale bootstrap discovery before invoking dependent callbacks", async () => {
    const contextDiscovery =
      deferred<Awaited<ReturnType<typeof api.discoverProjectContextSources>>>();
    vi.spyOn(api, "discoverProjectContextSources").mockImplementation(
      () => contextDiscovery.promise,
    );
    const skillsSpy = vi.spyOn(api, "discoverProjectSkills");
    const draftSpy = vi.spyOn(api, "draftProjectAssistant");
    const dependencies = controllerDependencies();
    const { result, rerender } = renderHook(
      (options: ControllerOptions) => useProjectAssistantController(options),
      { initialProps: controllerOptions(dependencies, "proj_a", "work_a") },
    );

    let pending!: Promise<void>;
    act(() => {
      pending = result.current.bootstrap();
    });
    expect(result.current.bootstrapPending).toBe(true);

    rerender(controllerOptions(dependencies, "proj_b", "work_b"));
    await act(async () => {
      contextDiscovery.resolve({ object: "project", data: testProject("proj_a") });
      await pending;
    });

    expect(dependencies.onProjectDiscovered).not.toHaveBeenCalled();
    expect(skillsSpy).not.toHaveBeenCalled();
    expect(draftSpy).not.toHaveBeenCalled();
    expect(dependencies.onDiscoveringContext).toHaveBeenLastCalledWith(false);
    expect(dependencies.onDiscoveringSkills).toHaveBeenLastCalledWith(false);
    expect(result.current.bootstrapPending).toBe(false);
    expect(result.current.status).toBe("idle");
    expect(result.current.proposal).toBeNull();
  });

  it("ignores a stale bootstrap proposal after work changes", async () => {
    const bootstrapDraft = deferred<Awaited<ReturnType<typeof api.draftProjectAssistant>>>();
    vi.spyOn(api, "discoverProjectContextSources").mockResolvedValue({
      object: "project",
      data: testProject("proj_a"),
    });
    vi.spyOn(api, "discoverProjectSkills").mockResolvedValue({
      object: "project_skill.list",
      data: [],
    });
    const draftSpy = vi
      .spyOn(api, "draftProjectAssistant")
      .mockImplementation(() => bootstrapDraft.promise);
    const dependencies = controllerDependencies();
    const { result, rerender } = renderHook(
      (options: ControllerOptions) => useProjectAssistantController(options),
      { initialProps: controllerOptions(dependencies, "proj_a", "work_a") },
    );

    let pending!: Promise<void>;
    act(() => {
      pending = result.current.bootstrap();
    });
    await waitFor(() => expect(draftSpy).toHaveBeenCalledTimes(1));

    rerender(controllerOptions(dependencies, "proj_a", "work_b"));
    await act(async () => {
      bootstrapDraft.resolve(proposalResponse("pa_stale_bootstrap"));
      await pending;
    });

    expect(result.current.bootstrapPending).toBe(false);
    expect(result.current.status).toBe("idle");
    expect(result.current.proposal).toBeNull();
    expect(window.sessionStorage.getItem("hecate.projectAssistant.lastProposal.proj_a")).toBeNull();
  });

  it("reconciles a stale apply result after work changes without publishing it", async () => {
    const applyRequest = deferred<Awaited<ReturnType<typeof api.applyProjectAssistant>>>();
    const refreshRequest = deferred<void>();
    vi.spyOn(api, "applyProjectAssistant").mockImplementation(() => applyRequest.promise);
    const dependencies = controllerDependencies();
    dependencies.refreshProjects = vi.fn(() => refreshRequest.promise);
    const { result, rerender } = renderHook(
      (options: ControllerOptions) => useProjectAssistantController(options),
      { initialProps: controllerOptions(dependencies, "proj_a", "work_a") },
    );

    act(() => {
      result.current.loadProposal({
        ...testProposal("pa_apply"),
        actions: [
          {
            kind: "create_assignment",
            patch: { project_id: "proj_a", work_item_id: "work_a" },
          },
        ],
      });
    });
    let pending!: Promise<void>;
    act(() => {
      pending = result.current.apply();
    });
    expect(result.current.status).toBe("applying");

    await act(async () => {
      applyRequest.resolve({
        object: "project_assistant.apply_result",
        data: {
          proposal_id: "pa_apply",
          status: "applied",
          applied: true,
          actions: [
            {
              kind: "create_work_item",
              id: "work_created",
              data: { project_id: "proj_a", work_item_id: "work_created" },
            },
          ],
        },
      });
      await Promise.resolve();
    });
    await waitFor(() => expect(dependencies.refreshProjects).toHaveBeenCalledTimes(1));
    expect(result.current.status).toBe("applying");

    rerender(controllerOptions(dependencies, "proj_a", "work_b"));
    rerender(controllerOptions(dependencies, "proj_a", "work_a"));
    await act(async () => {
      refreshRequest.resolve();
      await pending;
    });

    expect(result.current.applyResult).toBeNull();
    expect(result.current.proposal).toBeNull();
    expect(result.current.status).toBe("idle");
    expect(result.current.error).toBe("");
    expect(dependencies.refreshProjects).toHaveBeenCalledTimes(1);
    expect(dependencies.loadProjectMemory).toHaveBeenCalledWith("proj_a");
    expect(dependencies.loadWorkForProject).toHaveBeenCalledWith("proj_a", "work_a");
    expect(dependencies.loadWorkItemDetail).toHaveBeenCalledWith("proj_a", "work_a");
    expect(window.sessionStorage.getItem("hecate.projectAssistant.lastProposal.proj_a")).toBe(
      "pa_apply",
    );
  });

  it("protects a confirmed apply from proposal entry points through reconciliation", async () => {
    const applyRequest = deferred<Awaited<ReturnType<typeof api.applyProjectAssistant>>>();
    const refreshRequest = deferred<void>();
    const applySpy = vi
      .spyOn(api, "applyProjectAssistant")
      .mockImplementation(() => applyRequest.promise);
    const draftSpy = vi.spyOn(api, "draftProjectAssistant");
    const contextDiscoverySpy = vi.spyOn(api, "discoverProjectContextSources");
    const skillDiscoverySpy = vi.spyOn(api, "discoverProjectSkills");
    const dependencies = controllerDependencies();
    dependencies.refreshProjects = vi.fn(() => refreshRequest.promise);
    const { result } = renderHook(() =>
      useProjectAssistantController(controllerOptions(dependencies, "proj_a", "work_a")),
    );

    act(() => {
      result.current.loadProposal(testProposal("pa_apply"));
    });
    let pending!: Promise<void>;
    act(() => {
      pending = result.current.apply();
    });
    expect(result.current.status).toBe("applying");

    await act(async () => {
      applyRequest.resolve({
        object: "project_assistant.apply_result",
        data: {
          proposal_id: "pa_apply",
          status: "applied",
          applied: true,
          actions: [],
        },
      });
      await Promise.resolve();
    });
    await waitFor(() => expect(dependencies.refreshProjects).toHaveBeenCalledTimes(1));
    expect(result.current.status).toBe("applying");
    expect(result.current.applyResult).toBeNull();

    let loadedReplacement = true;
    await act(async () => {
      loadedReplacement = result.current.loadProposal(testProposal("pa_replacement"));
      result.current.dismiss();
      await Promise.all([
        result.current.propose(testDraftForm()),
        result.current.draftReviewFollowUp("artifact_a", "work_a"),
        result.current.bootstrap(),
        result.current.apply(),
      ]);
    });
    expect(loadedReplacement).toBe(false);
    expect(result.current.status).toBe("applying");
    expect(result.current.proposal?.id).toBe("pa_apply");
    expect(applySpy).toHaveBeenCalledTimes(1);
    expect(draftSpy).not.toHaveBeenCalled();
    expect(contextDiscoverySpy).not.toHaveBeenCalled();
    expect(skillDiscoverySpy).not.toHaveBeenCalled();

    await act(async () => {
      refreshRequest.resolve();
      await pending;
    });
    expect(result.current.status).toBe("applied");
    expect(result.current.applyResult?.proposal_id).toBe("pa_apply");
  });

  it("keeps a project-wide apply visible while reconciliation selects created work", async () => {
    const detailRequest = deferred<void>();
    vi.spyOn(api, "applyProjectAssistant").mockResolvedValue({
      object: "project_assistant.apply_result",
      data: {
        proposal_id: "pa_bootstrap_apply",
        status: "applied",
        applied: true,
        actions: [
          {
            kind: "create_work_item",
            id: "work_created",
            data: { project_id: "proj_a", work_item_id: "work_created" },
          },
        ],
      },
    });
    const dependencies = controllerDependencies();
    dependencies.loadWorkForProject = vi.fn(async () => "work_created");
    dependencies.loadWorkItemDetail = vi.fn(() => detailRequest.promise);
    const { result, rerender } = renderHook(
      (options: ControllerOptions) => useProjectAssistantController(options),
      { initialProps: controllerOptions(dependencies, "proj_a", "") },
    );

    act(() => {
      result.current.loadProposal(testProposal("pa_bootstrap_apply"));
    });
    let pending!: Promise<void>;
    act(() => {
      pending = result.current.apply();
    });
    await waitFor(() =>
      expect(dependencies.loadWorkItemDetail).toHaveBeenCalledWith("proj_a", "work_created"),
    );

    rerender(controllerOptions(dependencies, "proj_a", "work_created"));
    expect(result.current.status).toBe("applying");
    expect(result.current.proposal?.id).toBe("pa_bootstrap_apply");
    expect(result.current.applyResult).toBeNull();

    await act(async () => {
      detailRequest.resolve();
      await pending;
    });

    expect(result.current.status).toBe("applied");
    expect(result.current.proposal).toBeNull();
    expect(result.current.applyResult?.proposal_id).toBe("pa_bootstrap_apply");
  });

  it("does not dismiss a proposal while its confirmed apply is reconciling", async () => {
    const applyRequest = deferred<Awaited<ReturnType<typeof api.applyProjectAssistant>>>();
    vi.spyOn(api, "applyProjectAssistant").mockImplementation(() => applyRequest.promise);
    const dependencies = controllerDependencies();
    const { result } = renderHook(() =>
      useProjectAssistantController(controllerOptions(dependencies, "proj_a", "work_a")),
    );

    act(() => {
      result.current.loadProposal(testProposal("pa_apply"));
    });
    let pending!: Promise<void>;
    act(() => {
      pending = result.current.apply();
    });
    expect(result.current.status).toBe("applying");

    act(() => {
      result.current.dismiss();
    });
    expect(result.current.status).toBe("applying");
    expect(result.current.proposal?.id).toBe("pa_apply");

    await act(async () => {
      applyRequest.resolve({
        object: "project_assistant.apply_result",
        data: {
          proposal_id: "pa_apply",
          status: "applied",
          applied: true,
          actions: [],
        },
      });
      await pending;
    });

    expect(result.current.status).toBe("applied");
    expect(result.current.applyResult?.proposal_id).toBe("pa_apply");
    expect(dependencies.refreshProjects).toHaveBeenCalledTimes(1);
    expect(dependencies.loadProjectMemory).toHaveBeenCalledWith("proj_a");
  });
});

type ControllerOptions = Parameters<typeof useProjectAssistantController>[0];
type ControllerDependencies = Omit<
  ControllerOptions,
  "project" | "selectedProjectID" | "selectedWorkItem" | "selectedWorkItemID"
>;

function controllerDependencies(): ControllerDependencies {
  return {
    onProjectDiscovered: vi.fn(),
    onSkillsDiscovered: vi.fn(),
    onSkillsLoadState: vi.fn(),
    onDiscoveringContext: vi.fn(),
    onDiscoveringSkills: vi.fn(),
    onMemoryError: vi.fn(),
    onSkillsError: vi.fn(),
    refreshProjects: vi.fn(async () => undefined),
    loadWorkForProject: vi.fn(async () => "work_a"),
    loadWorkItemDetail: vi.fn(async () => undefined),
    loadProjectMemory: vi.fn(async () => undefined),
  };
}

function controllerOptions(
  dependencies: ControllerDependencies,
  projectID: string,
  workItemID: string,
): ControllerOptions {
  return {
    ...dependencies,
    project: testProject(projectID),
    selectedProjectID: projectID,
    selectedWorkItemID: workItemID,
    selectedWorkItem: workItemID ? testWorkItem(projectID, workItemID) : null,
  };
}

function testProject(id: string): ProjectRecord {
  return {
    id,
    name: `Project ${id}`,
    roots: [],
    created_at: "2026-06-22T10:00:00Z",
    updated_at: "2026-06-22T10:00:00Z",
  };
}

function testWorkItem(projectID: string, id: string): ProjectWorkItemRecord {
  return {
    id,
    project_id: projectID,
    title: `Work ${id}`,
    status: "ready",
    priority: "normal",
    created_at: "2026-06-22T10:00:00Z",
    updated_at: "2026-06-22T10:00:00Z",
  };
}

function testProposal(id: string): ProjectAssistantProposal {
  return {
    id,
    title: `Proposal ${id}`,
    summary: "",
    requires_confirmation: true,
    actions: [{ kind: "create_work_item", patch: { title: "New work" } }],
  };
}

function proposalResponse(id: string): Awaited<ReturnType<typeof api.draftProjectAssistant>> {
  return { object: "project_assistant.proposal", data: testProposal(id) };
}

function testDraftForm() {
  return {
    request: "Create useful work",
    roleID: "__auto__",
    driverKind: "__auto__",
    draftMode: "deterministic" as const,
  };
}

function testAssistantContext(
  projectID: string,
  workItemID: string,
): ProjectAssistantContextRecord {
  const project = testProject(projectID);
  const workItem = testWorkItem(projectID, workItemID);
  return {
    project,
    request: "Inspect context",
    selected_work: workItem,
    roles: [],
    budget: {
      memory_body_max_bytes: 0,
      memory_candidate_body_max_bytes: 0,
      body_original_bytes: 0,
      body_returned_bytes: 0,
      body_tokens_estimate: 0,
      body_truncated_count: 0,
    },
    selection: {
      driver_kind: "hecate_task",
      driver_source: "test",
      reason: "test",
    },
  };
}

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
}
