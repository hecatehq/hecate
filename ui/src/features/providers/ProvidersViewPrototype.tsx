// ProvidersViewPrototype — feature/ux-2 visual prototype for the Connections
// workspace (the page label is already "Connections" in AppShell).
//
// The design idea: a "connection" is anything Hecate reaches out to for model
// inference or agent execution. Today the page is just LLM providers; this
// prototype groups three connection kinds under one roof:
//
//   1. Cloud providers   — OpenAI, Anthropic, Google, etc. (API key + base URL)
//   2. Local runtimes    — Ollama, LM Studio, llama.cpp (loopback URL, optional key)
//   3. External agents   — Claude Code, Codex, Cursor (CLI on PATH or managed npx)
//
// All three share the same status grammar (ready / needs attention / not
// configured) so an operator can answer "what's working today?" at a glance.
//
// Key design moves vs the current ProvidersView:
//   - Top stat strip: ready count, attention count, model count, last refresh.
//     One-line answer to "is my system set up?"
//   - Single connection list, grouped by kind. Sortable. No tabs.
//   - Each row condenses what's currently spread across the row + an expand
//     panel: brand avatar, name + kind, status pill with reason, model count
//     link, last-checked time, three-action menu.
//   - Empty state per kind. "No cloud providers yet → Pick a preset" is the
//     primary CTA; advanced "Custom OpenAI-compatible" is secondary.
//   - The Add button is a single primary CTA in the page header; selecting
//     "Custom" or one of the known presets happens in a dialog, not a route.

import { useState } from "react";

type ConnectionKind = "cloud" | "local" | "agent";
type ConnectionHealth = "ready" | "attention" | "missing" | "checking";

type ConnectionRow = {
  id: string;
  kind: ConnectionKind;
  name: string;
  brand: string; // tint identifier
  detail: string; // base URL / runtime URL / managed-by hint
  health: ConnectionHealth;
  healthReason?: string; // why "attention" or "missing"
  modelCount?: number;
  lastChecked?: string;
};

const DEMO_CONNECTIONS: ConnectionRow[] = [
  // Cloud providers
  { id: "anthropic", kind: "cloud", name: "Anthropic", brand: "anthropic", detail: "api.anthropic.com", health: "ready", modelCount: 7, lastChecked: "2 min ago" },
  { id: "openai", kind: "cloud", name: "OpenAI", brand: "openai", detail: "api.openai.com", health: "ready", modelCount: 14, lastChecked: "2 min ago" },
  { id: "google", kind: "cloud", name: "Google Gemini", brand: "google", detail: "generativelanguage.googleapis.com", health: "missing", healthReason: "API key not set", lastChecked: "—" },
  // Local runtimes
  { id: "ollama", kind: "local", name: "Ollama", brand: "ollama", detail: "localhost:11434", health: "ready", modelCount: 3, lastChecked: "30s ago" },
  { id: "lmstudio", kind: "local", name: "LM Studio", brand: "lmstudio", detail: "localhost:1234", health: "attention", healthReason: "connection refused — is LM Studio running?", lastChecked: "30s ago" },
  // External agents
  { id: "claude_code", kind: "agent", name: "Claude Code", brand: "claude", detail: "claude-agent-acp · npx managed", health: "ready", lastChecked: "1 min ago" },
  { id: "codex", kind: "agent", name: "Codex", brand: "codex", detail: "codex-acp · npx managed", health: "attention", healthReason: "not signed in — run `codex login`", lastChecked: "1 min ago" },
  { id: "cursor", kind: "agent", name: "Cursor", brand: "cursor", detail: "cursor-agent · expected on PATH", health: "missing", healthReason: "binary not found on PATH", lastChecked: "1 min ago" },
];

const KIND_LABEL: Record<ConnectionKind, string> = {
  cloud: "Cloud providers",
  local: "Local runtimes",
  agent: "External agents",
};

const KIND_DESCRIPTION: Record<ConnectionKind, string> = {
  cloud: "API-keyed services. Hecate routes chat completions here when the request asks for a cloud model.",
  local: "Loopback model servers running on this machine. No API key required by default.",
  agent: "Standalone agent CLIs Hecate spawns as subprocesses (Codex, Claude Code, Cursor).",
};

const BRAND_TINT: Record<string, string> = {
  anthropic: "oklch(0.72 0.14 35)",
  openai: "oklch(0.72 0.10 165)",
  google: "oklch(0.74 0.10 260)",
  ollama: "oklch(0.72 0.06 240)",
  lmstudio: "oklch(0.72 0.08 290)",
  claude: "oklch(0.72 0.14 35)",
  codex: "oklch(0.74 0.10 260)",
  cursor: "oklch(0.72 0.12 320)",
};

