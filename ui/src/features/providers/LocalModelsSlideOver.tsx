// LocalModelsSlideOver — the manage-local-models surface. Three
// sections inside one slide-over:
//
//   1. Runtime status — current state + Stop button.
//   2. Installed models — per-row Start / Uninstall + "loaded" pill.
//   3. Catalog — curated entries with Install buttons; the in-flight
//      install renders a live progress row sourced from the SSE
//      stream.
//
// One paste-URL field at the bottom drops a custom .gguf URL into the
// installer — same backend path, just a different InstallSpec.
//
// State boundaries:
//   - The card owns the *summary* poll; this slide-over owns the
//     more detailed lists + the active install stream.
//   - Closing the slide-over cancels the SSE subscription but leaves
//     the install running on the gateway — the card's next poll
//     picks up the finished/failed registry row, and the SSE buffer
//     retains terminal events for 60 s so a re-open replays.

import { useEffect, useRef, useState } from "react";

import {
  cancelLocalModelInstall,
  getLocalModelsCatalog,
  getLocalModelsInstalled,
  getLocalModelsRuntime,
  installLocalModel,
  startLocalModel,
  stopLocalModel,
  subscribeLocalModelInstallEvents,
  uninstallLocalModel,
} from "../../lib/api";
import type {
  LocalModelCatalogEntry,
  LocalModelInstalled,
  LocalModelProgressEvent,
  LocalModelRuntimeResponse,
} from "../../types/runtime";
import { ConfirmModal, Icon, Icons, InlineError, SlideOver } from "../shared/ui";

type ActiveInstall = {
  installID: string;
  modelID: string;
  bytesDownloaded: number;
  bytesTotal: number;
  state: "running" | "completed" | "failed" | "cancelled";
  message?: string;
};

