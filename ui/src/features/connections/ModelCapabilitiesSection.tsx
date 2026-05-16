import { useMemo, useState, type ReactNode } from "react";
import type { RuntimeConsoleViewModel } from "../../app/useRuntimeConsole";
import { formatInteger } from "../../lib/format";
import type { ModelRecord } from "../../types/runtime";

type Props = {
  state: RuntimeConsoleViewModel["state"];
  actions: RuntimeConsoleViewModel["actions"];
};

function SectionHeader({
  title,
  description,
  meta,
  actions,
}: {
  title: string;
  description?: string;
  meta?: string;
  actions?: ReactNode;
}) {
  return (
    <div style={{ display: "flex", alignItems: "flex-start", gap: 12, marginBottom: 12 }}>
      <div style={{ minWidth: 0 }}>
        <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", marginBottom: description ? 3 : 0 }}>{title}</div>
        {description && (
          <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>{description}</div>
        )}
      </div>
      {meta && (
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)", whiteSpace: "nowrap" }}>{meta}</span>
      )}
      {actions && <div style={{ marginLeft: "auto", display: "flex", gap: 8, alignItems: "center" }}>{actions}</div>}
    </div>
  );
}

export function ModelCapabilitiesSection({ state, actions }: Props) {
  const [query, setQuery] = useState("");
  const rows = useMemo(() => {
    const q = query.trim().toLowerCase();
    return [...state.models]
      .filter((model) => {
        if (!q) return true;
        const provider = model.metadata?.provider ?? "";
        return model.id.toLowerCase().includes(q) || provider.toLowerCase().includes(q);
      })
      .sort((a, b) => {
        const left = `${a.metadata?.provider ?? ""}/${a.id}`;
        const right = `${b.metadata?.provider ?? ""}/${b.id}`;
        return left.localeCompare(right);
      });
  }, [query, state.models]);

  return (
    <div className="card" style={{ padding: "14px 16px", marginBottom: 24 }} data-testid="connections-model-capabilities">
      <SectionHeader
        title="Model capabilities"
        description="Control whether Hecate should try tools for each discovered model. Tools are on by default unless a model is explicitly marked off; use this when a local/custom model fails tool calls or when you want direct model chat only."
        meta={`${rows.length} model${rows.length === 1 ? "" : "s"}`}
      />

      <div style={{ marginBottom: 14 }}>
        <label style={{ display: "block", fontSize: 11, color: "var(--t3)", marginBottom: 6 }} htmlFor="model-capability-search">
          Filter by model or provider
        </label>
        <input
          id="model-capability-search"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="ollama, qwen, gpt..."
          style={{
            width: "100%",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius-sm)",
            background: "var(--bg2)",
            color: "var(--t0)",
            fontSize: 12,
            padding: "8px 10px",
          }}
        />
      </div>

      {rows.length === 0 ? (
        <div style={{ padding: "20px", textAlign: "center", color: "var(--t3)", fontSize: 12 }}>
          No models discovered yet. Add or start a provider, then refresh the dashboard.
        </div>
      ) : (
        <div style={{ overflow: "hidden", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)" }} data-testid="model-capabilities-list">
          {rows.map((model, index) => (
            <ModelCapabilityRow
              key={`${model.metadata?.provider ?? "unknown"}:${model.id}`}
              model={model}
              divider={index < rows.length - 1}
              onToolsChange={(enabled) => {
                const provider = model.metadata?.provider ?? "";
                if (!provider) return;
                void actions.upsertModelCapabilityOverride({
                  provider,
                  model: model.id,
                  tool_calling: enabled ? "basic" : "none",
                  streaming: model.metadata?.capabilities?.streaming,
                  max_context_tokens: model.metadata?.capabilities?.max_context_tokens,
                  note: enabled ? "Tools enabled from Connections." : "Tools disabled from Connections.",
                });
              }}
              onClear={() => {
                const provider = model.metadata?.provider ?? "";
                if (!provider) return;
                void actions.deleteModelCapabilityOverride(provider, model.id);
              }}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function ModelCapabilityRow({
  model,
  divider,
  onToolsChange,
  onClear,
}: {
  model: ModelRecord;
  divider: boolean;
  onToolsChange: (enabled: boolean) => void;
  onClear: () => void;
}) {
  const capabilities = model.metadata?.capabilities;
  const provider = model.metadata?.provider ?? "unknown";
  const toolCalling = capabilities?.tool_calling ?? "unknown";
  const source = capabilities?.source ?? "unknown";
  const toolsEnabled = toolCalling !== "none";
  const toolTone: ChipTone = toolsEnabled ? "green" : "red";
  const clearDisabled = source !== "operator_override";

  return (
    <div
      data-testid={`model-capability-row-${provider}-${model.id}`}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 12,
        padding: "12px 14px",
        borderBottom: divider ? "1px solid var(--border)" : "none",
      }}
    >
      <div style={{ minWidth: 0, flex: 1 }}>
        <div style={{ display: "flex", alignItems: "baseline", gap: 8, marginBottom: 3 }}>
          <span style={{ fontSize: 12, fontWeight: 500, color: "var(--t0)" }}>{model.id}</span>
          <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>·</span>
          <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)" }}>{provider}</span>
          <span
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              color: chipColor(toolTone),
              textTransform: "uppercase",
              letterSpacing: "0.04em",
            }}
          >
            tools {toolsEnabled ? "on" : "off"}
          </span>
        </div>
        <div style={{ display: "flex", flexWrap: "wrap", gap: 8, fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
          <span>source <span style={{ color: "var(--t1)" }}>{source}</span></span>
          {capabilities?.streaming !== undefined && <span>streaming <span style={{ color: "var(--t1)" }}>{capabilities.streaming ? "yes" : "no"}</span></span>}
          {capabilities?.max_context_tokens !== undefined && <span>context <span style={{ color: "var(--t1)" }}>{formatInteger(capabilities.max_context_tokens)}</span></span>}
        </div>
      </div>
      <div style={{ display: "flex", flexWrap: "wrap", justifyContent: "flex-end", alignItems: "center", gap: 6 }}>
        <div
          role="group"
          aria-label={`Tools for ${model.id}`}
          style={{
            display: "inline-flex",
            alignItems: "center",
            border: "1px solid var(--border)",
            borderRadius: "999px",
            overflow: "hidden",
            background: "var(--bg0)",
            height: 30,
          }}
        >
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            aria-pressed={toolsEnabled}
            onClick={() => onToolsChange(true)}
            style={{
              border: 0,
              borderRadius: 0,
              width: 70,
              padding: "4px 0",
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              background: toolsEnabled ? "var(--teal-bg)" : "transparent",
              color: toolsEnabled ? "var(--teal)" : "var(--t3)",
              justifyContent: "center",
            }}
          >
            tools on
          </button>
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            aria-pressed={!toolsEnabled}
            onClick={() => onToolsChange(false)}
            style={{
              border: 0,
              borderLeft: "1px solid var(--border)",
              borderRadius: 0,
              width: 70,
              padding: "4px 0",
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              background: !toolsEnabled ? "var(--bg3)" : "transparent",
              color: !toolsEnabled ? "var(--t0)" : "var(--t3)",
              justifyContent: "center",
            }}
          >
            tools off
          </button>
        </div>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onClear} disabled={clearDisabled}>
          Clear override
        </button>
      </div>
    </div>
  );
}

type ChipTone = "green" | "amber" | "red" | "muted";

function chipColor(tone: ChipTone): string {
  if (tone === "green") return "var(--green)";
  if (tone === "amber") return "var(--amber)";
  if (tone === "red") return "var(--red)";
  return "var(--t3)";
}
