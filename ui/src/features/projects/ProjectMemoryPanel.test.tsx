import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type {
  ProjectContextSourceRecord,
  ProjectMemoryCandidateRecord,
  ProjectMemoryRecord,
  ProjectRecord,
} from "../../types/project";
import { ProjectMemoryModal, ProjectMemoryPanel, ProjectSourceModal } from "./ProjectMemoryPanel";

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

function contextSource(
  overrides: Partial<ProjectContextSourceRecord> = {},
): ProjectContextSourceRecord {
  return {
    id: "ctx_source",
    kind: "url",
    title: "Design brief",
    path: "https://example.invalid/design",
    enabled: true,
    format: "url",
    trust_label: "operator_source",
    source_category: "operator_source",
    metadata: { note: "Canonical source for the design brief." },
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
      onDeleteSource: vi.fn(),
      onEditSource: vi.fn(),
      onPromoteCandidate: vi.fn(),
      onRejectCandidate: vi.fn(),
      onDelete: vi.fn(),
      onEdit: vi.fn(),
      onNew: vi.fn(),
      onNewSource: vi.fn(),
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
        project={project({
          roots: [
            {
              id: "root_1",
              path: "/workspace",
              kind: "local",
              active: true,
              created_at: "2026-06-12T00:00:00Z",
              updated_at: "2026-06-12T00:00:00Z",
            },
          ],
        })}
        rejectingCandidateID=""
        {...handlers}
      />,
    );

    expect(screen.getByRole("heading", { level: 1, name: "Memory" })).toBeTruthy();
    expect(screen.getByText("1 saved · 1 enabled · 1 to review · 1 source enabled")).toBeTruthy();
    expect(screen.getAllByText("AGENTS.md").length).toBeGreaterThan(0);
    expect(screen.getByText("Commit style")).toBeTruthy();
    const suggestion = screen.getByRole("article", {
      name: "Memory suggestion Generated summary",
    });
    expect(within(suggestion).getAllByText("Generated summary").length).toBeGreaterThan(0);
    expect(within(suggestion).getByText("Needs review")).toBeTruthy();
    expect(within(suggestion).getByText("Suggested memory")).toBeTruthy();
    expect(within(suggestion).getByText("Type")).toBeTruthy();
    expect(within(suggestion).getByText("Why Hecate suggested it")).toBeTruthy();
    expect(within(suggestion).getByText("Evidence")).toBeTruthy();
    expect(within(suggestion).getByText("Found in task_run Implementation run")).toBeTruthy();
    expect(within(suggestion).getByText("Review to save")).toBeTruthy();

    await userEvent.click(
      within(suggestion).getByText("Evidence and payload details", { selector: "summary" }),
    );
    expect(screen.getByText("Source refs: task_run Implementation run")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Refresh project memory" }));
    await userEvent.click(screen.getByText("Sources", { selector: "span" }));
    await userEvent.click(screen.getByRole("button", { name: "Find from folders" }));
    await userEvent.click(screen.getByRole("button", { name: "Add source" }));
    await userEvent.click(screen.getByRole("button", { name: "Add memory" }));
    const detailActions = screen.getAllByText("Details and actions", { selector: "summary" });
    await userEvent.click(detailActions[0]!);
    await userEvent.click(detailActions[1]!);
    await userEvent.click(screen.getByRole("button", { name: "Edit source AGENTS.md" }));
    await userEvent.click(screen.getByRole("button", { name: "Delete source AGENTS.md" }));
    await userEvent.click(screen.getByRole("button", { name: "Edit memory Commit style" }));
    await userEvent.click(screen.getByRole("button", { name: "Delete memory Commit style" }));
    await userEvent.click(
      screen.getByRole("button", {
        name: "Review memory suggestion Generated summary before saving",
      }),
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Dismiss memory suggestion Generated summary" }),
    );

    expect(handlers.onRefresh).toHaveBeenCalledTimes(1);
    expect(handlers.onDiscoverContextSources).toHaveBeenCalledTimes(1);
    expect(handlers.onNewSource).toHaveBeenCalledTimes(1);
    expect(handlers.onNew).toHaveBeenCalledTimes(1);
    expect(handlers.onEditSource).toHaveBeenCalledWith(project().context_sources?.[0]);
    expect(handlers.onDeleteSource).toHaveBeenCalledWith(project().context_sources?.[0]);
    expect(handlers.onEdit).toHaveBeenCalledWith(entry);
    expect(handlers.onDelete).toHaveBeenCalledWith(entry);
    expect(handlers.onPromoteCandidate).toHaveBeenCalledWith(candidate);
    expect(handlers.onRejectCandidate).toHaveBeenCalledWith(candidate);
  });

  it("renders saved and suggested memory bodies as Markdown", () => {
    render(
      <ProjectMemoryPanel
        candidates={[
          memoryCandidate({
            body: "# Suggested guidance\n\n1. Verify with `bun run test`.",
          }),
        ]}
        discoveringContext={false}
        entries={[
          memoryEntry({
            body: "# Saved guidance\n\n- Keep **review decisions** explicit.",
          }),
        ]}
        error=""
        loading={false}
        onDiscoverContextSources={vi.fn()}
        onDeleteSource={vi.fn()}
        onEditSource={vi.fn()}
        onPromoteCandidate={vi.fn()}
        onRejectCandidate={vi.fn()}
        onDelete={vi.fn()}
        onEdit={vi.fn()}
        onNew={vi.fn()}
        onNewSource={vi.fn()}
        onRefresh={vi.fn()}
        project={project()}
        rejectingCandidateID=""
      />,
    );

    const saved = screen.getByRole("article", { name: "Memory Commit style" });
    expect(within(saved).getByRole("heading", { level: 3, name: "Saved guidance" })).toBeTruthy();
    expect(within(saved).getByText("review decisions").tagName).toBe("STRONG");

    const suggestion = screen.getByRole("article", {
      name: "Memory suggestion Generated summary",
    });
    expect(
      within(suggestion).getByRole("heading", { level: 3, name: "Suggested guidance" }),
    ).toBeTruthy();
    expect(within(suggestion).getByText("bun run test").tagName).toBe("CODE");
  });

  it("renders source locators defensively", async () => {
    const handlers = {
      onDiscoverContextSources: vi.fn(),
      onDeleteSource: vi.fn(),
      onEditSource: vi.fn(),
      onPromoteCandidate: vi.fn(),
      onRejectCandidate: vi.fn(),
      onDelete: vi.fn(),
      onEdit: vi.fn(),
      onNew: vi.fn(),
      onNewSource: vi.fn(),
      onRefresh: vi.fn(),
    };

    render(
      <ProjectMemoryPanel
        candidates={[]}
        discoveringContext={false}
        entries={[]}
        error=""
        loading={false}
        project={project({
          context_sources: [
            contextSource(),
            contextSource({
              id: "ctx_unsafe",
              title: "Unsafe locator",
              path: "javascript:alert(1)",
              metadata: {},
            }),
          ],
        })}
        rejectingCandidateID=""
        {...handlers}
      />,
    );

    await userEvent.click(screen.getByText("Sources", { selector: "span" }));
    expect(screen.getByRole("link", { name: "https://example.invalid/design" })).toBeTruthy();
    expect(screen.getByText("javascript:alert(1)")).toBeTruthy();
    expect(screen.queryByRole("link", { name: "javascript:alert(1)" })).toBeNull();
    expect(screen.getByText("Canonical source for the design brief.")).toBeTruthy();
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

  it("saves project source forms", async () => {
    const onSave = vi.fn();

    render(
      <ProjectSourceModal
        source={null}
        error=""
        pending={false}
        onClose={vi.fn()}
        onSave={onSave}
      />,
    );

    await userEvent.selectOptions(screen.getByLabelText("Kind"), "note");
    await userEvent.type(screen.getByLabelText("Title"), "Research goals");
    await userEvent.type(screen.getByLabelText("Note"), "Keep source notes as metadata.");
    await userEvent.click(screen.getByRole("button", { name: "Create source" }));

    expect(onSave).toHaveBeenCalledWith({
      kind: "note",
      title: "Research goals",
      locator: "",
      enabled: true,
      format: "text",
      scope: "",
      trustLabel: "operator_source",
      sourceCategory: "operator_source",
      note: "Keep source notes as metadata.",
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

    const dialog = screen.getByRole("dialog", { name: "Review memory suggestion" });
    expect(within(dialog).getByText("Suggested memory")).toBeTruthy();
    expect(within(dialog).getByText("Needs review")).toBeTruthy();
    expect(
      within(dialog).getByText(
        "Hecate found this in task_run Implementation run. Save it only if it should become durable project guidance; edit the title or body first if the wording is too broad.",
      ),
    ).toBeTruthy();
    expect(within(dialog).getByText("Pending promotion")).toBeTruthy();
    expect(within(dialog).getByText("Evidence")).toBeTruthy();
    expect(within(dialog).getByText("Why Hecate suggested it")).toBeTruthy();
    await userEvent.click(
      within(dialog).getByText("Evidence and payload details", { selector: "summary" }),
    );
    expect(within(dialog).getByText("Source refs: task_run Implementation run")).toBeTruthy();
    await userEvent.click(screen.getByText("Advanced memory details", { selector: "summary" }));
    fireEvent.change(screen.getByLabelText("Trust label"), {
      target: { value: "operator_memory" },
    });
    await userEvent.click(screen.getByRole("button", { name: "Save to project memory" }));

    expect(onSave).toHaveBeenCalledWith({
      title: "Generated summary",
      body: "Keep generated content lower trust until reviewed.",
      trustLabel: "operator_memory",
      sourceKind: "task_output",
      sourceID: "run_1",
      enabled: true,
    });
  });

  it("locks memory and source drafts while saving", async () => {
    const memoryClose = vi.fn();
    const memorySave = vi.fn();
    const memoryRender = render(
      <ProjectMemoryModal
        entry={memoryEntry()}
        error=""
        pending
        onClose={memoryClose}
        onSave={memorySave}
      />,
    );

    const memoryTitle = screen.getByLabelText("Title");
    expect(memoryTitle).toBeDisabled();
    expect(screen.getByLabelText("Body")).toBeDisabled();
    expect(screen.getByLabelText("Enabled for this project")).toBeDisabled();
    expect(screen.getByLabelText("Trust label")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Saving…" })).toBeDisabled();
    fireEvent.submit(memoryTitle.closest("form")!);
    await userEvent.keyboard("{Escape}");
    expect(memorySave).not.toHaveBeenCalled();
    expect(memoryClose).not.toHaveBeenCalled();

    memoryRender.unmount();
    const sourceClose = vi.fn();
    const sourceSave = vi.fn();
    render(
      <ProjectSourceModal
        source={contextSource()}
        error=""
        pending
        onClose={sourceClose}
        onSave={sourceSave}
      />,
    );

    const sourceTitle = screen.getByLabelText("Title");
    expect(screen.getByLabelText("Kind")).toBeDisabled();
    expect(sourceTitle).toBeDisabled();
    expect(screen.getByLabelText("Locator")).toBeDisabled();
    expect(screen.getByLabelText("Trust label")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Saving…" })).toBeDisabled();
    fireEvent.submit(sourceTitle.closest("form")!);
    await userEvent.keyboard("{Escape}");
    expect(sourceSave).not.toHaveBeenCalled();
    expect(sourceClose).not.toHaveBeenCalled();
  });

  it("locks both review actions while the suggestion is being dismissed", () => {
    const candidate = memoryCandidate();
    render(
      <ProjectMemoryPanel
        candidates={[candidate]}
        discoveringContext={false}
        entries={[]}
        error=""
        loading={false}
        onDiscoverContextSources={vi.fn()}
        onDeleteSource={vi.fn()}
        onEditSource={vi.fn()}
        onPromoteCandidate={vi.fn()}
        onRejectCandidate={vi.fn()}
        onDelete={vi.fn()}
        onEdit={vi.fn()}
        onNew={vi.fn()}
        onNewSource={vi.fn()}
        onRefresh={vi.fn()}
        project={project()}
        rejectingCandidateID={candidate.id}
      />,
    );

    expect(screen.getByRole("button", { name: "Review first suggestion" })).toBeDisabled();
    expect(
      screen.getByRole("button", {
        name: "Review memory suggestion Generated summary before saving",
      }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Dismiss memory suggestion Generated summary" }),
    ).toBeDisabled();
  });

  it("separates resolved suggestion history from pending review", async () => {
    render(
      <ProjectMemoryPanel
        candidates={[memoryCandidate({ status: "promoted" })]}
        discoveringContext={false}
        entries={[memoryEntry()]}
        error=""
        loading={false}
        onDiscoverContextSources={vi.fn()}
        onDeleteSource={vi.fn()}
        onEditSource={vi.fn()}
        onPromoteCandidate={vi.fn()}
        onRejectCandidate={vi.fn()}
        onDelete={vi.fn()}
        onEdit={vi.fn()}
        onNew={vi.fn()}
        onNewSource={vi.fn()}
        onRefresh={vi.fn()}
        project={project()}
        rejectingCandidateID=""
      />,
    );

    expect(screen.queryByRole("heading", { name: "Suggestions to review" })).toBeNull();
    expect(screen.getByText("1 saved · 1 enabled · 0 to review · 1 source enabled")).toBeTruthy();
    await userEvent.click(screen.getByText("Reviewed suggestions", { selector: "span" }));
    expect(
      within(
        screen.getByRole("article", { name: "Memory suggestion Generated summary" }),
      ).getAllByText("Generated summary").length,
    ).toBeGreaterThan(0);
    expect(screen.queryByRole("button", { name: /Review memory suggestion/ })).toBeNull();
  });
});
