import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import {
  EntityDetailHeader,
  EntityDetailPane,
  EntityIndexHeader,
  EntityIndexHeading,
  EntityIndexPanel,
  EntityIndexState,
  EntityListRow,
  MasterDetailWorkspace,
} from "./EntityWorkspace";

describe("EntityWorkspace", () => {
  it("exposes the shared index and detail structure with named landmarks", () => {
    const { container } = render(
      <MasterDetailWorkspace>
        <EntityIndexPanel aria-label="Work index">
          <EntityIndexHeader>
            <EntityIndexHeading>Work</EntityIndexHeading>
          </EntityIndexHeader>
        </EntityIndexPanel>
        <EntityDetailPane>
          <EntityDetailHeader aria-label="Work details">Selected work</EntityDetailHeader>
        </EntityDetailPane>
      </MasterDetailWorkspace>,
    );

    expect(container.firstElementChild).toHaveClass("entity-workspace");
    expect(screen.getByRole("complementary", { name: "Work index" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Work", level: 2 })).toBeTruthy();
    expect(screen.getByRole("banner", { name: "Work details" })).toBeTruthy();
  });

  it("announces a busy index state", () => {
    render(<EntityIndexState busy>Loading work…</EntityIndexState>);

    expect(screen.getByRole("status")).toHaveAttribute("aria-busy", "true");
    expect(screen.getByText("Loading work…")).toBeTruthy();
  });
});

describe("EntityListRow", () => {
  it("uses a native deep link while plain clicks stay in the current app", async () => {
    const onActivate = vi.fn();
    const onDelete = vi.fn();
    const user = userEvent.setup();
    render(
      <EntityListRow
        active
        aria-label="Task Compile release"
        href="/tasks?task=task-1&run=run-1"
        onActivate={onActivate}
        actions={
          <button type="button" onClick={onDelete}>
            Delete
          </button>
        }
      >
        Compile release
      </EntityListRow>,
    );

    const link = screen.getByRole("link", { name: "Task Compile release" });
    expect(link).toHaveAttribute("href", "/tasks?task=task-1&run=run-1");
    expect(link).toHaveAttribute("aria-current", "page");

    await user.click(link);
    expect(onActivate).toHaveBeenCalledTimes(1);

    await user.hover(link);
    const deleteButton = screen.getByRole("button", { name: "Delete" });
    expect(link.contains(deleteButton)).toBe(false);
    await user.click(deleteButton);
    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(onActivate).toHaveBeenCalledTimes(1);
  });

  it("leaves modified clicks to native navigation and preserves Space activation", async () => {
    const onActivate = vi.fn();
    const user = userEvent.setup();
    render(
      <EntityListRow
        aria-label="Chat Release planning"
        href="/chats?chat=chat-1"
        onActivate={onActivate}
      >
        Release planning
      </EntityListRow>,
    );

    const link = screen.getByRole("link", { name: "Chat Release planning" });
    document.addEventListener("click", (event) => event.preventDefault(), { once: true });
    fireEvent.click(link, { ctrlKey: true });
    expect(onActivate).not.toHaveBeenCalled();

    act(() => link.focus());
    await user.keyboard(" ");
    expect(onActivate).toHaveBeenCalledTimes(1);
  });

  it("keeps disabled links focusable but blocks activation", async () => {
    const onActivate = vi.fn();
    const user = userEvent.setup();
    render(
      <EntityListRow
        aria-label="Chat Busy chat"
        disabled
        href="/chats?chat=busy"
        onActivate={onActivate}
      >
        Busy chat
      </EntityListRow>,
    );

    const link = screen.getByRole("link", { name: "Chat Busy chat" });
    expect(link).toHaveAttribute("aria-disabled", "true");
    act(() => link.focus());
    expect(link).toHaveFocus();
    await user.keyboard("{Enter} ");
    expect(onActivate).not.toHaveBeenCalled();
  });
});
