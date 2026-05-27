import { useEffect, useState } from "react";

import type {
  ChatChangedFileDiffRecord,
  ChatChangedFileRecord,
  ChatWorkspaceDiffRecord,
} from "../../types/chat";
import { InlineError } from "../shared/Atoms";
import { DiffViewer } from "../shared/DiffViewer";
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
  const [selectedDiffPath, setSelectedDiffPath] = useState("");
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
    setSelectedDiffPath("");
    setLoadFailed(next === null);
    setLoading(false);
  }

  async function toggleFileDiff(file: ChatChangedFileRecord) {
    if (selectedDiffPath === file.path) {
      setSelectedDiffPath("");
      return;
    }
    if (fileDiffs[file.path]) {
      setSelectedDiffPath(file.path);
      return;
    }
    setLoadingPath(file.path);
    setLocalError("");
    const next = await onGetWorkspaceFileDiff(sessionID, file.path);
    if (next) {
      setFileDiffs((current) => ({ ...current, [file.path]: next }));
      setSelectedDiffPath(file.path);
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
        setSelectedDiffPath("");
      } else {
        setFileDiffs((current) => {
          const nextDiffs = { ...current };
          for (const path of paths) delete nextDiffs[path];
          return nextDiffs;
        });
        if (paths.includes(selectedDiffPath)) setSelectedDiffPath("");
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
        <WorkspaceDiffSource workspace={workspace} />
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
                selectedDiffPath={selectedDiffPath}
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
              <details
                style={{
                  alignSelf: "start",
                  background: "var(--bg2)",
                  border: "1px solid var(--border)",
                  borderRadius: "var(--radius-sm)",
                  display: "grid",
                  overflow: "hidden",
                }}
              >
                <summary
                  style={{
                    alignItems: "center",
                    color: "var(--t1)",
                    cursor: "pointer",
                    display: "flex",
                    fontFamily: "var(--font-mono)",
                    fontSize: 11,
                    gap: 8,
                    justifyContent: "space-between",
                    padding: "7px 9px",
                  }}
                >
                  <span>Complete patch</span>
                  <span style={{ color: "var(--t3)", fontSize: 10 }}>
                    {summary || "current Git diff"}
                  </span>
                </summary>
                <WorkspaceDiffPreview diff={diff} height="min(58vh, 640px)" />
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

function WorkspaceDiffSource({ workspace }: { workspace: string }) {
  return (
    <div
      style={{
        alignSelf: "start",
        background: "color-mix(in srgb, var(--bg2) 86%, var(--accent) 14%)",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        display: "grid",
        gap: 4,
        minWidth: 0,
        padding: "8px 9px",
      }}
    >
      <div style={{ color: "var(--t2)", fontSize: 10, letterSpacing: "0.06em" }}>
        LIVE WORKSPACE DIFF
      </div>
      <div
        title={workspace}
        style={{
          color: "var(--t1)",
          fontFamily: "var(--font-mono)",
          fontSize: 11,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {workspace}
      </div>
    </div>
  );
}

function WorkspaceFileList({
  files,
  fileDiffs,
  selectedDiffPath,
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
  selectedDiffPath: string;
  loadingPath: string;
  revertingPath: string;
  confirmRevertPath: string;
  onToggleDiff: (file: ChatChangedFileRecord) => void;
  onRequestRevert: (path: string) => void;
  onCancelRevert: () => void;
  onConfirmRevert: (paths: string[], label: string) => void;
}) {
  const selectedDiff = selectedDiffPath ? fileDiffs[selectedDiffPath] : undefined;

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
          alignItems: "center",
          borderBottom: "1px solid var(--border)",
          display: "flex",
          gap: 8,
          justifyContent: "space-between",
          padding: "6px 8px",
        }}
      >
        <div style={{ minWidth: 0 }}>
          <div style={{ color: "var(--t1)", fontSize: 11, fontWeight: 650 }}>Changed files</div>
          <div
            style={{
              color: "var(--t3)",
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              marginTop: 1,
            }}
          >
            {files.length} file{files.length === 1 ? "" : "s"} in the working tree
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
          const diffSelected = selectedDiffPath === file.path;
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
                      {loadingPath === file.path
                        ? "Loading..."
                        : diffSelected
                          ? "Hide"
                          : "View diff"}
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
            </div>
          );
        })}
      </div>
      {selectedDiff && (
        <WorkspaceDiffPreview
          diff={selectedDiff.diff}
          height="min(48vh, 520px)"
          testID="workspace-file-diff-preview"
        />
      )}
      {selectedDiffPath && !selectedDiff && loadingPath === selectedDiffPath && (
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
}

function WorkspaceDiffPreview({
  diff,
  height,
  testID = "workspace-diff-preview",
}: {
  diff: string;
  height: string;
  testID?: string;
}) {
  return (
    <div
      data-preview-height={height}
      data-testid={testID}
      style={{
        background: "var(--bg0)",
        borderTop: "1px solid var(--border)",
        contain: "layout paint",
        height,
        isolation: "isolate",
        minHeight: 0,
        minWidth: 0,
        overscrollBehavior: "contain",
        overflow: "auto",
        padding: 8,
        position: "relative",
      }}
    >
      <DiffViewer compact diff={diff} />
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
