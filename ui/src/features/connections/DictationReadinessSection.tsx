import { useEffect, useId, useState } from "react";

import { getDictationOptions } from "../../lib/api";
import type { DictationProviderOption } from "../../types/dictation";
import type { ConfiguredStateResponse } from "../../types/provider";

type DictationReadinessPhase = "loading" | "ready" | "failed";

export type DictationReadinessSectionProps = {
  // An authoritative settings snapshot. Its identity changes after every
  // dashboard refresh, including a credential rotation whose public provider
  // metadata stays the same. The route API remains the availability authority;
  // this only makes the mounted card re-check after configuration work.
  providerConfigSnapshot?: ConfiguredStateResponse["data"] | null;
  // Hosted runtimes cannot offer setup for a machine-local transcription
  // server, even though a local desktop runtime can.
  localProviderSetupAvailable?: boolean;
  onAddProvider?: () => void;
};

// Dictation has an intentionally separate routing boundary: microphone audio
// goes only to one explicit speech-to-text provider, and the chat target sees
// ordinary editable text. Keep this readiness check out of model and adapter
// capability state so a Claude Code or Codex sign-in is never misrepresented as
// an audio-transcription credential.
export function DictationReadinessSection({
  providerConfigSnapshot,
  localProviderSetupAvailable = true,
  onAddProvider,
}: DictationReadinessSectionProps) {
  const titleID = useId();
  const [phase, setPhase] = useState<DictationReadinessPhase>("loading");
  const [options, setOptions] = useState<DictationProviderOption[]>([]);
  const [refresh, setRefresh] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setPhase("loading");
    void getDictationOptions(controller.signal)
      .then((response) => {
        if (controller.signal.aborted) return;
        setOptions(response.data);
        setPhase("ready");
      })
      .catch(() => {
        if (!controller.signal.aborted) setPhase("failed");
      });
    return () => controller.abort();
  }, [providerConfigSnapshot, refresh]);

  const availableOptions = options.filter((option) => option.available);
  const unavailableOptions = options.filter((option) => !option.available);
  const ready = phase === "ready" && availableOptions.length > 0;

  return (
    <section
      aria-labelledby={titleID}
      className="card cross-surface-focus-target"
      data-testid="connections-dictation"
      tabIndex={-1}
      style={{ marginBottom: 24, padding: "14px 16px" }}
    >
      <div style={{ display: "flex", alignItems: "flex-start", gap: 12, marginBottom: 12 }}>
        <div style={{ minWidth: 0 }}>
          <h2
            id={titleID}
            style={{ fontSize: 13, fontWeight: 600, color: "var(--t0)", margin: "0 0 4px" }}
          >
            Speech-to-text route readiness
          </h2>
          <div style={{ fontSize: 11, color: "var(--t2)", lineHeight: 1.45 }}>
            Turns a short microphone recording into editable text before it reaches the chat target.
            It works with Hecate Chat, Claude Code, Codex, and other External Agents; their sign-ins
            do not provide speech-to-text. This checks routing only; recording still needs
            microphone permission in your browser or desktop app.
          </div>
        </div>
        <DictationReadinessBadge phase={phase} ready={ready} />
      </div>

      {phase === "loading" && (
        <div role="status" style={statusStyle} data-testid="connections-dictation-loading">
          Checking speech-to-text routes…
        </div>
      )}

      {phase === "failed" && (
        <div style={statusStyle} data-testid="connections-dictation-error">
          <span role="status">Could not load speech-to-text readiness.</span>
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            onClick={() => setRefresh((current) => current + 1)}
            style={{ marginLeft: "auto", padding: "2px 7px" }}
          >
            Retry
          </button>
        </div>
      )}

      {phase === "ready" && (
        <>
          {ready ? (
            <div
              data-testid="connections-dictation-ready"
              role="status"
              style={{ ...statusStyle, color: "var(--green)", marginBottom: 10 }}
            >
              <span>
                {availableOptions.length === 1
                  ? "1 speech-to-text route is ready for every chat target."
                  : `${availableOptions.length} speech-to-text routes are ready for every chat target.`}
              </span>
            </div>
          ) : (
            <div
              data-testid="connections-dictation-unavailable"
              role="status"
              style={{ ...statusStyle, color: "var(--amber-lo)", marginBottom: 10 }}
            >
              <span>
                {options.length === 0
                  ? "No speech-to-text route is configured."
                  : "No configured speech-to-text route is ready."}
              </span>
              {onAddProvider && (
                <button
                  type="button"
                  className="btn btn-primary btn-sm"
                  onClick={onAddProvider}
                  style={{ marginLeft: "auto", whiteSpace: "nowrap" }}
                >
                  Add provider
                </button>
              )}
            </div>
          )}

          {options.length === 0 ? (
            <div style={{ fontSize: 11, color: "var(--t2)", lineHeight: 1.45 }}>
              {localProviderSetupAvailable ? (
                <>
                  Add OpenAI, Groq, or LocalAI in Connections. Hecate uses their explicitly verified
                  transcription routes: <code>gpt-4o-mini-transcribe</code>,{" "}
                  <code>whisper-large-v3-turbo</code>, or <code>whisper-1</code>.
                </>
              ) : (
                <>
                  Add OpenAI or Groq in Connections. This hosted runtime uses their explicitly
                  verified cloud transcription routes: <code>gpt-4o-mini-transcribe</code> or{" "}
                  <code>whisper-large-v3-turbo</code>.
                </>
              )}
            </div>
          ) : (
            <ul
              aria-label="Speech-to-text routes"
              style={{ display: "grid", gap: 7, listStyle: "none", margin: 0, padding: 0 }}
              data-testid="connections-dictation-routes"
            >
              {availableOptions.map((option) => (
                <DictationRouteRow key={option.provider} option={option} />
              ))}
              {unavailableOptions.map((option) => (
                <DictationRouteRow key={option.provider} option={option} />
              ))}
            </ul>
          )}

          <div style={{ marginTop: 10, fontSize: 10, color: "var(--t2)", lineHeight: 1.45 }}>
            Hecate forwards audio only to the selected speech-to-text provider and does not retain
            it. The transcript stays editable and is never sent automatically.
          </div>
        </>
      )}
    </section>
  );
}

