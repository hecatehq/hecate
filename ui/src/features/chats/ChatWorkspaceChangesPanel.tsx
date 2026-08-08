import {
  type RefObject,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import type {
  ChatChangedFileDiffRecord,
  ChatChangedFileRecord,
  ChatWorkspaceDiffRecord,
  ChatWorkspaceFileRecord,
  ChatWorkspaceFilesRecord,
  ChatWorkspaceLayeredDiffRecord,
  ChatWorkspaceReviewFileRecord,
  ChatWorkspaceReviewLayerKind,
  ChatWorkspaceReviewLayerRecord,
  ChatWorkspaceReviewPreviewRecord,
} from "../../types/chat";
import { InlineError } from "../shared/Atoms";
import { DiffViewer } from "../shared/DiffViewer";
import { Icon, Icons } from "../shared/Icons";
import { formatDiffStatSummary } from "../transcript/transcriptActivityHelpers";

import type { VisibleChatMessage } from "./ChatTranscript";

const EMPTY_CHANGED_FILES: ChatChangedFileRecord[] = [];
const EMPTY_WORKSPACE_FILES: ChatWorkspaceFileRecord[] = [];
const WORKSPACE_REVIEW_LAYER_ORDER: ChatWorkspaceReviewLayerKind[] = [
  "staged",
  "working_tree",
  "untracked",
];
const DISCARD_ALL_WORKING_TREE_KEY = "workspace-review:discard-all-working-tree";
const WORKSPACE_REVIEW_INITIAL_RENDER_LIMIT = 150;
const WORKSPACE_REVIEW_RENDER_INCREMENT = 150;
const WORKSPACE_FILE_TREE_INITIAL_RENDER_LIMIT = 150;
const WORKSPACE_FILE_TREE_RENDER_INCREMENT = 150;
const TEXT_DIFF_EXTENSIONS = new Set([
  "c",
  "cc",
  "cfg",
  "conf",
  "cpp",
  "css",
  "csv",
  "go",
  "h",
  "html",
  "js",
  "json",
  "jsonc",
  "jsx",
  "lock",
  "md",
  "mdc",
  "mjs",
  "mts",
  "rs",
  "sh",
  "sql",
  "svg",
  "toml",
  "ts",
  "tsx",
  "txt",
  "yaml",
  "yml",
]);
const NON_TEXT_DIFF_EXTENSIONS = new Set([
  "avif",
  "bmp",
  "gif",
  "heic",
  "ico",
  "jpeg",
  "jpg",
  "mov",
  "mp4",
  "pdf",
  "png",
  "webp",
  "zip",
]);

export type ChatWorkspaceChange = {
  key: string;
  messageID: string;
  label: string;
  diffStat?: string;
  diff?: string;
};

type WorkspacePanelOwner = {
  sessionID: string;
  workspace: string;
  generation: number;
  controller: AbortController;
};

type WorkspaceReadFence = {
  owner: WorkspacePanelOwner;
  generation: number;
  controller: AbortController;
  detachOwnerAbort: () => void;
};

type WorkspaceFileDiffReadResult =
  | { status: "ready"; value: ChatChangedFileDiffRecord | null }
  | { status: "stale" };

type WorkspaceFileDiffReadFence = WorkspaceReadFence & {
  path: string;
  promise?: Promise<WorkspaceFileDiffReadResult>;
};

type WorkspaceRevertIntent = {
  owner: WorkspacePanelOwner;
  snapshot: ChatWorkspaceDiffRecord;
  revision: string;
  entryIDs: string[];
  paths: string[];
  key: string;
};

type WorkspaceOperationFence = {
  owner: WorkspacePanelOwner;
  generation: number;
};

type CanonicalWorkspaceReviewLayer = Omit<ChatWorkspaceReviewLayerRecord, "kind"> & {
  kind: ChatWorkspaceReviewLayerKind;
};

type WorkspaceDiscardEntry = {
  id: string;
  path: string;
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
  refreshSignal = 0,
  revertDisabled = false,
  onGetWorkspaceDiff,
  onGetWorkspaceFiles,
  onGetWorkspaceFileDiff,
  onRevertWorkspaceFiles,
  onDiscardPendingChange,
}: {
  sessionID: string;
  workspace: string;
  refreshSignal?: number;
  revertDisabled?: boolean;
  onGetWorkspaceDiff: (sessionID: string) => Promise<ChatWorkspaceDiffRecord | null>;
  onGetWorkspaceFiles: (sessionID: string) => Promise<ChatWorkspaceFilesRecord | null>;
  onGetWorkspaceFileDiff: (
    sessionID: string,
    path: string,
  ) => Promise<ChatChangedFileDiffRecord | null>;
  onRevertWorkspaceFiles: (
    sessionID: string,
    paths: string[],
    expectedRevision: string,
  ) => Promise<ChatWorkspaceDiffRecord | null>;
  onDiscardPendingChange?: (pending: boolean) => void;
}) {
  const [activeView, setActiveView] = useState<"review" | "files">("review");
  const [snapshot, setSnapshot] = useState<ChatWorkspaceDiffRecord | null>(null);
  const [workspaceFiles, setWorkspaceFiles] = useState<ChatWorkspaceFilesRecord | null>(null);
  const [fileDiffs, setFileDiffs] = useState<Record<string, ChatChangedFileDiffRecord>>({});
  const [expandedDiffPaths, setExpandedDiffPaths] = useState<string[]>([]);
  // Keep the full workspace tree collapsed until the operator expands or searches;
  // the Review tab owns the eager changed-file diff preview.
  const [expandedFileDirs, setExpandedFileDirs] = useState<string[]>([]);
  const [reviewQuery, setReviewQuery] = useState("");
  const [fileQuery, setFileQuery] = useState("");
  const [loadingReview, setLoadingReview] = useState(false);
  const [loadingFiles, setLoadingFiles] = useState(false);
  const [loadingPath, setLoadingPath] = useState("");
  const [copyingPath, setCopyingPath] = useState("");
  const [copiedKey, setCopiedKey] = useState("");
  const [revertingPath, setRevertingPath] = useState("");
  const [revertIntent, setRevertIntentState] = useState<WorkspaceRevertIntent | null>(null);
  const [reviewFailed, setReviewFailed] = useState(false);
  const [filesFailed, setFilesFailed] = useState(false);
  const [localError, setLocalError] = useState("");
  const tabIDPrefix = useId();
  const reviewTabRef = useRef<HTMLButtonElement | null>(null);
  const filesTabRef = useRef<HTMLButtonElement | null>(null);
  const ownerRef = useRef<WorkspacePanelOwner | null>(null);
  const ownerGenerationRef = useRef(0);
  const operationGenerationRef = useRef(0);
  const copiedTimerRef = useRef<number | null>(null);
  const filesLoadedRef = useRef(false);
  const snapshotOwnerRef = useRef<WorkspacePanelOwner | null>(null);
  const snapshotValueRef = useRef<ChatWorkspaceDiffRecord | null>(null);
  const workspaceFilesOwnerRef = useRef<WorkspacePanelOwner | null>(null);
  const fileDiffsRef = useRef<Record<string, ChatChangedFileDiffRecord>>({});
  const failedFileDiffPathsRef = useRef<Set<string>>(new Set());
  const reviewReadRef = useRef<WorkspaceReadFence | null>(null);
  const filesReadRef = useRef<WorkspaceReadFence | null>(null);
  const fileDiffReadsRef = useRef<Map<string, WorkspaceFileDiffReadFence>>(new Map());
  const loadingFileDiffRef = useRef<WorkspaceFileDiffReadFence | null>(null);
  const copyOperationRef = useRef<WorkspaceOperationFence | null>(null);
  const revertOperationRef = useRef<WorkspaceOperationFence | null>(null);
  const revertIntentRef = useRef<WorkspaceRevertIntent | null>(null);
  const refreshSignalRef = useRef(refreshSignal);

  if (ownerRef.current === null) {
    ownerGenerationRef.current = 1;
    ownerRef.current = {
      sessionID,
      workspace,
      generation: ownerGenerationRef.current,
      controller: new AbortController(),
    };
  }

  function isCurrentOwner(owner: WorkspacePanelOwner) {
    const current = ownerRef.current;
    return (
      current === owner &&
      current.generation === owner.generation &&
      !owner.controller.signal.aborted
    );
  }

  function currentOwnerForProps() {
    const owner = ownerRef.current;
    if (
      !owner ||
      owner.sessionID !== sessionID ||
      owner.workspace !== workspace ||
      !isCurrentOwner(owner)
    ) {
      return null;
    }
    return owner;
  }

  function createReadFence(owner: WorkspacePanelOwner): WorkspaceReadFence | null {
    if (!isCurrentOwner(owner)) return null;
    const controller = new AbortController();
    const abortWithOwner = () => controller.abort();
    owner.controller.signal.addEventListener("abort", abortWithOwner, { once: true });
    if (owner.controller.signal.aborted) controller.abort();
    operationGenerationRef.current += 1;
    return {
      owner,
      generation: operationGenerationRef.current,
      controller,
      detachOwnerAbort: () => owner.controller.signal.removeEventListener("abort", abortWithOwner),
    };
  }

  function abortReadFence(fence: WorkspaceReadFence | null) {
    if (!fence) return;
    fence.detachOwnerAbort();
    fence.controller.abort();
  }

  function awaitFencedRead<T>(fence: WorkspaceReadFence, pending: Promise<T>): Promise<T> {
    if (fence.controller.signal.aborted) {
      return Promise.reject(new Error("Workspace read was aborted."));
    }
    return new Promise<T>((resolve, reject) => {
      const abort = () => reject(new Error("Workspace read was aborted."));
      fence.controller.signal.addEventListener("abort", abort, { once: true });
      pending.then(
        (value) => {
          fence.controller.signal.removeEventListener("abort", abort);
          resolve(value);
        },
        (error: unknown) => {
          fence.controller.signal.removeEventListener("abort", abort);
          reject(error);
        },
      );
    });
  }

  function beginRead(
    slot: { current: WorkspaceReadFence | null },
    owner: WorkspacePanelOwner,
  ): WorkspaceReadFence | null {
    if (!isCurrentOwner(owner)) return null;
    abortReadFence(slot.current);
    const fence = createReadFence(owner);
    slot.current = fence;
    return fence;
  }

  function isCurrentRead(slot: { current: WorkspaceReadFence | null }, fence: WorkspaceReadFence) {
    return (
      slot.current === fence &&
      slot.current.generation === fence.generation &&
      isCurrentOwner(fence.owner) &&
      !fence.controller.signal.aborted
    );
  }

  function finishRead(slot: { current: WorkspaceReadFence | null }, fence: WorkspaceReadFence) {
    fence.detachOwnerAbort();
    if (slot.current === fence) slot.current = null;
  }

  function abortReadSlot(slot: { current: WorkspaceReadFence | null }) {
    abortReadFence(slot.current);
    slot.current = null;
  }

  function abortFileDiffReads() {
    for (const fence of fileDiffReadsRef.current.values()) abortReadFence(fence);
    fileDiffReadsRef.current.clear();
    loadingFileDiffRef.current = null;
  }

  function isCurrentFileDiffRead(fence: WorkspaceFileDiffReadFence) {
    const current = fileDiffReadsRef.current.get(fence.path);
    return (
      current === fence &&
      current.generation === fence.generation &&
      isCurrentOwner(fence.owner) &&
      !fence.controller.signal.aborted
    );
  }

  function finishFileDiffRead(fence: WorkspaceFileDiffReadFence) {
    fence.detachOwnerAbort();
    if (fileDiffReadsRef.current.get(fence.path) === fence) {
      fileDiffReadsRef.current.delete(fence.path);
    }
  }

  function beginOperation(
    slot: { current: WorkspaceOperationFence | null },
    owner: WorkspacePanelOwner,
  ): WorkspaceOperationFence | null {
    if (!isCurrentOwner(owner)) return null;
    operationGenerationRef.current += 1;
    const operation = { owner, generation: operationGenerationRef.current };
    slot.current = operation;
    return operation;
  }

  function isCurrentOperation(
    slot: { current: WorkspaceOperationFence | null },
    operation: WorkspaceOperationFence,
  ) {
    return (
      slot.current === operation &&
      slot.current.generation === operation.generation &&
      isCurrentOwner(operation.owner)
    );
  }

  function finishOperation(
    slot: { current: WorkspaceOperationFence | null },
    operation: WorkspaceOperationFence,
  ) {
    if (slot.current === operation) slot.current = null;
  }

  function clearCopiedTimer() {
    if (copiedTimerRef.current === null) return;
    window.clearTimeout(copiedTimerRef.current);
    copiedTimerRef.current = null;
  }

  function updateRevertIntent(next: WorkspaceRevertIntent | null) {
    revertIntentRef.current = next;
    setRevertIntentState(next);
  }

  function replaceFileDiffs(
    owner: WorkspacePanelOwner,
    next: Record<string, ChatChangedFileDiffRecord>,
  ) {
    if (!isCurrentOwner(owner)) return;
    fileDiffsRef.current = next;
    setFileDiffs(next);
  }

  function rememberFileDiff(
    owner: WorkspacePanelOwner,
    path: string,
    diff: ChatChangedFileDiffRecord,
  ) {
    if (!isCurrentOwner(owner)) return;
    failedFileDiffPathsRef.current.delete(path);
    setFileDiffs((current) => {
      if (!isCurrentOwner(owner)) return current;
      const next = { ...current, [path]: diff };
      fileDiffsRef.current = next;
      return next;
    });
  }

  function commitSnapshot(owner: WorkspacePanelOwner, next: ChatWorkspaceDiffRecord | null) {
    if (!isCurrentOwner(owner)) return false;
    snapshotOwnerRef.current = owner;
    snapshotValueRef.current = next;
    setSnapshot(next);
    const intent = revertIntentRef.current;
    if (intent && (intent.owner !== owner || intent.snapshot !== next)) updateRevertIntent(null);
    return true;
  }

  function commitWorkspaceFiles(owner: WorkspacePanelOwner, next: ChatWorkspaceFilesRecord | null) {
    if (!isCurrentOwner(owner)) return false;
    workspaceFilesOwnerRef.current = owner;
    setWorkspaceFiles(next);
    return true;
  }

  async function loadFileDiff(
    owner: WorkspacePanelOwner,
    file: ChatChangedFileRecord,
  ): Promise<WorkspaceFileDiffReadResult> {
    if (!isCurrentOwner(owner)) return { status: "stale" };
    const cached = fileDiffsRef.current[file.path];
    if (cached) return { status: "ready", value: cached };
    const existing = fileDiffReadsRef.current.get(file.path);
    if (existing && isCurrentFileDiffRead(existing) && existing.promise) {
      return existing.promise;
    }

    if (existing) abortReadFence(existing);
    const baseFence = createReadFence(owner);
    if (!baseFence) return { status: "stale" };
    const fence: WorkspaceFileDiffReadFence = { ...baseFence, path: file.path };
    fileDiffReadsRef.current.set(file.path, fence);
    loadingFileDiffRef.current = fence;
    if (isCurrentFileDiffRead(fence)) setLoadingPath(file.path);

    fence.promise = (async () => {
      try {
        const next = await awaitFencedRead(
          fence,
          onGetWorkspaceFileDiff(owner.sessionID, file.path),
        );
        if (!isCurrentFileDiffRead(fence)) return { status: "stale" };
        if (next) rememberFileDiff(owner, file.path, next);
        return { status: "ready", value: next };
      } catch (error: unknown) {
        if (!isCurrentFileDiffRead(fence)) return { status: "stale" };
        throw error;
      } finally {
        const canWrite = isCurrentFileDiffRead(fence);
        if (loadingFileDiffRef.current === fence) {
          loadingFileDiffRef.current = null;
          if (canWrite) setLoadingPath("");
        }
        finishFileDiffRead(fence);
      }
    })();
    return fence.promise;
  }

  async function ensureFileDiff(owner: WorkspacePanelOwner, file: ChatChangedFileRecord) {
    if (!isCurrentOwner(owner) || snapshotOwnerRef.current !== owner) return;
    if (fileDiffsRef.current[file.path]) return;
    setLocalError("");
    try {
      const result = await loadFileDiff(owner, file);
      if (result.status === "stale" || !isCurrentOwner(owner)) return;
      if (!result.value) {
        failedFileDiffPathsRef.current.add(file.path);
        setLocalError("Could not load that file diff.");
      }
    } catch {
      if (!isCurrentOwner(owner)) return;
      failedFileDiffPathsRef.current.add(file.path);
      setLocalError("Could not load that file diff.");
    }
  }

  async function refreshReview(owner: WorkspacePanelOwner) {
    if (revertOperationRef.current && isCurrentOwner(revertOperationRef.current.owner)) return;
    const read = beginRead(reviewReadRef, owner);
    if (!read) return;
    abortFileDiffReads();
    if (!isCurrentRead(reviewReadRef, read)) return;
    setLoadingReview(true);
    setReviewFailed(false);
    setLocalError("");
    setLoadingPath("");
    updateRevertIntent(null);
    try {
      const next = await awaitFencedRead(read, onGetWorkspaceDiff(owner.sessionID));
      if (!isCurrentRead(reviewReadRef, read)) return;
      commitSnapshot(owner, next);
      failedFileDiffPathsRef.current = new Set();
      replaceFileDiffs(owner, {});
      setReviewFailed(next === null);
      if (next && isLayeredWorkspaceReview(next)) {
        const firstPreview = orderedWorkspaceReviewLayers(next)
          .flatMap((layer) => layer.files)
          .find(
            (file) =>
              (file.preview.kind === "text_diff" || file.preview.kind === "text") &&
              Boolean(file.preview.content),
          );
        setExpandedDiffPaths(firstPreview ? [firstPreview.id] : []);
        return;
      }
      let nestedReadBecameStale = false;
      const firstSelection = await findInitialDiffFile(
        next?.files ?? EMPTY_CHANGED_FILES,
        next?.diff ?? "",
        async (file) => {
          const result = await loadFileDiff(owner, file);
          if (result.status === "stale") {
            nestedReadBecameStale = true;
            return "";
          }
          return result.value?.diff ?? "";
        },
      );
      if (nestedReadBecameStale || !isCurrentRead(reviewReadRef, read)) return;
      setExpandedDiffPaths(firstSelection.file ? [firstSelection.file.path] : []);
      if (firstSelection.loadFailed) setLocalError("Could not load that file diff.");
    } catch {
      if (!isCurrentRead(reviewReadRef, read)) return;
      commitSnapshot(owner, null);
      failedFileDiffPathsRef.current = new Set();
      replaceFileDiffs(owner, {});
      setExpandedDiffPaths([]);
      setReviewFailed(true);
    } finally {
      const canWrite = isCurrentRead(reviewReadRef, read);
      finishRead(reviewReadRef, read);
      if (canWrite) setLoadingReview(false);
    }
  }

  async function refreshFiles(owner: WorkspacePanelOwner) {
    if (revertOperationRef.current && isCurrentOwner(revertOperationRef.current.owner)) return;
    const read = beginRead(filesReadRef, owner);
    if (!read) return;
    setLoadingFiles(true);
    setFilesFailed(false);
    setLocalError("");
    try {
      const next = await awaitFencedRead(read, onGetWorkspaceFiles(owner.sessionID));
      if (!isCurrentRead(filesReadRef, read)) return;
      commitWorkspaceFiles(owner, next);
      setFilesFailed(next === null);
      filesLoadedRef.current = Boolean(next);
    } catch {
      if (!isCurrentRead(filesReadRef, read)) return;
      commitWorkspaceFiles(owner, null);
      setFilesFailed(true);
    } finally {
      const canWrite = isCurrentRead(filesReadRef, read);
      finishRead(filesReadRef, read);
      if (canWrite) setLoadingFiles(false);
    }
  }

  async function writeClipboard(operation: WorkspaceOperationFence, text: string, key: string) {
    if (!navigator.clipboard?.writeText) {
      if (isCurrentOperation(copyOperationRef, operation)) {
        setLocalError("Clipboard access is not available in this environment.");
      }
      return;
    }
    try {
      await navigator.clipboard.writeText(text);
      if (!isCurrentOperation(copyOperationRef, operation)) return;
      setCopiedKey(key);
      clearCopiedTimer();
      const timer = window.setTimeout(() => {
        if (copiedTimerRef.current !== timer || !isCurrentOwner(operation.owner)) return;
        setCopiedKey("");
        copiedTimerRef.current = null;
      }, 1500);
      copiedTimerRef.current = timer;
    } catch {
      if (isCurrentOperation(copyOperationRef, operation)) {
        setLocalError("Could not copy that diff.");
      }
    }
  }

  async function copyText(text: string, key: string) {
    const owner = currentOwnerForProps();
    if (!owner || snapshotOwnerRef.current !== owner) return;
    const operation = beginOperation(copyOperationRef, owner);
    if (!operation) return;
    setCopyingPath("");
    setLocalError("");
    try {
      await writeClipboard(operation, text, key);
    } finally {
      const canWrite = isCurrentOperation(copyOperationRef, operation);
      finishOperation(copyOperationRef, operation);
      if (canWrite) setCopyingPath("");
    }
  }

  async function copyFileDiff(file: ChatChangedFileRecord) {
    const owner = currentOwnerForProps();
    const snapshotAtStart = snapshotValueRef.current;
    if (!owner || snapshotOwnerRef.current !== owner || !snapshotAtStart) return;
    const operation = beginOperation(copyOperationRef, owner);
    if (!operation) return;
    setCopyingPath(file.path);
    setLocalError("");
    try {
      const result = await loadFileDiff(owner, file);
      if (
        result.status === "stale" ||
        !isCurrentOperation(copyOperationRef, operation) ||
        snapshotValueRef.current !== snapshotAtStart
      ) {
        return;
      }
      const patch =
        result.value?.diff ||
        extractFilePatchFromWorkspaceDiff(snapshotAtStart.diff ?? "", file.path);
      if (patch) {
        await writeClipboard(operation, patch, `file:${file.path}`);
      } else {
        setLocalError("Could not load that file diff.");
      }
    } catch {
      if (isCurrentOperation(copyOperationRef, operation)) {
        setLocalError("Could not load that file diff.");
      }
    } finally {
      const canWrite = isCurrentOperation(copyOperationRef, operation);
      finishOperation(copyOperationRef, operation);
      if (canWrite) setCopyingPath("");
    }
  }

  async function copyReviewEntry(file: ChatWorkspaceReviewFileRecord) {
    const owner = currentOwnerForProps();
    const snapshotAtStart = snapshotValueRef.current;
    const content = file.preview.content ?? "";
    if (
      !owner ||
      snapshotOwnerRef.current !== owner ||
      !snapshotAtStart ||
      !isLayeredWorkspaceReview(snapshotAtStart) ||
      !content
    ) {
      return;
    }
    const operation = beginOperation(copyOperationRef, owner);
    if (!operation) return;
    setCopyingPath(file.id);
    setLocalError("");
    try {
      await writeClipboard(operation, content, `review-entry:${file.id}`);
    } finally {
      const canWrite = isCurrentOperation(copyOperationRef, operation);
      finishOperation(copyOperationRef, operation);
      if (canWrite) setCopyingPath("");
    }
  }

  function requestRevert(paths: string[], entryIDs: string[], key: string) {
    const owner = currentOwnerForProps();
    const currentSnapshot = snapshotValueRef.current;
    const revision = workspaceDiscardRevision(currentSnapshot);
    const allowedEntries = currentSnapshot
      ? discardableWorkspaceReviewEntries(currentSnapshot)
      : [];
    if (
      revertDisabled ||
      !owner ||
      snapshotOwnerRef.current !== owner ||
      !currentSnapshot ||
      !revision ||
      paths.length !== entryIDs.length ||
      paths.some(
        (path, index) =>
          !allowedEntries.some((file) => file.path === path && file.id === entryIDs[index]),
      )
    ) {
      return;
    }
    updateRevertIntent({
      owner,
      snapshot: currentSnapshot,
      revision,
      entryIDs: [...entryIDs],
      paths: [...paths],
      key,
    });
  }

  function cancelRevert() {
    const intent = revertIntentRef.current;
    if (!intent || !isCurrentOwner(intent.owner) || revertOperationRef.current) return;
    updateRevertIntent(null);
  }

  async function confirmRevert() {
    const activeRevert = revertOperationRef.current;
    if (activeRevert && isCurrentOperation(revertOperationRef, activeRevert)) return;
    const intent = revertIntentRef.current;
    if (
      revertDisabled ||
      !intent ||
      !isCurrentOwner(intent.owner) ||
      snapshotOwnerRef.current !== intent.owner ||
      snapshotValueRef.current !== intent.snapshot ||
      workspaceDiscardRevision(intent.snapshot) !== intent.revision
    ) {
      if (intent && isCurrentOwner(intent.owner)) updateRevertIntent(null);
      return;
    }
    const operation = beginOperation(revertOperationRef, intent.owner);
    if (!operation) return;
    reviewTabRef.current?.focus();
    const refreshFilesAfterRevert = filesLoadedRef.current;
    let shouldRefreshFiles = false;
    abortReadSlot(reviewReadRef);
    abortReadSlot(filesReadRef);
    abortFileDiffReads();
    setLoadingReview(false);
    setLoadingFiles(false);
    setLoadingPath("");
    setRevertingPath(intent.key);
    setLocalError("");
    onDiscardPendingChange?.(true);
    try {
      const next = await onRevertWorkspaceFiles(
        intent.owner.sessionID,
        [...intent.paths],
        intent.revision,
      );
      if (!isCurrentOperation(revertOperationRef, operation)) return;
      if (next) {
        commitSnapshot(intent.owner, next);
        setFileDiffs((current) => {
          if (!isCurrentOperation(revertOperationRef, operation)) return current;
          if (intent.paths.length === 0) {
            fileDiffsRef.current = {};
            return {};
          }
          const nextDiffs = { ...current };
          for (const path of intent.paths) delete nextDiffs[path];
          fileDiffsRef.current = nextDiffs;
          return nextDiffs;
        });
        setExpandedDiffPaths((current) => {
          if (!isCurrentOperation(revertOperationRef, operation)) return current;
          return intent.paths.length === 0
            ? []
            : current.filter((entryID) => !intent.entryIDs.includes(entryID));
        });
        if (intent.paths.length === 0) {
          failedFileDiffPathsRef.current = new Set();
        } else {
          for (const path of intent.paths) failedFileDiffPathsRef.current.delete(path);
        }
        shouldRefreshFiles = refreshFilesAfterRevert;
      } else {
        // A failed destructive request can mean the reviewed revision is
        // stale, staged state appeared, or another writer owns the workspace.
        // The callback deliberately hides transport details, so invalidate all
        // mutation authority on every null result and require a fresh review.
        commitSnapshot(intent.owner, null);
        failedFileDiffPathsRef.current = new Set();
        replaceFileDiffs(intent.owner, {});
        setExpandedDiffPaths([]);
        setReviewFailed(true);
        setLocalError(
          "Hecate could not confirm the discard result. Changes may have been applied. Refresh and inspect Git before continuing.",
        );
      }
    } catch {
      if (isCurrentOperation(revertOperationRef, operation)) {
        commitSnapshot(intent.owner, null);
        failedFileDiffPathsRef.current = new Set();
        replaceFileDiffs(intent.owner, {});
        setExpandedDiffPaths([]);
        setReviewFailed(true);
        setLocalError(
          "Hecate could not confirm the discard result. Changes may have been applied. Refresh and inspect Git before continuing.",
        );
      }
    } finally {
      onDiscardPendingChange?.(false);
      const canWrite = isCurrentOperation(revertOperationRef, operation);
      finishOperation(revertOperationRef, operation);
      if (canWrite) {
        updateRevertIntent(null);
        setRevertingPath("");
        if (shouldRefreshFiles) void refreshFiles(intent.owner);
      }
    }
  }

  function toggleFileDiff(file: ChatChangedFileRecord) {
    const owner = currentOwnerForProps();
    if (!owner || snapshotOwnerRef.current !== owner) return;
    const isExpanding = !expandedDiffPaths.includes(file.path);
    setExpandedDiffPaths((current) =>
      current.includes(file.path)
        ? current.filter((path) => path !== file.path)
        : [...current, file.path],
    );
    if (!isExpanding) return;
    const hasWorkspacePatch = Boolean(extractFilePatchFromWorkspaceDiff(diff, file.path).trim());
    if (!hasWorkspacePatch && !failedFileDiffPathsRef.current.has(file.path)) {
      void ensureFileDiff(owner, file);
    }
  }

  function toggleReviewEntry(file: ChatWorkspaceReviewFileRecord) {
    const owner = currentOwnerForProps();
    const currentSnapshot = snapshotValueRef.current;
    if (
      !owner ||
      snapshotOwnerRef.current !== owner ||
      !currentSnapshot ||
      !isLayeredWorkspaceReview(currentSnapshot)
    ) {
      return;
    }
    setExpandedDiffPaths((current) =>
      current.includes(file.id)
        ? current.filter((entryID) => entryID !== file.id)
        : [...current, file.id],
    );
  }

  useLayoutEffect(() => {
    let owner = ownerRef.current!;
    if (
      owner.sessionID !== sessionID ||
      owner.workspace !== workspace ||
      owner.controller.signal.aborted
    ) {
      owner.controller.abort();
      abortReadSlot(reviewReadRef);
      abortReadSlot(filesReadRef);
      abortFileDiffReads();
      ownerGenerationRef.current += 1;
      owner = {
        sessionID,
        workspace,
        generation: ownerGenerationRef.current,
        controller: new AbortController(),
      };
      ownerRef.current = owner;
      snapshotOwnerRef.current = null;
      snapshotValueRef.current = null;
      workspaceFilesOwnerRef.current = null;
      filesLoadedRef.current = false;
      fileDiffsRef.current = {};
      failedFileDiffPathsRef.current = new Set();
      loadingFileDiffRef.current = null;
      copyOperationRef.current = null;
      revertOperationRef.current = null;
      revertIntentRef.current = null;
      clearCopiedTimer();
      setSnapshot(null);
      setWorkspaceFiles(null);
      setFileDiffs({});
      setExpandedDiffPaths([]);
      setExpandedFileDirs([]);
      setReviewQuery("");
      setFileQuery("");
      setLoadingReview(false);
      setLoadingFiles(false);
      setLoadingPath("");
      setCopyingPath("");
      setCopiedKey("");
      setRevertingPath("");
      setRevertIntentState(null);
      setReviewFailed(false);
      setFilesFailed(false);
      setLocalError("");
    }
    refreshSignalRef.current = refreshSignal;
    return () => {
      owner.controller.abort();
      clearCopiedTimer();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionID, workspace]);

  useEffect(() => {
    const owner = currentOwnerForProps();
    if (owner) void refreshReview(owner);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionID, workspace]);

  useEffect(() => {
    const owner = currentOwnerForProps();
    if (
      owner &&
      activeView === "files" &&
      !filesLoadedRef.current &&
      !(filesReadRef.current && isCurrentRead(filesReadRef, filesReadRef.current))
    ) {
      void refreshFiles(owner);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeView, revertingPath, sessionID, workspace]);

  useEffect(() => {
    if (refreshSignalRef.current === refreshSignal) return;
    refreshSignalRef.current = refreshSignal;
    const owner = currentOwnerForProps();
    if (!owner) return;
    void refreshReview(owner);
    if (filesLoadedRef.current) void refreshFiles(owner);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshSignal]);

  const renderedOwner = currentOwnerForProps();
  const visibleSnapshot =
    renderedOwner && snapshotOwnerRef.current === renderedOwner ? snapshot : null;
  const visibleWorkspaceFiles =
    renderedOwner && workspaceFilesOwnerRef.current === renderedOwner ? workspaceFiles : null;
  const visibleFileDiffs = renderedOwner ? fileDiffs : {};
  const visibleRevertIntent =
    renderedOwner &&
    revertIntent?.owner === renderedOwner &&
    revertIntent.snapshot === visibleSnapshot
      ? revertIntent
      : null;
  const confirmRevertKey = visibleRevertIntent?.key ?? "";
  const layeredSnapshot =
    visibleSnapshot && isLayeredWorkspaceReview(visibleSnapshot) ? visibleSnapshot : null;
  const files = visibleSnapshot?.files ?? EMPTY_CHANGED_FILES;
  const diffStat = visibleSnapshot?.diff_stat?.trim() ?? "";
  const diff = visibleSnapshot?.diff?.trim() ?? "";
  const hasChanges = Boolean(visibleSnapshot?.has_changes || files.length > 0 || diffStat || diff);
  const workspaceRevertDisabled = revertDisabled || !workspaceDiscardRevision(visibleSnapshot);
  const reviewSummary = summarizeChangedFiles(files, diffStat);
  const filteredChangedFiles = useMemo(
    () => prioritizeDiffCandidates(filterChangedFiles(files, reviewQuery), diff),
    [files, reviewQuery, diff],
  );
  const fileTree = useMemo(
    () => buildWorkspaceFileTree(visibleWorkspaceFiles?.files ?? EMPTY_WORKSPACE_FILES, fileQuery),
    [visibleWorkspaceFiles, fileQuery],
  );

  useEffect(() => {
    if (activeView !== "files" || !fileQuery.trim()) return;
    setExpandedFileDirs(collectFileTreeFolderPaths(fileTree));
  }, [activeView, fileQuery, fileTree]);

  const reviewTabID = `${tabIDPrefix}-review-tab`;
  const reviewPanelID = `${tabIDPrefix}-review-panel`;
  const filesTabID = `${tabIDPrefix}-files-tab`;
  const filesPanelID = `${tabIDPrefix}-files-panel`;

  function moveWorkspacePanelTab(next: "review" | "files") {
    setActiveView(next);
    (next === "review" ? reviewTabRef : filesTabRef).current?.focus();
  }

  return (
    <div
      style={{
        background: "var(--bg1)",
        display: "flex",
        flex: "1 1 0",
        flexDirection: "column",
        height: "100%",
        minHeight: 0,
        minWidth: 0,
        overflow: "hidden",
      }}
    >
      <div
        style={{
          background: "var(--bg0)",
          borderBottom: "1px solid var(--border)",
          display: "flex",
          flexDirection: "column",
          gap: 7,
          padding: "9px 10px 8px",
          flex: "0 0 auto",
          minWidth: 0,
        }}
      >
        <div
          style={{
            alignItems: "center",
            display: "grid",
            gap: 8,
            gridTemplateColumns: "minmax(0, 1fr) auto",
            minWidth: 0,
          }}
        >
          <div
            aria-label="Workspace panel view"
            role="tablist"
            style={{
              alignItems: "center",
              background: "var(--bg1)",
              border: "1px solid var(--border)",
              borderRadius: 11,
              boxSizing: "border-box",
              display: "grid",
              gap: 2,
              gridTemplateColumns: "minmax(0, 1fr) minmax(0, 1fr)",
              maxWidth: 238,
              minWidth: 0,
              overflow: "hidden",
              padding: 2,
              whiteSpace: "nowrap",
            }}
          >
            <WorkspacePanelTab
              active={activeView === "review"}
              buttonRef={reviewTabRef}
              controlsID={reviewPanelID}
              disabled={Boolean(revertingPath)}
              icon={Icons.tasks}
              id={reviewTabID}
              label="Review"
              onClick={() => setActiveView("review")}
              onMove={moveWorkspacePanelTab}
              view="review"
            />
            <WorkspacePanelTab
              active={activeView === "files"}
              buttonRef={filesTabRef}
              controlsID={filesPanelID}
              disabled={Boolean(revertingPath)}
              icon={Icons.folder}
              id={filesTabID}
              label="Files"
              onClick={() => setActiveView("files")}
              onMove={moveWorkspacePanelTab}
              view="files"
            />
          </div>
          <div
            aria-label="Workspace review actions"
            style={{
              alignItems: "center",
              display: "flex",
              gap: 5,
              justifyContent: "flex-end",
              minWidth: 0,
            }}
          >
            <button
              aria-label="Refresh"
              className="btn btn-ghost btn-sm"
              disabled={
                !renderedOwner ||
                Boolean(revertingPath) ||
                (activeView === "review" ? loadingReview : loadingFiles)
              }
              onClick={() => {
                const owner = currentOwnerForProps();
                if (!owner) return;
                void (activeView === "review" ? refreshReview(owner) : refreshFiles(owner));
              }}
              title={activeView === "review" ? "Refresh workspace diff" : "Refresh workspace files"}
              type="button"
            >
              <Icon d={Icons.refresh} size={12} />
            </button>
          </div>
        </div>
        <WorkspacePathLabel workspace={workspace} />
        {renderedOwner && revertingPath && (
          <div style={{ color: "var(--amber)", fontSize: 10.5 }}>
            Discarding workspace changes… Keep this review open until Hecate confirms the result.
          </div>
        )}
      </div>

      <div
        style={{
          boxSizing: "border-box",
          display: "flex",
          flex: "1 1 0",
          flexDirection: "column",
          gap: 10,
          height: 0,
          minHeight: 0,
          minWidth: 0,
          overflow: "hidden",
          padding: "10px",
        }}
      >
        {activeView === "review" ? (
          <div
            aria-labelledby={reviewTabID}
            id={reviewPanelID}
            role="tabpanel"
            style={{ display: "flex", flex: "1 1 0", minHeight: 0, minWidth: 0 }}
          >
            {layeredSnapshot ? (
              <LayeredWorkspaceReviewView
                confirmRevertKey={confirmRevertKey}
                copiedKey={renderedOwner ? copiedKey : ""}
                copyingEntryID={renderedOwner ? copyingPath : ""}
                expandedEntryIDs={expandedDiffPaths}
                loading={Boolean(renderedOwner && loadingReview)}
                query={reviewQuery}
                revertingKey={renderedOwner ? revertingPath : ""}
                revertDisabled={workspaceRevertDisabled}
                snapshot={layeredSnapshot}
                onCancelRevert={cancelRevert}
                onChangeQuery={setReviewQuery}
                onConfirmRevert={() => void confirmRevert()}
                onCopyEntry={(file) => void copyReviewEntry(file)}
                onRequestRevert={(file) => requestRevert([file.path], [file.id], file.id)}
                onRequestRevertAll={() => requestRevert([], [], DISCARD_ALL_WORKING_TREE_KEY)}
                onToggleEntry={toggleReviewEntry}
              />
            ) : (
              <WorkspaceReviewView
                confirmRevertPath={confirmRevertKey}
                copiedKey={renderedOwner ? copiedKey : ""}
                copyingPath={renderedOwner ? copyingPath : ""}
                diff={diff}
                expandedDiffPaths={expandedDiffPaths}
                fileDiffs={visibleFileDiffs}
                files={filteredChangedFiles}
                hasChanges={hasChanges}
                loading={Boolean(renderedOwner && loadingReview)}
                loadingPath={renderedOwner ? loadingPath : ""}
                query={reviewQuery}
                revertingPath={renderedOwner ? revertingPath : ""}
                revertDisabled={workspaceRevertDisabled}
                reviewFailed={Boolean(renderedOwner && reviewFailed)}
                summary={reviewSummary}
                onCancelRevert={cancelRevert}
                onChangeQuery={setReviewQuery}
                onConfirmRevert={() => void confirmRevert()}
                onCopyAll={() => void copyText(diff, "full")}
                onCopyFileDiff={(file) => void copyFileDiff(file)}
                onRequestRevert={(path) => requestRevert([path], [path], path)}
                onRequestRevertAll={() => requestRevert([], [], DISCARD_ALL_WORKING_TREE_KEY)}
                onToggleDiff={toggleFileDiff}
              />
            )}
          </div>
        ) : (
          <div
            aria-labelledby={filesTabID}
            id={filesPanelID}
            role="tabpanel"
            style={{ display: "flex", flex: "1 1 0", minHeight: 0, minWidth: 0 }}
          >
            <WorkspaceFilesView
              expandedDirPaths={expandedFileDirs}
              files={visibleWorkspaceFiles}
              filesFailed={Boolean(renderedOwner && filesFailed)}
              loading={Boolean(renderedOwner && loadingFiles)}
              query={fileQuery}
              tree={fileTree}
              onChangeQuery={setFileQuery}
              onToggleFolder={(path) =>
                setExpandedFileDirs((current) =>
                  current.includes(path)
                    ? current.filter((candidate) => candidate !== path)
                    : [...current, path],
                )
              }
            />
          </div>
        )}
        {renderedOwner && localError && <InlineError message={localError} />}
      </div>
    </div>
  );
}

function WorkspacePanelTab({
  active,
  buttonRef,
  controlsID,
  disabled,
  icon,
  id,
  label,
  onClick,
  onMove,
  view,
}: {
  active: boolean;
  buttonRef: RefObject<HTMLButtonElement | null>;
  controlsID: string;
  disabled: boolean;
  icon: string | string[];
  id: string;
  label: string;
  onClick: () => void;
  onMove: (view: "review" | "files") => void;
  view: "review" | "files";
}) {
  return (
    <button
      aria-controls={active ? controlsID : undefined}
      aria-selected={active}
      className="workspace-panel-tab"
      disabled={disabled}
      id={id}
      onClick={onClick}
      onKeyDown={(event) => {
        if (event.key === "ArrowLeft" || event.key === "Home") {
          event.preventDefault();
          onMove(event.key === "Home" ? "review" : view === "review" ? "files" : "review");
        } else if (event.key === "ArrowRight" || event.key === "End") {
          event.preventDefault();
          onMove(event.key === "End" ? "files" : view === "files" ? "review" : "files");
        }
      }}
      ref={buttonRef}
      role="tab"
      style={{
        alignItems: "center",
        backgroundColor: active ? "var(--bg2)" : "transparent",
        border: "1px solid transparent",
        borderRadius: 8,
        color: active ? "var(--t0)" : "var(--t2)",
        cursor: disabled ? "wait" : "pointer",
        display: "flex",
        gap: 6,
        justifyContent: "center",
        fontSize: 11.5,
        fontWeight: active ? 700 : 600,
        lineHeight: 1,
        minHeight: 27,
        minWidth: 0,
        padding: "5px 8px",
        width: "100%",
      }}
      tabIndex={active ? 0 : -1}
      title={disabled ? "Wait for workspace discard to finish" : undefined}
      type="button"
    >
      <Icon d={icon} size={12} strokeWidth={1.7} />
      <span
        style={{
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {label}
      </span>
    </button>
  );
}

function LayeredWorkspaceReviewView({
  confirmRevertKey,
  copiedKey,
  copyingEntryID,
  expandedEntryIDs,
  loading,
  query,
  revertingKey,
  revertDisabled,
  snapshot,
  onCancelRevert,
  onChangeQuery,
  onConfirmRevert,
  onCopyEntry,
  onRequestRevert,
  onRequestRevertAll,
  onToggleEntry,
}: {
  confirmRevertKey: string;
  copiedKey: string;
  copyingEntryID: string;
  expandedEntryIDs: string[];
  loading: boolean;
  query: string;
  revertingKey: string;
  revertDisabled: boolean;
  snapshot: ChatWorkspaceLayeredDiffRecord;
  onCancelRevert: () => void;
  onChangeQuery: (query: string) => void;
  onConfirmRevert: () => void;
  onCopyEntry: (file: ChatWorkspaceReviewFileRecord) => void;
  onRequestRevert: (file: ChatWorkspaceReviewFileRecord) => void;
  onRequestRevertAll: () => void;
  onToggleEntry: (file: ChatWorkspaceReviewFileRecord) => void;
}) {
  const discardAllButtonRef = useRef<HTMLButtonElement | null>(null);
  const layers = orderedWorkspaceReviewLayers(snapshot);
  const filteredLayers = filterWorkspaceReviewLayers(layers, query);
  const [renderWindow, setRenderWindow] = useState({
    snapshot,
    query,
    limit: WORKSPACE_REVIEW_INITIAL_RENDER_LIMIT,
  });
  const renderLimit =
    renderWindow.snapshot === snapshot && renderWindow.query === query
      ? renderWindow.limit
      : WORKSPACE_REVIEW_INITIAL_RENDER_LIMIT;
  useEffect(() => {
    setRenderWindow((current) =>
      current.snapshot === snapshot && current.query === query
        ? current
        : { snapshot, query, limit: WORKSPACE_REVIEW_INITIAL_RENDER_LIMIT },
    );
  }, [query, snapshot]);
  const boundedLayers = boundWorkspaceReviewLayers(filteredLayers, renderLimit);
  const filteredEntryCount = filteredLayers.reduce((count, layer) => count + layer.files.length, 0);
  const renderedEntryCount = boundedLayers.reduce(
    (count, layer) => count + layer.visibleFiles.length,
    0,
  );
  const remainingEntryCount = Math.max(0, filteredEntryCount - renderedEntryCount);
  const workingTree = layers.find((layer) => layer.kind === "working_tree");
  const totalEntries = layers.reduce(
    (count, layer) => count + layer.files.length + (layer.omitted_count ?? 0),
    0,
  );
  const hasChanges =
    snapshot.has_changes || totalEntries > 0 || Boolean(snapshot.review_issues?.length);
  const discardAvailable = Boolean(workspaceDiscardRevision(snapshot));
  const discardReason = workspaceDiscardReason(snapshot, revertDisabled && !revertingKey);
  const showDiscardReason =
    Boolean(discardReason) &&
    (Boolean(workingTree?.files.length) ||
      workspaceDiscardReasonRequiresStandaloneNotice(snapshot.discard.reason));

  if (!hasChanges && !loading) {
    return (
      <div style={{ color: "var(--t3)", fontSize: 11, lineHeight: 1.5 }}>
        The current workspace is clean.
      </div>
    );
  }

  return (
    <section
      aria-label="Workspace review"
      style={{
        boxSizing: "border-box",
        display: "flex",
        flex: "1 1 0",
        flexDirection: "column",
        height: 0,
        minHeight: 0,
        minWidth: 0,
        overflow: "hidden",
      }}
    >
      <div
        style={{
          background: "transparent",
          border: "1px solid var(--border)",
          borderRadius: 10,
          boxSizing: "border-box",
          display: "flex",
          flex: "1 1 0",
          flexDirection: "column",
          minHeight: 0,
          minWidth: 0,
          overflow: "hidden",
        }}
      >
        <div
          style={{
            alignItems: "center",
            background: "var(--bg0)",
            borderBottom: "1px solid var(--border)",
            display: "grid",
            gap: 8,
            gridTemplateColumns: "minmax(0, 1fr) auto",
            minWidth: 0,
            padding: "8px 10px 7px",
          }}
        >
          <div style={{ minWidth: 0 }}>
            <div style={{ color: "var(--t0)", fontSize: 12, fontWeight: 750 }}>
              Workspace changes
            </div>
            <div
              style={{
                color: "var(--t3)",
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                lineHeight: 1.35,
                marginTop: 2,
              }}
            >
              {loading
                ? "Checking staged, working-tree, and untracked changes..."
                : `${totalEntries} review entr${totalEntries === 1 ? "y" : "ies"}${
                    snapshot.review_complete ? "" : " · incomplete"
                  }`}
            </div>
          </div>
          {discardAvailable && Boolean(workingTree?.files.length) && (
            <div style={{ alignItems: "center", display: "flex", gap: 5 }}>
              <button
                aria-label="Discard all working-tree changes"
                aria-describedby={discardReason ? "workspace-discard-reason" : undefined}
                className="btn btn-ghost btn-sm"
                disabled={revertDisabled || Boolean(revertingKey)}
                onClick={onRequestRevertAll}
                ref={discardAllButtonRef}
                title={discardReason || "Discard all working-tree changes"}
                type="button"
              >
                <Icon d={Icons.revert} size={12} />
                Discard working tree
              </button>
              {confirmRevertKey === DISCARD_ALL_WORKING_TREE_KEY && (
                <ConfirmInline
                  busy={revertingKey === DISCARD_ALL_WORKING_TREE_KEY}
                  disabled={revertDisabled}
                  cancelAriaLabel="Cancel discard all working-tree changes"
                  confirmAriaLabel="Confirm discard all working-tree changes"
                  confirmLabel="Discard all"
                  onCancel={onCancelRevert}
                  onConfirm={onConfirmRevert}
                  returnFocusRef={discardAllButtonRef}
                />
              )}
            </div>
          )}
        </div>
        {showDiscardReason && (
          <div
            id="workspace-discard-reason"
            style={{
              background: "var(--bg0)",
              borderBottom: "1px solid var(--border)",
              color: "var(--t3)",
              fontSize: 10,
              lineHeight: 1.45,
              padding: "7px 10px",
            }}
          >
            {discardReason}
          </div>
        )}
        {!snapshot.review_complete && (
          <WorkspaceReviewIncompleteNotice
            issues={snapshot.review_issues ?? []}
            omittedCount={snapshot.review_issues_omitted_count ?? 0}
          />
        )}
        <SearchBox
          disabled={Boolean(revertingKey)}
          label="Search workspace changes"
          placeholder="Search workspace changes"
          value={query}
          onChange={onChangeQuery}
        />
        <div
          aria-label="Workspace change layers"
          style={{
            flex: "1 1 0",
            minHeight: 0,
            minWidth: 0,
            overflowY: "auto",
            overscrollBehavior: "contain",
          }}
        >
          {boundedLayers.map(({ layer, visibleFiles }) => (
            <WorkspaceReviewLayer
              key={layer.kind}
              confirmRevertKey={confirmRevertKey}
              copiedKey={copiedKey}
              copyingEntryID={copyingEntryID}
              discardAvailable={discardAvailable}
              expandedEntryIDs={expandedEntryIDs}
              layer={layer}
              visibleFiles={visibleFiles}
              query={query}
              revertingKey={revertingKey}
              revertDisabled={revertDisabled}
              discardReason={discardReason}
              onCancelRevert={onCancelRevert}
              onConfirmRevert={onConfirmRevert}
              onCopyEntry={onCopyEntry}
              onRequestRevert={onRequestRevert}
              onToggleEntry={onToggleEntry}
            />
          ))}
          {remainingEntryCount > 0 && (
            <div
              style={{
                alignItems: "center",
                background: "var(--bg0)",
                display: "flex",
                justifyContent: "center",
                padding: 10,
              }}
            >
              <button
                className="btn btn-ghost btn-sm"
                onClick={() =>
                  setRenderWindow({
                    snapshot,
                    query,
                    limit: renderLimit + WORKSPACE_REVIEW_RENDER_INCREMENT,
                  })
                }
                type="button"
              >
                Show more changes · {remainingEntryCount} remaining
              </button>
            </div>
          )}
        </div>
      </div>
    </section>
  );
}

function WorkspaceReviewLayer({
  confirmRevertKey,
  copiedKey,
  copyingEntryID,
  discardAvailable,
  discardReason,
  expandedEntryIDs,
  layer,
  visibleFiles,
  query,
  revertingKey,
  revertDisabled,
  onCancelRevert,
  onConfirmRevert,
  onCopyEntry,
  onRequestRevert,
  onToggleEntry,
}: {
  confirmRevertKey: string;
  copiedKey: string;
  copyingEntryID: string;
  discardAvailable: boolean;
  discardReason: string;
  expandedEntryIDs: string[];
  layer: CanonicalWorkspaceReviewLayer;
  visibleFiles: ChatWorkspaceReviewFileRecord[];
  query: string;
  revertingKey: string;
  revertDisabled: boolean;
  onCancelRevert: () => void;
  onConfirmRevert: () => void;
  onCopyEntry: (file: ChatWorkspaceReviewFileRecord) => void;
  onRequestRevert: (file: ChatWorkspaceReviewFileRecord) => void;
  onToggleEntry: (file: ChatWorkspaceReviewFileRecord) => void;
}) {
  const layerLabel = workspaceReviewLayerLabel(layer.kind);
  const readOnly = layer.kind !== "working_tree";

  return (
    <section
      aria-label={`${layerLabel} changes`}
      style={{ borderBottom: "1px solid var(--border)", minWidth: 0 }}
    >
      <div
        style={{
          alignItems: "start",
          background: "var(--bg0)",
          display: "grid",
          gap: 8,
          gridTemplateColumns: "minmax(0, 1fr) auto",
          padding: "9px 10px 8px",
        }}
      >
        <div style={{ minWidth: 0 }}>
          <h3
            style={{
              color: "var(--t0)",
              fontSize: 11.5,
              fontWeight: 750,
              lineHeight: 1.3,
              margin: 0,
            }}
          >
            {layerLabel}
          </h3>
          <div style={{ color: "var(--t3)", fontSize: 10, lineHeight: 1.45, marginTop: 2 }}>
            {workspaceReviewLayerDescription(layer.kind)}
          </div>
        </div>
        <div style={{ alignItems: "center", display: "flex", gap: 6 }}>
          {!layer.complete && (
            <span style={workspaceReviewBadgeStyle("var(--amber)")}>Incomplete</span>
          )}
          {readOnly && <span style={workspaceReviewBadgeStyle("var(--t2)")}>Read only</span>}
          <span style={workspaceReviewBadgeStyle("var(--t3)")}>
            {layer.files.length + (layer.omitted_count ?? 0)}
          </span>
        </div>
      </div>
      {layer.omitted_count ? (
        <div
          role="status"
          style={{ color: "var(--amber)", fontSize: 10, lineHeight: 1.45, padding: "7px 10px" }}
        >
          {`${layer.omitted_count} additional entr${
            layer.omitted_count === 1 ? "y was" : "ies were"
          } omitted by the review limit.`}
        </div>
      ) : null}
      {layer.files.length === 0 ? (
        <div style={{ color: "var(--t3)", fontSize: 10.5, lineHeight: 1.5, padding: "8px 10px" }}>
          {query ? `No ${layerLabel.toLowerCase()} changes match that search.` : "No changes."}
        </div>
      ) : (
        <ul
          aria-label={`${layerLabel} change entries`}
          style={{ listStyle: "none", margin: 0, padding: 0 }}
        >
          {visibleFiles.map((file) => (
            <li data-testid="workspace-review-entry" key={file.id}>
              <WorkspaceReviewEntry
                confirmRevertKey={confirmRevertKey}
                copied={copiedKey === `review-entry:${file.id}`}
                copying={copyingEntryID === file.id}
                discardReason={discardReason}
                expanded={expandedEntryIDs.includes(file.id)}
                file={file}
                layerKind={layer.kind}
                reverting={Boolean(revertingKey)}
                discardAvailable={discardAvailable}
                revertDisabled={revertDisabled}
                onCancelRevert={onCancelRevert}
                onConfirmRevert={onConfirmRevert}
                onCopy={() => onCopyEntry(file)}
                onRequestRevert={() => onRequestRevert(file)}
                onToggle={() => onToggleEntry(file)}
              />
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function WorkspaceReviewEntry({
  confirmRevertKey,
  copied,
  copying,
  discardReason,
  expanded,
  file,
  layerKind,
  reverting,
  discardAvailable,
  revertDisabled,
  onCancelRevert,
  onConfirmRevert,
  onCopy,
  onRequestRevert,
  onToggle,
}: {
  confirmRevertKey: string;
  copied: boolean;
  copying: boolean;
  discardReason: string;
  expanded: boolean;
  file: ChatWorkspaceReviewFileRecord;
  layerKind: ChatWorkspaceReviewLayerKind;
  reverting: boolean;
  discardAvailable: boolean;
  revertDisabled: boolean;
  onCancelRevert: () => void;
  onConfirmRevert: () => void;
  onCopy: () => void;
  onRequestRevert: () => void;
  onToggle: () => void;
}) {
  const discardButtonRef = useRef<HTMLButtonElement | null>(null);
  const displayPath = escapeWorkspacePathForDisplay(file.path);
  const previewRegionID = `${file.id}-preview`;
  const hasCopyablePreview = Boolean(file.preview.content);
  const copyKind = file.preview.kind === "text" ? "contents" : "diff";
  const canDiscard = layerKind === "working_tree" && discardAvailable;
  const toggleLabel = `${expanded ? "Collapse" : "Expand"} ${workspaceReviewPreviewLabel(
    file.preview,
  )} ${displayPath}; ${fileStatusLabel(file.status || "modified")}; ${file.additions} addition${
    file.additions === 1 ? "" : "s"
  }, ${file.deletions} deletion${file.deletions === 1 ? "" : "s"}`;

  return (
    <div
      style={{
        backgroundColor: expanded ? "var(--teal-bg)" : "transparent",
        borderTop: "1px solid var(--border)",
        minWidth: 0,
      }}
    >
      <div
        style={{
          alignItems: "center",
          display: "grid",
          gap: 6,
          gridTemplateColumns: "minmax(0, 1fr) auto",
          minWidth: 0,
        }}
      >
        <button
          aria-controls={expanded ? previewRegionID : undefined}
          aria-expanded={expanded}
          aria-label={toggleLabel}
          className="workspace-review-entry-toggle"
          onClick={onToggle}
          style={{
            alignItems: "center",
            background: "transparent",
            border: 0,
            color: "inherit",
            cursor: "pointer",
            display: "grid",
            gap: 9,
            gridTemplateColumns: "auto auto minmax(0, 1fr) auto",
            minWidth: 0,
            padding: "7px 10px",
            textAlign: "left",
          }}
          type="button"
        >
          <Icon d={expanded ? Icons.chevD : Icons.chevR} size={10} />
          <span
            title={fileStatusLabel(file.status || "modified")}
            style={{
              alignItems: "center",
              border: "1px solid var(--border)",
              borderRadius: 6,
              color: fileStatusColor(file.status || "modified"),
              display: "inline-flex",
              fontFamily: "var(--font-mono)",
              fontSize: 9,
              height: 18,
              justifyContent: "center",
              width: 18,
            }}
          >
            {fileStatusGlyph(file.status || "modified")}
          </span>
          <span style={{ minWidth: 0 }}>
            <ChangedFilePathLabel path={file.path} />
            <span
              style={{
                color: "var(--t3)",
                display: "block",
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                marginTop: 2,
              }}
            >
              {formatWorkspaceReviewEntryMeta(file)}
            </span>
          </span>
          <WorkspaceReviewLineDelta file={file} />
        </button>
        <div style={{ alignItems: "center", display: "flex", gap: 4, paddingRight: 7 }}>
          {hasCopyablePreview && (
            <button
              aria-label={`${copied ? "Copied" : "Copy"} ${copyKind} ${displayPath}`}
              className="btn btn-ghost btn-sm"
              disabled={copying || Boolean(reverting)}
              onClick={onCopy}
              title={`${copied ? "Copied" : "Copy"} ${copyKind} ${displayPath}`}
              type="button"
            >
              <Icon d={copied ? Icons.check : Icons.copy} size={12} />
            </button>
          )}
          {canDiscard && (
            <button
              aria-label={`Discard ${displayPath}`}
              className="btn btn-ghost btn-sm"
              disabled={revertDisabled || Boolean(reverting)}
              onClick={onRequestRevert}
              ref={discardButtonRef}
              title={discardReason || `Discard ${displayPath}`}
              type="button"
            >
              <Icon d={Icons.revert} size={12} />
            </button>
          )}
          {canDiscard && confirmRevertKey === file.id && (
            <ConfirmInline
              busy={reverting}
              disabled={revertDisabled}
              cancelAriaLabel={`Cancel discard ${displayPath}`}
              confirmAriaLabel={`Confirm discard ${displayPath}`}
              confirmLabel="Discard"
              onCancel={onCancelRevert}
              onConfirm={onConfirmRevert}
              returnFocusRef={discardButtonRef}
            />
          )}
        </div>
      </div>
      {expanded && (
        <div
          aria-label={`${workspaceReviewPreviewLabel(file.preview)} ${displayPath}`}
          id={previewRegionID}
          role="region"
          style={{ background: "var(--bg0)", borderTop: "1px solid var(--border)", minWidth: 0 }}
        >
          <WorkspaceReviewPreview preview={file.preview} />
        </div>
      )}
    </div>
  );
}

function WorkspaceReviewLineDelta({ file }: { file: ChatWorkspaceReviewFileRecord }) {
  if (file.additions <= 0 && file.deletions <= 0) return <span aria-hidden="true" />;
  return (
    <span
      aria-label={`${file.additions} additions, ${file.deletions} deletions`}
      style={{
        fontFamily: "var(--font-mono)",
        fontSize: 11,
        minWidth: 34,
        textAlign: "right",
        whiteSpace: "nowrap",
      }}
    >
      {file.additions > 0 && <span style={{ color: "var(--green)" }}>+{file.additions}</span>}
      {file.deletions > 0 && (
        <span style={{ color: "var(--red)", marginLeft: 5 }}>-{file.deletions}</span>
      )}
    </span>
  );
}

function WorkspaceReviewPreview({ preview }: { preview: ChatWorkspaceReviewPreviewRecord }) {
  if (preview.kind === "text_diff") {
    return (
      <WorkspaceDiffPreview
        diff={escapeWorkspacePreviewForDisplay(preview.content ?? "")}
        hasTextPatch
        testID="workspace-review-diff-preview"
      />
    );
  }
  if (preview.kind === "text") {
    return (
      <pre
        data-testid="workspace-review-text-preview"
        style={{
          color: "var(--t1)",
          fontFamily: "var(--font-mono)",
          fontSize: 10.5,
          lineHeight: 1.55,
          margin: 0,
          maxHeight: 320,
          overflow: "auto",
          padding: "10px 12px",
          tabSize: 2,
          whiteSpace: "pre-wrap",
          wordBreak: "break-word",
        }}
      >
        {preview.content ? escapeWorkspacePreviewForDisplay(preview.content) : "(empty file)"}
      </pre>
    );
  }
  return (
    <div
      data-testid={`workspace-review-preview-${preview.kind}`}
      style={{ color: "var(--t3)", fontSize: 11, lineHeight: 1.5, padding: "10px 12px" }}
    >
      {workspaceReviewPreviewMessage(preview)}
    </div>
  );
}

function WorkspaceReviewIncompleteNotice({
  issues,
  omittedCount,
}: {
  issues: ChatWorkspaceLayeredDiffRecord["review_issues"];
  omittedCount: number;
}) {
  return (
    <div
      role="status"
      style={{
        background: "color-mix(in srgb, var(--amber) 8%, transparent)",
        borderBottom: "1px solid var(--border)",
        color: "var(--t1)",
        fontSize: 10.5,
        lineHeight: 1.5,
        padding: "8px 10px",
      }}
    >
      <strong style={{ color: "var(--amber)", fontWeight: 700 }}>Review incomplete.</strong> Some
      workspace state could not be represented safely. Inspect Git directly before deciding what to
      keep.
      {issues && issues.length > 0 && (
        <ul style={{ margin: "5px 0 0", paddingLeft: 18 }}>
          {issues.slice(0, 5).map((issue) => (
            <li key={`${issue.kind}:${issue.path}`}>
              {workspaceReviewIssueLabel(issue.kind)}: {escapeWorkspacePathForDisplay(issue.path)}
            </li>
          ))}
          {issues.length > 5 && <li>{issues.length - 5} more review issues are not shown.</li>}
          {omittedCount > 0 && (
            <li>
              {omittedCount} additional review issue{omittedCount === 1 ? " was" : "s were"} omitted
              by the response limit.
            </li>
          )}
        </ul>
      )}
      {(!issues || issues.length === 0) && omittedCount > 0 && (
        <div style={{ marginTop: 5 }}>
          {omittedCount} review issue{omittedCount === 1 ? " was" : "s were"} omitted by the
          response limit.
        </div>
      )}
    </div>
  );
}

function isLayeredWorkspaceReview(
  snapshot: ChatWorkspaceDiffRecord,
): snapshot is ChatWorkspaceLayeredDiffRecord {
  return (
    Array.isArray(snapshot.layers) &&
    snapshot.discard !== null &&
    typeof snapshot.discard === "object"
  );
}

function orderedWorkspaceReviewLayers(
  snapshot: ChatWorkspaceLayeredDiffRecord,
): CanonicalWorkspaceReviewLayer[] {
  return WORKSPACE_REVIEW_LAYER_ORDER.map((kind) => {
    const layer = snapshot.layers.find((candidate) => candidate.kind === kind);
    return layer
      ? { ...layer, kind, files: Array.isArray(layer.files) ? layer.files : [] }
      : { kind, complete: false, files: [] };
  });
}

function filterWorkspaceReviewLayers(
  layers: CanonicalWorkspaceReviewLayer[],
  rawQuery: string,
): CanonicalWorkspaceReviewLayer[] {
  const query = rawQuery.trim().toLowerCase();
  if (!query) return layers;
  return layers.map((layer) => ({
    ...layer,
    files: layer.files.filter((file) => file.path.toLowerCase().includes(query)),
  }));
}

function boundWorkspaceReviewLayers(
  layers: CanonicalWorkspaceReviewLayer[],
  rawLimit: number,
): { layer: CanonicalWorkspaceReviewLayer; visibleFiles: ChatWorkspaceReviewFileRecord[] }[] {
  let remaining = Math.max(0, Math.floor(rawLimit));
  let nonemptyLayers = layers.filter((layer) => layer.files.length > 0).length;
  return layers.map((layer) => {
    if (layer.files.length === 0 || remaining === 0) {
      if (layer.files.length > 0) nonemptyLayers -= 1;
      return { layer, visibleFiles: [] };
    }
    const fairShare = Math.ceil(remaining / Math.max(1, nonemptyLayers));
    const visibleFiles = layer.files.slice(0, fairShare);
    remaining -= visibleFiles.length;
    nonemptyLayers -= 1;
    return { layer, visibleFiles };
  });
}

function workspaceDiscardRevision(snapshot: ChatWorkspaceDiffRecord | null): string {
  if (!snapshot) return "";
  if (isLayeredWorkspaceReview(snapshot)) {
    return snapshot.discard.available ? (snapshot.discard.revision?.trim() ?? "") : "";
  }
  return snapshot.revision?.trim() ?? "";
}

function discardableWorkspaceReviewEntries(
  snapshot: ChatWorkspaceDiffRecord,
): WorkspaceDiscardEntry[] {
  if (isLayeredWorkspaceReview(snapshot)) {
    return (
      orderedWorkspaceReviewLayers(snapshot).find((layer) => layer.kind === "working_tree")
        ?.files ?? []
    ).map((file) => ({ id: file.id, path: file.path }));
  }
  return snapshot.files.map((file) => ({ id: file.path, path: file.path }));
}

function workspaceReviewLayerLabel(kind: ChatWorkspaceReviewLayerKind): string {
  switch (kind) {
    case "staged":
      return "Staged";
    case "working_tree":
      return "Working tree";
    case "untracked":
      return "Untracked";
  }
}

function workspaceReviewLayerDescription(kind: ChatWorkspaceReviewLayerKind): string {
  switch (kind) {
    case "staged":
      return "Changes already prepared in Git. Review only; Hecate will not discard them here.";
    case "working_tree":
      return "Unstaged tracked changes. These are the only entries this panel can discard.";
    case "untracked":
      return "New workspace files. Review only; Hecate will not delete them here.";
  }
}

function workspaceReviewBadgeStyle(color: string) {
  return {
    border: "1px solid var(--border)",
    borderRadius: 999,
    color,
    fontFamily: "var(--font-mono)",
    fontSize: 9,
    lineHeight: 1,
    padding: "4px 6px",
    whiteSpace: "nowrap",
  } as const;
}

function workspaceDiscardReason(
  snapshot: ChatWorkspaceLayeredDiffRecord,
  disabled: boolean,
): string {
  if (snapshot.discard.available) {
    if (!snapshot.discard.revision?.trim()) {
      return "Discard is unavailable until the workspace is reviewed again.";
    }
    return disabled ? "Discard is unavailable while agent work is active." : "";
  }
  switch (snapshot.discard.reason) {
    case "staged_changes":
      return "Discard is unavailable while staged changes are present. Staged changes remain read only.";
    case "working_tree_too_large":
      return "Discard is unavailable because the working-tree review exceeded its safety limit.";
    case "working_tree_preview_incomplete":
      return "Discard is unavailable because one or more working-tree previews are incomplete. Refresh or inspect Git directly.";
    case "review_incomplete":
      return "Discard is unavailable because the workspace review is incomplete.";
    case "workspace_changed":
      return "The workspace changed during review. Refresh before discarding.";
    case "refresh_required":
      return "The discard completed, but Hecate could not refresh the review. Refresh before continuing.";
    case "cleanup_failed":
      return "Git cleanup did not finish after discard. Refresh and check Git before continuing.";
    case "mutation_outcome_unknown":
      return "The discard command did not finish cleanly. Refresh and verify every selected file before continuing.";
    case "no_working_tree_changes":
      return "There are no unstaged tracked changes to discard.";
    default:
      return "Discard is unavailable for this review.";
  }
}

function workspaceDiscardReasonRequiresStandaloneNotice(reason?: string): boolean {
  return (
    reason === "refresh_required" ||
    reason === "cleanup_failed" ||
    reason === "mutation_outcome_unknown"
  );
}

function workspaceReviewPreviewLabel(preview: ChatWorkspaceReviewPreviewRecord): string {
  return preview.kind === "text_diff" ? "diff" : "preview";
}

function workspaceReviewPreviewMessage(preview: ChatWorkspaceReviewPreviewRecord): string {
  switch (preview.kind) {
    case "binary":
      return "Binary content is not displayed.";
    case "too_large":
      if (preview.reason === "total_limit") {
        return "Preview omitted because the review reached its total content limit.";
      }
      if (preview.reason === "layer_limit") {
        return "Preview omitted because this Git layer exceeded its review limit.";
      }
      return "Preview omitted because this entry exceeds the per-file content limit.";
    case "symlink":
      return "Symlink targets are not opened from workspace review.";
    case "special":
      return "This non-regular file is not opened from workspace review.";
    case "nested_repository":
      return "Nested repository contents are not opened from workspace review.";
    case "conflict":
      return "Git reports an unresolved conflict. Inspect and resolve it directly in the workspace.";
    case "unavailable":
      switch (preview.reason) {
        case "filesystem_unsupported":
        case "platform_unsupported":
          return "A safe inline preview is not available on this platform.";
        case "changed_during_read":
          return "The file changed while it was being read. Refresh to try again.";
        case "read_timeout":
          return "The file preview timed out. Refresh to try again.";
        case "workspace_unavailable":
          return "The workspace could not be opened for a safe preview.";
        case "read_failed":
          return "The file could not be read safely. Refresh to try again.";
        default:
          return "A safe inline preview is unavailable for this entry.";
      }
    default:
      return "This preview type is not supported by this version of Hecate.";
  }
}

function workspaceReviewIssueLabel(kind: string): string {
  switch (kind) {
    case "intent_to_add":
      return "Intent-to-add index entry";
    case "assume_unchanged":
      return "Assume-unchanged index entry";
    case "skip_worktree":
      return "Skip-worktree index entry";
    case "skip_worktree_assume_unchanged":
      return "Skip-worktree and assume-unchanged index entry";
    case "unmerged":
      return "Unmerged path";
    default:
      return "Unrepresented Git state";
  }
}

function formatWorkspaceReviewEntryMeta(file: ChatWorkspaceReviewFileRecord): string {
  const parts = [file.status || "modified", workspaceReviewPreviewMeta(file.preview)];
  if (typeof file.size_bytes === "number" && file.size_bytes >= 0) {
    parts.push(formatWorkspaceReviewBytes(file.size_bytes));
  }
  return parts.filter(Boolean).join(" · ");
}

function workspaceReviewPreviewMeta(preview: ChatWorkspaceReviewPreviewRecord): string {
  switch (preview.kind) {
    case "text_diff":
      return "text diff";
    case "text":
      return "text preview";
    case "nested_repository":
      return "nested repository";
    case "too_large":
      return "preview omitted";
    default:
      return escapeWorkspacePathForDisplay(preview.kind.replaceAll("_", " "));
  }
}

function formatWorkspaceReviewBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

function escapeWorkspacePathForDisplay(path: string): string {
  return escapeUnsafeWorkspaceText(path, false);
}

function escapeWorkspacePreviewForDisplay(content: string): string {
  return escapeUnsafeWorkspaceText(content, true);
}

function escapeUnsafeWorkspaceText(value: string, preserveTextWhitespace: boolean): string {
  return Array.from(value, (character) => {
    const codePoint = character.codePointAt(0) ?? 0;
    const preservedWhitespace =
      preserveTextWhitespace && (codePoint === 0x09 || codePoint === 0x0a || codePoint === 0x0d);
    const unsafeControl =
      (codePoint >= 0 && codePoint <= 0x1f) || (codePoint >= 0x7f && codePoint <= 0x9f);
    const unsafeDirection =
      codePoint === 0x061c ||
      codePoint === 0x200e ||
      codePoint === 0x200f ||
      (codePoint >= 0x202a && codePoint <= 0x202e) ||
      (codePoint >= 0x2066 && codePoint <= 0x2069);
    return !preservedWhitespace && (unsafeControl || unsafeDirection)
      ? escapeUnsafeWorkspaceCharacter(character)
      : character;
  }).join("");
}

function escapeUnsafeWorkspaceCharacter(character: string): string {
  const codePoint = character.codePointAt(0) ?? 0;
  return `\\u${codePoint.toString(16).toUpperCase().padStart(4, "0")}`;
}

function WorkspaceReviewView({
  confirmRevertPath,
  copiedKey,
  copyingPath,
  diff,
  expandedDiffPaths,
  fileDiffs,
  files,
  hasChanges,
  loading,
  loadingPath,
  query,
  revertingPath,
  revertDisabled,
  reviewFailed,
  summary,
  onCancelRevert,
  onChangeQuery,
  onConfirmRevert,
  onCopyAll,
  onCopyFileDiff,
  onRequestRevert,
  onRequestRevertAll,
  onToggleDiff,
}: {
  confirmRevertPath: string;
  copiedKey: string;
  copyingPath: string;
  diff: string;
  expandedDiffPaths: string[];
  fileDiffs: Record<string, ChatChangedFileDiffRecord>;
  files: ChatChangedFileRecord[];
  hasChanges: boolean;
  loading: boolean;
  loadingPath: string;
  query: string;
  revertingPath: string;
  revertDisabled: boolean;
  reviewFailed: boolean;
  summary: string;
  onCancelRevert: () => void;
  onChangeQuery: (query: string) => void;
  onConfirmRevert: () => void;
  onCopyAll: () => void;
  onCopyFileDiff: (file: ChatChangedFileRecord) => void;
  onRequestRevert: (path: string) => void;
  onRequestRevertAll: () => void;
  onToggleDiff: (file: ChatChangedFileRecord) => void;
}) {
  const discardAllButtonRef = useRef<HTMLButtonElement | null>(null);
  if (reviewFailed) {
    return (
      <div style={{ color: "var(--red)", fontSize: 11, lineHeight: 1.5 }}>
        Could not load the current workspace diff.
      </div>
    );
  }

  if (!hasChanges && !loading) {
    return (
      <div style={{ color: "var(--t3)", fontSize: 11, lineHeight: 1.5 }}>
        The current workspace is clean.
      </div>
    );
  }

  return (
    <section
      aria-label="Workspace review"
      style={{
        boxSizing: "border-box",
        display: "flex",
        flex: "1 1 0",
        flexDirection: "column",
        height: 0,
        minHeight: 0,
        minWidth: 0,
        overflow: "hidden auto",
        overscrollBehavior: "contain",
        gap: 0,
      }}
    >
      <div
        style={{
          background: "transparent",
          border: "1px solid var(--border)",
          borderRadius: 10,
          boxSizing: "border-box",
          display: "flex",
          flex: "1 1 0",
          flexDirection: "column",
          minHeight: 0,
          minWidth: 0,
          overflow: "hidden",
        }}
      >
        <div
          style={{
            alignItems: "center",
            background: "var(--bg0)",
            borderBottom: "1px solid var(--border)",
            display: "grid",
            gap: 8,
            gridTemplateColumns: "minmax(0, 1fr) auto",
            minWidth: 0,
            padding: "8px 10px 7px",
          }}
        >
          <div style={{ minWidth: 0 }}>
            <div style={{ color: "var(--t0)", fontSize: 12, fontWeight: 750 }}>Changed files</div>
            <div
              style={{
                color: "var(--t3)",
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                lineHeight: 1.35,
                marginTop: 2,
                overflow: "hidden",
                display: "-webkit-box",
                WebkitBoxOrient: "vertical",
                WebkitLineClamp: 2,
              }}
            >
              {loading ? "Checking current Git diff..." : summary}
            </div>
          </div>
          {hasChanges && (
            <div
              style={{
                alignItems: "center",
                display: "flex",
                flex: "0 0 auto",
                gap: 4,
                justifyContent: "flex-end",
                minWidth: 0,
              }}
            >
              <button
                aria-label="Copy complete workspace patch"
                className="btn btn-ghost btn-sm"
                disabled={!diff || Boolean(revertingPath)}
                onClick={onCopyAll}
                title="Copy complete workspace patch"
                type="button"
              >
                <Icon d={copiedKey === "full" ? Icons.check : Icons.copy} size={12} />
              </button>
              <button
                aria-label="Discard all workspace changes"
                className="btn btn-ghost btn-sm"
                disabled={revertDisabled || Boolean(revertingPath)}
                onClick={onRequestRevertAll}
                ref={discardAllButtonRef}
                title="Discard all workspace changes"
                type="button"
              >
                <Icon d={Icons.revert} size={12} />
              </button>
              {confirmRevertPath === DISCARD_ALL_WORKING_TREE_KEY && (
                <ConfirmInline
                  busy={revertingPath === DISCARD_ALL_WORKING_TREE_KEY}
                  disabled={revertDisabled}
                  cancelAriaLabel="Cancel discard all workspace changes"
                  confirmAriaLabel="Confirm discard all workspace changes"
                  confirmLabel="Discard all"
                  onCancel={onCancelRevert}
                  onConfirm={onConfirmRevert}
                  returnFocusRef={discardAllButtonRef}
                />
              )}
            </div>
          )}
        </div>
        <SearchBox
          disabled={Boolean(revertingPath)}
          label="Search changed files"
          placeholder="Search changed files"
          value={query}
          onChange={onChangeQuery}
        />
        {files.length === 0 ? (
          <div style={{ color: "var(--t3)", fontSize: 11, lineHeight: 1.5, padding: 12 }}>
            {loading
              ? "Loading changed files..."
              : query
                ? "No changed files match that search."
                : "No changed files found."}
          </div>
        ) : (
          <div
            aria-label="Changed files"
            style={{
              alignContent: "start",
              display: "grid",
              flex: "1 1 0",
              height: 0,
              minHeight: 0,
              minWidth: 0,
              overflowY: "auto",
              overscrollBehavior: "contain",
            }}
          >
            {files.map((file) => {
              const filePatch = textPatchForChangedFile(file, diff, fileDiffs);
              const expanded = expandedDiffPaths.includes(file.path);
              return (
                <ChangedFileReviewItem
                  key={file.path}
                  confirmRevertPath={confirmRevertPath}
                  copiedKey={copiedKey}
                  copyingPath={copyingPath}
                  diff={filePatch}
                  expanded={expanded}
                  file={file}
                  hasTextPatch={hasTextDiffHunks(filePatch.trim())}
                  loading={loadingPath === file.path}
                  revertingPath={revertingPath}
                  revertDisabled={revertDisabled}
                  summary={summarizeDiffAvailability(file, filePatch)}
                  onCancelRevert={onCancelRevert}
                  onConfirmRevert={onConfirmRevert}
                  onCopyFileDiff={onCopyFileDiff}
                  onRequestRevert={onRequestRevert}
                  onToggleDiff={onToggleDiff}
                />
              );
            })}
          </div>
        )}
      </div>
    </section>
  );
}

function ChangedFileReviewItem({
  confirmRevertPath,
  copiedKey,
  copyingPath,
  diff,
  expanded,
  file,
  hasTextPatch,
  loading,
  revertingPath,
  revertDisabled,
  summary,
  onCancelRevert,
  onConfirmRevert,
  onCopyFileDiff,
  onRequestRevert,
  onToggleDiff,
}: {
  confirmRevertPath: string;
  copiedKey: string;
  copyingPath: string;
  diff: string;
  expanded: boolean;
  file: ChatChangedFileRecord;
  hasTextPatch: boolean;
  loading: boolean;
  revertingPath: string;
  revertDisabled: boolean;
  summary: string;
  onCancelRevert: () => void;
  onConfirmRevert: () => void;
  onCopyFileDiff: (file: ChatChangedFileRecord) => void;
  onRequestRevert: (path: string) => void;
  onToggleDiff: (file: ChatChangedFileRecord) => void;
}) {
  const displayPath = escapeWorkspacePathForDisplay(file.path);
  const previewRegionID = `workspace-legacy-diff-${encodeURIComponent(file.path)}`;
  const discardButtonRef = useRef<HTMLButtonElement | null>(null);
  return (
    <div
      style={{
        backgroundColor: expanded ? "var(--teal-bg)" : "transparent",
        borderTop: "1px solid var(--border)",
        minWidth: 0,
      }}
    >
      <div
        style={{
          alignItems: "center",
          display: "grid",
          gap: 8,
          gridTemplateColumns: "minmax(0, 1fr) auto",
          minWidth: 0,
        }}
      >
        <button
          aria-current={expanded ? "true" : undefined}
          aria-controls={expanded ? previewRegionID : undefined}
          aria-expanded={expanded}
          aria-label={`${expanded ? "Collapse" : "Expand"} diff ${displayPath}`}
          className="workspace-review-entry-toggle"
          onClick={() => onToggleDiff(file)}
          style={{
            alignItems: "center",
            background: "transparent",
            border: 0,
            color: "inherit",
            cursor: "pointer",
            display: "grid",
            gap: 9,
            gridTemplateColumns: "auto auto minmax(0, 1fr) auto",
            minWidth: 0,
            padding: "7px 10px",
            textAlign: "left",
          }}
          type="button"
        >
          <Icon d={expanded ? Icons.chevD : Icons.chevR} size={10} />
          <span
            title={fileStatusLabel(file.status || "modified")}
            style={{
              alignItems: "center",
              border: "1px solid var(--border)",
              borderRadius: 6,
              color: fileStatusColor(file.status || "modified"),
              display: "inline-flex",
              fontFamily: "var(--font-mono)",
              fontSize: 9,
              height: 18,
              justifyContent: "center",
              width: 18,
            }}
          >
            {fileStatusGlyph(file.status || "modified")}
          </span>
          <span style={{ minWidth: 0 }}>
            <ChangedFilePathLabel path={file.path} />
            <span
              style={{
                color: "var(--t3)",
                display: "block",
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                marginTop: 2,
              }}
            >
              {formatChangedFileMeta(file)}
            </span>
          </span>
          <span
            aria-label={`${file.additions} additions, ${file.deletions} deletions`}
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 11,
              minWidth: 34,
              textAlign: "right",
              whiteSpace: "nowrap",
            }}
          >
            {file.additions > 0 && <span style={{ color: "var(--green)" }}>+{file.additions}</span>}
            {file.deletions > 0 && (
              <span style={{ color: "var(--red)", marginLeft: 5 }}>-{file.deletions}</span>
            )}
          </span>
        </button>
        <div
          style={{
            alignItems: "center",
            display: "flex",
            gap: 4,
            paddingRight: 7,
          }}
        >
          <button
            aria-label={`Copy diff ${displayPath}`}
            className="btn btn-ghost btn-sm"
            disabled={copyingPath === file.path || Boolean(revertingPath)}
            onClick={() => onCopyFileDiff(file)}
            title={`Copy diff ${displayPath}`}
            type="button"
          >
            <Icon d={copiedKey === `file:${file.path}` ? Icons.check : Icons.copy} size={12} />
          </button>
          <button
            aria-label={`Discard ${displayPath}`}
            className="btn btn-ghost btn-sm"
            disabled={revertDisabled || Boolean(revertingPath)}
            onClick={() => onRequestRevert(file.path)}
            ref={discardButtonRef}
            title={`Discard ${displayPath}`}
            type="button"
          >
            <Icon d={Icons.revert} size={12} />
          </button>
          {confirmRevertPath === file.path && (
            <ConfirmInline
              busy={revertingPath === file.path}
              disabled={revertDisabled}
              cancelAriaLabel={`Cancel discard ${displayPath}`}
              confirmAriaLabel={`Confirm discard ${displayPath}`}
              confirmLabel="Discard"
              onCancel={onCancelRevert}
              onConfirm={onConfirmRevert}
              returnFocusRef={discardButtonRef}
            />
          )}
        </div>
      </div>
      {expanded && (
        <div
          aria-label={`Diff ${displayPath}`}
          id={previewRegionID}
          role="region"
          style={{
            borderTop: "1px solid var(--border)",
            background: "var(--bg0)",
            minWidth: 0,
          }}
        >
          {loading ? (
            <div
              style={{
                color: "var(--t3)",
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                padding: "10px 12px 10px 36px",
              }}
            >
              Loading diff...
            </div>
          ) : (
            <>
              {summary && (
                <div
                  style={{
                    borderBottom: "1px solid var(--border)",
                    color: "var(--t3)",
                    fontFamily: "var(--font-mono)",
                    fontSize: 10,
                    padding: "7px 10px 7px 36px",
                  }}
                >
                  {summary}
                </div>
              )}
              <WorkspaceDiffPreview
                diff={diff}
                hasTextPatch={hasTextPatch}
                testID="workspace-file-diff-preview"
              />
            </>
          )}
        </div>
      )}
    </div>
  );
}

function ChangedFilePathLabel({ path, strong = false }: { path: string; strong?: boolean }) {
  const displayPath = escapeWorkspacePathForDisplay(path);
  const { directory, filename } = splitPathForDisplay(displayPath);

  return (
    <span style={{ display: "block", minWidth: 0 }} title={displayPath}>
      <span
        style={{
          color: "var(--t0)",
          display: "block",
          fontFamily: "var(--font-mono)",
          fontSize: strong ? 11.5 : 12,
          fontWeight: strong ? 700 : 500,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {filename}
      </span>
      {directory && (
        <span
          style={{
            color: "var(--t3)",
            display: "block",
            fontFamily: "var(--font-mono)",
            fontSize: 9.5,
            marginTop: 1,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {directory}
        </span>
      )}
    </span>
  );
}

function WorkspaceFilesView({
  expandedDirPaths,
  files,
  filesFailed,
  loading,
  query,
  tree,
  onChangeQuery,
  onToggleFolder,
}: {
  expandedDirPaths: string[];
  files: ChatWorkspaceFilesRecord | null;
  filesFailed: boolean;
  loading: boolean;
  query: string;
  tree: WorkspaceFileTreeNode[];
  onChangeQuery: (query: string) => void;
  onToggleFolder: (path: string) => void;
}) {
  if (filesFailed) {
    return (
      <div style={{ color: "var(--red)", fontSize: 11, lineHeight: 1.5 }}>
        Could not load workspace files.
      </div>
    );
  }

  return (
    <section
      aria-label="Workspace files"
      style={{
        background: "transparent",
        border: "1px solid var(--border)",
        borderRadius: 11,
        boxSizing: "border-box",
        display: "grid",
        flex: "1 1 0",
        gridTemplateRows: "auto auto minmax(0, 1fr)",
        height: "100%",
        maxHeight: "100%",
        minHeight: 0,
        minWidth: 0,
        overflow: "hidden",
      }}
    >
      <div
        style={{
          borderBottom: "1px solid var(--border)",
          display: "flex",
          flexDirection: "column",
          gap: 3,
          minWidth: 0,
          padding: "10px 12px",
        }}
      >
        <div
          style={{
            color: "var(--t0)",
            fontSize: 12.5,
            fontWeight: 750,
            minWidth: 0,
          }}
        >
          Workspace tree
        </div>
        <div
          style={{
            color: "var(--t3)",
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            minWidth: 0,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {loading
            ? "Loading workspace tree..."
            : `${files?.files.length ?? 0} visible entries${files?.truncated ? " · truncated" : ""}`}
        </div>
      </div>
      <SearchBox
        label="Search workspace files"
        placeholder="Search workspace files"
        value={query}
        onChange={onChangeQuery}
      />
      {tree.length === 0 && !loading ? (
        <div style={{ color: "var(--t3)", fontSize: 11, lineHeight: 1.5, padding: 12 }}>
          {query ? "No workspace files match that search." : "No workspace files loaded."}
        </div>
      ) : (
        <BoundedWorkspaceFileTree
          expandedDirPaths={expandedDirPaths}
          query={query}
          tree={tree}
          onToggleFolder={onToggleFolder}
        />
      )}
    </section>
  );
}

function BoundedWorkspaceFileTree({
  expandedDirPaths,
  query,
  tree,
  onToggleFolder,
}: {
  expandedDirPaths: string[];
  query: string;
  tree: WorkspaceFileTreeNode[];
  onToggleFolder: (path: string) => void;
}) {
  const visibleNodes = useMemo(
    () => flattenVisibleWorkspaceFileTree(tree, new Set(expandedDirPaths)),
    [expandedDirPaths, tree],
  );
  const [renderWindow, setRenderWindow] = useState({
    tree,
    query,
    expandedDirPaths,
    limit: WORKSPACE_FILE_TREE_INITIAL_RENDER_LIMIT,
  });
  const renderLimit =
    renderWindow.tree === tree &&
    renderWindow.query === query &&
    renderWindow.expandedDirPaths === expandedDirPaths
      ? renderWindow.limit
      : WORKSPACE_FILE_TREE_INITIAL_RENDER_LIMIT;

  useEffect(() => {
    setRenderWindow((current) =>
      current.tree === tree &&
      current.query === query &&
      current.expandedDirPaths === expandedDirPaths
        ? current
        : {
            tree,
            query,
            expandedDirPaths,
            limit: WORKSPACE_FILE_TREE_INITIAL_RENDER_LIMIT,
          },
    );
  }, [expandedDirPaths, query, tree]);

  const boundedNodes = visibleNodes.slice(0, renderLimit);
  const remainingNodeCount = Math.max(0, visibleNodes.length - boundedNodes.length);

  return (
    <div
      aria-label="Workspace file tree"
      style={{
        boxSizing: "border-box",
        display: "block",
        height: "100%",
        maxHeight: "100%",
        minHeight: 0,
        minWidth: 0,
        overflowX: "hidden",
        overflowY: "auto",
        overscrollBehavior: "contain",
      }}
    >
      {boundedNodes.map((node) => (
        <WorkspaceFileTreeRow
          key={node.key}
          expandedDirPaths={expandedDirPaths}
          node={node}
          onToggleFolder={onToggleFolder}
        />
      ))}
      {remainingNodeCount > 0 && (
        <div
          style={{
            alignItems: "center",
            background: "var(--bg0)",
            borderTop: "1px solid var(--border)",
            display: "flex",
            justifyContent: "center",
            padding: 10,
          }}
        >
          <button
            className="btn btn-ghost btn-sm"
            onClick={() =>
              setRenderWindow({
                tree,
                query,
                expandedDirPaths,
                limit: renderLimit + WORKSPACE_FILE_TREE_RENDER_INCREMENT,
              })
            }
            type="button"
          >
            Show more tree entries · {remainingNodeCount} remaining
          </button>
        </div>
      )}
    </div>
  );
}

function WorkspacePathLabel({ workspace }: { workspace: string }) {
  const displayWorkspace = escapeWorkspacePathForDisplay(workspace);
  return (
    <div
      title={displayWorkspace}
      style={{
        color: "var(--t3)",
        fontFamily: "var(--font-mono)",
        fontSize: 10,
        overflow: "hidden",
        textOverflow: "ellipsis",
        whiteSpace: "nowrap",
      }}
    >
      {displayWorkspace || "No workspace selected"}
    </div>
  );
}

function WorkspaceFileTreeRow({
  expandedDirPaths,
  node,
  onToggleFolder,
}: {
  expandedDirPaths: string[];
  node: WorkspaceFileTreeNode;
  onToggleFolder: (path: string) => void;
}) {
  if (node.kind === "folder") {
    const expanded = expandedDirPaths.includes(node.path);
    const displayName = escapeWorkspacePathForDisplay(node.name);
    const displayPath = escapeWorkspacePathForDisplay(node.path);
    return (
      <button
        aria-expanded={expanded}
        aria-label={`${expanded ? "Collapse" : "Expand"} folder ${displayPath}`}
        data-testid="workspace-file-tree-row"
        onClick={() => onToggleFolder(node.path)}
        style={{
          alignItems: "center",
          backgroundColor: "transparent",
          border: 0,
          borderTop: "1px solid var(--border)",
          color: "var(--t2)",
          cursor: "pointer",
          display: "grid",
          gap: 8,
          gridTemplateColumns: "auto auto minmax(0, 1fr) auto",
          minWidth: 0,
          padding: "7px 12px",
          paddingLeft: 12 + node.depth * 12,
          textAlign: "left",
          width: "100%",
        }}
        type="button"
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
          {displayName}
        </span>
        <span style={{ color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 9.5 }}>
          {node.fileCount}
        </span>
      </button>
    );
  }

  const displayName = escapeWorkspacePathForDisplay(node.name);
  const displayPath = escapeWorkspacePathForDisplay(node.file.path);

  return (
    <div
      data-testid="workspace-file-tree-row"
      title={displayPath}
      style={{
        alignItems: "center",
        borderTop: "1px solid var(--border)",
        color: "var(--t2)",
        display: "grid",
        gap: 8,
        gridTemplateColumns: "auto minmax(0, 1fr) auto",
        minWidth: 0,
        padding: "7px 12px",
        paddingLeft: 12 + node.depth * 12,
      }}
    >
      <span
        title={
          node.file.status
            ? fileStatusLabel(node.file.status)
            : node.file.kind === "unavailable"
              ? "Metadata unavailable on an unsupported filesystem"
              : "File"
        }
        style={{
          alignItems: "center",
          border: "1px solid var(--border)",
          borderRadius: 6,
          color: node.file.status ? fileStatusColor(node.file.status) : "var(--t3)",
          display: "inline-flex",
          fontFamily: "var(--font-mono)",
          fontSize: 9,
          height: 18,
          justifyContent: "center",
          width: 18,
        }}
      >
        {node.file.status
          ? fileStatusGlyph(node.file.status)
          : node.file.kind === "unavailable"
            ? "U"
            : "F"}
      </span>
      <span
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 10.5,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {displayName}
      </span>
      {node.file.status && (
        <span style={{ color: fileStatusColor(node.file.status), fontSize: 9.5 }}>
          {node.file.status}
        </span>
      )}
    </div>
  );
}

function SearchBox({
  disabled = false,
  label,
  placeholder,
  value,
  onChange,
}: {
  disabled?: boolean;
  label: string;
  placeholder: string;
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <label
      style={{
        alignItems: "center",
        background: "var(--bg0)",
        borderBottom: "1px solid var(--border)",
        color: "var(--t3)",
        display: "grid",
        gap: 8,
        gridTemplateColumns: "auto minmax(0, 1fr)",
        padding: "9px 12px",
      }}
    >
      <Icon d={Icons.search} size={12} />
      <input
        aria-label={label}
        className="workspace-panel-search-input"
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
        style={{
          background: "transparent",
          border: 0,
          color: "var(--t1)",
          font: "inherit",
          fontFamily: "var(--font-mono)",
          fontSize: 10.5,
          minWidth: 0,
          padding: 0,
        }}
        value={value}
      />
    </label>
  );
}

function ConfirmInline({
  busy,
  disabled,
  cancelAriaLabel,
  confirmAriaLabel,
  confirmLabel,
  onCancel,
  onConfirm,
  returnFocusRef,
}: {
  busy: boolean;
  disabled: boolean;
  cancelAriaLabel: string;
  confirmAriaLabel: string;
  confirmLabel: string;
  onCancel: () => void;
  onConfirm: () => void;
  returnFocusRef: RefObject<HTMLButtonElement | null>;
}) {
  const confirmRef = useRef<HTMLButtonElement | null>(null);

  useLayoutEffect(() => {
    confirmRef.current?.focus();
  }, []);

  return (
    <div style={{ display: "flex", gap: 5 }}>
      <button
        aria-label={confirmAriaLabel}
        className="btn btn-ghost btn-sm"
        disabled={busy || disabled}
        onClick={onConfirm}
        ref={confirmRef}
        type="button"
      >
        {busy ? "Working..." : confirmLabel}
      </button>
      <button
        aria-label={cancelAriaLabel}
        className="btn btn-ghost btn-sm"
        disabled={busy}
        onClick={() => {
          onCancel();
          returnFocusRef.current?.focus();
        }}
        type="button"
      >
        Cancel
      </button>
    </div>
  );
}

function WorkspaceDiffPreview({
  diff,
  hasTextPatch,
  testID = "workspace-diff-preview",
}: {
  diff: string;
  hasTextPatch?: boolean;
  testID?: string;
}) {
  const normalizedDiff = diff.trim();
  const [layoutTick, setLayoutTick] = useState(0);

  useEffect(() => {
    setLayoutTick(0);
    if (!normalizedDiff) return;
    let firstFrame = 0;
    let secondFrame = 0;
    firstFrame = window.requestAnimationFrame(() => {
      secondFrame = window.requestAnimationFrame(() => setLayoutTick((current) => current + 1));
    });
    return () => {
      window.cancelAnimationFrame(firstFrame);
      window.cancelAnimationFrame(secondFrame);
    };
  }, [normalizedDiff]);

  return (
    <div
      data-testid={testID}
      style={{
        background: "var(--bg0)",
        borderTop: "1px solid var(--border)",
        isolation: "isolate",
        minWidth: 0,
        overflow: "hidden",
        padding: normalizedDiff && hasTextPatch !== false ? 0 : 8,
      }}
    >
      {hasTextDiffHunks(normalizedDiff) ? (
        <DiffViewer
          key={`${testID}:${layoutTick}:${diffPreviewKey(normalizedDiff)}`}
          compact
          embedded
          diff={diff}
        />
      ) : normalizedDiff ? (
        <RawPatchPreview diff={normalizedDiff} />
      ) : (
        <NoTextDiffPreview />
      )}
    </div>
  );
}

function RawPatchPreview({ diff }: { diff: string }) {
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: 8,
        minWidth: 0,
        overflow: "hidden",
      }}
    >
      <div
        style={{
          background: "var(--bg2)",
          borderBottom: "1px solid var(--border)",
          color: "var(--t2)",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          letterSpacing: ".08em",
          padding: "7px 9px",
          textTransform: "uppercase",
        }}
      >
        raw patch
      </div>
      <pre
        style={{
          color: "var(--t1)",
          fontFamily: "var(--font-mono)",
          fontSize: 10.5,
          lineHeight: 1.55,
          margin: 0,
          maxHeight: 260,
          overflow: "auto",
          padding: "9px 10px",
          whiteSpace: "pre-wrap",
          wordBreak: "break-word",
        }}
      >
        {diff}
      </pre>
    </div>
  );
}

function textPatchForChangedFile(
  file: ChatChangedFileRecord,
  workspaceDiff: string,
  fileDiffs: Record<string, ChatChangedFileDiffRecord>,
): string {
  return fileDiffs[file.path]?.diff || extractFilePatchFromWorkspaceDiff(workspaceDiff, file.path);
}

function NoTextDiffPreview() {
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: 8,
        color: "var(--t3)",
        fontSize: 11,
        lineHeight: 1.5,
        padding: "10px 12px",
      }}
    >
      No text diff was captured for this file. It may be binary, generated, or unchanged in the
      textual patch.
    </div>
  );
}

function hasTextDiffHunks(diff: string): boolean {
  return /^@@\s/m.test(diff);
}

function diffPreviewKey(diff: string): string {
  return `${diff.length}:${diff.slice(0, 80)}`;
}

type WorkspaceFileTreeNode =
  | {
      kind: "folder";
      key: string;
      name: string;
      path: string;
      depth: number;
      fileCount: number;
      children: WorkspaceFileTreeNode[];
    }
  | {
      kind: "file";
      key: string;
      name: string;
      path: string;
      depth: number;
      file: ChatWorkspaceFileRecord;
    };

type WorkspaceFileTreeDraftNode = {
  children: Map<string, WorkspaceFileTreeDraftNode>;
  file?: ChatWorkspaceFileRecord;
};

function buildWorkspaceFileTree(
  files: ChatWorkspaceFileRecord[],
  rawQuery: string,
): WorkspaceFileTreeNode[] {
  const query = rawQuery.trim().toLowerCase();
  const root: WorkspaceFileTreeDraftNode = { children: new Map() };
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
    const name = parts.at(-1) ?? file.name ?? file.path;
    if (file.kind === "directory") {
      current.children.set(name, current.children.get(name) ?? { children: new Map() });
      continue;
    }
    current.children.set(name, { children: new Map(), file });
  }

  return sortedWorkspaceTreeEntries(root.children).flatMap(([name, child]) =>
    materializeWorkspaceFileTreeNode(name, name, child, 0),
  );
}

function materializeWorkspaceFileTreeNode(
  name: string,
  path: string,
  node: WorkspaceFileTreeDraftNode,
  depth: number,
): WorkspaceFileTreeNode[] {
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
  while (current.children.size === 1) {
    const [[onlyName, onlyChild]] = Array.from(current.children.entries());
    if (onlyChild.file) break;
    folderName = `${folderName}/${onlyName}`;
    folderPath = `${folderPath}/${onlyName}`;
    current = onlyChild;
  }

  const children = sortedWorkspaceTreeEntries(current.children).flatMap(([childName, child]) =>
    materializeWorkspaceFileTreeNode(childName, `${folderPath}/${childName}`, child, depth + 1),
  );

  return [
    {
      kind: "folder",
      key: `folder:${folderPath}`,
      name: folderName,
      path: folderPath,
      depth,
      fileCount: countFileTreeFiles(children),
      children,
    },
  ];
}

function sortedWorkspaceTreeEntries(
  children: Map<string, WorkspaceFileTreeDraftNode>,
): [string, WorkspaceFileTreeDraftNode][] {
  return Array.from(children.entries()).sort(([leftName, left], [rightName, right]) => {
    const leftIsFolder = !left.file;
    const rightIsFolder = !right.file;
    if (leftIsFolder !== rightIsFolder) return leftIsFolder ? -1 : 1;
    return leftName.localeCompare(rightName);
  });
}

function collectFileTreeFolderPaths(nodes: WorkspaceFileTreeNode[]): string[] {
  return nodes.flatMap((node) =>
    node.kind === "folder" ? [node.path, ...collectFileTreeFolderPaths(node.children)] : [],
  );
}

function flattenVisibleWorkspaceFileTree(
  nodes: WorkspaceFileTreeNode[],
  expandedDirPaths: Set<string>,
): WorkspaceFileTreeNode[] {
  return nodes.flatMap((node) => {
    if (node.kind !== "folder" || !expandedDirPaths.has(node.path)) return [node];
    return [node, ...flattenVisibleWorkspaceFileTree(node.children, expandedDirPaths)];
  });
}

function countFileTreeFiles(nodes: WorkspaceFileTreeNode[]): number {
  return nodes.reduce(
    (count, node) => count + (node.kind === "file" ? 1 : countFileTreeFiles(node.children)),
    0,
  );
}

function filterChangedFiles(files: ChatChangedFileRecord[], rawQuery: string) {
  const query = rawQuery.trim().toLowerCase();
  if (!query) return files;
  return files.filter((file) => file.path.toLowerCase().includes(query));
}

type InitialDiffSelection = {
  file?: ChatChangedFileRecord;
  loadFailed: boolean;
};

async function findInitialDiffFile(
  files: ChatChangedFileRecord[],
  diff: string,
  loadDiff: (file: ChatChangedFileRecord) => Promise<string>,
): Promise<InitialDiffSelection> {
  if (files.length === 0) return { loadFailed: false };

  const candidates = prioritizeDiffCandidates(files, diff);
  if (diff.trim()) {
    const textDiffFile = candidates.find((file) =>
      hasTextDiffHunks(extractFilePatchFromWorkspaceDiff(diff, file.path)),
    );
    if (textDiffFile) {
      try {
        await loadDiff(textDiffFile);
        return { file: textDiffFile, loadFailed: false };
      } catch {
        return { file: textDiffFile, loadFailed: true };
      }
    }
  }

  return { loadFailed: false };
}

function prioritizeDiffCandidates(
  files: ChatChangedFileRecord[],
  diff = "",
): ChatChangedFileRecord[] {
  const textPatchPaths = new Set<string>();
  if (diff.trim()) {
    for (const file of files) {
      if (hasTextDiffHunks(extractFilePatchFromWorkspaceDiff(diff, file.path))) {
        textPatchPaths.add(file.path);
      }
    }
  }

  const textPatchFiles = files.filter((file) => textPatchPaths.has(file.path));
  const remaining = files.filter((file) => !textPatchPaths.has(file.path));
  const textLikelyChanged = remaining.filter(
    (file) => isTextDiffCandidatePath(file.path) && changedFileHasLineDelta(file),
  );
  const textLikelyNoDelta = remaining.filter(
    (file) => isTextDiffCandidatePath(file.path) && !changedFileHasLineDelta(file),
  );
  const changedButUnknown = files.filter(
    (file) =>
      !textPatchPaths.has(file.path) &&
      !textLikelyChanged.includes(file) &&
      !textLikelyNoDelta.includes(file) &&
      !isLikelyBinaryPath(file.path) &&
      changedFileHasLineDelta(file),
  );
  const textUnlikely = files.filter(
    (file) =>
      !textPatchPaths.has(file.path) &&
      !textLikelyChanged.includes(file) &&
      !textLikelyNoDelta.includes(file) &&
      !changedButUnknown.includes(file),
  );
  return [
    ...textPatchFiles,
    ...textLikelyChanged,
    ...changedButUnknown,
    ...textLikelyNoDelta,
    ...textUnlikely,
  ];
}

function changedFileHasLineDelta(file: ChatChangedFileRecord): boolean {
  return file.additions > 0 || file.deletions > 0;
}

function isTextDiffCandidatePath(path: string): boolean {
  const extension = fileExtension(path);
  if (!extension) return true;
  if (TEXT_DIFF_EXTENSIONS.has(extension)) return true;
  return !NON_TEXT_DIFF_EXTENSIONS.has(extension);
}

function isLikelyBinaryPath(path: string): boolean {
  const extension = fileExtension(path);
  return extension ? NON_TEXT_DIFF_EXTENSIONS.has(extension) : false;
}

function fileExtension(path: string): string {
  const name = path.split("/").at(-1) ?? path;
  const dot = name.lastIndexOf(".");
  if (dot <= 0 || dot === name.length - 1) return "";
  return name.slice(dot + 1).toLowerCase();
}

function changedFileTotals(files: ChatChangedFileRecord[]) {
  return files.reduce(
    (totals, file) => ({
      additions: totals.additions + Math.max(0, file.additions),
      deletions: totals.deletions + Math.max(0, file.deletions),
    }),
    { additions: 0, deletions: 0 },
  );
}

function summarizeChangedFiles(files: ChatChangedFileRecord[], diffStat: string): string {
  const fromStat = diffStat ? formatDiffStatSummary(diffStat) : "";
  if (fromStat) return fromStat;
  const totals = changedFileTotals(files);
  return `${files.length} file${files.length === 1 ? "" : "s"} changed, ${totals.additions} insertion${totals.additions === 1 ? "" : "s"}(+), ${totals.deletions} deletion${totals.deletions === 1 ? "" : "s"}(-)`;
}

function extractFilePatchFromWorkspaceDiff(diff: string, path: string): string {
  const normalizedPath = path.replaceAll("\\", "/");
  const patch = diff.replace(/\r\n?/g, "\n");
  const headers = [...patch.matchAll(/^diff --git a\/(.+?) b\/(.+)$/gm)];
  for (let index = 0; index < headers.length; index += 1) {
    const match = headers[index];
    const start = match.index ?? 0;
    const end = headers[index + 1]?.index ?? patch.length;
    const left = (match[1] ?? "").replaceAll("\\", "/");
    const right = (match[2] ?? "").replaceAll("\\", "/");
    if (left === normalizedPath || right === normalizedPath) {
      return patch.slice(start, end).trim();
    }
  }
  return "";
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
    case "copied":
      return "C";
    case "conflict":
      return "!";
    case "type_changed":
      return "T";
    default:
      return "M";
  }
}

function fileStatusLabel(status: string): string {
  switch (status.toLowerCase()) {
    case "added":
    case "new":
    case "untracked":
      return "Added file";
    case "deleted":
    case "removed":
      return "Deleted file";
    case "renamed":
      return "Renamed file";
    case "copied":
      return "Copied file";
    case "conflict":
      return "Conflicted file";
    case "type_changed":
      return "Type-changed file";
    default:
      return "Modified file";
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
    case "copied":
      return "var(--amber)";
    case "conflict":
      return "var(--red)";
    case "type_changed":
      return "var(--amber)";
    default:
      return "var(--teal)";
  }
}

function formatChangedFileMeta(file: ChatChangedFileRecord): string {
  const parts = [file.status || "modified"];
  if (file.additions > 0) parts.push(`+${file.additions}`);
  if (file.deletions > 0) parts.push(`-${file.deletions}`);
  if (parts.length === 1) {
    parts.push(isLikelyBinaryPath(file.path) ? "binary / generated" : "metadata only");
  }
  return parts.join(" · ");
}

function summarizeDiffAvailability(file: ChatChangedFileRecord, diff: string): string {
  if (hasTextDiffHunks(diff.trim())) return "";
  if (isLikelyBinaryPath(file.path)) return "no text diff";
  return "diff not captured";
}

function splitPathForDisplay(path: string): { directory: string; filename: string } {
  const lastSlash = path.lastIndexOf("/");
  if (lastSlash < 0) return { directory: "", filename: path };
  return {
    directory: path.slice(0, lastSlash),
    filename: path.slice(lastSlash + 1) || path,
  };
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
