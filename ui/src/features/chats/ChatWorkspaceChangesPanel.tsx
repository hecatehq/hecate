import { useEffect, useState } from "react";

import type {
  ChatChangedFileDiffRecord,
  ChatChangedFileRecord,
  ChatWorkspaceDiffRecord,
} from "../../types/chat";
import { CodeBlock, InlineError } from "../shared/Atoms";
import { DiffStatList } from "../transcript/TranscriptActivityTimeline";
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
  sessionID,
  workspace,
  onGetWorkspaceDiff,
  onGetWorkspaceFileDiff,
  onRevertWorkspaceFiles,
}: {
  sessionID: string;
  workspace: string;
  onGetWorkspaceDiff: (sessionID: string) => Promise<ChatWorkspaceDiffRecord | null>;
  onGetWorkspaceFileDiff: (
    sessionID: string,
    path: string,
  ) => Promise<ChatChangedFileDiffRecord | null>;
  onRevertWorkspaceFiles: (
    sessionID: string,
    paths: string[],
  ) => Promise<ChatWorkspaceDiffRecord | null>;
}) {
  const [snapshot, setSnapshot] = useState<ChatWorkspaceDiffRecord | null>(null);
  const [fileDiffs, setFileDiffs] = useState<Record<string, ChatChangedFileDiffRecord>>({});
  const [openDiffPaths, setOpenDiffPaths] = useState<Set<string>>(() => new Set());
  const [loading, setLoading] = useState(false);
  const [loadingPath, setLoadingPath] = useState("");
  const [revertingPath, setRevertingPath] = useState("");
  const [confirmRevertPath, setConfirmRevertPath] = useState("");
  const [loadFailed, setLoadFailed] = useState(false);
  const [localError, setLocalError] = useState("");

  async function refresh() {
    setLoading(true);
    setLoadFailed(false);
    setLocalError("");
    const next = await onGetWorkspaceDiff(sessionID);
    setSnapshot(next);
    setFileDiffs({});
    setOpenDiffPaths(new Set());
    setLoadFailed(next === null);
    setLoading(false);
  }

  async function toggleFileDiff(file: ChatChangedFileRecord) {
    if (openDiffPaths.has(file.path)) {
      setOpenDiffPaths((current) => {
        const next = new Set(current);
        next.delete(file.path);
        return next;
      });
      return;
    }
    if (fileDiffs[file.path]) {
      setOpenDiffPaths((current) => new Set(current).add(file.path));
      return;
    }
    setLoadingPath(file.path);
    setLocalError("");
    const next = await onGetWorkspaceFileDiff(sessionID, file.path);
    if (next) {
      setFileDiffs((current) => ({ ...current, [file.path]: next }));
      setOpenDiffPaths((current) => new Set(current).add(file.path));
    } else {
      setLocalError("Could not load that current file diff.");
    }
    setLoadingPath("");
  }

  async function confirmRevert(paths: string[], label: string) {
    setRevertingPath(label);
    setLocalError("");
    const next = await onRevertWorkspaceFiles(sessionID, paths);
    if (next) {
      setSnapshot(next);
      if (paths.length === 0) {
        setFileDiffs({});
        setOpenDiffPaths(new Set());
      } else {
        setFileDiffs((current) => {
          const nextDiffs = { ...current };
          for (const path of paths) delete nextDiffs[path];
          return nextDiffs;
        });
        setOpenDiffPaths((current) => {
          const nextOpen = new Set(current);
          for (const path of paths) nextOpen.delete(path);
          return nextOpen;
        });
      }
    } else {
      setLocalError("Could not discard those workspace changes.");
    }
    setConfirmRevertPath("");
    setRevertingPath("");
  }

  useEffect(() => {
    void refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionID, workspace]);

  const diffStat = snapshot?.diff_stat?.trim() ?? "";
  const diff = snapshot?.diff?.trim() ?? "";
  const hasChanges = Boolean(snapshot?.has_changes && (diffStat || diff));
  const summary = diffStat ? formatDiffStatSummary(diffStat) : "";
  const files = snapshot?.files ?? [];

  return (
    <div
      style={{
        background: "var(--bg1)",
        display: "flex",
        flexDirection: "column",
        flex: 1,
        minHeight: 0,
        minWidth: 0,
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
          {loading
            ? "Checking current git diff..."
            : hasChanges
              ? summary || "Current git diff has changes."
              : "No current git diff."}
        </div>
      </div>
      <div
        style={{
          overflowX: "hidden",
          overflowY: "auto",
          padding: 14,
          display: "grid",
          gap: 10,
        }}
      >
        <div style={{ alignItems: "center", display: "flex", gap: 8 }}>
          <div
            style={{
              color: "var(--t2)",
              flex: 1,
              fontSize: 11,
              lineHeight: 1.5,
              minWidth: 0,
            }}
          >
            Live Git diff for{" "}
            <span style={{ color: "var(--t1)", fontFamily: "var(--font-mono)" }}>{workspace}</span>.
          </div>
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            disabled={loading}
            onClick={() => void refresh()}
            style={{ flexShrink: 0, fontSize: 11 }}
          >
            {loading ? "Refreshing..." : "Refresh"}
          </button>
        </div>
        {loadFailed ? (
          <div style={{ color: "var(--red)", fontSize: 11, lineHeight: 1.5 }}>
            Could not load the current workspace diff.
          </div>
        ) : hasChanges ? (
          <>
            {files.length > 0 ? (
              <WorkspaceFileList
                files={files}
                fileDiffs={fileDiffs}
                openDiffPaths={openDiffPaths}
                loadingPath={loadingPath}
                revertingPath={revertingPath}
                confirmRevertPath={confirmRevertPath}
                onToggleDiff={(file) => void toggleFileDiff(file)}
                onRequestRevert={setConfirmRevertPath}
                onCancelRevert={() => setConfirmRevertPath("")}
                onConfirmRevert={(paths, label) => void confirmRevert(paths, label)}
              />
            ) : (
              diffStat && <DiffStatList diffStat={diffStat} />
            )}
            {localError && <InlineError message={localError} />}
            {diff && (
              <details style={{ display: "grid", gap: 6 }}>
                <summary
                  style={{
                    color: "var(--t2)",
                    cursor: "pointer",
                    fontFamily: "var(--font-mono)",
                    fontSize: 11,
                  }}
                >
                  current git diff
                </summary>
                <div style={{ marginTop: 6 }}>
                  <CodeBlock code={diff} lang="diff" />
                </div>
              </details>
            )}
          </>
        ) : (
          <div style={{ color: "var(--t3)", fontSize: 11, lineHeight: 1.5 }}>
            The current workspace is clean.
          </div>
        )}
      </div>
    </div>
  );
}

function WorkspaceFileList({
  files,
  fileDiffs,
  openDiffPaths,
  loadingPath,
  revertingPath,
  confirmRevertPath,
  onToggleDiff,
  onRequestRevert,
  onCancelRevert,
  onConfirmRevert,
}: {
  files: ChatChangedFileRecord[];
  fileDiffs: Record<string, ChatChangedFileDiffRecord>;
  openDiffPaths: Set<string>;
  loadingPath: string;
  revertingPath: string;
  confirmRevertPath: string;
  onToggleDiff: (file: ChatChangedFileRecord) => void;
  onRequestRevert: (path: string) => void;
  onCancelRevert: () => void;
  onConfirmRevert: (paths: string[], label: string) => void;
}) {
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        background: "var(--bg2)",
        overflow: "hidden",
        minWidth: 0,
      }}
    >
      <div
        style={{
          alignItems: "center",
          borderBottom: "1px solid var(--border)",
          display: "flex",
          gap: 8,
          justifyContent: "space-between",
          padding: "6px 8px",
        }}
      >
        <span style={{ color: "var(--t2)", fontFamily: "var(--font-mono)", fontSize: 11 }}>
          {files.length} current changed file{files.length === 1 ? "" : "s"}
        </span>
        {confirmRevertPath === "__all__" ? (
          <div style={{ display: "flex", gap: 6 }}>
            <button
              className="btn btn-ghost btn-sm"
              disabled={Boolean(revertingPath)}
              onClick={() => onConfirmRevert([], "__all__")}
              type="button"
            >
              {revertingPath === "__all__" ? "Discarding..." : "Confirm discard all"}
            </button>
            <button className="btn btn-ghost btn-sm" onClick={onCancelRevert} type="button">
              Cancel
            </button>
          </div>
        ) : (
          <button
            className="btn btn-ghost btn-sm"
            disabled={Boolean(revertingPath)}
            onClick={() => onRequestRevert("__all__")}
            type="button"
          >
            Discard all
          </button>
        )}
      </div>
      <div style={{ display: "grid" }}>
        {files.map((file) => {
          const fileDiff = fileDiffs[file.path];
          const diffOpen = openDiffPaths.has(file.path);
          const diffButtonLabel = diffOpen ? `Hide diff ${file.path}` : `Show diff ${file.path}`;
          return (
            <div
              key={file.path}
              style={{
                borderTop: "1px solid var(--border)",
                display: "grid",
                minWidth: 0,
              }}
            >
              <div
                style={{
                  alignItems: "center",
                  display: "grid",
                  gap: 6,
                  gridTemplateColumns: "minmax(0, 1fr) auto",
                  padding: "5px 8px",
                }}
              >
                <div style={{ minWidth: 0 }}>
                  <div
                    style={{
                      color: "var(--t1)",
                      fontFamily: "var(--font-mono)",
                      fontSize: 10.5,
                      lineHeight: 1.3,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {file.path}
                  </div>
                  <div
                    style={{
                      color: "var(--t3)",
                      fontFamily: "var(--font-mono)",
                      fontSize: 9.5,
                      lineHeight: 1.25,
                      marginTop: 1,
                    }}
                  >
                    {formatChangedFileMeta(file)}
                  </div>
                </div>
                {confirmRevertPath === file.path ? (
                  <div style={{ display: "flex", gap: 4 }}>
                    <button
                      className="btn btn-ghost btn-sm"
                      disabled={Boolean(revertingPath)}
                      aria-label={`Confirm discard ${file.path}`}
                      onClick={() => onConfirmRevert([file.path], file.path)}
                      title={`Confirm discard ${file.path}`}
                      type="button"
                    >
                      {revertingPath === file.path ? "Discarding..." : "Confirm"}
                    </button>
                    <button className="btn btn-ghost btn-sm" onClick={onCancelRevert} type="button">
                      Cancel
                    </button>
                  </div>
                ) : (
                  <div style={{ display: "flex", gap: 4 }}>
                    <button
                      className="btn btn-ghost btn-sm"
                      disabled={loadingPath === file.path || Boolean(revertingPath)}
                      aria-label={diffButtonLabel}
                      onClick={() => onToggleDiff(file)}
                      title={diffButtonLabel}
                      type="button"
                    >
                      {loadingPath === file.path ? "Loading..." : diffOpen ? "Hide" : "Diff"}
                    </button>
                    <button
                      className="btn btn-ghost btn-sm"
                      disabled={Boolean(revertingPath)}
                      aria-label={`Discard ${file.path}`}
                      onClick={() => onRequestRevert(file.path)}
                      title={`Discard ${file.path}`}
                      type="button"
                    >
                      Discard
                    </button>
                  </div>
                )}
              </div>
              {diffOpen && fileDiff && (
                <div
                  style={{
                    borderTop: "1px solid var(--border)",
                    minWidth: 0,
                    overflow: "hidden",
                    padding: "6px 8px 8px",
                  }}
                >
                  <div
                    style={{
                      color: "var(--t2)",
                      fontFamily: "var(--font-mono)",
                      fontSize: 10,
                      marginBottom: 5,
                    }}
                  >
                    current diff · {fileDiff.path}
                  </div>
                  <CodeBlock code={fileDiff.diff} lang="diff" />
                </div>
              )}
              {diffOpen && !fileDiff && loadingPath === file.path && (
                <div
                  style={{
                    borderTop: "1px solid var(--border)",
                    color: "var(--t3)",
                    fontFamily: "var(--font-mono)",
                    fontSize: 10,
                    padding: "6px 8px 8px",
                  }}
                >
                  Loading current diff...
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function formatChangedFileMeta(file: ChatChangedFileRecord): string {
  const parts = [file.status || "modified"];
  if (file.additions > 0) parts.push(`+${file.additions}`);
  if (file.deletions > 0) parts.push(`-${file.deletions}`);
  if (parts.length === 1) parts.push("no line delta");
  return parts.join(" · ");
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
