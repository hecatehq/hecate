import {
  useEffect,
  useId,
  useRef,
  useState,
  type DragEvent,
  type FocusEvent,
  type MouseEvent as ReactMouseEvent,
} from "react";

import { getChatAttachmentContentBlob } from "../../lib/api";
import type { PendingChatAttachment } from "../../app/state/_shared";
import type { ChatAttachmentRecord } from "../../types/chat";
import { Icon, Icons, Modal } from "../shared/ui";

export const MAX_CHAT_IMAGE_ATTACHMENTS = 4;
export const MAX_CHAT_IMAGE_BYTES = 5 * 1024 * 1024;
export const MAX_CHAT_IMAGE_MESSAGE_BYTES = 12 * 1024 * 1024;

export type ChatAttachmentAcceptance = "images" | "files";

const STORED_IMAGE_PREVIEW_WIDTH = 132;
const STORED_IMAGE_PREVIEW_HEIGHT = 96;
const STORED_IMAGE_PREVIEW_FRAME_STYLE = {
  width: STORED_IMAGE_PREVIEW_WIDTH,
  height: STORED_IMAGE_PREVIEW_HEIGHT,
  boxSizing: "border-box" as const,
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  background: "var(--bg3)",
};

const VISUALLY_HIDDEN_STYLE = {
  position: "absolute" as const,
  width: 1,
  height: 1,
  padding: 0,
  margin: -1,
  overflow: "hidden",
  clip: "rect(0, 0, 0, 0)",
  whiteSpace: "nowrap" as const,
  border: 0,
};

export const CHAT_IMAGE_MEDIA_TYPES = new Set(["image/png", "image/jpeg", "image/webp"]);

export type ChatImageSelectionResult = {
  attachments: PendingChatAttachment[];
  error: string;
};

export function appendChatImageFiles(
  current: PendingChatAttachment[],
  files: Iterable<File>,
): ChatImageSelectionResult {
  return appendChatFiles(current, files, "images");
}

export function appendChatFiles(
  current: PendingChatAttachment[],
  files: Iterable<File>,
  acceptance: ChatAttachmentAcceptance,
): ChatImageSelectionResult {
  const attachments = [...current];
  const errors: string[] = [];
  const subject = acceptance === "files" ? "file" : "image";

  for (const file of files) {
    const declaredType = file.type.trim().toLowerCase();
    if (acceptance === "images" && declaredType && !CHAT_IMAGE_MEDIA_TYPES.has(declaredType)) {
      errors.push(`${file.name || "Image"} must be PNG, JPEG, or WebP.`);
      continue;
    }
    if (file.size <= 0) {
      errors.push(`${file.name || "File"} is empty.`);
      continue;
    }
    if (file.size > MAX_CHAT_IMAGE_BYTES) {
      errors.push(`${file.name || "File"} exceeds the 5 MiB limit.`);
      continue;
    }
    if (attachments.length >= MAX_CHAT_IMAGE_ATTACHMENTS) {
      errors.push(`A message can include up to 4 ${subject}s.`);
      break;
    }
    const combinedBytes = attachments.reduce(
      (total, attachment) => total + attachment.file.size,
      0,
    );
    if (file.size > MAX_CHAT_IMAGE_MESSAGE_BYTES - combinedBytes) {
      errors.push(
        `${acceptance === "files" ? "Files" : "Images"} in one message can total up to 12 MiB.`,
      );
      continue;
    }
    attachments.push({ id: pendingAttachmentID(), file });
  }

  return { attachments, error: errors[0] ?? "" };
}

