import { forwardRef, useState, type CSSProperties, type ReactNode } from "react";

import { isPlainNavigationClick } from "../../app/navigation";

type StyledChildrenProps = {
  children: ReactNode;
  className?: string;
  style?: CSSProperties;
};

function classNames(...values: Array<string | undefined>): string {
  return values.filter(Boolean).join(" ");
}

export function MasterDetailWorkspace({ children, className, style }: StyledChildrenProps) {
  return (
    <div
      className={classNames("entity-workspace", className)}
      style={{
        display: "flex",
        height: "100%",
        overflow: "hidden",
        position: "relative",
        ...style,
      }}
    >
      {children}
    </div>
  );
}

export function EntityIndexPanel({
  "aria-label": ariaLabel,
  children,
  className,
  style,
}: StyledChildrenProps & { "aria-label": string }) {
  return (
    <aside
      aria-label={ariaLabel}
      className={classNames("entity-index-panel", className)}
      style={{
        width: "var(--entity-index-width, 220px)",
        borderRight: "var(--entity-index-border-right, 1px solid var(--border))",
        borderBottom: "var(--entity-index-border-bottom, 0)",
        display: "flex",
        flexDirection: "column",
        flexShrink: 0,
        maxHeight: "var(--entity-index-max-height, none)",
        background: "var(--bg1)",
        overflow: "hidden",
        ...style,
      }}
    >
      {children}
    </aside>
  );
}

export function EntityIndexHeader({ children, className, style }: StyledChildrenProps) {
  return (
    <div
      className={classNames("entity-index-header", className)}
      style={{ borderBottom: "1px solid var(--border)", flexShrink: 0, ...style }}
    >
      {children}
    </div>
  );
}

export const EntityIndexHeading = forwardRef<
  HTMLHeadingElement,
  StyledChildrenProps & { tabIndex?: number }
>(function EntityIndexHeading({ children, className, style, tabIndex }, ref) {
  return (
    <h2
      ref={ref}
      className={classNames("entity-index-heading", className)}
      tabIndex={tabIndex}
      style={{
        margin: 0,
        padding: "8px 12px 4px",
        fontFamily: "var(--font-mono)",
        fontSize: 10,
        fontWeight: 400,
        letterSpacing: "0.08em",
        lineHeight: 1.5,
        textTransform: "uppercase",
        color: "var(--t3)",
        ...style,
      }}
    >
      {children}
    </h2>
  );
});

export function EntityIndexList({ children, className, style }: StyledChildrenProps) {
  return (
    <div
      className={classNames("entity-index-list", className)}
      style={{ flex: 1, minHeight: 0, overflowY: "auto", ...style }}
    >
      {children}
    </div>
  );
}

export function EntityIndexGroupLabel({ children, className, style }: StyledChildrenProps) {
  return (
    <div
      className={classNames("entity-index-group-label", className)}
      style={{
        padding: "8px 12px 3px",
        fontFamily: "var(--font-mono)",
        fontSize: 9,
        letterSpacing: "0.08em",
        textTransform: "uppercase",
        color: "var(--t3)",
        ...style,
      }}
    >
      {children}
    </div>
  );
}

export function EntityIndexState({
  busy = false,
  children,
  className,
  style,
}: StyledChildrenProps & { busy?: boolean }) {
  return (
    <div
      aria-busy={busy || undefined}
      className={classNames("entity-index-state", className)}
      role={busy ? "status" : undefined}
      style={{
        padding: "24px 12px",
        textAlign: "center",
        fontSize: 12,
        color: "var(--t3)",
        ...style,
      }}
    >
      {children}
    </div>
  );
}

type EntityListRowProps = {
  active?: boolean;
  actions?: ReactNode;
  "aria-label"?: string;
  children: ReactNode;
  className?: string;
  disabled?: boolean;
  href?: string;
  onActivate?: () => void;
  style?: CSSProperties;
};

export function EntityListRow({
  active = false,
  actions,
  "aria-label": ariaLabel,
  children,
  className,
  disabled = false,
  href,
  onActivate,
  style,
}: EntityListRowProps) {
  const [hovered, setHovered] = useState(false);
  const [focusWithin, setFocusWithin] = useState(false);
  const actionsVisible = hovered || focusWithin;
  const primaryStyle: CSSProperties = {
    background: "transparent",
    border: 0,
    color: "inherit",
    display: "block",
    flex: 1,
    font: "inherit",
    minWidth: 0,
    padding: 0,
    textAlign: "left",
    textDecoration: "none",
  };
  const sharedPrimaryProps = {
    "aria-current": active ? ("page" as const) : undefined,
    "aria-disabled": disabled || undefined,
    "aria-label": ariaLabel,
    className: "entity-list-row__primary",
    style: primaryStyle,
  };

  const primary = href ? (
    <a
      {...sharedPrimaryProps}
      href={href}
      onClick={(event) => {
        if (disabled) {
          event.preventDefault();
          return;
        }
        if (!onActivate || !isPlainNavigationClick(event)) return;
        event.preventDefault();
        onActivate();
      }}
      onKeyDown={(event) => {
        if (disabled || event.target !== event.currentTarget || event.key !== " ") return;
        event.preventDefault();
        onActivate?.();
      }}
    >
      {children}
    </a>
  ) : onActivate ? (
    <button
      {...sharedPrimaryProps}
      type="button"
      onClick={() => {
        if (!disabled) onActivate();
      }}
    >
      {children}
    </button>
  ) : (
    <div className="entity-list-row__content" style={primaryStyle}>
      {children}
    </div>
  );

  return (
    <div
      className={classNames("entity-list-row", className)}
      data-active={active || undefined}
      onBlur={(event) => {
        const nextFocus = event.relatedTarget;
        if (!(nextFocus instanceof Node) || !event.currentTarget.contains(nextFocus)) {
          setFocusWithin(false);
        }
      }}
      onFocus={() => setFocusWithin(true)}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      style={{
        alignItems: "flex-start",
        background: active ? "var(--teal-bg)" : "transparent",
        borderBottom: "1px solid var(--border)",
        borderLeft: active ? "2px solid var(--teal)" : "2px solid transparent",
        cursor: disabled ? "wait" : onActivate || href ? "pointer" : "default",
        display: "flex",
        gap: 7,
        padding: "8px 12px",
        transition: "background 0.1s",
        ...style,
      }}
    >
      {primary}
      {actions && (
        <div
          className="entity-list-row__actions"
          style={{
            display: "flex",
            flexShrink: 0,
            gap: 1,
            opacity: actionsVisible ? 1 : 0,
            transition: "opacity 0.15s",
            visibility: actionsVisible ? "visible" : "hidden",
          }}
        >
          {actions}
        </div>
      )}
    </div>
  );
}

export function EntityDetailPane({ children, className, style }: StyledChildrenProps) {
  return (
    <div
      className={classNames("entity-detail-pane", className)}
      style={{
        flex: 1,
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        minWidth: 0,
        position: "relative",
        ...style,
      }}
    >
      {children}
    </div>
  );
}

export function EntityDetailHeader({
  "aria-label": ariaLabel,
  children,
  className,
  style,
}: StyledChildrenProps & { "aria-label"?: string }) {
  return (
    <header
      aria-label={ariaLabel}
      className={classNames("entity-detail-header", className)}
      style={{
        height: "var(--topbar-h)",
        borderBottom: "1px solid var(--border)",
        display: "flex",
        alignItems: "center",
        padding: "0 12px",
        gap: 8,
        flexShrink: 0,
        minWidth: 0,
        background: "var(--bg1)",
        ...style,
      }}
    >
      {children}
    </header>
  );
}
