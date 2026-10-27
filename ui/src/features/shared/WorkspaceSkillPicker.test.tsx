import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { getWorkspaceSkills } from "../../lib/api";
import type { WorkspaceSkillSelection } from "../../types/workspace-skill";
import { WorkspaceSkillPicker } from "./WorkspaceSkillPicker";

vi.mock("../../lib/api", () => ({ getWorkspaceSkills: vi.fn() }));

const skill = {
  name: "review",
  path: ".agents/skills/review/SKILL.md",
  sha256: "a".repeat(64),
  bytes: 123,
};
function Picker() {
  const [value, setValue] = useState<WorkspaceSkillSelection[]>([]);
  return <WorkspaceSkillPicker workspace="/workspace" value={value} onChange={setValue} />;
}

describe("WorkspaceSkillPicker", () => {
  beforeEach(() => {
    vi.mocked(getWorkspaceSkills).mockReset();
  });

  it("requires explicit inspection and keyboard selection before sharing a skill", async () => {
    vi.mocked(getWorkspaceSkills).mockResolvedValue({ object: "workspace_skills", data: [skill] });
    const user = userEvent.setup();
    render(<Picker />);
    expect(getWorkspaceSkills).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Choose skills" }));
    const checkbox = await screen.findByRole("checkbox", { name: /review/ });
    expect(checkbox).not.toBeChecked();
    checkbox.focus();
    await user.keyboard(" ");
    expect(checkbox).toBeChecked();
    expect(screen.getByText(/1 skills selected/)).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Refresh skills" }));
    expect(checkbox).not.toBeChecked();
  });

  it("explains why inspection needs a workspace", () => {
    render(<WorkspaceSkillPicker workspace="" value={[]} onChange={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Choose skills" })).toBeDisabled();
    expect(screen.getByText("Choose a workspace first.")).toBeVisible();
  });

  it("surfaces inspection failure without selecting anything", async () => {
    vi.mocked(getWorkspaceSkills).mockRejectedValue(new Error("private host details"));
    const onChange = vi.fn();
    render(<WorkspaceSkillPicker workspace="/workspace" value={[]} onChange={onChange} />);
    await userEvent.setup().click(screen.getByRole("button", { name: "Choose skills" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Skills could not be inspected");
    expect(screen.queryByText("private host details")).toBeNull();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("aborts and ignores a late response after unmount", async () => {
    let resolve!: (result: Awaited<ReturnType<typeof getWorkspaceSkills>>) => void;
    vi.mocked(getWorkspaceSkills).mockImplementation(
      () =>
        new Promise((done) => {
          resolve = done;
        }),
    );
    const onChange = vi.fn();
    const view = render(
      <WorkspaceSkillPicker workspace="/workspace" value={[]} onChange={onChange} />,
    );
    await userEvent.setup().click(screen.getByRole("button", { name: "Choose skills" }));
    const signal = vi.mocked(getWorkspaceSkills).mock.calls[0][1];
    view.unmount();
    expect(signal?.aborted).toBe(true);
    await act(async () => resolve({ object: "workspace_skills", data: [skill] }));
    expect(onChange).not.toHaveBeenCalled();
  });
});
