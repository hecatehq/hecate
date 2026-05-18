import type { PendingAgentApproval } from "../../types/chat";
import { Icon, Icons } from "../shared/ui";
import { ChatNoticeFrame, ChatNoticeHeader, ChatNoticeRow } from "./ChatNotice";

// AgentApprovalAutoModeBanner is the persistent danger banner shown
// at the top of the Chats workspace when GATEWAY_AGENT_ADAPTER_APPROVAL_MODE
// is set to "auto" — every adapter RequestPermission is permitted with
// no operator review. Distinct from the per-session pending banner.
//
// Hidden in any mode other than "auto".
export function AgentApprovalAutoModeBanner({ mode }: { mode: string }) {
  if (mode !== "auto") return null;
  return (
    <ChatNoticeFrame
      role="alert"
      testID="agent-approval-auto-banner"
      tone="red"
      style={{
        padding: "10px 14px",
        display: "flex",
        alignItems: "center",
        gap: 10,
        fontSize: 12,
      }}
    >
      <Icon d={Icons.warning} size={16} />
      <div style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
        <span style={{ fontWeight: 500, fontSize: 12 }}>Auto-approval is on</span>
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--red-lo)" }}>
          GATEWAY_AGENT_ADAPTER_APPROVAL_MODE=auto — every adapter request is permitted without
          review.
        </span>
      </div>
    </ChatNoticeFrame>
  );
}

// AgentApprovalsBanner shows pending approvals for the active session,
// keyed by session id. Compact by design — a stack of more than two
// rows would crowd the chat, so we render the first two and surface
// any overflow as a "+N more" link that opens the next pending row in
// the modal.
//
// Click handler fires the modal open by approval id; the modal owns
// the full-row fetch and decision UI.
export function AgentApprovalsBanner({
  pending,
  onSelect,
}: {
  pending: PendingAgentApproval[];
  onSelect: (approvalID: string) => void;
}) {
  if (pending.length === 0) return null;
  const sorted = [...pending].sort((a, b) => a.created_at.localeCompare(b.created_at));
  const visible = sorted.slice(0, 2);
  const overflow = sorted.length - visible.length;
  // The "+N more" affordance opens the oldest unrendered pending
  // approval first so operators work the queue FIFO.
  const nextOverflowID = sorted[2]?.approval_id;

  return (
    <ChatNoticeFrame
      aria-label="Pending agent approvals"
      testID="agent-approval-banner"
      tone="amber"
    >
      <ChatNoticeHeader
        tone="amber"
        title={pending.length === 1 ? "Approval required" : `${pending.length} approvals required`}
      />
      {visible.map((row) => (
        <PendingApprovalRow key={row.approval_id} row={row} onSelect={onSelect} />
      ))}
      {overflow > 0 && nextOverflowID && (
        <button
          type="button"
          data-testid="agent-approval-banner-more"
          onClick={() => onSelect(nextOverflowID)}
          style={{
            display: "block",
            width: "100%",
            padding: "8px 12px",
            background: "transparent",
            border: "none",
            borderTop: "1px solid var(--amber-border)",
            color: "var(--amber)",
            fontFamily: "var(--font-mono)",
            fontSize: 11,
            textAlign: "left",
            cursor: "pointer",
          }}
        >
          + {overflow} more — review next
        </button>
      )}
    </ChatNoticeFrame>
  );
}

function PendingApprovalRow({
  row,
  onSelect,
}: {
  row: PendingAgentApproval;
  onSelect: (approvalID: string) => void;
}) {
  const label = row.tool_name ? `${row.tool_kind} · ${row.tool_name}` : row.tool_kind;
  const expiresIn = formatExpiresIn(row.expires_at);
  return (
    <ChatNoticeRow
      tone="amber"
      style={{
        display: "flex",
        alignItems: "center",
        gap: 10,
      }}
    >
      <span
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 11,
          color: "var(--amber)",
          minWidth: 0,
          flex: 1,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
        title={`${row.adapter_id} · ${label}`}
      >
        <span style={{ color: "var(--amber-lo)" }}>{row.adapter_id}</span> · {label}
      </span>
      {expiresIn && (
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--amber-lo)" }}>
          {expiresIn}
        </span>
      )}
      <button
        type="button"
        className="btn btn-primary btn-sm"
        onClick={() => onSelect(row.approval_id)}
        data-testid="agent-approval-banner-review"
      >
        Review
      </button>
    </ChatNoticeRow>
  );
}

// formatExpiresIn returns a short "in 4m" / "in 30s" label so
// operators see how long they have before prompt-mode timeout fires.
// Returns "" past expiry (the banner row is about to vanish anyway —
// an "expired" label would just be noise).
function formatExpiresIn(iso: string): string {
  const ms = new Date(iso).getTime() - Date.now();
  if (Number.isNaN(ms) || ms <= 0) return "";
  const s = Math.round(ms / 1000);
  if (s < 60) return `in ${s}s`;
  const m = Math.round(s / 60);
  if (m < 60) return `in ${m}m`;
  const h = Math.round(m / 60);
  return `in ${h}h`;
}
