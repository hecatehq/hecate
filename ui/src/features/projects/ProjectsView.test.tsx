import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { type ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ProvidersAndModelsProvider } from "../../app/state/providersAndModels";
import { ProjectsProvider } from "../../app/state/projects";
import { SettingsProvider } from "../../app/state/settings";
import {
  ApiError,
  applyProjectAssistant,
  createAgentProfile,
  createProjectAssignment,
  createProjectHandoff,
  createProjectMemory,
  createProjectWorkRole,
  createProjectWorkItem,
  deleteProjectAssignment,
  deleteAgentProfile,
  deleteProjectHandoff,
  deleteProjectMemory,
  deleteProjectWorkRole,
  deleteProjectWorkItem,
  discoverProjectContextSources,
  discoverProjectSkills,
  getProjectActivity,
  getAgentProfiles,
  getProjectAssignmentContext,
  getProjectAssignments,
  getProjectAssistantContext,
  getProjectCollaborationArtifacts,
  getProjectHandoffs,
  getProjectMemory,
  getProjectMemoryCandidates,
  getProjectSkills,
  getProjectWorkItem,
  getProjectWorkItems,
  getProjectWorkRoles,
  draftProjectAssistant,
  promoteProjectMemoryCandidate,
  rejectProjectMemoryCandidate,
  startProjectAssignment,
  updateProject,
  updateAgentProfile,
  updateProjectAssignment,
  updateProjectHandoff,
  updateProjectHandoffStatus,
  updateProjectMemory,
  updateProjectSkill,
  updateProjectWorkRole,
  updateProjectWorkItem,
} from "../../lib/api";
import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
} from "../../test/runtime-console-fixture";
import launchContextContractRaw from "../../test/fixtures/launch-context-v1-contract.json";
import { withRuntimeConsole } from "../../test/runtime-console-render";
import type {
  ProjectAssignmentRecord,
  ProjectActivityData,
  ProjectMemoryCandidateRecord,
  ProjectMemoryRecord,
  ProjectRecord,
  ProjectSkillRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { ProjectsView } from "./ProjectsView";

type LaunchContextContract = {
  sections: string[];
  fields: Record<string, string[]>;
};

const launchContextContract = launchContextContractRaw as LaunchContextContract;

function emptyActivityData() {
  return {
    project_id: "",
    summary: {
      work_item_count: 0,
      assignment_count: 0,
      active_count: 0,
      blocked_count: 0,
      completed_count: 0,
      recent_count: 0,
    },
    buckets: {
      active: [],
      blocked: [],
      completed: [],
      recent: [],
    },
    recent: [],
  };
}

async function openProjectWorkspaceTab(name: RegExp | string) {
  await userEvent.click(await screen.findByRole("tab", { name }));
}

async function openProjectAttentionMenu() {
  await userEvent.click(await screen.findByRole("button", { name: /Project attention/ }));
  return screen.getByRole("menu", { name: "Project attention" });
}

vi.mock("../../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...actual,
    getProjectActivity: vi.fn(async () => ({
      object: "project_activity",
      data: emptyActivityData(),
    })),
    getProjectWorkRoles: vi.fn(async () => ({ object: "project_roles", data: [] })),
    getProjectWorkItems: vi.fn(async () => ({ object: "project_work_items", data: [] })),
    getProjectWorkItem: vi.fn(async () => ({ object: "project_work_item", data: null })),
    getProjectAssignments: vi.fn(async () => ({ object: "project_assignments", data: [] })),
    getProjectAssignmentContext: vi.fn(async () => ({ object: "context_packet", data: null })),
    getProjectAssistantContext: vi.fn(async () => ({
      object: "project_assistant.context",
      data: {
        project: {
          id: "proj_1",
          name: "Hecate",
          roots: [],
          created_at: "2026-06-01T10:00:00Z",
          updated_at: "2026-06-01T11:00:00Z",
        },
        request: "Queue Software developer for Build cockpit UI",
        selected_work: {
          id: "work_1",
          title: "Build cockpit UI",
          brief: "Expose project work and native starts.",
          status: "ready",
          priority: "high",
          owner_role_id: "software_developer",
          reviewer_role_ids: ["reviewer_qa"],
          created_at: "2026-06-02T10:00:00Z",
          updated_at: "2026-06-02T11:00:00Z",
        },
        roles: [
          {
            id: "software_developer",
            name: "Software developer",
            description: "Owns implementation work.",
            default_driver_kind: "hecate_task",
            default_provider: "anthropic",
            default_model: "claude-sonnet-4",
            default_agent_profile: "implementation",
            built_in: true,
            created_at: "2026-06-01T10:00:00Z",
            updated_at: "2026-06-01T10:00:00Z",
          },
        ],
        assignments: [],
        memory: [],
        memory_candidates: [],
        recent_activity: [],
        budget: {
          memory_body_max_bytes: 4096,
          memory_candidate_body_max_bytes: 2048,
          body_original_bytes: 0,
          body_returned_bytes: 0,
          body_tokens_estimate: 0,
          body_truncated_count: 0,
        },
        selection: {
          role_id: "software_developer",
          role_name: "Software developer",
          role_source: "selected_work_owner",
          driver_kind: "hecate_task",
          driver_source: "role_default",
          reason:
            "Selected work item is owned by Software developer. Using hecate_task from the selected role default.",
        },
      },
    })),
    getProjectCollaborationArtifacts: vi.fn(async () => ({
      object: "project_collaboration_artifacts",
      data: [],
    })),
    getProjectHandoffs: vi.fn(async () => ({ object: "project_handoffs", data: [] })),
    getProjectMemory: vi.fn(async () => ({ object: "project_memory", data: [] })),
    getProjectMemoryCandidates: vi.fn(async () => ({
      object: "project_memory_candidates",
      data: [],
    })),
    getProjectSkills: vi.fn(async () => ({ object: "project_skills", data: [] })),
    getAgentProfiles: vi.fn(async () => ({ object: "agent_profiles", data: [] })),
    createAgentProfile: vi.fn(async () => ({ object: "agent_profile", data: null })),
    updateAgentProfile: vi.fn(async () => ({ object: "agent_profile", data: null })),
    deleteAgentProfile: vi.fn(async () => undefined),
    draftProjectAssistant: vi.fn(async () => ({
      object: "project_assistant.proposal",
      data: {
        id: "pa_test",
        title: "Queue Software developer for Build cockpit UI",
        summary: "Create a queued hecate_task assignment on the selected work item.",
        requires_confirmation: true,
        actions: [
          {
            kind: "create_assignment",
            target: { project_id: "proj_1" },
            patch: {
              project_id: "proj_1",
              work_item_id: "work_1",
              role_id: "software_developer",
              driver_kind: "hecate_task",
              status: "queued",
            },
            reason: "Queue a reviewable assignment without starting execution.",
          },
        ],
        trace_id: "trace_project_assistant",
      },
    })),
    applyProjectAssistant: vi.fn(async () => ({
      object: "project_assistant.apply_result",
      data: {
        proposal_id: "pa_test",
        applied: true,
        actions: [
          {
            kind: "create_assignment",
            id: "asgn_assistant",
            data: {
              project_id: "proj_1",
              assignment_id: "asgn_assistant",
            },
          },
        ],
      },
    })),
    createProjectHandoff: vi.fn(async () => ({ object: "project_handoff", data: null })),
    updateProjectHandoff: vi.fn(async () => ({ object: "project_handoff", data: null })),
    updateProjectHandoffStatus: vi.fn(async () => ({ object: "project_handoff", data: null })),
    deleteProjectHandoff: vi.fn(async () => undefined),
    createProjectMemory: vi.fn(async () => ({ object: "project_memory_entry", data: null })),
    updateProjectMemory: vi.fn(async () => ({ object: "project_memory_entry", data: null })),
    discoverProjectSkills: vi.fn(async () => ({ object: "project_skills", data: [] })),
    updateProjectSkill: vi.fn(async () => ({ object: "project_skill", data: null })),
    deleteProjectMemory: vi.fn(async () => undefined),
    promoteProjectMemoryCandidate: vi.fn(async () => ({
      object: "project_memory_candidate",
      data: null,
    })),
    rejectProjectMemoryCandidate: vi.fn(async () => ({
      object: "project_memory_candidate",
      data: null,
    })),
    startProjectAssignment: vi.fn(async () => ({ object: "project_assignment", data: null })),
    createProjectWorkItem: vi.fn(async () => ({ object: "project_work_item", data: null })),
    createProjectAssignment: vi.fn(async () => ({ object: "project_assignment", data: null })),
    createProjectWorkRole: vi.fn(async () => ({ object: "project_role", data: null })),
    updateProjectWorkRole: vi.fn(async () => ({ object: "project_role", data: null })),
    deleteProjectWorkRole: vi.fn(async () => undefined),
    updateProjectWorkItem: vi.fn(async () => ({ object: "project_work_item", data: null })),
    deleteProjectWorkItem: vi.fn(async () => undefined),
    updateProjectAssignment: vi.fn(async () => ({ object: "project_assignment", data: null })),
    deleteProjectAssignment: vi.fn(async () => undefined),
    updateProject: vi.fn(async () => ({ object: "project", data: null })),
    discoverProjectContextSources: vi.fn(async () => ({ object: "project", data: null })),
  };
});

const project: ProjectRecord = {
  id: "proj_1",
  name: "Hecate",
  roots: [
    {
      id: "root_1",
      path: "/Users/alice/dev/hecate",
      kind: "git",
      git_branch: "main",
      active: true,
      created_at: "2026-06-01T10:00:00Z",
      updated_at: "2026-06-01T10:00:00Z",
    },
  ],
  default_provider: "ollama",
  default_model: "qwen2.5-coder",
  created_at: "2026-06-01T10:00:00Z",
  updated_at: "2026-06-01T11:00:00Z",
};

const role: ProjectWorkRoleRecord = {
  id: "software_developer",
  project_id: "proj_1",
  name: "Software developer",
  description: "Owns implementation work.",
  instructions: "Keep changes reviewable.",
  default_driver_kind: "hecate_task",
  default_provider: "anthropic",
  default_model: "claude-sonnet-4",
  default_agent_profile: "implementation",
  built_in: true,
};

const workItem: ProjectWorkItemRecord = {
  id: "work_1",
  project_id: "proj_1",
  title: "Build cockpit UI",
  brief: "Expose project work and native starts.",
  status: "ready",
  priority: "high",
  owner_role_id: "software_developer",
  reviewer_role_ids: ["reviewer_qa"],
  created_at: "2026-06-02T10:00:00Z",
  updated_at: "2026-06-02T11:00:00Z",
};

const hecateAssignment: ProjectAssignmentRecord = {
  id: "asgn_1",
  project_id: "proj_1",
  work_item_id: "work_1",
  role_id: "software_developer",
  driver_kind: "hecate_task",
  status: "queued",
  execution_ref: {
    kind: "task_run",
    task_id: "task_1",
    run_id: "run_1",
    context_snapshot_id: "ctx_assignment_1",
    status: "awaiting_approval",
    pending_approval_count: 2,
  },
  execution: {
    task_id: "task_1",
    run_id: "run_1",
    status: "awaiting_approval",
    task_status: "running",
    run_status: "awaiting_approval",
    pending_approval_count: 2,
    step_count: 4,
    artifact_count: 1,
    provider: "ollama",
    model: "qwen2.5-coder",
  },
  created_at: "2026-06-02T10:00:00Z",
  updated_at: "2026-06-02T11:00:00Z",
  started_at: "2026-06-02T10:30:00Z",
};

const memoryEntry: ProjectMemoryRecord = {
  id: "mem_1",
  scope: "project",
  project_id: project.id,
  title: "Commit style",
  body: "Use conventional commits.",
  trust_label: "operator_memory",
  source_kind: "operator",
  enabled: true,
  created_at: "2026-06-02T09:00:00Z",
  updated_at: "2026-06-02T09:00:00Z",
};

const memoryCandidate: ProjectMemoryCandidateRecord = {
  id: "memcand_1",
  project_id: project.id,
  title: "Generated summary",
  body: "Keep generated content lower trust until reviewed.",
  suggested_kind: "note",
  suggested_trust_label: "generated_summary",
  suggested_source_kind: "task_output",
  suggested_source_id: "run_1",
  source_refs: [{ kind: "task_run", id: "run_1", title: "Implementation run" }],
  status: "pending",
  created_at: "2026-06-02T12:00:00Z",
  updated_at: "2026-06-02T12:00:00Z",
};

const projectSkill: ProjectSkillRecord = {
  id: "backend",
  project_id: project.id,
  title: "Backend",
  description: "Build backend changes.",
  path: ".hecate/skills/backend/SKILL.md",
  root_id: "root_1",
  format: "skill_md",
  enabled: true,
  status: "available",
  trust_label: "workspace_skill",
  source_context_source_ids: ["ctx_agents"],
  warnings: [],
  discovered_at: "2026-06-02T12:00:00Z",
  created_at: "2026-06-02T12:00:00Z",
  updated_at: "2026-06-02T12:00:00Z",
};

