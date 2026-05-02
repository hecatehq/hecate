import { useState, type ReactNode } from "react";
import type { RuntimeConsoleViewModel } from "../../app/useRuntimeConsole";
import { Badge, CopyBtn, Dot, Icon, Icons } from "../shared/ui";

type Props = {
  state: RuntimeConsoleViewModel["state"];
  actions: RuntimeConsoleViewModel["actions"];
};

// CostsView is the operator-facing balance + spend view. It is lifted
// wholesale from the SettingsView Budget and Usage tabs — a balance card
// up top, a per-request ledger table at the bottom — and re-housed as
// its own workspace so it sits alongside Providers / Tasks rather than
// buried under Settings. The action surface is unchanged: top-up,
// reset, adjust limits.
export function CostsView({ state, actions }: Props) {
  const budget = state.budget;
  const accountSummary = state.accountSummary;
  const ledger = state.requestLedger ?? [];

  const [editing, setEditing] = useState(false);
  const [editLimit, setEditLimit] = useState("");
  const [editWarn, setEditWarn] = useState("");

  function pct(debited: number, limit: number) {
    if (!limit) return 0;
    return Math.min(100, Math.round((debited / limit) * 100));
  }

  function barClass(p: number, warnThreshold: number): string {
    if (p >= 90) return "progress-red";
    if (p >= warnThreshold) return "progress-amber";
    return "progress-teal";
  }

  return (
    <div style={{ height: "100%", overflow: "hidden" }}>
      <div style={{ height: "100%", overflowY: "auto", padding: 16 }}>

        {/* Header row — matches the Providers pattern: title left, action
            buttons pushed right. */}
        <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 20 }}>
          <span style={{ fontSize: 14, fontWeight: 500, color: "var(--t0)" }}>Costs</span>
          <div style={{ marginLeft: "auto", display: "flex", gap: 6 }}>
            <button
              className="btn btn-sm"
              onClick={() => void actions.resetBudget()}
              disabled={!budget}>
              <Icon d={Icons.refresh} size={13} /> Reset balance
            </button>
            <button
              className="btn btn-primary btn-sm"
              onClick={() => void actions.topUpBudget()}
              disabled={!budget}>
              <Icon d={Icons.plus} size={13} /> Top up
            </button>
          </div>
        </div>

        {/* Balance card */}
        {!budget ? (
          <div className="card" style={{ padding: "24px", textAlign: "center", color: "var(--t3)", fontSize: 12, marginBottom: 24 }}>
            Budget data unavailable. Admin access required.
          </div>
        ) : (
          <div className="card" style={{ padding: "14px 16px", marginBottom: 24 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 10 }}>
              <span style={{ fontFamily: "var(--font-mono)", fontSize: 13, fontWeight: 500, color: "var(--t0)" }}>{budget.scope}</span>
              {budget.enforced ? <Badge status="enabled" label="enforced" /> : <Badge status="disabled" label="not enforced" />}
              <div style={{ marginLeft: "auto", display: "flex", gap: 6 }}>
                {editing ? (
                  <>
                    <button className="btn btn-primary btn-sm" onClick={() => { void actions.setBudgetLimit(); setEditing(false); }}>Save</button>
                    <button className="btn btn-sm" onClick={() => setEditing(false)}>Cancel</button>
                  </>
                ) : (
                  <button className="btn btn-ghost btn-sm" onClick={() => {
                    setEditing(true);
                    setEditLimit(String(budget.credited_micros_usd / 1_000_000));
                    setEditWarn("80");
                  }} style={{ gap: 4, fontSize: 11 }}>
                    <Icon d={Icons.edit} size={12} /> Adjust
                  </button>
                )}
              </div>
            </div>

            <div style={{ marginBottom: 10 }}>
              <div style={{ display: "flex", justifyContent: "space-between", marginBottom: 5 }}>
                <span style={{ fontSize: 11, color: "var(--t2)" }}>
                  <span style={{ fontFamily: "var(--font-mono)", color: "var(--t0)", fontWeight: 500 }}>{budget.debited_usd}</span>{" "}spent of {budget.credited_usd} credited
                </span>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)" }}>
                  {pct(budget.debited_micros_usd, budget.credited_micros_usd)}%
                </span>
              </div>
              <div className="progress-wrap">
                <div className={`progress-bar ${barClass(pct(budget.debited_micros_usd, budget.credited_micros_usd), 75)}`}
                  style={{ width: `${pct(budget.debited_micros_usd, budget.credited_micros_usd)}%` }} />
              </div>
              <div style={{ display: "flex", justifyContent: "space-between", marginTop: 3 }}>
                <span style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>balance: {budget.balance_usd}</span>
                <span style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>available: {budget.available_usd}</span>
              </div>
            </div>

            {editing && (
              <div style={{ display: "flex", gap: 10, marginBottom: 10, padding: 10, background: "var(--bg3)", borderRadius: "var(--radius-sm)", border: "1px solid var(--border)" }}>
                <div style={{ flex: 1 }}>
                  <label style={{ fontSize: 10, color: "var(--t3)", display: "block", marginBottom: 3, fontFamily: "var(--font-mono)" }}>CREDIT AMOUNT ($)</label>
                  <input className="input" type="number" value={editLimit}
                    onChange={e => { setEditLimit(e.target.value); void actions.setBudgetAmountUsd(e.target.value); }}
                    style={{ fontFamily: "var(--font-mono)" }} />
                </div>
                <div style={{ flex: 1 }}>
                  <label style={{ fontSize: 10, color: "var(--t3)", display: "block", marginBottom: 3, fontFamily: "var(--font-mono)" }}>LIMIT ($)</label>
                  <input className="input" type="number" value={editWarn}
                    onChange={e => { setEditWarn(e.target.value); void actions.setBudgetLimitUsd(e.target.value); }}
                    style={{ fontFamily: "var(--font-mono)" }} />
                </div>
              </div>
            )}

            {budget.warnings?.some(w => w.triggered) && (
              <div style={{ fontSize: 12, color: "var(--amber)", marginTop: 6 }}>
                <Icon d={Icons.warning} size={13} /> Warning threshold triggered at {budget.warnings.find(w => w.triggered)?.threshold_percent}%
              </div>
            )}
          </div>
        )}

        {/* Model cost estimates — surfaced when the gateway has them. */}
        {accountSummary?.estimates && accountSummary.estimates.length > 0 && (
          <div style={{ marginBottom: 24 }}>
            <SubHeader title="Model cost estimates" />
            <div className="card" style={{ overflow: "hidden" }}>
              <table className="table" style={{ tableLayout: "fixed" }}>
                <colgroup>
                  <col /><col style={{ width: 100 }} /><col style={{ width: 140 }} /><col style={{ width: 120 }} />
                </colgroup>
                <thead>
                  <tr><th>Model</th><th>Provider</th><th>Est. prompt tokens</th><th>Est. output tokens</th></tr>
                </thead>
                <tbody>
                  {accountSummary.estimates.slice(0, 10).map((e, i) => (
                    <tr key={`${e.provider}-${e.model}-${i}`}>
                      <td className="mono" style={{ color: "var(--t0)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{e.model}</td>
                      <td className="mono" style={{ color: "var(--t2)" }}>{e.provider}</td>
                      <td className="mono">{e.estimated_remaining_prompt_tokens?.toLocaleString() ?? "—"}</td>
                      <td className="mono">{e.estimated_remaining_output_tokens?.toLocaleString() ?? "—"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}

        {/* Usage / request ledger */}
        <SubHeader
          title="Request ledger"
          description="Live request usage, token counts, and request IDs."
          right={
            <>
              <span style={{ fontSize: 11, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>live</span>
              <Dot color="green" />
            </>
          }
        />
        {ledger.length > 0 ? (
          <div className="card" style={{ overflow: "hidden" }}>
            <table className="table" style={{ tableLayout: "fixed" }}>
              <colgroup>
                <col style={{ width: 80 }} /><col /><col style={{ width: 80 }} /><col style={{ width: 70 }} /><col style={{ width: 130 }} /><col style={{ width: 52 }} />
              </colgroup>
              <thead>
                <tr><th>Time</th><th>Model</th><th>Tokens</th><th>Cost</th><th>Request ID</th><th></th></tr>
              </thead>
              <tbody>
                {ledger.slice(0, 100).map(e => (
                  <tr key={e.request_id || e.timestamp}>
                    <td className="mono" style={{ color: "var(--t3)" }}>{e.timestamp ? new Date(e.timestamp).toLocaleTimeString() : "—"}</td>
                    <td className="mono" style={{ color: "var(--t1)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{e.model || "—"}</td>
                    <td className="mono">{e.total_tokens?.toLocaleString() ?? "—"}</td>
                    <td className="mono" style={{ color: "var(--t0)", fontWeight: 500 }}>{e.amount_usd || "—"}</td>
                    <td className="mono" style={{ color: "var(--t2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{e.request_id || "—"}</td>
                    <td>{e.request_id && <CopyBtn text={e.request_id} />}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="card" style={{ padding: "24px", textAlign: "center", color: "var(--t3)", fontSize: 12 }}>
            No usage events recorded yet.
          </div>
        )}
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