export function LocalModelsSlideOver({ onClose }: { onClose: () => void }) {
  const [catalog, setCatalog] = useState<LocalModelCatalogEntry[]>([]);
  const [installed, setInstalled] = useState<LocalModelInstalled[]>([]);
  const [runtime, setRuntime] = useState<LocalModelRuntimeResponse | null>(null);
  const [error, setError] = useState("");
  const [pasteURL, setPasteURL] = useState("");
  const [pasteToken, setPasteToken] = useState("");
  const [pasting, setPasting] = useState(false);
  const [active, setActive] = useState<ActiveInstall | null>(null);
  const [confirmRemove, setConfirmRemove] = useState<LocalModelInstalled | null>(null);
  const subscribeUnref = useRef<(() => void) | null>(null);

  async function refresh() {
    try {
      const [c, i, r] = await Promise.all([
        getLocalModelsCatalog(),
        getLocalModelsInstalled(),
        getLocalModelsRuntime(),
      ]);
      setCatalog(c.data ?? []);
      setInstalled(i.data ?? []);
      setRuntime(r);
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to load");
    }
  }

  useEffect(() => {
    void refresh();
    return () => {
      subscribeUnref.current?.();
      subscribeUnref.current = null;
    };
  }, []);

  async function handleInstall(spec: { catalog_id?: string; url?: string; hf_token?: string }) {
    setError("");
    try {
      const resp = await installLocalModel(spec);
      const start: ActiveInstall = {
        installID: resp.install_id,
        modelID: resp.model_id,
        bytesDownloaded: 0,
        bytesTotal: 0,
        state: "running",
      };
      setActive(start);
      subscribeUnref.current?.();
      subscribeUnref.current = subscribeLocalModelInstallEvents(
        resp.install_id,
        (ev) => onProgress(ev),
        () => {
          // Network drop / server close — treat as terminal so the
          // UI stops spinning. The next refresh() picks up the
          // real outcome from the registry.
          setActive((cur) => (cur ? { ...cur, state: "failed", message: "connection closed" } : cur));
        },
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : "install failed");
    }
  }

  function onProgress(ev: LocalModelProgressEvent) {
    setActive((cur) => {
      if (!cur) return cur;
      const next: ActiveInstall = { ...cur };
      switch (ev.kind) {
        case "started":
          next.bytesTotal = ev.bytes_total ?? cur.bytesTotal;
          break;
        case "progress":
          next.bytesDownloaded = ev.bytes_downloaded ?? cur.bytesDownloaded;
          if (ev.bytes_total) next.bytesTotal = ev.bytes_total;
          break;
        case "completed":
          next.state = "completed";
          next.bytesDownloaded = ev.bytes_downloaded ?? cur.bytesDownloaded;
          void refresh();
          break;
        case "failed":
          next.state = "failed";
          next.message = ev.message || ev.error_kind || "failed";
          break;
        case "cancelled":
          next.state = "cancelled";
          next.message = ev.message || "cancelled";
          break;
      }
      return next;
    });
  }

  async function handleCancelInstall() {
    if (!active) return;
    try {
      await cancelLocalModelInstall(active.installID);
    } catch (e) {
      setError(e instanceof Error ? e.message : "cancel failed");
    }
  }

  async function handleStart(modelID: string) {
    setError("");
    try {
      await startLocalModel(modelID);
      await refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : "start failed");
    }
  }

  async function handleStop() {
    setError("");
    try {
      await stopLocalModel();
      await refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : "stop failed");
    }
  }

  async function handleUninstall(modelID: string) {
    setError("");
    try {
      await uninstallLocalModel(modelID);
      await refresh();
      setConfirmRemove(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "uninstall failed");
    }
  }

  async function handlePasteSubmit() {
    const trimmed = pasteURL.trim();
    if (!trimmed) return;
    const tokenTrimmed = pasteToken.trim();
    setPasting(true);
    try {
      await handleInstall({
        url: trimmed,
        // Token sent only when the operator provided one — empty
        // string means anonymous fetch (public models). Server
        // does not persist the token; it lives in this session
        // only.
        ...(tokenTrimmed ? { hf_token: tokenTrimmed } : {}),
      });
      setPasteURL("");
      setPasteToken("");
    } finally {
      setPasting(false);
    }
  }

  return (
    <>
      <SlideOver
        title="Bundled model runtime"
        onClose={onClose}
        width={520}
        footer={
          <button
            type="button"
            className="btn"
            style={{ width: "100%", justifyContent: "center" }}
            onClick={onClose}
          >
            Done
          </button>
        }
      >
        <div style={{ display: "flex", flexDirection: "column", gap: 18 }}>
          {error && <InlineError message={error} />}

          <RuntimeSection runtime={runtime} onStop={handleStop} />

          {active && (
            <ActiveInstallRow
              install={active}
              onCancel={handleCancelInstall}
              onDismiss={() => setActive(null)}
            />
          )}

          <InstalledList
            installed={installed}
            activeModelID={runtime?.active?.active_model_id}
            onStart={handleStart}
            onUninstall={(m) => setConfirmRemove(m)}
          />

          <CatalogList
            catalog={catalog}
            disabled={Boolean(active && active.state === "running")}
            onInstall={(id) => handleInstall({ catalog_id: id })}
          />

          <PasteURLSection
            value={pasteURL}
            token={pasteToken}
            disabled={pasting || Boolean(active && active.state === "running")}
            onChange={setPasteURL}
            onTokenChange={setPasteToken}
            onSubmit={handlePasteSubmit}
          />
        </div>
      </SlideOver>
      {confirmRemove && (
        <ConfirmModal
          danger
          title="Uninstall model?"
          message={
            <>
              Remove <strong style={{ color: "var(--t0)" }}>{confirmRemove.display_name || confirmRemove.id}</strong>?
              {" "}
              The .gguf file is deleted from disk. You can re-install it from the catalog later.
            </>
          }
          confirmLabel="Uninstall"
          onClose={() => setConfirmRemove(null)}
          onConfirm={() => handleUninstall(confirmRemove.id)}
        />
      )}
    </>
  );
}

// ─── Sections ────────────────────────────────────────────────────────────────

function RuntimeSection({
  runtime,
  onStop,
}: {
  runtime: LocalModelRuntimeResponse | null;
  onStop: () => void;
}) {
  if (!runtime) return null;
  return (
    <section>
      <SectionHeader label="Runtime" />
      <div
        style={{
          padding: "10px 12px",
          background: "var(--bg2)",
          border: "1px solid var(--border)",
          borderRadius: "var(--radius)",
          display: "flex",
          alignItems: "center",
          gap: 10,
        }}
      >
        <RuntimeStatePill state={runtime.state} />
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontSize: 12, color: "var(--t0)" }}>
            {runtime.state === "running" && runtime.active?.active_model_id
              ? runtime.active.active_model_id
              : runtime.state === "idle"
                ? "No model loaded"
                : runtime.state === "starting"
                  ? "Loading model…"
                  : runtime.state === "stopping"
                    ? "Stopping…"
                    : "Last load failed"}
          </div>
          {runtime.active?.port ? (
            <div
              style={{
                color: "var(--t3)",
                fontSize: 10,
                fontFamily: "var(--font-mono)",
                marginTop: 2,
              }}
            >
              loopback :{runtime.active.port}
            </div>
          ) : null}
        </div>
        {(runtime.state === "running" || runtime.state === "starting") && (
          <button type="button" className="btn btn-sm" onClick={onStop}>
            <Icon d={Icons.stop} size={11} />
            Stop
          </button>
        )}
      </div>
    </section>
  );
}

