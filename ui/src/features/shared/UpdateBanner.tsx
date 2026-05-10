// UpdateBanner surfaces a desktop-app update when the Tauri updater
// detects a newer version on app start. Inert outside Tauri.
// "Install and Restart" downloads + installs the new bundle and
// relaunches; the plugin handles the relaunch.

import { useDesktopUpdate } from "../../lib/desktop-update";

export function UpdateBanner() {
  const { update, installing, progress, dismiss, installAndRestart } = useDesktopUpdate();
  if (!update) return null;
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
