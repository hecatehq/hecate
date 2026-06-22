import { formatLocaleTime } from "../../lib/format";
import { hasAgentTerminalToolMention } from "../../lib/agent-terminal-tools";
import type { ChatActivityRecord, ChatSegmentRecord, ChatSessionRecord } from "../../types/chat";
import { ChatNoticeFrame, ChatNoticeHeader, ChatNoticeRow } from "./ChatNotice";
import { toChatSegmentViewModel } from "./chatTurnViewModels";

export type HecateTaskApproval = {
  approvalID: string;
  title: string;
  kind?: string;
  detail?: string;
  createdAt?: string;
};

export function pendingHecateTaskApprovals(
  session: ChatSessionRecord | null,
): HecateTaskApproval[] {
  if (!session?.task_id) return [];
  const byID = new Map<string, HecateTaskApproval>();
  for (const message of session.messages ?? []) {
    for (const activity of message.activities ?? []) {
      if (activity.type !== "approval") continue;
      const approvalID = activity.approval_id || parseProjectedTaskApprovalID(activity.id);
      if (!approvalID) continue;
      const status = activity.status || "";
      if (isResolvedTaskApprovalStatus(status)) {
        byID.delete(approvalID);
        continue;
      }
      const pending =
        activity.needs_action || status === "pending" || status === "awaiting_approval";
      if (!pending) {
        byID.delete(approvalID);
        continue;
      }
      byID.set(approvalID, {
        approvalID,
        title: activity.title || activity.kind || "Approval required",
        kind: taskApprovalDisplayKind(activity),
        detail: cleanApprovalDetail(activity.detail),
        createdAt: activity.created_at,
      });
    }
  }
  return [...byID.values()].sort((a, b) => (a.createdAt || "").localeCompare(b.createdAt || ""));
}

function cleanApprovalDetail(detail?: string): string {
  return (detail || "")
    .replace(/\s+-\s+awaiting_approval$/i, "")
    .replace(/\s+-\s+pending$/i, "")
    .replace(/^Agent requested tools that require approval:\s*/i, "")
    .replace(/^builtin\.agent_loop_approval$/i, "")
    .trim();
}

function taskApprovalDisplayKind(activity: ChatActivityRecord): string {
  const kind = (activity.kind || "").trim();
  if (kind && kind !== "approval" && kind !== "agent_loop_approval") {
    return kind;
  }
  const haystack = `${activity.title || ""} ${activity.detail || ""}`.toLowerCase();
  if (hasAgentTerminalToolMention(haystack)) return "terminal_tool";
  if (haystack.includes("shell_exec")) return "shell_command";
  if (haystack.includes("git_exec")) return "git_exec";
  if (haystack.includes("file_write")) return "file_write";
  if (haystack.includes("network_egress")) return "network_egress";
  if (haystack.includes("agent_loop_tool_call")) return "agent_loop_tool_call";
  return "approval";
}

function hecateAgentSessionIsActive(status?: string): boolean {
  return status === "queued" || status === "running" || status === "awaiting_approval";
}

export function activeTaskBackedHecateSegment(
  session: ChatSessionRecord | null,
): ChatSegmentRecord | null {
  const segments = [...(session?.segments ?? [])].reverse();
  const activeSegment = segments.find((segment) => {
    const turn = toChatSegmentViewModel(segment);
    return turn.isTaskBacked && hecateAgentSessionIsActive(turn.status);
  });
  if (activeSegment) {
    return activeSegment;
  }
  if (session?.task_id && hecateAgentSessionIsActive(session.status)) {
    return {
      id: `task:${session.task_id}`,
      execution_mode: "hecate_task",
      provider: session.provider,
      model: session.model,
      task_id: session.task_id,
      latest_run_id: session.latest_run_id,
      workspace: session.workspace,
      status: session.status,
      message_count: 0,
      updated_at: session.updated_at,
    };
  }
  return null;
}

function parseProjectedTaskApprovalID(id?: string): string {
  const prefix = "task:approval:";
  if (!id?.startsWith(prefix)) return "";
  return id.slice(prefix.length);
}

