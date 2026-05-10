// CopyableID renders the truncated request-ID + a copy-to-clipboard
// button. Used in the recent-traces table cell and in the trace
// drawer header. Self-contained: owns its own "copied" timeout state
// so a click in one place doesn't flash the icon elsewhere.

import { useState } from "react";

import { Icon, Icons } from "../../shared/ui";

export function CopyableID({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      onClick={e => {
        e.stopPropagation();
        navigator.clipboard?.writeText(text).catch(() => {});
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1500);
      }}
      title={text}
      style={{
        background: "none", border: "none", padding: 0, cursor: "pointer",
        fontFamily: "var(--font-mono)", fontSize: 11,
        color: copied ? "var(--green)" : "var(--teal)",
        display: "inline-flex", alignItems: "center", gap: 4,
        overflow: "hidden", textOverflow: "ellipsis", maxWidth: "100%",
      }}>
      <span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>{text.slice(0, 8)}…</span>
      <Icon d={copied ? Icons.check : Icons.copy} size={11} />
    </button>
  );
}
