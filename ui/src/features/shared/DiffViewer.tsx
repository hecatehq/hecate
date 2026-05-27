import { parsePatchFiles } from "@pierre/diffs";
import { FileDiff } from "@pierre/diffs/react";
import { useMemo } from "react";

import { CodeBlock } from "./Atoms";

const DIFF_VIEWER_OPTIONS = {
  diffStyle: "unified",
  diffIndicators: "bars",
  hunkSeparators: "line-info-basic",
  overflow: "wrap",
  themeType: "system",
  unsafeCSS: `
    [data-diffs-header] {
      background: var(--diffs-header-bg);
      border-bottom: 1px solid var(--diffs-border);
      min-height: 32px;
      padding: 6px 10px;
    }
    [data-file-info] {
      border-color: var(--diffs-border);
    }
    [data-line] {
      min-height: 18px;
    }
    [data-separator] {
      background: var(--diffs-separator-bg);
      border-block: 1px solid var(--diffs-border);
      color: var(--diffs-muted);
    }
  `,
} as const;

export function DiffViewer({ diff, compact = false }: { diff: string; compact?: boolean }) {
  const patch = diff.trim();
  const parsedFiles = useMemo(() => parseDiffFiles(patch), [patch]);

  if (!patch) return null;
  if (parsedFiles.length === 0) return <CodeBlock code={diff} lang="diff" />;

  return (
    <div
      className={`diff-viewer ${compact ? "diff-viewer-compact" : ""}`}
      data-testid="diff-viewer"
    >
      {parsedFiles.map((file, index) => (
        <FileDiff
          key={`${file.name}:${file.prevName ?? ""}:${index}`}
          fileDiff={file}
          disableWorkerPool
          className="diff-viewer-file"
          options={{
            ...DIFF_VIEWER_OPTIONS,
            disableLineNumbers: compact,
          }}
        />
      ))}
    </div>
  );
}

function parseDiffFiles(patch: string) {
  try {
    return parsePatchFiles(patch, "hecate-diff", true).flatMap((parsedPatch) => parsedPatch.files);
  } catch {
    return [];
  }
}