export function ChatAttachmentDrafts({
  attachments,
  acceptance = "images",
  enabled,
  disabledReason,
  describedBy,
  error,
  compact = false,
  onAddFiles,
  onRemove,
}: {
  attachments: PendingChatAttachment[];
  acceptance?: ChatAttachmentAcceptance;
  enabled: boolean;
  disabledReason: string;
  describedBy?: string;
  error?: string;
  compact?: boolean;
  onAddFiles: (files: File[]) => void;
  onRemove: (id: string) => void;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  const addButtonRef = useRef<HTMLButtonElement>(null);
  const unavailableStatusRef = useRef<HTMLSpanElement>(null);
  const removeButtonRefs = useRef(new Map<string, HTMLButtonElement>());
  const previousAttachmentsRef = useRef(attachments);
  const reasonID = useId();
  const errorID = useId();
  const [dragging, setDragging] = useState(false);
  const [attachmentAnnouncement, setAttachmentAnnouncement] = useState("");
  const acceptsFiles = acceptance === "files";
  const atLimit = attachments.length >= MAX_CHAT_IMAGE_ATTACHMENTS;
  const canAdd = enabled && !atLimit;
  const unavailableReason = atLimit
    ? `A message can include up to 4 ${acceptsFiles ? "files" : "images"}.`
    : disabledReason;
  const attachmentDescriptionIDs = [
    describedBy ?? "",
    !canAdd && unavailableReason ? reasonID : "",
    error ? errorID : "",
  ]
    .filter(Boolean)
    .join(" ");

  useEffect(() => {
    const previous = previousAttachmentsRef.current;
    previousAttachmentsRef.current = attachments;
    const previousIDs = new Set(previous.map((attachment) => attachment.id));
    const currentIDs = new Set(attachments.map((attachment) => attachment.id));
    const added = attachments.filter((attachment) => !previousIDs.has(attachment.id));
    const removed = previous.filter((attachment) => !currentIDs.has(attachment.id));
    if (added.length === 0 && removed.length === 0) return;

    const countStatus = attachmentDraftCountStatus(attachments.length, acceptance);
    if (added.length > 0 && removed.length === 0) {
      const subject =
        added.length === 1
          ? `${added[0]?.file.name || "File"} added.`
          : `${added.length} ${acceptsFiles ? "files" : "images"} added.`;
      setAttachmentAnnouncement(`${subject} ${countStatus}`);
      return;
    }
    if (removed.length > 0 && added.length === 0) {
      const subject =
        removed.length === 1
          ? `${removed[0]?.file.name || "File"} removed.`
          : `${removed.length} ${acceptsFiles ? "files" : "images"} removed.`;
      setAttachmentAnnouncement(`${subject} ${countStatus}`);
      return;
    }
    setAttachmentAnnouncement(
      `${acceptsFiles ? "File" : "Image"} attachments updated. ${countStatus}`,
    );
  }, [acceptance, acceptsFiles, attachments]);

  function handleDrop(event: DragEvent<HTMLDivElement>) {
    if (event.dataTransfer.files.length === 0) return;
    event.preventDefault();
    setDragging(false);
    if (!canAdd) return;
    onAddFiles(Array.from(event.dataTransfer.files));
  }

  function handleRemove(attachmentID: string, index: number) {
    const source = removeButtonRefs.current.get(attachmentID) ?? null;
    const transferFocus = document.activeElement === source;
    const successorID = attachments[index + 1]?.id ?? attachments[index - 1]?.id ?? "";
    onRemove(attachmentID);
    requestAnimationFrame(() => {
      if (!transferFocus) return;
      const active = document.activeElement;
      const sourceStillOwnsFocus = active === source;
      const sourceDisappearedIntoBody =
        Boolean(source && !source.isConnected) && active === document.body;
      if (!sourceStillOwnsFocus && !sourceDisappearedIntoBody) return;
      if (successorID) {
        const successor = removeButtonRefs.current.get(successorID);
        if (successor) {
          successor.focus();
          return;
        }
      }
      if (addButtonRef.current && !addButtonRef.current.disabled) {
        addButtonRef.current.focus();
        return;
      }
      unavailableStatusRef.current?.focus();
    });
  }

  return (
    <div
      aria-label={acceptsFiles ? "File attachments" : "Image attachments"}
      role="group"
      onDragEnter={(event) => {
        if (event.dataTransfer.types.includes("Files") && canAdd) setDragging(true);
      }}
      onDragLeave={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragging(false);
      }}
      onDragOver={(event) => {
        if (!event.dataTransfer.types.includes("Files")) return;
        event.preventDefault();
        event.dataTransfer.dropEffect = canAdd ? "copy" : "none";
      }}
      onDrop={handleDrop}
      style={{
        display: "grid",
        minWidth: 0,
        gap: compact ? 4 : 6,
        marginBottom: compact ? 0 : 6,
        border: dragging ? "1px dashed var(--teal)" : "1px solid transparent",
        borderRadius: "var(--radius-sm)",
        background: dragging ? "var(--teal-bg)" : "transparent",
        padding: dragging ? 6 : compact ? 0 : "0 6px",
      }}
    >
      {attachments.length > 0 && (
        <div
          aria-label={acceptsFiles ? "Files ready to attach" : "Images ready to attach"}
          role="group"
          style={{ display: "flex", flexWrap: "wrap", gap: 7 }}
        >
          {attachments.map((attachment, index) => (
            <DraftAttachmentPreview
              key={attachment.id}
              attachment={attachment}
              removeButtonRef={(node) => {
                if (node) removeButtonRefs.current.set(attachment.id, node);
                else removeButtonRefs.current.delete(attachment.id);
              }}
              onRemove={() => handleRemove(attachment.id, index)}
            />
          ))}
        </div>
      )}

      <div style={{ display: "flex", alignItems: "center", gap: 8, minHeight: 22 }}>
        <input
          ref={inputRef}
          type="file"
          accept={acceptance === "images" ? "image/png,image/jpeg,image/webp" : undefined}
          multiple
          hidden
          disabled={!canAdd}
          aria-label={acceptsFiles ? "Choose files" : "Choose images"}
          aria-describedby={attachmentDescriptionIDs || undefined}
          aria-invalid={Boolean(error) || undefined}
          onChange={(event) => {
            const files = Array.from(event.currentTarget.files ?? []);
            event.currentTarget.value = "";
            if (files.length > 0) onAddFiles(files);
          }}
        />
        <button
          ref={addButtonRef}
          type="button"
          className="btn btn-ghost btn-sm"
          disabled={!canAdd}
          aria-describedby={attachmentDescriptionIDs || undefined}
          onClick={() => inputRef.current?.click()}
          title={
            canAdd
              ? acceptance === "images"
                ? "Attach PNG, JPEG, or WebP images · 4 images · 5 MiB each · 12 MiB total"
                : "Attach files · 4 files · 5 MiB each · 12 MiB total"
              : unavailableReason
          }
          style={{
            padding: "2px 7px",
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            gap: 5,
          }}
        >
          <Icon d={Icons.plus} size={11} /> {acceptsFiles ? "Files" : "Image"}
        </button>
        <span
          ref={unavailableStatusRef}
          id={!canAdd && unavailableReason ? reasonID : undefined}
          tabIndex={!canAdd && unavailableReason ? -1 : undefined}
          title={compact ? (canAdd ? undefined : unavailableReason) : undefined}
          style={{
            color: "var(--t3)",
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            maxWidth: compact ? 150 : undefined,
            overflow: compact ? "hidden" : undefined,
            textOverflow: compact ? "ellipsis" : undefined,
            whiteSpace: compact ? "nowrap" : undefined,
          }}
        >
          {dragging
            ? `Drop ${acceptsFiles ? "files" : "images"} here`
            : canAdd
              ? compact
                ? acceptance === "images"
                  ? "PNG/JPEG/WebP · 4 max"
                  : "4 files max"
                : acceptance === "images"
                  ? "paste, choose, or drop · PNG/JPEG/WebP · 5 MiB each · 12 MiB total"
                  : "paste, choose, or drop · 4 files · 5 MiB each · 12 MiB total"
              : unavailableReason}
        </span>
      </div>

      {error && (
        <div
          id={errorID}
          role="alert"
          style={{ color: "var(--red)", fontSize: 11, lineHeight: 1.4 }}
        >
          {error}
        </div>
      )}
      <div aria-atomic="true" aria-live="polite" role="status" style={VISUALLY_HIDDEN_STYLE}>
        {attachmentAnnouncement}
      </div>
    </div>
  );
}

