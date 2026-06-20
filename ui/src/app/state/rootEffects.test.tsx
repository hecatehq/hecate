import { render, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { RootEffects } from "./rootEffects";
import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
} from "../../test/runtime-console-fixture";
import { withRuntimeConsole } from "../../test/runtime-console-render";
import type { AgentAdapterRecord } from "../../types/agent-adapter";

function adapter(id: string, overrides: Partial<AgentAdapterRecord> = {}): AgentAdapterRecord {
  return {
    id,
    name: id,
    kind: "acp",
    command: `${id}-acp`,
    available: true,
    status: "available",
    cost_mode: "external",
    supports_logout: false,
    ...overrides,
  };
}

describe("RootEffects", () => {
  it("does not actively probe agent adapters during quiet startup", async () => {
    const probeAgentAdapter = vi.fn(async () => null);
    const state = createRuntimeConsoleFixture({
      agentAdapters: [
        adapter("codex"),
        adapter("claude_code"),
        adapter("cursor_agent"),
        adapter("grok_build"),
      ],
    });
    const actions = {
      ...createRuntimeConsoleActions(),
      probeAgentAdapter,
      loadDashboard: vi.fn(async () => undefined),
      loadProjects: vi.fn(async () => undefined),
    };

    render(withRuntimeConsole(<RootEffects />, { state, actions }));

    await waitFor(() => expect(actions.loadDashboard).toHaveBeenCalled());
    expect(probeAgentAdapter).not.toHaveBeenCalled();
  });

  it("keeps passive adapter arrivals from starting ACP sessions", async () => {
    const probeAgentAdapter = vi.fn(async () => null);
    const actions = {
      ...createRuntimeConsoleActions(),
      probeAgentAdapter,
      loadDashboard: vi.fn(async () => undefined),
      loadProjects: vi.fn(async () => undefined),
    };
    const initial = createRuntimeConsoleFixture({ agentAdapters: [] });
    const { rerender } = render(withRuntimeConsole(<RootEffects />, { state: initial, actions }));

    const next = createRuntimeConsoleFixture({
      agentAdapters: [adapter("cursor_agent"), adapter("grok_build")],
    });
    rerender(withRuntimeConsole(<RootEffects />, { state: next, actions }));

    await waitFor(() => expect(actions.loadDashboard).toHaveBeenCalled());
    expect(probeAgentAdapter).not.toHaveBeenCalled();
  });
});
