import { useCallback, useEffect, useRef, useState } from "react";

import {
  canUseDesktopCloudConnection,
  getDesktopCloudConnectionStatus,
  getDesktopCloudRuntimeConnections,
  openDesktopCloudRuntime,
  signInDesktopCloudAccount,
  signOutDesktopCloudConnection,
  startDesktopCloudConnection,
  startDesktopCloudRuntime,
  stopDesktopCloudConnection,
  type DesktopCloudConnectionStatus,
  type DesktopCloudRuntimeConnection,
} from "../../lib/cloud-connection";
import { formatRelativeTime } from "../../lib/runtime-utils";
import { Badge, Icon, Icons, InlineError, Toggle } from "../shared/ui";
import { SettingsSectionHeader as SectionHeader } from "./SettingsSectionHeader";

export function DesktopCloudSettings() {
  if (!canUseDesktopCloudConnection()) return null;
  return <DesktopCloudConnectionSettings />;
}

function DesktopCloudConnectionSettings() {
  const [status, setStatus] = useState<DesktopCloudConnectionStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<"signin" | "connect" | "disconnect" | "signout" | null>(null);
  const [error, setError] = useState("");
  const statusRequestGenerationRef = useRef(0);
  const statusRequestInFlightRef = useRef<Promise<void> | null>(null);
  const statusMutationInFlightRef = useRef(false);

  const loadStatus = useCallback((showLoading = false) => {
    if (statusMutationInFlightRef.current) return Promise.resolve();
    if (statusRequestInFlightRef.current) return statusRequestInFlightRef.current;
    const requestGeneration = ++statusRequestGenerationRef.current;
    if (showLoading) setLoading(true);
    const request = getDesktopCloudConnectionStatus()
      .then((nextStatus) => {
        if (requestGeneration !== statusRequestGenerationRef.current) return;
        setStatus(nextStatus);
        setError("");
      })
      .catch((err) => {
        if (requestGeneration !== statusRequestGenerationRef.current) return;
        setError(err instanceof Error ? err.message : "Failed to read Hecate Cloud status.");
      })
      .finally(() => {
        if (showLoading && requestGeneration === statusRequestGenerationRef.current) {
          setLoading(false);
        }
        if (statusRequestInFlightRef.current === request) statusRequestInFlightRef.current = null;
      });
    statusRequestInFlightRef.current = request;
    return request;
  }, []);

  useEffect(() => {
    void loadStatus(true);
    return () => {
      statusRequestGenerationRef.current += 1;
      statusRequestInFlightRef.current = null;
    };
  }, [loadStatus]);

  useEffect(() => {
    if (
      !status ||
      (!status.restoring && !["authorizing", "connecting", "reconnecting"].includes(status.phase))
    )
      return;
    const interval = window.setInterval(() => {
      if (document.visibilityState !== "visible") return;
      void loadStatus();
    }, 1000);
    return () => window.clearInterval(interval);
  }, [loadStatus, status?.phase, status?.restoring]);

  function beginStatusMutation(action: "signin" | "connect" | "disconnect" | "signout") {
    statusMutationInFlightRef.current = true;
    statusRequestGenerationRef.current += 1;
    statusRequestInFlightRef.current = null;
    setBusy(action);
    setError("");
  }

  function finishStatusMutation() {
    statusMutationInFlightRef.current = false;
    setBusy(null);
  }

  async function signIn() {
    beginStatusMutation("signin");
    try {
      setStatus(await signInDesktopCloudAccount());
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not sign in to Hecate Cloud.");
    } finally {
      finishStatusMutation();
    }
  }

  async function connect() {
    beginStatusMutation("connect");
    try {
      setStatus(await startDesktopCloudConnection());
    } catch (err) {
      setError(err instanceof Error ? err.message : "Remote access could not be enabled.");
    } finally {
      finishStatusMutation();
    }
  }

  async function disconnect() {
    beginStatusMutation("disconnect");
    try {
      setStatus(await stopDesktopCloudConnection());
    } catch (err) {
      setError(err instanceof Error ? err.message : "Remote access could not be disabled.");
    } finally {
      finishStatusMutation();
    }
  }

  async function signOut() {
    beginStatusMutation("signout");
    try {
      setStatus(await signOutDesktopCloudConnection());
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not sign out of Hecate Cloud.");
    } finally {
      finishStatusMutation();
    }
  }

  const signedIn = Boolean(status?.signed_in);
  const authorizing = Boolean(status?.authorizing);
  const accessOn = Boolean(status?.auto_start_enabled);
  const unavailable = !loading && (!status || !status.available);
  const actionDisabled = loading || status?.restoring || busy !== null || !status?.available;
  const connectionLabel = status?.running
    ? "Connected"
    : status?.phase === "connecting"
      ? "Connecting"
      : status?.phase === "reconnecting"
        ? "Reconnecting"
        : "Off";

  return (
    <>
      <section style={{ marginBottom: 20 }} data-testid="desktop-cloud-connection">
        <SectionHeader
          title="Hecate Cloud"
          description="Sign in to open Cloud runtimes and other computers. Remote access controls whether this computer can be opened elsewhere."
          meta={status?.running ? "connected" : signedIn ? "signed in" : undefined}
        />
        <div className="card" style={{ overflow: "hidden" }}>
          {loading || status?.restoring ? (
            <CloudMessage>Checking Hecate Cloud account…</CloudMessage>
          ) : !signedIn ? (
            <div
              style={{
                padding: "18px 20px",
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                gap: 20,
                flexWrap: "wrap",
              }}
            >
              <div style={{ minWidth: 0 }}>
                <div style={{ color: "var(--t0)", fontSize: 13, fontWeight: 650 }}>
                  {unavailable
                    ? "Hecate Cloud unavailable"
                    : authorizing
                      ? "Finish signing in"
                      : "Hecate Cloud account"}
                </div>
                <div style={{ marginTop: 4, color: "var(--t2)", fontSize: 12, lineHeight: 1.5 }}>
                  {unavailable
                    ? status?.message ||
                      "Hecate Cloud status could not be read. Check the desktop configuration and try again."
                    : authorizing
                      ? "Approve the sign-in request in your browser. This window updates automatically."
                      : "Sign in to open Cloud runtimes and other computers. Remote access to this computer stays off until you enable it."}
                </div>
              </div>
              <div style={{ display: "flex", gap: 8 }}>
                {unavailable ? (
                  <button
                    className="btn btn-primary"
                    disabled={loading || busy !== null}
                    onClick={() => void loadStatus(true)}
                  >
                    Retry
                  </button>
                ) : (
                  <>
                    {authorizing && (
                      <button
                        className="btn btn-ghost"
                        disabled={actionDisabled}
                        onClick={disconnect}
                      >
                        Cancel
                      </button>
                    )}
                    <button className="btn btn-primary" disabled={actionDisabled} onClick={signIn}>
                      {busy === "signin"
                        ? "Opening…"
                        : authorizing
                          ? "Open browser again"
                          : "Sign in to Hecate Cloud"}
                    </button>
                  </>
                )}
              </div>
            </div>
          ) : (
            <>
              <div
                style={{
                  padding: "15px 20px",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  gap: 16,
                  borderBottom: "1px solid var(--border)",
                }}
              >
                <div style={{ minWidth: 0 }}>
                  <div style={{ color: "var(--t0)", fontSize: 13, fontWeight: 650 }}>
                    {status?.account_email ?? "Hecate Cloud"}
                  </div>
                  <div style={{ marginTop: 2, color: "var(--t3)", fontSize: 11 }}>Signed in</div>
                </div>
                <button
                  className="btn btn-ghost btn-sm"
                  disabled={actionDisabled}
                  onClick={signOut}
                >
                  {busy === "signout" ? "Signing out…" : "Sign out"}
                </button>
              </div>
              <div
                style={{
                  padding: "17px 20px",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  gap: 20,
                }}
              >
                <div style={{ minWidth: 0 }}>
                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 8,
                      color: "var(--t0)",
                      fontSize: 13,
                      fontWeight: 650,
                    }}
                  >
                    Remote access for this computer
                    <span
                      style={{
                        color: status?.running ? "var(--teal)" : "var(--t3)",
                        fontSize: 11,
                        fontWeight: 600,
                      }}
                    >
                      {connectionLabel}
                    </span>
                  </div>
                  <div style={{ marginTop: 4, color: "var(--t2)", fontSize: 12, lineHeight: 1.5 }}>
                    Keep this app open to use this Hecate from your other devices. Remote External
                    Agent work may use this computer&apos;s configured CLI sign-ins; credentials
                    stay here.
                  </div>
                  {status?.phase !== "disconnected" && !status?.running && (
                    <div style={{ marginTop: 5, color: "var(--t3)", fontSize: 11 }}>
                      {status?.message}
                    </div>
                  )}
                </div>
                <Toggle
                  ariaLabel="Remote access for this computer"
                  disabled={actionDisabled}
                  on={accessOn}
                  onChange={(enabled) => void (enabled ? connect() : disconnect())}
                />
              </div>
            </>
          )}
          {(error || status?.last_error) && (
            <div style={{ padding: "0 20px 16px" }}>
              <InlineError message={error || status?.last_error || "Hecate Cloud failed."} />
            </div>
          )}
        </div>
      </section>
      {signedIn && <DesktopCloudRuntimeSettings onAccountStatusRefresh={loadStatus} />}
    </>
  );
}