function isResolvedTaskApprovalStatus(status: string): boolean {
  switch (status) {
    case "approved":
    case "rejected":
    case "denied":
    case "cancelled":
    case "timed_out":
      return true;
    default:
      return false;
  }
}

export function HecateTaskApprovalsBanner({
  approvals,
  taskID,
  runID,
  busyID,
  onOpenTask,
  onResolve,
}: {
  approvals: HecateTaskApproval[];
  taskID: string;
  runID?: string;
  busyID: string;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onResolve: (approvalID: string, decision: "approve" | "reject") => void;
}) {
  const visible = approvals.slice(0, 2);
  const overflow = approvals.length - visible.length;
  return (
    <ChatNoticeFrame
      aria-label="Pending Hecate Chat task approvals"
      testID="hecate-task-approval-banner"
      tone="amber"
    >
      <ChatNoticeHeader
        tone="amber"
        title={
          approvals.length === 1 ? "Approval required" : `${approvals.length} approvals required`
        }
        action={
          onOpenTask && (
            <button
              type="button"
              className="btn btn-ghost btn-sm"
              onClick={() => onOpenTask(taskID, runID)}
              style={{ marginLeft: "auto" }}
            >
              Open task
            </button>
          )
        }
      />
      {visible.map((approval) => {
        const approveBusy = busyID === `${approval.approvalID}:approve`;
        const rejectBusy = busyID === `${approval.approvalID}:reject`;
        const disabled = busyID !== "";
        const label = describeTaskApprovalKind(approval.kind || approval.title);
        return (
          <ChatNoticeRow
            key={approval.approvalID}
            tone="amber"
            style={{
              display: "grid",
              gridTemplateColumns: "minmax(0, 1fr) auto",
              gap: 12,
              alignItems: "center",
            }}
          >
            <div style={{ minWidth: 0 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <span
                  style={{
                    fontFamily: "var(--font-mono)",
                    fontSize: 11,
                    color: "var(--amber)",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  {label}
                </span>
                {approval.createdAt && (
                  <span
                    style={{
                      fontFamily: "var(--font-mono)",
                      fontSize: 10,
                      color: "var(--amber-lo)",
                    }}
                  >
                    {formatLocaleTime(approval.createdAt)}
                  </span>
                )}
              </div>
              {approval.detail && (
                <div
                  style={{
                    fontSize: 11,
                    color: "var(--amber-lo)",
                    marginTop: 3,
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  {approval.detail}
                </div>
              )}
            </div>
            <div style={{ display: "flex", gap: 8 }}>
              <button
                type="button"
                className="btn btn-primary btn-sm"
                aria-label={`Approve ${label}`}
                disabled={disabled}
                onClick={() => onResolve(approval.approvalID, "approve")}
              >
                {approveBusy ? "Approving..." : "Approve"}
              </button>
              <button
                type="button"
                className="btn btn-danger btn-sm"
                aria-label={`Reject ${label}`}
                disabled={disabled}
                onClick={() => onResolve(approval.approvalID, "reject")}
              >
                {rejectBusy ? "Rejecting..." : "Reject"}
              </button>
            </div>
          </ChatNoticeRow>
        );
      })}
      {overflow > 0 && (
        <ChatNoticeRow
          tone="amber"
          style={{
            padding: "7px 12px",
            color: "var(--amber)",
            fontFamily: "var(--font-mono)",
            fontSize: 11,
          }}
        >
          + {overflow} more in the backing Task
        </ChatNoticeRow>
      )}
    </ChatNoticeFrame>
  );
}

function describeTaskApprovalKind(kind: string): string {
  switch (kind) {
    case "approval":
      return "Approval";
    case "shell_command":
      return "Shell execution";
    case "terminal_tool":
      return "Terminal tool";
    case "git_exec":
      return "Git execution";
    case "file_write":
      return "File write";
    case "network_egress":
      return "Network egress";
    case "agent_loop_tool_call":
      return "Agent tool call";
    default:
      return kind.replaceAll("_", " ");
  }
}