const HEALTH_TONE: Record<ConnectionHealth, { color: string; bg: string; border: string; label: string }> = {
  ready:     { color: "var(--green)", bg: "var(--green-bg)", border: "var(--green-border)", label: "Ready" },
  attention: { color: "var(--amber)", bg: "var(--amber-bg)", border: "var(--amber-border)", label: "Needs attention" },
  missing:   { color: "var(--red)",   bg: "var(--red-bg)",   border: "var(--red-border)",   label: "Not configured" },
  checking:  { color: "var(--t2)",    bg: "var(--bg3)",      border: "var(--border)",       label: "Checking…" },
};

// Accept the same prop shape as the real ProvidersView so AppShell can swap
// the import verbatim. Props are ignored — prototype renders static demo data.
export function ProvidersViewPrototype(_props: { state?: unknown; actions?: unknown }) {
  void _props;
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const grouped = groupByKind(DEMO_CONNECTIONS);
  return (
    <div style={{ height: "100%", overflow: "hidden", display: "flex", flexDirection: "column" }}>
      <PageHeader />
      <div style={{ flex: 1, overflowY: "auto", padding: "0 16px 24px" }}>
        <StatStrip />
        {(["cloud", "local", "agent"] as ConnectionKind[]).map(kind => (
          <ConnectionGroup
            key={kind}
            kind={kind}
            rows={grouped[kind] || []}
            selectedID={selectedID}
            onSelect={setSelectedID}
          />
        ))}
      </div>
    </div>
  );
}

function groupByKind(rows: ConnectionRow[]) {
  const out: Partial<Record<ConnectionKind, ConnectionRow[]>> = {};
  for (const row of rows) {
    (out[row.kind] = out[row.kind] || []).push(row);
  }
  return out;
}

// ─── Page header ──────────────────────────────────────────────────────────────

function PageHeader() {
  return (
    <div style={{
      padding: "14px 16px 12px",
      borderBottom: "1px solid var(--border)",
      display: "flex",
      alignItems: "center",
      gap: 10,
    }}>
      <div>
        <h2 style={{
          margin: 0,
          color: "var(--t0)",
          fontSize: 14,
          fontWeight: 500,
        }}>
          Connections
        </h2>
        <p style={{
          margin: "2px 0 0",
          color: "var(--t2)",
          fontSize: 11,
          lineHeight: 1.4,
        }}>
          Cloud providers, local model runtimes, and external agent CLIs. Each connection has its own credentials and health.
        </p>
      </div>
      <span style={{ flex: 1 }} />
      <button type="button" style={{
        padding: "5px 10px",
        background: "transparent",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        color: "var(--t1)",
        fontSize: 11,
        cursor: "pointer",
        fontFamily: "var(--font-mono)",
      }}>
        Refresh all
      </button>
      <button type="button" style={{
        padding: "5px 14px",
        background: "var(--teal)",
        color: "oklch(0.12 0.02 185)",
        border: "none",
        borderRadius: "var(--radius-sm)",
        fontSize: 12,
        fontWeight: 600,
        cursor: "pointer",
      }}>
        ＋ Add connection
      </button>
    </div>
  );
}

// ─── Stat strip ───────────────────────────────────────────────────────────────

function StatStrip() {
  const ready = DEMO_CONNECTIONS.filter(c => c.health === "ready").length;
  const attention = DEMO_CONNECTIONS.filter(c => c.health === "attention").length;
  const missing = DEMO_CONNECTIONS.filter(c => c.health === "missing").length;
  const models = DEMO_CONNECTIONS.reduce((sum, c) => sum + (c.modelCount ?? 0), 0);
  return (
    <div style={{
      display: "grid",
      gridTemplateColumns: "repeat(4, 1fr)",
      gap: 12,
      margin: "16px 0",
    }}>
      <Stat label="Ready" value={ready} tone="ready" />
      <Stat label="Needs attention" value={attention} tone="attention" />
      <Stat label="Not configured" value={missing} tone="missing" />
      <Stat label="Models available" value={models} tone="muted" detail="across ready connections" />
    </div>
  );
}