function DesktopCloudRuntimeSettings({
  onAccountStatusRefresh,
}: {
  onAccountStatusRefresh: () => Promise<void>;
}) {
  const [connections, setConnections] = useState<DesktopCloudRuntimeConnection[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<{ connectionID: string; action: "start" | "open" } | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const requestGenerationRef = useRef(0);
  const requestInFlightRef = useRef<Promise<void> | null>(null);
  const mutationInFlightRef = useRef(false);

  const loadConnections = useCallback(
    (background = false) => {
      if (mutationInFlightRef.current) return Promise.resolve();
      if (requestInFlightRef.current) return requestInFlightRef.current;
      const requestGeneration = ++requestGenerationRef.current;
      if (!background) setLoading(true);
      const request = getDesktopCloudRuntimeConnections()
        .then((nextConnections) => {
          if (requestGeneration !== requestGenerationRef.current) return;
          setConnections(nextConnections);
          setError("");
        })
        .catch(async (err) => {
          if (requestGeneration !== requestGenerationRef.current) return;
          setError(err instanceof Error ? err.message : "Cloud instances could not be loaded.");
          await onAccountStatusRefresh();
        })
        .finally(() => {
          if (!background && requestGeneration === requestGenerationRef.current) setLoading(false);
          if (requestInFlightRef.current === request) requestInFlightRef.current = null;
        });
      requestInFlightRef.current = request;
      return request;
    },
    [onAccountStatusRefresh],
  );

  useEffect(() => {
    void loadConnections();
    return () => {
      requestGenerationRef.current += 1;
    };
  }, [loadConnections]);

  const hasStartingConnection = connections.some(
    (connection) => connection.kind === "hosted_runtime" && connection.status === "starting",
  );
  useEffect(() => {
    const refreshIfVisible = () => {
      if (document.visibilityState === "visible") void loadConnections(true);
    };
    const interval = window.setInterval(refreshIfVisible, hasStartingConnection ? 3000 : 15000);
    document.addEventListener("visibilitychange", refreshIfVisible);
    return () => {
      window.clearInterval(interval);
      document.removeEventListener("visibilitychange", refreshIfVisible);
    };
  }, [hasStartingConnection, loadConnections]);

  async function startConnection(connection: DesktopCloudRuntimeConnection) {
    mutationInFlightRef.current = true;
    requestGenerationRef.current += 1;
    requestInFlightRef.current = null;
    setBusy({ connectionID: connection.id, action: "start" });
    setError("");
    setNotice("");
    try {
      const result = await startDesktopCloudRuntime(connection.id);
      setConnections((current) =>
        current.map((item) =>
          item.id === result.connection_id
            ? { ...item, status: result.status, reachable: result.reachable, can_start: false }
            : item,
        ),
      );
      setNotice(result.message);
    } catch (err) {
      setError(err instanceof Error ? err.message : `${connection.name} could not be started.`);
      await onAccountStatusRefresh();
    } finally {
      mutationInFlightRef.current = false;
      setBusy(null);
    }
  }

  async function openConnection(connection: DesktopCloudRuntimeConnection) {
    setBusy({ connectionID: connection.id, action: "open" });
    setError("");
    setNotice("");
    try {
      const result = await openDesktopCloudRuntime(connection.id);
      setNotice(result.message);
    } catch (err) {
      setError(err instanceof Error ? err.message : `${connection.name} could not be opened.`);
      await onAccountStatusRefresh();
    } finally {
      setBusy(null);
    }
  }

  return (
    <section style={{ marginBottom: 20 }} data-testid="desktop-cloud-runtimes">
      <SectionHeader
        title="Other Hecate instances"
        description="Hosted runtimes and your other computers. Each opens in its own Hecate window."
        meta={
          loading
            ? undefined
            : `${connections.length} instance${connections.length === 1 ? "" : "s"}`
        }
        actions={
          <button
            className="btn btn-ghost btn-sm"
            disabled={loading || busy !== null}
            onClick={() => void loadConnections()}
          >
            <Icon d={Icons.refresh} size={13} /> {loading ? "Loading…" : "Refresh"}
          </button>
        }
      />
      <div className="card" style={{ overflow: "hidden" }}>
        {loading ? (
          <CloudMessage>Loading other Hecate instances…</CloudMessage>
        ) : connections.length === 0 && !error ? (
          <CloudMessage>
            No other runtimes or computers are available for this account.
          </CloudMessage>
        ) : (
          <ul style={{ listStyle: "none", margin: 0, padding: 0 }}>
            {connections.map((connection, index) => (
              <CloudRuntimeConnectionRow
                key={connection.id}
                connection={connection}
                busy={busy}
                last={index === connections.length - 1}
                onOpen={openConnection}
                onStart={startConnection}
              />
            ))}
          </ul>
        )}
        {(error || notice) && (
          <div style={{ padding: "0 20px 16px" }}>
            {error ? (
              <InlineError message={error} />
            ) : (
              <div role="status" style={{ color: "var(--t2)", fontSize: 11, lineHeight: 1.5 }}>
                {notice}
              </div>
            )}
          </div>
        )}
      </div>
    </section>
  );
}

function CloudRuntimeConnectionRow({
  busy,
  connection,
  last,
  onOpen,
  onStart,
}: {
  busy: { connectionID: string; action: "start" | "open" } | null;
  connection: DesktopCloudRuntimeConnection;
  last: boolean;
  onOpen: (connection: DesktopCloudRuntimeConnection) => Promise<void>;
  onStart: (connection: DesktopCloudRuntimeConnection) => Promise<void>;
}) {
  const lastSeen = connection.last_seen_at
    ? formatRelativeTime(connection.last_seen_at)
    : { label: "Not reported", iso: "" };
  const connectionBusy = busy?.connectionID === connection.id;
  const canStart =
    connection.kind === "hosted_runtime" && !connection.reachable && connection.can_start;
  const canOpen =
    connection.reachable && (connection.kind === "hosted_runtime" || connection.remote_enabled);
  const badge =
    connection.status === "starting"
      ? { status: "running", label: "starting" }
      : connection.reachable
        ? { status: "healthy", label: "online" }
        : connection.kind === "desktop_host" && !connection.remote_enabled
          ? { status: "disabled", label: "remote off" }
          : connection.status === "failed"
            ? { status: "failed", label: "failed" }
            : { status: "disabled", label: connection.status };

  return (
    <li
      style={{
        padding: "15px 20px",
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        gap: 20,
        flexWrap: "wrap",
        borderBottom: last ? undefined : "1px solid var(--border)",
      }}
    >
      <div style={{ minWidth: 0 }}>
        <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
          <span style={{ color: "var(--t0)", fontSize: 13, fontWeight: 650 }}>
            {connection.name}
          </span>
          <Badge status={badge.status} label={badge.label} />
        </div>
        <div
          style={{
            marginTop: 4,
            color: "var(--t3)",
            display: "flex",
            gap: 8,
            flexWrap: "wrap",
            fontSize: 11,
            lineHeight: 1.5,
          }}
        >
          <span>{connection.kind === "hosted_runtime" ? "Hosted runtime" : "Computer"}</span>
          {connection.version && <span>Version {connection.version}</span>}
          <span>
            Last seen{" "}
            {lastSeen.iso ? (
              <time dateTime={lastSeen.iso} title={lastSeen.iso}>
                {lastSeen.label}
              </time>
            ) : (
              lastSeen.label
            )}
          </span>
        </div>
      </div>
      <div style={{ display: "flex", gap: 8, marginLeft: "auto" }}>
        {canStart && (
          <button
            className="btn btn-ghost btn-sm"
            disabled={busy !== null}
            onClick={() => void onStart(connection)}
          >
            {connectionBusy && busy?.action === "start" ? "Starting…" : `Start ${connection.name}`}
          </button>
        )}
        {canOpen && (
          <button
            className="btn btn-primary btn-sm"
            disabled={busy !== null}
            onClick={() => void onOpen(connection)}
          >
            {connectionBusy && busy?.action === "open" ? "Opening…" : `Open ${connection.name}`}
          </button>
        )}
      </div>
    </li>
  );
}

function CloudMessage({ children }: { children: string }) {
  return <div style={{ padding: "18px 20px", color: "var(--t2)", fontSize: 12 }}>{children}</div>;
}
