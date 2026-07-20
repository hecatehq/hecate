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
  actionSummary?: string[];
  actionSummaryIncomplete?: boolean;
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
        actionSummary: activity.action_summary ? [...activity.action_summary] : undefined,
        actionSummaryIncomplete: activity.action_summary_incomplete,
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
  if (haystack.includes("http_request") || haystack.includes("web_search")) return "network_egress";
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
  disabled = false,
  onOpenTask,
  onResolve,
}: {
  approvals: HecateTaskApproval[];
  taskID: string;
  runID?: string;
  busyID: string;
  disabled?: boolean;
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
      style={{ maxHeight: "min(42vh, 420px)", overflowY: "auto", overscrollBehavior: "contain" }}
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
        const actionDisabled = disabled || busyID !== "";
        const label = describeTaskApprovalKind(approval.kind || approval.title);
        const hasReviewableSummary = Boolean(approval.actionSummary?.length);
        const inlineApprovalBlockedReason = !hasReviewableSummary
          ? "Open the backing task to review the complete pending actions"
          : approval.actionSummaryIncomplete
            ? "Open the backing task because the inline action summary is incomplete"
            : undefined;
        return (
          <ChatNoticeRow
            key={approval.approvalID}
            tone="amber"
            style={{
              display: "flex",
              flexDirection: "column",
              gap: 12,
              alignItems: "stretch",
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
                    lineHeight: 1.4,
                    overflowWrap: "anywhere",
                  }}
                >
                  {approval.detail}
                </div>
              )}
              {hasReviewableSummary && (
                <div style={{ marginTop: 8 }}>
                  <div
                    style={{
                      color: "var(--amber-lo)",
                      fontSize: 10,
                      fontWeight: 600,
                      letterSpacing: "0.04em",
                      textTransform: "uppercase",
                    }}
                  >
                    Pending actions
                  </div>
                  <div
                    role="region"
                    aria-label={`Review pending actions for ${label}`}
                    style={{
                      maxHeight: 144,
                      marginTop: 5,
                      overflowY: "auto",
                      overscrollBehavior: "contain",
                    }}
                  >
                    <ol
                      aria-label="Pending actions"
                      style={{
                        color: "var(--t1)",
                        fontFamily: "var(--font-mono)",
                        fontSize: 11,
                        lineHeight: 1.5,
                        margin: 0,
                        paddingLeft: 20,
                        paddingRight: 4,
                        overflowWrap: "anywhere",
                      }}
                    >
                      {approval.actionSummary?.map((line, index) => (
                        <li key={`${approval.approvalID}-action-${index}`}>{line}</li>
                      ))}
                    </ol>
                  </div>
                </div>
              )}
              {approval.actionSummaryIncomplete && (
                <div
                  style={{
                    color: "var(--amber-lo)",
                    fontSize: 10,
                    lineHeight: 1.4,
                    marginTop: 5,
                  }}
                >
                  Some calls or details were omitted or could not be summarized safely. Open the
                  backing task to review before approving.
                </div>
              )}
              {!hasReviewableSummary && (
                <div
                  role="status"
                  style={{
                    color: "var(--amber-lo)",
                    fontSize: 10,
                    lineHeight: 1.4,
                    marginTop: 5,
                  }}
                >
                  Open the backing task to review the complete pending actions before approving.
                </div>
              )}
            </div>
            <div
              style={{
                display: "flex",
                gap: 8,
                flexWrap: "wrap",
                justifyContent: "flex-end",
              }}
            >
              <button
                type="button"
                className="btn btn-primary btn-sm"
                aria-label={`Approve ${label}`}
                disabled={actionDisabled || Boolean(inlineApprovalBlockedReason)}
                title={inlineApprovalBlockedReason}
                onClick={() => onResolve(approval.approvalID, "approve")}
              >
                {approveBusy ? "Approving..." : "Approve"}
              </button>
              <button
                type="button"
                className="btn btn-danger btn-sm"
                aria-label={`Reject ${label}`}
                disabled={actionDisabled}
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
