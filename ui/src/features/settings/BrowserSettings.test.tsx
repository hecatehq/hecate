import { StrictMode } from "react";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { SettingsProvider, useSettings } from "../../app/state/settings";
import { ApiError, disableBrowser, enableBrowser, getBrowserSettings } from "../../lib/api";
import type { BrowserSettingsResponse } from "../../types/browser";
import { BrowserSettings } from "./BrowserSettings";

vi.mock("../../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/api")>()),
  getBrowserSettings: vi.fn(),
  enableBrowser: vi.fn(),
  disableBrowser: vi.fn(),
}));

const candidate = {
  id: "candidate-chrome",
  name: "Google Chrome",
  path: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
};

function response(
  overrides: Partial<BrowserSettingsResponse["data"]> = {},
): BrowserSettingsResponse {
  return {
    object: "browser_settings",
    data: {
      readiness: { available: false, status: "not_configured", message: "No browser selected." },
      source: "none",
      candidates: [candidate],
      backend: "memory",
      ...overrides,
    },
  };
}

function configured(status = "configured"): BrowserSettingsResponse {
  return response({
    source: "settings",
    selected: candidate,
    readiness: {
      available: true,
      status,
      message:
        status === "working" ? "A browser call succeeded." : "Browser selected; not yet used.",
    },
  });
}

function SharedReadiness() {
  return (
    <output aria-label="Shared readiness">
      {useSettings().state.config?.browser_evidence?.status}
    </output>
  );
}

function view(remoteRuntime = false) {
  return (
    <SettingsProvider
      initialState={{ config: { backend: "memory", providers: [], policy_rules: [], events: [] } }}
    >
      <BrowserSettings remoteRuntime={remoteRuntime} />
      <SharedReadiness />
    </SettingsProvider>
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(getBrowserSettings).mockResolvedValue(response());
  vi.mocked(enableBrowser).mockResolvedValue(configured());
  vi.mocked(disableBrowser).mockResolvedValue(response());
});

