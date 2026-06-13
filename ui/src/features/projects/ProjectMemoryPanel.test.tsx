import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type {
  ProjectMemoryCandidateRecord,
  ProjectMemoryRecord,
  ProjectRecord,
} from "../../types/project";
import { ProjectMemoryModal, ProjectMemoryPanel } from "./ProjectMemoryPanel";

function project(overrides: Partial<ProjectRecord> = {}): ProjectRecord {
  return {
    id: "proj_1",
    name: "Hecate",
    roots: [],
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
        created_at: "2026-06-12T00:00:00Z",
        updated_at: "2026-06-12T00:00:00Z",
      },
    ],
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function memoryEntry(overrides: Partial<ProjectMemoryRecord> = {}): ProjectMemoryRecord {
  return {
    id: "mem_1",
    scope: "project",
    project_id: "proj_1",
    title: "Commit style",
    body: "Use conventional commits.",
    trust_label: "operator_memory",
    source_kind: "operator",
    enabled: true,
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function memoryCandidate(
  overrides: Partial<ProjectMemoryCandidateRecord> = {},
): ProjectMemoryCandidateRecord {
  return {
    id: "memcand_1",
    project_id: "proj_1",
    title: "Generated summary",
    body: "Keep generated content lower trust until reviewed.",
    suggested_kind: "note",
    suggested_trust_label: "generated_summary",
    suggested_source_kind: "task_output",
    suggested_source_id: "run_1",
    source_refs: [{ kind: "task_run", id: "run_1", title: "Implementation run" }],
    status: "pending",
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

describe("ProjectMemoryPanel", () => {
  it("renders memory, context sources, candidates, and action controls", async () => {
    const handlers = {
      onDiscoverContextSources: vi.fn(),
      onPromoteCandidate: vi.fn(),
      onRejectCandidate: vi.fn(),
      onDelete: vi.fn(),
      onEdit: vi.fn(),
      onNew: vi.fn(),
      onRefresh: vi.fn(),
    };
    const entry = memoryEntry();
    const candidate = memoryCandidate();

    render(
      <ProjectMemoryPanel
        candidates={[candidate]}
        discoveringContext={false}
        entries={[entry]}
        error=""
        loading={false}
        project={project()}
        rejectingCandidateID=""
        {...handlers}
      />,
    );

    expect(screen.getByText("1 enabled / 1 saved entries · 1 pending candidates")).toBeTruthy();
    expect(screen.getAllByText("AGENTS.md").length).toBeGreaterThan(0);
    expect(screen.getByText("Commit style")).toBeTruthy();
    expect(screen.getByText("Generated summary")).toBeTruthy();
    expect(screen.getByText("Source refs: task_run Implementation run")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Refresh project memory" }));
    await userEvent.click(screen.getByRole("button", { name: "Discover" }));
    await userEvent.click(screen.getByRole("button", { name: "Memory" }));
    await userEvent.click(screen.getByRole("button", { name: "Edit memory Commit style" }));
    await userEvent.click(screen.getByRole("button", { name: "Delete memory Commit style" }));
    await userEvent.click(
      screen.getByRole("button", { name: "Promote memory candidate Generated summary" }),
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Reject memory candidate Generated summary" }),
    );

    expect(handlers.onRefresh).toHaveBeenCalledTimes(1);
    expect(handlers.onDiscoverContextSources).toHaveBeenCalledTimes(1);
    expect(handlers.onNew).toHaveBeenCalledTimes(1);
    expect(handlers.onEdit).toHaveBeenCalledWith(entry);
    expect(handlers.onDelete).toHaveBeenCalledWith(entry);
    expect(handlers.onPromoteCandidate).toHaveBeenCalledWith(candidate);
    expect(handlers.onRejectCandidate).toHaveBeenCalledWith(candidate);
  });

  it("saves new memory entries with default form metadata", async () => {
    const onSave = vi.fn();

    render(
      <ProjectMemoryModal
        entry={null}
        error=""
        pending={false}
        onClose={vi.fn()}
        onSave={onSave}
      />,
    );

    await userEvent.type(screen.getByLabelText("Title"), "Review posture");
    await userEvent.type(screen.getByLabelText("Body"), "Keep generated summaries labelled.");
    await userEvent.click(screen.getByRole("button", { name: "Create memory" }));

    expect(onSave).toHaveBeenCalledWith({
      title: "Review posture",
      body: "Keep generated summaries labelled.",
      trustLabel: "operator_memory",
      sourceKind: "operator",
      sourceID: "",
      enabled: true,
    });
  });

  it("promotes candidates with provenance defaults", async () => {
    const onSave = vi.fn();

    render(
      <ProjectMemoryModal
        candidate={memoryCandidate()}
        entry={null}
        error=""
        pending={false}
        onClose={vi.fn()}
        onSave={onSave}
      />,
    );

    expect(screen.getByText("Candidate provenance")).toBeTruthy();
    expect(screen.getByText("Source refs: task_run Implementation run")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Trust label"), {
      target: { value: "operator_memory" },
    });
    await userEvent.click(screen.getByRole("button", { name: "Promote memory" }));

    expect(onSave).toHaveBeenCalledWith({
      title: "Generated summary",
      body: "Keep generated content lower trust until reviewed.",
      trustLabel: "operator_memory",
      sourceKind: "task_output",
      sourceID: "run_1",
      enabled: true,
    });
  });
});