function Stat({ label, value, tone, detail }: { label: string; value: number; tone: "ready" | "attention" | "missing" | "muted"; detail?: string }) {
  const color =
    tone === "ready"     ? "var(--green)" :
    tone === "attention" ? "var(--amber)" :
    tone === "missing"   ? "var(--red)" :
                           "var(--t1)";
  const bg =
    tone === "ready"     ? "var(--green-bg)" :
    tone === "attention" ? "var(--amber-bg)" :
    tone === "missing"   ? "var(--red-bg)" :
                           "var(--bg2)";
  return (
    <div style={{
      padding: "10px 12px",
      background: bg,
      border: "1px solid var(--border)",
      borderRadius: "var(--radius)",
    }}>
      <div style={{
        color: "var(--t3)",
        fontSize: 10,
        fontFamily: "var(--font-mono)",
        letterSpacing: "0.08em",
        textTransform: "uppercase",
      }}>
        {label}
      </div>
      <div style={{ color, fontSize: 22, fontWeight: 500, marginTop: 4, lineHeight: 1 }}>{value}</div>
      {detail && (
        <div style={{ color: "var(--t3)", fontSize: 10, marginTop: 3 }}>{detail}</div>
      )}
    </div>
  );
}

// ─── Connection group ─────────────────────────────────────────────────────────

function ConnectionGroup({
  kind,
  rows,
  selectedID,
  onSelect,
}: {
  kind: ConnectionKind;
  rows: ConnectionRow[];
  selectedID: string | null;
  onSelect: (id: string | null) => void;
}) {
  return (
    <section style={{ marginBottom: 22 }}>
      <header style={{ marginBottom: 8, display: "flex", alignItems: "baseline", gap: 8 }}>
        <h3 style={{ margin: 0, fontSize: 12, color: "var(--t0)", fontWeight: 500 }}>
          {KIND_LABEL[kind]}
        </h3>
        <span style={{ color: "var(--t3)", fontSize: 10, fontFamily: "var(--font-mono)" }}>
          {rows.length}
        </span>
        <p style={{ margin: 0, color: "var(--t2)", fontSize: 11, lineHeight: 1.4, flex: 1 }}>
          {KIND_DESCRIPTION[kind]}
        </p>
      </header>
      {rows.length === 0 ? (
        <EmptyGroup kind={kind} />
      ) : (
        <div style={{
          border: "1px solid var(--border)",
          borderRadius: "var(--radius)",
          background: "var(--bg2)",
          overflow: "hidden",
        }}>
          {rows.map((row, i) => (
            <ConnectionRowView
              key={row.id}
              row={row}
              expanded={selectedID === row.id}
              onToggle={() => onSelect(selectedID === row.id ? null : row.id)}
              divider={i < rows.length - 1}
            />
          ))}
        </div>
      )}
    </section>
  );
}

function EmptyGroup({ kind }: { kind: ConnectionKind }) {
  return (
    <div style={{
      border: "1px dashed var(--border2)",
      borderRadius: "var(--radius)",
      padding: "20px",
      textAlign: "center",
      color: "var(--t2)",
      fontSize: 12,
    }}>
      No {KIND_LABEL[kind].toLowerCase()} configured yet.{" "}
      <button type="button" style={{
        background: "none",
        border: "none",
        color: "var(--teal)",
        cursor: "pointer",
        fontFamily: "inherit",
        fontSize: 12,
        padding: 0,
        textDecoration: "underline",
        textDecorationColor: "rgba(70, 200, 200, 0.4)",
      }}>
        Add your first
      </button>
    </div>
  );
}

// ─── Connection row ───────────────────────────────────────────────────────────

function ConnectionRowView({
  row,
  expanded,
  onToggle,
  divider,
}: {
  row: ConnectionRow;
  expanded: boolean;
  onToggle: () => void;
  divider: boolean;
}) {
  const tone = HEALTH_TONE[row.health];
  const tint = BRAND_TINT[row.brand] || "var(--t1)";
  return (
    <div style={{ borderBottom: divider ? "1px solid var(--border)" : undefined }}>
      <div
        onClick={onToggle}
        style={{
          padding: "10px 14px",
          display: "grid",
          gridTemplateColumns: "32px 1fr auto auto",
          gap: 12,
          alignItems: "center",
          cursor: "pointer",
          background: expanded ? "var(--bg3)" : "transparent",
        }}>
        {/* Brand */}
        <div style={{
          width: 32,
          height: 32,
          borderRadius: "var(--radius-sm)",
          background: "var(--bg3)",
          border: `1px solid ${tint}`,
          color: tint,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          fontSize: 13,
          fontWeight: 600,
          fontFamily: "var(--font-mono)",
        }}>
          {row.name.charAt(0).toUpperCase()}
        </div>
        {/* Identity */}
        <div style={{ minWidth: 0 }}>
          <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
            <span style={{ color: "var(--t0)", fontSize: 13, fontWeight: 500 }}>{row.name}</span>
            {row.modelCount !== undefined && (
              <span style={{ color: "var(--t3)", fontSize: 10, fontFamily: "var(--font-mono)" }}>
                {row.modelCount} models
              </span>
            )}
            {row.lastChecked && (
              <span style={{ color: "var(--t3)", fontSize: 10, fontFamily: "var(--font-mono)" }}>
                checked {row.lastChecked}
              </span>
            )}
          </div>
          <div style={{
            color: "var(--t2)",
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            marginTop: 2,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}>
            {row.detail}
          </div>
        </div>
        {/* Status pill */}
        <div style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 6,
          padding: "3px 8px",
          background: tone.bg,
          border: `1px solid ${tone.border}`,
          borderRadius: "var(--radius-sm)",
          color: tone.color,
          fontSize: 11,
          fontFamily: "var(--font-mono)",
        }}>
          <span style={{ width: 6, height: 6, borderRadius: 999, background: tone.color }} />
          {tone.label}
        </div>
        <button type="button" style={{
          ...iconBtn,
          color: "var(--t3)",
          transform: expanded ? "rotate(180deg)" : "none",
          transition: "transform 150ms",
        }} onClick={(e) => { e.stopPropagation(); onToggle(); }}>
          <span style={{ fontSize: 10 }}>▾</span>
        </button>
      </div>
      {expanded && <ConnectionExpand row={row} />}
    </div>
  );
}

