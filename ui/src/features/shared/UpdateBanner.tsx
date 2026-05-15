// UpdateBanner surfaces a desktop-app update when the Tauri updater
// detects a newer version, and gives transient feedback after a
// manual "Check for Updates…" trigger so the user can tell their
// click did something. Inert outside Tauri.
// "Install and Restart" downloads + installs the new bundle and
// relaunches; the plugin handles the relaunch.

import { useDesktopUpdate } from "../../lib/desktop-update";

export function UpdateBanner() {
  const { update, installing, progress, lastCheckResult, dismiss, installAndRestart } =
    useDesktopUpdate();

  if (update) {
    return (
      <div className="page-banner page-banner--update" role="status" aria-live="polite">
        <span>
          Hecate {update.version} is available.
          {installing && (
            <>
              {" "}
              <span className="page-banner__progress-text">
                {progress !== null
                  ? `Downloading… ${Math.round(progress * 100)}%`
                  : "Downloading…"}
              </span>
            </>
          )}
        </span>
        <span className="page-banner__actions">
          {installing && (
            // Indeterminate <progress> when total is unknown
            // (Started event missing); otherwise determinate.
            <progress
              className="page-banner__progress"
              value={progress !== null ? progress : undefined}
              max={1}
              aria-label="Update download progress"
            />
          )}
          <button
            className="btn btn-primary btn-sm"
            disabled={installing}
            onClick={() => void installAndRestart()}>
            {installing ? "Installing…" : "Install and Restart"}
          </button>
          <button className="btn btn-ghost btn-sm" onClick={dismiss} disabled={installing}>
            Dismiss
          </button>
        </span>
      </div>
    );
  }

  // Transient feedback after a manual menu-driven check that didn't
  // surface an update. Auto-clears in the hook after a few seconds —
  // no dismiss button on purpose, the banner is short-lived.
  if (lastCheckResult === "up-to-date") {
    return (
      <div className="page-banner page-banner--update" role="status" aria-live="polite">
        <span>Hecate is up to date.</span>
      </div>
    );
  }

  if (lastCheckResult === "error") {
    return (
      <div className="page-banner page-banner--error" role="status" aria-live="polite">
        <span>Couldn't check for updates — see console for details.</span>
      </div>
    );
  }

  return null;
}
