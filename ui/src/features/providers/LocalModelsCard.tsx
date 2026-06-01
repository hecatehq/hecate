// LocalModelsCard — the Connections-workspace card that summarizes the
// Hecate-managed local-model runtime (llama.cpp) and opens the
// SlideOver for catalog browse + per-model install/start/stop.
//
// Polls /hecate/v1/local-models/runtime + /installed on a low-rate
// interval (8 s) so the operator's UI stays in sync without a
// dedicated SSE connection. While an install is active, the
// SlideOver upgrades to per-install SSE for live progress.

import { useEffect, useState } from "react";

import { getLocalModelsInstalled, getLocalModelsRuntime } from "../../lib/api";
import type {
  LocalModelInstalled,
  LocalModelRuntimeResponse,
} from "../../types/runtime";
import { Icon, Icons } from "../shared/ui";

import { LocalModelsSlideOver } from "./LocalModelsSlideOver";

const POLL_INTERVAL_MS = 8000;

export function LocalModelsCard() {
  const [runtime, setRuntime] = useState<LocalModelRuntimeResponse | null>(null);
  const [installed, setInstalled] = useState<LocalModelInstalled[]>([]);
  const [slideOverOpen, setSlideOverOpen] = useState(false);
  const [loading, setLoading] = useState(true);

  async function refresh() {
    try {
      const [rt, inst] = await Promise.all([
        getLocalModelsRuntime(),
        // /installed is dormant-aware: returns 503 when feature is
        // off. Suppress and treat as empty so the card still
        // renders the "not available" state without an error.
        getLocalModelsInstalled().catch(() => ({ object: "local_models.installed", data: [] })),
      ]);
      setRuntime(rt);
      setInstalled(inst.data ?? []);
    } catch {
      // Dormant build path: keep runtime as null; the card renders
      // a "not available" state from that.
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void refresh();
    const id = setInterval(() => void refresh(), POLL_INTERVAL_MS);
    return () => clearInterval(id);
  }, []);

  // Re-poll when the slide-over closes so installed/running state
  // catches up immediately.
  useEffect(() => {
    if (slideOverOpen) return;
    void refresh();
  }, [slideOverOpen]);

  if (loading) {
    return (
      <div className="card" style={{ padding: "14px 16px", marginBottom: 24 }}>
        <div style={{ fontSize: 12, color: "var(--t3)" }}>Loading bundled model runtime…</div>
      </div>
    );
  }

  const available = runtime?.available ?? false;
  if (!available) {
    return <DormantCard reason={runtime?.reason ?? "binary_not_found"} />;
  }

  return (
    <>
      <ActiveCard
        runtime={runtime!}
        installed={installed}
        onManage={() => setSlideOverOpen(true)}
      />
      {slideOverOpen && (
        <LocalModelsSlideOver
          onClose={() => setSlideOverOpen(false)}
        />
      )}
    </>
  );
}

function DormantCard({ reason }: { reason: string }) {
  return (
    <div
      className="card"
      data-testid="local-models-card-dormant"
      style={{
        padding: "14px 16px",
        marginBottom: 24,
        borderStyle: "dashed",
        background: "var(--bg2)",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 4 }}>
        <Icon d={Icons.info} size={14} />
        <span style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)" }}>
          Bundled model runtime
        </span>
        <span
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            color: "var(--t3)",
            letterSpacing: "0.06em",
            textTransform: "uppercase",
            marginLeft: "auto",
          }}
        >
          {dormantReasonLabel(reason)}
        </span>
      </div>
      <div style={{ fontSize: 11, color: "var(--t2)", lineHeight: 1.5 }}>
        Local model downloads and runtime live in the Hecate desktop app. This
        build of the gateway doesn't include the bundled llama.cpp binary, so
        local-model endpoints are disabled.
      </div>
    </div>
  );
}

function dormantReasonLabel(reason: string): string {
  switch (reason) {
    case "binary_not_found":
      return "Not bundled";
    case "binary_not_executable":
      return "Binary unusable";
    case "flag_off":
      return "Disabled";
    default:
      return "Unavailable";
  }
}

function ActiveCard({
  runtime,
  installed,
  onManage,
}: {
  runtime: LocalModelRuntimeResponse;
  installed: LocalModelInstalled[];
  onManage: () => void;
}) {
  const stateLabel = describeRuntimeState(runtime);
  const stateColor = runtimeStateColor(runtime.state);
  return (
    <div
      className="card"
      data-testid="local-models-card-active"
      style={{ padding: "14px 16px", marginBottom: 24 }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 6 }}>
        <Icon d={Icons.model} size={14} />
        <span style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)" }}>
          Bundled model runtime
        </span>
        <span
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            color: stateColor,
            letterSpacing: "0.06em",
            textTransform: "uppercase",
            marginLeft: "auto",
          }}
        >
          {stateLabel}
        </span>
      </div>
      <div style={{ fontSize: 11, color: "var(--t2)", lineHeight: 1.5, marginBottom: 10 }}>
        Download open-weight models from HuggingFace and run them through Hecate's
        bundled llama.cpp server. Models stream through the chat composer's
        model picker as soon as they finish installing.
      </div>
      <div style={{ display: "flex", alignItems: "center", gap: 14 }}>
        <ModelCountSummary installed={installed} activeID={runtime.active?.active_model_id} />
        <span style={{ flex: 1 }} />
        <button
          className="btn btn-primary btn-sm"
          onClick={onManage}
          data-testid="local-models-card-manage"
        >
          <Icon d={Icons.settings} size={13} />
          Manage models
        </button>
      </div>
      {runtime.state === "failed" && runtime.active?.last_error && (
        <div
          style={{
            marginTop: 10,
            padding: "6px 8px",
            background: "var(--red-bg)",
            border: "1px solid var(--red-border)",
            borderRadius: "var(--radius-sm)",
            color: "var(--red)",
            fontSize: 11,
            fontFamily: "var(--font-mono)",
          }}
        >
          {runtime.active.last_error}
        </div>
      )}
    </div>
  );
}

function ModelCountSummary({
  installed,
  activeID,
}: {
  installed: LocalModelInstalled[];
  activeID?: string;
}) {
  const count = installed.length;
  if (count === 0) {
    return (
      <span style={{ fontSize: 11, color: "var(--t2)" }}>
        No models installed yet.
      </span>
    );
  }
  const active = activeID && installed.find((m) => m.id === activeID);
  return (
    <span style={{ fontSize: 11, color: "var(--t2)" }}>
      <strong style={{ color: "var(--t0)" }}>{count}</strong> installed
      {active ? (
        <>
          {" · "}
          <span style={{ color: "var(--teal)" }}>
            {active.display_name || active.id} running
          </span>
        </>
      ) : null}
    </span>
  );
}

function describeRuntimeState(runtime: LocalModelRuntimeResponse): string {
  switch (runtime.state) {
    case "running":
      return "Running";
    case "starting":
      return "Loading…";
    case "stopping":
      return "Stopping…";
    case "failed":
      return "Error";
    case "idle":
    default:
      return "Idle";
  }
}

function runtimeStateColor(state: LocalModelRuntimeResponse["state"]): string {
  switch (state) {
    case "running":
      return "var(--teal)";
    case "starting":
    case "stopping":
      return "var(--amber)";
    case "failed":
      return "var(--red)";
    case "idle":
    default:
      return "var(--t3)";
  }
}
