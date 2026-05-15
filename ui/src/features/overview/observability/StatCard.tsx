// StatCard is the single-cell stat display used in the Observability
// runtime strip. It favors operator language over raw internal names:
// label/value for scanning, helper copy in the tooltip/title, and
// optional status text for "Idle" / "Memory store" style states.

type StatCardProps = {
  label: string;
  value: string | number;
  sub?: string;
  help?: string;
  highlight?: boolean;
  status?: "idle" | "active" | "warning";
};

export function StatCard({ label, value, sub, help, highlight, status }: StatCardProps) {
  const tone = highlight || status === "active" || status === "warning" ? "var(--amber)" : "var(--t0)";
  return (
    <div className="card" title={help} style={{ padding: "12px 14px", minWidth: 0 }}>
      <div className="kicker" style={{ color: "var(--t2)", marginBottom: 6 }}>{label}</div>
      <div style={{ fontSize: 22, fontWeight: 600, fontFamily: "var(--font-mono)", color: tone, lineHeight: 1 }}>{value}</div>
      {sub && <div style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)", marginTop: 4 }}>{sub}</div>}
    </div>
  );
}
