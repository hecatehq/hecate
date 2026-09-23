import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  createAgentPreset,
  deleteAgentPreset,
  getAgentPresets,
  updateAgentPreset,
} from "../../lib/api";
import type { AgentPresetRecord } from "../../types/agent-preset";
import { WorkPoliciesSettings } from "./WorkPoliciesSettings";

vi.mock("../../lib/api", async (importOriginal) => ({
  ...((await importOriginal()) as Record<string, unknown>),
  getAgentPresets: vi.fn(),
  createAgentPreset: vi.fn(),
  updateAgentPreset: vi.fn(),
  deleteAgentPreset: vi.fn(),
}));

function policy(overrides: Partial<AgentPresetRecord> = {}): AgentPresetRecord {
  return {
    id: "implementation",
    name: "Implementation",
    surface: "any",
    tools_enabled: true,
    writes_allowed: false,
    network_allowed: false,
    browser_interactions_allowed: false,
    approval_policy: "inherit",
    project_memory_policy: "inherit",
    context_source_policy: "inherit",
    ...overrides,
  };
}

beforeEach(() => {
  vi.mocked(getAgentPresets).mockReset();
  vi.mocked(getAgentPresets).mockResolvedValue({ object: "agent_presets", data: [] });
  vi.mocked(createAgentPreset).mockReset();
  vi.mocked(updateAgentPreset).mockReset();
  vi.mocked(deleteAgentPreset).mockReset();
  vi.mocked(deleteAgentPreset).mockResolvedValue();
});

describe("WorkPoliciesSettings", () => {
  it("creates, updates, and deletes global work policies through the shared editor", async () => {
    vi.mocked(createAgentPreset).mockResolvedValue({
      object: "agent_preset",
      data: policy(),
    });
    vi.mocked(updateAgentPreset).mockResolvedValue({
      object: "agent_preset",
      data: policy({ name: "Implementation review" }),
    });
    const user = userEvent.setup();
    render(<WorkPoliciesSettings />);

    expect(await screen.findByText("0 saved")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: /Manage policies/i }));
    await user.type(screen.getByLabelText("Policy ID"), "implementation");
    await user.type(screen.getByLabelText("Name"), "Implementation");
    await user.click(screen.getByRole("button", { name: "Create policy" }));

    expect(createAgentPreset).toHaveBeenCalledWith(
      expect.objectContaining({ id: "implementation", name: "Implementation" }),
    );
    await waitFor(() => expect(screen.getByText("1 saved")).toBeTruthy());

    await user.clear(screen.getByLabelText("Name"));
    await user.type(screen.getByLabelText("Name"), "Implementation review");
    await user.click(screen.getByRole("button", { name: "Save policy" }));
    expect(updateAgentPreset).toHaveBeenCalledWith(
      "implementation",
      expect.objectContaining({ name: "Implementation review" }),
    );

    await user.click(screen.getByRole("button", { name: "Delete policy" }));
    await user.click(screen.getByRole("button", { name: "Delete work policy" }));
    expect(deleteAgentPreset).toHaveBeenCalledWith("implementation");
    await waitFor(() => expect(screen.getByText("0 saved")).toBeTruthy());
  });
});