export function ChatAttachmentGallery({ attachments }: { attachments?: ChatAttachmentRecord[] }) {
  if (!attachments?.length) return null;
  const hasOnlyImages = attachments.every((attachment) =>
    isPreviewableImageMediaType(attachment.media_type),
  );
  return (
    <div
      aria-label={hasOnlyImages ? "Attached images" : "Attached files"}
      role="group"
      style={{ display: "flex", flexWrap: "wrap", gap: 8, marginBottom: 8 }}
    >
      {attachments.map((attachment) =>
        isPreviewableImageMediaType(attachment.media_type) ? (
          <StoredImagePreview key={attachment.id} attachment={attachment} />
        ) : (
          <StoredFileDownload key={attachment.id} attachment={attachment} />
        ),
      )}
    </div>
  );
}

function DraftAttachmentPreview({
  attachment,
  removeButtonRef,
  onRemove,
}: {
  attachment: PendingChatAttachment;
  removeButtonRef: (node: HTMLButtonElement | null) => void;
  onRemove: () => void;
}) {
  const previewable = isPreviewableImageMediaType(attachment.file.type);
  const src = useObjectURL(previewable ? attachment.file : null);
  const filename = attachment.file.name || "unnamed file";
  return (
    <div
      style={{
        position: "relative",
        width: previewable ? 88 : 188,
        height: 70,
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        background: "var(--bg3)",
        overflow: "hidden",
      }}
    >
      {src ? (
        <img
          src={src}
          alt={filename}
          style={{
            width: "100%",
            height: "100%",
            objectFit: "cover",
            display: "block",
          }}
        />
      ) : (
        <div
          style={{
            height: "100%",
            display: "grid",
            alignContent: "center",
            gap: 3,
            padding: "8px 34px 8px 9px",
            boxSizing: "border-box",
          }}
        >
          <span
            title={filename}
            style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
          >
            {filename}
          </span>
          <span style={{ color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 10 }}>
            {formatBytes(attachment.file.size)}
          </span>
        </div>
      )}
      <button
        ref={removeButtonRef}
        type="button"
        aria-label={`Remove ${filename}`}
        title={`Remove ${filename}`}
        onClick={onRemove}
        style={{
          position: "absolute",
          top: 4,
          right: 4,
          width: 22,
          height: 22,
          display: "grid",
          placeItems: "center",
          border: "1px solid var(--border-strong)",
          borderRadius: 999,
          background: "var(--bg0)",
          color: "var(--t1)",
          cursor: "pointer",
        }}
      >
        <Icon d={Icons.x} size={11} />
      </button>
    </div>
  );
}

