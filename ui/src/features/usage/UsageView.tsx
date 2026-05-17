import type { ReactNode } from "react";
import { useSettings } from "../../app/state/settings";
import { useUsage } from "../../app/state/usage";
import { formatInteger, formatLocaleTime, formatMicrosUSD } from "../../lib/format";
import type { UsageEventRecord } from "../../types/usage";
import { CopyBtn } from "../shared/ui";

type UsageEntry = UsageEventRecord;
type UsageTotals = {
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
  costMicrosUSD: number;
};

// Only cross-chat Hecate-controlled provider calls belong here; active-chat
// adapter usage lives in ChatView where the reported values have context.
export function UsageView() {
  const usage = useUsage();
  const settings = useSettings();
  const usageEvents = usage.state.events ?? [];
  const providerKindByID = new Map((settings.state.config?.providers ?? []).map(provider => [provider.id, provider.kind]));
  const cloudEvents = usageEvents.filter(entry => usageEventIsCloud(entry, providerKindByID));
  const cloudTotals = sumUsageEvents(cloudEvents);
  const hasCloudUsage = cloudEvents.length > 0;

  return (
    <div style={{ height: "100%", overflow: "hidden" }}>
      <div style={{ height: "100%", overflowY: "auto", padding: 16 }}>
        <div style={{ marginBottom: 18 }}>
          <div>
            <div style={{ fontSize: 14, fontWeight: 500, color: "var(--t0)", marginBottom: 3 }}>Usage</div>
            <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
              Cloud-provider token usage measured by Hecate. Local providers are hidden. External-agent reported usage appears in the active chat.
            </div>
          </div>
        </div>

        {!hasCloudUsage && (
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
                      <td className="mono" style={{ color: "var(--t3)" }}>{formatLocaleTime(entry.timestamp)}</td>
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
        Local models do not spend cloud-provider tokens, and external-agent usage is shown in the chat where it was reported.
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

