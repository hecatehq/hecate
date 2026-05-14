import type { ReactNode } from "react";
import type { RuntimeConsoleViewModel } from "../../app/useRuntimeConsole";
import type { AgentChatUsageRecord, UsageEventRecord } from "../../types/runtime";
import { CopyBtn } from "../shared/ui";

type Props = {
  state: RuntimeConsoleViewModel["state"];
  actions: RuntimeConsoleViewModel["actions"];
};

type UsageEntry = UsageEventRecord;
type UsageTotals = {
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
  costMicrosUSD: number;
};

// CostsView keeps the internal workspace id for compatibility, but the
// product surface is "Usage": tokens first, reported cost second.
// Hecate-controlled provider calls are authoritative; external-agent usage is
// adapter-reported and explicitly labelled as such.
export function CostsView({ state }: Props) {
  const usageEvents = state.usageEvents ?? [];
  const providerKindByID = new Map((state.settingsConfig?.providers ?? []).map(provider => [provider.id, provider.kind]));
  const cloudEvents = usageEvents.filter(entry => usageEventIsCloud(entry, providerKindByID));
  const cloudTotals = sumUsageEvents(cloudEvents);
  const latestAgentUsage = findLatestAgentUsage(state.activeAgentChatSession);
  const hasCloudUsage = cloudEvents.length > 0;
  const hasAgentUsage = Boolean(latestAgentUsage);

  return (
    <div style={{ height: "100%", overflow: "hidden" }}>
      <div style={{ height: "100%", overflowY: "auto", padding: 16 }}>
        <div style={{ marginBottom: 18 }}>
          <div>
            <div style={{ fontSize: 14, fontWeight: 500, color: "var(--t0)", marginBottom: 3 }}>Usage</div>
            <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
              Cloud-provider token usage measured by Hecate. Local providers are hidden. External-agent usage appears only when the active chat reports it.
            </div>
          </div>
        </div>

        {!hasCloudUsage && !hasAgentUsage && (
          <EmptyUsageState />
        )}

        {hasCloudUsage && (
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))",
              gap: 10,
              marginBottom: 18,
            }}
          >
            <MetricCard
              label="Cloud tokens"
              value={formatInteger(cloudTotals.totalTokens)}
              detail={`${formatInteger(cloudTotals.promptTokens)} prompt · ${formatInteger(cloudTotals.completionTokens)} output`}
            />
            <MetricCard
              label="Reported cloud cost"
              value={formatMicrosUSD(cloudTotals.costMicrosUSD)}
              detail="Shown only when the provider response includes a reported cost"
            />
          </div>
        )}

        {hasCloudUsage && (
          <>
            <SubHeader
              title="Recent cloud calls"
              description="Hecate-controlled cloud-provider calls. Local-provider rows are hidden because they do not spend cloud-provider tokens."
            />
            <div className="card" style={{ overflow: "hidden", marginBottom: 20 }}>
              <table className="table" style={{ tableLayout: "fixed" }}>
                <colgroup>
                  <col style={{ width: 82 }} />
                  <col style={{ width: 110 }} />
                  <col />
                  <col style={{ width: 92 }} />
                  <col style={{ width: 92 }} />
                  <col style={{ width: 84 }} />
                  <col style={{ width: 130 }} />
                  <col style={{ width: 52 }} />
                </colgroup>
                <thead>
                  <tr>
                    <th>Time</th>
                    <th>Provider</th>
                    <th>Model</th>
                    <th>Prompt</th>
                    <th>Output</th>
                    <th>Cost</th>
                    <th>Request ID</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  {cloudEvents.slice(0, 100).map((entry, index) => (
                    <tr key={entry.request_id || `${entry.timestamp}-${index}`}>
                      <td className="mono" style={{ color: "var(--t3)" }}>{formatTime(entry.timestamp)}</td>
                      <td className="mono" style={{ color: "var(--t1)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{entry.provider || "—"}</td>
                      <td className="mono" style={{ color: "var(--t1)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{entry.model || "—"}</td>
                      <td className="mono">{formatInteger(entry.prompt_tokens ?? 0)}</td>
                      <td className="mono">{formatInteger(entry.completion_tokens ?? 0)}</td>
                      <td className="mono" style={{ color: "var(--t0)", fontWeight: 500 }}>{entry.amount_usd || formatMicrosUSD(entry.amount_micros_usd ?? 0)}</td>
                      <td className="mono" style={{ color: "var(--t2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{entry.request_id || "—"}</td>
                      <td>{entry.request_id && <CopyBtn text={entry.request_id} />}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}

        {latestAgentUsage && (
          <>
            <SubHeader
              title="Active external-agent usage"
              description="Codex, Claude Code, and Cursor can use their own subscriptions. These values are adapter-reported and not enforced by Hecate."
            />
            <div className="card" style={{ padding: "14px 16px" }}>
              <div style={{ display: "grid", gap: 8 }}>
                <UsageLine label="Context used" value={formatAgentContext(latestAgentUsage)} />
                <UsageLine label="Reported cost" value={formatAgentCost(latestAgentUsage) || "Not reported"} />
                <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
                  These values come from the active external-agent chat message. They are useful for orientation, not billing enforcement.
                </div>
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function EmptyUsageState() {
  return (
    <div className="card" style={{ padding: "28px", marginBottom: 20, textAlign: "center" }}>
      <div style={{ fontSize: 14, color: "var(--t0)", fontWeight: 500, marginBottom: 8 }}>
        No cloud usage recorded yet
      </div>
      <div style={{ fontSize: 12, color: "var(--t3)", lineHeight: 1.55, maxWidth: 620, margin: "0 auto" }}>
        Send a Hecate-controlled request through a cloud provider to see token usage here.
        Local models do not spend cloud-provider tokens, and external agents only appear when they report usage for the active chat.
      </div>
    </div>
  );
}

function MetricCard({ label, value, detail }: { label: string; value: string; detail: string }) {
  return (
    <div className="card" style={{ padding: "13px 14px" }}>
      <div style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)", textTransform: "uppercase", letterSpacing: "0.08em", marginBottom: 8 }}>
        {label}
      </div>
      <div style={{ fontSize: 24, lineHeight: 1, fontWeight: 650, color: "var(--t0)", marginBottom: 8 }}>
        {value}
      </div>
      <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.4 }}>
        {detail}
      </div>
    </div>
  );
}

function UsageLine({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ display: "flex", justifyContent: "space-between", gap: 12 }}>
      <span style={{ fontSize: 11, color: "var(--t3)" }}>{label}</span>
      <span style={{ fontSize: 11, color: "var(--t1)", fontFamily: "var(--font-mono)" }}>{value}</span>
    </div>
  );
}

function SubHeader({ title, description, right }: { title: string; description?: string; right?: ReactNode }) {
  return (
    <div style={{ display: "flex", alignItems: "flex-start", gap: 12, marginBottom: 12 }}>
      <div style={{ minWidth: 0 }}>
        <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", marginBottom: description ? 3 : 0 }}>{title}</div>
        {description && <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>{description}</div>}
      </div>
      {right && <div style={{ marginLeft: "auto", display: "flex", gap: 8, alignItems: "center" }}>{right}</div>}
    </div>
  );
}

function usageEventIsCloud(entry: UsageEntry, providerKindByID: Map<string, string>): boolean {
  if (entry.type && entry.type !== "usage") return false;
  const provider = entry.provider || "";
  if (!provider) return true;
  return providerKindByID.get(provider) !== "local";
}

function sumUsageEvents(entries: UsageEntry[]): UsageTotals {
  return entries.reduce<UsageTotals>((acc, entry) => {
    const promptTokens = entry.prompt_tokens ?? 0;
    const completionTokens = entry.completion_tokens ?? 0;
    acc.promptTokens += promptTokens;
    acc.completionTokens += completionTokens;
    acc.totalTokens += entry.total_tokens ?? (promptTokens + completionTokens);
    acc.costMicrosUSD += entry.amount_micros_usd ?? 0;
    return acc;
  }, { promptTokens: 0, completionTokens: 0, totalTokens: 0, costMicrosUSD: 0 });
}

function findLatestAgentUsage(session: RuntimeConsoleViewModel["state"]["activeAgentChatSession"]): AgentChatUsageRecord | null {
  const messages = session?.messages ?? [];
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const usage = messages[index]?.usage;
    if (usage && !agentUsageEmpty(usage)) return usage;
  }
  return null;
}

function agentUsageEmpty(usage: AgentChatUsageRecord): boolean {
  return !usage.reported_cost_amount && !usage.reported_cost_currency && !(usage.context_size ?? 0) && !(usage.context_used ?? 0);
}

function formatAgentContext(usage: AgentChatUsageRecord): string {
  const used = usage.context_used ?? 0;
  const size = usage.context_size ?? 0;
  if (size > 0) return `${formatInteger(used)} / ${formatInteger(size)}`;
  if (used > 0) return formatInteger(used);
  return "—";
}

function formatAgentCost(usage: AgentChatUsageRecord): string {
  if (!usage.reported_cost_amount && !usage.reported_cost_currency) return "";
  const currency = usage.reported_cost_currency ? ` ${usage.reported_cost_currency}` : "";
  return `${usage.reported_cost_amount || "0"}${currency}`;
}

function formatTime(value?: string): string {
  if (!value) return "—";
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed)) return "—";
  return new Date(parsed).toLocaleTimeString();
}

function formatInteger(value: number): string {
  return Number.isFinite(value) ? value.toLocaleString() : "—";
}

function formatMicrosUSD(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "$0.000";
  return `$${(value / 1_000_000).toFixed(3)}`;
}
