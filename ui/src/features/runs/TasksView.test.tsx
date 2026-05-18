import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { getModels, getProviders, getTasks } from "../../lib/api";
import { createRuntimeConsoleActions, createRuntimeConsoleFixture } from "../../test/runtime-console-fixture";
import { withRuntimeConsole } from "../../test/runtime-console-render";
import { streamTurnCostKey, TasksView } from "./TasksView";

vi.mock("../../lib/api", async importOriginal => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...actual,
    getTasks: vi.fn(async () => ({ object: "list", data: [] })),
    getModels: vi.fn(async () => ({ object: "list", data: [] })),
    getProviders: vi.fn(async () => ({ object: "list", data: [] })),
  };
});

const localSession = { label: "Local" };

afterEach(() => {
  vi.mocked(getTasks).mockReset();
  vi.mocked(getTasks).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(getModels).mockReset();
  vi.mocked(getModels).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(getProviders).mockReset();
  vi.mocked(getProviders).mockResolvedValue({ object: "list", data: [] });
});

describe("streamTurnCostKey", () => {
  it("normalizes zero-based backend turn indexes to one-based UI turn numbers", () => {
    expect(streamTurnCostKey(0)).toBe(1);
    expect(streamTurnCostKey(1)).toBe(2);
  });

  it("rejects invalid turn indexes", () => {
    expect(streamTurnCostKey(undefined)).toBeNull();
    expect(streamTurnCostKey(-1)).toBeNull();
    expect(streamTurnCostKey(Number.NaN)).toBeNull();
  });
});

describe("TasksView empty state", () => {
  it("shows an actionable task-start canvas instead of a passive selection placeholder", async () => {
    const state = createRuntimeConsoleFixture({ session: localSession });
    render(withRuntimeConsole(<TasksView />, { state, actions: createRuntimeConsoleActions() }));

    await waitFor(() => {
      expect(screen.getByText("Start a task")).toBeTruthy();
    });

    expect(screen.queryByText("Select a task to inspect.")).toBeNull();
    // One button lives in the task sidebar; the second is the
    // main-pane start affordance for an empty task workspace.
    expect(screen.getAllByRole("button", { name: "New task" }).length).toBeGreaterThanOrEqual(2);
  });
});