function resetProjectWorkMocks() {
  vi.mocked(getProjectActivity).mockResolvedValue({
    object: "project_activity",
    data: {
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
        blocked: [
          {
            id: hecateAssignment.id,
            project_id: project.id,
            work_item: {
              id: workItem.id,
              title: workItem.title,
              status: "running",
              priority: workItem.priority,
            },
            assignment: hecateAssignment,
            role,
            status: "awaiting_approval",
            blocking_signal: "awaiting_approval",
            status_summary: "2 approval pending",
            linked_task_id: "task_1",
            linked_run_id: "run_1",
            artifact_summary: { count: 1, latest_kind: "handoff", latest_title: "Runtime notes" },
            handoff_summary: { count: 0 },
            recent_artifacts: [
              {
                id: "art_1",
                project_id: project.id,
                work_item_id: workItem.id,
                assignment_id: hecateAssignment.id,
                kind: "handoff",
                title: "Runtime notes",
                body: "Approval is waiting.",
                created_at: "2026-06-02T11:05:00Z",
                updated_at: "2026-06-02T11:05:00Z",
              },
            ],
            updated_at: "2026-06-02T11:05:00Z",
          },
        ],
        completed: [],
        recent: [],
      },
      recent: [],
    },
  });
  vi.mocked(getProjectWorkRoles).mockResolvedValue({ object: "project_roles", data: [role] });
  vi.mocked(getProjectWorkItems).mockResolvedValue({
    object: "project_work_items",
    data: [{ ...workItem, assignments: [hecateAssignment] }],
  });
  vi.mocked(getProjectWorkItem).mockResolvedValue({
    object: "project_work_item",
    data: workItem,
  });
  vi.mocked(getProjectAssignments).mockResolvedValue({
    object: "project_assignments",
    data: [hecateAssignment],
  });
  vi.mocked(getProjectSkills).mockResolvedValue({ object: "project_skills", data: [] });
  vi.mocked(getProjectAssignmentContext).mockResolvedValue({
    object: "context_packet",
    data: {
      id: "ctx_assignment_1",
      execution_mode: "hecate_task",
      provider: "ollama",
      model: "qwen2.5-coder",
      execution_profile: "implementation",
      workspace: "/tmp/hecate-project",
      refs: {
        project_id: project.id,
        work_item_id: workItem.id,
        assignment_id: hecateAssignment.id,
        task_id: "task_1",
        run_id: "run_1",
      },
      items: [
        {
          section: "profile",
          kind: "agent_profile",
          trust_level: "runtime_state",
          origin: "implementation",
          title: "Implementation profile",
          body: "Tools enabled. Writes allowed.",
          included: true,
        },
        {
          section: "skills",
          kind: "project_skills",
          trust_level: "workspace_skill",
          origin: "project_skills",
          title: "Project skills",
          body: "Requested: backend\nResolved enabled skills: backend (.hecate/skills/backend/SKILL.md)",
          included: true,
          inclusion_reason:
            "Skill metadata resolved for this assignment; skill bodies are not injected",
        },
        {
          section: "memory",
          kind: "memory",
          trust_level: "operator_memory",
          origin: "mem_backend",
          title: "Backend preference",
          body: "Prefer Go-first changes.",
          included: false,
          inclusion_reason: "Visible to operator, not injected into assignment launch context.",
        },
        {
          section: "sources",
          kind: "workspace_instruction",
          trust_level: "workspace_guidance",
          origin: "AGENTS.md",
          title: "Workspace instructions",
          body: "Discovered project guidance.",
          included: false,
        },
        {
          section: "project_work",
          kind: "work_item",
          trust_level: "runtime_state",
          origin: workItem.id,
          title: workItem.title,
          body: workItem.brief,
          included: true,
        },
        {
          section: "runtime",
          kind: "trace",
          trust_level: "runtime_state",
          origin: "run_1",
          title: "Run evidence",
          body: "Trace and run identifiers captured.",
          included: true,
        },
      ],
    },
  });
  vi.mocked(getProjectAssistantContext).mockResolvedValue({
    object: "project_assistant.context",
    data: {
      project: {
        id: project.id,
        name: project.name,
        roots: project.roots.map(({ id, path, kind, git_remote, git_branch, active }) => ({
          id,
          path,
          kind,
          git_remote,
          git_branch,
          active,
        })),
        default_provider: project.default_provider,
        default_model: project.default_model,
        created_at: project.created_at,
        updated_at: project.updated_at,
      },
      request: `Queue ${role.name} for ${workItem.title}`,
      selected_work: {
        id: workItem.id,
        title: workItem.title,
        brief: workItem.brief,
        status: workItem.status,
        priority: workItem.priority,
        owner_role_id: workItem.owner_role_id,
        reviewer_role_ids: workItem.reviewer_role_ids,
        created_at: workItem.created_at,
        updated_at: workItem.updated_at,
      },
      roles: [
        {
          id: role.id,
          name: role.name,
          description: role.description,
          default_driver_kind: role.default_driver_kind,
          default_provider: role.default_provider,
          default_model: role.default_model,
          default_agent_profile: role.default_agent_profile,
          built_in: role.built_in,
          created_at: "2026-06-01T10:00:00Z",
          updated_at: "2026-06-01T10:00:00Z",
        },
      ],
      assignments: [hecateAssignment],
      memory: [
        {
          id: memoryEntry.id,
          title: memoryEntry.title,
          body: memoryEntry.body,
          body_original_bytes: memoryEntry.body.length,
          body_returned_bytes: memoryEntry.body.length,
          body_tokens_estimate: Math.ceil(memoryEntry.body.length / 4),
          body_truncated: false,
          trust_label: memoryEntry.trust_label,
          source_kind: memoryEntry.source_kind,
          source_id: memoryEntry.source_id,
          enabled: memoryEntry.enabled,
          created_at: memoryEntry.created_at,
          updated_at: memoryEntry.updated_at,
        },
      ],
      memory_candidates: [
        {
          id: memoryCandidate.id,
          title: memoryCandidate.title,
          body: memoryCandidate.body,
          body_original_bytes: memoryCandidate.body.length,
          body_returned_bytes: memoryCandidate.body.length,
          body_tokens_estimate: Math.ceil(memoryCandidate.body.length / 4),
          body_truncated: false,
          suggested_kind: memoryCandidate.suggested_kind,
          suggested_trust_label: memoryCandidate.suggested_trust_label,
          suggested_source_kind: memoryCandidate.suggested_source_kind,
          suggested_source_id: memoryCandidate.suggested_source_id,
          source_refs: memoryCandidate.source_refs,
          status: memoryCandidate.status,
          status_reason: memoryCandidate.status_reason,
          promoted_memory_id: memoryCandidate.promoted_memory_id,
          created_at: memoryCandidate.created_at,
          updated_at: memoryCandidate.updated_at,
        },
      ],
      recent_activity: [
        {
          kind: "selected_work",
          id: workItem.id,
          title: workItem.title,
          status: workItem.status,
          updated_at: workItem.updated_at,
        },
      ],
      budget: {
        memory_body_max_bytes: 4096,
        memory_candidate_body_max_bytes: 2048,
        body_original_bytes: memoryEntry.body.length + memoryCandidate.body.length,
        body_returned_bytes: memoryEntry.body.length + memoryCandidate.body.length,
        body_tokens_estimate:
          Math.ceil(memoryEntry.body.length / 4) + Math.ceil(memoryCandidate.body.length / 4),
        body_truncated_count: 0,
      },
      selection: {
        role_id: role.id,
        role_name: role.name,
        role_source: "selected_work_owner",
        driver_kind: "hecate_task",
        driver_source: "role_default",
        reason:
          "Selected work item is owned by Software developer. Using hecate_task from the selected role default.",
      },
    },
  });
  vi.mocked(getProjectCollaborationArtifacts).mockResolvedValue({
    object: "project_collaboration_artifacts",
    data: [],
  });
  vi.mocked(getProjectHandoffs).mockResolvedValue({
    object: "project_handoffs",
    data: [],
  });
  vi.mocked(getProjectMemory).mockResolvedValue({ object: "project_memory", data: [] });
  vi.mocked(getProjectMemoryCandidates).mockResolvedValue({
    object: "project_memory_candidates",
    data: [],
  });
  vi.mocked(getAgentProfiles).mockResolvedValue({
    object: "agent_profiles",
    data: [
      {
        id: "implementation",
        name: "Implementation",
        description: "Build project assignments",
        instructions: "",
        surface: "hecate_task",
        provider_hint: "",
        model_hint: "",
        execution_profile: "implementation",
        tools_enabled: true,
        writes_allowed: true,
        network_allowed: false,
        approval_policy: "inherit",
        project_memory_policy: "visible_only",
        context_source_policy: "include_enabled",
        skill_ids: [],
        external_agent_kind: "",
        external_agent_options: {},
        created_at: "2026-06-04T10:00:00Z",
        updated_at: "2026-06-04T10:00:00Z",
      },
    ],
  });
  vi.mocked(createAgentProfile).mockImplementation(async (payload) => ({
    object: "agent_profile",
    data: {
      id: payload.id || "profile_new",
      name: payload.name,
      description: payload.description ?? "",
      instructions: payload.instructions ?? "",
      surface: payload.surface || "any",
      provider_hint: payload.provider_hint ?? "",
      model_hint: payload.model_hint ?? "",
      execution_profile: payload.execution_profile ?? "",
      tools_enabled: payload.tools_enabled ?? true,
      writes_allowed: payload.writes_allowed ?? false,
      network_allowed: payload.network_allowed ?? false,
      approval_policy: payload.approval_policy || "inherit",
      project_memory_policy: payload.project_memory_policy || "inherit",
      context_source_policy: payload.context_source_policy || "inherit",
      skill_ids: payload.skill_ids ?? [],
      external_agent_kind: payload.external_agent_kind ?? "",
      external_agent_options: {},
      created_at: "2026-06-04T12:00:00Z",
      updated_at: "2026-06-04T12:00:00Z",
    },
  }));
  vi.mocked(updateAgentProfile).mockImplementation(async (id, payload) => ({
    object: "agent_profile",
    data: {
      id,
      name: payload.name || "Implementation",
      description: payload.description ?? "",
      instructions: payload.instructions ?? "",
      surface: payload.surface || "any",
      provider_hint: payload.provider_hint ?? "",
      model_hint: payload.model_hint ?? "",
      execution_profile: payload.execution_profile ?? "",
      tools_enabled: payload.tools_enabled ?? true,
      writes_allowed: payload.writes_allowed ?? false,
      network_allowed: payload.network_allowed ?? false,
      approval_policy: payload.approval_policy || "inherit",
      project_memory_policy: payload.project_memory_policy || "inherit",
      context_source_policy: payload.context_source_policy || "inherit",
      skill_ids: payload.skill_ids ?? [],
      external_agent_kind: payload.external_agent_kind ?? "",
      external_agent_options: {},
      created_at: "2026-06-04T10:00:00Z",
      updated_at: "2026-06-04T12:00:00Z",
    },
  }));
  vi.mocked(deleteAgentProfile).mockResolvedValue(undefined);
  vi.mocked(draftProjectAssistant).mockResolvedValue({
    object: "project_assistant.proposal",
    data: {
      id: "pa_test",
      title: "Queue Software developer for Build cockpit UI",
      summary: "Create a queued hecate_task assignment on the selected work item.",
      requires_confirmation: true,
      actions: [
        {
          kind: "create_assignment",
          target: { project_id: project.id },
          patch: {
            project_id: project.id,
            work_item_id: workItem.id,
            role_id: role.id,
            driver_kind: "hecate_task",
            status: "queued",
          },
          reason: "Queue a reviewable assignment without starting execution.",
        },
      ],
      trace_id: "trace_project_assistant",
    },
  });
  vi.mocked(applyProjectAssistant).mockResolvedValue({
    object: "project_assistant.apply_result",
    data: {
      proposal_id: "pa_test",
      applied: true,
      actions: [
        {
          kind: "create_assignment",
          id: "asgn_assistant",
          data: {
            project_id: project.id,
            assignment_id: "asgn_assistant",
          },
        },
      ],
    },
  });
  vi.mocked(createProjectHandoff).mockResolvedValue({
    object: "project_handoff",
    data: {
      id: "handoff_new",
      project_id: project.id,
      work_item_id: workItem.id,
      title: "QA handoff",
      summary: "Ready for review.",
      recommended_next_action: "Start QA.",
      status: "pending",
      provenance_kind: "operator",
      trust_label: "operator_reviewed",
      created_at: "2026-06-02T12:00:00Z",
      updated_at: "2026-06-02T12:00:00Z",
      status_changed_at: "2026-06-02T12:00:00Z",
    },
  });
  vi.mocked(updateProjectHandoff).mockResolvedValue({
    object: "project_handoff",
    data: {
      id: "handoff_new",
      project_id: project.id,
      work_item_id: workItem.id,
      title: "QA handoff",
      summary: "Ready for review.",
      recommended_next_action: "Start QA.",
      target_assignment_id: "asgn_new",
      status: "accepted",
      provenance_kind: "operator",
      trust_label: "operator_reviewed",
      created_at: "2026-06-02T12:00:00Z",
      updated_at: "2026-06-02T12:05:00Z",
      status_changed_at: "2026-06-02T12:05:00Z",
    },
  });
  vi.mocked(updateProjectHandoffStatus).mockResolvedValue({
    object: "project_handoff",
    data: {
      id: "handoff_new",
      project_id: project.id,
      work_item_id: workItem.id,
      title: "QA handoff",
      summary: "Ready for review.",
      recommended_next_action: "Start QA.",
      status: "accepted",
      provenance_kind: "operator",
      trust_label: "operator_reviewed",
      created_at: "2026-06-02T12:00:00Z",
      updated_at: "2026-06-02T12:05:00Z",
      status_changed_at: "2026-06-02T12:05:00Z",
    },
  });
  vi.mocked(deleteProjectHandoff).mockResolvedValue(undefined);
  vi.mocked(createProjectMemory).mockResolvedValue({
    object: "project_memory_entry",
    data: { ...memoryEntry, id: "mem_new", title: "Review posture" },
  });
  vi.mocked(updateProjectMemory).mockResolvedValue({
    object: "project_memory_entry",
    data: { ...memoryEntry, body: "Prefer small commits.", updated_at: "2026-06-02T10:00:00Z" },
  });
  vi.mocked(deleteProjectMemory).mockResolvedValue(undefined);
  vi.mocked(promoteProjectMemoryCandidate).mockResolvedValue({
    object: "project_memory_candidate",
    data: { ...memoryCandidate, status: "promoted", promoted_memory_id: "mem_promoted" },
  });
  vi.mocked(rejectProjectMemoryCandidate).mockResolvedValue({
    object: "project_memory_candidate",
    data: { ...memoryCandidate, status: "rejected" },
  });
  vi.mocked(startProjectAssignment).mockResolvedValue({
    object: "project_assignment",
    data: { ...hecateAssignment, status: "running" },
  });
  vi.mocked(createProjectWorkItem).mockResolvedValue({
    object: "project_work_item",
    data: { ...workItem, id: "work_new", title: "New cockpit work" },
  });
  vi.mocked(createProjectAssignment).mockResolvedValue({
    object: "project_assignment",
    data: { ...hecateAssignment, id: "asgn_new", status: "queued", execution: undefined },
  });
  vi.mocked(createProjectWorkRole).mockResolvedValue({
    object: "project_role",
    data: {
      id: "role_frontend_custom",
      project_id: "proj_1",
      name: "Frontend implementer",
      skill_ids: ["frontend"],
      built_in: false,
    },
  });
  vi.mocked(updateProjectWorkRole).mockResolvedValue({
    object: "project_role",
    data: {
      id: "role_frontend_custom",
      project_id: "proj_1",
      name: "Frontend implementer",
      default_driver_kind: "external_agent",
      default_provider: "anthropic",
      default_model: "claude-sonnet-4",
      default_agent_profile: "safe_external_review",
      skill_ids: ["frontend"],
      built_in: false,
    },
  });
  vi.mocked(deleteProjectWorkRole).mockResolvedValue(undefined);
  vi.mocked(updateProjectWorkItem).mockResolvedValue({
    object: "project_work_item",
    data: { ...workItem, title: "Edited cockpit UI", status: "review", priority: "urgent" },
  });
  vi.mocked(deleteProjectWorkItem).mockResolvedValue(undefined);
  vi.mocked(updateProjectAssignment).mockResolvedValue({
    object: "project_assignment",
    data: { ...hecateAssignment, role_id: "software_developer", status: "running" },
  });
  vi.mocked(deleteProjectAssignment).mockResolvedValue(undefined);
  vi.mocked(updateProject).mockResolvedValue({
    object: "project",
    data: {
      ...project,
      default_provider: "ollama",
      default_model: "ministral-3:latest",
      default_workspace_mode: "in_place",
    },
  });
  vi.mocked(discoverProjectContextSources).mockResolvedValue({
    object: "project",
    data: project,
  });
}

function directWrapper(initialState: Parameters<typeof ProjectsProvider>[0]["initialState"]) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <SettingsProvider>
        <ProvidersAndModelsProvider>
          <ProjectsProvider initialState={initialState}>{children}</ProjectsProvider>
        </ProvidersAndModelsProvider>
      </SettingsProvider>
    );
  };
}

function expectLaunchContextContract(text: string) {
  const sectionLabels = launchContextContract.sections.map((section) =>
    section === "Project" ? "Project:" : section,
  );
  for (const section of sectionLabels) {
    expect(text).toContain(section);
  }
  for (const field of Object.values(launchContextContract.fields).flat()) {
    expect(text).toContain(`- ${field}:`);
  }
}

afterEach(() => {
  window.localStorage.clear();
  vi.mocked(getProjectActivity).mockReset();
  vi.mocked(getProjectWorkRoles).mockReset();
  vi.mocked(getProjectWorkItems).mockReset();
  vi.mocked(getProjectWorkItem).mockReset();
  vi.mocked(getProjectAssignments).mockReset();
  vi.mocked(getProjectAssignmentContext).mockReset();
  vi.mocked(getProjectAssistantContext).mockReset();
  vi.mocked(getProjectCollaborationArtifacts).mockReset();
  vi.mocked(getProjectHandoffs).mockReset();
  vi.mocked(getProjectMemory).mockReset();
  vi.mocked(getProjectMemoryCandidates).mockReset();
  vi.mocked(getProjectSkills).mockReset();
  vi.mocked(getAgentProfiles).mockReset();
  vi.mocked(createAgentProfile).mockReset();
  vi.mocked(updateAgentProfile).mockReset();
  vi.mocked(deleteAgentProfile).mockReset();
  vi.mocked(draftProjectAssistant).mockReset();
  vi.mocked(applyProjectAssistant).mockReset();
  vi.mocked(createProjectHandoff).mockReset();
  vi.mocked(updateProjectHandoff).mockReset();
  vi.mocked(updateProjectHandoffStatus).mockReset();
  vi.mocked(deleteProjectHandoff).mockReset();
  vi.mocked(createProjectMemory).mockReset();
  vi.mocked(updateProjectMemory).mockReset();
  vi.mocked(discoverProjectSkills).mockReset();
  vi.mocked(updateProjectSkill).mockReset();
  vi.mocked(deleteProjectMemory).mockReset();
  vi.mocked(promoteProjectMemoryCandidate).mockReset();
  vi.mocked(rejectProjectMemoryCandidate).mockReset();
  vi.mocked(startProjectAssignment).mockReset();
  vi.mocked(createProjectWorkItem).mockReset();
  vi.mocked(createProjectAssignment).mockReset();
  vi.mocked(createProjectWorkRole).mockReset();
  vi.mocked(updateProjectWorkRole).mockReset();
  vi.mocked(deleteProjectWorkRole).mockReset();
  vi.mocked(updateProjectWorkItem).mockReset();
  vi.mocked(deleteProjectWorkItem).mockReset();
  vi.mocked(updateProjectAssignment).mockReset();
  vi.mocked(deleteProjectAssignment).mockReset();
  vi.mocked(updateProject).mockReset();
  vi.mocked(discoverProjectContextSources).mockReset();
});

