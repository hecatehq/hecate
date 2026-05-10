// UpdateBanner surfaces a desktop-app update when the Tauri updater
// detects a newer version on app start. Inert outside Tauri.
// "Install and Restart" downloads + installs the new bundle and
// relaunches; the plugin handles the relaunch.

import { useDesktopUpdate } from "../../lib/desktop-update";

export function UpdateBanner() {
  const { update, installing, dismiss, installAndRestart } = useDesktopUpdate();
  if (!update) return null;
  return (
    <div className="page-banner page-banner--update" role="status" aria-live="polite">
      <span>Hecate {update.version} is available.</span>
      <span className="page-banner__actions">
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
