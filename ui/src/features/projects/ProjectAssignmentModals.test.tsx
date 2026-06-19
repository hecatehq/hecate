import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type {
  ProjectAssignmentRecord,
  ProjectRecord,
  ProjectRootRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { EditAssignmentModal, NewAssignmentModal } from "./ProjectAssignmentModals";

function root(overrides: Partial<ProjectRootRecord>): ProjectRootRecord {
  return {
    id: "root_main",
    path: "/workspace/main",
    kind: "workspace",
    active: true,
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function project(): ProjectRecord {
  return {
    id: "proj_1",
    name: "Hecate",
    roots: [root({ id: "root_main", path: "/workspace/main" })],
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
  };
}

function role(overrides: Partial<ProjectWorkRoleRecord> = {}): ProjectWorkRoleRecord {
  return {
    id: "software_developer",
    project_id: "proj_1",
    name: "Software developer",
    default_driver_kind: "hecate_task",
    built_in: false,
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function workItem(overrides: Partial<ProjectWorkItemRecord> = {}): ProjectWorkItemRecord {
  return {
    id: "work_1",
    project_id: "proj_1",
    title: "Build cockpit",
    status: "ready",
    priority: "normal",
    root_id: "root_main",
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function assignment(overrides: Partial<ProjectAssignmentRecord> = {}): ProjectAssignmentRecord {
  return {
    id: "assign_1",
    project_id: "proj_1",
    work_item_id: "work_1",
    role_id: "software_developer",
    root_id: "root_main",
    driver_kind: "hecate_task",
    status: "queued",
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

describe("ProjectAssignmentModals", () => {
  it("creates assignments and follows selected role driver defaults", async () => {
    const onCreate = vi.fn();

    render(
      <NewAssignmentModal
        error=""
        pending={false}
        project={project()}
        workItem={workItem()}
        roles={[
          role(),
          role({
            id: "researcher",
            name: "Researcher",
            default_driver_kind: "external_agent",
          }),
        ]}
        onClose={vi.fn()}
        onCreate={onCreate}
      />,
    );

    const plan = screen.getByRole("region", { name: "Queued assignment plan" });
    expect(within(plan).getByText("Review before start")).toBeTruthy();
    expect(within(plan).getByText("Software developer")).toBeTruthy();
    expect(within(plan).getByText("Uses the work item root")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Role"), { target: { value: "researcher" } });
    expect(screen.getByLabelText("Driver")).toHaveValue("external_agent");
    expect(within(plan).getByText("Researcher")).toBeTruthy();
    expect(within(plan).getByText("external_agent")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Create queued assignment" }));

    expect(onCreate).toHaveBeenCalledWith({
      roleID: "researcher",
      driverKind: "external_agent",
      rootID: "",
    });
  });

  it("keeps assignment plan fields unresolved until a role is available", () => {
    render(
      <NewAssignmentModal
        error=""
        pending={false}
        project={project()}
        workItem={workItem()}
        roles={[]}
        onClose={vi.fn()}
        onCreate={vi.fn()}
      />,
    );

    const plan = screen.getByRole("region", { name: "Queued assignment plan" });
    expect(screen.getByRole("dialog", { name: "Create queued assignment" })).toBeTruthy();
    expect(within(plan).getAllByText("Select a role")).toHaveLength(2);
    expect(screen.getByRole("button", { name: "Create queued assignment" })).toBeDisabled();
  });

  it("edits canonical assignment execution reference fields", async () => {
    const onSave = vi.fn();

    render(
      <EditAssignmentModal
        assignment={assignment({
          execution_ref: {
            kind: "task_run",
            task_id: "task_old",
            run_id: "run_old",
          },
        })}
        error=""
        pending={false}
        project={project()}
        workItem={workItem()}
        roles={[role()]}
        onClose={vi.fn()}
        onSave={onSave}
      />,
    );

    await userEvent.clear(screen.getByLabelText("Task ID"));
    await userEvent.type(screen.getByLabelText("Task ID"), "task_new");
    await userEvent.clear(screen.getByLabelText("Run ID"));
    await userEvent.type(screen.getByLabelText("Run ID"), "run_new");
    await userEvent.type(screen.getByLabelText("Context snapshot ID"), "ctx_new");
    await userEvent.click(screen.getByRole("button", { name: "Save assignment" }));

    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        id: "assign_1",
        taskID: "task_new",
        runID: "run_new",
        contextSnapshotID: "ctx_new",
      }),
    );
  });
});
