import { useEffect, useState, type ReactNode } from "react";

import { ApiError } from "../../lib/api";
import type {
  ContextPacketItemRecord,
  ContextPacketRecord,
  ContextPacketRefsRecord,
  ContextPacketSourceRecord,
} from "../../types/context";
import { InlineError } from "./Atoms";
import { Badge, Icon, Icons, Modal } from "./ui";

type ContextInspectorModalTriggerProps = {
  buttonLabel?: string;
  buttonTitle?: string;
  emptyDetail?: string;
  loadPacket?: (() => Promise<ContextPacketRecord>) | null;
  modalTitle: string;
  resourceKey: string;
  unavailableDetail?: string;
};

type RemoteState =
  | { status: "idle" | "loading" }
  | { status: "ready"; packet: ContextPacketRecord }
  | { status: "unavailable"; detail: string }
  | { status: "error"; detail: string };

const sectionOrder = [
  "instructions",
  "project",
  "project_work",
  "memory",
  "sources",
  "workspace",
  "runtime",
] as const;

const refOrder: Array<[keyof ContextPacketRefsRecord, string]> = [
  ["project_id", "Project"],
  ["work_item_id", "Work item"],
  ["assignment_id", "Assignment"],
  ["role_id", "Role"],
  ["task_id", "Task"],
  ["run_id", "Run"],
  ["session_id", "Chat session"],
  ["message_id", "Message"],
];

export function ContextInspectorModalTrigger({
  buttonLabel = "Inspect context",
  buttonTitle,
  emptyDetail,
  loadPacket,
  modalTitle,
  resourceKey,
  unavailableDetail = "This snapshot may predate stored context packets or have no linked packet.",
}: ContextInspectorModalTriggerProps) {
  const [open, setOpen] = useState(false);
  const [reloadNonce, setReloadNonce] = useState(0);
  const [state, setState] = useState<RemoteState>({ status: "idle" });

  useEffect(() => {
    setReloadNonce(0);
    setState({ status: "idle" });
  }, [resourceKey]);

  useEffect(() => {
    if (!open || !loadPacket) return;
    let cancelled = false;
    setState({ status: "loading" });
    loadPacket()
      .then((packet) => {
        if (cancelled) return;
        setState({ status: "ready", packet });
      })
      .catch((error) => {
        if (cancelled) return;
        if (error instanceof ApiError && error.status === 404) {
          setState({ status: "unavailable", detail: unavailableDetail });
          return;
        }
        setState({
          status: "error",
          detail: error instanceof Error ? error.message : "Failed to load context snapshot.",
        });
      });
    return () => {
      cancelled = true;
    };
  }, [loadPacket, open, reloadNonce, resourceKey, unavailableDetail]);

  const canInspect = Boolean(loadPacket);
  return (
    <>
      <button
        className="btn btn-ghost btn-sm"
        type="button"
        disabled={!canInspect}
        onClick={() => setOpen(true)}
        title={
          buttonTitle ||
          (canInspect
            ? "Inspect the stored context snapshot."
            : "No context snapshot available yet.")
        }
      >
        <Icon d={Icons.search} size={12} />
        {buttonLabel}
      </button>
      {open && (
        <Modal
          title={modalTitle}
          width={820}
          onClose={() => setOpen(false)}
          footer={
            <>
              <button className="btn btn-ghost" type="button" onClick={() => setOpen(false)}>
                Close
              </button>
              {canInspect && (
                <button
                  className="btn btn-ghost"
                  type="button"
                  onClick={() => setReloadNonce((current) => current + 1)}
                >
                  Reload
                </button>
              )}
            </>
          }
        >
          <div style={{ padding: 16, overflowY: "auto" }}>
            {state.status === "loading" || state.status === "idle" ? (
              <ContextInspectorNotice
                title="Loading context snapshot…"
                detail="Fetching the persisted packet for this run or assignment."
                tone="muted"
              />
            ) : null}
            {state.status === "unavailable" ? (
              <ContextInspectorNotice
                title="Context snapshot unavailable"
                detail={state.detail}
                tone="warning"
              />
            ) : null}
            {state.status === "error" ? (
              <div style={{ display: "grid", gap: 10 }}>
                <InlineError message={state.detail} />
                <div style={{ color: "var(--t2)", fontSize: 12 }}>
                  Try again after the linked run or chat packet finishes persisting.
                </div>
              </div>
            ) : null}
            {state.status === "ready" ? (
              <ContextInspectorPanel packet={state.packet} emptyDetail={emptyDetail} />
            ) : null}
          </div>
        </Modal>
      )}
    </>
  );
}

