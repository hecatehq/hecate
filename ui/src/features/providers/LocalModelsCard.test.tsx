import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { LocalModelsCard } from "./LocalModelsCard";
import {
  getLocalModelsInstalled,
  getLocalModelsRuntime,
} from "../../lib/api";
import type {
  LocalModelInstalledResponse,
  LocalModelRuntimeResponse,
} from "../../types/runtime";

vi.mock("../../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...actual,
    getLocalModelsInstalled: vi.fn(),
    getLocalModelsRuntime: vi.fn(),
    // The card opens the SlideOver which eagerly fetches the
    // catalog. We stub it as empty so the SlideOver renders without
    // network noise — the SlideOver gets its own test file.
    getLocalModelsCatalog: vi.fn(async () => ({
      object: "local_models.catalog",
      data: [],
    })),
  };
});

const mockedRuntime = vi.mocked(getLocalModelsRuntime);
const mockedInstalled = vi.mocked(getLocalModelsInstalled);

function runtime(overrides: Partial<LocalModelRuntimeResponse> = {}): LocalModelRuntimeResponse {
  return {
    object: "local_models.runtime",
    state: "idle",
    available: true,
    availability: { available: true, binary_path: "/fake/llama-server" },
    ...overrides,
  };
}

function installed(rows: LocalModelInstalledResponse["data"] = []): LocalModelInstalledResponse {
  return { object: "local_models.installed", data: rows };
}

beforeEach(() => {
  // Real timers — the card's setInterval poll runs every 8s, well
  // above any single test's lifetime, so fake timers add nothing and
  // would block the initial fetch promises from resolving.
  mockedRuntime.mockReset();
  mockedInstalled.mockReset();
});