function StoredFileDownload({ attachment }: { attachment: ChatAttachmentRecord }) {
  const controllerRef = useRef<AbortController | null>(null);
  const [downloading, setDownloading] = useState(false);
  const [failed, setFailed] = useState(false);
  const [announcement, setAnnouncement] = useState("");
  const errorID = useId();

  useEffect(
    () => () => {
      controllerRef.current?.abort();
    },
    [],
  );

  async function download() {
    if (downloading) return;
    const controller = new AbortController();
    controllerRef.current?.abort();
    controllerRef.current = controller;
    setDownloading(true);
    setFailed(false);
    setAnnouncement("");
    let objectURL = "";
    try {
      const blob = await getChatAttachmentContentBlob(
        attachment.session_id,
        attachment.id,
        controller.signal,
      );
      if (controller.signal.aborted) return;
      objectURL = createObjectURL(blob);
      if (!objectURL) throw new Error("download URL unavailable");
      const link = document.createElement("a");
      link.href = objectURL;
      link.download = attachment.filename || "attachment";
      link.rel = "noopener";
      link.hidden = true;
      document.body.append(link);
      link.click();
      link.remove();
      const completedURL = objectURL;
      objectURL = "";
      globalThis.setTimeout(() => revokeObjectURL(completedURL), 0);
      setAnnouncement(`${attachment.filename} download started.`);
    } catch {
      if (!controller.signal.aborted) {
        setFailed(true);
        setAnnouncement(`${attachment.filename} download failed.`);
      }
    } finally {
      if (objectURL) revokeObjectURL(objectURL);
      if (controllerRef.current === controller) controllerRef.current = null;
      if (!controller.signal.aborted) setDownloading(false);
    }
  }

  return (
    <div
      style={{
        minWidth: 210,
        maxWidth: "100%",
        minHeight: 58,
        display: "grid",
        gridTemplateColumns: "minmax(0, 1fr) auto",
        alignItems: "center",
        gap: 10,
        border: failed ? "1px solid var(--red-border)" : "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        background: "var(--bg3)",
        padding: "8px 9px",
      }}
    >
      <div style={{ minWidth: 0, display: "grid", gap: 3 }}>
        <span
          title={attachment.filename}
          style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
        >
          {attachment.filename}
        </span>
        <span style={{ color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 10 }}>
          {formatBytes(attachment.size_bytes)} · {attachment.media_type || "unknown type"}
        </span>
        {failed && (
          <span id={errorID} role="alert" style={{ color: "var(--red)", fontSize: 10 }}>
            Download failed. Try again.
          </span>
        )}
      </div>
      <button
        type="button"
        className="btn btn-ghost btn-sm"
        aria-describedby={failed ? errorID : undefined}
        aria-label={`${failed ? "Retry download" : "Download"} ${attachment.filename}`}
        aria-busy={downloading || undefined}
        disabled={downloading}
        onClick={() => void download()}
        style={{ padding: "3px 7px", fontSize: 10 }}
      >
        {downloading ? "Downloading..." : failed ? "Retry" : "Download"}
      </button>
      <span aria-atomic="true" aria-live="polite" role="status" style={VISUALLY_HIDDEN_STYLE}>
        {announcement}
      </span>
    </div>
  );
}