function DictationReadinessBadge({
  phase,
  ready,
}: {
  phase: DictationReadinessPhase;
  ready: boolean;
}) {
  const label =
    phase === "loading"
      ? "checking"
      : phase === "failed"
        ? "check failed"
        : ready
          ? "ready"
          : "setup needed";
  const color =
    phase === "loading"
      ? "var(--t2)"
      : phase === "failed"
        ? "var(--red)"
        : ready
          ? "var(--green)"
          : "var(--amber-lo)";
  return (
    <span
      aria-label={`Speech-to-text route ${label}`}
      style={{
        color,
        flexShrink: 0,
        fontFamily: "var(--font-mono)",
        fontSize: 10,
        letterSpacing: "0.04em",
        textTransform: "uppercase",
      }}
    >
      {label}
    </span>
  );
}

function DictationRouteRow({ option }: { option: DictationProviderOption }) {
  const local = option.provider_kind === "local";
  const reason = option.unavailable_reason?.trim() || "route is unavailable";
  return (
    <li
      style={{
        alignItems: "center",
        background: option.available ? "rgba(0, 191, 179, 0.04)" : "var(--bg2)",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        display: "flex",
        flexWrap: "wrap",
        gap: 8,
        minWidth: 0,
        padding: "8px 10px",
      }}
    >
      <span style={{ color: "var(--t1)", fontSize: 11, fontWeight: 600 }}>{option.provider}</span>
      <span
        style={{
          color: "var(--t2)",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          minWidth: 0,
        }}
      >
        {local ? "local" : "cloud"} · {option.default_model}
      </span>
      <span
        style={{
          color: option.available ? "var(--green)" : "var(--amber-lo)",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          marginLeft: "auto",
          minWidth: 0,
          textAlign: "right",
        }}
        title={option.available ? "ready" : reason}
      >
        {option.available ? "ready" : reason}
      </span>
    </li>
  );
}

const statusStyle = {
  alignItems: "center",
  background: "var(--bg2)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  color: "var(--t2)",
  display: "flex",
  fontSize: 11,
  gap: 8,
  lineHeight: 1.45,
  padding: "8px 10px",
};
