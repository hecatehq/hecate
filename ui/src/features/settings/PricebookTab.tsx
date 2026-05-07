import React, { useEffect, useMemo, useRef, useState } from "react";
import type { RuntimeConsoleViewModel } from "../../app/useRuntimeConsole";
import type {
  ConfiguredAuditEventRecord,
  ConfiguredPricebookRecord,
  PricebookImportDiff,
  PricebookImportFailureRecord,
} from "../../types/runtime";
import { Badge, ConfirmModal, Icon, Icons, InlineError, Modal, ProviderPicker } from "../shared/ui";
import type { ProviderOption } from "../shared/ui";

type Props = {
  state: RuntimeConsoleViewModel["state"];
  actions: RuntimeConsoleViewModel["actions"];
};

// formatPricePerMillion converts micros-USD-per-Mtok to a human "$0.150 / 1M"
// label. The display unit is dollars-per-million-tokens, with 3 decimals so
// sub-cent-per-Mtok prices (e.g. some Groq cached reads) don't collapse to "$0".
export function formatPricePerMillion(micros: number): string {
  if (!Number.isFinite(micros) || micros <= 0) return "—";
  const dollars = micros / 1_000_000;
  return `$${dollars.toFixed(3)} / 1M`;
}

// dollarsToMicros parses a dollar string from the form input back to micros.
// Accepts "0.15", "$0.15", " 0.15 / 1M ", etc. Returns null on invalid input
// so the caller can surface a validation error instead of silently writing 0.
//
// Implementation note: we extract the first decimal-looking number with a
// regex rather than stripping non-digit chars, so "0.15 / 1M" matches "0.15"
// and not "0.151" (which would happen if we naively strip the slash).
export function dollarsToMicros(input: string): number | null {
  const trimmed = input.trim();
  if (trimmed === "") return null;
  const match = trimmed.match(/-?\d+(?:\.\d+)?/);
  if (!match) return null;
  const n = Number(match[0]);
  if (!Number.isFinite(n) || n < 0) return null;
  return Math.round(n * 1_000_000);
}

// formatPriceCompact is like formatPricePerMillion but drops the "/ 1M"
// suffix — the consent dialog puts in/out/cache labels in front of each
// value, so the unit is already implied. Result: "$0.150" or "—".
function formatPriceCompact(micros: number): string {
  if (!Number.isFinite(micros) || micros <= 0) return "—";
  const dollars = micros / 1_000_000;
  return `$${dollars.toFixed(3)}`;
}

// describeAddedDetail formats a brand-new entry's price line. All three
// fields are shown so the operator can scan what they're about to add.
export function describeAddedDetail(r: ConfiguredPricebookRecord): string {
  return [
    `in ${formatPriceCompact(r.input_micros_usd_per_million_tokens)}`,
    `out ${formatPriceCompact(r.output_micros_usd_per_million_tokens)}`,
    `cache ${formatPriceCompact(r.cached_input_micros_usd_per_million_tokens)}`,
  ].join("  ");
}

// describeUpdatedDetail shows only the fields that actually changed
// between previous and incoming. Without this filter, an updated row
// where only the cache price moved looks identical to a no-op
// ("$0.100 → $0.100") and the operator can't tell what's different.
export function describeUpdatedDetail(prev: ConfiguredPricebookRecord, next: ConfiguredPricebookRecord): string {
  const parts: string[] = [];
  if (prev.input_micros_usd_per_million_tokens !== next.input_micros_usd_per_million_tokens) {
    parts.push(`in ${formatPriceCompact(prev.input_micros_usd_per_million_tokens)} → ${formatPriceCompact(next.input_micros_usd_per_million_tokens)}`);
  }
  if (prev.output_micros_usd_per_million_tokens !== next.output_micros_usd_per_million_tokens) {
    parts.push(`out ${formatPriceCompact(prev.output_micros_usd_per_million_tokens)} → ${formatPriceCompact(next.output_micros_usd_per_million_tokens)}`);
  }
  if (prev.cached_input_micros_usd_per_million_tokens !== next.cached_input_micros_usd_per_million_tokens) {
    parts.push(`cache ${formatPriceCompact(prev.cached_input_micros_usd_per_million_tokens)} → ${formatPriceCompact(next.cached_input_micros_usd_per_million_tokens)}`);
  }
  // Defensive: the backend already filters identical rows into
  // `unchanged`, but if anything ever does slip through we don't want
  // an empty detail string masquerading as "no change".
  return parts.length === 0 ? "no change" : parts.join("  ");
}

function pricebookKey(provider: string, model: string): string {
  return `${provider}/${model}`;
}

