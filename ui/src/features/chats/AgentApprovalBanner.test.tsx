import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { PendingAgentApproval } from "../../types/chat";
import { AgentApprovalAutoModeBanner, AgentApprovalsBanner } from "./AgentApprovalBanner";

function approval(overrides: Partial<PendingAgentApproval> = {}): PendingAgentApproval {
  return {
    approval_id: "ap-1",
    session_id: "s",
    adapter_id: "codex",
    tool_kind: "fs",
    tool_name: "write_file",
    created_at: "2026-04-21T10:00:00Z",
    expires_at: "2026-04-21T10:05:00Z",
    ...overrides,
  };
}

describe("AgentApprovalAutoModeBanner", () => {
  it("renders nothing for prompt / deny / empty modes", () => {
    const { rerender, queryByTestId } = render(<AgentApprovalAutoModeBanner mode="prompt" />);
    expect(queryByTestId("agent-approval-auto-banner")).toBeNull();

    rerender(<AgentApprovalAutoModeBanner mode="deny" />);
    expect(queryByTestId("agent-approval-auto-banner")).toBeNull();

    rerender(<AgentApprovalAutoModeBanner mode="" />);
    expect(queryByTestId("agent-approval-auto-banner")).toBeNull();
  });

  it("renders for mode=auto with the env-var hint and warn text", () => {
    render(<AgentApprovalAutoModeBanner mode="auto" />);
    const banner = screen.getByTestId("agent-approval-auto-banner");
    expect(banner.textContent).toMatch(/Auto-approval is on/);
    expect(banner.textContent).toMatch(/HECATE_AGENT_ADAPTER_APPROVAL_MODE=auto/);
  });
});

describe("AgentApprovalsBanner", () => {
  it("renders nothing when pending is empty", () => {
    const { queryByTestId } = render(<AgentApprovalsBanner pending={[]} onSelect={vi.fn()} />);
    expect(queryByTestId("agent-approval-banner")).toBeNull();
  });

  it("renders both rows when there are exactly two pending", () => {
    render(
      <AgentApprovalsBanner
        pending={[
          approval({ approval_id: "ap-1", tool_name: "write_file" }),
          approval({
            approval_id: "ap-2",
            tool_name: "exec_command",
            created_at: "2026-04-21T10:01:00Z",
          }),
        ]}
        onSelect={vi.fn()}
      />,
    );
    expect(screen.getByTestId("agent-approval-banner")).toBeTruthy();
    expect(screen.getAllByTestId("agent-approval-banner-review")).toHaveLength(2);
    expect(screen.queryByTestId("agent-approval-banner-more")).toBeNull();
  });

  it("collapses to two visible rows + an overflow button when there are three or more", () => {
    render(
      <AgentApprovalsBanner
        pending={[
          approval({ approval_id: "ap-1", created_at: "2026-04-21T10:00:00Z" }),
          approval({ approval_id: "ap-2", created_at: "2026-04-21T10:01:00Z" }),
          approval({ approval_id: "ap-3", created_at: "2026-04-21T10:02:00Z" }),
          approval({ approval_id: "ap-4", created_at: "2026-04-21T10:03:00Z" }),
        ]}
        onSelect={vi.fn()}
      />,
    );
    expect(screen.getAllByTestId("agent-approval-banner-review")).toHaveLength(2);
    const more = screen.getByTestId("agent-approval-banner-more");
    expect(more.textContent).toMatch(/\+ 2 more/);
  });

  it("calls onSelect with the row's approval id on Review click", async () => {
    const onSelect = vi.fn();
    render(
      <AgentApprovalsBanner
        pending={[
          approval({ approval_id: "ap-1" }),
          approval({ approval_id: "ap-2", created_at: "2026-04-21T10:01:00Z" }),
        ]}
        onSelect={onSelect}
      />,
    );
    const user = userEvent.setup();
    await user.click(screen.getAllByTestId("agent-approval-banner-review")[0]!);
    expect(onSelect).toHaveBeenCalledWith("ap-1");
  });

  it("opens the next pending approval (FIFO) when overflow is clicked", async () => {
    const onSelect = vi.fn();
    render(
      <AgentApprovalsBanner
        pending={[
          approval({ approval_id: "ap-1", created_at: "2026-04-21T10:00:00Z" }),
          approval({ approval_id: "ap-2", created_at: "2026-04-21T10:01:00Z" }),
          approval({ approval_id: "ap-3", created_at: "2026-04-21T10:02:00Z" }),
        ]}
        onSelect={onSelect}
      />,
    );
    const user = userEvent.setup();
    await user.click(screen.getByTestId("agent-approval-banner-more"));
    // ap-1 + ap-2 are visible; the overflow should open ap-3.
    expect(onSelect).toHaveBeenCalledWith("ap-3");
  });
});
