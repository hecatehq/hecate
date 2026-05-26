import { useEffect, useState } from "react";
import type { KeyboardEvent, ReactNode } from "react";
import { CursorIcon, VisualStudioCode, Xcode } from "@dev.icons/react";
import { Apple, Terminal } from "@dev.icons/react/mono";

import {
  canOpenWorkspaceFromUI,
  openWorkspaceTarget,
  workspaceOpenTargets,
  type WorkspaceOpenTarget,
} from "../../lib/workspace-open";
import { Icon, Icons } from "../shared/ui";
import { focusDropdownItem, focusInitialDropdownItem } from "../shared/dropdownKeyboard";
import { useFloatingDropdownStyle } from "../shared/useFloatingDropdownStyle";
import { useFloatingMenu } from "../shared/useFloatingMenu";

const WORKSPACE_OPEN_DEFAULT_KEY = "hecate.workspaceOpen.defaultTarget";

export function WorkspaceOpenMenu({ workspacePath }: { workspacePath: string }) {
  const [error, setError] = useState("");
  const [defaultTargetID, setDefaultTargetID] = useState(() => readDefaultWorkspaceOpenTarget());
  const {
    open,
    setOpen,
    toggle,
    close,
    wrapRef: ref,
    triggerRef,
    menuRef,
  } = useFloatingMenu<HTMLDivElement, HTMLButtonElement>({
    onClose: () => setError(""),
  });
  const floatingStyle = useFloatingDropdownStyle(triggerRef, open, "right");
  const workspace = workspacePath.trim();
  const allTargets = workspaceOpenTargets();
  const defaultTarget = allTargets.find((target) => target.id === defaultTargetID) ?? allTargets[0];
  const targets = defaultTarget
    ? [defaultTarget, ...allTargets.filter((target) => target.id !== defaultTarget.id)]
    : allTargets;

  useEffect(() => {
    if (!open) return;
    const frame = requestAnimationFrame(() => {
      focusInitialDropdownItem(menuRef.current);
    });
    return () => cancelAnimationFrame(frame);
  }, [open, menuRef]);

  if (!workspace || !canOpenWorkspaceFromUI()) return null;

  async function openDefaultTarget() {
    if (!defaultTarget) return;
    await openTarget(defaultTarget, { openMenuOnError: true });
  }

  function onMenuKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key === "Escape") {
      event.preventDefault();
      close();
      triggerRef.current?.focus();
      return;
    }
    if (
      event.key === "ArrowDown" ||
      event.key === "ArrowUp" ||
      event.key === "Home" ||
      event.key === "End"
    ) {
      event.preventDefault();
      focusDropdownItem(menuRef.current, event.key);
    }
  }

  async function openTarget(
    target: WorkspaceOpenTarget,
    options: { openMenuOnError?: boolean } = {},
  ) {
    setError("");
    try {
      await openWorkspaceTarget(workspace, target.id);
      rememberDefaultWorkspaceOpenTarget(target.id);
      setDefaultTargetID(target.id);
      setOpen(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : `Could not open ${target.label}.`);
      if (options.openMenuOnError) setOpen(true);
    }
  }

  return (
    <div className="dropdown-wrap" ref={ref} style={{ flexShrink: 0 }}>
      <div
        className="workspace-open-trigger"
        style={{
          alignItems: "center",
          display: "flex",
          overflow: "hidden",
        }}
      >
        <button
          className="btn btn-ghost btn-sm chat-header-action workspace-open-trigger-main"
          type="button"
          aria-label={`Open workspace in ${defaultTarget?.label ?? "default app"}`}
          onClick={() => void openDefaultTarget()}
          title={
            defaultTarget
              ? `Open workspace in ${defaultTarget.label}: ${workspace}`
              : `Open workspace: ${workspace}`
          }
          style={{
            borderBottomRightRadius: 0,
            borderTopRightRadius: 0,
          }}
        >
          {defaultTarget ? (
            <WorkspaceOpenTargetIcon compact target={defaultTarget} />
          ) : (
            <Icon d={Icons.open} size={14} />
          )}
        </button>
        <button
          ref={triggerRef}
          className="btn btn-ghost btn-sm chat-header-action workspace-open-trigger-chevron"
          type="button"
          aria-label="Choose workspace opener"
          aria-expanded={open}
          aria-haspopup="menu"
          onClick={toggle}
          title="Choose workspace opener"
          style={{
            color: open ? "var(--teal)" : "var(--t2)",
            borderBottomLeftRadius: 0,
            borderTopLeftRadius: 0,
            background: open ? "var(--teal-bg)" : "transparent",
          }}
        >
          <Icon d={Icons.chevD} size={10} />
        </button>
      </div>
      {open && floatingStyle && (
        <div
          ref={menuRef}
          role="menu"
          className="dropdown-menu dropdown-menu-floating workspace-open-menu"
          onKeyDown={onMenuKeyDown}
          style={{ ...floatingStyle, minWidth: 220, width: 220 }}
        >
          {targets.map((target) => (
            <button
              key={target.id}
              type="button"
              data-dropdown-item
              role="menuitem"
              className="dropdown-item workspace-open-item"
              onClick={() => void openTarget(target)}
              title={`Open in ${target.label}`}
            >
              <WorkspaceOpenTargetIcon target={target} />
              <span className="workspace-open-label">{target.label}</span>
            </button>
          ))}
          {error && (
            <div
              role="alert"
              style={{
                borderTop: "1px solid var(--border)",
                color: "var(--red)",
                fontFamily: "var(--font-mono)",
                fontSize: 12,
                lineHeight: 1.35,
                padding: "10px 14px",
              }}
            >
              {error}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function readDefaultWorkspaceOpenTarget(): string {
  try {
    return localStorage.getItem(WORKSPACE_OPEN_DEFAULT_KEY) ?? "";
  } catch {
    return "";
  }
}

function rememberDefaultWorkspaceOpenTarget(target: WorkspaceOpenTarget["id"]) {
  try {
    localStorage.setItem(WORKSPACE_OPEN_DEFAULT_KEY, target);
  } catch {
    // Best-effort preference only.
  }
}

function WorkspaceOpenTargetIcon({
  compact = false,
  target,
}: {
  compact?: boolean;
  target: WorkspaceOpenTarget;
}) {
  const size = compact ? 22 : 20;
  const icon = workspaceOpenTargetIcon(target, compact ? 15 : 14);
  return (
    <span
      aria-hidden="true"
      data-testid={`workspace-open-icon-${target.id}`}
      style={{
        alignItems: "center",
        background: icon.background,
        border: icon.border ?? "1px solid var(--border)",
        borderRadius: 7,
        color: icon.color,
        display: "inline-flex",
        flexShrink: 0,
        height: size,
        justifyContent: "center",
        overflow: "hidden",
        width: size,
      }}
    >
      {icon.node}
    </span>
  );
}

function workspaceOpenTargetIcon(
  target: WorkspaceOpenTarget,
  glyphSize: number,
): {
  background: string;
  border?: string;
  color: string;
  node: ReactNode;
} {
  const deviconProps = { height: glyphSize, width: glyphSize, style: { display: "block" } };
  const invertedDeviconProps = {
    ...deviconProps,
    style: { ...deviconProps.style, filter: "var(--mono-icon-filter)" },
  };
  switch (target.kind) {
    case "folder":
      return {
        background: "var(--bg0)",
        border: "1px solid color-mix(in oklch, var(--border) 70%, transparent)",
        color: "var(--t1)",
        node: <Apple {...deviconProps} />,
      };
  }
  switch (target.id) {
    case "vscode":
      return {
        background: "var(--bg0)",
        border: "1px solid color-mix(in oklch, var(--border) 70%, transparent)",
        color: "inherit",
        node: <VisualStudioCode {...deviconProps} />,
      };
    case "vscode_insiders":
      return {
        background: "color-mix(in oklch, var(--teal-bg) 72%, var(--bg0))",
        border: "1px solid color-mix(in oklch, var(--teal-border) 45%, var(--border))",
        color: "inherit",
        node: <VisualStudioCode {...deviconProps} />,
      };
    case "cursor":
      return {
        background: "var(--bg0)",
        border: "1px solid color-mix(in oklch, var(--border) 70%, transparent)",
        color: "inherit",
        node: <CursorIcon {...deviconProps} />,
      };
    case "xcode":
      return {
        background: "var(--bg0)",
        border: "1px solid color-mix(in oklch, var(--border) 70%, transparent)",
        color: "inherit",
        node: <Xcode {...deviconProps} />,
      };
    case "terminal":
      return {
        background: "var(--mono-icon)",
        border: "1px solid color-mix(in oklch, var(--border2) 70%, transparent)",
        color: "var(--bg0)",
        node: <Terminal {...invertedDeviconProps} />,
      };
    case "iterm2":
      return {
        background: "var(--mono-icon)",
        border: "1px solid color-mix(in oklch, var(--border2) 70%, transparent)",
        color: "var(--bg0)",
        node: <Terminal {...invertedDeviconProps} />,
      };
    case "zed":
      return {
        background: "var(--bg0)",
        color: "var(--t1)",
        node: (
          <span
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: Math.max(13, glyphSize - 4),
              fontWeight: 650,
              lineHeight: 1,
            }}
          >
            Z
          </span>
        ),
      };
    default:
      return {
        background: "var(--bg0)",
        color: "var(--t1)",
        node: <Icon d={Icons.open} size={13} />,
      };
  }
}
