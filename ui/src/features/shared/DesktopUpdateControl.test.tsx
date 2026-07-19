import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { DesktopUpdateController } from "../../lib/desktop-update";
import { DesktopUpdateCenter } from "./DesktopUpdateControl";

const useDesktopUpdateMock = vi.fn();
vi.mock("../../lib/desktop-update", () => ({
  useDesktopUpdate: (options?: unknown) => useDesktopUpdateMock(options),
}));

function enterTauriRuntime() {
  (globalThis as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__ = {};
}

function exitTauriRuntime() {
  delete (globalThis as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
}

function controllerFixture(
  overrides: Partial<DesktopUpdateController> = {},
): DesktopUpdateController {
  return {
    update: null,
    checking: false,
    manualCheck: null,
    lastCheckedAt: null,
    lastSuccessfulCheck: null,
    dismissed: false,
    installing: false,
    installPhase: "idle",
    progress: null,
    installFailure: null,
    restartReady: false,
    dismiss: vi.fn(),
    clearManualCheck: vi.fn(),
    installAndRestart: vi.fn().mockResolvedValue(undefined),
    retryRestart: vi.fn().mockResolvedValue(undefined),
    checkNow: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
}

beforeEach(() => {
  useDesktopUpdateMock.mockReset();
  exitTauriRuntime();
});

afterEach(() => {
  exitTauriRuntime();
});

describe("DesktopUpdateCenter", () => {
  it("does not render in the browser runtime", () => {
    useDesktopUpdateMock.mockReturnValue(controllerFixture());

    render(<DesktopUpdateCenter />);

    expect(screen.queryByRole("button", { name: "Updates" })).toBeNull();
  });

  it("opens a portal-mounted dialog with literal release notes and returns focus", async () => {
    enterTauriRuntime();
    useDesktopUpdateMock.mockReturnValue(
      controllerFixture({
        update: {
          currentVersion: "0.3.0-alpha.1",
          version: "0.3.0-alpha.2",
          publishedAt: "2026-07-19T08:30:00Z",
          notes:
            '<img data-unsafe="release-note-image" src="https://invalid.example"> **Plain text**',
        },
      }),
    );

    render(<DesktopUpdateCenter />);
    const trigger = screen.getByRole("button", { name: "Update 0.3.0-alpha.2" });
    trigger.focus();
    fireEvent.click(trigger);

    const dialog = await screen.findByRole("dialog", { name: "Hecate desktop update" });
    expect(dialog).toHaveAttribute("id", "hecate-desktop-update-dialog");
    expect(trigger).toHaveAttribute("aria-controls", "hecate-desktop-update-dialog");
    expect(dialog).toHaveTextContent("Current");
    expect(dialog).toHaveTextContent("0.3.0-alpha.1");
    expect(dialog).toHaveTextContent("Available");
    expect(dialog).toHaveTextContent("0.3.0-alpha.2");
    expect(dialog).toHaveTextContent('<img data-unsafe="release-note-image"');
    expect(document.querySelector("[data-unsafe='release-note-image']")).toBeNull();
    expect(dialog.parentElement).toHaveStyle({ zIndex: "1100" });
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Install and restart" })).toHaveFocus(),
    );

    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("dialog", { name: "Hecate desktop update" })).toBeNull();
    await waitFor(() => expect(trigger).toHaveFocus());
  });

  it("shows a dismissed update as dismissed rather than up to date", async () => {
    enterTauriRuntime();
    useDesktopUpdateMock.mockReturnValue(
      controllerFixture({
        dismissed: true,
        lastCheckedAt: Date.now(),
        lastSuccessfulCheck: "update",
      }),
    );

    render(<DesktopUpdateCenter />);
    fireEvent.click(screen.getByRole("button", { name: "Updates" }));

    expect(await screen.findByText(/dismissed for this app session/i)).toBeInTheDocument();
    expect(screen.queryByText(/hecate is up to date/i)).toBeNull();
  });

  it("renders checking state safely while a known update is refreshed", async () => {
    enterTauriRuntime();
    useDesktopUpdateMock.mockReturnValue(
      controllerFixture({
        checking: true,
        manualCheck: { id: 3, phase: "checking" },
        update: {
          currentVersion: "0.3.0-alpha.1",
          version: "0.3.0-alpha.2",
        },
      }),
    );

    render(<DesktopUpdateCenter />);
    expect(screen.getByRole("button", { name: "Checking…" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Checking…" }));

    expect(await screen.findByText("Checking for updates…")).toBeInTheDocument();
    expect(
      screen
        .getAllByRole("button", { name: "Checking…" })
        .some((button) => button.hasAttribute("disabled")),
    ).toBe(true);
  });

  it("moves focus to the dialog when an install disables the focused action", async () => {
    enterTauriRuntime();
    const installAndRestart = vi.fn().mockResolvedValue(undefined);
    const update = {
      currentVersion: "0.3.0-alpha.1",
      version: "0.3.0-alpha.2",
    };
    useDesktopUpdateMock.mockReturnValue(controllerFixture({ installAndRestart, update }));

    const { rerender } = render(<DesktopUpdateCenter />);
    fireEvent.click(screen.getByRole("button", { name: "Update 0.3.0-alpha.2" }));
    const install = await screen.findByRole("button", { name: "Install and restart" });
    await waitFor(() => expect(install).toHaveFocus());
    fireEvent.click(install);
    expect(installAndRestart).toHaveBeenCalledTimes(1);

    useDesktopUpdateMock.mockReturnValue(
      controllerFixture({
        installing: true,
        installPhase: "downloading",
        installAndRestart,
        update,
      }),
    );
    rerender(<DesktopUpdateCenter />);

    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "Hecate desktop update" })).toHaveFocus(),
    );
  });

  it("moves focus when a refresh disables the focused install action", async () => {
    enterTauriRuntime();
    const update = {
      currentVersion: "0.3.0-alpha.1",
      version: "0.3.0-alpha.2",
    };
    useDesktopUpdateMock.mockReturnValue(controllerFixture({ update }));

    const { rerender } = render(<DesktopUpdateCenter />);
    fireEvent.click(screen.getByRole("button", { name: "Update 0.3.0-alpha.2" }));
    const install = await screen.findByRole("button", { name: "Install and restart" });
    await waitFor(() => expect(install).toHaveFocus());

    useDesktopUpdateMock.mockReturnValue(controllerFixture({ checking: true, update }));
    rerender(<DesktopUpdateCenter />);

    await waitFor(() => expect(screen.getByRole("button", { name: "Close" })).toHaveFocus());
  });

  it("gives a failed update check an explicit accessible trigger name", () => {
    enterTauriRuntime();
    useDesktopUpdateMock.mockReturnValue(
      controllerFixture({ manualCheck: { id: 4, phase: "error" } }),
    );

    render(<DesktopUpdateCenter />);

    expect(
      screen.getByRole("button", { name: "Update check failed. Open update details to try again" }),
    ).toBeInTheDocument();
  });

  it("shows determinate download progress", async () => {
    enterTauriRuntime();
    useDesktopUpdateMock.mockReturnValue(
      controllerFixture({
        installing: true,
        installPhase: "downloading",
        progress: 0.5,
        update: {
          currentVersion: "0.3.0-alpha.1",
          version: "0.3.0-alpha.2",
        },
      }),
    );

    render(<DesktopUpdateCenter />);
    fireEvent.click(screen.getByRole("button", { name: "Updating…" }));

    const progress = await screen.findByLabelText("Update download progress");
    expect(progress).toHaveAttribute("value", "0.5");
    expect(screen.getByText("Downloading… 50%")).toBeInTheDocument();
  });

  it("does not claim an update is installed while a stalled install is only trying to restart", async () => {
    enterTauriRuntime();
    useDesktopUpdateMock.mockReturnValue(
      controllerFixture({
        installing: true,
        installPhase: "restarting",
        restartReady: false,
        update: {
          currentVersion: "0.3.0-alpha.1",
          version: "0.3.0-alpha.2",
        },
      }),
    );

    render(<DesktopUpdateCenter />);
    fireEvent.click(screen.getByRole("button", { name: "Updating…" }));

    expect(screen.getAllByText("Trying to restart to finish the update…")).toHaveLength(2);
    expect(screen.queryByText("Restarting Hecate…")).toBeNull();
    expect(screen.queryByText("The update is installed. Restarting Hecate…")).toBeNull();
  });

  it("offers a restart-only retry after an installed update cannot relaunch", async () => {
    enterTauriRuntime();
    const retryRestart = vi.fn().mockResolvedValue(undefined);
    useDesktopUpdateMock.mockReturnValue(
      controllerFixture({
        installFailure: "restart",
        restartReady: true,
        retryRestart,
        update: {
          currentVersion: "0.3.0-alpha.1",
          version: "0.3.0-alpha.2",
        },
      }),
    );

    render(<DesktopUpdateCenter />);
    fireEvent.click(screen.getByRole("button", { name: "Update 0.3.0-alpha.2" }));

    expect(
      await screen.findByText(/update is installed, but hecate could not restart/i),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry restart" }));
    expect(retryRestart).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("button", { name: "Try install again" })).toBeNull();
  });
});