export function ContextInspectorDetails({ packet }: { packet: ContextPacketRecord }) {
  return (
    <details style={{ marginTop: 8 }}>
      <summary
        style={{
          color: "var(--t3)",
          cursor: "pointer",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          lineHeight: 1.6,
        }}
      >
        {contextInspectorSummary(packet)}
      </summary>
      <div style={{ marginTop: 6 }}>
        <ContextInspectorPanel packet={packet} compact />
      </div>
    </details>
  );
}

export function contextPacketEmpty(packet: ContextPacketRecord): boolean {
  return (
    !packet.id &&
    !packet.version &&
    !packet.execution_mode &&
    !packet.provider &&
    !packet.model &&
    !packet.execution_profile &&
    !packet.workspace &&
    !packet.system_prompt_included &&
    !packet.message_count &&
    !hasRefs(packet.refs) &&
    (packet.sources ?? []).length === 0 &&
    (packet.items ?? []).length === 0
  );
}

function ContextInspectorPanel({
  compact = false,
  emptyDetail,
  packet,
}: {
  compact?: boolean;
  emptyDetail?: string;
  packet: ContextPacketRecord;
}) {
  const items = packet.items ?? [];
  const sources = packet.sources ?? [];
  const groups = groupContextItemsBySection(items);
  const refs = visibleRefs(packet.refs);
  const legacyOnly = groups.length === 0 && sources.length > 0;
  const empty = contextPacketEmpty(packet);

  if (empty) {
    return (
      <ContextInspectorNotice
        title="No context metadata captured"
        detail={emptyDetail || "This snapshot does not include any itemized context metadata yet."}
        tone="muted"
      />
    );
  }

  return (
    <div
      style={{
        background: "var(--bg1)",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        display: "grid",
        gap: 12,
        padding: compact ? "8px 9px" : 12,
      }}
    >
      <ContextSummary packet={packet} />
      {!compact && refs.length > 0 && (
        <section style={{ display: "grid", gap: 8 }}>
          <div className="kicker">Linked refs</div>
          <div
            style={{
              display: "grid",
              gap: "8px 12px",
              gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))",
            }}
          >
            {refs.map(([label, value]) => (
              <ContextMetaCell key={label} label={label} value={value} />
            ))}
          </div>
        </section>
      )}
      {groups.length > 0 && (
        <div style={{ display: "grid", gap: 10 }}>
          {groups.map((group) => (
            <ContextSectionGroup key={group.section} group={group} />
          ))}
        </div>
      )}
      {legacyOnly && (
        <section style={{ display: "grid", gap: 8 }}>
          <div className="kicker">Legacy sources</div>
          <div style={{ color: "var(--t3)", fontSize: 11 }}>
            This older packet only exposes top-level sources. Newer packets render itemized
            sections.
          </div>
          <div style={{ display: "grid", gap: 8 }}>
            {sources.map((source, index) => (
              <ContextSourceRow key={`${source.kind}-${source.label}-${index}`} source={source} />
            ))}
          </div>
        </section>
      )}
    </div>
  );
}

