import { CodeBlock } from "../shared/Atoms";
import { DiffViewer } from "../shared/DiffViewer";
import { MarkdownContent, type MarkdownHeadingStartLevel } from "../shared/MarkdownContent";

export function TranscriptMarkdown({
  content,
  headingStartLevel,
}: {
  content: string;
  headingStartLevel?: MarkdownHeadingStartLevel;
}) {
  return (
    <MarkdownContent
      content={content}
      headingStartLevel={headingStartLevel}
      renderCodeBlock={(code, language) =>
        isDiffFence(language) ? (
          <DiffViewer compact embedded diff={code} />
        ) : (
          <CodeBlock code={code} lang={language ?? ""} />
        )
      }
    />
  );
}

function isDiffFence(lang: string | undefined): boolean {
  const normalized = (lang ?? "").trim().toLowerCase();
  return normalized === "diff" || normalized === "patch";
}