function StoredImagePreview({ attachment }: { attachment: ChatAttachmentRecord }) {
  const viewportRef = useRef<HTMLDivElement>(null);
  const loadButtonRef = useRef<HTMLButtonElement>(null);
  const loadingStatusRef = useRef<HTMLDivElement>(null);
  const retryButtonRef = useRef<HTMLButtonElement>(null);
  const imageButtonRef = useRef<HTMLButtonElement>(null);
  const focusTransferSourceRef = useRef<HTMLElement | null>(null);
  const nearViewport = useNearViewport(viewportRef);
  const [loadRequested, setLoadRequested] = useState(false);
  const [previewOpen, setPreviewOpen] = useState(false);
  const shouldLoad = nearViewport || loadRequested || previewOpen;
  const [src, setSrc] = useState("");
  const [failed, setFailed] = useState(false);
  const [loadAttempt, setLoadAttempt] = useState(0);

  useEffect(() => {
    // A manual off-screen load remains available to assistive-technology users
    // until the preview enters the viewport. From then on, the observer owns
    // unloading again so distant transcript images release their object URLs.
    if (nearViewport) setLoadRequested(false);
  }, [nearViewport]);

  useEffect(() => {
    if (!shouldLoad) {
      setSrc("");
      setFailed(false);
      return;
    }
    const controller = new AbortController();
    let active = true;
    let objectURL = "";
    setSrc("");
    setFailed(false);
    void getChatAttachmentContentBlob(attachment.session_id, attachment.id, controller.signal)
      .then((blob) => {
        if (!active) return;
        objectURL = createObjectURL(blob);
        if (!objectURL) {
          setFailed(true);
          return;
        }
        setSrc(objectURL);
      })
      .catch(() => {
        if (active && !controller.signal.aborted) setFailed(true);
      });
    return () => {
      active = false;
      controller.abort();
      if (objectURL) revokeObjectURL(objectURL);
    };
  }, [attachment.id, attachment.session_id, loadAttempt, shouldLoad]);

  useEffect(() => {
    const source = focusTransferSourceRef.current;
    if (!source) return;
    const active = document.activeElement;
    const sourceStillOwnsFocus = active === source;
    const sourceDisappearedIntoBody = !source.isConnected && active === document.body;
    if (!sourceStillOwnsFocus && !sourceDisappearedIntoBody) {
      focusTransferSourceRef.current = null;
      return;
    }
    const target = src
      ? imageButtonRef.current
      : failed
        ? retryButtonRef.current
        : !shouldLoad
          ? loadButtonRef.current
          : loadingStatusRef.current;
    if (!target) return;
    focusTransferSourceRef.current = target;
    if (active !== target) target.focus();
  }, [failed, shouldLoad, src]);

  function rememberFocusSource(node: HTMLElement) {
    focusTransferSourceRef.current = node;
  }

  function releaseFocusSource(event: FocusEvent<HTMLElement>) {
    const next = event.relatedTarget;
    if (next && viewportRef.current?.contains(next as Node)) return;
    // A null related target is also what browsers report when React replaces
    // the focused preview state. Leave the source in place; the effect above
    // transfers only when that source actually disconnected.
    if (next) focusTransferSourceRef.current = null;
  }

  function requestManualLoad(event: ReactMouseEvent<HTMLButtonElement>) {
    rememberFocusSource(event.currentTarget);
    setLoadRequested(true);
  }

  function retryManualLoad(event: ReactMouseEvent<HTMLButtonElement>) {
    rememberFocusSource(event.currentTarget);
    setFailed(false);
    setLoadAttempt((current) => current + 1);
  }

  function closePreview() {
    // Keep the opener mounted long enough for the dialog to restore focus when
    // its thumbnail moved outside the observer range while the preview was open.
    if (!nearViewport) setLoadRequested(true);
    setPreviewOpen(false);
  }

  let preview;
  if (!shouldLoad) {
    preview = (
      <div
        style={{
          ...STORED_IMAGE_PREVIEW_FRAME_STYLE,
          display: "grid",
          placeContent: "center",
          justifyItems: "center",
          gap: 6,
          padding: 8,
          color: "var(--t2)",
          fontSize: 11,
        }}
      >
        <span
          title={attachment.filename}
          style={{
            maxWidth: "100%",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {attachment.filename}
        </span>
        <button
          ref={loadButtonRef}
          type="button"
          className="btn btn-ghost btn-sm"
          aria-label={`Load ${attachment.filename}`}
          onClick={requestManualLoad}
          onFocus={(event) => rememberFocusSource(event.currentTarget)}
          onBlur={releaseFocusSource}
          style={{ padding: "2px 7px", fontSize: 10 }}
        >
          Load
        </button>
      </div>
    );
  } else if (!src && !failed) {
    preview = (
      <div
        ref={loadingStatusRef}
        role="status"
        aria-label={`Loading ${attachment.filename}`}
        tabIndex={-1}
        onFocus={(event) => rememberFocusSource(event.currentTarget)}
        onBlur={releaseFocusSource}
        style={STORED_IMAGE_PREVIEW_FRAME_STYLE}
      />
    );
  } else if (failed) {
    preview = (
      <div
        style={{
          ...STORED_IMAGE_PREVIEW_FRAME_STYLE,
          display: "flex",
          flexDirection: "column",
          justifyContent: "center",
          alignItems: "center",
          gap: 5,
          border: "1px solid var(--red-border)",
          color: "var(--t2)",
          fontSize: 11,
          padding: 7,
          textAlign: "center",
        }}
      >
        <span
          role="status"
          title={`Image unavailable: ${attachment.filename}`}
          style={{ maxWidth: "100%", overflow: "hidden", textOverflow: "ellipsis" }}
        >
          Image unavailable: {attachment.filename}
        </span>
        <button
          ref={retryButtonRef}
          type="button"
          className="btn btn-ghost btn-sm"
          onClick={retryManualLoad}
          onFocus={(event) => rememberFocusSource(event.currentTarget)}
          onBlur={releaseFocusSource}
          aria-label={`Retry ${attachment.filename}`}
        >
          Retry
        </button>
      </div>
    );
  } else {
    preview = (
      <button
        ref={imageButtonRef}
        type="button"
        aria-label={`Open ${attachment.filename}`}
        aria-haspopup="dialog"
        aria-expanded={previewOpen}
        title={`${attachment.filename} · ${formatBytes(attachment.size_bytes)}`}
        onClick={() => setPreviewOpen(true)}
        onFocus={(event) => rememberFocusSource(event.currentTarget)}
        onBlur={releaseFocusSource}
        style={{
          ...STORED_IMAGE_PREVIEW_FRAME_STYLE,
          display: "block",
          overflow: "hidden",
          padding: 0,
          color: "inherit",
          cursor: "zoom-in",
        }}
      >
        <img
          src={src}
          alt={attachment.filename}
          width={STORED_IMAGE_PREVIEW_WIDTH}
          height={STORED_IMAGE_PREVIEW_HEIGHT}
          loading="lazy"
          style={{
            display: "block",
            width: "100%",
            height: "100%",
            objectFit: "contain",
          }}
        />
      </button>
    );
  }

  return (
    <>
      <div
        ref={viewportRef}
        style={{
          width: STORED_IMAGE_PREVIEW_WIDTH,
          height: STORED_IMAGE_PREVIEW_HEIGHT,
          maxWidth: "100%",
        }}
      >
        {preview}
      </div>
      {previewOpen && src && (
        <Modal
          title={attachment.filename}
          ariaLabel={`Image preview: ${attachment.filename}`}
          width={960}
          returnFocusRef={imageButtonRef}
          onClose={closePreview}
          footer={
            <span
              style={{
                color: "var(--t3)",
                fontFamily: "var(--font-mono)",
                fontSize: 11,
              }}
            >
              {formatBytes(attachment.size_bytes)} · {attachment.media_type || "unknown type"}
            </span>
          }
        >
          <div
            style={{
              display: "grid",
              placeItems: "center",
              minHeight: 160,
              background: "var(--bg0)",
              borderRadius: "var(--radius-sm)",
              overflow: "hidden",
            }}
          >
            <img
              src={src}
              alt={attachment.filename}
              style={{
                display: "block",
                maxWidth: "100%",
                maxHeight: "calc(80dvh - 150px)",
                objectFit: "contain",
              }}
            />
          </div>
        </Modal>
      )}
    </>
  );
}

function useNearViewport(ref: React.RefObject<Element | null>): boolean {
  const [nearViewport, setNearViewport] = useState(
    () => typeof globalThis.IntersectionObserver !== "function",
  );

  useEffect(() => {
    const element = ref.current;
    if (!element || typeof globalThis.IntersectionObserver !== "function") {
      setNearViewport(true);
      return;
    }
    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries.find((candidate) => candidate.target === element) ?? entries[0];
        if (!entry) return;
        setNearViewport(entry.isIntersecting || entry.intersectionRatio > 0);
      },
      { rootMargin: "240px 0px" },
    );
    observer.observe(element);
    return () => observer.disconnect();
  }, [ref]);

  return nearViewport;
}

