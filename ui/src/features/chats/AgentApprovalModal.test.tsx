import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { ChatApprovalRecord } from "../../types/chat";
import { AgentApprovalModal } from "./AgentApprovalModal";

function approvalRecord(overrides: Partial<ChatApprovalRecord> = {}): ChatApprovalRecord {
  return {
    id: "ap-1",
    session_id: "s",
    adapter_id: "codex",
    tool_kind: "fs",
    tool_name: "write_file",
    status: "pending",
    acp_options: [
      { option_id: "approve_once", kind: "allow_once", name: "Approve once" },
      { option_id: "approve_session", kind: "allow_always", name: "Approve for the session" },
    ],
    scope_choices: ["once", "session", "adapter_tool"],
    created_at: "2026-04-21T10:00:00Z",
    expires_at: "2026-04-21T10:05:00Z",
    ...overrides,
  };
}

function setup(
  fetchResult: ChatApprovalRecord | null = approvalRecord(),
  resolveResult = true,
  cancelResult = true,
) {
  const fetchApproval = vi.fn(async () => fetchResult);
  const onResolve = vi.fn(async () => resolveResult);
  const onCancel = vi.fn(async () => cancelResult);
  const onClose = vi.fn();
  return { fetchApproval, onResolve, onCancel, onClose };
}

