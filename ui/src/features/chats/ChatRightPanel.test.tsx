import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ChatRightPanel } from "./ChatRightPanel";

describe("ChatRightPanel", () => {
  it("renders children in a shared right panel width", () => {
    render(
      <ChatRightPanel
        ariaLabel="Chat settings panel"
        className="test-panel"
        id="chat-right-panel"
        width={380}
        onWidthChange={vi.fn()}
      >
        <div>Panel content</div>
      </ChatRightPanel>,
    );

    expect(screen.getByLabelText("Chat settings panel")).toHaveClass(
      "chat-right-panel",
      "test-panel",
    );
    expect(screen.getByLabelText("Chat settings panel")).toHaveStyle({ width: "380px" });
    expect(screen.getByLabelText("Chat settings panel")).toHaveAttribute("id", "chat-right-panel");
    expect(screen.getByLabelText("Chat settings panel")).toHaveAttribute("tabindex", "-1");
    expect(screen.getByText("Panel content")).toBeInTheDocument();
  });

  it("resizes horizontally from the left edge", () => {
    const onWidthChange = vi.fn();
    render(
      <ChatRightPanel ariaLabel="Workspace changes panel" width={380} onWidthChange={onWidthChange}>
        <div>Diff content</div>
      </ChatRightPanel>,
    );

    const handle = screen.getByRole("separator", { name: "Resize right panel" });
    fireEvent.pointerDown(handle, { clientX: 800, pointerId: 1 });
    fireEvent.pointerMove(handle, { clientX: 740, pointerId: 1 });

    expect(onWidthChange).toHaveBeenCalledWith(440);
  });

  it("supports keyboard resizing", () => {
    const onWidthChange = vi.fn();
    render(
      <ChatRightPanel ariaLabel="Chat settings panel" width={380} onWidthChange={onWidthChange}>
        <div>Panel content</div>
      </ChatRightPanel>,
    );

    const handle = screen.getByRole("separator", { name: "Resize right panel" });
    expect(handle).toHaveAttribute("aria-orientation", "vertical");
    expect(handle).toHaveAttribute("aria-valuemin", "320");
    expect(handle).toHaveAttribute("aria-valuemax", "560");
    expect(handle).toHaveAttribute("aria-valuenow", "380");
    fireEvent.keyDown(handle, { key: "ArrowLeft" });
    fireEvent.keyDown(handle, { key: "ArrowRight" });

    expect(onWidthChange).toHaveBeenCalledWith(388);
    expect(onWidthChange).toHaveBeenCalledWith(372);
  });
});
