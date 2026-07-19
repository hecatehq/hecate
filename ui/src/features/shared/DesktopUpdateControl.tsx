// DesktopUpdateCenter owns the one desktop updater lifecycle for ConsoleShell.
// The compact status-bar button is deliberately the only in-page affordance;
// the macOS overlay titlebar stays a clean native drag surface.

import { useCallback, useRef, useState, type RefObject } from "react";
import { createPortal } from "react-dom";

import {
  type DesktopUpdate,
  type DesktopUpdateController,
  useDesktopUpdate,
} from "../../lib/desktop-update";
import { isTauriRuntime } from "../../lib/tauri";
import { Icon, Icons } from "./Icons";
import { Modal } from "./Overlays";

const MAX_RELEASE_NOTES_LENGTH = 12_000;
const UPDATE_DIALOG_ID = "hecate-desktop-update-dialog";

export function DesktopUpdateCenter() {
  const [detailsOpen, setDetailsOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const openDetails = useCallback(() => setDetailsOpen(true), []);
  const controller = useDesktopUpdate({ onManualCheck: openDetails });

  const closeDetails = useCallback(() => {
    controller.clearManualCheck();
    setDetailsOpen(false);
  }, [controller.clearManualCheck]);

  if (!isTauriRuntime()) return null;

  const status = describeUpdateStatus(controller);

  return (
    <>
      <div className="desktop-update-control">
        <button
          ref={triggerRef}
          aria-expanded={detailsOpen}
          aria-controls={detailsOpen ? UPDATE_DIALOG_ID : undefined}
          aria-haspopup="dialog"
          aria-label={status.ariaLabel}
          className="desktop-update-control__trigger"
          data-update-state={status.state}
          onClick={openDetails}
          title={status.title}
          type="button"
        >
          <Icon d={status.icon} size={13} />
          <span>{status.label}</span>
        </button>
      </div>
      {detailsOpen &&
        createPortal(
          <DesktopUpdateDetails
            controller={controller}
            onClose={closeDetails}
            returnFocusRef={triggerRef}
          />,
          document.body,
        )}
    </>
  );
}

function DesktopUpdateDetails({
  controller,
  onClose,
  returnFocusRef,
}: {
  controller: DesktopUpdateController;
  onClose: () => void;
  returnFocusRef: RefObject<HTMLButtonElement | null>;
}) {
  const checkButtonRef = useRef<HTMLButtonElement>(null);
  const installButtonRef = useRef<HTMLButtonElement>(null);
  const restartButtonRef = useRef<HTMLButtonElement>(null);
  const {
    checkNow,
    checking,
    dismissed,
    dismiss,
    installAndRestart,
    installFailure,
    installing,
    installPhase,
    lastCheckedAt,
    lastSuccessfulCheck,
    manualCheck,
    progress,
    retryRestart,
    restartReady,
    update,
  } = controller;
  const initialFocusRef = update
    ? installFailure === "restart"
      ? restartButtonRef
      : installButtonRef
    : checkButtonRef;
  const releaseNotes = boundedReleaseNotes(update);
  const status = updateDetailsStatus({
    update,
    checking,
    installing,
    installPhase,
    manualCheck,
    lastCheckedAt,
    lastSuccessfulCheck,
    installFailure,
    dismissed,
    restartReady,
  });
  const publishedDate = formatPublishedDate(update?.publishedAt);

  return (
    <Modal
      ariaLabel="Hecate desktop update"
      dismissible={!installing}
      footer={
        <div className="desktop-update-details__actions">
          {update ? (
            <>
              {installFailure === "restart" ? (
                <button
                  ref={restartButtonRef}
                  className="btn btn-primary btn-sm"
                  disabled={installing || checking}
                  onClick={() => void retryRestart()}
                  type="button"
                >
                  Retry restart
                </button>
              ) : (
                <button
                  ref={installButtonRef}
                  className="btn btn-primary btn-sm"
                  disabled={installing || checking}
                  onClick={() => void installAndRestart()}
                  type="button"
                >
                  {checking
                    ? "Checking…"
                    : installing
                      ? installPhase === "restarting"
                        ? "Restarting…"
                        : installPhase === "finishing"
                          ? "Finishing…"
                          : "Installing…"
                      : installFailure === "install"
                        ? "Try install again"
                        : "Install and restart"}
                </button>
              )}
              {!installing && installFailure !== "restart" && (
                <button
                  className="btn btn-ghost btn-sm"
                  onClick={() => {
                    dismiss();
                    onClose();
                  }}
                  type="button"
                >
                  Dismiss update
                </button>
              )}
            </>
          ) : (
            <button
              ref={checkButtonRef}
              className="btn btn-primary btn-sm"
              disabled={checking}
              onClick={() => void checkNow()}
              type="button"
            >
              {checking
                ? "Checking…"
                : manualCheck?.phase === "error"
                  ? "Try again"
                  : "Check for updates"}
            </button>
          )}
        </div>
      }
      // Both installing and a manual refresh can disable the currently
      // focused primary action. Re-evaluate dialog focus for either path.
      focusToken={installing || checking}
      id={UPDATE_DIALOG_ID}
      initialFocusRef={initialFocusRef}
      onClose={onClose}
      returnFocusRef={returnFocusRef}
      title="Hecate update"
      width={520}
    >
      <div className="desktop-update-details">
        <p
          aria-atomic="true"
          aria-live="polite"
          className="desktop-update-details__status"
          role="status"
        >
          {status}
        </p>

        {update && (
          <dl className="desktop-update-details__versions">
            <div>
              <dt>Current</dt>
              <dd>{update.currentVersion}</dd>
            </div>
            <div>
              <dt>Available</dt>
              <dd>{update.version}</dd>
            </div>
            {publishedDate && (
              <div>
                <dt>Published</dt>
                <dd>{publishedDate}</dd>
              </div>
            )}
          </dl>
        )}

        {installing && (
          <div className="desktop-update-details__progress">
            <progress
              aria-label="Update download progress"
              className="desktop-update-details__progress-bar"
              max={1}
              value={progress ?? undefined}
            />
            <span>
              {installPhase === "restarting"
                ? restartReady
                  ? "Restarting Hecate…"
                  : "Trying to restart to finish the update…"
                : installPhase === "finishing"
                  ? "Finishing installation…"
                  : progress !== null
                    ? `Downloading… ${Math.round(progress * 100)}%`
                    : "Downloading…"}
            </span>
          </div>
        )}

        {installFailure && (
          <p className="desktop-update-details__error" role="alert">
            {installFailure === "restart"
              ? restartReady
                ? "The update is installed, but Hecate could not restart. Restart it manually or try again."
                : "Hecate could not restart after the download completed. The install may already be staged; restart it manually or try again."
              : "Hecate could not finish the update. Try again, or open the app log from the Hecate menu for diagnostics."}
          </p>
        )}

        {releaseNotes && (
          <section
            aria-labelledby="desktop-update-release-notes"
            className="desktop-update-details__notes"
          >
            <h3 id="desktop-update-release-notes">Release notes from the published release</h3>
            {/* Release metadata is advisory. React renders it as text, never HTML/Markdown. */}
            <pre>{releaseNotes}</pre>
          </section>
        )}

        <p className="desktop-update-details__verification">
          Hecate verifies the downloaded package before installation. Your chats, settings, and
          local runtime data stay in place.
        </p>
      </div>
    </Modal>
  );
}

function describeUpdateStatus(controller: DesktopUpdateController): {
  state: "available" | "checking" | "error" | "idle" | "installing";
  label: string;
  title: string;
  ariaLabel?: string;
  icon: string | string[];
} {
  if (controller.installing) {
    return {
      state: "installing",
      label: "Updating…",
      title: "Hecate update in progress",
      icon: Icons.refresh,
    };
  }
  if (controller.manualCheck?.phase === "checking") {
    return {
      state: "checking",
      label: "Checking…",
      title: "Checking for Hecate updates",
      icon: Icons.refresh,
    };
  }
  if (controller.update) {
    return {
      state: "available",
      label: `Update ${controller.update.version}`,
      title: `Hecate ${controller.update.version} is available`,
      icon: Icons.refresh,
    };
  }
  if (controller.manualCheck?.phase === "error") {
    return {
      state: "error",
      label: "Updates",
      title: "The last update check failed — open update details to try again",
      ariaLabel: "Update check failed. Open update details to try again",
      icon: Icons.warning,
    };
  }
  return {
    state: "idle",
    label: "Updates",
    title: "Check for Hecate updates",
    icon: Icons.refresh,
  };
}

function updateDetailsStatus({
  update,
  checking,
  installing,
  installPhase,
  manualCheck,
  lastCheckedAt,
  lastSuccessfulCheck,
  dismissed,
  installFailure,
  restartReady,
}: Pick<
  DesktopUpdateController,
  | "update"
  | "checking"
  | "installing"
  | "installPhase"
  | "manualCheck"
  | "lastCheckedAt"
  | "lastSuccessfulCheck"
  | "dismissed"
  | "installFailure"
  | "restartReady"
>): string {
  if (installFailure === "restart") {
    return restartReady
      ? "The update is installed, but Hecate did not restart."
      : "Hecate did not restart after the download completed.";
  }
  if (installFailure === "install") return "The previous update attempt did not complete.";
  if (installing) {
    return installPhase === "restarting"
      ? restartReady
        ? "The update is installed. Restarting Hecate…"
        : "Trying to restart to finish the update…"
      : installPhase === "finishing"
        ? "The update has downloaded. Finishing installation…"
        : "Downloading the update…";
  }
  if (manualCheck?.phase === "checking" || checking) return "Checking for updates…";
  if (update) return `Hecate ${update.version} is ready to install.`;
  if (manualCheck?.phase === "error")
    return "Hecate could not check for updates. Try again when ready.";
  if (dismissed) {
    return "This update is dismissed for this app session. Check again to look for a newer release.";
  }
  if (manualCheck?.phase === "up-to-date" || lastSuccessfulCheck === "up-to-date") {
    return `Hecate is up to date. Last checked ${formatCheckTime(lastCheckedAt)}.`;
  }
  return "Hecate checks for updates automatically while this desktop app is open.";
}

function boundedReleaseNotes(update: DesktopUpdate | null): string | null {
  const notes = update?.notes?.trim();
  if (!notes) return null;
  const characters = Array.from(notes);
  if (characters.length <= MAX_RELEASE_NOTES_LENGTH) return notes;
  return `${characters.slice(0, MAX_RELEASE_NOTES_LENGTH).join("")}…`;
}

function formatPublishedDate(value: string | undefined): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(date);
}

function formatCheckTime(value: number | null): string {
  if (!value) return "recently";
  return new Intl.DateTimeFormat(undefined, {
    hour: "numeric",
    minute: "2-digit",
  }).format(new Date(value));
}