describe("BrowserSettings", () => {
  it("discovers passively and requires an explicit selection and enable action", async () => {
    const user = userEvent.setup();
    render(view());
    const picker = await screen.findByRole("combobox", { name: "Installed browser" });
    expect(picker).toHaveValue("");
    expect(screen.getByRole("button", { name: "Enable browser" })).toBeDisabled();
    expect(enableBrowser).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: /check/i })).toBeNull();
    expect(screen.getByText(/does not verify the publisher/)).toBeVisible();
    expect(screen.getByText(/selection resets when Hecate restarts/)).toBeVisible();

    await user.selectOptions(picker, candidate.id);
    expect(enableBrowser).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Enable browser" }));
    expect(enableBrowser).toHaveBeenCalledWith(candidate.id, expect.any(AbortSignal));
    expect(await screen.findByText("Configured (unverified)")).toBeVisible();
    expect(screen.getByLabelText("Shared readiness")).toHaveTextContent("configured");
    expect(screen.getByText(/no separate check is required/)).toBeVisible();
    expect(screen.getByText(/not your personal browser tabs or signed-in sessions/)).toBeVisible();
    expect(
      within(screen.getByRole("region", { name: "Browser setup" })).getByRole("status"),
    ).toHaveTextContent("Browser settings saved.");

    await user.click(screen.getByRole("button", { name: "Disable browser" }));
    expect(disableBrowser).toHaveBeenCalledWith(expect.any(AbortSignal));
    expect(await screen.findByText("Not configured")).toBeVisible();
    expect(screen.getByLabelText("Shared readiness")).toHaveTextContent("not_configured");
    expect(picker).toHaveFocus();
  });

  it("refreshes working evidence without a write or a check", async () => {
    const user = userEvent.setup();
    vi.mocked(getBrowserSettings)
      .mockResolvedValueOnce(configured())
      .mockResolvedValueOnce(configured("working"));
    render(view());
    await screen.findByText("Configured (unverified)");
    await user.click(screen.getByRole("button", { name: "Refresh discovery" }));
    expect(await screen.findByText("Working")).toBeVisible();
    expect(enableBrowser).not.toHaveBeenCalled();
    expect(screen.getByLabelText("Shared readiness")).toHaveTextContent("working");
  });

  it("prevents re-enabling the same working browser but permits a different selection", async () => {
    const user = userEvent.setup();
    const other = { id: "candidate-edge", name: "Microsoft Edge", path: "/apps/edge" };
    vi.mocked(getBrowserSettings).mockResolvedValue({
      ...configured("working"),
      data: { ...configured("working").data, candidates: [candidate, other] },
    });
    render(view());
    const picker = await screen.findByRole("combobox");
    await user.selectOptions(picker, candidate.id);
    expect(screen.getByRole("button", { name: "Enabled" })).toBeDisabled();
    expect(screen.getByText(/This browser is already enabled/)).toBeVisible();
    expect(screen.getByText("Working")).toBeVisible();
    expect(enableBrowser).not.toHaveBeenCalled();
    await user.selectOptions(picker, other.id);
    expect(screen.getByRole("button", { name: "Enable browser" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "Enable browser" }));
    expect(enableBrowser).toHaveBeenCalledWith(other.id, expect.any(AbortSignal));
  });

  it("allows explicitly reselecting a saved browser that is no longer available", async () => {
    const user = userEvent.setup();
    vi.mocked(getBrowserSettings).mockResolvedValue(
      response({
        source: "settings",
        selected: candidate,
        readiness: {
          available: false,
          status: "unavailable",
          message: "The selected browser has changed.",
        },
      }),
    );
    render(view());
    await user.selectOptions(await screen.findByRole("combobox"), candidate.id);
    expect(screen.getByRole("button", { name: "Enable browser" })).toBeEnabled();
  });

  it.each([true, false])(
    "keeps environment setup read-only even when available=%s",
    async (available) => {
      vi.mocked(getBrowserSettings).mockResolvedValue(
        response({
          source: "environment",
          selected: candidate,
          readiness: {
            available,
            status: available ? "configured" : "unavailable",
            message: "Environment browser setting.",
          },
        }),
      );
      render(view());
      expect(
        await screen.findByText(/This override takes precedence even when unavailable/),
      ).toBeVisible();
      expect(screen.queryByRole("button", { name: "Enable browser" })).toBeNull();
      expect(screen.queryByRole("button", { name: "Disable browser" })).toBeNull();
      expect(screen.queryByRole("combobox")).toBeNull();
    },
  );

  it("does not request host-local discovery in a remote runtime", () => {
    render(view(true));
    expect(
      screen.getByText(/Browser tools are available only in a local Hecate runtime/),
    ).toBeVisible();
    expect(getBrowserSettings).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "Refresh discovery" })).toBeNull();
  });

  it("explains how to repair a host-local access denial", async () => {
    vi.mocked(getBrowserSettings).mockRejectedValue(new ApiError("raw error", 403, "forbidden"));
    render(view());
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Open Hecate on the runtime's machine without a reverse proxy",
    );
    expect(screen.queryByText("raw error")).toBeNull();
    expect(screen.queryByRole("button", { name: "Enable browser" })).toBeNull();
  });

  it("shows installation guidance without automatically selecting or enabling anything", async () => {
    vi.mocked(getBrowserSettings).mockResolvedValue(response({ candidates: [] }));
    render(view());
    expect(await screen.findByText(/No supported browser found/)).toBeVisible();
    expect(screen.getByRole("combobox")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Enable browser" })).toBeDisabled();
  });

  it("requires a passive refresh after an ambiguous write without leaking raw errors", async () => {
    const user = userEvent.setup();
    vi.mocked(enableBrowser).mockRejectedValue(new Error("secret-path-or-proxy-body"));
    render(view());
    await user.selectOptions(await screen.findByRole("combobox"), candidate.id);
    await user.click(screen.getByRole("button", { name: "Enable browser" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Could not confirm the browser setting",
    );
    expect(screen.queryByText(/secret-path-or-proxy-body/)).toBeNull();
    expect(screen.getByRole("button", { name: "Enable browser" })).toBeDisabled();
    vi.mocked(getBrowserSettings).mockResolvedValue(configured());
    await user.click(screen.getByRole("button", { name: "Refresh discovery" }));
    await screen.findByText("Configured (unverified)");
    expect(enableBrowser).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "Disable browser" })).toBeEnabled();
  });

  it("aborts discovery on unmount and ignores an old StrictMode response", async () => {
    let settleOld!: (value: BrowserSettingsResponse) => void;
    vi.mocked(getBrowserSettings)
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            settleOld = resolve;
          }),
      )
      .mockResolvedValueOnce(configured("working"));
    const { unmount } = render(<StrictMode>{view()}</StrictMode>);
    await screen.findByText("Working");
    expect(vi.mocked(getBrowserSettings).mock.calls[0][0]?.aborted).toBe(true);
    await act(async () => settleOld(response()));
    expect(screen.getByText("Working")).toBeVisible();
    expect(screen.getByLabelText("Shared readiness")).toHaveTextContent("working");
    unmount();
  });

  it("ignores an in-flight mutation after the view changes to a remote runtime", async () => {
    const user = userEvent.setup();
    let settle!: (value: BrowserSettingsResponse) => void;
    vi.mocked(enableBrowser).mockImplementation(
      () =>
        new Promise((resolve) => {
          settle = resolve;
        }),
    );
    const { rerender } = render(view());
    await user.selectOptions(await screen.findByRole("combobox"), candidate.id);
    await user.click(screen.getByRole("button", { name: "Enable browser" }));
    expect(screen.getByRole("button", { name: "Enable browser" })).toBeDisabled();
    rerender(view(true));
    expect(vi.mocked(enableBrowser).mock.calls[0][1]?.aborted).toBe(true);
    await act(async () => settle(configured()));
    expect(screen.queryByText("Configured (unverified)")).toBeNull();
    expect(screen.getByLabelText("Shared readiness").textContent).toBe("not_configured");
  });

  it("escapes control characters in browser paths and accessible option labels", async () => {
    vi.mocked(getBrowserSettings).mockResolvedValue(
      response({ candidates: [{ ...candidate, path: "/apps/\u202eevil\nchrome" }] }),
    );
    render(view());
    const picker = await screen.findByRole("combobox");
    expect(
      within(picker).getByRole("option", {
        name: "Google Chrome — /apps/\\u202eevil\\u000achrome",
      }),
    ).toBeTruthy();
  });

  it("offers refresh after a failed read and ignores that read after unmount", async () => {
    vi.mocked(getBrowserSettings).mockRejectedValueOnce(new Error("raw diagnostic"));
    const { unmount } = render(view());
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not load browser setup");
    expect(screen.getByRole("button", { name: "Refresh discovery" })).toBeEnabled();
    unmount();
    await waitFor(() => expect(enableBrowser).not.toHaveBeenCalled());
  });
});
