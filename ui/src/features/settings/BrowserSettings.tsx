import { useEffect, useId, useRef, useState } from "react";

import { browserReadinessLabel } from "../../lib/browser-readiness";
import { SettingsSectionHeader } from "./SettingsSectionHeader";
import { useBrowserSettings } from "./useBrowserSettings";

export function BrowserSettings({ remoteRuntime }: { remoteRuntime: boolean }) {
  const browser = useBrowserSettings(remoteRuntime);
  const [candidateID, setCandidateID] = useState("");
  const pickerRef = useRef<HTMLSelectElement>(null);
  const disablingFocusRef = useRef<Element | null>(null);
  const id = useId();
  const data = browser.data;
  const environmentManaged = data?.source === "environment";
  const candidates = data?.candidates ?? [];
  const selectedCandidate = candidates.find((candidate) => candidate.id === candidateID);
  const alreadyEnabled = Boolean(
    selectedCandidate &&
    data?.source === "settings" &&
    data.readiness.available &&
    selectedCandidate.id === data.selected?.id,
  );
  const editingDisabled = browser.pending || browser.needsRefresh || !data || environmentManaged;

  useEffect(() => {
    if (browser.pending) return;
    const focused = disablingFocusRef.current;
    disablingFocusRef.current = null;
    if (focused && !focused.isConnected && document.activeElement === document.body) {
      pickerRef.current?.focus();
    }
  }, [browser.pending, data]);

  return (
    <section aria-label="Browser setup" style={{ marginBottom: 20 }}>
      <SettingsSectionHeader
        title="Browser setup"
        description="Choose an installed Chromium browser for supervised Hecate Chat and Task tools."
        meta={remoteRuntime ? "Local only" : browserReadinessLabel(data?.readiness)}
        actions={
          !remoteRuntime && (
            <button
              type="button"
              className="btn btn-ghost btn-sm"
              disabled={browser.pending}
              onClick={() => void browser.refresh()}
              title="Read installed browser metadata without starting a browser"
            >
              {browser.pending ? "Loading…" : "Refresh discovery"}
            </button>
          )
        }
      />
      <div
        className="card"
        aria-busy={browser.pending}
        style={{ padding: "14px 16px", color: "var(--t2)", fontSize: 12, lineHeight: 1.5 }}
      >
        {remoteRuntime ? (
          <p style={{ margin: 0 }}>
            Browser tools are available only in a local Hecate runtime. Open local Settings to
            configure a browser; this window cannot inspect or enable browsers on the controlled
            instance.
          </p>
        ) : (
          <div style={{ display: "grid", gap: 12 }}>
            {browser.error && (
              <p role="alert" style={{ margin: 0, color: "var(--red)" }}>
                {browser.error}
              </p>
            )}
            {data && (
              <>
                <div>
                  <p style={{ margin: 0 }}>{data.readiness.message}</p>
                  {data.readiness.operator_action && (
                    <p style={{ margin: "4px 0 0" }}>{data.readiness.operator_action}</p>
                  )}
                </div>
                {data.selected && (
                  <div>
                    <strong>Selected browser: {displayBrowserText(data.selected.name)}</strong>
                    <div
                      className="settings-machine-text"
                      style={{ fontFamily: "var(--font-mono)" }}
                    >
                      {displayBrowserText(data.selected.path)}
                    </div>
                  </div>
                )}
                {environmentManaged ? (
                  <p style={{ margin: 0 }}>
                    Managed by <code>HECATE_TASK_BROWSER_EXECUTABLE</code>. This override takes
                    precedence even when unavailable. Change or remove it and restart Hecate to edit
                    browser setup here.
                  </p>
                ) : (
                  <>
                    <div style={{ display: "grid", gap: 6 }}>
                      <label htmlFor={`${id}-browser`}>Installed browser</label>
                      <select
                        ref={pickerRef}
                        id={`${id}-browser`}
                        className="input"
                        style={{ minWidth: 0 }}
                        value={selectedCandidate ? candidateID : ""}
                        onChange={(event) => setCandidateID(event.target.value)}
                        disabled={editingDisabled || candidates.length === 0}
                        aria-describedby={`${id}-discovery ${id}-enable-help`}
                      >
                        <option value="">Choose a browser…</option>
                        {candidates.map((candidate) => (
                          <option key={candidate.id} value={candidate.id}>
                            {displayBrowserText(candidate.name)} —{" "}
                            {displayBrowserText(candidate.path)}
                          </option>
                        ))}
                      </select>
                      <div id={`${id}-discovery`} style={{ color: "var(--t3)" }}>
                        {candidates.length === 0
                          ? "No supported browser found. Install Chrome, Chromium, Edge, or Brave, then refresh discovery. A custom installation can use HECATE_TASK_BROWSER_EXECUTABLE."
                          : "Discovery reads installed app metadata without starting a browser. It does not verify the publisher or certify that an app is free of malware."}
                      </div>
                    </div>
                    <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
                      <button
                        type="button"
                        className="btn btn-primary btn-sm"
                        disabled={editingDisabled || !selectedCandidate || alreadyEnabled}
                        aria-describedby={`${id}-enable-help`}
                        onClick={() => {
                          if (selectedCandidate && !alreadyEnabled)
                            void browser.enable(selectedCandidate.id);
                        }}
                      >
                        {alreadyEnabled ? "Enabled" : "Enable browser"}
                      </button>
                      {data.source === "settings" && (
                        <button
                          type="button"
                          className="btn btn-ghost btn-sm"
                          disabled={editingDisabled}
                          onClick={() => {
                            disablingFocusRef.current = document.activeElement;
                            void browser.disable();
                          }}
                        >
                          Disable browser
                        </button>
                      )}
                    </div>
                    <p id={`${id}-enable-help`} style={{ margin: 0 }}>
                      {alreadyEnabled
                        ? "This browser is already enabled. Choose a different browser to change it. "
                        : "Select an installed browser, then enable it explicitly. "}
                      Enabling takes effect immediately but does not start the browser. A successful
                      approved browser call confirms it is working; no separate check is required.
                    </p>
                  </>
                )}
                <p style={{ margin: 0, color: "var(--t3)" }}>
                  Browser tools still need a Work policy with allowed origins and approval for every
                  call. Each call uses a fresh temporary profile, not your personal browser tabs or
                  signed-in sessions. Disabling blocks future calls; it does not stop a browser call
                  already in progress.
                </p>
                {data.backend === "memory" && data.source !== "environment" && (
                  <p style={{ margin: 0, color: "var(--t3)" }}>
                    This runtime uses memory storage. Your browser selection resets when Hecate
                    restarts.
                  </p>
                )}
              </>
            )}
            {!data && !browser.error && <p style={{ margin: 0 }}>Loading browser setup…</p>}
          </div>
        )}
      </div>
      <div role="status" aria-live="polite" className="sr-only">
        {browser.announcement}
      </div>
    </section>
  );
}

// Local paths are evidence, not markup or trusted visual labels.
function displayBrowserText(value: string): string {
  return Array.from(value, (char) => {
    const code = char.codePointAt(0) ?? 0;
    const unsafe =
      code < 0x20 ||
      (code >= 0x7f && code <= 0x9f) ||
      code === 0x061c ||
      code === 0x200e ||
      code === 0x200f ||
      (code >= 0x202a && code <= 0x202e) ||
      (code >= 0x2066 && code <= 0x2069);
    return unsafe ? `\\u${code.toString(16).padStart(4, "0")}` : char;
  }).join("");
}
