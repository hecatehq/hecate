import { useEffect, useRef, useState } from "react";

import type {
  ChatChangedFileDiffRecord,
  ChatChangedFileRecord,
  ChatWorkspaceDiffRecord,
} from "../../types/chat";
import { InlineError } from "../shared/Atoms";
import { DiffViewer } from "../shared/DiffViewer";
import { Icon, Icons } from "../shared/Icons";
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
  const [expandedDiffPaths, setExpandedDiffPaths] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [loadingPath, setLoadingPath] = useState("");
  const [copyingPath, setCopyingPath] = useState("");
  const [copiedKey, setCopiedKey] = useState("");
  const [revertingPath, setRevertingPath] = useState("");
  const [confirmRevertPath, setConfirmRevertPath] = useState("");
  const [loadFailed, setLoadFailed] = useState(false);
  const [localError, setLocalError] = useState("");
  const copiedTimerRef = useRef<number | null>(null);

  async function refresh() {
    setLoading(true);
    setLoadFailed(false);
    setLocalError("");
    const next = await onGetWorkspaceDiff(sessionID);
    setSnapshot(next);
    setFileDiffs({});
    setExpandedDiffPaths([]);
    setLoadFailed(next === null);
    const firstFile = next?.has_changes ? next.files?.[0] : undefined;
    if (firstFile) {
      setExpandedDiffPaths([firstFile.path]);
      setLoadingPath(firstFile.path);
      const firstDiff = await onGetWorkspaceFileDiff(sessionID, firstFile.path);
      if (firstDiff) setFileDiffs({ [firstFile.path]: firstDiff });
      setLoadingPath("");
    }
    setLoading(false);
  }

  async function loadFileDiff(
    file: ChatChangedFileRecord,
  ): Promise<ChatChangedFileDiffRecord | null> {
    const cached = fileDiffs[file.path];
    if (cached) return cached;
    const next = await onGetWorkspaceFileDiff(sessionID, file.path);
    if (next) setFileDiffs((current) => ({ ...current, [file.path]: next }));
    return next;
  }

  async function toggleFileDiff(file: ChatChangedFileRecord) {
    if (expandedDiffPaths.includes(file.path)) {
      setExpandedDiffPaths((current) => current.filter((path) => path !== file.path));
      return;
    }
    setExpandedDiffPaths((current) => [...current, file.path]);
    if (fileDiffs[file.path]) return;
    setLoadingPath(file.path);
    setLocalError("");
    const next = await loadFileDiff(file);
    if (!next) {
      setExpandedDiffPaths((current) => current.filter((path) => path !== file.path));
      setLocalError("Could not load that current file diff.");
    }
    setLoadingPath("");
  }

  async function copyText(text: string, key: string) {
    if (!navigator.clipboard?.writeText) {
      setLocalError("Clipboard access is not available in this environment.");
      return;
    }
    try {
      await navigator.clipboard.writeText(text);
      setCopiedKey(key);
      if (copiedTimerRef.current !== null) window.clearTimeout(copiedTimerRef.current);
      copiedTimerRef.current = window.setTimeout(() => {
        setCopiedKey("");
        copiedTimerRef.current = null;
      }, 1500);
    } catch {
      setLocalError("Could not copy that diff.");
    }
  }

  async function copyFileDiff(file: ChatChangedFileRecord) {
    setCopyingPath(file.path);
    setLocalError("");
    const next = await loadFileDiff(file);
    if (next?.diff) {
      await copyText(next.diff, `file:${file.path}`);
    } else {
      setLocalError("Could not load that current file diff.");
    }
    setCopyingPath("");
  }

  async function confirmRevert(paths: string[], label: string) {
    setRevertingPath(label);
    setLocalError("");
    const next = await onRevertWorkspaceFiles(sessionID, paths);
    if (next) {
      setSnapshot(next);
      if (paths.length === 0) {
        setFileDiffs({});
        setExpandedDiffPaths([]);
      } else {
        setFileDiffs((current) => {
          const nextDiffs = { ...current };
          for (const path of paths) delete nextDiffs[path];
          return nextDiffs;
        });
        setExpandedDiffPaths((current) => current.filter((path) => !paths.includes(path)));
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

  useEffect(() => {
    return () => {
      if (copiedTimerRef.current !== null) window.clearTimeout(copiedTimerRef.current);
    };
  }, []);

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
          alignItems: "flex-start",
          borderBottom: "1px solid var(--border)",
          display: "flex",
          gap: 10,
          justifyContent: "space-between",
          padding: "14px 14px 12px",
        }}
      >
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 12, fontWeight: 650, color: "var(--t0)" }}>Workspace changes</div>
          <div style={{ marginTop: 4, fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
            {loading
              ? "Checking current Git diff..."
              : hasChanges
                ? summary || "Current Git diff has changes."
                : "No current Git diff."}
          </div>
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
      <div
        style={{
          flex: 1,
          overflowX: "hidden",
          overflowY: "auto",
          padding: 14,
          display: "grid",
          gap: 10,
          alignContent: "start",
          minHeight: 0,
        }}
      >
        {loadFailed ? (
          <div style={{ color: "var(--red)", fontSize: 11, lineHeight: 1.5 }}>
            Could not load the current workspace diff.
          </div>
        ) : hasChanges ? (
          <>
            {files.length > 0 ? (
              <WorkspaceDiffPanel
                copiedKey={copiedKey}
                copyingPath={copyingPath}
                diff={diff}
                expandedDiffPaths={expandedDiffPaths}
                files={files}
                fileDiffs={fileDiffs}
                loadingPath={loadingPath}
                summary={summary}
                workspace={workspace}
                revertingPath={revertingPath}
                confirmRevertPath={confirmRevertPath}
                onCopyFullDiff={() => void copyText(diff, "full")}
                onCopyFileDiff={(file) => void copyFileDiff(file)}
                onToggleDiff={(file) => void toggleFileDiff(file)}
                onRequestRevert={setConfirmRevertPath}
                onCancelRevert={() => setConfirmRevertPath("")}
                onConfirmRevert={(paths, label) => void confirmRevert(paths, label)}
              />
            ) : (
              diffStat && <DiffStatList diffStat={diffStat} />
            )}
            {localError && <InlineError message={localError} />}
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

function WorkspaceDiffPanel({
  copiedKey,
  copyingPath,
  diff,
  expandedDiffPaths,
  files,
  fileDiffs,
  loadingPath,
  summary,
  workspace,
  revertingPath,
  confirmRevertPath,
  onCopyFullDiff,
  onCopyFileDiff,
  onToggleDiff,
  onRequestRevert,
  onCancelRevert,
  onConfirmRevert,
}: {
  copiedKey: string;
  copyingPath: string;
  diff: string;
  expandedDiffPaths: string[];
  files: ChatChangedFileRecord[];
  fileDiffs: Record<string, ChatChangedFileDiffRecord>;
  loadingPath: string;
  summary: string;
  workspace: string;
  revertingPath: string;
  confirmRevertPath: string;
  onCopyFullDiff: () => void;
  onCopyFileDiff: (file: ChatChangedFileRecord) => void;
  onToggleDiff: (file: ChatChangedFileRecord) => void;
  onRequestRevert: (path: string) => void;
  onCancelRevert: () => void;
  onConfirmRevert: (paths: string[], label: string) => void;
}) {
  return (
    <div
      style={{
        alignSelf: "start",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        background: "var(--bg2)",
        overflow: "hidden",
        minWidth: 0,
      }}
    >
      <div
        style={{
          alignItems: "flex-start",
          borderBottom: "1px solid var(--border)",
          display: "flex",
          gap: 8,
          justifyContent: "space-between",
          padding: "8px 9px",
        }}
      >
        <div style={{ minWidth: 0 }}>
          <div style={{ color: "var(--t1)", fontSize: 11, fontWeight: 650 }}>
            Live workspace diff
          </div>
          <div
            title={workspace}
            style={{
              color: "var(--t3)",
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              marginTop: 1,
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {workspace}
          </div>
          <div
            style={{
              color: "var(--t3)",
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              marginTop: 1,
            }}
          >
            {summary || `${files.length} current changed file${files.length === 1 ? "" : "s"}`}
          </div>
        </div>
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
          <div style={{ display: "flex", gap: 4, flexShrink: 0 }}>
            {diff && (
              <button
                aria-label="Copy complete workspace patch"
                className="btn btn-ghost btn-sm"
                disabled={Boolean(revertingPath)}
                onClick={onCopyFullDiff}
                title="Copy complete workspace patch"
                type="button"
              >
                <Icon d={copiedKey === "full" ? Icons.check : Icons.copy} size={12} />
                {copiedKey === "full" ? "Copied" : "Copy patch"}
              </button>
            )}
            <button
              className="btn btn-ghost btn-sm"
              disabled={Boolean(revertingPath)}
              onClick={() => onRequestRevert("__all__")}
              type="button"
            >
              Discard all
            </button>
          </div>
        )}
      </div>
      <div style={{ display: "grid" }}>
        {files.map((file) => {
          const diffSelected = expandedDiffPaths.includes(file.path);
          const selectedDiff = fileDiffs[file.path];
          const diffButtonLabel = diffSelected
            ? `Hide diff ${file.path}`
            : `Show diff ${file.path}`;
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
                  background: diffSelected ? "var(--teal-bg)" : "transparent",
                  display: "grid",
                  gap: 6,
                  gridTemplateColumns: "minmax(0, 1fr) auto",
                  padding: "6px 8px",
                }}
              >
                <button
                  type="button"
                  aria-label={diffButtonLabel}
                  onClick={() => onToggleDiff(file)}
                  title={diffButtonLabel}
                  style={{
                    alignItems: "center",
                    background: "transparent",
                    border: 0,
                    color: "inherit",
                    cursor: "pointer",
                    display: "grid",
                    gap: 7,
                    gridTemplateColumns: "auto minmax(0, 1fr)",
                    minWidth: 0,
                    padding: 0,
                    textAlign: "left",
                  }}
                >
                  <Icon d={diffSelected ? Icons.chevD : Icons.chevR} size={11} />
                  <span style={{ minWidth: 0 }}>
                    <span
                      style={{
                        color: "var(--t1)",
                        display: "block",
                        fontFamily: "var(--font-mono)",
                        fontSize: 10.5,
                        lineHeight: 1.3,
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                        whiteSpace: "nowrap",
                      }}
                    >
                      {file.path}
                    </span>
                    <span
                      style={{
                        color: "var(--t3)",
                        display: "block",
                        fontFamily: "var(--font-mono)",
                        fontSize: 9.5,
                        lineHeight: 1.25,
                        marginTop: 1,
                      }}
                    >
                      {formatChangedFileMeta(file)}
                    </span>
                  </span>
                </button>
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
                      disabled={copyingPath === file.path || Boolean(revertingPath)}
                      aria-label={`Copy diff ${file.path}`}
                      onClick={() => onCopyFileDiff(file)}
                      title={`Copy diff ${file.path}`}
                      type="button"
                    >
                      <Icon
                        d={copiedKey === `file:${file.path}` ? Icons.check : Icons.copy}
                        size={12}
                      />
                    </button>
                    <button
                      className="btn btn-ghost btn-sm"
                      disabled={Boolean(revertingPath)}
                      aria-label={`Discard ${file.path}`}
                      onClick={() => onRequestRevert(file.path)}
                      title={`Discard ${file.path}`}
                      type="button"
                    >
                      <Icon d={Icons.revert} size={12} />
                    </button>
                  </div>
                )}
              </div>
              {diffSelected && selectedDiff && (
                <WorkspaceDiffPreview
                  diff={selectedDiff.diff}
                  testID="workspace-file-diff-preview"
                />
              )}
              {diffSelected && !selectedDiff && loadingPath === file.path && (
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

function WorkspaceDiffPreview({
  diff,
  testID = "workspace-diff-preview",
}: {
  diff: string;
  testID?: string;
}) {
  return (
    <div
      data-testid={testID}
      style={{
        background: "var(--bg0)",
        borderTop: "1px solid var(--border)",
        isolation: "isolate",
        minWidth: 0,
        overscrollBehavior: "contain",
        overflow: "visible",
        padding: 8,
        position: "relative",
      }}
    >
      <DiffViewer compact embedded diff={diff} />
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
