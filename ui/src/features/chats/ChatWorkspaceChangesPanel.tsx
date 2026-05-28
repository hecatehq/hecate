import { useEffect, useMemo, useRef, useState } from "react";
import type { CSSProperties } from "react";

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

const EMPTY_CHANGED_FILES: ChatChangedFileRecord[] = [];

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
  const [expandedDirPaths, setExpandedDirPaths] = useState<string[]>([]);
  const [query, setQuery] = useState("");
  const [contextMenu, setContextMenu] = useState<WorkspaceTreeContextMenuState | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadingPath, setLoadingPath] = useState("");
  const [copyingPath, setCopyingPath] = useState("");
  const [copiedKey, setCopiedKey] = useState("");
  const [revertingPath, setRevertingPath] = useState("");
  const [confirmRevertPath, setConfirmRevertPath] = useState("");
  const [loadFailed, setLoadFailed] = useState(false);
  const [localError, setLocalError] = useState("");
  const copiedTimerRef = useRef<number | null>(null);
  const previousFolderPathsRef = useRef<string[]>([]);
  const preSearchExpandedDirPathsRef = useRef<string[] | null>(null);

  async function refresh() {
    setLoading(true);
    setLoadFailed(false);
    setLocalError("");
    try {
      const next = await onGetWorkspaceDiff(sessionID);
      setSnapshot(next);
      setFileDiffs({});
      setExpandedDiffPaths([]);
      setLoadFailed(next === null);
      const firstFile = next?.has_changes ? next.files?.[0] : undefined;
      if (firstFile) {
        setExpandedDiffPaths([firstFile.path]);
        setLoadingPath(firstFile.path);
        try {
          const firstDiff = await onGetWorkspaceFileDiff(sessionID, firstFile.path);
          if (firstDiff) {
            setFileDiffs({ [firstFile.path]: firstDiff });
          } else {
            setExpandedDiffPaths([]);
            setLocalError("Could not load that current file diff.");
          }
        } catch {
          setExpandedDiffPaths([]);
          setLocalError("Could not load that current file diff.");
        } finally {
          setLoadingPath("");
        }
      }
    } catch {
      setSnapshot(null);
      setFileDiffs({});
      setExpandedDiffPaths([]);
      setLoadFailed(true);
    } finally {
      setLoading(false);
    }
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
    try {
      const next = await loadFileDiff(file);
      if (!next) {
        setExpandedDiffPaths((current) => current.filter((path) => path !== file.path));
        setLocalError("Could not load that current file diff.");
      }
    } catch {
      setExpandedDiffPaths((current) => current.filter((path) => path !== file.path));
      setLocalError("Could not load that current file diff.");
    } finally {
      setLoadingPath("");
    }
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
    try {
      const next = await loadFileDiff(file);
      if (next?.diff) {
        await copyText(next.diff, `file:${file.path}`);
      } else {
        setLocalError("Could not load that current file diff.");
      }
    } catch {
      setLocalError("Could not load that current file diff.");
    } finally {
      setCopyingPath("");
    }
  }

  async function confirmRevert(paths: string[], label: string) {
    setRevertingPath(label);
    setLocalError("");
    try {
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
    } catch {
      setLocalError("Could not discard those workspace changes.");
    } finally {
      setConfirmRevertPath("");
      setRevertingPath("");
    }
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
  const files = snapshot?.files ?? EMPTY_CHANGED_FILES;
  const tree = useMemo(() => buildWorkspaceFileTree(files, query), [files, query]);

  useEffect(() => {
    const folderPaths = collectWorkspaceTreeFolderPaths(tree);
    if (query.trim()) {
      setExpandedDirPaths((current) => {
        preSearchExpandedDirPathsRef.current ??= current;
        return folderPaths;
      });
      return;
    }

    setExpandedDirPaths((current) => {
      const restored = preSearchExpandedDirPathsRef.current ?? current;
      preSearchExpandedDirPathsRef.current = null;
      const previous = previousFolderPathsRef.current;
      previousFolderPathsRef.current = folderPaths;
      if (previous.length === 0) return folderPaths;

      const previousSet = new Set(previous);
      const folderSet = new Set(folderPaths);
      const next = new Set(restored.filter((path) => folderSet.has(path)));
      // Preserve the operator's manual collapse state on refresh, while still
      // auto-opening folders that appeared since the previous tree.
      for (const path of folderPaths) {
        if (!previousSet.has(path)) next.add(path);
      }
      return folderPaths.filter((path) => next.has(path));
    });
  }, [query, tree]);

  useEffect(() => {
    if (!contextMenu) return;
    const close = () => setContextMenu(null);
    window.addEventListener("click", close);
    window.addEventListener("keydown", close);
    window.addEventListener("resize", close);
    window.addEventListener("scroll", close, true);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("keydown", close);
      window.removeEventListener("resize", close);
      window.removeEventListener("scroll", close, true);
    };
  }, [contextMenu]);

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
                expandedDirPaths={expandedDirPaths}
                expandedDiffPaths={expandedDiffPaths}
                files={files}
                fileDiffs={fileDiffs}
                loadingPath={loadingPath}
                query={query}
                summary={summary}
                tree={tree}
                workspace={workspace}
                revertingPath={revertingPath}
                confirmRevertPath={confirmRevertPath}
                contextMenu={contextMenu}
                onChangeQuery={setQuery}
                onCopyFullDiff={() => void copyText(diff, "full")}
                onCopyFileDiff={(file) => void copyFileDiff(file)}
                onToggleDiff={(file) => void toggleFileDiff(file)}
                onToggleFolder={(path) =>
                  setExpandedDirPaths((current) =>
                    current.includes(path)
                      ? current.filter((candidate) => candidate !== path)
                      : [...current, path],
                  )
                }
                onOpenContextMenu={setContextMenu}
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
  contextMenu,
  diff,
  expandedDirPaths,
  expandedDiffPaths,
  files,
  fileDiffs,
  loadingPath,
  query,
  summary,
  tree,
  workspace,
  revertingPath,
  confirmRevertPath,
  onChangeQuery,
  onCopyFullDiff,
  onCopyFileDiff,
  onOpenContextMenu,
  onToggleDiff,
  onToggleFolder,
  onRequestRevert,
  onCancelRevert,
  onConfirmRevert,
}: {
  copiedKey: string;
  copyingPath: string;
  contextMenu: WorkspaceTreeContextMenuState | null;
  diff: string;
  expandedDirPaths: string[];
  expandedDiffPaths: string[];
  files: ChatChangedFileRecord[];
  fileDiffs: Record<string, ChatChangedFileDiffRecord>;
  loadingPath: string;
  query: string;
  summary: string;
  tree: WorkspaceTreeNode[];
  workspace: string;
  revertingPath: string;
  confirmRevertPath: string;
  onChangeQuery: (query: string) => void;
  onCopyFullDiff: () => void;
  onCopyFileDiff: (file: ChatChangedFileRecord) => void;
  onOpenContextMenu: (menu: WorkspaceTreeContextMenuState) => void;
  onToggleDiff: (file: ChatChangedFileRecord) => void;
  onToggleFolder: (path: string) => void;
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
        background: "var(--bg1)",
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
          <div
            style={{
              color: "var(--t2)",
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              letterSpacing: ".12em",
              textTransform: "uppercase",
            }}
          >
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
      <div
        style={{
          borderBottom: "1px solid var(--border)",
          display: "grid",
          gap: 6,
          padding: "8px",
        }}
      >
        <label
          style={{
            alignItems: "center",
            background: "var(--bg0)",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius-xs)",
            color: "var(--t3)",
            display: "grid",
            gap: 6,
            gridTemplateColumns: "auto minmax(0, 1fr)",
            padding: "7px 8px",
          }}
        >
          <Icon d={Icons.search} size={12} />
          <input
            aria-label="Search changed files"
            value={query}
            onChange={(event) => onChangeQuery(event.target.value)}
            placeholder="Search changed files"
            style={{
              background: "transparent",
              border: 0,
              color: "var(--t1)",
              font: "inherit",
              fontFamily: "var(--font-mono)",
              fontSize: 10.5,
              minWidth: 0,
              outline: "none",
              padding: 0,
            }}
          />
        </label>
        {query && tree.length === 0 && (
          <div style={{ color: "var(--t3)", fontSize: 11, padding: "4px 2px 2px" }}>
            No changed files match that search.
          </div>
        )}
      </div>
      <div style={{ display: "grid", position: "relative" }}>
        {tree.map((node) => (
          <WorkspaceTreeNodeRow
            key={node.key}
            confirmRevertPath={confirmRevertPath}
            copiedKey={copiedKey}
            copyingPath={copyingPath}
            expandedDiffPaths={expandedDiffPaths}
            expandedDirPaths={expandedDirPaths}
            fileDiffs={fileDiffs}
            loadingPath={loadingPath}
            node={node}
            revertingPath={revertingPath}
            onCancelRevert={onCancelRevert}
            onConfirmRevert={onConfirmRevert}
            onCopyFileDiff={onCopyFileDiff}
            onOpenContextMenu={onOpenContextMenu}
            onRequestRevert={onRequestRevert}
            onToggleDiff={onToggleDiff}
            onToggleFolder={onToggleFolder}
          />
        ))}
        {contextMenu && (
          <WorkspaceTreeContextMenu
            copiedKey={copiedKey}
            file={contextMenu.file}
            left={contextMenu.left}
            top={contextMenu.top}
            onCopyFileDiff={onCopyFileDiff}
            onRequestRevert={onRequestRevert}
          />
        )}
      </div>
    </div>
  );
}