function ContextSummary({ packet }: { packet: ContextPacketRecord }) {
  const modelValue = [packet.provider, packet.model].filter(Boolean).join(" / ") || "—";
  const modeValue = packet.execution_mode ? humanExecutionMode(packet.execution_mode) : "—";
  const profileValue = packet.execution_profile || "—";
  const workspaceValue = packet.workspace || "—";
  const systemPromptValue =
    typeof packet.system_prompt_included === "boolean"
      ? packet.system_prompt_included
        ? "Included"
        : "Not included"
      : "Unknown";
  const messageCountValue =
    typeof packet.message_count === "number"
      ? `${packet.message_count} message${packet.message_count === 1 ? "" : "s"}`
      : "—";

  return (
    <section style={{ display: "grid", gap: 8 }}>
      <div className="kicker">Launch summary</div>
      <div
        style={{
          display: "grid",
          gap: "8px 12px",
          gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))",
        }}
      >
        <ContextMetaCell label="Mode" value={modeValue} />
        <ContextMetaCell label="Provider / model" value={modelValue} />
        <ContextMetaCell label="Execution profile" value={profileValue} />
        <ContextMetaCell label="Workspace" value={workspaceValue} />
        <ContextMetaCell label="System prompt" value={systemPromptValue} />
        <ContextMetaCell label="Transcript count" value={messageCountValue} />
      </div>
    </section>
  );
}

function ContextMetaCell({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div>
      <div className="kicker" style={{ marginBottom: 4 }}>
        {label}
      </div>
      <div
        style={{
          color: "var(--t1)",
          fontFamily: "var(--font-mono)",
          fontSize: 11,
          lineHeight: 1.5,
          wordBreak: "break-word",
        }}
      >
        {value}
      </div>
    </div>
  );
}

type ContextSectionGroupRecord = {
  section: string;
  items: ContextPacketItemRecord[];
};

function ContextSectionGroup({ group }: { group: ContextSectionGroupRecord }) {
  const includedCount = group.items.filter((item) => item.included).length;
  const excludedCount = group.items.length - includedCount;
  return (
    <section
      style={{
        borderTop: "1px solid var(--border)",
        display: "grid",
        gap: 8,
        paddingTop: 10,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
        <div className="kicker" style={{ margin: 0 }}>
          {humanSectionLabel(group.section)}
        </div>
        <span className="badge badge-muted">{group.items.length}</span>
        {includedCount > 0 && <Badge status="ok" label={`${includedCount} included`} />}
        {excludedCount > 0 && <Badge status="disabled" label={`${excludedCount} inspect-only`} />}
      </div>
      <div style={{ display: "grid", gap: 8 }}>
        {group.items.map((item, index) => (
          <ContextItemRow key={`${item.kind}-${item.origin}-${index}`} item={item} />
        ))}
      </div>
    </section>
  );
}

function ContextItemRow({ item }: { item: ContextPacketItemRecord }) {
  const detail = [item.origin, item.body_ref].filter(Boolean).join(" · ");
  const note = item.body?.trim() || item.inclusion_reason?.trim();
  return (
    <div
      style={{
        display: "grid",
        gap: 5,
        padding: "8px 10px",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        background: "var(--bg2)",
      }}
    >
      <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
        <div style={{ color: "var(--t0)", fontFamily: "var(--font-mono)", fontSize: 11 }}>
          {item.title || item.kind}
        </div>
        <Badge
          status={item.included ? "ok" : "disabled"}
          label={item.included ? "included" : "inspect-only"}
        />
        <span className="badge badge-muted">{humanTrustLevel(item.trust_level)}</span>
      </div>
      {detail && (
        <div style={{ color: "var(--t3)", fontSize: 11, fontFamily: "var(--font-mono)" }}>
          {detail}
        </div>
      )}
      {note && (
        <div style={{ color: "var(--t2)", fontSize: 12, lineHeight: 1.5, whiteSpace: "pre-wrap" }}>
          {note}
        </div>
      )}
    </div>
  );
}

function ContextSourceRow({ source }: { source: ContextPacketSourceRecord }) {
  const detail = source.detail?.trim();
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        background: "var(--bg2)",
        display: "grid",
        gap: 4,
        padding: "8px 10px",
      }}
    >
      <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
        <div style={{ color: "var(--t0)", fontFamily: "var(--font-mono)", fontSize: 11 }}>
          {source.label || source.kind}
        </div>
        {source.trust && <span className="badge badge-muted">{source.trust}</span>}
      </div>
      {detail && <div style={{ color: "var(--t3)", fontSize: 11 }}>{detail}</div>}
    </div>
  );
}

