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
    ...overrides,
  };
}

describe("RootEffects", () => {
  it("probes direct startup agent adapters concurrently", async () => {
    const pending = new Map<string, () => void>();
    const probeAgentAdapter = vi.fn(
      (adapterID: string) =>
        new Promise<null>((resolve) => {
          pending.set(adapterID, () => resolve(null));
        }),
    );
    const state = createRuntimeConsoleFixture({
      agentAdapters: [
        adapter("codex", { managed: true }),
        adapter("claude_code", { managed: true }),
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

    await waitFor(() => expect(probeAgentAdapter).toHaveBeenCalledTimes(2));
    expect(probeAgentAdapter.mock.calls.map(([id]) => id)).toEqual(["cursor_agent", "grok_build"]);
    // If the effect awaited each probe serially, only the first call
    // would exist until its promise resolved. Seeing both pending
    // promises proves the startup pass is concurrent.
    expect([...pending.keys()]).toEqual(["cursor_agent", "grok_build"]);
    for (const resolve of pending.values()) resolve();
  });

  it("does not run managed adapter probes during quiet startup", async () => {
    const probeAgentAdapter = vi.fn(async () => null);
    const state = createRuntimeConsoleFixture({
      agentAdapters: [
        adapter("codex", { managed: true, managed_package: "@zed-industries/codex-acp" }),
        adapter("claude_code", {
          managed: true,
          managed_package: "@agentclientprotocol/claude-agent-acp",
        }),
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

  it("probes adapters that arrive after dashboard load only once", async () => {
    const probeAgentAdapter = vi.fn(async () => null);
    const actions = {
      ...createRuntimeConsoleActions(),
      probeAgentAdapter,
      loadDashboard: vi.fn(async () => undefined),
      loadProjects: vi.fn(async () => undefined),
    };
    const initial = createRuntimeConsoleFixture({ agentAdapters: [] });
    const { rerender } = render(withRuntimeConsole(<RootEffects />, { state: initial, actions }));

    expect(probeAgentAdapter).not.toHaveBeenCalled();

    const next = createRuntimeConsoleFixture({ agentAdapters: [adapter("claude_code")] });
    rerender(withRuntimeConsole(<RootEffects />, { state: next, actions }));

    await waitFor(() => expect(probeAgentAdapter).toHaveBeenCalledWith("claude_code"));
    rerender(withRuntimeConsole(<RootEffects />, { state: { ...next }, actions }));
    expect(probeAgentAdapter).toHaveBeenCalledTimes(1);
  });
});
