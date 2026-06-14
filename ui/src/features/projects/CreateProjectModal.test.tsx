import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { CreateProjectModal } from "./CreateProjectModal";

describe("CreateProjectModal", () => {
  it("creates a rootless project from name and purpose", async () => {
    const onSave = vi.fn();
    render(
      <CreateProjectModal
        error=""
        pending={false}
        onChooseWorkspace={vi.fn(async () => null)}
        onClose={vi.fn()}
        onSave={onSave}
      />,
    );

    const dialog = screen.getByRole("dialog", { name: "Create project" });
    fireEvent.change(within(dialog).getByLabelText("Name"), {
      target: { value: "Launch plan" },
    });
    fireEvent.change(within(dialog).getByLabelText("Purpose"), {
      target: { value: "Coordinate the launch narrative and approvals." },
    });
    await userEvent.click(within(dialog).getByRole("button", { name: "Create project" }));

    expect(onSave).toHaveBeenCalledWith({
      name: "Launch plan",
      description: "Coordinate the launch narrative and approvals.",
      rootPath: "",
      rootGitBranch: "",
    });
  });

  it("can choose an optional workspace folder without requiring code", async () => {
    const onChooseWorkspace = vi.fn(async () => ({
      path: "/Users/alice/dev/hecate",
      branch: "main",
    }));
    render(
      <CreateProjectModal
        error=""
        pending={false}
        onChooseWorkspace={onChooseWorkspace}
        onClose={vi.fn()}
        onSave={vi.fn()}
      />,
    );

    const dialog = screen.getByRole("dialog", { name: "Create project" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Choose folder" }));

    await waitFor(() => {
      expect(within(dialog).getByLabelText("Name")).toHaveValue("hecate");
    });
    expect(within(dialog).getByLabelText("Folder path")).toHaveValue("/Users/alice/dev/hecate");
    expect(within(dialog).getByLabelText("Git branch")).toHaveValue("main");
  });
});