function RuntimeStatePill({ state }: { state: LocalModelRuntimeResponse["state"] }) {
  const cls =
    state === "running"
      ? "dot dot-green"
      : state === "starting" || state === "stopping"
        ? "dot dot-amber"
        : state === "failed"
          ? "dot dot-red"
          : "dot dot-muted";
  return <span className={cls} />;
}

function ActiveInstallRow({
  install,
  onCancel,
  onDismiss,
}: {
  install: ActiveInstall;
  onCancel: () => void;
  onDismiss: () => void;
}) {
  const pct =
    install.bytesTotal > 0
      ? Math.round((install.bytesDownloaded / install.bytesTotal) * 100)
      : 0;
  const isTerminal =
    install.state === "completed" ||
    install.state === "failed" ||
    install.state === "cancelled";
  const tint =
    install.state === "completed"
      ? "var(--green)"
      : install.state === "failed"
        ? "var(--red)"
        : install.state === "cancelled"
          ? "var(--amber)"
          : "var(--teal)";
  return (
    <section>
      <SectionHeader label="Active install" />
      <div
        style={{
          padding: "10px 12px",
          background: "var(--bg2)",
          border: "1px solid var(--border)",
          borderRadius: "var(--radius)",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 6 }}>
          <span style={{ fontSize: 12, color: "var(--t0)", flex: 1 }}>
            {install.modelID}
          </span>
          <span
            style={{
              color: tint,
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              letterSpacing: "0.04em",
              textTransform: "uppercase",
            }}
          >
            {install.state === "running"
              ? install.bytesTotal > 0
                ? `${pct}%`
                : "downloading"
              : install.state}
          </span>
        </div>
        <div
          className="progress-wrap"
          style={{ background: "var(--bg3)", borderRadius: 3, height: 4 }}
        >
          <div
            className="progress-bar"
            style={{
              width: `${install.state === "completed" ? 100 : pct}%`,
              background: tint,
              height: "100%",
              borderRadius: 3,
              transition: "width 250ms",
            }}
          />
        </div>
        {install.message && (
          <div style={{ marginTop: 6, fontSize: 11, color: "var(--t2)" }}>
            {install.message}
          </div>
        )}
        <div style={{ display: "flex", gap: 6, marginTop: 8 }}>
          {install.state === "running" && (
            <button type="button" className="btn btn-sm" onClick={onCancel}>
              <Icon d={Icons.x} size={11} />
              Cancel
            </button>
          )}
          {isTerminal && (
            <button type="button" className="btn btn-ghost btn-sm" onClick={onDismiss}>
              Dismiss
            </button>
          )}
        </div>
      </div>
    </section>
  );
}

