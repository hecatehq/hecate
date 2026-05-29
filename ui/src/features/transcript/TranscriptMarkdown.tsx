import { useMemo } from "react";
import type React from "react";

import { parseInlineNodes, parseMarkdownBlocks } from "../../lib/markdown";
import { CodeBlock } from "../shared/Atoms";
import { DiffViewer } from "../shared/DiffViewer";

export function TranscriptMarkdown({ content }: { content: string }) {
  // Re-parsing markdown is the per-row hot cost on a streamed transcript.
  // Memoizing on `content` means a row only re-parses when its text
  // actually changes — not on hover, copy, or parent re-renders that
  // leave the content identical.
  const blocks = useMemo(() => parseMarkdownBlocks(content), [content]);
  return (
    <div style={{ fontSize: 13, color: "var(--t0)", lineHeight: 1.7 }}>
      {blocks.map((block, i) => {
        if (block.type === "code") {
          if (isDiffFence(block.lang)) {
            return <DiffViewer key={i} compact embedded diff={block.text} />;
          }
          return <CodeBlock key={i} code={block.text} lang={block.lang ?? ""} />;
        }
        if (block.type === "heading") {
          const sizes: Record<number, string> = { 1: "16px", 2: "14px", 3: "13px" };
          return (
            <div
              key={i}
              style={{
                fontWeight: 600,
                fontSize: sizes[block.level ?? 1] ?? "13px",
                margin: "10px 0 4px",
                color: "var(--t0)",
              }}
            >
              {renderInline(block.text)}
            </div>
          );
        }
        if (block.type === "ul") {
          return (
            <ul key={i} style={{ margin: "4px 0 4px 20px", padding: 0 }}>
              {block.items!.map((item, j) => (
                <li key={j} style={{ marginBottom: 2 }}>
                  {renderInline(item)}
                </li>
              ))}
            </ul>
          );
        }
        if (block.type === "ol") {
          return (
            <ol key={i} style={{ margin: "4px 0 4px 20px", padding: 0 }}>
              {block.items!.map((item, j) => (
                <li key={j} style={{ marginBottom: 2 }}>
                  {renderInline(item)}
                </li>
              ))}
            </ol>
          );
        }
        if (block.type === "task") {
          return (
            <ul
              key={i}
              style={{ display: "grid", gap: 4, listStyle: "none", margin: "6px 0", padding: 0 }}
            >
              {block.tasks!.map((task, j) => (
                <li key={j} style={{ alignItems: "flex-start", display: "flex", gap: 8 }}>
                  <span
                    aria-label={task.checked ? "Completed task" : "Incomplete task"}
                    role="img"
                    style={{
                      alignItems: "center",
                      background: task.checked ? "var(--teal-soft)" : "var(--bg3)",
                      border: `1px solid ${task.checked ? "var(--teal-border)" : "var(--border)"}`,
                      borderRadius: 4,
                      color: task.checked ? "var(--teal)" : "transparent",
                      display: "inline-flex",
                      flex: "0 0 auto",
                      fontSize: 10,
                      height: 14,
                      justifyContent: "center",
                      marginTop: 4,
                      width: 14,
                    }}
                  >
                    x
                  </span>
                  <span>{renderInline(task.text)}</span>
                </li>
              ))}
            </ul>
          );
        }
        if (block.type === "table") {
          return (
            <div
              key={i}
              style={{
                border: "1px solid var(--border)",
                borderRadius: "var(--radius-sm)",
                margin: "8px 0",
                overflowX: "auto",
              }}
            >
              <table style={{ borderCollapse: "collapse", minWidth: "100%", fontSize: 12 }}>
                <thead>
                  <tr>
                    {block.table!.headers.map((header, j) => (
                      <th
                        key={j}
                        style={{
                          background: "var(--bg3)",
                          borderBottom: "1px solid var(--border)",
                          color: "var(--t1)",
                          fontWeight: 600,
                          padding: "6px 8px",
                          textAlign: "left",
                          whiteSpace: "nowrap",
                        }}
                      >
                        {renderInline(header)}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {block.table!.rows.map((row, j) => (
                    <tr key={j}>
                      {block.table!.headers.map((_, k) => (
                        <td
                          key={k}
                          style={{
                            borderTop: j === 0 ? "none" : "1px solid var(--border)",
                            color: "var(--t0)",
                            padding: "6px 8px",
                            verticalAlign: "top",
                          }}
                        >
                          {renderInline(row[k] ?? "")}
                        </td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          );
        }
        if (block.type === "hr") {
          return (
            <hr
              key={i}
              style={{ border: "none", borderTop: "1px solid var(--border)", margin: "10px 0" }}
            />
          );
        }
        return (
          <p key={i} style={{ margin: "0 0 6px", whiteSpace: "pre-wrap" }}>
            {renderInline(block.text)}
          </p>
        );
      })}
    </div>
  );
}

function isDiffFence(lang: string | undefined): boolean {
  const normalized = (lang ?? "").trim().toLowerCase();
  return normalized === "diff" || normalized === "patch";
}

function renderInline(text: string): React.ReactNode {
  return parseInlineNodes(text).map((node, i) => {
    if (node.t === "bold") return <strong key={i}>{renderInline(node.v)}</strong>;
    if (node.t === "italic") return <em key={i}>{renderInline(node.v)}</em>;
    if (node.t === "code")
      return (
        <code
          key={i}
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: "0.9em",
            background: "var(--bg3)",
            padding: "1px 4px",
            margin: "0 1px",
            borderRadius: "var(--radius-sm)",
            color: "var(--teal)",
          }}
        >
          {node.v}
        </code>
      );
    if (node.t === "link") {
      return (
        <a
          key={i}
          href={safeMarkdownHref(node.href)}
          rel="noreferrer"
          target="_blank"
          style={{
            color: "var(--teal)",
            textDecoration: "none",
            borderBottom: "1px solid var(--teal-border)",
          }}
        >
          {node.v}
        </a>
      );
    }
    return node.v;
  });
}

function safeMarkdownHref(href: string): string {
  if (/^https?:\/\//i.test(href) || /^mailto:/i.test(href)) {
    return href;
  }
  return "#";
}