describe("ProjectsView index", () => {
  it("renders project rows as compact navigation with update recency", async () => {
    resetProjectWorkMocks();
    const recentlyUpdatedProject = {
      ...project,
      updated_at: new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString(),
    };
    window.localStorage.setItem("hecate.project", recentlyUpdatedProject.id);
    render(<ProjectsView />, {
      wrapper: directWrapper({ projects: [recentlyUpdatedProject] }),
    });

    const projectList = screen.getByRole("region", { name: "Projects" });
    expect(projectList.style.width).toBe("220px");
    expect(screen.getByRole("button", { name: "Open project Hecate" })).toBeTruthy();
    expect(within(projectList).queryByText("/Users/alice/dev/hecate")).toBeNull();
    expect(screen.getByText("/Users/alice/dev/hecate · qwen2.5-coder")).toBeTruthy();
    expect(within(projectList).queryByText("ollama / qwen2.5-coder")).toBeNull();
    expect(within(projectList).getByText("Updated 2h ago")).toBeTruthy();
    expect((await screen.findAllByText("Build cockpit UI")).length).toBeGreaterThan(0);
  });

  it("keeps work coordination in the cockpit when the selected project has no work", async () => {
    resetProjectWorkMocks();
    vi.mocked(getProjectActivity).mockResolvedValue({
      object: "project_activity",
      data: emptyActivityData(),
    });
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [],
    });
    window.localStorage.setItem("hecate.project", project.id);
    window.localStorage.setItem("hecate.projects.panel_collapsed", "1");

    render(<ProjectsView />, {
      wrapper: directWrapper({ projects: [project] }),
    });

    expect(await screen.findByText("Work Queue")).toBeTruthy();
    expect(screen.getByRole("region", { name: "Projects" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Open project Hecate" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Collapse projects panel" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Expand projects panel" })).toBeNull();
    expect(screen.queryByRole("region", { name: "Collapsed projects panel" })).toBeNull();
    expect(screen.queryByRole("region", { name: "Project work items" })).toBeNull();
    const workPanel = screen.getByRole("region", { name: "Work coordination" });
    expect(workPanel).toBeTruthy();
    expect(screen.getByText("No work items for this project.")).toBeTruthy();
    expect(within(workPanel).getByRole("button", { name: "Work" })).toBeTruthy();
  });

  it("keeps cockpit controls and work coordination in stable regions when work items exist", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);

    render(<ProjectsView />, {
      wrapper: directWrapper({ projects: [project] }),
    });

    expect(await screen.findByText("Expose project work and native starts.")).toBeTruthy();
    const projectList = screen.getByRole("region", { name: "Projects" });
    const detail = screen.getByRole("region", { name: "Selected work item" });
    const workPanel = screen.getByRole("region", { name: "Work coordination" });
    const selectedWorkCard = within(detail).getByRole("article", {
      name: "Build cockpit UI work item",
    });
    expect(within(projectList).queryByText("/Users/alice/dev/hecate")).toBeNull();
    expect(screen.getByText("/Users/alice/dev/hecate · qwen2.5-coder")).toBeTruthy();
    expect(within(selectedWorkCard).getByText("Brief")).toBeTruthy();
    expect(within(selectedWorkCard).getByText("Assignments")).toBeTruthy();
    expect(within(selectedWorkCard).getByText("Collaboration Artifacts")).toBeTruthy();
    expect(within(selectedWorkCard).getByText("Handoffs")).toBeTruthy();
    const headerActions = screen.getByLabelText("Project header actions");
    expect(headerActions).toBeTruthy();
    expect(within(detail).queryByLabelText("Project header actions")).toBeNull();
    const actionLabels = within(headerActions)
      .getAllByRole("button")
      .map((button) => button.getAttribute("aria-label") ?? "");
    expect(actionLabels[0]).toMatch(/^Project attention/);
    expect(actionLabels.slice(1)).toEqual([
      "Roles",
      "Agent profiles",
      "Project settings",
      "Refresh project work",
    ]);
    expect(within(detail).queryByText("Cockpit")).toBeNull();
    expect(within(workPanel).getByRole("button", { name: "Work" })).toBeTruthy();
    expect(within(workPanel).getByText("Work Queue")).toBeTruthy();
    expect(screen.queryByRole("region", { name: "Project work items" })).toBeNull();

    await openProjectAttentionMenu();
    expect(screen.getByRole("menu", { name: "Project attention" })).toBeTruthy();
    fireEvent.mouseDown(workPanel);
    await waitFor(() => {
      expect(screen.queryByRole("menu", { name: "Project attention" })).toBeNull();
    });
  });

  it("groups live operations separately from project continuity", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);

    render(<ProjectsView />, {
      wrapper: directWrapper({ projects: [project] }),
    });

    expect(await screen.findByText("Expose project work and native starts.")).toBeTruthy();
    const workspace = screen.getByRole("region", { name: "Project workspace" });
    const assistant = within(workspace).getByRole("region", { name: "Project Assistant" });
    const tabs = within(workspace).getByRole("tablist", { name: "Project workspace views" });
    expect(assistant.compareDocumentPosition(tabs) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByRole("region", { name: "Work queue" })).toBeTruthy();
    expect(screen.getByRole("button", { name: /Project attention/ })).toBeTruthy();
    expect(screen.queryByRole("region", { name: "Needs attention" })).toBeNull();
    expect(screen.queryByRole("complementary", { name: "Project continuity" })).toBeNull();
    expect(within(tabs).getByRole("tab", { name: /Work Coordination/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(within(tabs).getByRole("tab", { name: /Timeline \/ Decision Log/ })).toBeTruthy();
    expect(within(tabs).getByRole("tab", { name: /Memory \/ Context/ })).toBeTruthy();
    expect(within(tabs).getByRole("tab", { name: /Skills/ })).toBeTruthy();
    const workPanel = within(workspace).getByRole("region", { name: "Work coordination" });
    expect(workPanel).toBeTruthy();
    expect(workPanel.querySelector(".project-work-coordination-grid")).toBeTruthy();
    expect(within(workspace).getByRole("heading", { name: "Build cockpit UI" })).toBeTruthy();
    expect(within(workspace).queryByLabelText("Project timeline")).toBeNull();

    await openProjectWorkspaceTab(/Timeline \/ Decision Log/);
    expect(within(workspace).getByLabelText("Project timeline")).toBeTruthy();
    expect(within(workspace).queryByRole("heading", { name: "Build cockpit UI" })).toBeNull();

    await openProjectWorkspaceTab(/Memory \/ Context/);
    expect(within(workspace).getByText("No project memory entries saved yet.")).toBeTruthy();
    expect(within(workspace).queryByLabelText("Project timeline")).toBeNull();
  });

  it("renders and updates project skills from the registry", async () => {
    resetProjectWorkMocks();
    vi.mocked(getProjectSkills).mockResolvedValue({
      object: "project_skills",
      data: [projectSkill],
    });
    vi.mocked(discoverProjectSkills).mockResolvedValue({
      object: "project_skills",
      data: [{ ...projectSkill, id: "qa", title: "QA", path: ".agents/skills/qa/SKILL.md" }],
    });
    vi.mocked(updateProjectSkill).mockResolvedValue({
      object: "project_skill",
      data: { ...projectSkill, enabled: false },
    });
    window.localStorage.setItem("hecate.project", project.id);

    render(<ProjectsView />, {
      wrapper: directWrapper({ projects: [project] }),
    });

    await openProjectWorkspaceTab(/Skills/);
    const workspace = screen.getByRole("region", { name: "Project workspace" });
    expect(within(workspace).getByText("Build backend changes.")).toBeTruthy();
    await userEvent.click(
      within(workspace).getByRole("checkbox", { name: "Enable skill Backend" }),
    );
    await waitFor(() => {
      expect(updateProjectSkill).toHaveBeenCalledWith(project.id, "backend", { enabled: false });
    });
    await userEvent.click(within(workspace).getByRole("button", { name: "Discover" }));
    await waitFor(() => {
      expect(discoverProjectSkills).toHaveBeenCalledWith(project.id);
    });
    expect(await within(workspace).findByText(/\.agents\/skills\/qa\/SKILL\.md/)).toBeTruthy();
  });

  it("renders empty, loading, and error states for the project index", () => {
    const empty = render(<ProjectsView />, {
      wrapper: directWrapper({ projects: [] }),
    });
    expect(screen.getByText("No projects yet")).toBeTruthy();
    empty.unmount();

    render(<ProjectsView />, {
      wrapper: directWrapper({ projects: [], loading: true }),
    });
    expect(screen.getByText("Loading projects…")).toBeTruthy();
    cleanup();

    render(<ProjectsView />, {
      wrapper: directWrapper({ projects: [], error: "project list failed" }),
    });
    expect(screen.getByText("project list failed")).toBeTruthy();
  });

  it("uses existing project actions for create, rename, and delete", async () => {
    resetProjectWorkMocks();
    const user = userEvent.setup();
    const actions = {
      ...createRuntimeConsoleActions(),
      createProjectFromFolder: vi.fn(async () => project),
      renameProject: vi.fn(async () => undefined),
      deleteProject: vi.fn(async () => true),
      selectProject: vi.fn(async () => undefined),
    };
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions }));

    await user.click(screen.getByRole("button", { name: "Add" }));
    expect(actions.createProjectFromFolder).toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Rename project Hecate" }));
    const renameInput = screen.getByLabelText("Rename Hecate");
    await user.type(renameInput, " workspace");
    expect(renameInput).toHaveValue("Hecate workspace");
    expect(actions.selectProject).not.toHaveBeenCalled();
    fireEvent.change(renameInput, {
      target: { value: "Hecate console" },
    });
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(actions.renameProject).toHaveBeenCalledWith(project.id, "Hecate console");

    await user.click(screen.getByRole("button", { name: "Delete project Hecate" }));
    expect(
      screen.getByText(/Workspace files and the git repository are not deleted/i),
    ).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Delete project record" }));
    expect(actions.deleteProject).toHaveBeenCalledWith(project.id);
  });
});

