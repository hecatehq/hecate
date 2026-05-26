import type { ChatChangedFileDiffRecord, ChatChangedFileRecord } from "../../types/chat";
import { TranscriptDiffReview } from "../transcript/TranscriptDiffReview";
import { formatDiffStatSummary } from "../transcript/transcriptActivityHelpers";

import type { VisibleChatMessage } from "./ChatTranscript";

export type ChatWorkspaceChange = {
  key: string;
  messageID: string;
  label: string;
  diffStat?: string;
  diff?: string;
};

export function collectChatWorkspaceChanges(messages: VisibleChatMessage[]): ChatWorkspaceChange[] {
  return messages.flatMap((message) => {
    if (message.role !== "assistant" || (!message.diff_stat && !message.diff)) return [];
    return [
      {
        key: `workspace-files:${message.id}`,
        messageID: message.id,
        label: workspaceChangeLabel(message),
        diffStat: message.diff_stat,
        diff: message.diff,
      },
    ];
  });
}

export function workspaceChangeSummaryLabel(
  change: ChatWorkspaceChange,
  prefix = "Workspace changes",
): string {
  const summary = change.diffStat ? formatDiffStatSummary(change.diffStat) : "";
  return `${prefix}${summary ? ` · ${summary}` : ""}`;
}

export function compactWorkspaceChangeLabel(diffStat?: string): string {
  const summary = diffStat ? formatDiffStatSummary(diffStat) : "";
  const files = summary.match(/\b(\d+)\s+files?\s+changed\b/i)?.[1];
  if (files) return Number(files) === 1 ? "1 file" : `${files} files`;
  return "Files changed";
}

export function ChatWorkspaceChangesPanel({
  changes,
  sessionID,
  focusMessageID,
  onListFiles,
  onGetFileDiff,
  onRevertFiles,
}: {
  changes: ChatWorkspaceChange[];
  sessionID: string;
  focusMessageID?: string | null;
  onListFiles: (sessionID: string, messageID: string) => Promise<ChatChangedFileRecord[]>;
  onGetFileDiff: (
    sessionID: string,
    messageID: string,
    path: string,
  ) => Promise<ChatChangedFileDiffRecord | null>;
  onRevertFiles: (sessionID: string, messageID: string, paths: string[]) => Promise<boolean>;
}) {
  const intro =
    "Changes are already in your workspace. Inspect captured diffs, keep them, or revert selected paths.";

  return (
    <aside
      aria-label="Workspace changes panel"
      style={{
        width: "min(430px, 40vw)",
        minWidth: 340,
        maxWidth: 480,
        flexShrink: 0,
        borderLeft: "1px solid var(--border)",
        background: "var(--bg1)",
        display: "flex",
        flexDirection: "column",
        minHeight: 0,
      }}
    >
      <div
        style={{
          borderBottom: "1px solid var(--border)",
          padding: "14px 14px 12px",
        }}
      >
        <div style={{ fontSize: 12, fontWeight: 650, color: "var(--t0)" }}>Workspace changes</div>
        <div style={{ marginTop: 4, fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
          {changes.length === 1
            ? "One agent turn changed files in this workspace."
            : `${changes.length} agent turns changed files in this workspace.`}
        </div>
      </div>
      <div style={{ overflowY: "auto", padding: 14, display: "grid", gap: 10 }}>
        <div style={{ color: "var(--t2)", fontSize: 11, lineHeight: 1.5 }}>{intro}</div>
        {changes.map((change) => (
          <TranscriptDiffReview
            key={change.key}
            sessionID={sessionID}
            messageID={change.messageID}
            diffStat={change.diffStat}
            diff={change.diff}
            summaryLabel={workspaceChangeSummaryLabel(change, change.label)}
            intro=""
            testID={changes.length === 1 ? "session-diff-review" : "session-diff-review-set"}
            defaultOpen={changes.length === 1 || change.messageID === focusMessageID}
            onListFiles={onListFiles}
            onGetFileDiff={onGetFileDiff}
            onRevertFiles={onRevertFiles}
          />
        ))}
      </div>
    </aside>
  );
}

function workspaceChangeLabel(message: VisibleChatMessage): string {
  const time = message.created_at
    ? new Date(message.created_at).toLocaleTimeString("en-US", {
        hour: "2-digit",
        minute: "2-digit",
      })
    : "";
  const actor = message.agent_name || message.agent_id || message.model || "Assistant";
  return [actor, time].filter(Boolean).join(" · ");
}