function useObjectURL(blob: Blob | null): string {
  const [src, setSrc] = useState("");
  useEffect(() => {
    if (!blob) {
      setSrc("");
      return;
    }
    const objectURL = createObjectURL(blob);
    setSrc(objectURL);
    return () => revokeObjectURL(objectURL);
  }, [blob]);
  return src;
}

function createObjectURL(blob: Blob): string {
  return typeof URL.createObjectURL === "function" ? URL.createObjectURL(blob) : "";
}

function revokeObjectURL(url: string) {
  if (url && typeof URL.revokeObjectURL === "function") URL.revokeObjectURL(url);
}

function isPreviewableImageMediaType(mediaType: string): boolean {
  return CHAT_IMAGE_MEDIA_TYPES.has(mediaType.trim().toLowerCase());
}

function pendingAttachmentID(): string {
  const randomID = globalThis.crypto?.randomUUID?.();
  return randomID ? `pending-file-${randomID}` : `pending-file-${Date.now()}-${Math.random()}`;
}

function attachmentDraftCountStatus(count: number, acceptance: ChatAttachmentAcceptance): string {
  const noun = acceptance === "files" ? "file" : "image";
  if (count === 0) return `No ${noun}s ready to attach.`;
  if (count === 1) return `1 ${noun} ready to attach.`;
  return `${count} ${noun}s ready to attach.`;
}

export const ChatImageAttachmentDrafts = ChatAttachmentDrafts;
export const ChatImageAttachmentGallery = ChatAttachmentGallery;

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}
