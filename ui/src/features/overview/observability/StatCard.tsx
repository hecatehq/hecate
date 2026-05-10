// StatCard is the single-cell stat display used in the Observability
// runtime strip (queue depth, worker count, in-flight, etc.). Tiny on
// purpose — just label + value + optional sub-text + optional amber
// highlight when the value is non-zero / out of normal range.

type StatCardProps = {
  label: string;
  value: string | number;
  sub?: string;
  highlight?: boolean;
};

export function StatCard({ label, value, sub, highlight }: StatCardProps) {
  return (
    <div className="card" style={{ padding: "12px 14px", minWidth: 110 }}>
      <div className="kicker" style={{ color: "var(--t2)", marginBottom: 6 }}>{label}</div>
      <div style={{ fontSize: 22, fontWeight: 600, fontFamily: "var(--font-mono)", color: highlight ? "var(--amber)" : "var(--t0)", lineHeight: 1 }}>{value}</div>
      {sub && <div style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)", marginTop: 4 }}>{sub}</div>}
    </div>
  );
}
