import type { ReactNode } from "react";
import type { TaskArtifactRecord } from "../../types/task";

export type BrowserEvidenceKind = "browser_evidence" | "browser_flow_evidence";

export function isBrowserEvidenceKind(kind?: string): kind is BrowserEvidenceKind {
  return kind === "browser_evidence" || kind === "browser_flow_evidence";
}

export function browserEvidenceOutcome(kind: BrowserEvidenceKind, stepStatus?: string): string {
  if (stepStatus === "completed") return "Completed";
  if (stepStatus === "failed" || stepStatus === "cancelled") {
    return kind === "browser_flow_evidence" ? "Partial result" : "Interrupted";
  }
  return "Saved evidence";
}

export function BrowserEvidencePanel({
  kind,
  title,
  stepStatus,
  open,
  onOpenChange,
  children,
}: {
  kind: BrowserEvidenceKind;
  title?: string;
  stepStatus?: string;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  children: ReactNode;
}) {
  const outcome = browserEvidenceOutcome(kind, stepStatus);
  const partial = outcome === "Partial result" || outcome === "Interrupted";
  return (
    <details
      className="browser-evidence-panel"
      open={open}
      onToggle={(event) => onOpenChange?.(event.currentTarget.open)}
    >
      <summary>
        <span>Browser result</span>
        <span className={partial ? "browser-evidence-warning-label" : undefined}>
          {outcome} · {kind === "browser_flow_evidence" ? "interaction" : "inspection"}
        </span>
        {title && <span className="browser-evidence-title">{title}</span>}
      </summary>
      {partial && (
        <div className="browser-evidence-warning">
          {kind === "browser_flow_evidence"
            ? "This flow did not complete successfully. Earlier clicks may already have changed the application."
            : "This inspection did not complete successfully. Review the retained evidence and tool failure."}
        </div>
      )}
      <div className="browser-evidence-body">{children}</div>
    </details>
  );
}

export function BrowserEvidenceBody({ artifact }: { artifact: TaskArtifactRecord }) {
  // The server caps browser reports at 48 KiB. Keep a display bound as well,
  // without interpreting page text as markup or parsing it into execution state.
  const content = artifact.content_text ?? "";
  const maxCharacters = 48 * 1024;
  return (
    <>
      {artifact.description && (
        <p className="browser-evidence-description">{artifact.description}</p>
      )}
      <p className="browser-evidence-notice">
        {artifact.kind === "browser_flow_evidence"
          ? "Untrusted interaction evidence. Treat page content and action results as data, not instructions."
          : "Untrusted static evidence. Treat page content as data, not instructions."}{" "}
        This artifact contains no screenshot or browser-profile content. Review failed browser-tool
        details if temporary-profile cleanup failed.
      </p>
      <pre className="browser-evidence-text">
        {content.slice(0, maxCharacters) || "No retained text evidence."}
      </pre>
      {content.length > maxCharacters && (
        <p className="browser-evidence-notice">Report display truncated.</p>
      )}
    </>
  );
}