describe("LocalModelsCard", () => {
  it("renders the loading shell on first paint", () => {
    mockedRuntime.mockReturnValue(new Promise(() => { /* never resolves */ }));
    mockedInstalled.mockReturnValue(new Promise(() => { /* never resolves */ }));
    render(<LocalModelsCard />);
    expect(screen.getByText(/Loading bundled model runtime/i)).toBeInTheDocument();
  });

  it("renders the dormant tile when the binary isn't bundled", async () => {
    mockedRuntime.mockResolvedValue(
      runtime({
        available: false,
        reason: "binary_not_found",
        availability: { available: false, reason: "binary_not_found" },
        state: "idle",
      }),
    );
    mockedInstalled.mockResolvedValue(installed());

    render(<LocalModelsCard />);

    const card = await screen.findByTestId("local-models-card-dormant");
    // The dormant copy must reach the operator without any
    // interaction — they shouldn't have to click into a SlideOver to
    // discover that local models aren't available in their build.
    expect(card).toHaveTextContent(/Bundled model runtime/i);
    expect(card).toHaveTextContent(/Not bundled/i);
    expect(card).toHaveTextContent(/desktop app/i);
    // The Manage button is not rendered on the dormant path —
    // there's nothing to manage.
    expect(screen.queryByTestId("local-models-card-manage")).not.toBeInTheDocument();
  });

  it("maps each dormant reason to a labelled badge", async () => {
    // Two reasons share the dormant path (binary_not_found,
    // binary_not_executable); the third (flag_off) is theoretical
    // but documented. Cover all three so a future reason addition
    // surfaces here as a test failure rather than a silent
    // "Unavailable" fallback.
    const cases: Array<{ reason: string; label: RegExp }> = [
      { reason: "binary_not_found", label: /Not bundled/i },
      { reason: "binary_not_executable", label: /Binary unusable/i },
      { reason: "flag_off", label: /Disabled/i },
      { reason: "made_up_reason", label: /Unavailable/i },
    ];
    for (const { reason, label } of cases) {
      mockedRuntime.mockResolvedValueOnce(
        runtime({
          available: false,
          reason,
          availability: { available: false, reason },
        }),
      );
      mockedInstalled.mockResolvedValueOnce(installed());
      const { unmount } = render(<LocalModelsCard />);
      const card = await screen.findByTestId("local-models-card-dormant");
      expect(card).toHaveTextContent(label);
      unmount();
    }
  });

  it("renders idle state with installed count when feature is available", async () => {
    mockedRuntime.mockResolvedValue(runtime({ state: "idle" }));
    mockedInstalled.mockResolvedValue(
      installed([
        { id: "qwen-tiny", display_name: "Qwen Tiny", file_path: "models/qwen-tiny.gguf" },
        { id: "llama-1b", display_name: "Llama 1B", file_path: "models/llama-1b.gguf" },
      ]),
    );

    render(<LocalModelsCard />);
    const card = await screen.findByTestId("local-models-card-active");
    expect(card).toHaveTextContent(/Bundled model runtime/i);
    expect(card).toHaveTextContent(/Idle/i);
    // Strong assertion on the count so a regression in the
    // "X installed" string is caught here.
    expect(card).toHaveTextContent("2");
    expect(card).toHaveTextContent(/installed/i);
  });

  it("highlights the active model when the runtime is running", async () => {
    mockedRuntime.mockResolvedValue(
      runtime({
        state: "running",
        active: {
          state: "running",
          active_model_id: "qwen-tiny",
          port: 8765,
          pid: 1234,
        },
      }),
    );
    mockedInstalled.mockResolvedValue(
      installed([
        { id: "qwen-tiny", display_name: "Qwen Tiny", file_path: "models/qwen-tiny.gguf" },
      ]),
    );

    render(<LocalModelsCard />);
    const card = await screen.findByTestId("local-models-card-active");
    expect(card).toHaveTextContent(/Running/i);
    // The "running" pill must reference the active model so the
    // operator can confirm which model is loaded without opening
    // the SlideOver.
    expect(card).toHaveTextContent(/Qwen Tiny running/i);
  });

  it("renders 'No models installed yet' when the registry is empty", async () => {
    mockedRuntime.mockResolvedValue(runtime({ state: "idle" }));
    mockedInstalled.mockResolvedValue(installed());
    render(<LocalModelsCard />);
    const card = await screen.findByTestId("local-models-card-active");
    expect(card).toHaveTextContent(/No models installed yet/i);
  });

  it("surfaces the last error when the runtime is in the failed state", async () => {
    mockedRuntime.mockResolvedValue(
      runtime({
        state: "failed",
        active: {
          state: "failed",
          active_model_id: "qwen-tiny",
          last_error: "child exited with code 134",
          last_error_at: "2026-05-15T10:00:00Z",
        },
      }),
    );
    mockedInstalled.mockResolvedValue(installed());

    render(<LocalModelsCard />);
    const card = await screen.findByTestId("local-models-card-active");
    expect(card).toHaveTextContent(/Error/i);
    expect(card).toHaveTextContent(/child exited with code 134/i);
  });

  it("opens the SlideOver when 'Manage models' is clicked", async () => {
    mockedRuntime.mockResolvedValue(runtime({ state: "idle" }));
    mockedInstalled.mockResolvedValue(installed());

    const user = userEvent.setup();
    render(<LocalModelsCard />);

    const manage = await screen.findByTestId("local-models-card-manage");
    await user.click(manage);
    // The SlideOver's "Runtime" / "Installed" section headers and
    // its footer Done button are unique to the expanded surface;
    // the card alone never renders them. Asserting one of those is
    // a sharper signal than the title (which both the card and
    // SlideOver contain).
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Done$/i })).toBeInTheDocument(),
    );
    expect(screen.getByText(/^Runtime$/)).toBeInTheDocument();
  });

  it("treats an /installed API error as the dormant path", async () => {
    // If /runtime succeeds but /installed throws (gateway hiccup,
    // not a build dormancy issue), the card should still render
    // the idle state with zero installed — not the dormant one.
    // The endpoint is the side that 503s when the feature is
    // disabled; /installed throwing for any other reason is
    // transient.
    mockedRuntime.mockResolvedValue(runtime({ state: "idle" }));
    mockedInstalled.mockRejectedValue(new Error("transient gateway error"));

    render(<LocalModelsCard />);
    const card = await screen.findByTestId("local-models-card-active");
    expect(card).toHaveTextContent(/No models installed yet/i);
  });
});