// formatRelativeTime renders an RFC3339 timestamp as a coarse relative
// hint ("2m ago", "3h ago", "yesterday", "5d ago"). The consent dialog
// uses this to tell the operator how stale the proposal set is at a
// glance — exact timestamps go in the title attribute for hover.
//
// Exported so unit tests can pin down the boundary cases.
export function formatRelativeTime(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const diffSec = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (diffSec < 60) return "just now";
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`;
  if (diffSec < 86_400) return `${Math.floor(diffSec / 3600)}h ago`;
  if (diffSec < 86_400 * 2) return "yesterday";
  return `${Math.floor(diffSec / 86_400)}d ago`;
}

// formatAbsoluteTime renders an RFC3339 timestamp as a short local
// time string suitable for inline display. Used alongside the relative
// hint so the operator can see both the "how long ago" and the exact
// wall-clock time without hovering.
//
// Exported alongside formatRelativeTime for symmetry.
export function formatAbsoluteTime(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  return new Date(t).toLocaleString(undefined, {
    month: "short", day: "numeric",
    hour: "numeric", minute: "2-digit",
  });
}

// UnifiedRow is the central data structure: every row in the table is
// one of these, regardless of whether it has a price or where it came
// from. Three statuses cover the full surface:
//   priced     — model is in the live catalog AND has a pricebook entry.
//   unpriced   — model is in the live catalog with NO pricebook entry.
//   deprecated — model is in the pricebook but no longer in the catalog
//                (provider stopped offering it; we keep historical pricing
//                so old usage still attributes correctly).
type RowStatus = "priced" | "unpriced" | "deprecated";

type UnifiedRow = {
  key: string;
  provider: string;
  providerName: string;
  model: string;
  status: RowStatus;
  // Pricebook record — present for `priced` and `deprecated` rows.
  entry?: ConfiguredPricebookRecord;
  // LiteLLM-proposed price for this (provider, model). Drives the
  // inline "Import" button on unpriced rows.
  litellmEntry?: ConfiguredPricebookRecord;
};

type StatusFilter = "all" | RowStatus;

export function PricebookTab({ state, actions }: Props) {
  const rows = state.settingsConfig?.pricebook ?? [];
  const [editingKey, setEditingKey] = useState<string | null>(null);
  // Filter state.
  const [providerFilter, setProviderFilter] = useState<string>("auto");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [search, setSearch] = useState("");
  // Lazy-loaded LiteLLM diff: powers both the per-row "Import" button
  // and the bulk consent dialog. Failure is silent — the inline "Set
  // price" path still works without it.
  const [litellmDiff, setLitellmDiff] = useState<PricebookImportDiff | null>(null);
  const [diffError, setDiffError] = useState<string>("");
  const [consentOpen, setConsentOpen] = useState(false);
  // Pending Clear-price confirmation: the row whose broom button was
  // clicked. ConfirmModal renders while non-null.
  const [clearingRow, setClearingRow] = useState<{ provider: string; model: string } | null>(null);
  const [clearingPending, setClearingPending] = useState(false);
  // Pending per-row Import confirmation. Same UX shape as Clear: the
  // row button just *opens* the modal; the modal's Confirm button
  // runs the apply. Carries the full UnifiedRow so the modal can
  // render a price-diff body without recomputing it.
  const [importingRow, setImportingRow] = useState<UnifiedRow | null>(null);
  const [importingPending, setImportingPending] = useState(false);
  // Audit-history viewer. Click the row's clock icon → opens a modal
  // listing every audit event whose target_id matches "provider/model".
  // We filter client-side from settingsConfig.events because that surface
  // already streams the full event log; no separate endpoint needed.
  const [historyRow, setHistoryRow] = useState<{ provider: string; model: string } | null>(null);

  // Refetch the LiteLLM diff whenever the pricebook changes.
  //
  // Why this matters: the backend's preview computes the diff against
  // the *current* pricebook. A model that already has a price gets
  // bucketed into `unchanged` / `updated` / `skipped` — none of which
  // give us LiteLLM's price data on the row when the price is cleared.
  // Without a refetch, clearing a price leaves the row "unpriced" but
  // with no LiteLLM data in the cache, so "Import all" stays disabled
  // and the inline Import button never appears.
  //
  // `state.settingsConfig?.pricebook` is freshly allocated by every
  // `loadDashboard()` call (which runs after every settings mutation),
  // so depending on it is enough to refire after delete / upsert / apply.
  useEffect(() => {
    let cancelled = false;
    actions.previewPricebookImport()
      .then(d => {
        if (cancelled) return;
        setLitellmDiff(d);
        setDiffError("");
      })
      .catch(err => { if (!cancelled) setDiffError(err instanceof Error ? err.message : "Failed to load LiteLLM data."); });
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.settingsConfig?.pricebook]);

  // Local-provider id set: pricebook hides these entirely. Local
  // providers (ollama, llamacpp, lmstudio, localai) are always free —
  // showing them in a price table is just noise.
  const localProviders = useMemo(() => {
    const set = new Set<string>();
    for (const p of state.providerPresets ?? []) {
      if (p.kind === "local") set.add(p.id);
    }
    for (const p of state.settingsConfig?.providers ?? []) {
      if (p.kind === "local") set.add(p.id);
    }
    return set;
  }, [state.providerPresets, state.settingsConfig?.providers]);

  // Build a key→litellm-record index from the diff. All three diff
  // sections contribute LiteLLM's proposed price:
  //   - added:   model not in pricebook → LiteLLM proposes a new entry
  //   - updated: imported pricebook entry differs from LiteLLM
  //   - skipped: manual pricebook entry differs from LiteLLM (manual
  //              rows are protected from blanket apply, but the UI
  //              offers an explicit "Replace manual" affordance and
  //              still wants to know what LiteLLM would set)
  // For `updated` and `skipped` we read `entry` (the proposal), not
  // `previous` (the current row). The `if (!u?.entry) continue` guard
  // catches a stale gateway returning the old `skipped: []record`
  // shape — defensive so a shape mismatch never crashes the page.
  const litellmByKey = useMemo(() => {
    const m = new Map<string, ConfiguredPricebookRecord>();
    for (const r of litellmDiff?.added ?? []) m.set(pricebookKey(r.provider, r.model), r);
    for (const u of litellmDiff?.updated ?? []) {
      if (!u?.entry) continue;
      m.set(pricebookKey(u.entry.provider, u.entry.model), u.entry);
    }
    for (const u of litellmDiff?.skipped ?? []) {
      if (!u?.entry) continue;
      m.set(pricebookKey(u.entry.provider, u.entry.model), u.entry);
    }
    return m;
  }, [litellmDiff]);

  // Display name for a provider id. Falls back to the bare id when no
  // preset matches (custom providers added by operators).
  const providerName = useMemo(() => {
    const presetById = new Map<string, string>();
    for (const p of state.providerPresets ?? []) presetById.set(p.id, p.name);
    for (const p of state.settingsConfig?.providers ?? []) {
      if (!presetById.has(p.id)) presetById.set(p.id, p.name || p.id);
    }
    return (id: string) => presetById.get(id) ?? id;
  }, [state.providerPresets, state.settingsConfig?.providers]);

  // Provider options for the filter dropdown — every CLOUD provider
  // that appears in either the catalog or the pricebook. Local providers
  // are excluded; deprecated cloud providers (gone from catalog but
  // still in pricebook) stay so operators can find their rows.
  const providerOptions = useMemo<ProviderOption[]>(() => {
    const ids = new Set<string>();
    for (const m of state.models) {
      const p = m.metadata?.provider;
      if (p && !localProviders.has(p)) ids.add(p);
    }
    for (const r of rows) {
      if (!localProviders.has(r.provider)) ids.add(r.provider);
    }
    return [...ids]
      .map(id => ({ id, name: providerName(id) }))
      .sort((a, b) => a.name.localeCompare(b.name));
  }, [state.models, rows, providerName, localProviders]);

  // Build the unified row set: every (provider, model) pair the gateway
  // knows about — either from the live catalog or from a stored
  // pricebook entry. Catalog seeds the rows; pricebook entries promote
  // them to `priced` or, if not in the catalog, append as `deprecated`.
  // Local-provider rows are excluded entirely.
  const unified = useMemo<UnifiedRow[]>(() => {
    const byKey = new Map<string, UnifiedRow>();
    for (const m of state.models) {
      const provider = m.metadata?.provider;
      if (!provider) continue;
      if (localProviders.has(provider)) continue;
      const key = pricebookKey(provider, m.id);
      if (byKey.has(key)) continue;
      byKey.set(key, {
        key,
        provider,
        providerName: providerName(provider),
        model: m.id,
        status: "unpriced",
        litellmEntry: litellmByKey.get(key),
      });
    }
    for (const r of rows) {
      if (localProviders.has(r.provider)) continue;
      const key = pricebookKey(r.provider, r.model);
      const existing = byKey.get(key);
      if (existing) {
        existing.status = "priced";
        existing.entry = r;
      } else {
        byKey.set(key, {
          key,
          provider: r.provider,
          providerName: providerName(r.provider),
          model: r.model,
          status: "deprecated",
          entry: r,
          litellmEntry: litellmByKey.get(key),
        });
      }
    }
    return [...byKey.values()];
  }, [state.models, rows, litellmByKey, providerName, localProviders]);

  // Apply the three filters: provider, status, free-text search.
  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return unified.filter(r => {
      if (providerFilter !== "auto" && r.provider !== providerFilter) return false;
      if (statusFilter !== "all" && r.status !== statusFilter) return false;
      if (q !== "") {
        if (
          !r.model.toLowerCase().includes(q) &&
          !r.providerName.toLowerCase().includes(q) &&
          !r.provider.toLowerCase().includes(q)
        ) {
          return false;
        }
      }
      return true;
    });
  }, [unified, providerFilter, statusFilter, search]);

  // Group filtered rows by provider; sort groups by display name and
  // models within each group alphabetically.
  const grouped = useMemo(() => {
    const map = new Map<string, UnifiedRow[]>();
    for (const r of filtered) {
      const list = map.get(r.provider) ?? [];
      list.push(r);
      map.set(r.provider, list);
    }
    return [...map.entries()]
      .map(([providerId, entries]) => ({
        providerId,
        providerName: providerName(providerId),
        entries: [...entries].sort((a, b) => a.model.localeCompare(b.model)),
      }))
      .sort((a, b) => a.providerName.localeCompare(b.providerName));
  }, [filtered, providerName]);

  // Per-status counts for the status tabs. Counted off `unified` so
  // they reflect the post-local-filter universe, not raw catalog size.
  const statusCounts = useMemo(() => {
    let priced = 0, unpriced = 0, deprecated = 0;
    for (const r of unified) {
      if (r.status === "priced") priced++;
      else if (r.status === "unpriced") unpriced++;
      else deprecated++;
    }
    return { all: unified.length, priced, unpriced, deprecated };
  }, [unified]);

  // rowImportable returns true when LiteLLM has a proposal for the row
  // AND it differs from the current entry (or the row is unpriced).
  // Used by both the inline LiteLLM column and the bulk-import counter.
  function rowImportable(r: UnifiedRow): boolean {
    if (!r.litellmEntry) return false;
    if (!r.entry) return true; // unpriced — anything LiteLLM has is new
    const a = r.entry, b = r.litellmEntry;
    return (
      a.input_micros_usd_per_million_tokens !== b.input_micros_usd_per_million_tokens ||
      a.output_micros_usd_per_million_tokens !== b.output_micros_usd_per_million_tokens ||
      a.cached_input_micros_usd_per_million_tokens !== b.cached_input_micros_usd_per_million_tokens
    );
  }

  // Bulk count is scoped to the *filtered* row set. The consent dialog
  // still shows every importable change (so the operator can review the
  // full universe), but the button counter only reflects what matches
  // the active filter — and those are the rows pre-selected when the
  // dialog opens.
  const filteredImportableKeys = useMemo(() => {
    const keys = new Set<string>();
    for (const r of filtered) {
      if (rowImportable(r)) keys.add(r.key);
    }
    return keys;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filtered]);
  const bulkImportable = filteredImportableKeys.size;


  // Status tabs: same visual language as the page-level Settings tabs
  // (mono, uppercase, teal underline on active). Counts inline so the
  // operator can see status distribution at a glance.
  const statusTabs: Array<{ value: StatusFilter; label: string; count: number }> = [
    { value: "all",        label: "all",        count: statusCounts.all },
    { value: "priced",     label: "priced",     count: statusCounts.priced },
    { value: "unpriced",   label: "unpriced",   count: statusCounts.unpriced },
    { value: "deprecated", label: "deprecated", count: statusCounts.deprecated },
  ];

  return (
    <>
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 12 }}>
        <span style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)" }}>Pricing</span>
        <span style={{ fontSize: 11, color: "var(--t3)" }}>Manage cloud-model pricebook entries and import upstream pricing proposals.</span>
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>{unified.length} models</span>
      </div>

      {/* Status tabs — primary segmentation. Border-bottom on the
          container makes the active tab's underline merge with the
          page rule, matching the SettingsView tab visual. */}
      <div
        role="tablist"
        aria-label="Pricebook status"
        style={{ display: "flex", gap: 2, borderBottom: "1px solid var(--border)", marginBottom: 12 }}>
        {statusTabs.map(t => {
          const active = statusFilter === t.value;
          return (
            <button
              key={t.value}
              type="button"
              role="tab"
              aria-label={t.label}
              aria-selected={active}
              onClick={() => setStatusFilter(t.value)}
              style={{
                padding: "7px 12px",
                fontSize: 12,
                fontFamily: "var(--font-mono)",
                background: "none",
                border: "none",
                borderBottom: active ? "2px solid var(--teal)" : "2px solid transparent",
                color: active ? "var(--teal)" : "var(--t2)",
                cursor: "pointer",
                marginBottom: -1,
                textTransform: "uppercase",
                letterSpacing: "0.04em",
                display: "flex",
                alignItems: "center",
                gap: 6,
              }}>
              {t.label}
              <span style={{ fontSize: 10, opacity: 0.7 }}>{t.count}</span>
            </button>
          );
        })}
      </div>

      {/* Filter row: provider + search + Import-all on one line. */}
      <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 12, flexWrap: "wrap" }}>
        <ProviderPicker
          value={providerFilter}
          onChange={setProviderFilter}
          options={providerOptions}
          includeAuto
        />
        <input
          className="input"
          placeholder="Search models…"
          value={search}
          onChange={e => setSearch(e.target.value)}
          aria-label="Search models"
          style={{ flex: 1, minWidth: 160, maxWidth: 280 }}
        />
        {/* Fetched-at hint sits just before the Import all button so
            the operator can see at a glance how stale the proposal
            set is. Hover for the exact timestamp. */}
        {litellmDiff?.fetched_at && (
          <span
            className="kicker"
            title={`Fetched at ${litellmDiff.fetched_at}`}
            style={{ marginLeft: "auto" }}>
            fetched {formatRelativeTime(litellmDiff.fetched_at)}
          </span>
        )}
        <button
          className="btn btn-primary btn-sm"
          // Fixed width matches the per-row Apply button so the
          // "Import all" stack at the right edge reads as one column
          // of buttons, not a ragged set of differently-sized chips.
          style={{ marginLeft: litellmDiff?.fetched_at ? 0 : "auto", width: 130, justifyContent: "center" }}
          disabled={!litellmDiff || bulkImportable === 0}
          title={
            !litellmDiff ? "Loading LiteLLM data…" :
            bulkImportable === 0 ? "No LiteLLM updates available" :
            `${bulkImportable} change${bulkImportable === 1 ? "" : "s"} available`
          }
          onClick={() => setConsentOpen(true)}>
          <Icon d={Icons.refresh} size={13} /> Import all
          {bulkImportable > 0 && (
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, marginLeft: 4, opacity: 0.85 }}>
              ({bulkImportable})
            </span>
          )}
        </button>
      </div>

      {state.settingsError && (
        <div style={{ marginBottom: 8 }}>
          <InlineError message={state.settingsError} />
        </div>
      )}
      {diffError && (
        <div style={{ marginBottom: 8 }}>
          <InlineError message={`LiteLLM unavailable: ${diffError}`} />
        </div>
      )}

      {grouped.length > 0 ? (
        <div style={{ display: "flex", flexDirection: "column", gap: 14 }}>
          {grouped.map(group => (
            <div key={group.providerId}>
              <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 6 }}>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--teal)", fontWeight: 500 }}>{group.providerName}</span>
                <span style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>
                  {group.entries.length} {group.entries.length === 1 ? "model" : "models"}
                </span>
              </div>
              <div className="card" style={{ overflow: "hidden" }}>
                {/*
                  Fixed column widths so model/price/status/action columns
                  align across every per-provider table on the page. Without
                  this, each table sizes columns to its own widest cell and
                  the columns wander vertically as the eye scans down.
                  table-layout: fixed honors <colgroup> widths.
                */}
                <table className="table" style={{ tableLayout: "fixed" }}>
                  <colgroup>
                    <col />
                    <col style={{ width: 120 }} />
                    <col style={{ width: 120 }} />
                    <col style={{ width: 120 }} />
                    <col style={{ width: 110 }} />
                    <col style={{ width: 90 }} />
                    <col style={{ width: 130 }} />
                  </colgroup>
                  <thead>
                    <tr>
                      <th>Model</th>
                      <th>Input ($/1M)</th>
                      <th>Output ($/1M)</th>
                      <th>Cached ($/1M)</th>
                      <th>Status</th>
                      <th>Manage</th>
                      {/* Last column carries the per-row LiteLLM Import
                          button. Header is intentionally blank — the
                          buttons themselves carry the action verb, and
                          the column lines up vertically with the
                          "Import all" button in the filter row above. */}
                      <th></th>
                    </tr>
                  </thead>
                  <tbody>
                    {group.entries.map(row => {
                      const editing = editingKey === row.key;
                      if (editing) {
                        const seed: ConfiguredPricebookRecord = row.entry ?? {
                          provider: row.provider,
                          model: row.model,
                          input_micros_usd_per_million_tokens: 0,
                          output_micros_usd_per_million_tokens: 0,
                          cached_input_micros_usd_per_million_tokens: 0,
                          source: "manual",
                        };
                        return (
                          <PricebookEditRow
                            key={row.key}
                            row={seed}
                            isNew={!row.entry}
                            onCancel={() => setEditingKey(null)}
                            onSave={async patch => {
                              await actions.upsertPricebookEntry(patch);
                              setEditingKey(null);
                            }}
                          />
                        );
                      }
                      return (
                        <PricebookViewRow
                          key={row.key}
                          row={row}
                          importable={rowImportable(row)}
                          onSetPrice={() => setEditingKey(row.key)}
                          onImport={() => setImportingRow(row)}
                          onEdit={() => setEditingKey(row.key)}
                          onDelete={() => row.entry && setClearingRow({ provider: row.entry.provider, model: row.entry.model })}
                          onHistory={() => setHistoryRow({ provider: row.provider, model: row.model })}
                        />
                      );
                    })}
                  </tbody>
                </table>
              </div>
            </div>
          ))}
        </div>
      ) : (
        <div className="card" style={{ padding: "24px", textAlign: "center", color: "var(--t3)", fontSize: 12 }}>
          {unified.length === 0
            ? "No models known to the gateway yet. Configure a provider to populate the catalog."
            : "No models match the current filters."}
        </div>
      )}

      {consentOpen && litellmDiff && (
        <PricebookImportConsent
          diff={litellmDiff}
          unified={unified}
          // Pre-select the rows matching the active filter — operator
          // can still review the full universe of changes in the
          // dialog and toggle anything they want.
          initialSelectedKeys={filteredImportableKeys}
          providerName={providerName}
          onClose={() => setConsentOpen(false)}
          onConfirm={async keys => {
            // applyPricebookImport returns the diff — including any
            // per-row failures. If everything landed cleanly we close
            // the dialog; if anything failed, hand the failures back
            // so the dialog can render them inline and stay open.
            const result = await actions.applyPricebookImport(keys);
            if (!result.failed || result.failed.length === 0) {
              setConsentOpen(false);
            }
            return result;
          }}
        />
      )}

      {importingRow && importingRow.litellmEntry && (
        <ConfirmModal
          title="Import price update"
          message={
            <>
              <div style={{ marginBottom: 12 }}>
                {importingRow.entry?.source === "manual" ? (
                  <>Replace manual price for </>
                ) : importingRow.entry ? (
                  <>Update price for </>
                ) : (
                  <>Import price for </>
                )}
                <code style={{ fontFamily: "var(--font-mono)", color: "var(--t0)" }}>
                  {importingRow.provider}/{importingRow.model}
                </code>
                ?
              </div>
              <PriceDiffPanel previous={importingRow.entry} next={importingRow.litellmEntry} />
              {importingRow.entry?.source === "manual" && (
                <div style={{ fontSize: 11, color: "var(--amber)", marginTop: 12, lineHeight: 1.5 }}>
                  This row's <strong>source</strong> flips from <code style={{ fontFamily: "var(--font-mono)" }}>manual</code> to <code style={{ fontFamily: "var(--font-mono)" }}>imported</code>. Future bulk imports will keep it in sync with the published price unless you edit it again.
                </div>
              )}
            </>
          }
          confirmLabel="Import"
          pending={importingPending}
          onClose={() => { if (!importingPending) setImportingRow(null); }}
          onConfirm={async () => {
            setImportingPending(true);
            try {
              await actions.applyPricebookImport([importingRow.key]);
              setImportingRow(null);
            } finally {
              setImportingPending(false);
            }
          }}
        />
      )}

      {clearingRow && (
        <ConfirmModal
          title="Clear price"
          message={
            <>
              Clear pricebook entry for{" "}
              <code style={{ fontFamily: "var(--font-mono)", color: "var(--t0)" }}>
                {clearingRow.provider}/{clearingRow.model}
              </code>
              ? The model stays in the catalog as <em>unpriced</em> until you set
              a new price or import an update.
            </>
          }
          confirmLabel="Clear price"
          danger
          pending={clearingPending}
          onClose={() => { if (!clearingPending) setClearingRow(null); }}
          onConfirm={async () => {
            setClearingPending(true);
            try {
              await actions.deletePricebookEntry(clearingRow.provider, clearingRow.model);
              setClearingRow(null);
            } finally {
              setClearingPending(false);
            }
          }}
        />
      )}

      {historyRow && (
        <PricebookHistoryModal
          provider={historyRow.provider}
          model={historyRow.model}
          events={state.settingsConfig?.events ?? []}
          onClose={() => setHistoryRow(null)}
        />
      )}
    </>
  );
}

// ─── View row ────────────────────────────────────────────────────────────────

function PricebookViewRow({
  row,
  importable,
  onSetPrice,
  onImport,
  onEdit,
  onDelete,
  onHistory,
}: {
  row: UnifiedRow;
  // True when LiteLLM has a proposal for this row that differs from
  // the current entry (or the row is unpriced). Computed by the parent
  // so it stays consistent with the bulk count.
  importable: boolean;
  onSetPrice: () => void;
  onImport: () => void;
  onEdit: () => void;
  onDelete: () => void;
  onHistory: () => void;
}) {
  const dim = row.status === "deprecated";
  const e = row.entry;
  // Hover detail for the Import button — describes what'll change so
  // the operator can preview without opening the modal. The modal
  // body re-uses the same diff text in larger form.
  const importTitle = (() => {
    if (!row.litellmEntry || !importable) return "";
    if (!e) return `Import price: ${describeAddedDetail(row.litellmEntry)}`;
    if (e.source === "manual") return `Replace manual price: ${describeUpdatedDetail(e, row.litellmEntry)}`;
    return `Update price: ${describeUpdatedDetail(e, row.litellmEntry)}`;
  })();
  return (
    <tr style={dim ? { opacity: 0.6 } : undefined}>
      <td className="mono" style={{ color: dim ? "var(--t3)" : "var(--t0)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={row.model}>
        {row.model}
      </td>
      <td className="mono">{e ? formatPricePerMillion(e.input_micros_usd_per_million_tokens) : "—"}</td>
      <td className="mono">{e ? formatPricePerMillion(e.output_micros_usd_per_million_tokens) : "—"}</td>
      <td className="mono">{e ? formatPricePerMillion(e.cached_input_micros_usd_per_million_tokens) : "—"}</td>
      <td>
        <StatusBadge row={row} />
      </td>
      <td>
        {/* minHeight pins the action cell so rows with icon-only buttons
            stay the same height as rows with text-button "Set price". */}
        <div style={{ display: "flex", gap: 4, justifyContent: "flex-end", alignItems: "center", minHeight: 22 }}>
          {row.status === "unpriced" && (
            <button
              className="btn btn-sm"
              style={{ padding: "3px 8px", fontSize: 11 }}
              onClick={onSetPrice}
              title="Set price manually"
              aria-label={`Set price for ${row.provider}/${row.model}`}>
              Set price
            </button>
          )}
          {(row.status === "priced" || row.status === "deprecated") && (
            <>
              <button
                className="btn btn-ghost btn-sm"
                style={{ padding: "3px 6px" }}
                onClick={onHistory}
                title="View price history"
                aria-label={`History for ${row.provider}/${row.model}`}>
                <Icon d={Icons.activity} size={12} />
              </button>
              <button
                className="btn btn-ghost btn-sm"
                style={{ padding: "3px 6px" }}
                onClick={onEdit}
                title="Edit price"
                aria-label={`Edit ${row.provider}/${row.model}`}>
                <Icon d={Icons.edit} size={12} />
              </button>
              <button
                className="btn btn-ghost btn-sm"
                style={{ color: "var(--red)", padding: "3px 6px" }}
                onClick={onDelete}
                title="Clear price"
                aria-label={`Clear ${row.provider}/${row.model}`}>
                <Icon d={Icons.broom} size={13} />
              </button>
            </>
          )}
        </div>
      </td>
      {/* "From LiteLLM" — last column, button right-aligned so the
          stack lines up vertically with the "Import all" button in
          the filter row above. The verb itself describes the action
          (Import / Update / Replace) so the column read is "what
          would LiteLLM do to this row?" at a glance. */}
      <td>
        <div style={{ display: "flex", justifyContent: "flex-end", alignItems: "center", minHeight: 22 }}>
          {/* Non-importable rows render an empty cell — no dash, no
              placeholder. Less visual noise on rows where there's
              nothing to do; the eye scans only over actionable rows. */}
          {importable && (
            <button
              className="btn btn-primary btn-sm"
              // Same fixed width as the "Import all" button above so
              // the right-aligned button stack reads as a column.
              style={{ width: 130, justifyContent: "center", fontSize: 11 }}
              onClick={onImport}
              title={importTitle}
              aria-label={`Import update for ${row.provider}/${row.model}`}>
              Import
            </button>
          )}
        </div>
      </td>
    </tr>
  );
}

// StatusBadge merges the row's status with the pricebook source so the
// operator gets a single chip instead of two redundant columns. The
// shape:
//   priced + imported → "imported" (green)
//   priced + manual   → "manual"   (blue)
//   unpriced          → "unpriced" (muted)
//   deprecated        → "deprecated" (amber) — we drop the source for
//                       deprecated rows because the foreground signal is
//                       "this model is gone", not how its price was set.
function StatusBadge({ row }: { row: UnifiedRow }) {
  if (row.status === "deprecated") {
    return <Badge status="warn" label="deprecated" />;
  }
  if (row.status === "unpriced") {
    return <Badge status="disabled" label="unpriced" />;
  }
  const source = row.entry?.source === "imported" ? "imported" : "manual";
  return <Badge status={source === "imported" ? "enabled" : "healthy"} label={source} />;
}

// ─── Edit row ────────────────────────────────────────────────────────────────

function PricebookEditRow({
  row,
  isNew,
  onCancel,
  onSave,
}: {
  row: ConfiguredPricebookRecord;
  // isNew = true when this edit-row is being used to set a price for an
  // unpriced model. The seed values come in as zeros; we render them as
  // empty strings so the operator can type without backspacing first.
  isNew: boolean;
  onCancel: () => void;
  onSave: (patch: ConfiguredPricebookRecord) => Promise<void>;
}) {
  const initial = (micros: number) =>
    isNew && micros === 0 ? "" : (micros / 1_000_000).toFixed(3);
  const [input, setInput] = useState(initial(row.input_micros_usd_per_million_tokens));
  const [output, setOutput] = useState(initial(row.output_micros_usd_per_million_tokens));
  const [cached, setCached] = useState(initial(row.cached_input_micros_usd_per_million_tokens));
  const [error, setError] = useState("");

  // Auto-focus the first input when entering edit mode so the operator
  // can start typing immediately. For Edit (non-new) we also select the
  // existing value so a single keystroke replaces it.
  const firstInputRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const el = firstInputRef.current;
    if (!el) return;
    el.focus();
    if (!isNew) el.select();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Inline style shared by all three price inputs. Total height matches
  // the view-row's content height (22px) so the row doesn't visually
  // jump when toggling between view and edit modes. Without this, the
  // default .input padding (6px) blows the row up by ~6-8px.
  const inputStyle: React.CSSProperties = {
    fontFamily: "var(--font-mono)",
    width: 80,
    height: 22,
    padding: "0 8px",
    fontSize: 11,
    boxSizing: "border-box",
  };

  async function save() {
    const inputMicros = dollarsToMicros(input || "0");
    const outputMicros = dollarsToMicros(output || "0");
    const cachedMicros = dollarsToMicros(cached || "0");
    if (inputMicros === null || outputMicros === null || cachedMicros === null) {
      setError("Prices must be non-negative numbers.");
      return;
    }
    setError("");
    // Edits and new manual entries always persist as `manual` — operator
    // intent overrides any future LiteLLM import. The bulk import skips
    // manual rows by design.
    await onSave({
      provider: row.provider,
      model: row.model,
      input_micros_usd_per_million_tokens: inputMicros,
      output_micros_usd_per_million_tokens: outputMicros,
      cached_input_micros_usd_per_million_tokens: cachedMicros,
      source: "manual",
    });
  }

  return (
    <tr>
      <td className="mono" style={{ color: "var(--t0)" }}>{row.model}</td>
      <td><input ref={firstInputRef} className="input" style={inputStyle} placeholder="0.150" value={input} onChange={e => setInput(e.target.value)} aria-label="Input price" /></td>
      <td><input className="input" style={inputStyle} placeholder="0.600" value={output} onChange={e => setOutput(e.target.value)} aria-label="Output price" /></td>
      <td><input className="input" style={inputStyle} placeholder="0.075" value={cached} onChange={e => setCached(e.target.value)} aria-label="Cached price" /></td>
      <td>
        <Badge status="healthy" label="manual" />
      </td>
      <td>
        {/* minHeight pins the action cell to the tallest variant
            (text buttons) so rows with icon-only Edit/Delete stay the
            same height as rows with "Import" or "Set price" buttons. */}
        <div style={{ display: "flex", gap: 4, justifyContent: "flex-end", alignItems: "center", minHeight: 22 }}>
          <button className="btn btn-primary btn-sm" style={{ padding: "3px 6px" }} onClick={() => void save()}>Save</button>
          <button className="btn btn-ghost btn-sm" style={{ padding: "3px 6px" }} onClick={onCancel}>Cancel</button>
        </div>
        {error && <div style={{ fontSize: 10, color: "var(--red)", marginTop: 3 }}>{error}</div>}
      </td>
    </tr>
  );
}

// ─── Import consent dialog ───────────────────────────────────────────────────

// ConsentChange is a row in the consent dialog: one (provider, model)
// pair LiteLLM wants to add, update (imported rows), or replace (manual
// rows that the operator is explicitly opting in to overwrite).
type ConsentChange =
  | { kind: "add"; key: string; provider: string; model: string; entry: ConfiguredPricebookRecord }
  | { kind: "update"; key: string; provider: string; model: string; entry: ConfiguredPricebookRecord; previous: ConfiguredPricebookRecord }
  | { kind: "replace"; key: string; provider: string; model: string; entry: ConfiguredPricebookRecord; previous: ConfiguredPricebookRecord };

function PricebookImportConsent({
  diff,
  unified,
  initialSelectedKeys,
  providerName,
  onClose,
  onConfirm,
}: {
  diff: PricebookImportDiff;
  // Unified rows from the parent. We use this to filter the diff down
  // to changes that actually land in the table — i.e. catalog cloud
  // models. Local-provider and LiteLLM-only entries are skipped.
  unified: UnifiedRow[];
  // Keys to pre-check in the dialog — typically the rows matching the
  // active filter. Operator can still toggle anything in the full list.
  initialSelectedKeys: Set<string>;
  // Display-name lookup so consent rows group under the same provider
  // labels the main table uses (Anthropic, OpenAI, …).
  providerName: (id: string) => string;
  onClose: () => void;
  // onConfirm returns the apply result so the dialog can read
  // `result.failed` and surface per-row failures inline. Returning
  // void would hide partial-success outcomes from the operator.
  onConfirm: (keys: string[]) => Promise<PricebookImportDiff>;
}) {
  // Set of (provider, model) keys present in the table — the universe
  // of changes the operator can consent to. Built once on dialog open.
  const tableKeys = useMemo(() => {
    const s = new Set<string>();
    for (const r of unified) s.add(r.key);
    return s;
  }, [unified]);

  // Compute the consent rows from the diff, restricted to keys we
  // actually show in the table. Deterministic order: alphabetical.
  const changes = useMemo<ConsentChange[]>(() => {
    const out: ConsentChange[] = [];
    for (const r of diff.added ?? []) {
      const key = pricebookKey(r.provider, r.model);
      if (!tableKeys.has(key)) continue;
      out.push({ kind: "add", key, provider: r.provider, model: r.model, entry: r });
    }
    for (const u of diff.updated ?? []) {
      // Defensive: skip entries from a stale gateway that doesn't yet
      // carry the new {entry, previous} pair shape.
      if (!u?.entry) continue;
      const key = pricebookKey(u.entry.provider, u.entry.model);
      if (!tableKeys.has(key)) continue;
      out.push({ kind: "update", key, provider: u.entry.provider, model: u.entry.model, entry: u.entry, previous: u.previous });
    }
    // Skipped now carries LiteLLM's proposal for manual rows. We
    // surface them in a "Replace manual" section so the operator can
    // explicitly opt in to overwriting them (the backend honors keys
    // from this section only when listed explicitly).
    for (const u of diff.skipped ?? []) {
      if (!u?.entry) continue;
      const key = pricebookKey(u.entry.provider, u.entry.model);
      if (!tableKeys.has(key)) continue;
      out.push({ kind: "replace", key, provider: u.entry.provider, model: u.entry.model, entry: u.entry, previous: u.previous });
    }
    return out.sort((a, b) => a.key.localeCompare(b.key));
  }, [diff, tableKeys]);

  // Selection: pre-check the keys the parent asked us to (typically
  // those matching the active filter). Operator can toggle anything
  // else in or out before confirming. Initialized once.
  const [selected, setSelected] = useState<Set<string>>(() => {
    const init = new Set<string>();
    for (const c of changes) {
      if (initialSelectedKeys.has(c.key)) init.add(c.key);
    }
    return init;
  });
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  // Per-row failures from the most recent apply attempt. The dialog
  // stays open while this is non-empty so the operator can see what
  // didn't land — closing too eagerly hid the failure detail.
  const [failed, setFailed] = useState<PricebookImportFailureRecord[]>([]);

  function toggle(key: string) {
    setSelected(prev => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  function toggleAll() {
    setSelected(prev => prev.size === changes.length ? new Set() : new Set(changes.map(c => c.key)));
  }

  async function confirm() {
    if (selected.size === 0) return;
    setSubmitting(true);
    setError("");
    setFailed([]);
    try {
      const result = await onConfirm([...selected]);
      // Per-row failures: keep the dialog open so the operator can
      // see exactly which rows didn't land. Successful rows have
      // already been removed from state.settingsConfig.pricebook by the
      // parent's loadDashboard call, so the next render of `changes`
      // will only show remaining work — including the failed ones,
      // which the operator can re-attempt or uncheck.
      if (result.failed && result.failed.length > 0) {
        setFailed(result.failed);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to apply import.");
    } finally {
      setSubmitting(false);
    }
  }

  const adds = changes.filter(c => c.kind === "add");
  const updates = changes.filter(c => c.kind === "update");
  const replaces = changes.filter(c => c.kind === "replace");
  const allChecked = selected.size === changes.length && changes.length > 0;

  // Provider-grouped variants of each section, so the consent dialog
  // renders the same provider hierarchy as the main pricebook table.
  const groupedAdds = useMemo(() => groupChangesByProvider(adds, providerName), [adds, providerName]);
  const groupedUpdates = useMemo(() => groupChangesByProvider(updates, providerName), [updates, providerName]);
  const groupedReplaces = useMemo(() => groupChangesByProvider(replaces, providerName), [replaces, providerName]);

  return (
    <Modal
      title="Update pricebook"
      onClose={onClose}
      footer={
        <>
          {error && <div style={{ marginBottom: 8 }}><InlineError message={error} /></div>}
          <button
            className="btn btn-primary"
            style={{ width: "100%", justifyContent: "center" }}
            disabled={selected.size === 0 || submitting}
            onClick={() => void confirm()}>
            {submitting ? "Applying…" : `Apply ${selected.size} change${selected.size === 1 ? "" : "s"}`}
          </button>
        </>
      }>
      {changes.length === 0 ? (
        <div style={{ fontSize: 12, color: "var(--t2)", padding: "12px 0" }}>
          The pricebook is already up to date with LiteLLM — nothing to import.
        </div>
      ) : (
        <>
          {/* Provenance — when this proposal set was fetched from the
              upstream catalog. Helps the operator decide whether the
              data is stale (e.g. open a new dialog after a long pause). */}
          {diff.fetched_at && (
            <div className="kicker" style={{ marginBottom: 8 }}>
              Fetched {formatRelativeTime(diff.fetched_at)} · <span title={diff.fetched_at}>{formatAbsoluteTime(diff.fetched_at)}</span>
            </div>
          )}

          {/* Per-row failures from the most recent apply attempt. The
              dialog stays open while these are present so the operator
              can re-try or uncheck the rows that didn't land. */}
          {failed.length > 0 && (
            <div style={{ marginBottom: 12, padding: "10px 12px", background: "var(--red-bg)", border: "1px solid var(--red-border)", borderRadius: "var(--radius-sm)" }}>
              <div className="kicker" style={{ color: "var(--red)", marginBottom: 6 }}>
                {failed.length} failed
              </div>
              {failed.map((f, i) => (
                <div key={i} style={{ display: "flex", gap: 8, alignItems: "baseline", padding: "3px 0", fontSize: 11, color: "var(--t1)" }}>
                  <code style={{ fontFamily: "var(--font-mono)", color: "var(--t0)", flexShrink: 0 }}>
                    {f.entry.provider}/{f.entry.model}
                  </code>
                  <span style={{ color: "var(--t3)" }}>—</span>
                  <span style={{ color: "var(--red)" }}>{f.error}</span>
                </div>
              ))}
            </div>
          )}

          {/* Summary chip strip — at-a-glance counts in the same
              chip-of-numbers style as the status tabs. Replaces a
              wordy lead paragraph; the section bodies tell the story
              in detail. */}
          <div style={{ display: "flex", alignItems: "center", gap: 14, marginBottom: 12, padding: "8px 12px", background: "var(--bg2)", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)" }}>
            <SummaryChip label="new" count={adds.length} />
            <SummaryChip label="updated" count={updates.length} />
            <SummaryChip label="manual override" count={replaces.length} muted={replaces.length === 0} />
            <label style={{ marginLeft: "auto", display: "flex", alignItems: "center", gap: 6, cursor: "pointer" }}>
              <input
                type="checkbox"
                checked={allChecked}
                ref={el => { if (el) el.indeterminate = !allChecked && selected.size > 0; }}
                onChange={toggleAll}
                aria-label="Toggle all"
              />
              <span className="kicker" style={{ color: "var(--t2)" }}>
                {allChecked ? "deselect all" : "select all"}
              </span>
            </label>
          </div>

          {adds.length > 0 && (
            <ConsentSection title="New prices" count={adds.length}>
              <ProviderGroupedRows
                groups={groupedAdds}
                renderDetail={c => c.kind === "add" ? describeAddedDetail(c.entry) : ""}
                selected={selected}
                onToggle={toggle}
              />
            </ConsentSection>
          )}

          {updates.length > 0 && (
            <ConsentSection title="Price updates" count={updates.length}>
              <ProviderGroupedRows
                groups={groupedUpdates}
                renderDetail={c => c.kind === "update" ? describeUpdatedDetail(c.previous, c.entry) : ""}
                selected={selected}
                onToggle={toggle}
              />
            </ConsentSection>
          )}

          {replaces.length > 0 && (
            <ConsentSection
              title="Override manual"
              count={replaces.length}
              hint="These are manual prices the operator set explicitly. Checking them swaps the manual value for the imported one and flips the source back to imported.">
              <ProviderGroupedRows
                groups={groupedReplaces}
                renderDetail={c => c.kind === "replace" ? describeUpdatedDetail(c.previous, c.entry) : ""}
                selected={selected}
                onToggle={toggle}
              />
            </ConsentSection>
          )}
        </>
      )}
    </Modal>
  );
}

// PriceDiffPanel renders an Input / Output / Cached price summary as a
// clean key-value grid. Used by the per-row Import confirmation modal
// — replaces a single-line "in $X out $Y cache $Z" string that was hard
// to scan at a glance.
//
// When `previous` is provided AND a field changed, that row shows a
// `prev → next` diff. Unchanged rows still render so the operator
// sees the complete picture (not just deltas), with a muted
// "no change" hint instead of a value pair.
function PriceDiffPanel({
  previous,
  next,
}: {
  previous?: ConfiguredPricebookRecord;
  next: ConfiguredPricebookRecord;
}) {
  const fields: Array<{ label: string; key: keyof Pick<ConfiguredPricebookRecord, "input_micros_usd_per_million_tokens" | "output_micros_usd_per_million_tokens" | "cached_input_micros_usd_per_million_tokens"> }> = [
    { label: "Input",  key: "input_micros_usd_per_million_tokens" },
    { label: "Output", key: "output_micros_usd_per_million_tokens" },
    { label: "Cached", key: "cached_input_micros_usd_per_million_tokens" },
  ];
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "auto 1fr",
        rowGap: 6,
        columnGap: 16,
        padding: "10px 14px",
        background: "var(--bg2)",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
      }}>
      {fields.map(f => {
        const nextVal = next[f.key];
        const prevVal = previous ? previous[f.key] : undefined;
        const changed = previous !== undefined && prevVal !== nextVal;
        return (
          <React.Fragment key={f.key}>
            <span style={{
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              color: "var(--t3)",
              textTransform: "uppercase",
              letterSpacing: "0.05em",
              alignSelf: "center",
            }}>{f.label}</span>
            <span style={{
              fontFamily: "var(--font-mono)",
              fontSize: 12,
              color: changed || !previous ? "var(--t0)" : "var(--t3)",
            }}>
              {previous === undefined ? (
                formatPricePerMillion(nextVal)
              ) : changed ? (
                <>
                  <span style={{ color: "var(--t3)" }}>{formatPriceCompact(prevVal!)}</span>
                  <span style={{ color: "var(--t3)", margin: "0 6px" }}>→</span>
                  <span>{formatPriceCompact(nextVal)} / 1M</span>
                </>
              ) : (
                <span style={{ color: "var(--t3)" }}>{formatPricePerMillion(nextVal)} (no change)</span>
              )}
            </span>
          </React.Fragment>
        );
      })}
    </div>
  );
}

// ProviderGroup is one bucket of consent rows under a single provider
// (e.g. all "Add" entries for OpenAI). The consent dialog renders one
// of these per provider inside each section.
type ProviderGroup<T extends ConsentChange> = {
  providerId: string;
  providerName: string;
  changes: T[];
};

// groupChangesByProvider splits a flat consent-change list into provider
// buckets, sorts buckets by provider display name, and sorts changes
// within each bucket alphabetically by model. The output mirrors how
// the main pricebook table is grouped, so the dialog reads the same way.
function groupChangesByProvider<T extends ConsentChange>(
  changes: T[],
  providerName: (id: string) => string,
): ProviderGroup<T>[] {
  const map = new Map<string, T[]>();
  for (const c of changes) {
    const list = map.get(c.provider) ?? [];
    list.push(c);
    map.set(c.provider, list);
  }
  return [...map.entries()]
    .map(([providerId, list]) => ({
      providerId,
      providerName: providerName(providerId),
      changes: [...list].sort((a, b) => a.model.localeCompare(b.model)),
    }))
    .sort((a, b) => a.providerName.localeCompare(b.providerName));
}

// SummaryChip is the per-section count tile in the consent dialog
// header strip. Matches the visual rhythm of the status tab counts
// elsewhere on the page (mono, uppercase, muted label + numeric chip).
function SummaryChip({ label, count, muted = false }: { label: string; count: number; muted?: boolean }) {
  return (
    <div style={{ display: "flex", alignItems: "baseline", gap: 5 }}>
      <span style={{
        fontFamily: "var(--font-mono)",
        fontSize: 14,
        fontWeight: 500,
        color: muted || count === 0 ? "var(--t3)" : "var(--t0)",
      }}>{count}</span>
      <span style={{
        fontFamily: "var(--font-mono)",
        fontSize: 9,
        color: "var(--t3)",
        textTransform: "uppercase",
        letterSpacing: "0.05em",
      }}>{label}</span>
    </div>
  );
}

// ProviderGroupedRows renders a flat list of provider-grouped consent
// changes, with a small mono provider header before each bucket.
// Sub-headers stay subtle (t2, smaller) so they don't compete with the
// section titles above them — the visual hierarchy reads as
//   SECTION → PROVIDER → ROWS.
function ProviderGroupedRows<T extends ConsentChange>({
  groups,
  renderDetail,
  selected,
  onToggle,
}: {
  groups: ProviderGroup<T>[];
  renderDetail: (c: T) => string;
  selected: Set<string>;
  onToggle: (key: string) => void;
}) {
  return (
    <>
      {groups.map((group, i) => (
        <div key={group.providerId} style={{ marginTop: i === 0 ? 0 : 8 }}>
          <div style={{ display: "flex", alignItems: "baseline", gap: 6, padding: "4px 0 2px" }}>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)", fontWeight: 500 }}>
              {group.providerName}
            </span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
              {group.changes.length}
            </span>
          </div>
          {group.changes.map(c => (
            <ConsentRow
              key={c.key}
              label={c.model}
              detail={renderDetail(c)}
              checked={selected.has(c.key)}
              onToggle={() => onToggle(c.key)}
            />
          ))}
        </div>
      ))}
    </>
  );
}

function ConsentSection({ title, count, hint, children }: {
  title: string;
  count: number;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div style={{ marginBottom: 14 }}>
      {/* Section header reads in the same voice as the SettingsView group
          headers (KeysTab tenants, PricebookTab provider groups): mono,
          teal, count chip in muted t3. */}
      <div style={{ display: "flex", alignItems: "baseline", gap: 8, marginBottom: 6, paddingLeft: 4 }}>
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--teal)", fontWeight: 500 }}>{title}</span>
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>{count}</span>
      </div>
      {hint && <div style={{ fontSize: 11, color: "var(--t3)", marginBottom: 6, paddingLeft: 4, lineHeight: 1.4 }}>{hint}</div>}
      <div className="card" style={{ padding: "4px 10px" }}>{children}</div>
    </div>
  );
}

function ConsentRow({
  label,
  detail,
  checked,
  onToggle,
}: {
  label: string;
  detail: string;
  checked: boolean;
  onToggle: () => void;
}) {
  return (
    <label
      title={label}
      style={{ display: "flex", alignItems: "center", gap: 8, padding: "5px 0", cursor: "pointer" }}>
      <input type="checkbox" checked={checked} onChange={onToggle} aria-label={label} style={{ flexShrink: 0 }} />
      <span
        style={{
          flex: 1,
          minWidth: 0,
          fontFamily: "var(--font-mono)",
          fontSize: 11,
          color: "var(--t1)",
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}>
        {label}
      </span>
      <span
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          color: "var(--t3)",
          flexShrink: 0,
          whiteSpace: "nowrap",
        }}>
        {detail}
      </span>
    </label>
  );
}

// PricebookHistoryModal renders the audit-event log filtered to a
// single pricebook row. Events come from settingsConfig.events (already
// streamed by the dashboard load) — we filter client-side because the
// existing surface is sufficient and adding a per-entry endpoint just
// to filter the same data would be churn.
//
// Events are stored newest-last in the settings state; we reverse
// for display so the most recent action is at the top, matching every
// other reverse-chronological list in the UI.
function PricebookHistoryModal({
  provider,
  model,
  events,
  onClose,
}: {
  provider: string;
  model: string;
  events: ConfiguredAuditEventRecord[];
  onClose: () => void;
}) {
  const targetID = `${provider}/${model}`;
  const filtered = useMemo(() => {
    return events
      .filter(e => e.target_type === "pricebook_entry" && e.target_id === targetID)
      .slice()
      .reverse();
  }, [events, targetID]);

  return (
    <Modal
      title={`Price history: ${targetID}`}
      onClose={onClose}
      width={520}
      footer={
        <button className="btn" onClick={onClose}>Close</button>
      }>
      {filtered.length === 0 ? (
        <div style={{ padding: "20px 4px", textAlign: "center", color: "var(--t3)", fontSize: 13 }}>
          No history yet. Audit events for this entry will appear here once
          the price is changed, imported, or cleared.
        </div>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
          {filtered.map((event, i) => (
            <PricebookHistoryEvent key={`${event.timestamp}-${i}`} event={event} />
          ))}
        </div>
      )}
    </Modal>
  );
}

function PricebookHistoryEvent({ event }: { event: ConfiguredAuditEventRecord }) {
  // Action labels — translate the wire `action` strings into something
  // an operator scanning the list will recognize at a glance.
  const actionLabel = (() => {
    switch (event.action) {
      case "pricebook_entry.created":  return { text: "created", color: "var(--green)" };
      case "pricebook_entry.updated":  return { text: "updated", color: "var(--teal)" };
      case "pricebook_entry.deleted":  return { text: "cleared", color: "var(--red)" };
      case "pricebook_entry.imported": return { text: "imported", color: "var(--teal)" };
      default:                         return { text: event.action.replace("pricebook_entry.", ""), color: "var(--t2)" };
    }
  })();
  return (
    <div style={{ padding: "8px 10px", background: "var(--bg2)", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 4 }}>
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: actionLabel.color, fontWeight: 500, textTransform: "uppercase", letterSpacing: "0.04em" }}>
          {actionLabel.text}
        </span>
        <span style={{ flex: 1 }} />
        {event.timestamp && (
          <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }} title={event.timestamp}>
            {new Date(event.timestamp).toLocaleString()}
          </span>
        )}
      </div>
      <div style={{ display: "flex", gap: 12, fontSize: 11, color: "var(--t2)" }}>
        <span>by <span style={{ color: "var(--t1)", fontFamily: "var(--font-mono)" }}>{event.actor || "—"}</span></span>
        {event.detail && <span title={event.detail} style={{ color: "var(--t1)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{event.detail}</span>}
      </div>
    </div>
  );
}