function ContextInspectorNotice({
  title,
  detail,
  tone,
}: {
  title: string;
  detail: string;
  tone: "muted" | "warning";
}) {
  return (
    <div
      style={{
        background: tone === "warning" ? "var(--amber-bg)" : "var(--bg2)",
        border: `1px solid ${tone === "warning" ? "var(--amber-border)" : "var(--border)"}`,
        borderRadius: "var(--radius-sm)",
        display: "grid",
        gap: 6,
        padding: "10px 12px",
      }}
    >
      <div style={{ color: tone === "warning" ? "var(--amber)" : "var(--t1)", fontWeight: 500 }}>
        {title}
      </div>
      <div style={{ color: tone === "warning" ? "var(--amber-lo)" : "var(--t2)", fontSize: 12 }}>
        {detail}
      </div>
    </div>
  );
}

function contextInspectorSummary(packet: ContextPacketRecord): string {
  const modelLabel = [packet.provider, packet.model].filter(Boolean).join(" · ");
  const summaryParts = [
    "what the agent saw",
    packet.message_count
      ? `${packet.message_count} message${packet.message_count === 1 ? "" : "s"}`
      : "",
    modelLabel,
  ].filter(Boolean);
  return summaryParts.join(" · ");
}

function groupContextItemsBySection(items: ContextPacketItemRecord[]): ContextSectionGroupRecord[] {
  const groups = new Map<string, ContextPacketItemRecord[]>();
  for (const item of items) {
    const section = item.section || "runtime";
    const group = groups.get(section) ?? [];
    group.push(item);
    groups.set(section, group);
  }
  return Array.from(groups.entries())
    .sort(([left], [right]) => sectionSortIndex(left) - sectionSortIndex(right))
    .map(([section, groupedItems]) => ({ section, items: groupedItems }));
}

function sectionSortIndex(section: string): number {
  const index = sectionOrder.indexOf(section as (typeof sectionOrder)[number]);
  return index === -1 ? sectionOrder.length + 1 : index;
}

function visibleRefs(refs?: ContextPacketRefsRecord): Array<[string, string]> {
  if (!refs) return [];
  return refOrder
    .map(([key, label]) => [label, refs[key]?.trim() || ""] as [string, string])
    .filter(([, value]) => value !== "");
}

function hasRefs(refs?: ContextPacketRefsRecord): boolean {
  return visibleRefs(refs).length > 0;
}

function humanExecutionMode(mode: string): string {
  switch (mode) {
    case "external_agent":
      return "External agent";
    case "hecate_task":
      return "Hecate task runtime";
    default:
      return mode;
  }
}

function humanSectionLabel(section: string): string {
  switch (section) {
    case "instructions":
      return "Instructions";
    case "project":
      return "Project";
    case "project_work":
      return "Launch context";
    case "memory":
      return "Memory";
    case "sources":
      return "Context sources";
    case "workspace":
      return "Workspace";
    case "runtime":
      return "Runtime";
    default:
      return section.replaceAll("_", " ");
  }
}

function humanTrustLevel(level: string): string {
  switch (level) {
    case "system_instruction":
      return "system instruction";
    case "operator_memory":
      return "operator memory";
    case "workspace_guidance":
      return "workspace guidance";
    case "runtime_state":
      return "runtime state";
    case "tool_output":
      return "tool output";
    case "generated_summary":
      return "generated summary";
    case "external_untrusted":
      return "external untrusted";
    default:
      return level.replaceAll("_", " ");
  }
}