function WorkspaceTreeNodeRow({
  confirmRevertPath,
  copiedKey,
  copyingPath,
  expandedDiffPaths,
  expandedDirPaths,
  fileDiffs,
  loadingPath,
  node,
  revertingPath,
  onCancelRevert,
  onConfirmRevert,
  onCopyFileDiff,
  onOpenContextMenu,
  onRequestRevert,
  onToggleDiff,
  onToggleFolder,
}: {
  confirmRevertPath: string;
  copiedKey: string;
  copyingPath: string;
  expandedDiffPaths: string[];
  expandedDirPaths: string[];
  fileDiffs: Record<string, ChatChangedFileDiffRecord>;
  loadingPath: string;
  node: WorkspaceTreeNode;
  revertingPath: string;
  onCancelRevert: () => void;
  onConfirmRevert: (paths: string[], label: string) => void;
  onCopyFileDiff: (file: ChatChangedFileRecord) => void;
  onOpenContextMenu: (menu: WorkspaceTreeContextMenuState) => void;
  onRequestRevert: (path: string) => void;
  onToggleDiff: (file: ChatChangedFileRecord) => void;
  onToggleFolder: (path: string) => void;
}) {
  if (node.kind === "folder") {
    const expanded = expandedDirPaths.includes(node.path);
    return (
      <div style={{ display: "grid", minWidth: 0 }}>
        <button
          type="button"
          aria-label={`${expanded ? "Collapse" : "Expand"} folder ${node.path}`}
          onClick={() => onToggleFolder(node.path)}
          style={{
            alignItems: "center",
            background: "transparent",
            border: 0,
            borderTop: "1px solid var(--border)",
            color: "var(--t2)",
            cursor: "pointer",
            display: "grid",
            gap: 7,
            gridTemplateColumns: "auto auto minmax(0, 1fr) auto",
            minWidth: 0,
            padding: "6px 8px",
            paddingLeft: 8 + node.depth * 12,
            textAlign: "left",
          }}
        >
          <Icon d={expanded ? Icons.chevD : Icons.chevR} size={10} />
          <Icon d={Icons.folder} size={13} />
          <span
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 10.5,
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {node.name}
          </span>
          <span style={{ color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 9.5 }}>
            {node.fileCount}
          </span>
        </button>
        {expanded &&
          node.children.map((child) => (
            <WorkspaceTreeNodeRow
              key={child.key}
              confirmRevertPath={confirmRevertPath}
              copiedKey={copiedKey}
              copyingPath={copyingPath}
              expandedDiffPaths={expandedDiffPaths}
              expandedDirPaths={expandedDirPaths}
              fileDiffs={fileDiffs}
              loadingPath={loadingPath}
              node={child}
              revertingPath={revertingPath}
              onCancelRevert={onCancelRevert}
              onConfirmRevert={onConfirmRevert}
              onCopyFileDiff={onCopyFileDiff}
              onOpenContextMenu={onOpenContextMenu}
              onRequestRevert={onRequestRevert}
              onToggleDiff={onToggleDiff}
              onToggleFolder={onToggleFolder}
            />
          ))}
      </div>
    );
  }

  const file = node.file;
  const diffSelected = expandedDiffPaths.includes(file.path);
  const selectedDiff = fileDiffs[file.path];
  const diffButtonLabel = diffSelected ? `Hide diff ${file.path}` : `Show diff ${file.path}`;

  return (
    <div style={{ borderTop: "1px solid var(--border)", display: "grid", minWidth: 0 }}>
      <div
        style={{
          alignItems: "center",
          background: diffSelected ? "var(--teal-bg)" : "transparent",
          display: "grid",
          gap: 6,
          gridTemplateColumns: "minmax(0, 1fr) auto",
          padding: "6px 8px",
          paddingLeft: 8 + node.depth * 12,
        }}
      >
        <button
          type="button"
          aria-label={diffButtonLabel}
          onClick={() => onToggleDiff(file)}
          onContextMenu={(event) => {
            event.preventDefault();
            onOpenContextMenu({
              file,
              left: Math.min(event.clientX, window.innerWidth - 190),
              top: Math.min(event.clientY, window.innerHeight - 96),
            });
          }}
          title={diffButtonLabel}
          style={{
            alignItems: "center",
            background: "transparent",
            border: 0,
            color: "inherit",
            cursor: "pointer",
            display: "grid",
            gap: 7,
            gridTemplateColumns: "auto auto minmax(0, 1fr)",
            minWidth: 0,
            padding: 0,
            textAlign: "left",
          }}
        >
          <Icon d={diffSelected ? Icons.chevD : Icons.chevR} size={10} />
          <span
            style={{
              alignItems: "center",
              border: "1px solid var(--border)",
              borderRadius: 6,
              color: fileStatusColor(file.status),
              display: "inline-flex",
              fontFamily: "var(--font-mono)",
              fontSize: 9,
              height: 18,
              justifyContent: "center",
              width: 18,
            }}
          >
            {fileStatusGlyph(file.status)}
          </span>
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
              {node.name}
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
              <Icon d={copiedKey === `file:${file.path}` ? Icons.check : Icons.copy} size={12} />
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
        <WorkspaceDiffPreview diff={selectedDiff.diff} testID="workspace-file-diff-preview" />
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
}

function WorkspaceTreeContextMenu({
  copiedKey,
  file,
  left,
  top,
  onCopyFileDiff,
  onRequestRevert,
}: {
  copiedKey: string;
  file: ChatChangedFileRecord;
  left: number;
  top: number;
  onCopyFileDiff: (file: ChatChangedFileRecord) => void;
  onRequestRevert: (path: string) => void;
}) {
  return (
    <div
      role="menu"
      style={{
        background: "var(--bg2)",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        boxShadow: "0 18px 60px rgba(0,0,0,.35)",
        display: "grid",
        left,
        minWidth: 170,
        overflow: "hidden",
        position: "fixed",
        top,
        zIndex: 50,
      }}
    >
      <button
        role="menuitem"
        type="button"
        onClick={() => onCopyFileDiff(file)}
        style={contextMenuButtonStyle}
      >
        {copiedKey === `file:${file.path}` ? "Copied file diff" : "Copy file diff"}
      </button>
      <button
        role="menuitem"
        type="button"
        onClick={() => onRequestRevert(file.path)}
        style={{ ...contextMenuButtonStyle, color: "var(--red)" }}
      >
        Discard file changes
      </button>
    </div>
  );
}

type WorkspaceTreeContextMenuState = {
  file: ChatChangedFileRecord;
  left: number;
  top: number;
};

type WorkspaceTreeNode =
  | {
      kind: "folder";
      key: string;
      name: string;
      path: string;
      depth: number;
      fileCount: number;
      children: WorkspaceTreeNode[];
    }
  | {
      kind: "file";
      key: string;
      name: string;
      path: string;
      depth: number;
      file: ChatChangedFileRecord;
    };

type WorkspaceTreeDraftNode = {
  children: Map<string, WorkspaceTreeDraftNode>;
  file?: ChatChangedFileRecord;
};

const contextMenuButtonStyle = {
  background: "transparent",
  border: 0,
  color: "var(--t1)",
  cursor: "pointer",
  font: "inherit",
  fontSize: 12,
  padding: "10px 12px",
  textAlign: "left",
} satisfies CSSProperties;

function buildWorkspaceFileTree(
  files: ChatChangedFileRecord[],
  rawQuery: string,
): WorkspaceTreeNode[] {
  const query = rawQuery.trim().toLowerCase();
  const root: WorkspaceTreeDraftNode = { children: new Map() };
  const filtered = query ? files.filter((file) => file.path.toLowerCase().includes(query)) : files;

  for (const file of filtered) {
    const parts = file.path.split("/").filter(Boolean);
    let current = root;
    for (const part of parts.slice(0, -1)) {
      let next = current.children.get(part);
      if (!next) {
        next = { children: new Map() };
        current.children.set(part, next);
      }
      current = next;
    }
    const name = parts.at(-1) ?? file.path;
    current.children.set(name, { children: new Map(), file });
  }

  return sortedWorkspaceTreeEntries(root.children).flatMap(([name, child]) =>
    materializeWorkspaceTreeNode(name, name, child, 0),
  );
}

function materializeWorkspaceTreeNode(
  name: string,
  path: string,
  node: WorkspaceTreeDraftNode,
  depth: number,
): WorkspaceTreeNode[] {
  if (node.file) {
    return [
      {
        kind: "file",
        key: `file:${node.file.path}`,
        name,
        path: node.file.path,
        depth,
        file: node.file,
      },
    ];
  }

  let folderName = name;
  let folderPath = path;
  let current = node;

  // Collapse one-child directory chains so deep paths stay scannable in the
  // narrow right panel without forcing the operator through empty folders.
  while (current.children.size === 1) {
    const [[onlyName, onlyChild]] = Array.from(current.children.entries());
    if (onlyChild.file) break;
    folderName = `${folderName}/${onlyName}`;
    folderPath = `${folderPath}/${onlyName}`;
    current = onlyChild;
  }

  const children = sortedWorkspaceTreeEntries(current.children).flatMap(([childName, child]) =>
    materializeWorkspaceTreeNode(childName, `${folderPath}/${childName}`, child, depth + 1),
  );

  return [
    {
      kind: "folder",
      key: `folder:${folderPath}`,
      name: folderName,
      path: folderPath,
      depth,
      fileCount: countWorkspaceTreeFiles(children),
      children,
    },
  ];
}

function sortedWorkspaceTreeEntries(
  children: Map<string, WorkspaceTreeDraftNode>,
): [string, WorkspaceTreeDraftNode][] {
  return Array.from(children.entries()).sort(([leftName, left], [rightName, right]) => {
    const leftIsFolder = !left.file;
    const rightIsFolder = !right.file;
    if (leftIsFolder !== rightIsFolder) return leftIsFolder ? -1 : 1;
    return leftName.localeCompare(rightName);
  });
}

function collectWorkspaceTreeFolderPaths(nodes: WorkspaceTreeNode[]): string[] {
  return nodes.flatMap((node) =>
    node.kind === "folder" ? [node.path, ...collectWorkspaceTreeFolderPaths(node.children)] : [],
  );
}

function countWorkspaceTreeFiles(nodes: WorkspaceTreeNode[]): number {
  return nodes.reduce(
    (count, node) => count + (node.kind === "file" ? 1 : countWorkspaceTreeFiles(node.children)),
    0,
  );
}

function fileStatusGlyph(status: string): string {
  switch (status.toLowerCase()) {
    case "added":
    case "new":
    case "untracked":
      return "+";
    case "deleted":
    case "removed":
      return "-";
    case "renamed":
      return "R";
    default:
      return "M";
  }
}

function fileStatusColor(status: string): string {
  switch (status.toLowerCase()) {
    case "added":
    case "new":
    case "untracked":
      return "var(--green)";
    case "deleted":
    case "removed":
      return "var(--red)";
    case "renamed":
      return "var(--amber)";
    default:
      return "var(--teal)";
  }
}

function WorkspaceDiffPreview({
  diff,
  testID = "workspace-diff-preview",
}: {
  diff: string;
  testID?: string;
}) {
  const [layoutReady, setLayoutReady] = useState(false);

  useEffect(() => {
    setLayoutReady(false);
    let firstFrame = 0;
    let secondFrame = 0;
    firstFrame = window.requestAnimationFrame(() => {
      secondFrame = window.requestAnimationFrame(() => setLayoutReady(true));
    });
    return () => {
      window.cancelAnimationFrame(firstFrame);
      window.cancelAnimationFrame(secondFrame);
    };
  }, [diff]);

  return (
    <div
      data-testid={layoutReady ? testID : undefined}
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
      {layoutReady ? (
        <DiffViewer key={`${testID}:${diff.length}`} compact embedded diff={diff} />
      ) : (
        <div
          style={{
            color: "var(--t3)",
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            padding: "6px 0",
          }}
        >
          Preparing diff...
        </div>
      )}
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
