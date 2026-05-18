import type { CSSProperties, ReactNode } from "react";

import { Icon, Icons } from "../shared/ui";

export type ChatNoticeTone = "amber" | "red" | "teal";

export function ChatNoticeFrame({
  tone = "amber",
  role = "region",
  ariaLabel,
  testID,
  children,
  style,
}: {
  tone?: ChatNoticeTone;
  role?: "alert" | "region";
  ariaLabel?: string;
  testID?: string;
  children: ReactNode;
  style?: CSSProperties;
}) {
  const vars = noticeToneVars(tone);
  return (
    <div
      role={role}
      aria-label={ariaLabel}
      data-testid={testID}
      style={{
        margin: "10px 16px 0",
        border: `1px solid ${vars.border}`,
        borderRadius: "var(--radius)",
        background: vars.bg,
        color: vars.fg,
        overflow: "hidden",
        flexShrink: 0,
        ...style,
      }}
    >
      {children}
    </div>
  );
}

export function ChatNoticeHeader({
  tone = "amber",
  title,
  icon = Icons.warning,
  action,
}: {
  tone?: ChatNoticeTone;
  title: ReactNode;
  icon?: string;
  action?: ReactNode;
}) {
  const vars = noticeToneVars(tone);
  return (
    <div
      style={{
        padding: "8px 12px",
        borderBottom: `1px solid ${vars.border}`,
        display: "flex",
        alignItems: "center",
        gap: 8,
      }}
    >
      <Icon d={icon} size={14} />
      <span style={{ fontWeight: 500, color: vars.fg, fontSize: 12 }}>{title}</span>
      {action}
    </div>
  );
}

export function ChatNoticeRow({
  tone = "amber",
  children,
  style,
}: {
  tone?: ChatNoticeTone;
  children: ReactNode;
  style?: CSSProperties;
}) {
  const vars = noticeToneVars(tone);
  return (
    <div
      style={{
        padding: "8px 12px",
        borderTop: `1px solid ${vars.border}`,
        ...style,
      }}
    >
      {children}
    </div>
  );
}

export function ChatNoticeInline({
  tone = "amber",
  title,
  message,
  action,
  actionBusyLabel = "Working...",
  onAction,
  actionBusy = false,
  actionDisabled = false,
  actionTitle,
}: {
  tone?: ChatNoticeTone;
  title: string;
  message: string;
  action: string;
  actionBusyLabel?: string;
  onAction: () => void;
  actionBusy?: boolean;
  actionDisabled?: boolean;
  actionTitle?: string;
}) {
  const vars = noticeToneVars(tone);
  return (
    <div
      style={{
        maxWidth: 820,
        margin: "0 auto 8px",
        border: `1px solid ${vars.softBorder}`,
        borderRadius: "var(--radius-sm)",
        background: vars.softBg,
        padding: "8px 10px",
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        gap: 12,
        fontSize: 12,
        color: "var(--t2)",
        lineHeight: 1.45,
      }}
    >
      <span style={{ minWidth: 0 }}>
        <strong style={{ color: vars.fg, marginRight: 6 }}>{title}.</strong>
        {message}
      </span>
      <button
        type="button"
        className="btn btn-ghost btn-sm"
        onClick={onAction}
        disabled={actionDisabled}
        title={actionTitle}
        style={{ flexShrink: 0, color: vars.fg, borderColor: vars.softBorder }}
      >
        {actionBusy ? actionBusyLabel : action}
      </button>
    </div>
  );
}

function noticeToneVars(tone: ChatNoticeTone) {
  switch (tone) {
    case "red":
      return {
        bg: "var(--red-bg)",
        fg: "var(--red)",
        lo: "var(--red-lo)",
        border: "var(--red-border)",
        softBg: "rgba(255, 95, 95, 0.06)",
        softBorder: "rgba(255, 95, 95, 0.32)",
      };
    case "teal":
      return {
        bg: "var(--teal-bg)",
        fg: "var(--teal)",
        lo: "var(--teal-lo)",
        border: "var(--teal-border)",
        softBg: "rgba(0, 194, 174, 0.06)",
        softBorder: "rgba(0, 194, 174, 0.32)",
      };
    case "amber":
    default:
      return {
        bg: "var(--amber-bg)",
        fg: "var(--amber)",
        lo: "var(--amber-lo)",
        border: "var(--amber-border)",
        softBg: "rgba(245, 191, 79, 0.055)",
        softBorder: "rgba(245, 191, 79, 0.28)",
      };
  }
}