function InstalledList({
  installed,
  activeModelID,
  onStart,
  onUninstall,
}: {
  installed: LocalModelInstalled[];
  activeModelID?: string;
  onStart: (id: string) => void;
  onUninstall: (m: LocalModelInstalled) => void;
}) {
  return (
    <section>
      <SectionHeader label={`Installed (${installed.length})`} />
      {installed.length === 0 ? (
        <div
          style={{
            fontSize: 11,
            color: "var(--t3)",
            padding: "8px 0",
            lineHeight: 1.5,
          }}
        >
          Nothing installed yet — pick a model from the catalog below.
        </div>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
          {installed.map((m) => {
            const isActive = m.id === activeModelID;
            return (
              <div
                key={m.id}
                style={{
                  padding: "8px 10px",
                  background: "var(--bg2)",
                  border: `1px solid ${isActive ? "var(--teal-border)" : "var(--border)"}`,
                  borderRadius: "var(--radius-sm)",
                }}
              >
                <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
                  <span style={{ fontSize: 12, color: "var(--t0)", flex: 1, fontWeight: 500 }}>
                    {m.display_name || m.id}
                  </span>
                  {isActive && (
                    <span
                      className="badge badge-teal"
                      style={{ fontSize: 9 }}
                    >
                      loaded
                    </span>
                  )}
                  {m.size_bytes ? (
                    <span
                      style={{
                        fontFamily: "var(--font-mono)",
                        fontSize: 10,
                        color: "var(--t3)",
                      }}
                    >
                      {formatBytes(m.size_bytes)}
                    </span>
                  ) : null}
                </div>
                <div
                  style={{
                    fontFamily: "var(--font-mono)",
                    fontSize: 10,
                    color: "var(--t3)",
                    marginTop: 2,
                  }}
                >
                  {m.id}
                </div>
                <div style={{ display: "flex", gap: 6, marginTop: 8 }}>
                  {!isActive && (
                    <button
                      type="button"
                      className="btn btn-primary btn-sm"
                      onClick={() => onStart(m.id)}
                    >
                      <Icon d={Icons.send} size={11} />
                      Start
                    </button>
                  )}
                  <button
                    type="button"
                    className="btn btn-danger btn-sm"
                    onClick={() => onUninstall(m)}
                  >
                    <Icon d={Icons.trash} size={11} />
                    Uninstall
                  </button>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}

function CatalogList({
  catalog,
  disabled,
  onInstall,
}: {
  catalog: LocalModelCatalogEntry[];
  disabled: boolean;
  onInstall: (id: string) => void;
}) {
  return (
    <section>
      <SectionHeader label={`Catalog (${catalog.length})`} />
      <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
        {catalog.map((entry) => (
          <div
            key={entry.id}
            style={{
              padding: "8px 10px",
              background: "var(--bg2)",
              border: "1px solid var(--border)",
              borderRadius: "var(--radius-sm)",
            }}
          >
            <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
              <span style={{ fontSize: 12, color: "var(--t0)", flex: 1, fontWeight: 500 }}>
                {entry.display_name}
              </span>
              {entry.size_bytes ? (
                <span
                  style={{
                    fontFamily: "var(--font-mono)",
                    fontSize: 10,
                    color: "var(--t3)",
                  }}
                >
                  {formatBytes(entry.size_bytes)}
                </span>
              ) : null}
            </div>
            {entry.description && (
              <div style={{ fontSize: 11, color: "var(--t2)", lineHeight: 1.5, marginTop: 4 }}>
                {entry.description}
              </div>
            )}
            <div style={{ display: "flex", gap: 6, marginTop: 8, alignItems: "center" }}>
              {entry.installed ? (
                <span className="badge badge-teal" style={{ fontSize: 9 }}>
                  installed
                </span>
              ) : (
                <button
                  type="button"
                  className="btn btn-primary btn-sm"
                  disabled={disabled}
                  onClick={() => onInstall(entry.id)}
                >
                  <Icon d={Icons.plus} size={11} />
                  Install
                </button>
              )}
              {entry.license && (
                <span
                  style={{
                    fontFamily: "var(--font-mono)",
                    fontSize: 10,
                    color: "var(--t3)",
                    marginLeft: "auto",
                  }}
                >
                  {entry.license}
                </span>
              )}
            </div>
          </div>
        ))}
      </div>
    </section>
  );
}

function PasteURLSection({
  value,
  token,
  disabled,
  onChange,
  onTokenChange,
  onSubmit,
}: {
  value: string;
  token: string;
  disabled: boolean;
  onChange: (v: string) => void;
  onTokenChange: (v: string) => void;
  onSubmit: () => void;
}) {
  return (
    <section>
      <SectionHeader label="Custom HuggingFace URL" />
      <p style={{ margin: "0 0 6px", color: "var(--t2)", fontSize: 11, lineHeight: 1.5 }}>
        Paste a direct <strong style={{ color: "var(--t0)" }}>.gguf</strong> download URL from HuggingFace.
        Repo-page URLs aren't supported — open the repo on HF and copy the
        file URL for the specific quant you want. Gated repos (Meta's
        official Llama, Google's official Gemma, etc.) also need an HF
        access token below.
      </p>
      <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
        <input
          className="input"
          type="text"
          placeholder="https://huggingface.co/<repo>/resolve/main/<file>.gguf"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          style={{ fontFamily: "var(--font-mono)" }}
          aria-label="HuggingFace GGUF URL"
        />
        <input
          className="input"
          type="password"
          // Gated-repo token. Optional — public repos work without
          // one. The token rides the install request and is not
          // persisted; clear it after install completes.
          name="hecate-local-models-hf-token"
          autoComplete="new-password"
          autoCorrect="off"
          spellCheck={false}
          data-1p-ignore="true"
          data-lpignore="true"
          data-form-type="other"
          placeholder="HuggingFace access token (optional, for gated repos)"
          value={token}
          onChange={(e) => onTokenChange(e.target.value)}
          style={{ fontFamily: "var(--font-mono)", letterSpacing: "0.08em" }}
          aria-label="HuggingFace access token"
        />
        <button
          type="button"
          className="btn btn-primary btn-sm"
          disabled={disabled || !value.trim()}
          onClick={onSubmit}
          style={{ alignSelf: "flex-end" }}
        >
          Install
        </button>
      </div>
    </section>
  );
}

function SectionHeader({ label }: { label: string }) {
  return (
    <div
      className="kicker-lg"
      style={{ marginBottom: 6 }}
    >
      {label}
    </div>
  );
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}
