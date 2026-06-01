import { useEffect, useRef, useState } from "react";

import { Icon, Icons } from "./Icons";

function compactID(text: string): string {
  if (text.length <= 18) return text;
  return `${text.slice(0, 10)}…${text.slice(-6)}`;
}

// CopyableID keeps debug identifiers useful without letting long
// machine ids dominate metadata grids and tables. Full value stays in
// the tooltip and clipboard; the visible label stays compact.
export function CopyableID({ text, compact = false }: { text: string; compact?: boolean }) {
  const [copied, setCopied] = useState(false);
  const resetTimerRef = useRef<number | null>(null);
  const label = compact ? compactID(text) : text;

  useEffect(() => {
    return () => {
      if (resetTimerRef.current !== null) {
        window.clearTimeout(resetTimerRef.current);
      }
    };
  }, []);

  function markCopied() {
    if (resetTimerRef.current !== null) {
      window.clearTimeout(resetTimerRef.current);
    }
    setCopied(true);
    resetTimerRef.current = window.setTimeout(() => {
      setCopied(false);
      resetTimerRef.current = null;
    }, 1500);
  }

  return (
    <button
      type="button"
      onClick={async (e) => {
        e.stopPropagation();
        if (!navigator.clipboard?.writeText) return;
        try {
          await navigator.clipboard.writeText(text);
          markCopied();
        } catch {
          // Keep the icon honest: no success state unless the browser confirms the write.
        }
      }}
      title={text}
      aria-label={`Copy ${text}`}
      style={{
        background: "none",
        border: "none",
        padding: 0,
        cursor: "pointer",
        fontFamily: "var(--font-mono)",
        fontSize: 11,
        color: copied ? "var(--green)" : "var(--teal)",
        display: "inline-flex",
        alignItems: "center",
        gap: 4,
        maxWidth: "100%",
        minWidth: 0,
      }}
    >
      <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
        {label}
      </span>
      <Icon d={copied ? Icons.check : Icons.copy} size={11} />
    </button>
  );
}
