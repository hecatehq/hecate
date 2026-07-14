import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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

    const plan = screen.getByRole("region", { name: "Assignment plan" });
    expect(screen.getByLabelText("Responsibility")).toHaveFocus();
    expect(within(plan).getByText("Ready to add")).toBeTruthy();
    expect(within(plan).getByText("Software developer")).toBeTruthy();
    expect(within(plan).getByText("Uses the work item's workspace")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Responsibility"), {
      target: { value: "researcher" },
    });
    expect(screen.getByLabelText("Work done by")).toHaveValue("external_agent");
    expect(within(plan).getByText("Researcher")).toBeTruthy();
    expect(within(plan).getByText("External Agent")).toBeTruthy();
    expect(within(plan).queryByText("external_agent")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Add assignment" }));

    expect(onCreate).toHaveBeenCalledWith({
      roleID: "researcher",
      driverKind: "external_agent",
      rootID: "",
    });
  });

  it("defaults to the selected work item's owner when built-in roles are present", async () => {
    const onCreate = vi.fn();

    render(
      <NewAssignmentModal
        error=""
        pending={false}
        project={{ ...project(), roots: [] }}
        workItem={workItem({ owner_role_id: "researcher", root_id: undefined })}
        roles={[
          role({ built_in: true }),
          role({
            id: "researcher",
            name: "Researcher",
            default_driver_kind: "manual",
          }),
        ]}
        onClose={vi.fn()}
        onCreate={onCreate}
      />,
    );

    expect(screen.getByLabelText("Responsibility")).toHaveValue("researcher");
    expect(screen.getByLabelText("Work done by")).toHaveValue("manual");
    await userEvent.click(screen.getByRole("button", { name: "Add assignment" }));
    expect(onCreate).toHaveBeenCalledWith({
      roleID: "researcher",
      driverKind: "manual",
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

    const plan = screen.getByRole("region", { name: "Assignment plan" });
    expect(screen.getByRole("dialog", { name: "Add assignment" })).toBeTruthy();
    expect(within(plan).getAllByText("Select a responsibility")).toHaveLength(2);
    expect(screen.getByRole("button", { name: "Add assignment" })).toBeDisabled();
  });

  it("does not dismiss or resubmit assignment forms while a mutation is pending", async () => {
    const onCreate = vi.fn();
    const onClose = vi.fn();
    const firstRender = render(
      <NewAssignmentModal
        error=""
        pending
        project={project()}
        workItem={workItem()}
        roles={[role()]}
        onClose={onClose}
        onCreate={onCreate}
      />,
    );

    const addDialog = screen.getByRole("dialog", { name: "Add assignment" });
    expect(addDialog.querySelector("form")).toHaveAttribute("aria-busy", "true");
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    fireEvent.submit(addDialog.querySelector("form")!);
    await userEvent.keyboard("{Escape}");
    expect(onCreate).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    firstRender.unmount();

    const onSave = vi.fn();
    render(
      <EditAssignmentModal
        assignment={assignment()}
        error=""
        pending
        project={project()}
        workItem={workItem()}
        roles={[role()]}
        onClose={vi.fn()}
        onSave={onSave}
      />,
    );

    fireEvent.submit(
      screen.getByRole("dialog", { name: "Edit assignment" }).querySelector("form")!,
    );
    expect(onSave).not.toHaveBeenCalled();
  });

  it("submits an assignment only once while creation is in flight", async () => {
    let resolveCreate = () => {};
    const creation = new Promise<void>((resolve) => {
      resolveCreate = resolve;
    });
    const onCreate = vi.fn(() => creation);
    const onClose = vi.fn();

    render(
      <NewAssignmentModal
        error=""
        pending={false}
        project={project()}
        workItem={workItem()}
        roles={[role()]}
        onClose={onClose}
        onCreate={onCreate}
      />,
    );

    const form = screen.getByRole("dialog", { name: "Add assignment" }).querySelector("form")!;
    fireEvent.submit(form);
    fireEvent.submit(form);

    expect(onCreate).toHaveBeenCalledTimes(1);
    expect(form).toHaveAttribute("aria-busy", "true");
    expect(screen.getByRole("button", { name: "Adding…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    expect(screen.getByLabelText("Responsibility")).toBeDisabled();
    expect(screen.getByLabelText("Work done by")).toBeDisabled();
    expect(screen.getByLabelText("Workspace (optional)")).toBeDisabled();
    await userEvent.keyboard("{Escape}");
    expect(onClose).not.toHaveBeenCalled();
    resolveCreate();
    await creation;
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Add assignment" })).toBeEnabled(),
    );
    expect(screen.getByLabelText("Responsibility")).toBeEnabled();
    expect(screen.getByLabelText("Work done by")).toBeEnabled();
    expect(screen.getByLabelText("Workspace (optional)")).toBeEnabled();
  });

  it("adds rootless Human assignments without workspace controls", async () => {
    const onCreate = vi.fn();

    render(
      <NewAssignmentModal
        error=""
        pending={false}
        project={{ ...project(), roots: [] }}
        workItem={workItem({ root_id: undefined })}
        roles={[role({ default_driver_kind: "manual" })]}
        onClose={vi.fn()}
        onCreate={onCreate}
      />,
    );

    const destination = screen.getByLabelText("Work done by");
    expect(destination).toHaveValue("manual");
    expect(destination).toHaveAccessibleDescription(
      "Track work completed by a person outside Hecate.",
    );
    expect(screen.getByRole("option", { name: "Human" })).toBeTruthy();
    expect(screen.queryByLabelText("Workspace (optional)")).toBeNull();
    expect(
      within(screen.getByRole("region", { name: "Assignment plan" })).queryByText("Workspace"),
    ).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Add assignment" }));

    expect(onCreate).toHaveBeenCalledWith({
      roleID: "software_developer",
      driverKind: "manual",
      rootID: "",
    });
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

  it("keeps queued Human assignment details editable without exposing runtime fields", async () => {
    const onSave = vi.fn();

    render(
      <EditAssignmentModal
        assignment={assignment({ driver_kind: "manual", root_id: "root_main" })}
        error=""
        pending={false}
        project={project()}
        workItem={workItem()}
        roles={[role()]}
        onClose={vi.fn()}
        onSave={onSave}
      />,
    );

    const destination = screen.getByLabelText("Work done by");
    expect(destination).toHaveValue("manual");
    expect(destination).toHaveAccessibleDescription(/Use the work item actions/);
    expect(destination).not.toBeDisabled();
    expect(screen.getByLabelText("Workspace (optional)")).toHaveValue("root_main");
    expect(screen.getByLabelText("Status")).toHaveValue("queued");
    expect(screen.queryByLabelText("Task ID")).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Save assignment" }));

    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({ driverKind: "manual", rootID: "root_main" }),
    );
  });

  it("resets and locks queued Human details when cancellation is selected", async () => {
    const onSave = vi.fn();

    render(
      <EditAssignmentModal
        assignment={assignment({ driver_kind: "manual", root_id: "root_main" })}
        error=""
        pending={false}
        project={{
          ...project(),
          roots: [
            root({ id: "root_main", path: "/workspace/main" }),
            root({ id: "root_other", path: "/workspace/other" }),
          ],
        }}
        workItem={workItem()}
        roles={[
          role(),
          role({ id: "researcher", name: "Researcher", default_driver_kind: "manual" }),
        ]}
        onClose={vi.fn()}
        onSave={onSave}
      />,
    );

    await userEvent.selectOptions(screen.getByLabelText("Responsibility"), "researcher");
    await userEvent.selectOptions(screen.getByLabelText("Workspace (optional)"), "root_other");
    await userEvent.selectOptions(screen.getByLabelText("Status"), "cancelled");

    expect(screen.getByLabelText("Responsibility")).toHaveValue("software_developer");
    expect(screen.getByLabelText("Workspace (optional)")).toHaveValue("root_main");
    expect(screen.getByLabelText("Responsibility")).toBeDisabled();
    expect(screen.getByLabelText("Work done by")).toBeDisabled();
    expect(screen.getByLabelText("Workspace (optional)")).toBeDisabled();
    expect(screen.getByText(/Progress is saved separately/)).toBeTruthy();

    await userEvent.click(
      screen.getByRole("checkbox", { name: "I understand this closes the assignment" }),
    );
    await userEvent.click(screen.getByRole("button", { name: "Save assignment" }));
    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        roleID: "software_developer",
        driverKind: "manual",
        rootID: "root_main",
        status: "cancelled",
      }),
    );
  });

  it("locks active Human assignment details while preserving explicit status control", async () => {
    const onSave = vi.fn();

    render(
      <EditAssignmentModal
        assignment={assignment({
          driver_kind: "manual",
          root_id: "root_main",
          status: "running",
          started_at: "2026-07-13T10:00:00Z",
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

    expect(screen.getByLabelText("Responsibility")).toBeDisabled();
    expect(screen.getByLabelText("Work done by")).toBeDisabled();
    expect(screen.getByLabelText("Workspace (optional)")).toBeDisabled();
    const status = screen.getByLabelText("Status");
    expect(status).not.toBeDisabled();
    expect(status).toHaveValue("running");
    expect(within(status).queryByRole("option", { name: "queued" })).toBeNull();

    await userEvent.selectOptions(status, "awaiting_approval");
    await userEvent.click(screen.getByRole("button", { name: "Save assignment" }));
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ status: "awaiting_approval" }));
  });

  it.each([
    ["failed", "Mark this work as failed?"],
    ["cancelled", "Cancel this work?"],
  ])("requires explicit confirmation before saving Human status %s", async (status, warning) => {
    const onSave = vi.fn();

    render(
      <EditAssignmentModal
        assignment={assignment({
          driver_kind: "manual",
          status: "running",
          started_at: "2026-07-13T10:00:00Z",
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

    await userEvent.selectOptions(screen.getByLabelText("Status"), status);
    expect(screen.getByText(warning)).toBeTruthy();
    const save = screen.getByRole("button", { name: "Save assignment" });
    expect(save).toBeDisabled();

    fireEvent.submit(
      screen.getByRole("dialog", { name: "Edit assignment" }).querySelector("form")!,
    );
    expect(onSave).not.toHaveBeenCalled();

    await userEvent.click(
      screen.getByRole("checkbox", { name: "I understand this closes the assignment" }),
    );
    expect(save).not.toBeDisabled();
    await userEvent.click(save);
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ status }));
  });
});
