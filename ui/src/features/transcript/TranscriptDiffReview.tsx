import { useState } from "react";
import type { SyntheticEvent } from "react";

import type { AgentChatChangedFileDiffRecord, AgentChatChangedFileRecord } from "../../types/runtime";
import { CodeBlock, InlineError } from "../shared/ui";
import { DiffStatList, formatDiffStatSummary } from "./TranscriptActivityTimeline";

type Props = {
  sessionID: string;
  messageID: string;
  diffStat?: string;
  diff?: string;
  onListFiles?: (sessionID: string, messageID: string) => Promise<AgentChatChangedFileRecord[]>;
  onGetFileDiff?: (sessionID: string, messageID: string, path: string) => Promise<AgentChatChangedFileDiffRecord | null>;
  onRevertFiles?: (sessionID: string, messageID: string, paths: string[]) => Promise<boolean>;
};

export function TranscriptDiffReview({
  sessionID,
  messageID,
  diffStat,
  diff,
  onListFiles,
  onGetFileDiff,
  onRevertFiles,
}: Props) {
  const [files, setFiles] = useState<AgentChatChangedFileRecord[] | null>(null);
  const [selectedDiff, setSelectedDiff] = useState<AgentChatChangedFileDiffRecord | null>(null);
  const [loadingFiles, setLoadingFiles] = useState(false);
  const [loadingPath, setLoadingPath] = useState("");
  const [revertingPath, setRevertingPath] = useState("");
  const [confirmRevertPath, setConfirmRevertPath] = useState("");
  const [localError, setLocalError] = useState("");
  const hasReviewAPI = Boolean(sessionID && onListFiles && onGetFileDiff && onRevertFiles);

  async function loadFiles() {
    if (!hasReviewAPI || !onListFiles) return;
    setLoadingFiles(true);
    setLocalError("");
    try {
      const nextFiles = await onListFiles(sessionID, messageID);
      setFiles(nextFiles);
    } catch {
      setLocalError("Could not load changed files. The captured diff may no longer be available.");
    } finally {
      setLoadingFiles(false);
    }
  }

  async function inspectFile(file: AgentChatChangedFileRecord) {
    if (!hasReviewAPI || !onGetFileDiff) return;
    setLoadingPath(file.path);
    setLocalError("");
    try {
      const nextDiff = await onGetFileDiff(sessionID, messageID, file.path);
      if (nextDiff) {
        setSelectedDiff(nextDiff);
      } else {
        setLocalError("Could not load that file diff.");
      }
    } catch {
      setLocalError("Could not load that file diff.");
    } finally {
      setLoadingPath("");
    }
  }

  async function confirmRevert(paths: string[], label: string) {
    if (!hasReviewAPI || !onRevertFiles) return;
    setRevertingPath(label);
    setLocalError("");
    try {
      const ok = await onRevertFiles(sessionID, messageID, paths);
      if (!ok) {
        setLocalError("Revert failed. The workspace may not be a Git repository, or the file changed since capture.");
        return;
      }
      setSelectedDiff(null);
      setFiles(null);
      await loadFiles();
    } catch {
      setLocalError("Revert failed. The workspace may not be a Git repository, or the file changed since capture.");
    } finally {
      setRevertingPath("");
      setConfirmRevertPath("");
    }
  }

  const summary = diffStat ? formatDiffStatSummary(diffStat) : "";
  const visibleFiles = files ?? [];

  return (
    <details
      data-testid="agent-diff-review"
      onToggle={(event: SyntheticEvent<HTMLDetailsElement>) => {
        if (event.currentTarget.open && files === null && !loadingFiles) {
          void loadFiles();
        }
      }}
      style={{ marginTop: 8 }}
    >
      <summary style={{ cursor: "pointer", fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>
        files changed{summary ? ` · ${summary}` : ""}
      </summary>
      <div style={{ display: "grid", gap: 8, marginTop: 6 }}>
        <div style={{ color: "var(--t3)", fontSize: 11, lineHeight: 1.5 }}>
          External-agent changes are already in your workspace. Inspect the captured Git diff, keep it, or revert selected paths.
        </div>
        {!hasReviewAPI && (
          <>
            {diffStat && <DiffStatList diffStat={diffStat} />}
            {diff && <CodeBlock code={diff} lang="diff" />}
          </>
        )}
        {hasReviewAPI && (
          <>
            {loadingFiles && (
              <div style={{ color: "var(--t2)", fontFamily: "var(--font-mono)", fontSize: 11 }}>Loading changed files...</div>
            )}
            {!loadingFiles && visibleFiles.length === 0 && diffStat && (
              <DiffStatList diffStat={diffStat} />
            )}
            {visibleFiles.length > 0 && (
              <div style={{
                border: "1px solid var(--border)",
                borderRadius: "var(--radius-sm)",
                background: "var(--bg2)",
                overflow: "hidden",
              }}>
                <div style={{
                  alignItems: "center",
                  borderBottom: "1px solid var(--border)",
                  display: "flex",
                  gap: 8,
                  justifyContent: "space-between",
                  padding: "7px 9px",
                }}>
                  <span style={{ color: "var(--t2)", fontFamily: "var(--font-mono)", fontSize: 11 }}>
                    {visibleFiles.length} changed file{visibleFiles.length === 1 ? "" : "s"}
                  </span>
                  {confirmRevertPath === "__all__" ? (
                    <div style={{ display: "flex", gap: 6 }}>
                      <button
                        className="btn btn-ghost btn-sm"
                        disabled={Boolean(revertingPath)}
                        onClick={() => void confirmRevert([], "__all__")}
                        type="button"
                      >
                        {revertingPath === "__all__" ? "Reverting..." : "Confirm revert all"}
                      </button>
                      <button className="btn btn-ghost btn-sm" onClick={() => setConfirmRevertPath("")} type="button">Cancel</button>
                    </div>
                  ) : (
                    <button
                      className="btn btn-ghost btn-sm"
                      disabled={Boolean(revertingPath)}
                      onClick={() => setConfirmRevertPath("__all__")}
                      type="button"
                    >
                      Revert all
                    </button>
                  )}
                </div>
                <div style={{ display: "grid" }}>
                  {visibleFiles.map(file => (
                    <div
                      key={file.path}
                      style={{
                        alignItems: "center",
                        borderTop: "1px solid var(--border)",
                        display: "grid",
                        gap: 8,
                        gridTemplateColumns: "minmax(0, 1fr) auto",
                        padding: "7px 9px",
                      }}
                    >
                      <div style={{ minWidth: 0 }}>
                        <div style={{ color: "var(--t1)", fontFamily: "var(--font-mono)", fontSize: 11, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                          {file.path}
                        </div>
                        <div style={{ color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 10, marginTop: 2 }}>
                          {formatChangedFileMeta(file)}
                        </div>
                      </div>
                      {confirmRevertPath === file.path ? (
                        <div style={{ display: "flex", gap: 6 }}>
                          <button
                            className="btn btn-ghost btn-sm"
                            disabled={Boolean(revertingPath)}
                            onClick={() => void confirmRevert([file.path], file.path)}
                            type="button"
                          >
                            {revertingPath === file.path ? "Reverting..." : `Confirm revert ${file.path}`}
                          </button>
                          <button className="btn btn-ghost btn-sm" onClick={() => setConfirmRevertPath("")} type="button">Cancel</button>
                        </div>
                      ) : (
                        <div style={{ display: "flex", gap: 6 }}>
                          <button
                            className="btn btn-ghost btn-sm"
                            disabled={loadingPath === file.path || Boolean(revertingPath)}
                            onClick={() => void inspectFile(file)}
                            type="button"
                          >
                            {loadingPath === file.path ? "Loading..." : `Inspect ${file.path}`}
                          </button>
                          <button
                            className="btn btn-ghost btn-sm"
                            disabled={Boolean(revertingPath)}
                            onClick={() => setConfirmRevertPath(file.path)}
                            type="button"
                          >
                            Revert {file.path}
                          </button>
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              </div>
            )}
            {localError && <InlineError message={localError} />}
            {selectedDiff && (
              <div style={{ display: "grid", gap: 6 }}>
                <div style={{ color: "var(--t2)", fontFamily: "var(--font-mono)", fontSize: 11 }}>
                  diff · {selectedDiff.path}
                </div>
                <CodeBlock code={selectedDiff.diff} lang="diff" />
              </div>
            )}
          </>
        )}
      </div>
    </details>
  );
}

function formatChangedFileMeta(file: AgentChatChangedFileRecord): string {
  const parts = [file.status || "modified"];
  if (file.additions > 0) parts.push(`+${file.additions}`);
  if (file.deletions > 0) parts.push(`-${file.deletions}`);
  if (parts.length === 1) parts.push("no line delta");
  return parts.join(" · ");
}
