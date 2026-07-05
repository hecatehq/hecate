// Atoms: small, self-contained UI primitives.
//
// Badge / Dot / Toggle render status. CopyBtn / InlineError / CodeBlock
// are interaction primitives used across the console. Each is small
// enough that splitting one-per-file would just be noise; they live
// together because they share the "single low-level building block"
// shape.

import { useEffect, useRef, useState } from "react";
import type { KeyboardEvent } from "react";

import { Icon, Icons } from "./Icons";

// ─── Badge ───────────────────────────────────────────────────────────────────

type BadgeStatus =
  | "queued"
  | "running"
  | "awaiting"
  | "done"
  | "failed"
  | "enabled"
  | "disabled"
  | "healthy"
  | "degraded"
  | "down"
  | "ok"
  | "warn"
  | "error";
export function Badge({ status, label }: { status: BadgeStatus | string; label?: string }) {
  const map: Record<string, { cls: string; text: string }> = {
    queued: { text: label || "queued", cls: "badge-muted" },
    running: { text: label || "running", cls: "badge-teal" },
    awaiting: { text: label || "approval", cls: "badge-amber" },
    awaiting_approval: { text: label || "approval", cls: "badge-amber" },
    done: { text: label || "done", cls: "badge-green" },
    completed: { text: label || "done", cls: "badge-green" },
    failed: { text: label || "failed", cls: "badge-red" },
    cancelled: { text: label || "cancelled", cls: "badge-red" },
    enabled: { text: label || "enabled", cls: "badge-green" },
    disabled: { text: label || "disabled", cls: "badge-muted" },
    healthy: { text: label || "healthy", cls: "badge-green" },
    degraded: { text: label || "degraded", cls: "badge-amber" },
    down: { text: label || "down", cls: "badge-red" },
    ok: { text: label || "ok", cls: "badge-green" },
    warn: { text: label || "warn", cls: "badge-amber" },
    error: { text: label || "error", cls: "badge-red" },
  };
  const { text, cls } = map[status] ?? { text: label || status, cls: "badge-muted" };
  return <span className={`badge ${cls}`}>{text}</span>;
}

// ─── Dot ─────────────────────────────────────────────────────────────────────

export function Dot({
  color = "green",
  pulse = false,
}: {
  color?: "green" | "amber" | "red" | "muted";
  pulse?: boolean;
}) {
  const cls = { green: "dot-green", amber: "dot-amber", red: "dot-red", muted: "dot-muted" }[color];
  return (
    <span className={`dot ${cls}`} style={pulse ? { animation: "dot-pulse 2s infinite" } : {}} />
  );
}

// ─── Toggle ──────────────────────────────────────────────────────────────────

export function Toggle({
  on,
  onChange,
  label,
  ariaLabel,
}: {
  on: boolean;
  onChange: (v: boolean) => void;
  label?: string;
  ariaLabel?: string;
}) {
  const toggle = () => onChange(!on);
  const onKeyDown = (event: KeyboardEvent<HTMLSpanElement>) => {
    if (event.key !== "Enter" && event.key !== " ") return;
    event.preventDefault();
    event.stopPropagation();
    toggle();
  };
  return (
    <label className="toggle-wrap" onClick={toggle}>
      <span
        role="switch"
        aria-checked={on}
        aria-label={ariaLabel ?? label}
        tabIndex={0}
        onKeyDown={onKeyDown}
        className={`toggle ${on ? "on" : ""}`}
      />
      {label && <span style={{ fontSize: 12, color: "var(--t1)" }}>{label}</span>}
    </label>
  );
}

// ─── CopyBtn ─────────────────────────────────────────────────────────────────

export function CopyBtn({ label = "copy", text }: { label?: string; text: string }) {
  const [copied, setCopied] = useState(false);
  const resetTimerRef = useRef<number | null>(null);

  useEffect(() => {
    return () => {
      if (resetTimerRef.current !== null) window.clearTimeout(resetTimerRef.current);
    };
  }, []);

  const copy = () => {
    navigator.clipboard?.writeText(text).catch(() => {});
    if (resetTimerRef.current !== null) window.clearTimeout(resetTimerRef.current);
    setCopied(true);
    resetTimerRef.current = window.setTimeout(() => {
      setCopied(false);
      resetTimerRef.current = null;
    }, 1800);
  };
  return (
    <button className="btn btn-ghost btn-sm" onClick={copy} style={{ gap: 4, padding: "3px 6px" }}>
      <Icon d={copied ? Icons.check : Icons.copy} size={12} />
      {copied ? "copied" : label}
    </button>
  );
}

// ─── InlineError ─────────────────────────────────────────────────────────────

export function InlineError({ message }: { message: string }) {
  if (!message) return null;
  return (
    <div
      style={{
        display: "flex",
        alignItems: "flex-start",
        gap: 8,
        padding: "7px 10px",
        borderRadius: "var(--radius-sm)",
        background: "var(--red-bg)",
        border: "1px solid var(--red-border)",
        color: "var(--red)",
        fontSize: 12,
        fontFamily: "var(--font-mono)",
        lineHeight: 1.4,
      }}
    >
      <span style={{ flexShrink: 0, marginTop: 1 }}>✕</span>
      <span>{message}</span>
    </div>
  );
}

// ─── CodeBlock ───────────────────────────────────────────────────────────────

export function CodeBlock({ code, lang = "bash" }: { code: string; lang?: string }) {
  const [copied, setCopied] = useState(false);
  const resetTimerRef = useRef<number | null>(null);
  const normalizedLang = lang.trim().toLowerCase();
  const isDiff = normalizedLang === "diff" || normalizedLang === "patch";

  useEffect(() => {
    return () => {
      if (resetTimerRef.current !== null) window.clearTimeout(resetTimerRef.current);
    };
  }, []);

  const copy = () => {
    navigator.clipboard?.writeText(code).catch(() => {});
    if (resetTimerRef.current !== null) window.clearTimeout(resetTimerRef.current);
    setCopied(true);
    resetTimerRef.current = window.setTimeout(() => {
      setCopied(false);
      resetTimerRef.current = null;
    }, 2000);
  };
  return (
    <div className="code-block">
      <div className="code-block-header">
        <span className="code-lang">{lang}</span>
        <button className="code-copy-btn" onClick={copy}>
          <Icon d={copied ? Icons.check : Icons.copy} size={12} />
          {copied ? "copied" : "copy"}
        </button>
      </div>
      <pre className={`code-pre ${isDiff ? "code-pre-diff" : ""}`}>
        {isDiff ? <DiffCode code={code} /> : <code>{code}</code>}
      </pre>
    </div>
  );
}

function DiffCode({ code }: { code: string }) {
  const lines = code.split("\n");
  return (
    <code className="diff-code">
      {lines.map((line, index) => (
        <span key={index} className={`diff-line ${diffLineClass(line)}`}>
          {line || " "}
        </span>
      ))}
    </code>
  );
}

function diffLineClass(line: string): string {
  if (line.startsWith("+") && !line.startsWith("+++")) return "diff-line-add";
  if (line.startsWith("-") && !line.startsWith("---")) return "diff-line-remove";
  if (line.startsWith("@@")) return "diff-line-hunk";
  if (
    line.startsWith("diff --git") ||
    line.startsWith("index ") ||
    line.startsWith("+++ ") ||
    line.startsWith("--- ")
  ) {
    return "diff-line-meta";
  }
  return "diff-line-context";
}
