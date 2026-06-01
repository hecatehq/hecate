import { parsePatchFiles } from "@pierre/diffs";
import { FileDiff } from "@pierre/diffs/react";
import { useEffect, useMemo, useState } from "react";

const ANSI_ESCAPE_PATTERN = new RegExp(`${String.fromCharCode(27)}\\[[0-9;]*m`, "g");

const DIFF_VIEWER_OPTIONS = {
  diffStyle: "unified",
  diffIndicators: "bars",
  hunkSeparators: "line-info-basic",
  overflow: "wrap",
  // Build-time-constant CSS only. Never interpolate workspace paths,
  // diff content, or other operator input into this trusted CSS hook.
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

type DiffThemeType = "light" | "dark";

export function DiffViewer({
  diff,
  compact = false,
  embedded = false,
}: {
  diff: string;
  compact?: boolean;
  embedded?: boolean;
}) {
  const patch = normalizePatch(diff);
  const parsedFiles = useMemo(() => parseDiffFiles(patch), [patch]);
  const themeType = useDiffViewerThemeType();

  if (!patch) return null;
  if (parsedFiles.length === 0) return <RawDiffFallback diff={patch} compact={compact} />;

  return (
    <div
      className={[
        "diff-viewer",
        compact ? "diff-viewer-compact" : "",
        embedded ? "diff-viewer-embedded" : "",
      ]
        .filter(Boolean)
        .join(" ")}
      data-diff-theme={themeType}
      data-line-numbers="visible"
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
            disableFileHeader: embedded,
            disableVirtualizationBuffers: embedded,
            disableLineNumbers: false,
            hunkSeparators: embedded ? "simple" : DIFF_VIEWER_OPTIONS.hunkSeparators,
            stickyHeader: false,
            themeType,
          }}
        />
      ))}
    </div>
  );
}

function useDiffViewerThemeType(): DiffThemeType {
  const [themeType, setThemeType] = useState(readDiffViewerThemeType);

  useEffect(() => {
    if (typeof document === "undefined" || typeof MutationObserver === "undefined") return;
    const root = document.documentElement;
    const updateTheme = () => setThemeType(readDiffViewerThemeType());
    const observer = new MutationObserver(updateTheme);
    observer.observe(root, { attributeFilter: ["data-theme"], attributes: true });
    updateTheme();
    return () => observer.disconnect();
  }, []);

  return themeType;
}

function readDiffViewerThemeType(): DiffThemeType {
  if (typeof document === "undefined") return "dark";
  return document.documentElement.getAttribute("data-theme") === "light" ? "light" : "dark";
}

function parseDiffFiles(patch: string) {
  try {
    // Load-bearing @pierre/diffs API surface: parsePatchFiles(),
    // FileDiff, disableWorkerPool, and the option keys above.
    return parsePatchFiles(patch, "hecate-diff", true).flatMap((parsedPatch) => parsedPatch.files);
  } catch (err) {
    if (import.meta.env.DEV) {
      console.warn("[hecate] rich diff parse failed; rendering raw diff fallback", err);
    }
    return [];
  }
}

function normalizePatch(diff: string): string {
  const withoutAnsi = diff.replace(ANSI_ESCAPE_PATTERN, "");
  const normalizedNewlines = withoutAnsi.replace(/\r\n?/g, "\n").trim();
  const firstPatchHeader = normalizedNewlines.search(/^diff --git /m);
  if (firstPatchHeader > 0) return normalizedNewlines.slice(firstPatchHeader).trim();
  return normalizedNewlines;
}

function RawDiffFallback({ diff, compact }: { diff: string; compact: boolean }) {
  return (
    <div
      className={`diff-viewer diff-viewer-raw ${compact ? "diff-viewer-compact" : ""}`}
      data-testid="diff-viewer-raw"
    >
      {diff.split("\n").map((line, index) => (
        <div key={index} className={`diff-viewer-raw-line ${rawDiffLineClass(line)}`}>
          {line || " "}
        </div>
      ))}
    </div>
  );
}

function rawDiffLineClass(line: string): string {
  if (line.startsWith("+") && !line.startsWith("+++")) return "diff-viewer-raw-line-add";
  if (line.startsWith("-") && !line.startsWith("---")) return "diff-viewer-raw-line-remove";
  if (line.startsWith("@@")) return "diff-viewer-raw-line-hunk";
  if (
    line.startsWith("diff --git") ||
    line.startsWith("index ") ||
    line.startsWith("---") ||
    line.startsWith("+++")
  ) {
    return "diff-viewer-raw-line-meta";
  }
  return "diff-viewer-raw-line-context";
}