describe("ProjectsView cockpit", () => {
  it("loads work items after selecting a project", async () => {
    resetProjectWorkMocks();
    const state = createRuntimeConsoleFixture({ projects: [project] });
    const actions = {
      ...createRuntimeConsoleActions(),
      selectProject: vi.fn(async () => undefined),
    };
    render(withRuntimeConsole(<ProjectsView />, { state, actions }));

    await userEvent.click(screen.getByRole("button", { name: "Open project Hecate" }));

    await waitFor(() => {
      expect(getProjectWorkItems).toHaveBeenCalledWith(project.id);
    });
    expect(actions.selectProject).toHaveBeenCalledWith(project.id);
    expect((await screen.findAllByText("Build cockpit UI")).length).toBeGreaterThan(0);
  });

  it("reviews and applies Project Assistant assignment proposals", async () => {
    resetProjectWorkMocks();
    const user = userEvent.setup();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Selected work: Build cockpit UI");
    const assistant = await screen.findByRole("region", { name: "Project Assistant" });
    await user.click(within(assistant).getByRole("button", { name: "Draft proposal" }));

    await waitFor(() => {
      expect(draftProjectAssistant).toHaveBeenCalledWith({
        project_id: project.id,
        work_item_id: workItem.id,
        request: "Queue Software developer for Build cockpit UI",
      });
    });
    expect(await within(assistant).findByText("Create assignment")).toBeTruthy();
    expect(within(assistant).getByText("work_item_id")).toBeTruthy();
    expect(within(assistant).getByText(workItem.id)).toBeTruthy();
    expect(
      within(assistant).getByRole("button", { name: "Copy trace_project_assistant" }),
    ).toBeTruthy();

    await user.click(within(assistant).getByRole("button", { name: "Apply proposal" }));

    await waitFor(() => {
      expect(applyProjectAssistant).toHaveBeenCalledWith({
        proposal: expect.objectContaining({ id: "pa_test" }),
        confirm: true,
      });
    });
    expect(await within(assistant).findByText("Applied 1 action from pa_test.")).toBeTruthy();
    expect(getProjectWorkItems).toHaveBeenLastCalledWith(project.id);
    expect(getProjectAssignments).toHaveBeenLastCalledWith(project.id, workItem.id);
  });

  it("can request model-backed Project Assistant drafts", async () => {
    resetProjectWorkMocks();
    const user = userEvent.setup();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Selected work: Build cockpit UI");
    const assistant = await screen.findByRole("region", { name: "Project Assistant" });
    await user.selectOptions(within(assistant).getByLabelText("Draft"), "model");
    await user.click(within(assistant).getByRole("button", { name: "Draft proposal" }));

    await waitFor(() => {
      expect(draftProjectAssistant).toHaveBeenCalledWith({
        project_id: project.id,
        work_item_id: workItem.id,
        request: "Queue Software developer for Build cockpit UI",
        draft_mode: "model",
      });
    });
  });

  it("can request bootstrap Project Assistant drafts", async () => {
    resetProjectWorkMocks();
    const user = userEvent.setup();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Selected work: Build cockpit UI");
    const assistant = await screen.findByRole("region", { name: "Project Assistant" });
    await user.selectOptions(within(assistant).getByLabelText("Draft"), "bootstrap");
    await user.click(within(assistant).getByRole("button", { name: "Draft proposal" }));

    await waitFor(() => {
      expect(draftProjectAssistant).toHaveBeenCalledWith({
        project_id: project.id,
        work_item_id: workItem.id,
        request: "Queue Software developer for Build cockpit UI",
        draft_mode: "bootstrap",
      });
    });
  });

  it("discovers guidance and skills before drafting project bootstrap proposals", async () => {
    resetProjectWorkMocks();
    const user = userEvent.setup();
    const discoveredProject: ProjectRecord = {
      ...project,
      context_sources: [
        {
          id: "ctx_agents",
          kind: "workspace_instruction",
          title: "AGENTS.md",
          path: "AGENTS.md",
          enabled: true,
          source_category: "workspace_guidance",
          trust_label: "workspace_guidance",
          created_at: "2026-06-02T09:00:00Z",
          updated_at: "2026-06-02T09:00:00Z",
        },
      ],
    };
    vi.mocked(discoverProjectContextSources).mockResolvedValue({
      object: "project",
      data: discoveredProject,
    });
    vi.mocked(discoverProjectSkills).mockResolvedValue({
      object: "project_skills",
      data: [projectSkill],
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Selected work: Build cockpit UI");
    const assistant = await screen.findByRole("region", { name: "Project Assistant" });
    await user.click(within(assistant).getByRole("button", { name: "Bootstrap project" }));

    await waitFor(() => {
      expect(discoverProjectContextSources).toHaveBeenCalledWith(project.id);
      expect(discoverProjectSkills).toHaveBeenCalledWith(project.id);
      expect(draftProjectAssistant).toHaveBeenCalledWith({
        project_id: project.id,
        request: "Bootstrap project guidance",
        draft_mode: "bootstrap",
      });
    });
    expect(vi.mocked(discoverProjectContextSources).mock.invocationCallOrder[0]).toBeLessThan(
      vi.mocked(discoverProjectSkills).mock.invocationCallOrder[0],
    );
    expect(vi.mocked(discoverProjectSkills).mock.invocationCallOrder[0]).toBeLessThan(
      vi.mocked(draftProjectAssistant).mock.invocationCallOrder[0],
    );
    expect(screen.getByRole("tab", { name: /Skills/ })).toHaveTextContent("1");
  });

  it("inspects Project Assistant context selection before drafting", async () => {
    resetProjectWorkMocks();
    const user = userEvent.setup();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Selected work: Build cockpit UI");
    const assistant = await screen.findByRole("region", { name: "Project Assistant" });
    await user.click(within(assistant).getByRole("button", { name: "Inspect context" }));

    await waitFor(() => {
      expect(getProjectAssistantContext).toHaveBeenCalledWith({
        project_id: project.id,
        work_item_id: workItem.id,
        request: "Queue Software developer for Build cockpit UI",
      });
    });
    expect(
      await within(assistant).findByText("Auto selected Software developer via Hecate task"),
    ).toBeTruthy();
    expect(
      within(assistant).getByText(
        "Selected work item is owned by Software developer. Using hecate_task from the selected role default.",
      ),
    ).toBeTruthy();
    expect(within(assistant).getByText("context")).toBeTruthy();
    expect(within(assistant).getByText("Candidates")).toBeTruthy();
    expect(within(assistant).getByText("Body tokens")).toBeTruthy();
    expect(within(assistant).getByText("Truncated")).toBeTruthy();
    expect(within(assistant).getAllByText("Build cockpit UI").length).toBeGreaterThan(0);
  });

  it("renders Project Assistant context budget values", async () => {
    resetProjectWorkMocks();
    vi.mocked(getProjectAssistantContext).mockResolvedValueOnce({
      object: "project_assistant.context",
      data: {
        project: {
          id: project.id,
          name: project.name,
          roots: [],
          created_at: project.created_at,
          updated_at: project.updated_at,
        },
        request: `Queue ${role.name} for ${workItem.title}`,
        selected_work: {
          id: workItem.id,
          title: workItem.title,
          status: workItem.status,
          owner_role_id: workItem.owner_role_id,
          created_at: workItem.created_at,
          updated_at: workItem.updated_at,
        },
        roles: [
          {
            id: role.id,
            name: role.name,
            built_in: role.built_in,
            created_at: "2026-06-01T10:00:00Z",
            updated_at: "2026-06-01T10:00:00Z",
          },
        ],
        assignments: [],
        memory: [],
        memory_candidates: [],
        recent_activity: [],
        budget: {
          memory_body_max_bytes: 4096,
          memory_candidate_body_max_bytes: 2048,
          body_original_bytes: 12000,
          body_returned_bytes: 6144,
          body_tokens_estimate: 321,
          body_truncated_count: 2,
        },
        selection: {
          role_id: role.id,
          role_name: role.name,
          role_source: "selected_work_owner",
          driver_kind: "hecate_task",
          driver_source: "role_default",
          reason:
            "Selected work item is owned by Software developer. Using hecate_task from the selected role default.",
        },
      },
    });
    const user = userEvent.setup();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Selected work: Build cockpit UI");
    const assistant = await screen.findByRole("region", { name: "Project Assistant" });
    await user.click(within(assistant).getByRole("button", { name: "Inspect context" }));

    const context = await within(assistant).findByLabelText("Project Assistant context");
    expect(within(context).getByText("Body tokens")).toBeTruthy();
    expect(within(context).getByText("~321")).toBeTruthy();
    expect(within(context).getByText("Truncated")).toBeTruthy();
    expect(within(context).getByText("2")).toBeTruthy();
  });

  it("surfaces Project Assistant context inspection errors", async () => {
    resetProjectWorkMocks();
    vi.mocked(getProjectAssistantContext).mockRejectedValueOnce(
      new ApiError("project assistant target not found", 404, "not_found"),
    );
    const user = userEvent.setup();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Selected work: Build cockpit UI");
    const assistant = await screen.findByRole("region", { name: "Project Assistant" });
    await user.click(within(assistant).getByRole("button", { name: "Inspect context" }));

    expect(await within(assistant).findByText("project assistant target not found")).toBeTruthy();
    expect(within(assistant).queryByLabelText("Project Assistant context")).toBeNull();
  });

  it("drafts Project Assistant work proposals without an owner role when none exists", async () => {
    resetProjectWorkMocks();
    vi.mocked(getProjectWorkRoles).mockResolvedValue({ object: "project_work_roles", data: [] });
    vi.mocked(getProjectWorkItems).mockResolvedValue({ object: "project_work_items", data: [] });
    const user = userEvent.setup();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    const assistant = await screen.findByRole("region", { name: "Project Assistant" });
    await screen.findByText("No work items for this project.");
    expect(within(assistant).getByText("Project queue")).toBeTruthy();
    fireEvent.change(within(assistant).getByLabelText("Request"), {
      target: { value: "Write project brief\nCapture the next operator task." },
    });
    await user.click(within(assistant).getByRole("button", { name: "Draft proposal" }));

    await waitFor(() => {
      expect(draftProjectAssistant).toHaveBeenCalledWith({
        project_id: project.id,
        request: "Write project brief\nCapture the next operator task.",
      });
    });
  });

  it("dismisses Project Assistant proposals without applying", async () => {
    resetProjectWorkMocks();
    const user = userEvent.setup();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Selected work: Build cockpit UI");
    const assistant = await screen.findByRole("region", { name: "Project Assistant" });
    await user.click(within(assistant).getByRole("button", { name: "Draft proposal" }));
    expect(await within(assistant).findByText("Create assignment")).toBeTruthy();

    await user.click(within(assistant).getByRole("button", { name: "Dismiss proposal" }));

    expect(within(assistant).queryByText("Create assignment")).toBeNull();
    expect(applyProjectAssistant).not.toHaveBeenCalled();
  });

  it("surfaces stale Project Assistant proposal conflicts and refreshes work", async () => {
    resetProjectWorkMocks();
    vi.mocked(applyProjectAssistant).mockRejectedValueOnce(
      new ApiError("project assistant conflict", 409, "conflict"),
    );
    const user = userEvent.setup();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Selected work: Build cockpit UI");
    const assistant = await screen.findByRole("region", { name: "Project Assistant" });
    await user.click(within(assistant).getByRole("button", { name: "Draft proposal" }));
    await within(assistant).findByText("Create assignment");
    await user.click(within(assistant).getByRole("button", { name: "Apply proposal" }));

    expect(await within(assistant).findByText(/proposal is stale, conflicts/)).toBeTruthy();
    expect(getProjectWorkItems).toHaveBeenLastCalledWith(project.id);
    expect(getProjectAssignments).toHaveBeenLastCalledWith(project.id, workItem.id);
  });

  it("surfaces partial Project Assistant apply progress", async () => {
    resetProjectWorkMocks();
    vi.mocked(draftProjectAssistant).mockResolvedValueOnce({
      object: "project_assistant.proposal",
      data: {
        id: "pa_partial",
        title: "Apply two changes",
        summary: "Create an assignment and a memory candidate.",
        requires_confirmation: true,
        actions: [
          {
            kind: "create_assignment",
            target: { project_id: project.id },
            patch: {
              project_id: project.id,
              work_item_id: workItem.id,
              role_id: role.id,
              driver_kind: "hecate_task",
              status: "queued",
            },
            reason: "Queue the selected work.",
          },
          {
            kind: "create_memory_candidate",
            target: { project_id: project.id },
            patch: {
              project_id: project.id,
              title: "Decision",
              body: "Keep the apply flow resumable.",
              source_kind: "operator_note",
            },
            reason: "Capture the decision.",
          },
        ],
        trace_id: "trace_partial_apply",
      },
    });
    vi.mocked(applyProjectAssistant).mockRejectedValueOnce(
      new ApiError("project assistant apply failed at action 1", 409, "conflict", {
        fields: {
          failed_action_index: 1,
          partial_result: {
            proposal_id: "pa_partial",
            applied: false,
            actions: [
              {
                kind: "create_assignment",
                id: "asgn_partial",
                data: { project_id: project.id, assignment_id: "asgn_partial" },
              },
            ],
          },
        },
      }),
    );
    const user = userEvent.setup();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Selected work: Build cockpit UI");
    const assistant = await screen.findByRole("region", { name: "Project Assistant" });
    await user.click(within(assistant).getByRole("button", { name: "Draft proposal" }));
    await within(assistant).findByText("Create memory candidate");
    await user.click(within(assistant).getByRole("button", { name: "Apply proposal" }));

    expect(
      await within(assistant).findByText(
        "Project Assistant applied 1 of 2 actions, then failed at action 2. Apply the same proposal again after fixing the target state to resume from the next unapplied action.",
      ),
    ).toBeTruthy();
    expect(getProjectWorkItems).toHaveBeenLastCalledWith(project.id);
    expect(getProjectAssignments).toHaveBeenLastCalledWith(project.id, workItem.id);
  });

  it("manages project memory entries in the cockpit", async () => {
    resetProjectWorkMocks();
    vi.mocked(getProjectMemory).mockResolvedValue({
      object: "project_memory",
      data: [memoryEntry],
    });
    const user = userEvent.setup();
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await openProjectWorkspaceTab(/Memory \/ Context/);
    expect(await screen.findByText("Commit style")).toBeTruthy();
    expect(screen.getAllByText("operator_memory").length).toBeGreaterThan(0);
    expect(screen.getByText("Use conventional commits.")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Edit memory Commit style" }));
    fireEvent.change(screen.getByLabelText("Body"), {
      target: { value: "Prefer small commits." },
    });
    await user.click(screen.getByRole("button", { name: "Save memory" }));
    expect(updateProjectMemory).toHaveBeenCalledWith(project.id, memoryEntry.id, {
      title: "Commit style",
      body: "Prefer small commits.",
      trust_label: "operator_memory",
      source_kind: "operator",
      source_id: "",
      enabled: true,
    });

    await user.click(screen.getByRole("button", { name: "Memory" }));
    await user.type(screen.getByLabelText("Title"), "Review posture");
    await user.type(screen.getByLabelText("Body"), "Keep generated summaries labelled.");
    await user.click(screen.getByRole("button", { name: "Create memory" }));
    expect(createProjectMemory).toHaveBeenCalledWith(project.id, {
      title: "Review posture",
      body: "Keep generated summaries labelled.",
      trust_label: "operator_memory",
      source_kind: "operator",
      source_id: "",
      enabled: true,
    });

    await user.click(screen.getByRole("button", { name: "Delete memory Commit style" }));
    await user.click(screen.getByRole("button", { name: "Delete memory" }));
    expect(deleteProjectMemory).toHaveBeenCalledWith(project.id, memoryEntry.id);
  });

  it("discovers workspace guidance sources from the memory context panel", async () => {
    resetProjectWorkMocks();
    vi.mocked(discoverProjectContextSources).mockResolvedValue({
      object: "project",
      data: {
        ...project,
        context_sources: [
          {
            id: "ctx_agents",
            kind: "workspace_instruction",
            title: "AGENTS.md",
            path: "AGENTS.md",
            enabled: true,
            format: "agents_md",
            scope: "workspace",
            trust_label: "workspace_guidance",
            source_category: "workspace_guidance",
            metadata: { host: "portable" },
            created_at: "2026-06-08T10:00:00Z",
            updated_at: "2026-06-08T10:00:00Z",
          },
        ],
      },
    });
    const user = userEvent.setup();
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await openProjectWorkspaceTab(/Memory \/ Context/);
    await user.click(screen.getByRole("button", { name: "Discover" }));

    expect(discoverProjectContextSources).toHaveBeenCalledWith(project.id);
    expect((await screen.findAllByText("AGENTS.md")).length).toBeGreaterThan(0);
    expect(screen.getByText("workspace_instruction")).toBeTruthy();
    expect(screen.getByText("agents_md")).toBeTruthy();
    expect(screen.getAllByText("workspace").length).toBeGreaterThan(0);
  });

  it("reviews project memory candidates before promotion", async () => {
    resetProjectWorkMocks();
    const rejectCandidate: ProjectMemoryCandidateRecord = {
      ...memoryCandidate,
      id: "memcand_2",
      title: "Temporary note",
      body: "Maybe skip verification.",
    };
    vi.mocked(getProjectMemory).mockResolvedValue({
      object: "project_memory",
      data: [],
    });
    vi.mocked(getProjectMemoryCandidates)
      .mockResolvedValueOnce({
        object: "project_memory_candidates",
        data: [memoryCandidate, rejectCandidate],
      })
      .mockResolvedValue({
        object: "project_memory_candidates",
        data: [],
      });
    vi.mocked(promoteProjectMemoryCandidate).mockResolvedValue({
      object: "project_memory_candidate",
      data: { ...memoryCandidate, status: "promoted", promoted_memory_id: "mem_promoted" },
    });
    vi.mocked(rejectProjectMemoryCandidate).mockResolvedValue({
      object: "project_memory_candidate",
      data: { ...rejectCandidate, status: "rejected" },
    });

    const user = userEvent.setup();
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await openProjectWorkspaceTab(/Memory \/ Context/);
    expect(await screen.findByText("Generated summary")).toBeTruthy();
    expect(screen.getByText("Temporary note")).toBeTruthy();
    expect(screen.getAllByText("generated_summary").length).toBeGreaterThan(0);
    expect(screen.getAllByText(/Source refs: task_run Implementation run/).length).toBeGreaterThan(
      0,
    );

    await user.click(
      screen.getByRole("button", { name: "Reject memory candidate Temporary note" }),
    );
    expect(rejectProjectMemoryCandidate).toHaveBeenCalledWith(project.id, "memcand_2", {});

    await user.click(
      screen.getByRole("button", { name: "Promote memory candidate Generated summary" }),
    );
    expect(screen.getByRole("button", { name: "Promote memory" })).toBeTruthy();
    expect(screen.getByText("Candidate provenance")).toBeTruthy();
    expect(screen.getAllByText(/Source refs: task_run Implementation run/).length).toBeGreaterThan(
      0,
    );
    fireEvent.change(screen.getByLabelText("Trust label"), {
      target: { value: "operator_memory" },
    });
    fireEvent.change(screen.getByLabelText("Source kind"), {
      target: { value: "operator" },
    });
    await user.click(screen.getByRole("button", { name: "Promote memory" }));

    expect(promoteProjectMemoryCandidate).toHaveBeenCalledWith(project.id, memoryCandidate.id, {
      title: "Generated summary",
      body: "Keep generated content lower trust until reviewed.",
      trust_label: "operator_memory",
      source_kind: "operator",
      source_id: "run_1",
      enabled: true,
    });
  });

  it("resets the project memory editor when switching entries", async () => {
    resetProjectWorkMocks();
    const generatedEntry: ProjectMemoryRecord = {
      ...memoryEntry,
      id: "mem_2",
      title: "Generated handoff",
      body: "Summarize cautiously.",
      trust_label: "generated_summary",
      source_kind: "handoff",
    };
    vi.mocked(getProjectMemory).mockResolvedValue({
      object: "project_memory",
      data: [memoryEntry, generatedEntry],
    });
    const user = userEvent.setup();
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await openProjectWorkspaceTab(/Memory \/ Context/);
    expect(await screen.findByText("Commit style")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Edit memory Commit style" }));
    expect(screen.getByLabelText("Title")).toHaveValue("Commit style");
    expect(screen.getByLabelText("Body")).toHaveValue("Use conventional commits.");

    await user.click(screen.getByRole("button", { name: "Edit memory Generated handoff" }));

    expect(screen.getByLabelText("Title")).toHaveValue("Generated handoff");
    expect(screen.getByLabelText("Body")).toHaveValue("Summarize cautiously.");
  });

  it("clears stale project memory while switching projects", async () => {
    resetProjectWorkMocks();
    const secondProject: ProjectRecord = {
      ...project,
      id: "proj_2",
      name: "Apollo",
      roots: [
        {
          ...project.roots[0],
          id: "root_2",
          path: "/Users/alice/dev/apollo",
        },
      ],
    };
    let resolveSecondMemory = (_value: {
      object: "project_memory";
      data: ProjectMemoryRecord[];
    }) => {};
    const secondMemoryRequest = new Promise<{
      object: "project_memory";
      data: ProjectMemoryRecord[];
    }>((resolve) => {
      resolveSecondMemory = resolve;
    });
    vi.mocked(getProjectMemory).mockImplementation(async (projectID) => {
      if (projectID === secondProject.id) {
        return secondMemoryRequest;
      }
      return { object: "project_memory", data: [memoryEntry] };
    });
    window.localStorage.setItem("hecate.project", project.id);
    const user = userEvent.setup();
    const state = createRuntimeConsoleFixture({
      projects: [project, secondProject],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await openProjectWorkspaceTab(/Memory \/ Context/);
    expect(await screen.findByText("Use conventional commits.")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Edit memory Commit style" }));
    expect(screen.getByRole("button", { name: "Save memory" })).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Open project Apollo" }));

    await waitFor(() => {
      expect(getProjectMemory).toHaveBeenCalledWith(secondProject.id, true);
    });
    expect(screen.queryByText("Use conventional commits.")).toBeNull();
    expect(screen.queryByRole("button", { name: "Save memory" })).toBeNull();

    resolveSecondMemory({ object: "project_memory", data: [] });
    expect(await screen.findByText("No project memory entries saved yet.")).toBeTruthy();
  });

  it("keeps project work visible when activity loading fails", async () => {
    resetProjectWorkMocks();
    vi.mocked(getProjectActivity).mockRejectedValueOnce(new Error("activity failed"));
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    expect(await screen.findByText("Expose project work and native starts.")).toBeTruthy();
    expect(screen.getByText("Work Queue")).toBeTruthy();
    expect(screen.queryByText("activity failed")).toBeNull();
  });

  it("clears stale work item selection before switching projects", async () => {
    resetProjectWorkMocks();
    const secondProject: ProjectRecord = {
      ...project,
      id: "proj_2",
      name: "Apollo",
      roots: [
        {
          ...project.roots[0],
          id: "root_2",
          path: "/Users/alice/dev/apollo",
        },
      ],
    };
    const secondWorkItem: ProjectWorkItemRecord = {
      ...workItem,
      id: "work_2",
      project_id: "proj_2",
      title: "Build Apollo cockpit",
      brief: "Show Apollo project work.",
    };
    vi.mocked(getProjectWorkItems).mockImplementation(async (projectID) => ({
      object: "project_work_items",
      data:
        projectID === secondProject.id
          ? [{ ...secondWorkItem, assignments: [] }]
          : [{ ...workItem, assignments: [hecateAssignment] }],
    }));
    vi.mocked(getProjectWorkItem).mockImplementation(async (projectID, workItemID) => ({
      object: "project_work_item",
      data:
        projectID === secondProject.id && workItemID === secondWorkItem.id
          ? secondWorkItem
          : workItem,
    }));
    vi.mocked(getProjectAssignments).mockImplementation(async (projectID) => ({
      object: "project_assignments",
      data: projectID === secondProject.id ? [] : [hecateAssignment],
    }));
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project, secondProject],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    expect(await screen.findByText("Expose project work and native starts.")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Open project Apollo" }));

    expect(await screen.findByText("Show Apollo project work.")).toBeTruthy();
    expect(getProjectWorkItem).toHaveBeenCalledWith(secondProject.id, secondWorkItem.id);
    expect(getProjectWorkItem).not.toHaveBeenCalledWith(secondProject.id, workItem.id);
  });

  it("uses projected work-item assignments for list summaries without per-item requests", async () => {
    resetProjectWorkMocks();
    const secondWorkItem: ProjectWorkItemRecord = {
      ...workItem,
      id: "work_2",
      title: "Write project docs",
    };
    const emptyWorkItem: ProjectWorkItemRecord = {
      ...workItem,
      id: "work_3",
      title: "Plan empty lane",
    };
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [
        { ...workItem, assignments: [hecateAssignment] },
        {
          ...secondWorkItem,
          assignments: [{ ...hecateAssignment, id: "asgn_2", work_item_id: secondWorkItem.id }],
        },
        emptyWorkItem,
      ],
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    const firstRow = await screen.findByRole("button", {
      name: "Open work item Build cockpit UI",
    });
    const secondRow = await screen.findByRole("button", {
      name: "Open work item Write project docs",
    });
    const emptyRow = await screen.findByRole("button", {
      name: "Open work item Plan empty lane",
    });
    expect(within(firstRow).queryByText("1 assignment")).toBeTruthy();
    expect(within(secondRow).getByText("1 assignment")).toBeTruthy();
    expect(within(emptyRow).queryByText(/assignment/)).toBeNull();
    await waitFor(() => {
      expect(getProjectAssignments).toHaveBeenCalledTimes(1);
    });
  });

  it("preserves the selected work item when refreshing project work", async () => {
    resetProjectWorkMocks();
    const secondWorkItem: ProjectWorkItemRecord = {
      ...workItem,
      id: "work_2",
      title: "Write project docs",
      brief: "Document the project workflow.",
    };
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [
        { ...workItem, assignments: [hecateAssignment] },
        { ...secondWorkItem, assignments: [] },
      ],
    });
    vi.mocked(getProjectWorkItem).mockImplementation(async (_projectID, workItemID) => ({
      object: "project_work_item",
      data: workItemID === secondWorkItem.id ? secondWorkItem : workItem,
    }));
    vi.mocked(getProjectAssignments).mockImplementation(async (_projectID, workItemID) => ({
      object: "project_assignments",
      data: workItemID === secondWorkItem.id ? [] : [hecateAssignment],
    }));
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    const secondRow = await screen.findByRole("button", {
      name: "Open work item Write project docs",
    });
    await userEvent.click(secondRow);
    expect(await screen.findByText("Document the project workflow.")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Refresh project work" }));

    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "Open work item Write project docs" }),
      ).toHaveAttribute("aria-current", "true");
    });
    expect(await screen.findByText("Document the project workflow.")).toBeTruthy();
  });

  it("shows selected work item assignments and projected execution state", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    expect(await screen.findByText("Expose project work and native starts.")).toBeTruthy();
    const detail = screen.getByLabelText("Selected work item");
    expect(within(detail).getAllByText("Software developer").length).toBeGreaterThan(0);
    expect(within(detail).getAllByText("approval").length).toBeGreaterThan(0);
    expect(within(detail).getAllByText("2 approval pending").length).toBeGreaterThan(0);
    expect(within(detail).getByText("4 steps")).toBeTruthy();
    expect(within(detail).getAllByText("ollama / qwen2.5-coder").length).toBeGreaterThan(0);
  });

  it("renders project activity inbox states and actions", async () => {
    resetProjectWorkMocks();
    const onOpenTask = vi.fn();
    const onOpenChat = vi.fn();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(
      withRuntimeConsole(<ProjectsView onOpenTask={onOpenTask} onOpenChat={onOpenChat} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    expect(await screen.findByText("Work Queue")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Show all work items" })).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Show blocked assignments" }));
    const queue = screen.getByLabelText("Work queue");
    expect(
      within(queue).getByRole("button", { name: "Open work item Build cockpit UI" }),
    ).toBeTruthy();
    expect(within(queue).getByText("1 assignment")).toBeTruthy();

    const detail = screen.getByRole("region", { name: "Selected work item" });
    expect(within(detail).getAllByText("2 approval pending").length).toBeGreaterThan(0);
    const evidence = within(detail).getByRole("group", { name: "Execution evidence" });
    expect(within(evidence).getByText("Task")).toBeTruthy();
    expect(within(evidence).getByText("task_1")).toBeTruthy();
    expect(within(evidence).getByText("Run")).toBeTruthy();
    expect(within(evidence).getByText("run_1")).toBeTruthy();
    expect(within(evidence).getByText("Context snapshot")).toBeTruthy();
    expect(within(evidence).getByText("ctx_assignment_1")).toBeTruthy();
    expect(within(evidence).getByText("Provider / model")).toBeTruthy();
    expect(within(evidence).getByText("ollama / qwen2.5-coder")).toBeTruthy();

    await userEvent.click(within(detail).getByRole("button", { name: "Open task" }));
    expect(onOpenTask).toHaveBeenCalledWith("task_1", "run_1");

    await userEvent.click(
      within(detail).getByTitle(
        "Inspect the best available stored context snapshot for this assignment.",
      ),
    );
    expect(getProjectAssignmentContext).toHaveBeenCalledWith(
      project.id,
      workItem.id,
      hecateAssignment.id,
    );
    const dialog = await screen.findByRole("dialog", { name: "Assignment asgn_1 context" });
    expect(dialog).toBeTruthy();
    expect(within(dialog).getByText("Profile")).toBeTruthy();
    expect(within(dialog).getByText("Skills")).toBeTruthy();
    expect(within(dialog).getByText("Memory")).toBeTruthy();
    expect(within(dialog).getByText("Project sources")).toBeTruthy();
    expect(within(dialog).getByText("Work context")).toBeTruthy();
    expect(within(dialog).getByText("Runtime evidence")).toBeTruthy();
    expect(within(dialog).getByText("Task")).toBeTruthy();
    expect(within(dialog).getByText("task_1")).toBeTruthy();
    expect(within(dialog).getByText("Project skills")).toBeTruthy();
    expect(within(dialog).getByText("Expose project work and native starts.")).toBeTruthy();

    await userEvent.click(within(detail).getByRole("button", { name: "Open chat" }));
    expect(onOpenChat).toHaveBeenCalledWith(
      expect.objectContaining({
        projectID: project.id,
        model: "qwen2.5-coder",
      }),
    );

    await userEvent.click(
      within(queue).getByRole("button", { name: "Open work item Build cockpit UI" }),
    );
    expect(screen.getByRole("article", { name: "Build cockpit UI work item" })).toBeTruthy();
  });

  it("prepares queued external-agent assignment chats from the selected work item", async () => {
    resetProjectWorkMocks();
    const externalAssignment: ProjectAssignmentRecord = {
      ...hecateAssignment,
      driver_kind: "external_agent",
      status: "queued",
      execution_ref: undefined,
      execution: undefined,
    };
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [{ ...workItem, assignments: [externalAssignment] }],
    });
    vi.mocked(getProjectAssignments).mockResolvedValue({
      object: "project_assignments",
      data: [externalAssignment],
    });
    let resolveStartAssignment: (
      value: Awaited<ReturnType<typeof startProjectAssignment>>,
    ) => void = () => {};
    vi.mocked(startProjectAssignment).mockReturnValue(
      new Promise((resolve) => {
        resolveStartAssignment = resolve;
      }),
    );
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(
      await screen.findByRole("button", { name: "Open work item Build cockpit UI" }),
    );
    const detail = await screen.findByRole("region", { name: "Selected work item" });
    const prepareButton = within(detail).getByRole("button", { name: "Prepare chat" });
    await userEvent.dblClick(prepareButton);

    expect(startProjectAssignment).toHaveBeenCalledWith(
      project.id,
      workItem.id,
      externalAssignment.id,
      "external_agent",
    );
    expect(startProjectAssignment).toHaveBeenCalledTimes(1);
    resolveStartAssignment({
      object: "project_assignment",
      data: {
        ...externalAssignment,
        status: "running",
        execution_ref: {
          kind: "chat_session",
          chat_session_id: "chat_external_1",
          context_snapshot_id: "ctx_external_1",
          status: "running",
        },
      },
    });
    await waitFor(() => {
      expect(prepareButton).not.toBeDisabled();
    });
  });

  it("opens linked external-agent assignment chat sessions directly", async () => {
    resetProjectWorkMocks();
    const onOpenChat = vi.fn();
    const linkedAssignment: ProjectAssignmentRecord = {
      ...hecateAssignment,
      driver_kind: "external_agent",
      status: "running",
      execution_ref: {
        kind: "chat_session",
        chat_session_id: "chat_external_1",
        context_snapshot_id: "ctx_external_1",
        status: "running",
      },
      execution: undefined,
    };
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [{ ...workItem, assignments: [linkedAssignment] }],
    });
    vi.mocked(getProjectAssignments).mockResolvedValue({
      object: "project_assignments",
      data: [linkedAssignment],
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(
      withRuntimeConsole(<ProjectsView onOpenChat={onOpenChat} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Open work item Build cockpit UI" }),
    );
    const detail = await screen.findByRole("region", { name: "Selected work item" });
    await userEvent.click(within(detail).getByRole("button", { name: "Open chat" }));

    expect(onOpenChat).toHaveBeenCalledWith({
      projectID: project.id,
      chatSessionID: "chat_external_1",
    });
  });

  it("prefills handoffs from linked external-agent assignment context", async () => {
    resetProjectWorkMocks();
    const linkedAssignment: ProjectAssignmentRecord = {
      ...hecateAssignment,
      driver_kind: "external_agent",
      status: "running",
      execution_ref: {
        kind: "chat_session",
        chat_session_id: "chat_external_1",
        context_snapshot_id: "ctx_external_1",
        status: "running",
      },
      execution: undefined,
    };
    vi.mocked(getProjectActivity).mockResolvedValue({
      object: "project_activity",
      data: {
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
          active: [
            {
              id: linkedAssignment.id,
              project_id: project.id,
              work_item: {
                id: workItem.id,
                title: workItem.title,
                status: "running",
                priority: workItem.priority,
              },
              assignment: linkedAssignment,
              role,
              status: "running",
              blocking_signal: "running",
              status_summary: "linked chat · running · assistant completed · 2 messages",
              linked_chat_id: "chat_external_1",
              linked_message_id: "msg_external_1",
              linked_chat: {
                id: "chat_external_1",
                title: "External implementation",
                agent_id: "codex",
                driver_kind: "acp",
                native_session_id: "native_external_1",
                status: "running",
                latest_message_id: "msg_external_1",
                latest_role: "assistant",
                latest_status: "completed",
                message_count: 2,
                updated_at: "2026-06-02T11:20:00Z",
              },
              artifact_summary: { count: 0 },
              handoff_summary: { count: 0 },
              updated_at: "2026-06-02T11:20:00Z",
            },
          ],
          blocked: [],
          completed: [],
          recent: [],
        },
        recent: [],
      },
    });
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [{ ...workItem, assignments: [linkedAssignment] }],
    });
    vi.mocked(getProjectAssignments).mockResolvedValue({
      object: "project_assignments",
      data: [linkedAssignment],
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(
      await screen.findByRole("button", { name: "Open work item Build cockpit UI" }),
    );
    const detail = await screen.findByRole("region", { name: "Selected work item" });
    expect(within(detail).getByText("chat completed")).toBeTruthy();
    expect(
      within(detail).getByText("linked chat · running · assistant completed · 2 messages"),
    ).toBeTruthy();

    await userEvent.click(
      within(detail).getByRole("button", {
        name: `Create handoff from assignment ${linkedAssignment.id}`,
      }),
    );
    const dialog = await screen.findByRole("dialog", { name: "New handoff" });
    expect(within(dialog).getByLabelText("Source assignment")).toHaveValue(linkedAssignment.id);
    expect(within(dialog).getByLabelText("Source chat")).toHaveValue("chat_external_1");
    expect(within(dialog).getByLabelText("Source message")).toHaveValue("msg_external_1");
    expect(within(dialog).getByLabelText("Context refs")).toHaveValue(
      "ctx_external_1, chat_external_1, msg_external_1",
    );
  });

  it("renders a project timeline from activity, decisions, artifacts, and memory", async () => {
    resetProjectWorkMocks();
    const onOpenTask = vi.fn();
    const onOpenChat = vi.fn();
    vi.mocked(getProjectMemory).mockResolvedValue({
      object: "project_memory",
      data: [
        {
          ...memoryEntry,
          updated_at: "2026-06-02T10:58:00Z",
        },
      ],
    });
    vi.mocked(getProjectMemoryCandidates).mockResolvedValue({
      object: "project_memory_candidates",
      data: [
        {
          ...memoryCandidate,
          updated_at: "2026-06-02T11:08:00Z",
        },
        {
          ...memoryCandidate,
          id: "memcand_promoted",
          title: "Promoted convention",
          body: "Promoted into durable project memory.",
          status: "promoted",
          promoted_memory_id: "mem_promoted",
          created_at: "2026-06-02T10:57:00Z",
          updated_at: "2026-06-02T10:57:00Z",
        },
        {
          ...memoryCandidate,
          id: "memcand_rejected",
          title: "Rejected guess",
          body: "Rejected before it became durable memory.",
          status: "rejected",
          status_reason: "Too speculative.",
          created_at: "2026-06-02T10:56:00Z",
          updated_at: "2026-06-02T10:56:00Z",
        },
      ],
    });
    vi.mocked(getProjectActivity).mockResolvedValue({
      object: "project_activity",
      data: {
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
          blocked: [
            {
              id: hecateAssignment.id,
              project_id: project.id,
              work_item: {
                id: workItem.id,
                title: workItem.title,
                status: "running",
                priority: workItem.priority,
              },
              assignment: hecateAssignment,
              role,
              status: "awaiting_approval",
              blocking_signal: "awaiting_approval",
              status_summary: "2 approval pending",
              linked_task_id: "task_1",
              linked_run_id: "run_1",
              artifact_summary: {
                count: 2,
                latest_kind: "decision_note",
                latest_title: "Release gate",
                latest_at: "2026-06-02T11:10:00Z",
              },
              handoff_summary: {
                count: 1,
                pending_count: 1,
                latest_status: "pending",
                latest_title: "QA handoff",
                latest_at: "2026-06-02T11:07:00Z",
                target_role_id: "reviewer_qa",
              },
              recent_artifacts: [
                {
                  id: "art_decision",
                  project_id: project.id,
                  work_item_id: workItem.id,
                  assignment_id: hecateAssignment.id,
                  kind: "decision_note",
                  title: "Release gate",
                  body: "Ship only after UI checks pass.",
                  author_role_id: "reviewer",
                  created_at: "2026-06-02T11:10:00Z",
                  updated_at: "2026-06-02T11:10:00Z",
                },
                {
                  id: "art_handoff",
                  project_id: project.id,
                  work_item_id: workItem.id,
                  assignment_id: hecateAssignment.id,
                  kind: "handoff",
                  title: "Runtime notes",
                  body: "Approval is waiting.",
                  created_at: "2026-06-02T11:05:00Z",
                  updated_at: "2026-06-02T11:05:00Z",
                },
              ],
              recent_handoffs: [
                {
                  id: "handoff_1",
                  project_id: project.id,
                  work_item_id: workItem.id,
                  source_assignment_id: hecateAssignment.id,
                  source_run_id: "run_1",
                  title: "QA handoff",
                  summary: "Ready for QA handoff.",
                  recommended_next_action: "Create a QA assignment.",
                  target_role_id: "reviewer_qa",
                  status: "pending",
                  provenance_kind: "agent_draft",
                  trust_label: "operator_reviewed",
                  created_by_role_id: "software_developer",
                  created_at: "2026-06-02T11:07:00Z",
                  updated_at: "2026-06-02T11:07:00Z",
                  status_changed_at: "2026-06-02T11:07:00Z",
                },
              ],
              updated_at: "2026-06-02T11:00:00Z",
            },
          ],
          completed: [],
          recent: [],
        },
        recent: [],
      },
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(
      withRuntimeConsole(<ProjectsView onOpenTask={onOpenTask} onOpenChat={onOpenChat} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    await openProjectWorkspaceTab(/Timeline \/ Decision Log/);
    await waitFor(() => {
      expect(getProjectMemoryCandidates).toHaveBeenCalledWith(project.id, true);
    });
    const timeline = screen.getByLabelText("Project timeline");
    await waitFor(() => {
      expect(within(timeline).getByText("Release gate")).toBeTruthy();
    });
    expect(within(timeline).getByText("Ship only after UI checks pass.")).toBeTruthy();
    expect(within(timeline).getByText("Memory candidate: Generated summary")).toBeTruthy();
    expect(within(timeline).getByText("Memory candidate: Promoted convention")).toBeTruthy();
    expect(within(timeline).getByText("Memory candidate: Rejected guess")).toBeTruthy();
    expect(within(timeline).getByText("QA handoff")).toBeTruthy();
    expect(within(timeline).getByText("Ready for QA handoff.")).toBeTruthy();
    expect(within(timeline).getByText("Runtime notes")).toBeTruthy();
    expect(within(timeline).getByText("Context memory: Commit style")).toBeTruthy();
    const story = timeline.textContent ?? "";
    expect(story.indexOf("Release gate")).toBeLessThan(
      story.indexOf("Memory candidate: Generated summary"),
    );
    expect(story.indexOf("Memory candidate: Generated summary")).toBeLessThan(
      story.indexOf("QA handoff"),
    );
    expect(story.indexOf("QA handoff")).toBeLessThan(story.indexOf("Runtime notes"));
    expect(story.indexOf("Runtime notes")).toBeLessThan(
      story.indexOf("Context memory: Commit style"),
    );

    const decisionLog = screen.getByLabelText("Decision log");
    expect(within(decisionLog).getByText("Release gate")).toBeTruthy();
    expect(within(decisionLog).getByText("reviewer")).toBeTruthy();

    await userEvent.click(
      within(timeline).getByRole("button", { name: /Open timeline task task_1/ }),
    );
    expect(onOpenTask).toHaveBeenCalledWith("task_1", "run_1");

    await userEvent.click(
      within(timeline).getByRole("button", { name: /Open timeline chat for Build cockpit UI/ }),
    );
    expect(onOpenChat).toHaveBeenCalledWith(
      expect.objectContaining({
        projectID: project.id,
        model: "qwen2.5-coder",
      }),
    );
  });

  it("shows compact counts when timeline and decisions are truncated", async () => {
    resetProjectWorkMocks();
    vi.mocked(getProjectMemoryCandidates).mockResolvedValue({
      object: "project_memory_candidates",
      data: Array.from({ length: 6 }, (_, index) => ({
        ...memoryCandidate,
        id: `memcand_${index + 1}`,
        title: `Candidate ${index + 1}`,
        body: `Candidate body ${index + 1}.`,
        updated_at: `2026-06-02T11:${20 + index}:00Z`,
      })),
    });
    vi.mocked(getProjectActivity).mockResolvedValue({
      object: "project_activity",
      data: {
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
          blocked: [
            {
              id: hecateAssignment.id,
              project_id: project.id,
              work_item: {
                id: workItem.id,
                title: workItem.title,
                status: "running",
                priority: workItem.priority,
              },
              assignment: hecateAssignment,
              role,
              status: "awaiting_approval",
              blocking_signal: "awaiting_approval",
              status_summary: "2 approval pending",
              linked_task_id: "task_1",
              linked_run_id: "run_1",
              artifact_summary: {
                count: 6,
                latest_kind: "decision_note",
                latest_title: "Release decision 6",
                latest_at: "2026-06-02T11:16:00Z",
              },
              handoff_summary: { count: 0 },
              recent_artifacts: Array.from({ length: 6 }, (_, index) => ({
                id: `art_decision_${index + 1}`,
                project_id: project.id,
                work_item_id: workItem.id,
                assignment_id: hecateAssignment.id,
                kind: "decision_note",
                title: `Release decision ${index + 1}`,
                body: `Decision body ${index + 1}.`,
                created_at: `2026-06-02T11:${10 + index}:00Z`,
                updated_at: `2026-06-02T11:${10 + index}:00Z`,
              })),
              updated_at: "2026-06-02T11:00:00Z",
            },
          ],
          completed: [],
          recent: [],
        },
        recent: [],
      },
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await openProjectWorkspaceTab(/Timeline \/ Decision Log/);
    await waitFor(() => {
      expect(screen.getByText("Showing 12 of 13 story items.")).toBeTruthy();
    });
    expect(screen.getByText("Showing 5 of 6 decisions.")).toBeTruthy();
  });

  it("keeps the decision log explicit when no decision artifacts exist", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await openProjectWorkspaceTab(/Timeline \/ Decision Log/);
    expect(
      screen.getByText(/No explicit decision notes yet. Existing decision_note artifacts/),
    ).toBeTruthy();
  });

  it("keeps live approval rows in activity inbox while needs attention owns setup gaps", async () => {
    resetProjectWorkMocks();
    vi.mocked(getProjectMemory).mockResolvedValue({
      object: "project_memory",
      data: [memoryEntry],
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Work Queue");
    expect(screen.queryByText("Project Health")).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Show blocked assignments" }));
    const activity = screen.getByLabelText("Work queue");
    expect(
      within(activity).getByRole("button", { name: "Open work item Build cockpit UI" }),
    ).toBeTruthy();
    const health = await openProjectAttentionMenu();
    expect(within(health).queryByText(/Approval waiting: Build cockpit UI/i)).toBeNull();
    expect(within(health).getByText("No project attention items detected.")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Project settings" }));
    expect(screen.getByRole("complementary", { name: "Project settings panel" })).toHaveStyle({
      width: "380px",
    });
    expect(
      within(screen.getByRole("region", { name: "Selected work item" })).queryByRole(
        "complementary",
        { name: "Project settings panel" },
      ),
    ).toBeNull();
    expect(screen.getByText("Assignment defaults")).toBeTruthy();
    expect(screen.getByText("Project context")).toBeTruthy();
  });

  it("uses the shared chat right-panel width for project settings", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    window.localStorage.setItem("hecate.chat.rightPanelWidth", "432");
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Project settings" }));
    const panel = screen.getByRole("complementary", { name: "Project settings panel" });
    expect(panel).toHaveStyle({ width: "432px" });

    fireEvent.keyDown(screen.getByRole("separator", { name: "Resize right panel" }), {
      key: "ArrowLeft",
    });
    expect(panel).toHaveStyle({ width: "440px" });
    expect(window.localStorage.getItem("hecate.chat.rightPanelWidth")).toBe("440");
  });

  it("keeps live failures in activity inbox while stale links remain in needs attention", async () => {
    resetProjectWorkMocks();
    const failedAssignment: ProjectAssignmentRecord = {
      ...hecateAssignment,
      id: "asgn_failed",
      status: "failed",
      execution_ref: {
        kind: "task_run",
        task_id: "task_1",
        run_id: "run_failed",
        status: "failed",
      },
      execution: {
        ...hecateAssignment.execution,
        run_id: "run_failed",
        status: "failed",
        pending_approval_count: 0,
      },
      updated_at: "2026-06-02T12:00:00Z",
    };
    const staleAssignment: ProjectAssignmentRecord = {
      ...hecateAssignment,
      id: "asgn_stale_health",
      status: "running",
      execution_ref: {
        kind: "task_run",
        task_id: "task_1",
        run_id: "run_missing",
        status: "running",
        missing: true,
      },
      execution: {
        ...hecateAssignment.execution,
        run_id: "run_missing",
        status: "running",
        pending_approval_count: 0,
        missing: true,
      },
      updated_at: "2026-06-01T08:00:00Z",
    };
    vi.mocked(getProjectActivity).mockResolvedValue({
      object: "project_activity",
      data: {
        project_id: project.id,
        summary: {
          work_item_count: 1,
          assignment_count: 3,
          active_count: 0,
          blocked_count: 3,
          completed_count: 0,
          recent_count: 3,
        },
        buckets: {
          active: [],
          blocked: [
            {
              id: hecateAssignment.id,
              project_id: project.id,
              work_item: {
                id: workItem.id,
                title: workItem.title,
                status: "running",
                priority: workItem.priority,
              },
              assignment: hecateAssignment,
              role,
              status: "awaiting_approval",
              blocking_signal: "awaiting_approval",
              status_summary: "2 approval pending",
              linked_task_id: "task_1",
              linked_run_id: "run_1",
              artifact_summary: { count: 0 },
              handoff_summary: { count: 0 },
              updated_at: "2026-06-02T11:00:00Z",
            },
            {
              id: failedAssignment.id,
              project_id: project.id,
              work_item: {
                id: workItem.id,
                title: workItem.title,
                status: "running",
                priority: workItem.priority,
              },
              assignment: failedAssignment,
              role,
              status: "failed",
              blocking_signal: "failed",
              status_summary: "execution failed",
              linked_task_id: "task_1",
              linked_run_id: "run_failed",
              artifact_summary: { count: 0 },
              handoff_summary: { count: 0 },
              updated_at: "2026-06-02T12:00:00Z",
            },
            {
              id: staleAssignment.id,
              project_id: project.id,
              work_item: {
                id: workItem.id,
                title: workItem.title,
                status: "running",
                priority: workItem.priority,
              },
              assignment: staleAssignment,
              role,
              status: "running",
              blocking_signal: "stale_unknown",
              status_summary: "linked run missing",
              linked_task_id: "task_1",
              linked_run_id: "run_missing",
              artifact_summary: { count: 0 },
              handoff_summary: { count: 0 },
              updated_at: "2026-06-01T08:00:00Z",
            },
          ],
          completed: [],
          recent: [],
        },
        recent: [],
      },
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Work Queue");
    await userEvent.click(screen.getByRole("button", { name: "Show blocked assignments" }));
    const activity = screen.getByLabelText("Work queue");
    expect(
      within(activity).getByRole("button", { name: "Open work item Build cockpit UI" }),
    ).toBeTruthy();
    const health = await openProjectAttentionMenu();
    expect(within(health).queryByText(/Execution needs review: Build cockpit UI/i)).toBeNull();
    expect(within(health).getByText(/Stale or unknown assignment: Build cockpit UI/i)).toBeTruthy();
  });

  it("surfaces handoff and memory candidate health after project promotion surfaces load", async () => {
    resetProjectWorkMocks();
    const handoffActivity: ProjectActivityData = {
      project_id: project.id,
      summary: {
        work_item_count: 1,
        assignment_count: 1,
        active_count: 0,
        blocked_count: 0,
        completed_count: 0,
        recent_count: 1,
      },
      buckets: {
        active: [],
        blocked: [],
        completed: [],
        recent: [
          {
            id: hecateAssignment.id,
            project_id: project.id,
            work_item: {
              id: workItem.id,
              title: workItem.title,
              status: "running",
              priority: workItem.priority,
            },
            assignment: {
              ...hecateAssignment,
              execution: {
                ...hecateAssignment.execution,
                status: "running",
                pending_approval_count: 0,
              },
            },
            role,
            status: "running",
            blocking_signal: "running",
            status_summary: "work recently updated",
            linked_task_id: "task_1",
            linked_run_id: "run_1",
            artifact_summary: { count: 0 },
            handoff_summary: {
              count: 4,
              pending_count: 1,
              accepted_count: 1,
              latest_status: "pending",
              latest_title: "QA handoff",
              latest_at: "2026-06-04T10:00:00Z",
            },
            recent_handoffs: [
              {
                id: "handoff_pending",
                project_id: project.id,
                work_item_id: workItem.id,
                source_assignment_id: hecateAssignment.id,
                target_role_id: "reviewer_qa",
                title: "QA handoff",
                summary: "Implementation is ready for review.",
                recommended_next_action: "Create a queued QA assignment.",
                status: "pending",
                provenance_kind: "agent_draft",
                trust_label: "operator_reviewed",
                created_at: "2026-06-04T10:00:00Z",
                updated_at: "2026-06-04T10:00:00Z",
                status_changed_at: "2026-06-04T10:00:00Z",
              },
              {
                id: "handoff_superseded",
                project_id: project.id,
                work_item_id: workItem.id,
                title: "Old QA handoff",
                summary: "Earlier handoff.",
                recommended_next_action: "Ignore the old handoff.",
                status: "superseded",
                provenance_kind: "operator",
                trust_label: "operator_reviewed",
                created_at: "2026-06-04T09:00:00Z",
                updated_at: "2026-06-04T09:30:00Z",
                status_changed_at: "2026-06-04T09:30:00Z",
              },
              {
                id: "handoff_accepted",
                project_id: project.id,
                work_item_id: workItem.id,
                title: "Accepted QA handoff",
                summary: "Already accepted.",
                recommended_next_action: "Use the accepted target assignment.",
                status: "accepted",
                provenance_kind: "operator",
                trust_label: "operator_reviewed",
                created_at: "2026-06-04T08:45:00Z",
                updated_at: "2026-06-04T09:15:00Z",
                status_changed_at: "2026-06-04T09:15:00Z",
              },
              {
                id: "handoff_dismissed",
                project_id: project.id,
                work_item_id: workItem.id,
                title: "Dismissed QA handoff",
                summary: "No longer needed.",
                recommended_next_action: "No action.",
                status: "dismissed",
                provenance_kind: "operator",
                trust_label: "operator_reviewed",
                created_at: "2026-06-04T08:00:00Z",
                updated_at: "2026-06-04T08:30:00Z",
                status_changed_at: "2026-06-04T08:30:00Z",
              },
            ],
            updated_at: "2026-06-04T10:00:00Z",
          },
        ],
      },
      recent: [],
    };
    const sourceHandoffItem = handoffActivity.buckets.recent[0]!;
    handoffActivity.buckets.recent.push({
      ...sourceHandoffItem,
      id: "asgn_target",
      assignment: {
        ...sourceHandoffItem.assignment,
        id: "asgn_target",
        role_id: "reviewer_qa",
      },
      role: {
        ...role,
        id: "reviewer_qa",
        name: "Reviewer QA",
      },
    });
    vi.mocked(getProjectActivity).mockResolvedValue({
      object: "project_activity",
      data: handoffActivity,
    });
    vi.mocked(getProjectMemoryCandidates).mockResolvedValue({
      object: "project_memory_candidates",
      data: [
        memoryCandidate,
        {
          ...memoryCandidate,
          id: "memcand_promoted",
          title: "Promoted convention",
          status: "promoted",
        },
        {
          ...memoryCandidate,
          id: "memcand_rejected",
          title: "Rejected guess",
          status: "rejected",
        },
      ],
    });

    const user = userEvent.setup();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Work Queue");
    const health = await openProjectAttentionMenu();
    expect(within(health).getByText(/Pending handoff: Build cockpit UI/i)).toBeTruthy();
    expect(within(health).getByText(/QA handoff/i)).toBeTruthy();
    expect(within(health).getByText("Memory candidate pending review")).toBeTruthy();
    expect(
      screen.queryByRole("button", { name: "Promote memory candidate Promoted convention" }),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Reject memory candidate Rejected guess" }),
    ).toBeNull();

    await user.click(within(health).getByRole("button", { name: "Review memory candidate" }));
    expect(screen.getByRole("button", { name: "Promote memory" })).toBeTruthy();
  });

  it("uses activity inbox tabs to focus activity buckets", async () => {
    resetProjectWorkMocks();
    const activeActivity: ProjectActivityData = {
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
        active: [
          {
            id: "asgn_active",
            project_id: project.id,
            work_item: {
              id: workItem.id,
              title: workItem.title,
              status: "running",
              priority: workItem.priority,
            },
            assignment: {
              ...hecateAssignment,
              id: "asgn_active",
              status: "running",
              execution: { ...hecateAssignment.execution, status: "running" },
            },
            role,
            status: "running",
            blocking_signal: "running",
            status_summary: "run live now",
            linked_task_id: "task_1",
            linked_run_id: "run_1",
            artifact_summary: { count: 1 },
            updated_at: "2026-06-02T11:05:00Z",
          },
        ],
        blocked: [],
        completed: [],
        recent: [],
      },
      recent: [],
    };
    vi.mocked(getProjectActivity).mockResolvedValue({
      object: "project_activity",
      data: activeActivity,
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    expect(await screen.findByText("Work Queue")).toBeTruthy();
    const inbox = screen.getByText("Work Queue").closest("div");
    expect(inbox).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Show blocked assignments" }));
    expect(await screen.findByText("No blocked assignments for this project.")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Show active assignments" }));

    await waitFor(() => {
      expect(
        within(screen.getByLabelText("Work queue")).getByRole("button", {
          name: "Open work item Build cockpit UI",
        }),
      ).toBeTruthy();
    });
  });

  it("surfaces missing defaults and empty context in needs attention", async () => {
    resetProjectWorkMocks();
    const projectWithoutDefaults: ProjectRecord = {
      ...project,
      default_provider: undefined,
      default_model: undefined,
      context_sources: [
        {
          id: "ctx_1",
          kind: "doc",
          title: "Notes",
          path: "notes.md",
          enabled: false,
          created_at: "2026-06-02T09:00:00Z",
          updated_at: "2026-06-02T09:00:00Z",
        },
      ],
    };
    vi.mocked(getProjectMemory).mockResolvedValue({ object: "project_memory", data: [] });
    window.localStorage.setItem("hecate.project", projectWithoutDefaults.id);
    const state = createRuntimeConsoleFixture({
      projects: [projectWithoutDefaults],
      activeProjectID: projectWithoutDefaults.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Work Queue");
    const health = await openProjectAttentionMenu();
    expect(within(health).getByText("Provider/model defaults missing")).toBeTruthy();
    expect(within(health).getByText("No project memory or context sources enabled")).toBeTruthy();

    const contextAttentionItem = within(health).getByRole("button", {
      name: "Open attention item No project memory or context sources enabled",
    });
    expect(contextAttentionItem).toHaveClass("project-attention-item");

    await userEvent.click(contextAttentionItem);
    expect(screen.getByRole("tab", { name: /Memory \/ Context/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await userEvent.click(screen.getByRole("button", { name: "Memory" }));
    expect(screen.getByRole("dialog", { name: "New project memory" })).toBeTruthy();
  });

  it("opens project skills from skill-related needs attention rows", async () => {
    resetProjectWorkMocks();
    vi.mocked(getProjectWorkRoles).mockResolvedValue({
      object: "project_roles",
      data: [{ ...role, skill_ids: ["backend"] }],
    });
    vi.mocked(getProjectSkills).mockResolvedValue({
      object: "project_skills",
      data: [{ ...projectSkill, enabled: false }],
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Work Queue");
    const health = await openProjectAttentionMenu();
    const skillsAttentionItem = within(health).getByRole("button", {
      name: "Open attention item Project skills need review",
    });

    await userEvent.click(skillsAttentionItem);

    expect(screen.getByRole("tab", { name: /Skills/ })).toHaveAttribute("aria-selected", "true");
    expect(await screen.findByText("Project Skills")).toBeTruthy();
    expect(screen.getByRole("checkbox", { name: "Enable skill Backend" })).toBeTruthy();
    expect(screen.getByDisplayValue("Backend")).toBeTruthy();
  });

  it("surfaces stale and missing linked execution in needs attention", async () => {
    resetProjectWorkMocks();
    const staleAssignment: ProjectAssignmentRecord = {
      ...hecateAssignment,
      id: "asgn_stale",
      status: "running",
      execution_ref: {
        kind: "task_run",
        task_id: "task_1",
        run_id: "run_missing",
        status: "running",
        missing: true,
      },
      execution: {
        ...hecateAssignment.execution,
        run_id: "run_missing",
        status: "running",
        pending_approval_count: 0,
        missing: true,
      },
      updated_at: "2026-06-01T08:00:00Z",
    };
    vi.mocked(getProjectActivity).mockResolvedValue({
      object: "project_activity",
      data: {
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
          blocked: [
            {
              id: staleAssignment.id,
              project_id: project.id,
              work_item: {
                id: workItem.id,
                title: workItem.title,
                status: "running",
                priority: workItem.priority,
              },
              assignment: staleAssignment,
              role,
              status: "running",
              blocking_signal: "stale_unknown",
              status_summary: "linked run missing",
              linked_task_id: "task_1",
              linked_run_id: "run_missing",
              artifact_summary: { count: 0 },
              updated_at: "2026-06-01T08:00:00Z",
            },
          ],
          completed: [],
          recent: [],
        },
        recent: [],
      },
    });
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [{ ...workItem, assignments: [staleAssignment] }],
    });
    vi.mocked(getProjectAssignments).mockResolvedValue({
      object: "project_assignments",
      data: [staleAssignment],
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Work Queue");
    const health = await openProjectAttentionMenu();
    expect(within(health).getByText(/Stale or unknown assignment: Build cockpit UI/i)).toBeTruthy();
    expect(within(health).getByText(/linked run missing/i)).toBeTruthy();

    await userEvent.click(within(health).getByRole("button", { name: "View blocked" }));
    await waitFor(() => {
      expect(screen.getByText("linked run missing")).toBeTruthy();
    });
  });

  it("renders an all-clear needs attention state when context and defaults are ready", async () => {
    resetProjectWorkMocks();
    const readyProject: ProjectRecord = {
      ...project,
      context_sources: [
        {
          id: "ctx_ready",
          kind: "doc",
          title: "Runbook",
          path: "docs/runbook.md",
          enabled: true,
          created_at: "2026-06-04T09:00:00Z",
          updated_at: "2026-06-04T09:00:00Z",
        },
      ],
    };
    const completedAssignment: ProjectAssignmentRecord = {
      ...hecateAssignment,
      status: "completed",
      execution_ref: {
        kind: "task_run",
        task_id: "task_1",
        run_id: "run_1",
        status: "completed",
      },
      execution: {
        ...hecateAssignment.execution,
        status: "completed",
        pending_approval_count: 0,
        finished_at: "2026-06-04T10:00:00Z",
      },
      updated_at: "2026-06-04T10:00:00Z",
      completed_at: "2026-06-04T10:00:00Z",
    };
    vi.mocked(getProjectActivity).mockResolvedValue({
      object: "project_activity",
      data: {
        project_id: readyProject.id,
        summary: {
          work_item_count: 1,
          assignment_count: 1,
          active_count: 0,
          blocked_count: 0,
          completed_count: 1,
          recent_count: 1,
        },
        buckets: {
          active: [],
          blocked: [],
          completed: [
            {
              id: completedAssignment.id,
              project_id: readyProject.id,
              work_item: {
                id: workItem.id,
                title: workItem.title,
                status: "done",
                priority: workItem.priority,
              },
              assignment: completedAssignment,
              role,
              status: "completed",
              blocking_signal: "completed",
              status_summary: "completed",
              linked_task_id: "task_1",
              linked_run_id: "run_1",
              artifact_summary: { count: 1 },
              updated_at: "2026-06-04T10:00:00Z",
            },
          ],
          recent: [],
        },
        recent: [],
      },
    });
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [{ ...workItem, status: "done", assignments: [completedAssignment] }],
    });
    vi.mocked(getProjectAssignments).mockResolvedValue({
      object: "project_assignments",
      data: [completedAssignment],
    });
    vi.mocked(getProjectMemory).mockResolvedValue({
      object: "project_memory",
      data: [memoryEntry],
    });
    window.localStorage.setItem("hecate.project", readyProject.id);
    const state = createRuntimeConsoleFixture({
      projects: [readyProject],
      activeProjectID: readyProject.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Work Queue");
    const health = await openProjectAttentionMenu();
    expect(within(health).getByText("No project attention items detected.")).toBeTruthy();
  });

  it("starts not-started assignments from the activity inbox", async () => {
    resetProjectWorkMocks();
    const notStartedAssignment: ProjectAssignmentRecord = {
      ...hecateAssignment,
      id: "asgn_not_started",
      status: "queued",
      execution_ref: undefined,
      execution: undefined,
      started_at: undefined,
    };
    vi.mocked(getProjectActivity).mockResolvedValue({
      object: "project_activity",
      data: {
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
          blocked: [
            {
              id: notStartedAssignment.id,
              project_id: project.id,
              work_item: {
                id: workItem.id,
                title: workItem.title,
                status: "ready",
                priority: workItem.priority,
              },
              assignment: notStartedAssignment,
              role,
              status: "queued",
              blocking_signal: "not_started",
              status_summary: "not started",
              artifact_summary: { count: 0 },
              updated_at: "2026-06-02T11:00:00Z",
            },
          ],
          completed: [],
          recent: [],
        },
        recent: [],
      },
    });
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [{ ...workItem, assignments: [notStartedAssignment] }],
    });
    vi.mocked(getProjectAssignments).mockResolvedValue({
      object: "project_assignments",
      data: [notStartedAssignment],
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await screen.findByText("Work Queue");
    await userEvent.click(screen.getByRole("button", { name: "Show blocked assignments" }));
    const activity = screen.getByLabelText("Work queue");
    expect(
      within(activity).getByRole("button", { name: "Open work item Build cockpit UI" }),
    ).toBeTruthy();
    await openProjectWorkspaceTab(/Timeline \/ Decision Log/);
    const timeline = screen.getByLabelText("Project timeline");
    expect(within(timeline).queryByText("not started")).toBeNull();
    await openProjectWorkspaceTab(/Work Coordination/);

    await userEvent.click(await screen.findByRole("button", { name: "Start" }));

    expect(startProjectAssignment).toHaveBeenCalledWith(
      project.id,
      workItem.id,
      notStartedAssignment.id,
      "hecate_task",
    );
  });

  it("opens chat from an assignment using the projected model", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    const onOpenChat = vi.fn();
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(
      withRuntimeConsole(<ProjectsView onOpenChat={onOpenChat} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    await userEvent.click(await screen.findByRole("button", { name: "Open chat" }));

    expect(onOpenChat).toHaveBeenCalledWith(
      expect.objectContaining({
        projectID: project.id,
        provider: "ollama",
        model: "qwen2.5-coder",
        title: "Build cockpit UI - Software developer",
      }),
    );
    const request = onOpenChat.mock.calls[0]?.[0];
    expectLaunchContextContract(request.draft);
    expect(request.draft).toContain("Launch context");
    expect(request.draft).toContain("Project: Hecate (proj_1)");
    expect(request.draft).toContain("- Title: Build cockpit UI");
    expect(request.draft).toContain("- Status: awaiting_approval");
    expect(request.draft).toContain("- Driver: hecate_task");
    expect(request.draft).toContain("- Name: Software developer");
    expect(request.draft).toContain("- Provider: ollama");
    expect(request.draft).toContain("- Model: qwen2.5-coder");
    expect(request.draft).toContain(
      "Role defaults: driver=hecate_task, provider=anthropic, model=claude-sonnet-4, profile=implementation",
    );
    expect(request.draft).toContain("Linked runtime ids:");
    expect(request.draft).toContain("task=task_1, run=run_1");
    expect(request.draft).toContain("Request:\n- ");
  });

  it("indents multiline launch-context values in assignment chat drafts", async () => {
    resetProjectWorkMocks();
    const multilineRole: ProjectWorkRoleRecord = {
      ...role,
      description: "Owns implementation work.\nCoordinates with review.",
      instructions: "Keep changes reviewable.\nCall out risks.",
    };
    const multilineWorkItem: ProjectWorkItemRecord = {
      ...workItem,
      brief: "Expose project work and native starts.\nKeep the first launch editable.",
    };
    vi.mocked(getProjectWorkRoles).mockResolvedValue({
      object: "project_roles",
      data: [multilineRole],
    });
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [{ ...multilineWorkItem, assignments: [hecateAssignment] }],
    });
    vi.mocked(getProjectWorkItem).mockResolvedValue({
      object: "project_work_item",
      data: multilineWorkItem,
    });
    window.localStorage.setItem("hecate.project", project.id);
    const onOpenChat = vi.fn();
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(
      withRuntimeConsole(<ProjectsView onOpenChat={onOpenChat} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    await userEvent.click(await screen.findByRole("button", { name: "Open chat" }));

    const request = onOpenChat.mock.calls[0]?.[0];
    expect(request.draft).toContain(
      "- Brief: Expose project work and native starts.\n  Keep the first launch editable.",
    );
    expect(request.draft).toContain(
      "- Description: Owns implementation work.\n  Coordinates with review.",
    );
    expect(request.draft).toContain("- Instructions: Keep changes reviewable.\n  Call out risks.");
  });

  it("opens chat from an assignment using role defaults when no run is linked", async () => {
    resetProjectWorkMocks();
    const unstartedAssignment: ProjectAssignmentRecord = {
      ...hecateAssignment,
      execution_ref: undefined,
      execution: undefined,
      status: "queued",
      started_at: undefined,
    };
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [{ ...workItem, assignments: [unstartedAssignment] }],
    });
    vi.mocked(getProjectAssignments).mockResolvedValue({
      object: "project_assignments",
      data: [unstartedAssignment],
    });
    window.localStorage.setItem("hecate.project", project.id);
    const onOpenChat = vi.fn();
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(
      withRuntimeConsole(<ProjectsView onOpenChat={onOpenChat} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    await userEvent.click(await screen.findByRole("button", { name: "Open chat" }));

    expect(onOpenChat).toHaveBeenCalledWith(
      expect.objectContaining({
        projectID: project.id,
        provider: "anthropic",
        model: "claude-sonnet-4",
        title: "Build cockpit UI - Software developer",
      }),
    );
    const request = onOpenChat.mock.calls[0]?.[0];
    expect(request.draft).toContain("- Status: queued");
    expect(request.draft).toContain("- Provider: anthropic");
    expect(request.draft).toContain("- Model: claude-sonnet-4");
    expect(request.draft).not.toContain("Linked runtime ids:");
  });

  it("creates work items from the Projects cockpit", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Work" }));
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "New cockpit work" },
    });
    fireEvent.change(screen.getByLabelText("Brief"), {
      target: { value: "Make project work creatable in the UI." },
    });
    fireEvent.change(screen.getByLabelText("Priority"), {
      target: { value: "urgent" },
    });
    await userEvent.click(screen.getByRole("button", { name: "Create work item" }));

    expect(createProjectWorkItem).toHaveBeenCalledWith(project.id, {
      title: "New cockpit work",
      brief: "Make project work creatable in the UI.",
      status: "ready",
      priority: "urgent",
      owner_role_id: "software_developer",
    });
  });

  it("edits and deletes work items from the selected detail", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    const detail = screen.getByLabelText("Selected work item");
    expect(await within(detail).findByText("Expose project work and native starts.")).toBeTruthy();

    await userEvent.click(within(detail).getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "Edited cockpit UI" },
    });
    fireEvent.change(screen.getByLabelText("Status"), {
      target: { value: "review" },
    });
    fireEvent.change(screen.getByLabelText("Priority"), {
      target: { value: "urgent" },
    });
    await userEvent.click(screen.getByRole("button", { name: "Save work item" }));

    expect(updateProjectWorkItem).toHaveBeenCalledWith(project.id, workItem.id, {
      title: "Edited cockpit UI",
      brief: "Expose project work and native starts.",
      status: "review",
      priority: "urgent",
      owner_role_id: "software_developer",
      reviewer_role_ids: ["reviewer_qa"],
    });

    await userEvent.click(within(detail).getByRole("button", { name: "Delete" }));
    expect(
      screen.getByText(/Linked tasks, runs, chats, workspace files, and git history/i),
    ).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Delete work item" }));

    expect(deleteProjectWorkItem).toHaveBeenCalledWith(project.id, workItem.id);
  });

  it("adds assignments from the selected work item", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Assignment" }));
    fireEvent.change(screen.getByLabelText("Driver"), {
      target: { value: "external_agent" },
    });
    await userEvent.click(screen.getByRole("button", { name: "Add assignment" }));

    expect(createProjectAssignment).toHaveBeenCalledWith(project.id, workItem.id, {
      role_id: "software_developer",
      driver_kind: "external_agent",
    });
  });

  it("creates target assignments from handoffs without starting them", async () => {
    resetProjectWorkMocks();
    const qaRole: ProjectWorkRoleRecord = {
      id: "reviewer_qa",
      project_id: project.id,
      name: "QA reviewer",
      default_driver_kind: "external_agent",
      default_provider: "anthropic",
      default_model: "claude-sonnet-4",
      default_agent_profile: "qa_external",
      built_in: false,
    };
    vi.mocked(getProjectWorkRoles).mockResolvedValue({
      object: "project_roles",
      data: [role, qaRole],
    });
    vi.mocked(getProjectHandoffs).mockResolvedValue({
      object: "project_handoffs",
      data: [
        {
          id: "handoff_1",
          project_id: project.id,
          work_item_id: workItem.id,
          source_assignment_id: "asgn_1",
          source_run_id: "run_1",
          source_chat_session_id: "chat_1",
          source_message_id: "msg_1",
          title: "QA handoff",
          summary: "Ready for review.",
          recommended_next_action: "Create a QA assignment.",
          target_role_id: "reviewer_qa",
          target_work_item_id: "work_followup",
          context_refs: ["ctx_1"],
          status: "pending",
          provenance_kind: "agent_draft",
          trust_label: "operator_reviewed",
          created_at: "2026-06-02T12:00:00Z",
          updated_at: "2026-06-02T12:00:00Z",
          status_changed_at: "2026-06-02T12:00:00Z",
        },
      ],
    });
    vi.mocked(createProjectAssignment).mockResolvedValueOnce({
      object: "project_assignment",
      data: {
        ...hecateAssignment,
        id: "asgn_new",
        work_item_id: "work_followup",
        role_id: "reviewer_qa",
        driver_kind: "external_agent",
        status: "queued",
      },
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    const detail = screen.getByLabelText("Selected work item");
    await waitFor(() => {
      expect(within(detail).getAllByText("QA handoff").length).toBeGreaterThan(0);
    });
    const sourceEvidence = within(detail).getByRole("group", { name: "Source evidence" });
    expect(within(sourceEvidence).getByText("assignment asgn_1")).toBeTruthy();
    expect(within(sourceEvidence).getByText("chat chat_1")).toBeTruthy();
    expect(within(sourceEvidence).getByText("context ctx_1")).toBeTruthy();
    await userEvent.click(
      within(detail).getByRole("button", { name: "Create follow-up assignment" }),
    );

    await waitFor(() => {
      expect(createProjectAssignment).toHaveBeenCalledWith(project.id, "work_followup", {
        role_id: "reviewer_qa",
        driver_kind: "external_agent",
      });
    });
    expect(updateProjectHandoff).toHaveBeenCalledWith(
      project.id,
      workItem.id,
      "handoff_1",
      expect.objectContaining({
        target_assignment_id: "asgn_new",
        target_role_id: "reviewer_qa",
        status: "accepted",
      }),
    );
    expect(startProjectAssignment).not.toHaveBeenCalled();
  });

  it("falls back to Hecate task follow-up assignments when the target role has no default driver", async () => {
    resetProjectWorkMocks();
    const reviewRole: ProjectWorkRoleRecord = {
      id: "role_review",
      project_id: project.id,
      name: "Review",
      built_in: false,
    };
    vi.mocked(getProjectWorkRoles).mockResolvedValue({
      object: "project_roles",
      data: [role, reviewRole],
    });
    vi.mocked(getProjectHandoffs).mockResolvedValue({
      object: "project_handoffs",
      data: [
        {
          id: "handoff_driver_fallback",
          project_id: project.id,
          work_item_id: workItem.id,
          title: "Review handoff",
          summary: "Ready for review.",
          recommended_next_action: "Create a review assignment.",
          target_role_id: "role_review",
          status: "pending",
          provenance_kind: "agent_draft",
          trust_label: "operator_reviewed",
          created_at: "2026-06-02T12:00:00Z",
          updated_at: "2026-06-02T12:00:00Z",
          status_changed_at: "2026-06-02T12:00:00Z",
        },
      ],
    });
    vi.mocked(createProjectAssignment).mockResolvedValueOnce({
      object: "project_assignment",
      data: {
        ...hecateAssignment,
        id: "asgn_review",
        role_id: "role_review",
        driver_kind: "hecate_task",
        status: "queued",
      },
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    const detail = screen.getByLabelText("Selected work item");
    await waitFor(() => {
      expect(within(detail).getAllByText("Review handoff").length).toBeGreaterThan(0);
    });
    await userEvent.click(
      within(detail).getByRole("button", { name: "Create follow-up assignment" }),
    );

    await waitFor(() => {
      expect(createProjectAssignment).toHaveBeenCalledWith(project.id, workItem.id, {
        role_id: "role_review",
        driver_kind: "hecate_task",
      });
    });
    expect(updateProjectHandoff).toHaveBeenCalledWith(
      project.id,
      workItem.id,
      "handoff_driver_fallback",
      expect.objectContaining({
        target_assignment_id: "asgn_review",
        target_role_id: "role_review",
        status: "accepted",
      }),
    );
    expect(startProjectAssignment).not.toHaveBeenCalled();
  });

  it("uses a role default driver when adding assignments", async () => {
    const externalRole: ProjectWorkRoleRecord = {
      id: "role_external",
      project_id: project.id,
      name: "External reviewer",
      default_driver_kind: "external_agent",
      built_in: false,
    };
    resetProjectWorkMocks();
    vi.mocked(getProjectWorkRoles).mockResolvedValue({
      object: "project_roles",
      data: [role, externalRole],
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Assignment" }));
    fireEvent.change(screen.getByLabelText("Role"), {
      target: { value: "role_external" },
    });
    await userEvent.click(screen.getByRole("button", { name: "Add assignment" }));

    expect(createProjectAssignment).toHaveBeenCalledWith(project.id, workItem.id, {
      role_id: "role_external",
      driver_kind: "external_agent",
    });
  });

  it("creates custom roles with execution defaults", async () => {
    resetProjectWorkMocks();
    vi.mocked(getProjectSkills).mockResolvedValue({
      object: "project_skills",
      data: [
        {
          ...projectSkill,
          id: "frontend",
          title: "Frontend",
          path: ".agents/skills/frontend/SKILL.md",
        },
        {
          ...projectSkill,
          id: "ui",
          title: "UI",
          path: ".agents/skills/ui/SKILL.md",
        },
      ],
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Roles" }));
    const dialog = screen.getByRole("dialog", { name: "Project roles" });
    await userEvent.click(within(dialog).getByRole("button", { name: /New custom role/i }));
    fireEvent.change(within(dialog).getByLabelText("Name"), {
      target: { value: "Frontend implementer" },
    });
    fireEvent.change(within(dialog).getByLabelText("Description"), {
      target: { value: "Builds UI" },
    });
    fireEvent.change(within(dialog).getByLabelText("Instructions"), {
      target: { value: "Use existing UI primitives." },
    });
    fireEvent.change(within(dialog).getByLabelText("Default driver"), {
      target: { value: "hecate_task" },
    });
    fireEvent.change(within(dialog).getByLabelText("Default profile"), {
      target: { value: "implementation" },
    });
    fireEvent.change(within(dialog).getByLabelText("Default provider"), {
      target: { value: "ollama" },
    });
    fireEvent.change(within(dialog).getByLabelText("Default model"), {
      target: { value: "ministral-3:latest" },
    });
    await userEvent.click(await within(dialog).findByLabelText("Use skill Frontend"));
    await userEvent.click(within(dialog).getByLabelText("Use skill UI"));
    await userEvent.click(within(dialog).getByRole("button", { name: "Create role" }));

    expect(createProjectWorkRole).toHaveBeenCalledWith(project.id, {
      name: "Frontend implementer",
      description: "Builds UI",
      instructions: "Use existing UI primitives.",
      default_driver_kind: "hecate_task",
      default_provider: "ollama",
      default_model: "ministral-3:latest",
      default_agent_profile: "implementation",
      skill_ids: ["frontend", "ui"],
    });
    await waitFor(() => {
      expect(within(dialog).getByRole("button", { name: "Save role" })).toBeTruthy();
    });
    expect(within(dialog).getByRole("button", { name: "Delete role" })).toBeTruthy();
  });

  it("creates agent profiles with project skill selections", async () => {
    resetProjectWorkMocks();
    vi.mocked(getProjectSkills).mockResolvedValue({
      object: "project_skills",
      data: [projectSkill],
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Agent profiles" }));
    const dialog = screen.getByRole("dialog", { name: "Agent profiles" });
    await userEvent.click(within(dialog).getByRole("button", { name: "New profile" }));
    fireEvent.change(within(dialog).getByLabelText("Profile id"), {
      target: { value: "reviewer" },
    });
    fireEvent.change(within(dialog).getByLabelText("Name"), {
      target: { value: "Reviewer" },
    });
    fireEvent.change(within(dialog).getByLabelText("Description"), {
      target: { value: "Reviews implementation assignments." },
    });
    fireEvent.change(within(dialog).getByLabelText("Instructions"), {
      target: { value: "Review the diff and surface risks." },
    });
    fireEvent.change(within(dialog).getByLabelText("Surface"), {
      target: { value: "hecate_task" },
    });
    fireEvent.change(within(dialog).getByLabelText("Execution profile"), {
      target: { value: "review" },
    });
    fireEvent.change(within(dialog).getByLabelText("Provider hint"), {
      target: { value: "ollama" },
    });
    fireEvent.change(within(dialog).getByLabelText("Model hint"), {
      target: { value: "qwen2.5-coder" },
    });
    await userEvent.click(within(dialog).getByLabelText("Writes allowed"));
    fireEvent.change(within(dialog).getByLabelText("Approval policy"), {
      target: { value: "require" },
    });
    fireEvent.change(within(dialog).getByLabelText("Memory policy"), {
      target: { value: "include" },
    });
    fireEvent.change(within(dialog).getByLabelText("Context source policy"), {
      target: { value: "visible_only" },
    });
    await userEvent.click(await within(dialog).findByLabelText("Use skill Backend"));
    await userEvent.click(within(dialog).getByRole("button", { name: "Create profile" }));

    expect(createAgentProfile).toHaveBeenCalledWith({
      id: "reviewer",
      name: "Reviewer",
      description: "Reviews implementation assignments.",
      instructions: "Review the diff and surface risks.",
      surface: "hecate_task",
      provider_hint: "ollama",
      model_hint: "qwen2.5-coder",
      execution_profile: "review",
      tools_enabled: true,
      writes_allowed: true,
      network_allowed: false,
      approval_policy: "require",
      project_memory_policy: "include",
      context_source_policy: "visible_only",
      skill_ids: ["backend"],
      external_agent_kind: "",
    });
  });

  it("updates and deletes agent profiles", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Agent profiles" }));
    const dialog = screen.getByRole("dialog", { name: "Agent profiles" });
    fireEvent.change(within(dialog).getByLabelText("Name"), {
      target: { value: "Implementation reviewer" },
    });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save profile" }));

    expect(updateAgentProfile).toHaveBeenCalledWith(
      "implementation",
      expect.objectContaining({
        name: "Implementation reviewer",
        surface: "hecate_task",
        project_memory_policy: "visible_only",
        context_source_policy: "include_enabled",
      }),
    );

    await userEvent.click(await within(dialog).findByRole("button", { name: "Delete profile" }));
    expect(deleteAgentProfile).not.toHaveBeenCalled();
    expect(screen.getByText(/Other projects may also reference this global profile/i)).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Delete agent profile" }));
    expect(deleteAgentProfile).toHaveBeenCalledWith("implementation");
  });

  it("edits and deletes assignments from the selected work item", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Edit assignment asgn_1" }));
    fireEvent.change(screen.getByLabelText("Status"), {
      target: { value: "running" },
    });
    fireEvent.change(screen.getByLabelText("Driver"), {
      target: { value: "external_agent" },
    });
    await userEvent.click(screen.getByRole("button", { name: "Save assignment" }));

    expect(updateProjectAssignment).toHaveBeenCalledWith(
      project.id,
      workItem.id,
      hecateAssignment.id,
      {
        role_id: "software_developer",
        driver_kind: "external_agent",
        status: "running",
        execution_ref: {
          kind: "task_run",
          task_id: "task_1",
          run_id: "run_1",
          context_snapshot_id: "ctx_assignment_1",
        },
      },
    );

    await userEvent.click(screen.getByRole("button", { name: "Delete assignment asgn_1" }));
    expect(
      screen.getByText(/Linked tasks, runs, chats, and external-agent executions/i),
    ).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Delete assignment" }));

    expect(deleteProjectAssignment).toHaveBeenCalledWith(
      project.id,
      workItem.id,
      hecateAssignment.id,
    );
  });

  it("updates project defaults needed by native starts", async () => {
    resetProjectWorkMocks();
    const projectWithUpdatedDefaults = {
      ...project,
      default_model: "ministral-3:latest",
    };
    window.localStorage.setItem("hecate.project", projectWithUpdatedDefaults.id);
    const state = createRuntimeConsoleFixture({
      projects: [projectWithUpdatedDefaults],
      activeProjectID: projectWithUpdatedDefaults.id,
      settingsConfig: {
        backend: "memory",
        providers: [
          {
            id: "ollama",
            name: "Ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            credential_configured: false,
          },
        ],
        policy_rules: [],
        events: [],
      },
      providers: [],
      providerPresets: [
        {
          id: "ollama",
          name: "Ollama",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:11434/v1",
        },
      ],
      models: [
        {
          id: "qwen2.5-coder",
          owned_by: "ollama",
          metadata: { provider: "ollama", provider_kind: "local" },
        },
        {
          id: "ministral-3:latest",
          owned_by: "ollama",
          metadata: { provider: "ollama", provider_kind: "local" },
        },
      ],
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Project settings" }));
    expect(screen.getByRole("button", { name: /Ollama/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: /Model picker: ministral-3:latest/i })).toBeTruthy();
    expect(screen.queryByLabelText("Provider ID")).toBeNull();
    expect(screen.queryByLabelText("Model")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: /Ollama/ }));
    const providerMenu = document.querySelector(".dropdown-menu") as HTMLElement;
    expect(within(providerMenu).getByRole("option", { name: /Ollama/i })).toBeTruthy();
    await userEvent.click(document.body);
    await userEvent.click(screen.getByRole("button", { name: /Model picker/i }));
    await userEvent.click(await screen.findByText("qwen2.5-coder"));
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Workspace mode" }), [
      "ephemeral",
    ]);
    expect(screen.getByRole("complementary", { name: "Project settings panel" })).toHaveStyle({
      width: "380px",
    });
    await userEvent.click(screen.getByRole("button", { name: "Save defaults" }));

    expect(updateProject).toHaveBeenCalledWith(projectWithUpdatedDefaults.id, {
      default_provider: "ollama",
      default_model: "qwen2.5-coder",
      default_agent_profile: "",
      default_workspace_mode: "ephemeral",
    });
  });

  it("preserves inherited project model defaults when saving settings", async () => {
    resetProjectWorkMocks();
    const projectWithInheritedModel = {
      ...project,
      default_provider: "ollama",
      default_model: undefined,
    };
    window.localStorage.setItem("hecate.project", projectWithInheritedModel.id);
    const state = createRuntimeConsoleFixture({
      projects: [projectWithInheritedModel],
      activeProjectID: projectWithInheritedModel.id,
      settingsConfig: {
        backend: "memory",
        providers: [
          {
            id: "ollama",
            name: "Ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            credential_configured: false,
          },
        ],
        policy_rules: [],
        events: [],
      },
      providerPresets: [
        {
          id: "ollama",
          name: "Ollama",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:11434/v1",
        },
      ],
      models: [
        {
          id: "qwen2.5-coder",
          owned_by: "ollama",
          metadata: { provider: "ollama", provider_kind: "local", default: true },
        },
      ],
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Project settings" }));
    expect(
      screen.getByRole("button", { name: /Model picker: inherit runtime default/i }),
    ).toBeTruthy();
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Workspace mode" }), [
      "ephemeral",
    ]);
    await userEvent.click(screen.getByRole("button", { name: "Save defaults" }));

    expect(updateProject).toHaveBeenCalledWith(projectWithInheritedModel.id, {
      default_provider: "ollama",
      default_model: "",
      default_agent_profile: "",
      default_workspace_mode: "ephemeral",
    });
  });

  it("starts native Hecate assignments and refreshes detail state", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    const queuedAssignment = {
      ...hecateAssignment,
      status: "running",
      execution_ref: {
        kind: "task_run",
        task_id: "task_1",
        run_id: "run_1",
        status: "queued",
      },
      execution: { ...hecateAssignment.execution, status: "queued" },
    };
    vi.mocked(getProjectAssignments).mockResolvedValue({
      object: "project_assignments",
      data: [queuedAssignment],
    });
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Start" }));

    expect(startProjectAssignment).toHaveBeenCalledWith(
      project.id,
      workItem.id,
      queuedAssignment.id,
      "hecate_task",
    );
    await waitFor(() => {
      expect(getProjectWorkItem).toHaveBeenCalledTimes(2);
    });
  });

  it("renders finished-only assignment timestamps without a blank started label", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    vi.mocked(getProjectAssignments).mockResolvedValue({
      object: "project_assignments",
      data: [
        {
          ...hecateAssignment,
          status: "completed",
          started_at: undefined,
          completed_at: "2026-06-02T12:00:00Z",
          execution: {
            ...hecateAssignment.execution,
            status: "completed",
            started_at: undefined,
            finished_at: "2026-06-02T12:00:00Z",
          },
        },
      ],
    });
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    expect(await screen.findByText(/^Finished /)).toBeTruthy();
    expect(screen.queryByText(/^Started\s*$/)).toBeNull();
  });

  it("exposes chat preparation for queued external-agent assignments", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    const externalAssignment: ProjectAssignmentRecord = {
      ...hecateAssignment,
      id: "asgn_external",
      driver_kind: "external_agent",
      status: "queued",
      execution_ref: undefined,
      execution: undefined,
    };
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [{ ...workItem, assignments: [externalAssignment] }],
    });
    vi.mocked(getProjectWorkItem).mockResolvedValue({
      object: "project_work_item",
      data: { ...workItem, assignments: [externalAssignment] },
    });
    vi.mocked(getProjectAssignments).mockResolvedValue({
      object: "project_assignments",
      data: [externalAssignment],
    });
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(
      await screen.findByRole("button", { name: "Open work item Build cockpit UI" }),
    );
    const detail = await screen.findByRole("region", { name: "Selected work item" });
    expect(within(detail).getByRole("button", { name: "Prepare chat" })).toBeTruthy();
    expect(screen.queryByText("Chat not prepared")).toBeNull();
  });
});