describe("AgentApprovalModal", () => {
  it("shows a loading state until the full row resolves, then renders adapter / tool identity", async () => {
    let resolveFetch!: (row: ChatApprovalRecord | null) => void;
    const fetchApproval = vi.fn(
      () =>
        new Promise<ChatApprovalRecord | null>((resolve) => {
          resolveFetch = resolve;
        }),
    );
    const { onResolve, onCancel, onClose } = setup();
    render(
      <AgentApprovalModal
        sessionID="s"
        approvalID="ap-1"
        onClose={onClose}
        fetchApproval={fetchApproval}
        onResolve={onResolve}
        onCancel={onCancel}
      />,
    );
    expect(screen.getByTestId("agent-approval-modal-loading")).toBeTruthy();
    resolveFetch(approvalRecord());
    await waitFor(() => expect(screen.queryByTestId("agent-approval-modal-loading")).toBeNull());
    expect(screen.getByText(/codex/)).toBeTruthy();
    expect(screen.getByText(/fs · write_file/)).toBeTruthy();
  });

  it("seeds defaults from the row (first scope choice + first ACP option) and posts them on submit", async () => {
    const { fetchApproval, onResolve, onCancel, onClose } = setup();
    render(
      <AgentApprovalModal
        sessionID="s"
        approvalID="ap-1"
        onClose={onClose}
        fetchApproval={fetchApproval}
        onResolve={onResolve}
        onCancel={onCancel}
      />,
    );
    await waitFor(() => expect(fetchApproval).toHaveBeenCalled());
    const user = userEvent.setup();
    await user.click(screen.getByTestId("agent-approval-modal-submit"));
    await waitFor(() =>
      expect(onResolve).toHaveBeenCalledWith("s", "ap-1", {
        decision: "approve",
        scope: "once",
        selected_option: "approve_once",
      }),
    );
    expect(onClose).toHaveBeenCalled();
  });

  it("explains deny/cancel semantics and selected grant scope", async () => {
    const { fetchApproval, onResolve, onCancel, onClose } = setup();
    render(
      <AgentApprovalModal
        sessionID="s"
        approvalID="ap-1"
        onClose={onClose}
        fetchApproval={fetchApproval}
        onResolve={onResolve}
        onCancel={onCancel}
      />,
    );
    await waitFor(() => expect(fetchApproval).toHaveBeenCalled());

    expect(await screen.findByText(/Deny sends the selected reject option/)).toBeTruthy();
    expect(screen.getByText("allow always")).toBeTruthy();
    expect(screen.getByTestId("agent-approval-modal-scope-description").textContent).toContain(
      "Only this pending request",
    );

    const user = userEvent.setup();
    await user.click(screen.getByTestId("agent-approval-modal-scope-session"));
    expect(screen.getByTestId("agent-approval-modal-scope-description").textContent).toContain(
      "external-agent chat session",
    );
  });

  it("requires a confirm step when the broad agent-tool scope is picked", async () => {
    const { fetchApproval, onResolve, onCancel, onClose } = setup();
    render(
      <AgentApprovalModal
        sessionID="s"
        approvalID="ap-1"
        onClose={onClose}
        fetchApproval={fetchApproval}
        onResolve={onResolve}
        onCancel={onCancel}
      />,
    );
    await waitFor(() => expect(fetchApproval).toHaveBeenCalled());
    const user = userEvent.setup();

    // Pick the broad scope.
    await user.click(await screen.findByTestId("agent-approval-modal-scope-adapter_tool"));
    expect(screen.getByTestId("agent-approval-modal-broad-warning")).toBeTruthy();
    expect(screen.getByText("agent tool")).toBeTruthy();
    expect(screen.getByTestId("agent-approval-modal-scope-description").textContent).toContain(
      "Every future call",
    );
    expect(screen.queryByText("adapter_tool")).toBeNull();

    // First submit click only arms — must not have called onResolve yet.
    await user.click(screen.getByTestId("agent-approval-modal-submit"));
    expect(onResolve).not.toHaveBeenCalled();

    // Second submit click sends the resolve.
    await user.click(screen.getByTestId("agent-approval-modal-submit"));
    await waitFor(() => expect(onResolve).toHaveBeenCalled());
    expect(onResolve).toHaveBeenCalledWith(
      "s",
      "ap-1",
      expect.objectContaining({ decision: "approve", scope: "adapter_tool" }),
    );
  });

  it("forwards a cancel-approval click to onCancel", async () => {
    const { fetchApproval, onResolve, onCancel, onClose } = setup();
    render(
      <AgentApprovalModal
        sessionID="s"
        approvalID="ap-1"
        onClose={onClose}
        fetchApproval={fetchApproval}
        onResolve={onResolve}
        onCancel={onCancel}
      />,
    );
    await waitFor(() => expect(fetchApproval).toHaveBeenCalled());
    const user = userEvent.setup();
    await user.click(screen.getByTestId("agent-approval-modal-cancel"));
    await waitFor(() => expect(onCancel).toHaveBeenCalledWith("s", "ap-1"));
    expect(onClose).toHaveBeenCalled();
  });

  it("renders an error and offers Cancel approval when the row fetch returns null", async () => {
    const { fetchApproval, onResolve, onCancel, onClose } = setup(null);
    render(
      <AgentApprovalModal
        sessionID="s"
        approvalID="ap-1"
        onClose={onClose}
        fetchApproval={fetchApproval}
        onResolve={onResolve}
        onCancel={onCancel}
      />,
    );
    await waitFor(() => expect(fetchApproval).toHaveBeenCalled());
    expect(await screen.findByText(/Could not load this approval/)).toBeTruthy();
  });

  it("flips the decision to deny and selects a reject option when available", async () => {
    const { fetchApproval, onResolve, onCancel, onClose } = setup(
      approvalRecord({
        acp_options: [
          { option_id: "approve_once", kind: "allow_once", name: "Approve once" },
          { option_id: "reject_once", kind: "reject_once", name: "Reject once" },
        ],
      }),
    );
    render(
      <AgentApprovalModal
        sessionID="s"
        approvalID="ap-1"
        onClose={onClose}
        fetchApproval={fetchApproval}
        onResolve={onResolve}
        onCancel={onCancel}
      />,
    );
    await waitFor(() => expect(fetchApproval).toHaveBeenCalled());
    const user = userEvent.setup();
    // findByTestId: the deny radio only renders after the row
    // resolves, and the loading skeleton can still be on screen
    // when fetchApproval's call is observed but its promise
    // microtask hasn't flushed yet (CI flake).
    await user.click(await screen.findByTestId("agent-approval-modal-decision-deny"));
    expect(
      screen.getByTestId("agent-approval-modal-option-approve_once").querySelector("input"),
    ).toBeDisabled();
    await user.click(screen.getByTestId("agent-approval-modal-submit"));
    await waitFor(() =>
      expect(onResolve).toHaveBeenCalledWith(
        "s",
        "ap-1",
        expect.objectContaining({
          decision: "deny",
          selected_option: "reject_once",
        }),
      ),
    );
  });

  it("does not send an allow selected_option when denying without reject options", async () => {
    const { fetchApproval, onResolve, onCancel, onClose } = setup();
    render(
      <AgentApprovalModal
        sessionID="s"
        approvalID="ap-1"
        onClose={onClose}
        fetchApproval={fetchApproval}
        onResolve={onResolve}
        onCancel={onCancel}
      />,
    );
    await waitFor(() => expect(fetchApproval).toHaveBeenCalled());
    const user = userEvent.setup();
    await user.click(screen.getByTestId("agent-approval-modal-decision-deny"));
    await user.click(screen.getByTestId("agent-approval-modal-submit"));
    await waitFor(() =>
      expect(onResolve).toHaveBeenCalledWith("s", "ap-1", {
        decision: "deny",
        scope: "once",
      }),
    );
  });
});