const iconBtn: React.CSSProperties = {
  width: 26,
  height: 26,
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  background: "transparent",
  border: "1px solid transparent",
  borderRadius: "var(--radius-sm)",
  color: "var(--t2)",
  cursor: "pointer",
};

function ConnectionExpand({ row }: { row: ConnectionRow }) {
  return (
    <div style={{
      padding: "12px 14px 16px 14px",
      background: "var(--bg2)",
      borderTop: "1px solid var(--border)",
    }}>
      {row.healthReason && (
        <div style={{
          padding: "8px 10px",
          marginBottom: 12,
          background: HEALTH_TONE[row.health].bg,
          border: `1px solid ${HEALTH_TONE[row.health].border}`,
          borderRadius: "var(--radius-sm)",
          color: HEALTH_TONE[row.health].color,
          fontSize: 12,
        }}>
          {row.healthReason}
        </div>
      )}
      <div style={{
        display: "grid",
        gridTemplateColumns: "repeat(auto-fill, minmax(180px, 1fr))",
        gap: 12,
        marginBottom: 14,
      }}>
        <Field label="Kind" value={KIND_LABEL[row.kind]} />
        <Field label="Endpoint" value={row.detail} mono />
        {row.modelCount !== undefined && <Field label="Models" value={`${row.modelCount} available`} />}
        {row.lastChecked && <Field label="Last checked" value={row.lastChecked} mono />}
      </div>
      <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
        <RowAction>Edit</RowAction>
        <RowAction>Refresh</RowAction>
        {row.health === "missing" && row.kind === "agent" && (
          <RowAction primary>Set up</RowAction>
        )}
        {row.health === "attention" && row.kind === "agent" && (
          <RowAction primary>Open setup</RowAction>
        )}
        {row.modelCount !== undefined && row.modelCount > 0 && (
          <RowAction>Show {row.modelCount} models</RowAction>
        )}
        <span style={{ flex: 1 }} />
        <RowAction tone="red">Remove</RowAction>
      </div>
    </div>
  );
}

function Field({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <div style={{ color: "var(--t3)", fontSize: 10, fontFamily: "var(--font-mono)", letterSpacing: "0.06em", textTransform: "uppercase" }}>
        {label}
      </div>
      <div style={{
        color: "var(--t0)",
        fontSize: 12,
        fontFamily: mono ? "var(--font-mono)" : "inherit",
        marginTop: 2,
        wordBreak: "break-all",
      }}>
        {value}
      </div>
    </div>
  );
}

function RowAction({ children, primary, tone }: { children: React.ReactNode; primary?: boolean; tone?: "red" }) {
  const fg = tone === "red" ? "var(--red)" : primary ? "oklch(0.12 0.02 185)" : "var(--t1)";
  const bg = primary ? "var(--teal)" : "var(--bg3)";
  const border = primary ? "var(--teal)" : "var(--border)";
  return (
    <button type="button" style={{
      padding: "5px 10px",
      background: bg,
      color: fg,
      border: `1px solid ${border}`,
      borderRadius: "var(--radius-sm)",
      fontSize: 11,
      fontFamily: "inherit",
      cursor: "pointer",
      fontWeight: primary ? 600 : 400,
    }}>
      {children}
    </button>
  );
}
